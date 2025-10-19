package poll

import (
	"fmt"
	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"
)

type PollModule struct{}

func New(deps *types.Dependencies) *PollModule {
	return &PollModule{}
}

func (m *PollModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var min_options float64 = 2
	var max_options float64 = 10

	cmds["quick-poll"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "quick-poll",
			Description: "Create a quick poll with numbered options",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "num_options",
					Description: fmt.Sprintf("The number of options for the quick poll (%d-%d)", int(min_options), int(max_options)),
					Required:    true,
					MinValue:    &min_options,
					MaxValue:    max_options,
				},
			},
		},
		HandlerFunc: m.handleQuickPoll,
	}
}

func (m *PollModule) Service() types.ModuleService {
	return nil
}

func (m *PollModule) handleQuickPoll(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Obtaining and parsing through options
	options := i.ApplicationCommandData().Options
	if len(options) < 1 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Missing required parameter. Please specify the number of options",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	var optionCount int = int(options[0].IntValue())

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "✅ Creating quick-poll!",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// Create message for the bot to react to
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: "📠 Quick Poll Created! **React below to vote!**",
	})
	if err != nil {
		return
	}

	var emojiArray []string = []string{
		"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣",
		"6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟",
	}

	for j := 0; j < optionCount && j < len(emojiArray); j++ {
		s.MessageReactionAdd(msg.ChannelID, msg.ID, emojiArray[j])
	}
}
