package score

import (
	"errors"
	"fmt"
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
func (failingStore) TakeAllScoreItem(string, string, string) (database.TakeResult, int64, error) {
	return 0, 0, errors.New("boom")
}
func (failingStore) SuggestScoreItemNames(string, string, string) ([]string, error) {
	return nil, errors.New("boom")
}
func (failingStore) GetScoreItems(string, string) ([]database.ScoreItem, error) {
	return nil, errors.New("boom")
}
func (failingStore) GetScoreLeaderboard(string, string, int) (string, []database.ScoreLeaderboardEntry, error) {
	return "", nil, errors.New("boom")
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

func allOpt() *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: "all", Type: discordgo.ApplicationCommandOptionBoolean, Value: true}
}

func TestTake_AllRemovesEverythingAndAnnouncesTheAmount(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(24)))

	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses"), allOpt()))

	require.Len(t, c.sent, 2)
	assert.Equal(t, "<@user1> has lost 24 horses!", c.sent[1].msg.Content)
	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Empty(t, items)

	c.edits = nil
	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses"), allOpt()))
	assert.Len(t, c.sent, 2)
	require.Len(t, c.edits, 1)
	assert.Contains(t, c.edits[0], "doesn't have any horses")
}

func TestTake_AllWithCountIsRejected(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(24)))
	c.responses = nil

	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(2), allOpt()))

	assert.Len(t, c.sent, 1)
	require.Len(t, c.responses, 1)
	assert.Contains(t, c.responses[0].Data.Content, "not both")
	items, err := m.store.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	assert.Equal(t, int64(24), items[0].Count)
}

func autocomplete(command string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	i := interaction(command, "mod1", opts...)
	i.Type = discordgo.InteractionApplicationCommandAutocomplete
	return i
}

func focusedThing(query string) *discordgo.ApplicationCommandInteractionDataOption {
	o := thingOpt(query)
	o.Focused = true
	return o
}

func choiceNames(t *testing.T, c *capture) []string {
	t.Helper()
	require.NotEmpty(t, c.responses)
	resp := c.responses[len(c.responses)-1]
	require.Equal(t, discordgo.InteractionApplicationCommandAutocompleteResult, resp.Type)
	names := []string{}
	for _, choice := range resp.Data.Choices {
		assert.Equal(t, choice.Name, choice.Value)
		names = append(names, choice.Name)
	}
	return names
}

func TestAutocomplete_SuggestsOnlyThingsStillHeld(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("Horses"), countOpt(2)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("horses"), countOpt(3)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("giraffe")))

	m.HandleAutocomplete(nil, autocomplete("give", focusedThing("")))
	assert.Equal(t, []string{"giraffe", "Horses"}, choiceNames(t, c), "one entry per thing, first spelling")

	m.HandleAutocomplete(nil, autocomplete("give", focusedThing("OR")))
	assert.Equal(t, []string{"Horses"}, choiceNames(t, c))

	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses"), allOpt()))
	m.handleTake(nil, interaction("take", "mod1", userOpt("user2"), thingOpt("horses"), allOpt()))
	m.HandleAutocomplete(nil, autocomplete("give", focusedThing("")))
	assert.Equal(t, []string{"giraffe"}, choiceNames(t, c), "gone once nobody has any")
}

func TestAutocomplete_TakeSuggestsOnlyThatUsersThings(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("giraffe")))

	m.HandleAutocomplete(nil, autocomplete("take", userOpt("user1"), focusedThing("")))
	assert.Equal(t, []string{"horses"}, choiceNames(t, c))

	m.HandleAutocomplete(nil, autocomplete("take", focusedThing("")))
	assert.Equal(t, []string{"giraffe", "horses"}, choiceNames(t, c), "server-wide until a user is picked")
}

func TestAutocomplete_GiveListsRecipientsThingsFirst(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("apples")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("zebras")))

	m.HandleAutocomplete(nil, autocomplete("give", userOpt("user1"), focusedThing("")))
	assert.Equal(t, []string{"zebras", "apples"}, choiceNames(t, c))
}

func TestAutocomplete_FailureStillRespondsWithEmptyChoices(t *testing.T) {
	m, c := newTestModule(t, failingStore{})

	m.HandleAutocomplete(nil, autocomplete("give", focusedThing("h")))

	assert.Empty(t, choiceNames(t, c))
}

func TestMergeSuggestions_DedupesAndCaps(t *testing.T) {
	var many []string
	for n := range 30 {
		many = append(many, strings.Repeat("x", n+1))
	}
	merged := mergeSuggestions([]string{"Horses"}, append([]string{"horses"}, many...))
	assert.Len(t, merged, 25)
	assert.Equal(t, []string{"Horses", "x"}, merged[:2])
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

func TestLeaderboard_RanksHoldersPubliclyWithoutPinging(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("Horses"), countOpt(5)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("horses"), countOpt(24)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user3"), thingOpt("horses"), countOpt(5)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user4"), thingOpt("horses"), countOpt(1)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user4"), thingOpt("giraffe"), countOpt(99)))
	c.responses = nil

	m.handleLeaderboard(nil, interaction("leaderboard", "someone", thingOpt("HORSES")))

	require.Len(t, c.responses, 1)
	resp := c.responses[0]
	assert.Equal(t, discordgo.InteractionResponseChannelMessageWithSource, resp.Type)
	assert.Zero(t, resp.Data.Flags, "leaderboard is posted publicly")
	assert.Equal(t, "Leaderboard for Horses:\n\n1. <@user2> — 24\n2. <@user1> — 5\n2. <@user3> — 5\n4. <@user4> — 1", resp.Data.Content)
	require.NotNil(t, resp.Data.AllowedMentions)
	assert.Empty(t, resp.Data.AllowedMentions.Users)
	assert.Empty(t, resp.Data.AllowedMentions.Parse)
}

func TestLeaderboard_ShowsTopTen(t *testing.T) {
	m, c := newTestModule(t, nil)
	for n := 1; n <= 12; n++ {
		m.handleGive(nil, interaction("give", "mod1", userOpt(fmt.Sprintf("user%02d", n)), thingOpt("horses"), countOpt(float64(n))))
	}
	c.responses = nil

	m.handleLeaderboard(nil, interaction("leaderboard", "someone", thingOpt("horses")))

	content := c.responses[0].Data.Content
	assert.Equal(t, leaderboardSize, strings.Count(content, "<@"))
	assert.Contains(t, content, "1. <@user12> — 12")
	assert.NotContains(t, content, "<@user02>")
}

func TestLeaderboard_NobodyHasIt(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses")))
	m.handleTake(nil, interaction("take", "mod1", userOpt("user1"), thingOpt("horses"), allOpt()))
	c.responses = nil

	m.handleLeaderboard(nil, interaction("leaderboard", "someone", thingOpt("horses")))

	require.Len(t, c.responses, 1)
	assert.Equal(t, "Nobody has any horses.", c.responses[0].Data.Content)
}

func TestLeaderboard_LoadFailureIsEphemeral(t *testing.T) {
	m, c := newTestModule(t, failingStore{})

	m.handleLeaderboard(nil, interaction("leaderboard", "someone", thingOpt("horses")))

	require.Len(t, c.responses, 1)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, c.responses[0].Data.Flags)
}

func TestAutocomplete_LeaderboardSuggestsServerWide(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("giraffe")))

	m.HandleAutocomplete(nil, autocomplete("leaderboard", focusedThing("")))

	assert.Equal(t, []string{"giraffe", "horses"}, choiceNames(t, c))
}
