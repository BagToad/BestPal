package events

import (
	"fmt"
	"gamerpal/internal/config"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// OnGuildScheduledEventCreate forwards new scheduled events to the configured event feed channel.
func OnGuildScheduledEventCreate(s *discordgo.Session, e *discordgo.GuildScheduledEventCreate, cfg *config.Config) {
	feedChannelID := cfg.GetEventFeedChannelID()
	if feedChannelID == "" {
		return
	}

	eventURL := fmt.Sprintf("https://discord.com/events/%s/%s", e.GuildID, e.ID)

	msg := heredoc.Docf(`
		New event created! [Link](<%s>)

		Title: %s
	`, eventURL, e.Name)

	if _, err := s.ChannelMessageSend(feedChannelID, msg); err != nil {
		cfg.Logger.Errorf("Failed to send event feed message: %v", err)
	}
}
