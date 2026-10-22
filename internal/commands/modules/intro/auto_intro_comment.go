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

// components returns the rendered components for the Discord message:
// a preamble TextDisplay at index 0, an ActionsRow at index 1 with the lookup
// Button first, and a game-thread TextDisplay at index 2 only when
// len(m.gameThreads) > 0.
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

// resolveLookupButton expects a *discordgo.ActionsRow at components[1]
// containing a *discordgo.Button at its Components[0].
func resolveLookupButton(components []discordgo.MessageComponent) *discordgo.Button {
	return components[1].(*discordgo.ActionsRow).Components[0].(*discordgo.Button)
}

// setLookupButtonLoading updates only the resolved button's loading state.
func setLookupButtonLoading(button *discordgo.Button, loading bool) {
	button.Disabled = loading
	button.Label = lookupButtonDefaultLabel
	if loading {
		button.Label = lookupButtonLoadingLabel
	}
}

// updateGameThreads expects a non-nil message with two or three components
// and a *discordgo.TextDisplay at message.Components[2] when present.
func updateGameThreads(message *discordgo.Message, threads []GameThread) {
	content := formatGameThreads(threads)
	switch len(message.Components) {
	case 2:
		message.Components = append(message.Components, &discordgo.TextDisplay{Content: content})
	case 3:
		message.Components[2].(*discordgo.TextDisplay).Content = content
	}
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
