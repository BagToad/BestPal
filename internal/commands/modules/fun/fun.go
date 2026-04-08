package fun

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"time"

	"github.com/bwmarrin/discordgo"
)

const cavemanSystemPrompt = `translate to broken caveman english in roughly the same word length.

ONLY reply with the translation in this format:

<format>

hey, I translated this to caveman for you:

> <translation>
</format>`

// FunModule implements the CommandModule interface for fun commands
type FunModule struct {
	config *config.Config
}

// New creates a new fun module
func New(deps *types.Dependencies) *FunModule {
	return &FunModule{
		config: deps.Config,
	}
}

// Register adds fun-related commands to the command map
func (m *FunModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var modPerms int64 = discordgo.PermissionBanMembers

	cmds["typing"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "typing",
			Description:              "Make the bot show as typing in a channel",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel to type in",
					Required:    true,
					ChannelTypes: []discordgo.ChannelType{
						discordgo.ChannelTypeGuildText,
						discordgo.ChannelTypeGuildNews,
						discordgo.ChannelTypeGuildPublicThread,
						discordgo.ChannelTypeGuildPrivateThread,
						discordgo.ChannelTypeGuildNewsThread,
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "minutes",
					Description: "How many minutes to show as typing (1-10)",
					Required:    true,
					MinValue:    utils.Float64Ptr(1),
					MaxValue:    10,
				},
			},
		},
		HandlerFunc: m.handleTyping,
	}

	cmds["Translate to caveman"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "Translate to caveman",
			Type:                     discordgo.MessageApplicationCommand,
			DefaultMemberPermissions: &modPerms,
		},
		HandlerFunc: m.handleCavemanTranslate,
	}

	cmds["connect4"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "connect4",
			Description: "Challenge someone to a game of Connect 4",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "opponent",
					Description: "The player you want to challenge",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handleConnect4Challenge,
	}
}

// handleCavemanTranslate handles the "Translate to caveman" message context menu command.
func (m *FunModule) handleCavemanTranslate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	targetMsg := data.Resolved.Messages[data.TargetID]

	if targetMsg == nil || targetMsg.Content == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ That message has no text content to translate.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer so the user knows we're working on it
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	modelsClient := utils.NewModelsClient(m.config)
	translation := modelsClient.ModelsRequest(cavemanSystemPrompt, targetMsg.Content, "deepseek/DeepSeek-V3-0324")

	if translation == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("❌ Failed to translate. The AI didn't return anything."),
		})
		return
	}

	// Reply to the original message in the channel
	_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Content: translation,
		Reference: &discordgo.MessageReference{
			MessageID: targetMsg.ID,
			ChannelID: i.ChannelID,
		},
	})
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr(fmt.Sprintf("❌ Translation succeeded but failed to send reply: %v", err)),
		})
		return
	}

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: strPtr("🪨 Caveman translation sent!"),
	})
}

// Service returns nil as this module has no services requiring initialization
func (m *FunModule) Service() types.ModuleService {
	return nil
}

// handleTyping makes the bot show as typing in a channel for a specified duration.
func (m *FunModule) handleTyping(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	var channelID string
	var minutes int64

	for _, opt := range options {
		switch opt.Name {
		case "channel":
			ch := opt.ChannelValue(s)
			if ch != nil {
				channelID = ch.ID
			}
		case "minutes":
			minutes = opt.IntValue()
		}
	}

	ch, err := s.Channel(channelID)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not access that channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	perms, err := s.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil || perms&discordgo.PermissionSendMessages == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ I don't have permission to send messages in %s.", ch.Mention()),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("⌨️ Typing in %s for %d minute(s)...", ch.Mention(), minutes),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// Discord typing indicator lasts ~10 seconds, so we re-trigger every 8s
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		end := time.Now().Add(time.Duration(minutes) * time.Minute)

		// Fire immediately, then on each tick
		_ = s.ChannelTyping(channelID)
		for t := range ticker.C {
			if t.After(end) {
				return
			}
			if err := s.ChannelTyping(channelID); err != nil {
				m.config.Logger.Warnf("typing loop stopped for channel %s: %v", channelID, err)
				return
			}
		}
	}()
}

func strPtr(s string) *string {
	return &s
}
