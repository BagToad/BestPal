package trickortreat

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
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
	responses []*discordgo.InteractionResponse
	sent      []*discordgo.MessageSend
	sentTo    []string
	edits     []*discordgo.MessageEdit
	deleted   []string
	nextID    int
}

var testNow = time.Unix(1_790_000_000, 0)

func newTestModule(t *testing.T, kv map[string]any) (*Module, *capture, *database.DB) {
	t.Helper()
	db, err := database.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	if kv == nil {
		kv = map[string]any{}
	}
	kv["gamerpals_server_id"] = "guild1"

	c := &capture{nextID: 1000}
	m := &Module{
		config:    config.NewMockConfig(kv),
		store:     db,
		session:   &discordgo.Session{},
		now:       func() time.Time { return testNow },
		randN:     func(int) int { return 0 },
		randFloat: func() float64 { return 0 },
		locks:     map[string]*sync.Mutex{},
		ops: discordOps{
			Respond: func(_ *discordgo.Session, _ *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
				c.responses = append(c.responses, resp)
				return nil
			},
			SendMessage: func(_ *discordgo.Session, channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
				c.nextID++
				c.sent = append(c.sent, msg)
				c.sentTo = append(c.sentTo, channelID)
				return &discordgo.Message{ID: fmt.Sprint(c.nextID), ChannelID: channelID}, nil
			},
			EditMessage: func(_ *discordgo.Session, edit *discordgo.MessageEdit) error {
				c.edits = append(c.edits, edit)
				return nil
			},
			DeleteMessage: func(_ *discordgo.Session, _, messageID string) error {
				c.deleted = append(c.deleted, messageID)
				return nil
			},
		},
	}
	m.service = &Service{m: m}
	return m, c, db
}

func click(customID, messageID, userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionMessageComponent,
		GuildID:   "guild1",
		ChannelID: "chan1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: userID}},
		Message:   &discordgo.Message{ID: messageID},
		Data:      discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

func command(name, userID string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		GuildID:   "guild1",
		ChannelID: "chan1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: userID, Username: userID}},
		Data:      discordgo.ApplicationCommandInteractionData{Name: name, Options: opts},
	}}
}

func lastResponse(t *testing.T, c *capture) *discordgo.InteractionResponse {
	t.Helper()
	require.NotEmpty(t, c.responses)
	return c.responses[len(c.responses)-1]
}

func lastEdit(t *testing.T, c *capture) *discordgo.MessageEdit {
	t.Helper()
	require.NotEmpty(t, c.edits)
	return c.edits[len(c.edits)-1]
}

func button(t *testing.T, components []discordgo.MessageComponent) discordgo.Button {
	t.Helper()
	require.Len(t, components, 1)
	row, ok := components[0].(discordgo.ActionsRow)
	require.True(t, ok)
	require.Len(t, row.Components, 1)
	b, ok := row.Components[0].(discordgo.Button)
	require.True(t, ok)
	return b
}

func spawn(t *testing.T, m *Module) string {
	t.Helper()
	id, err := m.spawnBowl(m.session, "guild1", "chan1")
	require.NoError(t, err)
	return id
}

func emptyBowl(t *testing.T, m *Module, bowl string) {
	t.Helper()
	for n := range database.BowlSize {
		m.HandleComponent(nil, click(treatButtonID, bowl, fmt.Sprintf("filler%d", n)))
	}
}

func TestBowlImagesAreEmbedded(t *testing.T) {
	for n := 0; n <= database.BowlSize; n++ {
		f, err := bowlImage(n)
		require.NoErrorf(t, err, "bowl_%d.png", n)
		data, err := io.ReadAll(f.Reader)
		require.NoError(t, err)
		assert.Truef(t, strings.HasPrefix(string(data), "\x89PNG"), "bowl_%d.png is a PNG", n)
	}
}

func TestSpawn_PostsFullBowlWithTreatButton(t *testing.T) {
	m, c, db := newTestModule(t, nil)

	id := spawn(t, m)

	require.Len(t, c.sent, 1)
	msg := c.sent[0]
	require.Len(t, msg.Files, 1)
	assert.Equal(t, "bowl_10.png", msg.Files[0].Name)
	require.Len(t, msg.Embeds, 1)
	assert.Equal(t, "🎃 A Wild Candy Bowl Appeared!", msg.Embeds[0].Title)
	assert.Contains(t, msg.Embeds[0].Description, "Grab a treat before the bowl is empty!")
	assert.Equal(t, "attachment://bowl_10.png", msg.Embeds[0].Image.URL)
	b := button(t, msg.Components)
	assert.Equal(t, "Grab a Treat!", b.Label)
	assert.Equal(t, discordgo.SuccessButton, b.Style)
	assert.NotNil(t, msg.AllowedMentions, "silent drop")
	assert.Empty(t, msg.Content, "no @here or @everyone")

	bowl, _, ok, err := db.GetBowl(id)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "chan1", bowl.ChannelID)
	assert.Equal(t, 10, bowl.Remaining)
}

func TestTreat_ConfirmsPrivatelyAndSwapsImage(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	bowl := spawn(t, m)

	m.HandleComponent(nil, click(treatButtonID, bowl, "u1"))

	resp := lastResponse(t, c)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, resp.Data.Flags)
	assert.Equal(t, "🍬 You grabbed a treat! You now have **1 candy**.", resp.Data.Content)
	edit := lastEdit(t, c)
	require.Len(t, edit.Files, 1)
	assert.Equal(t, "bowl_9.png", edit.Files[0].Name)
	require.NotNil(t, edit.Attachments)
	assert.Empty(t, *edit.Attachments, "old image is replaced")
	embed := (*edit.Embeds)[0]
	assert.Equal(t, "attachment://bowl_9.png", embed.Image.URL)
	assert.Contains(t, embed.Description, fmt.Sprintf("• <t:%d:T> <@u1> grabbed a treat! (9 left)", testNow.Unix()))

	m.HandleComponent(nil, click(treatButtonID, bowl, "u1"))
	assert.Equal(t, "You have already claimed a treat from this bowl!", lastResponse(t, c).Data.Content)
}

func TestTreat_LastCandyTurnsButtonIntoTrick(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	bowl := spawn(t, m)

	emptyBowl(t, m, bowl)

	edit := lastEdit(t, c)
	assert.Equal(t, "bowl_0.png", edit.Files[0].Name)
	b := button(t, *edit.Components)
	assert.Equal(t, "TRICK!", b.Label)
	assert.Equal(t, discordgo.DangerButton, b.Style)
	desc := (*edit.Embeds)[0].Description
	assert.Contains(t, desc, "<@filler9> grabbed the last treat! (0 left)")
	assert.Contains(t, desc, fmt.Sprintf("Closes <t:%d:R>", testNow.Add(trickWindow).Unix()))

	m.HandleComponent(nil, click(treatButtonID, bowl, "late"))
	assert.Equal(t, "The bowl is empty! Try **TRICK!** instead.", lastResponse(t, c).Data.Content)
}

func TestTrick_RespondsPubliclyWithPings(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	bowl := spawn(t, m)
	m.HandleComponent(nil, click(trickButtonID, bowl, "sad"))
	assert.Equal(t, "There's still candy in this bowl!", lastResponse(t, c).Data.Content)

	emptyBowl(t, m, bowl)
	// "sad" tricks first (outcome 1) without ever getting candy from this
	// bowl; outcome 4 (index 3) then shares with them.
	m.HandleComponent(nil, click(trickButtonID, bowl, "sad"))
	m.randN = func(n int) int {
		if n == len(trickOutcomes) {
			return 3
		}
		return 0
	}

	m.HandleComponent(nil, click(trickButtonID, bowl, "filler0"))

	resp := lastResponse(t, c)
	assert.Equal(t, discordgo.InteractionResponseChannelMessageWithSource, resp.Type)
	assert.Zero(t, resp.Data.Flags, "public")
	assert.Equal(t, "🎃 **TRICK!** — <@filler0>\nYou spotted <@sad> standing nearby with an empty bag and kindly handed them 1 candy!\n\n⚡ **Outcome:** -1 Candy from <@filler0> • +1 Candy to <@sad>", resp.Data.Content)
	assert.Equal(t, []string{"filler0", "sad"}, resp.Data.AllowedMentions.Users)
	assert.Contains(t, (*lastEdit(t, c).Embeds)[0].Description, "<@filler0> triggered a TRICK: Shared a candy! 🍬")

	u, _, _, err := db.GetBucket("guild1", "sad")
	require.NoError(t, err)
	assert.Equal(t, int64(1), u.Candies)

	m.HandleComponent(nil, click(trickButtonID, bowl, "filler0"))
	assert.Equal(t, "You have already triggered a trick on this bowl!", lastResponse(t, c).Data.Content)
}

func TestTrick_EveryOutcomeRuns(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	require.Len(t, trickOutcomes, 20)
	for k, o := range trickOutcomes {
		require.Equal(t, k+1, o.ID)
	}

	bowl := spawn(t, m)
	emptyBowl(t, m, bowl)
	for k := range trickOutcomes {
		m.randN = func(n int) int {
			if n == len(trickOutcomes) {
				return k
			}
			return 0
		}
		before := len(c.responses)
		m.HandleComponent(nil, click(trickButtonID, bowl, fmt.Sprintf("trickster%d", k)))
		require.Len(t, c.responses, before+1)
		resp := lastResponse(t, c)
		assert.Zerof(t, resp.Data.Flags, "outcome %d is public", k+1)
		assert.Containsf(t, resp.Data.Content, "⚡ **Outcome:** ", "outcome %d", k+1)
		assert.NotContainsf(t, resp.Data.Content, "went wrong", "outcome %d", k+1)
	}
}

func TestTrick_OutcomeEffects(t *testing.T) {
	m, _, db := newTestModule(t, nil)
	bowl := spawn(t, m)
	emptyBowl(t, m, bowl)
	pick := func(id int) {
		m.randN = func(n int) int {
			if n == len(trickOutcomes) {
				return id - 1
			}
			return 0
		}
	}

	pick(16)
	m.HandleComponent(nil, click(trickButtonID, bowl, "filler1"))
	pick(1)
	m.HandleComponent(nil, click(trickButtonID, bowl, "filler2"))
	pick(14)
	m.HandleComponent(nil, click(trickButtonID, bowl, "filler3"))
	pick(10)
	m.HandleComponent(nil, click(trickButtonID, bowl, "filler4"))
	pick(6)
	m.HandleComponent(nil, click(trickButtonID, bowl, "filler5"))

	u, _, _, err := db.GetBucket("guild1", "filler1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), u.Candies, "pumpkin stash +2")
	u, _, _, err = db.GetBucket("guild1", "filler2")
	require.NoError(t, err)
	assert.Zero(t, u.Candies, "TP escape drops everything under 10")
	u, _, _, err = db.GetBucket("guild1", "filler3")
	require.NoError(t, err)
	assert.Equal(t, 3, u.BonusCandy)
	assert.Equal(t, bonusSourceFullSized, u.BonusSource)
	u, _, _, err = db.GetBucket("guild1", "filler4")
	require.NoError(t, err)
	assert.Equal(t, 2, u.TimeoutCharges)
	assert.Equal(t, "Barking Guard Dog", u.TimeoutSource)
	_, items, _, err := db.GetBucket("guild1", "filler5")
	require.NoError(t, err)
	assert.Equal(t, []database.Souvenir{{Type: souvenirEggshell, Quantity: 1}}, items)
}

func TestClosedBowl_ClickClearsButtons(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	bowl := spawn(t, m)
	require.NoError(t, db.ArchiveBowl(bowl))

	m.HandleComponent(nil, click(treatButtonID, bowl, "u1"))

	resp := lastResponse(t, c)
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, resp.Type)
	assert.Empty(t, resp.Data.Components)
	require.Len(t, resp.Data.Embeds, 1, "the embed is sent back so it isn't cleared")
	assert.Contains(t, resp.Data.Embeds[0].Description, "This bowl is all done.")
}

func TestExpireTick_ArchivesAfterTrickWindow(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	bowl := spawn(t, m)
	emptyBowl(t, m, bowl)

	require.NoError(t, m.expireTick())
	b, _, _, err := db.GetBowl(bowl)
	require.NoError(t, err)
	assert.True(t, b.Active, "window still open")

	edits := len(c.edits)
	m.now = func() time.Time { return testNow.Add(trickWindow) }
	require.NoError(t, m.expireTick())

	b, _, _, err = db.GetBowl(bowl)
	require.NoError(t, err)
	assert.False(t, b.Active)
	require.Len(t, c.edits, edits+1)
	edit := lastEdit(t, c)
	assert.Empty(t, *edit.Components, "buttons removed")
	assert.Nil(t, edit.Files, "image kept")
	assert.Contains(t, (*edit.Embeds)[0].Description, "<@filler9> grabbed the last treat!", "log is kept")

	m.HandleComponent(nil, click(trickButtonID, bowl, "u1"))
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, lastResponse(t, c).Type)
}

func TestSpawnTick(t *testing.T) {
	m, c, _ := newTestModule(t, map[string]any{
		config.KeyTrickOrTreatEnabled:  true,
		config.KeyTrickOrTreatChannels: []string{"a", "b", "c"},
	})
	now := testNow
	m.now = func() time.Time { return now }
	rolls := []float64{0.5}
	m.randFloat = func() float64 {
		v := rolls[0]
		rolls = rolls[1:]
		return v
	}

	require.NoError(t, m.spawnTick())
	assert.Empty(t, c.sent, "first tick only schedules the first roll")
	assert.Equal(t, testNow.Add(30*time.Minute), m.nextSpawnRoll)

	now = testNow.Add(29 * time.Minute)
	require.NoError(t, m.spawnTick())
	assert.Empty(t, c.sent)

	// Next roll in 1h * (0.5 + 0.5). Then per channel: chance, roll.
	now = testNow.Add(30 * time.Minute)
	rolls = []float64{0.5, 1, 0.14, 0, 0.99, 0, 0.09}
	require.NoError(t, m.spawnTick())
	assert.Equal(t, now.Add(time.Hour), m.nextSpawnRoll)
	assert.Equal(t, []string{"a", "c"}, c.sentTo, "chance is 10-15%: a rolls 0.14 < 0.15, b 0.99 misses, c 0.09 < 0.10")

	// Channels with an active bowl are skipped.
	now = m.nextSpawnRoll
	rolls = []float64{0.5, 0, 0}
	require.NoError(t, m.spawnTick())
	assert.Equal(t, []string{"a", "c", "b"}, c.sentTo)
}

func TestSpawnTick_Disabled(t *testing.T) {
	m, c, _ := newTestModule(t, map[string]any{config.KeyTrickOrTreatChannels: []string{"a"}})
	m.nextSpawnRoll = testNow
	require.NoError(t, m.spawnTick())
	assert.Empty(t, c.sent)
}

func TestSpawn_DeletesMessageWhenNotRecorded(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	require.NoError(t, db.Close())

	_, err := m.spawnBowl(nil, "guild1", "chan1")
	require.Error(t, err)
	assert.Equal(t, []string{"1001"}, c.deleted)
}

func TestBucket(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	bowl := spawn(t, m)
	emptyBowl(t, m, bowl)
	m.randN = func(n int) int {
		if n == len(trickOutcomes) {
			return 13 // Full-Sized Bar
		}
		return 0
	}
	m.HandleComponent(nil, click(trickButtonID, bowl, "filler3"))

	m.handleBucket(nil, command("bucket", "filler3"))

	resp := lastResponse(t, c)
	assert.Zero(t, resp.Data.Flags)
	require.Len(t, resp.Data.Embeds, 1)
	e := resp.Data.Embeds[0]
	assert.Equal(t, "🎃 filler3's Trick-or-Treat Bucket", e.Title)
	assert.Equal(t, "1 Candy  •  Rank #1 on /candy-leaderboard", e.Fields[0].Value)
	assert.Equal(t, "Empty pockets.", e.Fields[1].Value)
	assert.Equal(t, "• 🟢 +3 Candies on Next Bucket Claim (Full-Sized Bar House Buff)", e.Fields[2].Value)
}

func TestLeaderboard(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	m.handleLeaderboard(nil, command("candy-leaderboard", "u"))
	assert.Equal(t, "🍬 Nobody has any candy yet. Watch for a candy bowl!", lastResponse(t, c).Data.Content)

	bowl := spawn(t, m)
	m.HandleComponent(nil, click(treatButtonID, bowl, "u1"))
	m.handleLeaderboard(nil, command("candy-leaderboard", "u"))
	resp := lastResponse(t, c)
	assert.Equal(t, "🏆 **Candy Leaderboard**\n1. <@u1> — 1 candy", resp.Data.Content)
	assert.Empty(t, resp.Data.AllowedMentions.Users, "no pings")
}

func TestResetBowl(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	bowl := spawn(t, m)
	emptyBowl(t, m, bowl)
	require.NoError(t, db.ArchiveBowl(bowl))

	opt := &discordgo.ApplicationCommandInteractionDataOption{Name: "message_id", Type: discordgo.ApplicationCommandOptionString,
		Value: "https://discord.com/channels/guild1/chan1/" + bowl}
	m.handleReset(nil, command("reset-bowl", "admin", opt))

	assert.Equal(t, "🎃 Bowl refilled with 10 candies and reopened.", lastResponse(t, c).Data.Content)
	edit := lastEdit(t, c)
	assert.Equal(t, "bowl_10.png", edit.Files[0].Name)
	assert.Equal(t, "Grab a Treat!", button(t, *edit.Components).Label)

	opt.Value = "not an id"
	m.handleReset(nil, command("reset-bowl", "admin", opt))
	assert.Equal(t, "❌ That doesn't look like a message ID or link.", lastResponse(t, c).Data.Content)
	opt.Value = "123"
	m.handleReset(nil, command("reset-bowl", "admin", opt))
	assert.Equal(t, "❌ I don't know a candy bowl with that message ID.", lastResponse(t, c).Data.Content)
}

func TestDebugEmptyBowl(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	bowl := spawn(t, m)
	m.HandleComponent(nil, click(treatButtonID, bowl, "alice"))

	opt := &discordgo.ApplicationCommandInteractionDataOption{Name: "message_id", Type: discordgo.ApplicationCommandOptionString, Value: bowl}
	cmd := command("debug-empty-bowl", "mod", opt)
	cmd.Member.Permissions = discordgo.PermissionManageGuild
	m.handleDebugEmpty(nil, cmd)
	assert.Equal(t, "❌ You must be an Administrator to use debug commands.", lastResponse(t, c).Data.Content)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, lastResponse(t, c).Data.Flags)
	b, _, _, err := db.GetBowl(bowl)
	require.NoError(t, err)
	assert.Equal(t, 9, b.Remaining)

	cmd = command("debug-empty-bowl", "admin", opt)
	cmd.Member.Permissions = discordgo.PermissionAdministrator
	opt.Value = "https://discord.com/channels/guild1/chan1/" + bowl
	m.handleDebugEmpty(nil, cmd)
	assert.Equal(t, "🎃 Bowl emptied. TRICK! is live for the next 30 minutes.", lastResponse(t, c).Data.Content)
	edit := lastEdit(t, c)
	assert.Equal(t, "bowl_0.png", edit.Files[0].Name)
	assert.Equal(t, "TRICK!", button(t, *edit.Components).Label)
	b, _, _, err = db.GetBowl(bowl)
	require.NoError(t, err)
	assert.Equal(t, 0, b.Remaining)
	assert.Equal(t, testNow.Add(30*time.Minute).Unix(), b.ExpiresAt.Unix())
	_, log, _, err := db.GetBowl(bowl)
	require.NoError(t, err)
	require.Len(t, log, 10)
	assert.Equal(t, "<@admin> grabbed a treat! (8 left)", log[1].Text)
	assert.Equal(t, "<@admin> grabbed the last treat! (0 left)", log[9].Text)
	assert.Contains(t, (*edit.Embeds)[0].Description, "grabbed the last treat!")
	ann := c.sent[len(c.sent)-1]
	assert.Equal(t, "chan1", c.sentTo[len(c.sentTo)-1])
	assert.Equal(t, bowl, ann.Reference.MessageID)
	assert.Equal(t, fmt.Sprintf("🎃 <@admin> grabbed the last treat. The bowl is empty! **TRICK!** is live until <t:%[1]d:t> (<t:%[1]d:R>). [Go to the bowl](https://discord.com/channels/guild1/chan1/%[2]s)", b.ExpiresAt.Unix(), bowl), ann.Content)
	assert.Equal(t, discordgo.MessageFlagsSuppressEmbeds, ann.Flags)
	assert.Empty(t, ann.AllowedMentions.Parse)

	// Tricks work right away; alice can still trick after her treat.
	m.HandleComponent(nil, click(trickButtonID, bowl, "alice"))
	assert.Contains(t, lastResponse(t, c).Data.Content, "TRICK!")

	require.NoError(t, db.ArchiveBowl(bowl))
	m.handleDebugEmpty(nil, cmd)
	assert.Equal(t, "❌ Couldn't find an active candy bowl with that message ID.", lastResponse(t, c).Data.Content)
	opt.Value = "nope"
	m.handleDebugEmpty(nil, cmd)
	assert.Equal(t, "❌ That doesn't look like a message ID or link.", lastResponse(t, c).Data.Content)
}

func TestDebugEmptyBowl_LastTreat(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	bowl := spawn(t, m)
	cmd := command("debug-empty-bowl", "admin",
		&discordgo.ApplicationCommandInteractionDataOption{Name: "message_id", Type: discordgo.ApplicationCommandOptionString, Value: bowl},
		&discordgo.ApplicationCommandInteractionDataOption{Name: "mode", Type: discordgo.ApplicationCommandOptionString, Value: debugModeLastTreat})
	cmd.Member.Permissions = discordgo.PermissionAdministrator
	sentBefore := len(c.sent)
	m.handleDebugEmpty(nil, cmd)
	assert.Contains(t, lastResponse(t, c).Data.Content, "One treat left")
	assert.Len(t, c.sent, sentBefore, "no announcement until the bowl is actually empty")
	edit := lastEdit(t, c)
	assert.Equal(t, "bowl_1.png", edit.Files[0].Name)
	assert.Equal(t, "Grab a Treat!", button(t, *edit.Components).Label)
	b, log, _, err := db.GetBowl(bowl)
	require.NoError(t, err)
	assert.Equal(t, 1, b.Remaining)
	assert.True(t, b.ExpiresAt.IsZero())
	require.Len(t, log, 9)

	// The admin was never recorded as a participant, so they can grab the
	// last treat themselves and empty the bowl the real way.
	m.HandleComponent(nil, click(treatButtonID, bowl, "admin"))
	assert.Contains(t, lastResponse(t, c).Data.Content, "You grabbed a treat!")
	edit = lastEdit(t, c)
	assert.Equal(t, "bowl_0.png", edit.Files[0].Name)
	assert.Equal(t, "TRICK!", button(t, *edit.Components).Label)
	assert.Contains(t, (*edit.Embeds)[0].Description, "<@admin> grabbed the last treat! (0 left)")
	require.Len(t, c.sent, sentBefore+1)
	assert.Contains(t, c.sent[sentBefore].Content, "<@admin> grabbed the last treat. The bowl is empty! **TRICK!** is live")
	assert.Equal(t, bowl, c.sent[sentBefore].Reference.MessageID)

	m.handleDebugEmpty(nil, cmd)
	assert.Equal(t, "❌ That bowl is already empty. Use `/reset-bowl` to refill it first.", lastResponse(t, c).Data.Content)
}

func TestBowlEmbed_TrimsOldestLogLines(t *testing.T) {
	var log []database.BowlLogEntry
	for n := range 200 {
		log = append(log, database.BowlLogEntry{At: testNow, Text: fmt.Sprintf("<@user%03d> triggered a TRICK: something long enough", n)})
	}
	e := bowlEmbed(database.Bowl{Active: true}, log)
	assert.LessOrEqual(t, len(e.Description), maxDescription)
	assert.Contains(t, e.Description, "…\n")
	assert.Contains(t, e.Description, "<@user199>")
	assert.NotContains(t, e.Description, "<@user000>")
}

func TestHandleComponent_NoStore(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	m.store = nil
	m.HandleComponent(nil, click(treatButtonID, "x", "u"))
	assert.Equal(t, "❌ Database is unavailable.", lastResponse(t, c).Data.Content)
}

func TestCandyCommand(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	run := func(perms int64, sub, userID string, n float64) string {
		cmd := command("candy", "mod", &discordgo.ApplicationCommandInteractionDataOption{
			Name: sub, Type: discordgo.ApplicationCommandOptionSubCommand,
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: userID},
				{Name: "amount", Type: discordgo.ApplicationCommandOptionInteger, Value: n},
			},
		})
		cmd.Member.Permissions = perms
		m.handleCandy(nil, cmd)
		resp := lastResponse(t, c)
		assert.Equal(t, discordgo.MessageFlagsEphemeral, resp.Data.Flags)
		assert.Empty(t, resp.Data.AllowedMentions.Users, "no pings")
		return resp.Data.Content
	}
	balance := func() int64 {
		u, _, _, err := db.GetBucket("guild1", "bob")
		require.NoError(t, err)
		return u.Candies
	}
	const mod = discordgo.PermissionBanMembers

	assert.Equal(t, "❌ You must be a moderator to use this command.",
		run(discordgo.PermissionManageMessages, "add", "bob", 5))
	assert.Equal(t, int64(0), balance())

	assert.Equal(t, "✅ Added **5** candies to <@bob>'s bucket. They now have **5**.", run(mod, "add", "bob", 5))
	assert.Equal(t, "✅ Added **1** candy to <@bob>'s bucket. They now have **6**.",
		run(discordgo.PermissionAdministrator, "add", "bob", 1))
	assert.Equal(t, "✅ Removed **2** candies from <@bob>'s bucket. They now have **4**.", run(mod, "remove", "bob", 2))
	assert.Equal(t, "✅ Removed **4** candies from <@bob>'s bucket. They now have **0**.", run(mod, "remove", "bob", 100))
	assert.Equal(t, "<@bob>'s bucket is already empty, so there was nothing to remove.", run(mod, "remove", "bob", 1))
	assert.Equal(t, "❌ Pick a user and an amount of at least 1.", run(mod, "add", "bob", 0))
	assert.Equal(t, int64(0), balance())
}
