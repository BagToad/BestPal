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

func TestAutoIntroCommentComponents(t *testing.T) {
	components := newAutoIntroComment("guild1", "feed1").components()
	require.Len(t, components, 1)
	preamble, ok := components[0].(discordgo.TextDisplay)
	require.True(t, ok)
	assert.Contains(t, preamble.Content, "https://discord.com/channels/guild1/feed1")
}
