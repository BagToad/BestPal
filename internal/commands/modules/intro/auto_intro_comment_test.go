package intro

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAutoIntroComment(t *testing.T) {
	got := newAutoIntroComment("guild123", "feed456")
	assert.Equal(t, "💥 Your intro is up on [the feed](https://discord.com/channels/guild123/feed456)\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed", got.preamble)
}

func TestAutoIntroCommentComponentsIncludesLookupButton(t *testing.T) {
	components := newAutoIntroComment("guild1", "feed1").components()

	require.GreaterOrEqual(t, len(components), 2)
	preamble, ok := components[0].(discordgo.TextDisplay)
	require.True(t, ok)
	assert.Contains(t, preamble.Content, "https://discord.com/channels/guild1/feed1")

	row, ok := components[1].(discordgo.ActionsRow)
	require.True(t, ok)
	require.Len(t, row.Components, 1)
	btn, ok := row.Components[0].(discordgo.Button)
	require.True(t, ok)
	assert.Equal(t, lookupButtonDefaultLabel, btn.Label)
	assert.False(t, btn.Disabled)
}

func TestAutoIntroCommentComponentsLoadingState(t *testing.T) {
	autoIntroComment := newAutoIntroComment("guild1", "feed1")
	autoIntroComment.aiLoadingState = true
	components := autoIntroComment.components()

	require.Len(t, components, 2)
	row, ok := components[1].(discordgo.ActionsRow)
	require.True(t, ok)
	require.Len(t, row.Components, 1)
	btn, ok := row.Components[0].(discordgo.Button)
	require.True(t, ok)
	assert.Equal(t, lookupButtonLoadingLabel, btn.Label)
	assert.True(t, btn.Disabled)
}

func TestAutoIntroCommentComponentsIncludesGameThreadsText(t *testing.T) {
	autoIntroComment := newAutoIntroComment("guild1", "feed1")
	autoIntroComment.gameThreads = []GameThread{
		{Name: "Destiny 2", URL: "https://discord.com/channels/guild/100"},
	}
	components := autoIntroComment.components()

	require.Len(t, components, 3)
	gameText, ok := components[2].(discordgo.TextDisplay)
	require.True(t, ok)
	assert.Contains(t, gameText.Content, "Destiny 2")
}

func TestFormatGameThreads(t *testing.T) {
	got := formatGameThreads([]GameThread{
		{Name: "Destiny 2", URL: "https://discord.com/channels/guild/100"},
		{Name: "Warframe"},
	})
	assert.Contains(t, got, gameThreadsHeader)
	assert.Contains(t, got, "- [Destiny 2](https://discord.com/channels/guild/100)")
	assert.NotContains(t, got, "Warframe")
	assert.Contains(t, got, "ℹ️ Missing a thread? Ask me to create new threads.")
}
