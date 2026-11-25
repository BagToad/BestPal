package squads

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renameCmd(uid, name string) *discordgo.InteractionCreate {
	return command(uid, 0, "squad", "rename", &discordgo.ApplicationCommandInteractionDataOption{
		Name: "name", Type: discordgo.ApplicationCommandOptionString, Value: name})
}

func TestAnyMemberCanRenameTheirSquadThread(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.sent = nil

	m.handleSquad(nil, renameCmd("b", "  The   Night Owls "))
	assert.Contains(t, editContent(api.lastEdit()), "Renamed your squad to **The Night Owls**")
	assert.Equal(t, "The Night Owls", api.renamed[sq.ChannelID])
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, "The Night Owls", got.Name)
	msgs := sentTo(api, sq.ChannelID)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "<@b> renamed the squad to **The Night Owls**")
	assert.Empty(t, msgs[0].AllowedMentions.Users)

	m.handleSquad(nil, renameCmd("b", "The Night Owls"))
	assert.Contains(t, editContent(api.lastEdit()), "already called")

	m.handleSquad(nil, renameCmd("b", strings.Repeat("x", 150)))
	assert.Len(t, api.renamed[sq.ChannelID], maxSquadName)
}

func TestRenameNeedsAThreadSquad(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b")

	m.handleSquad(nil, renameCmd("a", "New name"))
	assert.Contains(t, editContent(api.lastEdit()), "isn't available for squads that have their own channel")
	assert.Contains(t, editContent(api.lastEdit()), "Newer squads live in private threads")
	assert.Empty(t, api.renamed)
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, sq.Name, got.Name)

	m.handleSquad(nil, renameCmd("zed", "New name"))
	assert.Contains(t, editContent(api.lastEdit()), "not in a squad")
}

func TestRenameRateLimited(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.renameErr = &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{TooManyRequests: &discordgo.TooManyRequests{}}}

	m.handleSquad(nil, renameCmd("a", "New name"))
	assert.Contains(t, editContent(api.lastEdit()), "Try again in a bit")
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, sq.Name, got.Name)
}

func TestRenameLimitedToTwoPerTenMinutes(t *testing.T) {
	m, api, db, now := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")

	m.handleSquad(nil, renameCmd("a", "One"))
	m.handleSquad(nil, renameCmd("b", "Two"))
	m.handleSquad(nil, renameCmd("a", "Three"))
	assert.Contains(t, editContent(api.lastEdit()), "Try again in a bit")
	assert.Equal(t, "Two", api.renamed[sq.ChannelID], "the third rename never reaches Discord")

	*now = now.Add(renameWindow)
	m.handleSquad(nil, renameCmd("a", "Three"))
	assert.Equal(t, "Three", api.renamed[sq.ChannelID])
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, "Three", got.Name)
}

func TestRenameRefusedWhileAnotherIsInFlight(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	busy, _ := m.reserveRename(sq.ID)
	require.False(t, busy)

	m.handleSquad(nil, renameCmd("b", "Mine"))
	assert.Contains(t, editContent(api.lastEdit()), "Someone else is renaming")
	assert.Empty(t, api.renamed)
}

func TestSquadChoiceLabelFitsDiscordLimit(t *testing.T) {
	label := choiceLabel(strings.Repeat("x", maxSquadName), " (4 members)")
	assert.Len(t, []rune(label), 100)
	assert.True(t, strings.HasSuffix(label, "x… (4 members)"))
	assert.Equal(t, "Owls (2 members)", choiceLabel("Owls", " (2 members)"))
}

func TestDiscordRateLimitBlocksRenamesUntilItResets(t *testing.T) {
	m, api, db, now := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.renameErr = &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{TooManyRequests: &discordgo.TooManyRequests{}}}
	m.handleSquad(nil, renameCmd("a", "One"))
	api.renameErr = nil

	m.handleSquad(nil, renameCmd("a", "Two"))
	assert.Contains(t, editContent(api.lastEdit()), "Try again in a bit")
	assert.Empty(t, api.renamed, "no request while Discord's limit is used up")

	*now = now.Add(renameWindow)
	m.handleSquad(nil, renameCmd("a", "Two"))
	assert.Equal(t, "Two", api.renamed[sq.ChannelID])
}

func TestRenameReopensAnArchivedThread(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.renameArchived = true
	m.handleSquad(nil, renameCmd("a", "Moon Base"))
	assert.Equal(t, []string{sq.ChannelID}, api.reopened)
	assert.Equal(t, "Moon Base", api.renamed[sq.ChannelID])
}

func TestRenameDoesNotReopenAClosingSquad(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.renameArchived = true
	api.onRename = func() { require.NoError(t, db.MarkSquadDeleting(sq.ID, "mod")) }
	m.handleSquad(nil, renameCmd("a", "Moon Base"))
	assert.Empty(t, api.reopened)
	assert.Empty(t, api.renamed)
}

func TestDiscordRateLimitOnlyBlocksRenamesUntilDiscordSays(t *testing.T) {
	m, api, db, now := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.renameErr = &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{TooManyRequests: &discordgo.TooManyRequests{RetryAfter: time.Minute}}}
	m.handleSquad(nil, renameCmd("a", "One"))
	api.renameErr = nil

	*now = now.Add(30 * time.Second)
	m.handleSquad(nil, renameCmd("a", "Two"))
	assert.Empty(t, api.renamed)

	*now = now.Add(31 * time.Second)
	m.handleSquad(nil, renameCmd("a", "Two"))
	assert.Equal(t, "Two", api.renamed[sq.ChannelID])
}

func TestRenameOfClosedSquadIsNotTracked(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.onRename = func() { require.NoError(t, db.MarkSquadDeleting(sq.ID, "mod")) }
	m.handleSquad(nil, renameCmd("a", "Moon Base"))
	m.mu.Lock()
	defer m.mu.Unlock()
	assert.NotContains(t, m.renames, sq.ID)
}
