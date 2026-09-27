package score

import (
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capture struct {
	responses []*discordgo.InteractionResponse
	edits     []string
	sent      []sentMessage
	sendErr   error
}

type sentMessage struct {
	channelID string
	msg       *discordgo.MessageSend
}

type failingStore struct{}

func (failingStore) GiveScoreItem(string, string, string, int64) (database.GiveResult, error) {
	return 0, errors.New("boom")
}
func (failingStore) TakeScoreItem(string, string, string, int64) (database.TakeResult, int64, error) {
	return 0, 0, errors.New("boom")
}
func (failingStore) GetScoreItems(string, string) ([]database.ScoreItem, error) {
	return nil, errors.New("boom")
}

func newTestModule(t *testing.T, st store) (*Module, *capture) {
	t.Helper()
	if st == nil {
		db, err := database.NewDB(filepath.Join(t.TempDir(), "test.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		st = db
	}
	c := &capture{}
	return &Module{
		config: config.NewMockConfig(nil),
		store:  st,
		ops: discordOps{
			Respond: func(_ *discordgo.Session, _ *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
				c.responses = append(c.responses, resp)
				return nil
			},
			EditResponse: func(_ *discordgo.Session, _ *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
				c.edits = append(c.edits, *edit.Content)
				return nil
			},
			SendMessage: func(_ *discordgo.Session, channelID string, msg *discordgo.MessageSend) error {
				if c.sendErr != nil {
					return c.sendErr
				}
				c.sent = append(c.sent, sentMessage{channelID: channelID, msg: msg})
				return nil
			},
		},
	}, c
}

const canPost = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages

func interaction(command, invokerID string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:           discordgo.InteractionApplicationCommand,
			GuildID:        "guild1",
			ChannelID:      "chan1",
			AppPermissions: canPost,
			Member:         &discordgo.Member{User: &discordgo.User{ID: invokerID}},
			Data:           discordgo.ApplicationCommandInteractionData{Name: command, Options: opts},
		},
	}
}

func userOpt(id string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: id}
}

func thingOpt(thing string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "thing", Type: discordgo.ApplicationCommandOptionString, Value: thing}
}

func countOpt(n float64) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "count", Type: discordgo.ApplicationCommandOptionInteger, Value: n}
}

func TestGive_AnnouncesInChannelAndAcksModeratorPrivately(t *testing.T) {
	m, c := newTestModule(t, nil)

	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(24)))

	require.Len(t, c.sent, 1)
	assert.Equal(t, "chan1", c.sent[0].channelID)
	assert.Equal(t, "<@user1> has earned 24 horses!", c.sent[0].msg.Content)
	assert.Equal(t, []string{"user1"}, c.sent[0].msg.AllowedMentions.Users)
	assert.Empty(t, c.sent[0].msg.AllowedMentions.Parse)
	assert.NotContains(t, c.sent[0].msg.Content, "mod1")

	require.Len(t, c.responses, 1)
	assert.Equal(t, discordgo.InteractionResponseDeferredChannelMessageWithSource, c.responses[0].Type)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, c.responses[0].Data.Flags)
	assert.Equal(t, []string{"✅ Done."}, c.edits)

	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(24), items[0].Count)
}

func TestGive_CountDefaultsToOne(t *testing.T) {
	m, c := newTestModule(t, nil)

	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("giraffe")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("giraffe")))

	require.Len(t, c.sent, 2)
	assert.Equal(t, "<@user1> has earned 1 giraffe!", c.sent[0].msg.Content)
	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(2), items[0].Count)
}

func TestGive_HugeCountsUntilTheTotalWouldOverflow(t *testing.T) {
	m, c := newTestModule(t, nil)
	const discordMax = 1<<53 - 1

	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(discordMax)))
	require.Len(t, c.sent, 1)
	assert.Equal(t, "<@user1> has earned 9007199254740991 horses!", c.sent[0].msg.Content)

	_, err := m.store.GiveScoreItem("guild1", "user1", "horses", math.MaxInt64-2*discordMax)
	require.NoError(t, err)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(discordMax)))
	require.Len(t, c.sent, 2, "a give that fits exactly is announced")

	c.edits = nil
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses")))
	assert.Len(t, c.sent, 2, "overflowing give is not announced")
	require.Len(t, c.edits, 1)
	assert.Contains(t, c.edits[0], "too many")

	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Equal(t, int64(math.MaxInt64), items[0].Count)
}

func TestTake_AnnouncesLossAndRemovesAtZero(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(5)))
	c.responses, c.edits = nil, nil

	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("Horses"), countOpt(3)))

	require.Len(t, c.sent, 2)
	assert.Equal(t, "chan1", c.sent[1].channelID)
	assert.Equal(t, "<@user1> has lost 3 Horses!", c.sent[1].msg.Content)
	assert.Equal(t, []string{"user1"}, c.sent[1].msg.AllowedMentions.Users)
	assert.NotContains(t, c.sent[1].msg.Content, "mod1")
	require.Len(t, c.responses, 1)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, c.responses[0].Data.Flags)
	assert.Equal(t, []string{"✅ Done."}, c.edits)

	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(2), items[0].Count)

	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses")))
	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses")))
	assert.Equal(t, "<@user1> has lost 1 horses!", c.sent[3].msg.Content)
	items, err = m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestTake_MoreThanHeldOrNotHeldIsNotAnnounced(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2)))
	c.edits = nil

	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(3)))
	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("giraffe")))

	assert.Len(t, c.sent, 1, "only the give was announced")
	require.Len(t, c.edits, 2)
	assert.Contains(t, c.edits[0], "only has 2 horses")
	assert.Contains(t, c.edits[1], "doesn't have any giraffe")

	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), items[0].Count)
}

func TestTake_CannotPostInChannelChangesNothing(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2)))
	i := interaction("take", "mod1", userOpt("user1"), thingOpt("horses"))
	i.AppPermissions = discordgo.PermissionViewChannel

	m.handleTake(nil, i)

	assert.Len(t, c.sent, 1)
	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), items[0].Count)
}

func TestTake_SaveFailureDoesNotAnnounce(t *testing.T) {
	m, c := newTestModule(t, failingStore{})

	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses")))

	assert.Empty(t, c.sent)
	require.Len(t, c.edits, 1)
	assert.Contains(t, c.edits[0], "Failed to save")
}

func TestGive_RejectsBlankThingWithoutAnnouncing(t *testing.T) {
	m, c := newTestModule(t, nil)

	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("   ")))

	assert.Empty(t, c.sent)
	require.Len(t, c.responses, 1)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, c.responses[0].Data.Flags)
}

func TestGive_SaveFailureDoesNotAnnounce(t *testing.T) {
	m, c := newTestModule(t, failingStore{})

	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2)))

	assert.Empty(t, c.sent)
	require.Len(t, c.edits, 1)
	assert.Contains(t, c.edits[0], "Failed to save")
}

func TestGive_CannotPostInChannelSavesNothing(t *testing.T) {
	m, c := newTestModule(t, nil)
	i := interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2))
	i.AppPermissions = discordgo.PermissionViewChannel

	m.handleGive(nil, i)

	assert.Empty(t, c.sent)
	require.Len(t, c.responses, 1)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, c.responses[0].Data.Flags)
	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestGive_InThreadNeedsThreadSendPermission(t *testing.T) {
	m, c := newTestModule(t, nil)
	s := &discordgo.Session{State: discordgo.NewState()}
	require.NoError(t, s.State.GuildAdd(&discordgo.Guild{ID: "guild1"}))
	require.NoError(t, s.State.ChannelAdd(&discordgo.Channel{ID: "chan1", GuildID: "guild1", Type: discordgo.ChannelTypeGuildPublicThread}))

	m.handleGive(s, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2)))
	assert.Empty(t, c.sent, "SendMessages alone is not enough in a thread")

	i := interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2))
	i.AppPermissions = discordgo.PermissionViewChannel | discordgo.PermissionSendMessagesInThreads
	m.handleGive(s, i)
	assert.Len(t, c.sent, 1)
}

func TestGive_CountedGiveStacksAndAnnouncesTheAmountGiven(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(20)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("Horses"), countOpt(4)))

	require.Len(t, c.sent, 2)
	assert.Equal(t, "<@user1> has earned 4 Horses!", c.sent[1].msg.Content)
	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(24), items[0].Count)
}

func TestGive_AnnounceFailureTellsModeratorItWasSaved(t *testing.T) {
	m, c := newTestModule(t, nil)
	c.sendErr = errors.New("missing access")

	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2)))

	require.Len(t, c.edits, 1)
	assert.Contains(t, c.edits[0], "Saved")
	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestGive_NoDatabase(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.store = nil

	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses")))

	assert.Empty(t, c.sent)
	require.Len(t, c.responses, 1)
	assert.Contains(t, c.responses[0].Data.Content, "Database is unavailable")
}

func TestScore_ListsThingsForRequestedUserWithoutPinging(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(24)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("giraffe")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("cans of eggs"), countOpt(3)))
	c.responses = nil

	m.handleScore(nil, interaction("score", "someone", userOpt("user1")))

	require.Len(t, c.responses, 1)
	resp := c.responses[0]
	assert.Equal(t, discordgo.InteractionResponseChannelMessageWithSource, resp.Type)
	assert.Zero(t, resp.Data.Flags, "score is posted publicly")
	assert.Equal(t, "<@user1> has:\n\n- 24 horses\n- 1 giraffe\n- 3 cans of eggs", resp.Data.Content)
	require.NotNil(t, resp.Data.AllowedMentions)
	assert.Empty(t, resp.Data.AllowedMentions.Users)
	assert.Empty(t, resp.Data.AllowedMentions.Parse)
}

func TestScore_DefaultsToInvokerAndReportsNothing(t *testing.T) {
	m, c := newTestModule(t, nil)

	m.handleScore(nil, interaction("score", "user9"))

	require.Len(t, c.responses, 1)
	assert.Equal(t, "<@user9> has nothing.", c.responses[0].Data.Content)
}

func TestScore_LoadFailureIsEphemeral(t *testing.T) {
	m, c := newTestModule(t, failingStore{})

	m.handleScore(nil, interaction("score", "user9"))

	require.Len(t, c.responses, 1)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, c.responses[0].Data.Flags)
}

func TestBuildScoreMessage_TruncatesToDiscordLimit(t *testing.T) {
	items := make([]database.ScoreItem, 100)
	for idx := range items {
		items[idx] = database.ScoreItem{Name: strings.Repeat("x", maxThingLength), Count: math.MaxInt64}
	}

	msg := buildScoreMessage("user1", items)

	assert.LessOrEqual(t, utf8.RuneCountInString(msg), maxMessageLength)
	assert.True(t, strings.HasPrefix(msg, "<@user1> has:\n\n- 9223372036854775807 "))
	assert.Regexp(t, `\n- …and \d+ more$`, msg)
}

func TestBuildScoreMessage_CountsCharactersNotBytes(t *testing.T) {
	header := len("<@user1> has:\n")
	// Multi-byte characters: well over 2000 bytes but exactly 2000 characters.
	name := strings.Repeat("🦶", maxMessageLength-header-len("\n- 1 "))
	msg := buildScoreMessage("user1", []database.ScoreItem{{Name: name, Count: 1}})

	assert.Equal(t, maxMessageLength, utf8.RuneCountInString(msg))
	assert.NotContains(t, msg, "more")
}

func TestBuildScoreMessage_ExactFitHasNoOverflowNote(t *testing.T) {
	header := len("<@user1> has:\n")
	// One item whose line lands exactly on the limit.
	name := strings.Repeat("y", maxMessageLength-header-len("\n- 1 "))
	msg := buildScoreMessage("user1", []database.ScoreItem{{Name: name, Count: 1}})

	assert.Len(t, msg, maxMessageLength)
	assert.NotContains(t, msg, "more")
}
