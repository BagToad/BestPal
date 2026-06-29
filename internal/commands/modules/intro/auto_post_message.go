package intro

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type AutoMessage struct {
	guildId       string
	feedChannelId string
	FindingState  bool
	GameThreads   []GameThreads
}

func (s AutoMessage) buttonLabel() string {
	if s.FindingState {
		return "Finding your game threads..."
	}
	return "Find your game threads"
}

func (s AutoMessage) introText() string {
	if s.guildId == "" || s.feedChannelId == "" {
		return "💥 your intro is up on the feed\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed"
	}
	return fmt.Sprintf(
		"💥 your intro is up on [the feed](https://discord.com/channels/%s/%s)\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed",
		s.guildId,
		s.feedChannelId,
	)
}

func (s AutoMessage) renderGameThreadsText() string {
	if len(s.GameThreads) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Game threads:")
	for _, thread := range s.GameThreads {
		name := strings.TrimSpace(thread.Name)
		if name == "" {
			continue
		}
		url := strings.TrimSpace(thread.URL)
		status := strings.TrimSpace(strings.ToLower(thread.Status))
		if url == "" || status == "not found" {
			b.WriteString(fmt.Sprintf("\n- %s (_not found_)", name))
			continue
		}
		b.WriteString(fmt.Sprintf("\n- [%s](%s)", name, url))
	}

	return b.String()
}

func (s AutoMessage) Components() []discordgo.MessageComponent {
	components := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: s.introText()},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    s.buttonLabel(),
					Disabled: s.FindingState,
					Style:    discordgo.PrimaryButton,
					CustomID: LookupGameThreadsCustomID,
				},
			},
		},
	}

	if renderedThreads := s.renderGameThreadsText(); renderedThreads != "" {
		components = append(components, discordgo.TextDisplay{Content: renderedThreads})
	}

	return []discordgo.MessageComponent{
		discordgo.Container{
			Components: components,
		},
	}
}
