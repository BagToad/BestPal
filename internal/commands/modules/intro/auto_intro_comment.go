package intro

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const (
	lookupButtonDefaultLabel = "🎮 Find your game threads"
	lookupButtonLoadingLabel = "⏳ Finding your game threads..."

	gameThreadsHeader = "**Game Threads:**"
)

// AutoPost represents a rendered auto-comment on a user's introduction post
// use newAutoIntroComment to construct an instance of this struct
type AutoIntroComment struct {
	preamble string
	// LLM is thinking so button is disabled.
	aiLoadingState bool
	gameThreads    []GameThread
}

// newAutoIntroComment uses guildID and feedChannelID to create the preamble.
// The preamble is needed in every case where the AutoIntroComment is rendered, so we require it at construction time.
// This is a design choice because there is no other need for the guildID and feedChannelID in the AutoIntroComment struct.
func newAutoIntroComment(guildID, feedChannelID string) AutoIntroComment {
	return AutoIntroComment{
		preamble: fmt.Sprintf(
			"💥 Your intro is up on [the feed](https://discord.com/channels/%s/%s)\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed",
			guildID,
			feedChannelID,
		),
	}
}

// components returns the rendered components for the discord message.
func (m AutoIntroComment) components() []discordgo.MessageComponent {
	buttonLabel := lookupButtonDefaultLabel
	buttonDisabled := false
	if m.aiLoadingState {
		buttonLabel = lookupButtonLoadingLabel
		buttonDisabled = true
	}

	components := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: m.preamble},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    buttonLabel,
					Style:    discordgo.PrimaryButton,
					CustomID: LookupGameThreadsCustomID,
					Disabled: buttonDisabled,
				},
			},
		},
	}

	if len(m.gameThreads) > 0 {
		components = append(components, discordgo.TextDisplay{Content: formatGameThreads(m.gameThreads)})
	}

	return components
}

func formatGameThreads(threads []GameThread) string {
	var b strings.Builder
	b.WriteString(gameThreadsHeader)
	found := 0
	for _, thread := range threads {
		name := strings.TrimSpace(thread.Name)
		url := strings.TrimSpace(thread.URL)
		if name == "" || url == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("\n- [%s](%s)", name, url))
		found++
	}
	if found == 0 {
		b.WriteString("\n- No matching game threads found.")
	}
	b.WriteString("\n\nℹ️ Missing a thread? Ask me to create new threads.")
	return b.String()
}
