package slidepuzzle

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capture struct {
	responses     []*discordgo.InteractionResponse
	responseEdits []*discordgo.WebhookEdit
	sent          []*discordgo.MessageSend
	edits         []*discordgo.MessageEdit
	downloads     map[string][]byte
	nextID        int
	sendErr       error
	editErr       error
	deleted       []string
}

var testNow = time.Unix(1_790_000_000, 0)

const modPerms = discordgo.PermissionBanMembers

func newTestModule(t *testing.T, kv map[string]any) (*Module, *capture, *database.DB, *time.Time) {
	t.Helper()
	db, err := database.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	if kv == nil {
		kv = map[string]any{}
	}
	kv["gamerpals_server_id"] = "guild1"
	now := testNow
	c := &capture{nextID: 1000, downloads: map[string][]byte{}}
	m := &Module{
		config:  config.NewMockConfig(kv),
		store:   db,
		session: &discordgo.Session{},
		now:     func() time.Time { return now },
		randN:   func(int) int { return 0 },
		locks:   map[string]*sync.Mutex{},
		ops: discordOps{
			Respond: func(_ *discordgo.Session, _ *discordgo.Interaction, r *discordgo.InteractionResponse) error {
				c.responses = append(c.responses, r)
				return nil
			},
			EditResponse: func(_ *discordgo.Session, _ *discordgo.Interaction, e *discordgo.WebhookEdit) error {
				c.responseEdits = append(c.responseEdits, e)
				return nil
			},
			SendMessage: func(_ *discordgo.Session, channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
				if c.sendErr != nil {
					return nil, c.sendErr
				}
				c.nextID++
				c.sent = append(c.sent, msg)
				return &discordgo.Message{ID: fmt.Sprint(c.nextID), ChannelID: channelID}, nil
			},
			EditMessage: func(_ *discordgo.Session, e *discordgo.MessageEdit) error {
				if c.editErr != nil {
					return c.editErr
				}
				c.edits = append(c.edits, e)
				return nil
			},
			DeleteMessage: func(_ *discordgo.Session, _, messageID string) error {
				c.deleted = append(c.deleted, messageID)
				return nil
			},
			Download: func(url string, _ int64) ([]byte, error) {
				if d, ok := c.downloads[url]; ok {
					return d, nil
				}
				return nil, fmt.Errorf("404")
			},
		},
	}
	m.service = &Service{m: m}
	return m, c, db, &now
}

func interaction(typ discordgo.InteractionType, data discordgo.InteractionData, userID string, perms int64) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      typ,
		GuildID:   "guild1",
		ChannelID: "chan1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: userID}, Permissions: perms},
		Data:      data,
	}}
}

func click(customID, messageID, userID string, perms int64, values ...string) *discordgo.InteractionCreate {
	i := interaction(discordgo.InteractionMessageComponent, discordgo.MessageComponentInteractionData{CustomID: customID, Values: values}, userID, perms)
	i.Message = &discordgo.Message{ID: messageID}
	return i
}

func lastContent(t *testing.T, c *capture) string {
	t.Helper()
	require.NotEmpty(t, c.responses)
	return c.responses[len(c.responses)-1].Data.Content
}

func lastEditContent(t *testing.T, c *capture) string {
	t.Helper()
	require.NotEmpty(t, c.responseEdits)
	return *c.responseEdits[len(c.responseEdits)-1].Content
}

func setup(t *testing.T, m *Module, c *capture) database.SlidePuzzle {
	t.Helper()
	m.handleSetup(m.session, interaction(discordgo.InteractionApplicationCommand,
		discordgo.ApplicationCommandInteractionData{Name: "slidepuzzle-setup"}, "mod", modPerms))
	require.Contains(t, lastEditContent(t, c), "Started a")
	p, ok, err := m.store.GetSlidePuzzle("guild1")
	require.NoError(t, err)
	require.True(t, ok)
	return p
}

func TestSetupPostsShuffledBoard(t *testing.T) {
	m, c, _, _ := newTestModule(t, map[string]any{config.KeySlidePuzzleGridSize: 3})
	p := setup(t, m, c)

	require.Len(t, c.sent, 1)
	msg := c.sent[0]
	require.Len(t, msg.Files, 1)
	assert.Equal(t, boardFile, msg.Files[0].Name)
	assert.Equal(t, "attachment://"+boardFile, msg.Embeds[0].Image.URL)
	assert.Contains(t, msg.Embeds[0].Description, fmt.Sprintf("<t:%d:R>", testNow.Add(8*time.Hour).Unix()))
	assert.Equal(t, "0", msg.Embeds[0].Fields[0].Value)

	assert.Equal(t, 3, p.Grid)
	assert.Equal(t, "chan1", p.ChannelID)
	assert.Equal(t, "1001", p.MessageID)
	assert.False(t, isSolved(p.Board))
	assert.Equal(t, testNow.Add(8*time.Hour).Unix(), p.RoundEnds.Unix(), "default round length is 8h")
}

func TestSetupRequiresMod(t *testing.T) {
	m, c, _, _ := newTestModule(t, nil)
	m.handleSetup(m.session, interaction(discordgo.InteractionApplicationCommand,
		discordgo.ApplicationCommandInteractionData{Name: "slidepuzzle-setup"}, "pleb", 0))
	assert.Contains(t, lastContent(t, c), "Ban Members")
	assert.Empty(t, c.sent)
}

func TestVoteOncePerRoundAndCounters(t *testing.T) {
	m, c, _, _ := newTestModule(t, nil)
	p := setup(t, m, c)

	m.HandleComponent(m.session, click(voteButtonID, p.MessageID, "u1", 0))
	resp := c.responses[len(c.responses)-1]
	row := resp.Data.Components[0].(discordgo.ActionsRow)
	menu := row.Components[0].(discordgo.SelectMenu)
	assert.Equal(t, fmt.Sprintf("%s%d", pickPrefix, p.Round), menu.CustomID)
	movable := movableTiles(p.Board, p.Grid)
	require.Len(t, menu.Options, len(movable))
	tile := movable[0]

	m.HandleComponent(m.session, click(menu.CustomID, "eph", "u1", 0, fmt.Sprint(tile)))
	assert.Equal(t, discordgo.InteractionResponseDeferredMessageUpdate, c.responses[len(c.responses)-1].Type, "vote is deferred before taking the lock")
	assert.Contains(t, lastEditContent(t, c), "Vote locked in")
	edit := c.edits[len(c.edits)-1]
	assert.Equal(t, "1", (*edit.Embeds)[0].Fields[0].Value, "total votes shown on the board")
	require.Len(t, edit.Files, 1, "a vote redraws the image with the tile counts")

	// Second vote from the same member is refused, both from the menu and the button.
	m.HandleComponent(m.session, click(menu.CustomID, "eph", "u1", 0, fmt.Sprint(movable[1])))
	assert.Contains(t, lastEditContent(t, c), "already voted for tile")
	m.HandleComponent(m.session, click(voteButtonID, p.MessageID, "u1", 0))
	assert.Contains(t, lastContent(t, c), "already voted for tile")

	got, _, _ := m.store.GetSlidePuzzle("guild1")
	assert.EqualValues(t, 1, got.TotalVotes)

	// A tile that is not next to the gap is rejected.
	for _, t2 := range p.Board {
		if t2 != 0 && !contains(movable, t2) {
			m.HandleComponent(m.session, click(menu.CustomID, "eph", "u2", 0, fmt.Sprint(t2)))
			assert.Contains(t, lastEditContent(t, c), "can't move")
			break
		}
	}
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestRoundTickMakesTopMoveAndResetsVotes(t *testing.T) {
	m, c, _, now := newTestModule(t, map[string]any{config.KeySlidePuzzleRoundDuration: "1h"})
	p := setup(t, m, c)
	movable := movableTiles(p.Board, p.Grid)
	pick := fmt.Sprintf("%s%d", pickPrefix, p.Round)
	m.HandleComponent(m.session, click(pick, "eph", "u1", 0, fmt.Sprint(movable[1])))
	m.HandleComponent(m.session, click(pick, "eph", "u2", 0, fmt.Sprint(movable[1])))
	m.HandleComponent(m.session, click(pick, "eph", "u3", 0, fmt.Sprint(movable[0])))

	// Not due yet: nothing happens.
	require.NoError(t, m.roundTick())
	got, _, _ := m.store.GetSlidePuzzle("guild1")
	assert.Equal(t, p.Round, got.Round)

	*now = now.Add(time.Hour)
	editsBefore := len(c.edits)
	require.NoError(t, m.roundTick())
	got, _, _ = m.store.GetSlidePuzzle("guild1")
	want, _ := applyMove(p.Board, p.Grid, movable[1])
	assert.Equal(t, want, got.Board)
	assert.Equal(t, p.Round+1, got.Round)
	assert.EqualValues(t, 1, got.TotalMoves)
	assert.EqualValues(t, 3, got.TotalVotes, "total votes keep counting across moves")
	assert.Equal(t, now.Add(time.Hour).Unix(), got.RoundEnds.Unix())

	require.Len(t, c.edits, editsBefore+1)
	edit := c.edits[len(c.edits)-1]
	require.Len(t, edit.Files, 1, "a move redraws the image")
	require.NotNil(t, edit.Attachments)
	assert.Empty(t, *edit.Attachments, "old image attachment is replaced")
	assert.Equal(t, "1", (*edit.Embeds)[0].Fields[1].Value)

	// Votes reset: u1 can vote again in the new round; the old menu is stale.
	m.HandleComponent(m.session, click(pick, "eph", "u1", 0, fmt.Sprint(movable[0])))
	assert.Contains(t, lastEditContent(t, c), "round is over")
	tally, _ := m.store.SlidePuzzleTally("guild1", got.Round)
	assert.Empty(t, tally)
}

func TestRoundWithoutVotesIsExtended(t *testing.T) {
	m, c, _, now := newTestModule(t, nil)
	p := setup(t, m, c)
	*now = now.Add(8 * time.Hour)
	require.NoError(t, m.roundTick())
	got, _, _ := m.store.GetSlidePuzzle("guild1")
	assert.Equal(t, p.Board, got.Board)
	assert.Equal(t, p.Round, got.Round)
	assert.Equal(t, now.Add(8*time.Hour).Unix(), got.RoundEnds.Unix())
	assert.Nil(t, c.edits[len(c.edits)-1].Files)
}

func TestSolvingEndsThePuzzle(t *testing.T) {
	m, c, db, now := newTestModule(t, map[string]any{config.KeySlidePuzzleGridSize: 2})
	setup(t, m, c)
	// Force a board one move from solved.
	board, _ := applyMove(solvedBoard(2), 2, 3)
	p, err := db.StartSlidePuzzle(database.SlidePuzzle{
		GuildID: "guild1", ChannelID: "chan1", MessageID: "1001", Grid: 2, Board: board, RoundEnds: now.Add(time.Hour),
	}, *now)
	require.NoError(t, err)
	m.HandleComponent(m.session, click(fmt.Sprintf("%s%d", pickPrefix, p.Round), "eph", "u1", 0, "3"))

	*now = now.Add(time.Hour)
	require.NoError(t, m.roundTick())
	got, _, _ := db.GetSlidePuzzle("guild1")
	assert.True(t, got.Solved)
	edit := c.edits[len(c.edits)-1]
	assert.Contains(t, (*edit.Embeds)[0].Description, "Solved!")
	assert.Empty(t, *edit.Components, "vote button is removed once solved")

	// No further moves, and voting is closed until a mod resets.
	*now = now.Add(24 * time.Hour)
	n := len(c.edits)
	require.NoError(t, m.roundTick())
	assert.Len(t, c.edits, n)
	m.HandleComponent(m.session, click(voteButtonID, "1001", "u2", 0))
	assert.Contains(t, lastContent(t, c), "already solved")
}

func TestResetFlow(t *testing.T) {
	m, c, _, _ := newTestModule(t, nil)
	m.HandleComponent(m.session, click(resetButtonID, "panel", "mod", modPerms))
	assert.Contains(t, lastContent(t, c), "/slidepuzzle-setup")

	p := setup(t, m, c)
	m.HandleComponent(m.session, click(resetButtonID, "panel", "pleb", 0))
	assert.Contains(t, lastContent(t, c), "Ban Members")

	m.HandleComponent(m.session, click(resetButtonID, "panel", "mod", modPerms))
	assert.Contains(t, lastContent(t, c), "Reset the puzzle in <#chan1>?")
	m.config = config.NewMockConfig(map[string]any{"gamerpals_server_id": "guild1", config.KeySlidePuzzleGridSize: 4})
	m.HandleComponent(m.session, click(resetConfirmID, "panel", "mod", modPerms))
	assert.Contains(t, lastEditContent(t, c), "new 4x4 puzzle")

	got, _, _ := m.store.GetSlidePuzzle("guild1")
	assert.Equal(t, 4, got.Grid, "grid size applies on reset")
	assert.Equal(t, p.Round+1, got.Round)
	assert.NotEqual(t, p.MessageID, got.MessageID)
	assert.Len(t, c.sent, 2)
	retired := c.edits[len(c.edits)-1]
	assert.Equal(t, p.MessageID, retired.ID)
	assert.Contains(t, *retired.Content, "reset")
	assert.Empty(t, *retired.Components)

	// Old board's vote button no longer works.
	m.HandleComponent(m.session, click(voteButtonID, p.MessageID, "u1", 0))
	assert.Contains(t, lastContent(t, c), "no longer active")
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))))
	return buf.Bytes()
}

func uploadSubmit(userID string, perms int64, att *discordgo.MessageAttachment) *discordgo.InteractionCreate {
	data := discordgo.ModalSubmitInteractionData{CustomID: uploadModalID}
	if att != nil {
		data.Resolved.Attachments = map[string]*discordgo.MessageAttachment{att.ID: att}
	}
	return interaction(discordgo.InteractionModalSubmit, data, userID, perms)
}

func TestUploadFlow(t *testing.T) {
	m, c, _, _ := newTestModule(t, nil)

	m.HandleComponent(m.session, click(uploadButtonID, "panel", "pleb", 0))
	assert.Contains(t, lastContent(t, c), "Ban Members")

	m.HandleComponent(m.session, click(uploadButtonID, "panel", "mod", modPerms))
	modal := c.responses[len(c.responses)-1]
	require.Equal(t, discordgo.InteractionResponseModal, modal.Type)
	label := modal.Data.Components[0].(discordgo.Label)
	upload := label.Component.(discordgo.FileUpload)
	assert.Equal(t, uploadFileID, upload.CustomID)
	assert.Equal(t, 1, upload.MaxValues)

	c.downloads["https://cdn/x.png"] = pngBytes(t, 300, 200)
	m.HandleModalSubmit(m.session, uploadSubmit("mod", modPerms, &discordgo.MessageAttachment{
		ID: "a1", Filename: "cool_cat.png", ContentType: "image/png", Size: 100, URL: "https://cdn/x.png",
	}))
	assert.Contains(t, lastEditContent(t, c), "Added **cool\\_cat.png**")
	assert.Contains(t, lastEditContent(t, c), "2 images")
	ids, err := m.store.SlidePuzzleImageIDs("guild1")
	require.NoError(t, err)
	require.Len(t, ids, 1)

	// The uploaded image can be picked on reset.
	m.randN = func(n int) int { return n - 1 }
	id, err := m.pickImage("guild1")
	require.NoError(t, err)
	assert.Equal(t, ids[0], id)
	m.randN = func(int) int { return 0 }
	setup(t, m, c)

	// Rejections: non-image, oversized, undecodable, non-mod.
	m.HandleModalSubmit(m.session, uploadSubmit("mod", modPerms, &discordgo.MessageAttachment{ID: "a2", ContentType: "text/plain", URL: "u"}))
	assert.Contains(t, lastContent(t, c), "isn't an image")
	m.HandleModalSubmit(m.session, uploadSubmit("mod", modPerms, &discordgo.MessageAttachment{ID: "a3", ContentType: "image/png", Size: maxUploadBytes + 1, URL: "u"}))
	assert.Contains(t, lastContent(t, c), "over 10 MB")
	c.downloads["https://cdn/bad"] = []byte("nope")
	m.HandleModalSubmit(m.session, uploadSubmit("mod", modPerms, &discordgo.MessageAttachment{ID: "a4", ContentType: "image/png", Size: 4, URL: "https://cdn/bad"}))
	assert.Contains(t, lastEditContent(t, c), "not a supported image")
	m.HandleModalSubmit(m.session, uploadSubmit("pleb", 0, &discordgo.MessageAttachment{ID: "a5", ContentType: "image/png", URL: "https://cdn/x.png"}))
	assert.Contains(t, lastContent(t, c), "Ban Members")
	ids, _ = m.store.SlidePuzzleImageIDs("guild1")
	assert.Len(t, ids, 1)
}

func TestConfigValidation(t *testing.T) {
	assert.NoError(t, validateGrid("5"))
	assert.Error(t, validateGrid("1"))
	assert.Error(t, validateGrid("9"))
	assert.Error(t, validateGrid("five"))
	assert.NoError(t, validateRound("8h"))
	assert.NoError(t, validateRound("1m"))
	assert.Error(t, validateRound("30s"))
	assert.Error(t, validateRound("soon"))

	gc := config.NewMockConfig(map[string]any{config.KeySlidePuzzleGridSize: 42}).ForGuild("g")
	assert.Equal(t, 5, gc.GetSlidePuzzleGridSize(), "out-of-range grid falls back to 5")
}

func TestStateSurvivesRestart(t *testing.T) {
	m, c, db, now := newTestModule(t, nil)
	p := setup(t, m, c)
	movable := movableTiles(p.Board, p.Grid)
	m.HandleComponent(m.session, click(fmt.Sprintf("%s%d", pickPrefix, p.Round), "eph", "u1", 0, fmt.Sprint(movable[0])))

	// A fresh module over the same DB (as after a restart) finishes the round.
	m2 := &Module{config: m.config, store: db, ops: m.ops, session: m.session, now: m.now, randN: m.randN, locks: map[string]*sync.Mutex{}}
	*now = now.Add(8 * time.Hour)
	require.NoError(t, m2.roundTick())
	got, _, _ := db.GetSlidePuzzle("guild1")
	want, _ := applyMove(p.Board, p.Grid, movable[0])
	assert.Equal(t, want, got.Board)
}

func TestFailedPostKeepsCurrentPuzzle(t *testing.T) {
	m, c, _, _ := newTestModule(t, nil)
	p := setup(t, m, c)
	movable := movableTiles(p.Board, p.Grid)
	m.HandleComponent(m.session, click(fmt.Sprintf("%s%d", pickPrefix, p.Round), "eph", "u1", 0, fmt.Sprint(movable[0])))

	c.sendErr = fmt.Errorf("missing access")
	edits := len(c.edits)
	m.handleSetup(m.session, interaction(discordgo.InteractionApplicationCommand,
		discordgo.ApplicationCommandInteractionData{Name: "slidepuzzle-setup"}, "mod", modPerms))
	assert.Contains(t, lastEditContent(t, c), "Could not start")

	got, _, _ := m.store.GetSlidePuzzle("guild1")
	assert.Equal(t, p.MessageID, got.MessageID, "old puzzle stays active")
	assert.Equal(t, p.Round, got.Round)
	assert.EqualValues(t, 1, got.TotalVotes)
	tally, _ := m.store.SlidePuzzleTally("guild1", p.Round)
	assert.Equal(t, 1, tally[movable[0]], "votes are kept")
	assert.Len(t, c.edits, edits, "old board is not retired")
}

func TestFailedRedrawIsRetried(t *testing.T) {
	m, c, db, now := newTestModule(t, nil)
	p := setup(t, m, c)
	movable := movableTiles(p.Board, p.Grid)
	m.HandleComponent(m.session, click(fmt.Sprintf("%s%d", pickPrefix, p.Round), "eph", "u1", 0, fmt.Sprint(movable[0])))

	*now = now.Add(8 * time.Hour)
	c.editErr = fmt.Errorf("discord is down")
	assert.Error(t, m.roundTick())
	got, _, _ := db.GetSlidePuzzle("guild1")
	assert.True(t, got.NeedsRedraw)
	assert.EqualValues(t, 1, got.TotalMoves)

	// Next tick (well before the next round ends) publishes the pending board
	// without making another move.
	c.editErr = nil
	*now = now.Add(time.Minute)
	require.NoError(t, m.roundTick())
	got, _, _ = db.GetSlidePuzzle("guild1")
	assert.False(t, got.NeedsRedraw)
	assert.EqualValues(t, 1, got.TotalMoves)
	require.Len(t, c.edits[len(c.edits)-1].Files, 1)
}

func TestSolvedRedrawIsRetried(t *testing.T) {
	m, c, db, now := newTestModule(t, map[string]any{config.KeySlidePuzzleGridSize: 2})
	setup(t, m, c)
	board, _ := applyMove(solvedBoard(2), 2, 3)
	p, err := db.StartSlidePuzzle(database.SlidePuzzle{
		GuildID: "guild1", ChannelID: "chan1", MessageID: "1001", Grid: 2, Board: board, RoundEnds: now.Add(time.Hour),
	}, *now)
	require.NoError(t, err)
	m.HandleComponent(m.session, click(fmt.Sprintf("%s%d", pickPrefix, p.Round), "eph", "u1", 0, "3"))

	*now = now.Add(time.Hour)
	c.editErr = fmt.Errorf("discord is down")
	assert.Error(t, m.roundTick())
	c.editErr = nil
	require.NoError(t, m.roundTick())
	got, _, _ := db.GetSlidePuzzle("guild1")
	assert.True(t, got.Solved)
	assert.False(t, got.NeedsRedraw)
	assert.Contains(t, (*c.edits[len(c.edits)-1].Embeds)[0].Description, "Solved!")
}

func restErr(code int) error {
	return &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: code, Message: "gone"}}
}

func TestDeletedBoardIsReposted(t *testing.T) {
	m, c, db, now := newTestModule(t, nil)
	p := setup(t, m, c)
	movable := movableTiles(p.Board, p.Grid)
	m.HandleComponent(m.session, click(fmt.Sprintf("%s%d", pickPrefix, p.Round), "eph", "u1", 0, fmt.Sprint(movable[0])))

	*now = now.Add(8 * time.Hour)
	c.editErr = restErr(discordgo.ErrCodeUnknownMessage)
	require.NoError(t, m.roundTick())
	got, _, _ := db.GetSlidePuzzle("guild1")
	require.Len(t, c.sent, 2)
	require.Len(t, c.sent[1].Files, 1)
	assert.NotEqual(t, p.MessageID, got.MessageID)
	assert.Equal(t, "chan1", got.ChannelID)
	assert.False(t, got.NeedsRedraw)
	assert.EqualValues(t, 1, got.TotalMoves)

	// Voting continues on the reposted board.
	c.editErr = nil
	m.HandleComponent(m.session, click(voteButtonID, got.MessageID, "u2", 0))
	assert.Contains(t, lastContent(t, c), "Which tile")
}

func TestDeletedChannelPausesPuzzle(t *testing.T) {
	m, c, db, now := newTestModule(t, nil)
	setup(t, m, c)

	*now = now.Add(8 * time.Hour)
	c.editErr = restErr(discordgo.ErrCodeUnknownChannel)
	require.NoError(t, m.roundTick())
	got, ok, _ := db.GetSlidePuzzle("guild1")
	require.True(t, ok)
	assert.Empty(t, got.MessageID)
	due, err := db.DueSlidePuzzles(now.Add(24 * time.Hour))
	require.NoError(t, err)
	assert.Empty(t, due)

	// Setup brings it back.
	c.editErr = nil
	p := setup(t, m, c)
	assert.NotEmpty(t, p.MessageID)
}

func TestVoteAfterDeadlineRejected(t *testing.T) {
	m, c, _, now := newTestModule(t, nil)
	p := setup(t, m, c)
	pick := fmt.Sprintf("%s%d", pickPrefix, p.Round)
	movable := movableTiles(p.Board, p.Grid)

	*now = p.RoundEnds
	m.HandleComponent(m.session, click(pick, "eph", "u1", 0, fmt.Sprint(movable[0])))
	assert.Contains(t, lastEditContent(t, c), "has closed")
	m.HandleComponent(m.session, click(voteButtonID, p.MessageID, "u2", 0))
	assert.Contains(t, lastContent(t, c), "has closed")
	tally, _ := m.store.SlidePuzzleTally("guild1", p.Round)
	assert.Empty(t, tally)
}

func TestLongThinUploadRejected(t *testing.T) {
	_, err := normalizeUpload(pngBytes(t, 64, 4000))
	assert.ErrorContains(t, err, "at least 64px", "checked after downscaling (64x4000 would be stored at 20x1280)")
	_, err = normalizeUpload(pngBytes(t, 400, 2000))
	assert.ErrorContains(t, err, "longer than it is wide")
	_, err = normalizeUpload(pngBytes(t, 300, 1200))
	assert.NoError(t, err, "4:1 is allowed")
}

func TestConcurrentRenders(t *testing.T) {
	src, err := decodeImage(defaultImage)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for g := 2; g <= 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := renderBoard(src, g, shuffledBoard(g, func(n int) int { return 0 }), false, map[int]int{1: 123})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}

func TestFailedExtendRefreshIsRetried(t *testing.T) {
	m, c, db, now := newTestModule(t, nil)
	p := setup(t, m, c)

	*now = now.Add(8 * time.Hour)
	c.editErr = fmt.Errorf("discord is down")
	assert.Error(t, m.roundTick())
	got, _, _ := db.GetSlidePuzzle("guild1")
	assert.True(t, got.NeedsRefresh)
	assert.Equal(t, p.Round, got.Round)

	c.editErr = nil
	*now = now.Add(time.Minute)
	require.NoError(t, m.roundTick())
	got, _, _ = db.GetSlidePuzzle("guild1")
	assert.False(t, got.NeedsRefresh)
	edit := c.edits[len(c.edits)-1]
	assert.Nil(t, edit.Files, "a timer refresh doesn't re-upload the image")
	assert.Contains(t, (*edit.Embeds)[0].Description, fmt.Sprintf("<t:%d:R>", got.RoundEnds.Unix()))

	// Nothing pending: the next tick does nothing.
	n := len(c.edits)
	*now = now.Add(time.Minute)
	require.NoError(t, m.roundTick())
	assert.Len(t, c.edits, n)
}
