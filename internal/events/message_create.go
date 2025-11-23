package events

import (
	"fmt"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// OnMessageCreate handles message events
func OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate, cfg *config.Config) {
	// Ignore messages from bots (including ourselves)
	if m.Author.Bot {
		return
	}

	channel, err := s.Channel(m.ChannelID)
	if err == nil {
		if channel.Type == discordgo.ChannelTypeDM {
			helpDeskID := cfg.GetGamerPalsHelpDeskChannelID()
			_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("**DMs are not monitored. For help please see <#%s> in the GamerPals Discord**", helpDeskID))
		}
	}

	// Check if the bot is mentioned in the message & react
	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			emojiResponse, err := getModelsEmojiResponse(s, m, cfg)
			if err != nil {
				cfg.Logger.Errorf("Error getting emoji response: %v", err)
				return
			}

			err = s.MessageReactionAdd(m.ChannelID, m.ID, emojiResponse)
			if err != nil {
				cfg.Logger.Errorf("Error adding heart reaction: %v", err)
			}
			return
		}
	}
}

func getModelsEmojiResponse(s *discordgo.Session, m *discordgo.MessageCreate, cfg *config.Config) (string, error) {
	modelsClient := utils.NewModelsClient(cfg)
	emojis, err := s.GuildEmojis(cfg.GetGamerPalsServerID())
	if err != nil {
		return "", fmt.Errorf("error fetching guild emojis: %w", err)
	}

	var emojiNames strings.Builder
	for _, emoji := range emojis {
		emojiNames.WriteString(fmt.Sprintf("%s:%s", emoji.Name, emoji.ID))
		emojiNames.WriteString(", ")
	}

	systemPrompt := heredoc.Docf(`
		You are a frog-themed discord bot for the GamerPals community.

		You respond to messages by selecting a single INTERESTING emoji to respond to a user message. Select emojis that are fun and contextually fitting with the message, and where possible emojis that fit with the frog-theme of the discord. Your emoji selection can be sort of sassy when required, but you must be supportive, kind, and interesting in general.

		A valid emoji is one that discord natively supports or one from the following
		list of available emojis: %v

		If you choose a custom emoji from the list, you must respond with the format name:ID
		as you have been provided above.

		If you are given a message that is inappropriate, regarding sensitive topics like mental health, politics, religion, or any other controversial subjects, you must respond with $DONTKNOW$

		NEVER respond with these emojis: eggplant, peach, middle finger. Always respond with a different emoji.
	`, emojiNames.String())

	userPrompt := m.Content

	emojiResponse := modelsClient.ModelsRequest(systemPrompt, userPrompt, "gpt-4.1-nano")

	if emojiResponse == "$DONTKNOW$" || emojiResponse == "" {
		return "", nil
	}

	return emojiResponse, nil
}
