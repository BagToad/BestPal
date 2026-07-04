package intro

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	autoPostDefaultPreamble = "💥 Intro ready.\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed"

	lookupButtonDefaultLabel = "🎮 Find your game threads"
	lookupButtonLoadingLabel = "⏳ Finding your game threads..."

	gameThreadsHeader = "**Game Threads:**"
)

type PreambleState int

const (
	DefaultState PreambleState = iota
	FeedForwardedState
	CooldownSkipState
)

func preambleBuilder(state PreambleState, guildID, feedChannelID string, cooldownTimeRemaining time.Duration) string {
	switch state {
	case FeedForwardedState:
		if guildID != "" && feedChannelID != "" {
			return fmt.Sprintf(
				"💥 Your intro is up on [the feed](https://discord.com/channels/%s/%s)\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed",
				guildID,
				feedChannelID,
			)
		}
	case CooldownSkipState:
		if cooldownTimeRemaining > 0 {
			return fmt.Sprintf(
				"💥 Your intro is now up!\n⏳ Your intro wasn't auto-posted to the feed this time because you're still on cooldown (%s remaining).\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed when ready",
				formatDuration(cooldownTimeRemaining),
			)
		}
		return "💥 Your intro is now up!\n⏳ Your intro wasn't auto-posted to the feed this time because you're still on cooldown.\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed when ready"
	}
	return autoPostDefaultPreamble
}

type AutoPost struct {
	preamble     string
	loadingState bool
	// Raw rendered section from the third TextDisplay, kept as-is so retries/resets
	// don't need to parse and re-serialize game thread rows. see `gameThreadsBuilder` and `parseAutoPostFromComponents` for details.
	gameThreadsText string
}

func (m AutoPost) components() []discordgo.MessageComponent {
	preamble := strings.TrimSpace(m.preamble)
	if preamble == "" {
		preamble = preambleBuilder(DefaultState, "", "", 0)
	}

	buttonLabel := lookupButtonDefaultLabel
	buttonDisabled := false
	if m.loadingState {
		buttonLabel = lookupButtonLoadingLabel
		buttonDisabled = true
	}

	components := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: preamble},
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

	if gameThreadsText := strings.TrimSpace(m.gameThreadsText); gameThreadsText != "" {
		components = append(components, discordgo.TextDisplay{Content: gameThreadsText})
	}

	return components
}

func gameThreadsBuilder(threads []GameThreads) string {
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

func parseAutoPostFromComponents(components []discordgo.MessageComponent) (AutoPost, error) {
	inner, err := unwrapAutoPostComponents(components)
	if err != nil {
		return AutoPost{}, err
	}
	if len(inner) != 2 && len(inner) != 3 {
		return AutoPost{}, fmt.Errorf("unexpected component count: %d", len(inner))
	}

	// Preamble is always the first component, and must be a TextDisplay
	preamble, err := textDisplayContent(inner[0])
	if err != nil {
		return AutoPost{}, fmt.Errorf("invalid preamble component: %w", err)
	}

	autoMsg := AutoPost{
		preamble:     strings.TrimSpace(preamble),
		loadingState: false,
	}

	// Game threads text is only present for retries,
	// and is the third component if present, and must be a TextDisplay
	if len(inner) == 3 {
		gameThreadsText, err := textDisplayContent(inner[2])
		if err != nil {
			return AutoPost{}, fmt.Errorf("invalid game-threads component: %w", err)
		}
		gameThreadsText = strings.TrimSpace(gameThreadsText)
		if gameThreadsText != "" {
			autoMsg.gameThreadsText = gameThreadsText
		}
	}

	return autoMsg, nil
}

func unwrapAutoPostComponents(components []discordgo.MessageComponent) ([]discordgo.MessageComponent, error) {
	if len(components) == 1 {
		switch c := components[0].(type) {
		case discordgo.Container:
			return c.Components, nil
		case *discordgo.Container:
			if c == nil {
				return nil, errors.New("container is nil")
			}
			return c.Components, nil
		}
	}
	return components, nil
}

func textDisplayContent(component discordgo.MessageComponent) (string, error) {
	switch c := component.(type) {
	case discordgo.TextDisplay:
		return c.Content, nil
	case *discordgo.TextDisplay:
		if c == nil {
			return "", errors.New("text display is nil")
		}
		return c.Content, nil
	default:
		return "", fmt.Errorf("component is %T, want TextDisplay", component)
	}
}
