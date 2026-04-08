package fun

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"

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

	cmds["Translate to caveman"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "Translate to caveman",
			Type:                     discordgo.MessageApplicationCommand,
			DefaultMemberPermissions: &modPerms,
		},
		HandlerFunc: m.handleCavemanTranslate,
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

func strPtr(s string) *string {
	return &s
}
