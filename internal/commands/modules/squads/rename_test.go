package squads

import (
	"strings"
	"testing"

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
