package intro

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// AutoIntroComment represents the helper message posted on a user's introduction.
type AutoIntroComment struct {
	preamble string
}

// newAutoIntroComment uses guildID and feedChannelID to create the preamble.
// The preamble is needed in every case where the AutoIntroComment is rendered, so we require it at construction time.
// This is a design choice because there is no other need for the guildID and feedChannelID in the AutoIntroComment struct.
func newAutoIntroComment(guildID, feedChannelID string) AutoIntroComment {
	return AutoIntroComment{preamble: fmt.Sprintf(
		"💥 Your intro is up on [the feed](https://discord.com/channels/%s/%s)\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed",
		guildID,
		feedChannelID,
	)}
}

// components returns the rendered components for the Discord message.
func (m AutoIntroComment) components() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.TextDisplay{Content: m.preamble}}
}
