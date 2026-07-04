package intro

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoPostComponentsIncludesLookupButton(t *testing.T) {
	components := (AutoPost{
		preamble: preambleBuilder(FeedForwardedState, "guild1", "feed1", 0),
	}).components()

	require.GreaterOrEqual(t, len(components), 2)
	preamble, err := textDisplayContent(components[0])
	require.NoError(t, err)
	assert.Contains(t, preamble, "https://discord.com/channels/guild1/feed1")

	row, ok := components[1].(discordgo.ActionsRow)
	require.True(t, ok)
	require.Len(t, row.Components, 1)
	btn, ok := row.Components[0].(discordgo.Button)
	require.True(t, ok)
	assert.Equal(t, lookupButtonDefaultLabel, btn.Label)
	assert.False(t, btn.Disabled)
}

func TestAutoPostComponentsLoadingState(t *testing.T) {
	components := (AutoPost{
		preamble:     preambleBuilder(DefaultState, "", "", 0),
		loadingState: true,
	}).components()

	require.Len(t, components, 2)
	row, ok := components[1].(discordgo.ActionsRow)
	require.True(t, ok)
	require.Len(t, row.Components, 1)
	btn, ok := row.Components[0].(discordgo.Button)
	require.True(t, ok)
	assert.Equal(t, lookupButtonLoadingLabel, btn.Label)
	assert.True(t, btn.Disabled)
}

func TestParseAutoPostFromComponentsRoundTrip(t *testing.T) {
	original := AutoPost{
		preamble: preambleBuilder(CooldownSkipState, "", "", 0),
		gameThreadsText: gameThreadsBuilder([]GameThreads{
			{Name: "Destiny 2", URL: "https://discord.com/channels/guild/100"},
			{Name: "Warframe"},
		}),
	}

	parsed, err := parseAutoPostFromComponents(original.components())
	require.NoError(t, err)
	assert.Equal(t, original.preamble, parsed.preamble)
	assert.Equal(t, original.gameThreadsText, parsed.gameThreadsText)
	assert.False(t, parsed.loadingState)
}

func TestParseAutoPostFromComponentsWithContainer(t *testing.T) {
	inner := (AutoPost{
		preamble: preambleBuilder(DefaultState, "", "", 0),
	}).components()
	wrapped := []discordgo.MessageComponent{discordgo.Container{Components: inner}}

	parsed, err := parseAutoPostFromComponents(wrapped)
	require.NoError(t, err)
	assert.Equal(t, autoPostDefaultPreamble, parsed.preamble)
}

func TestParseAutoPostFromComponentsRejectsInvalidShapes(t *testing.T) {
	_, err := parseAutoPostFromComponents([]discordgo.MessageComponent{
		discordgo.ActionsRow{},
	})
	require.Error(t, err)
}

func TestGameThreadsBuilder(t *testing.T) {
	got := gameThreadsBuilder([]GameThreads{
		{Name: "Destiny 2", URL: "https://discord.com/channels/guild/100"},
		{Name: "Warframe"},
	})
	assert.Contains(t, got, gameThreadsHeader)
	assert.Contains(t, got, "- [Destiny 2](https://discord.com/channels/guild/100)")
	assert.NotContains(t, got, "Warframe")
	assert.Contains(t, got, "ℹ️ Missing a thread? Ask me to create new threads.")
}
