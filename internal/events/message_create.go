package events

import (
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// OnMessageCreate handles message events
func OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate, cfg *config.Config) {
	// Ignore messages from bots (including ourselves)
	if m.Author.Bot {
		return
	}

	// Check if the bot is mentioned in the message & react
	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			err := s.MessageReactionAdd(m.ChannelID, m.ID, "❤️")
			if err != nil {
				cfg.Logger.Errorf("Error adding heart reaction: %v", err)
			}
			return
		}
	}
}
