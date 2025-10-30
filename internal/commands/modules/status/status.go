package status

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// StatusModule provides the /status command to update the bot's presence text (intended for mods).
type StatusModule struct {
	config *config.Config
}

// New creates a new status module
func New(deps *types.Dependencies) *StatusModule { return &StatusModule{config: deps.Config} }

// Register registers /status.
func (m *StatusModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config
	var adminPerms int64 = discordgo.PermissionAdministrator

	cmds["status"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "status",
			Description:              "Update the bot's status (mod only)",
			DefaultMemberPermissions: &adminPerms,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "text",
					Description: "Status text to display (activity)",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handleStatus,
	}
}

// handleStatus updates the presence text.
func (m *StatusModule) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract text option
	var text string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "text" {
			text = opt.StringValue()
			break
		}
	}

	if text == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ You must provide status text.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	if len([]rune(text)) > 128 { // Discord activity name limit safeguard
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ Status text must be 128 characters or fewer.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	// Log the attempt and who requested it
	mentionText := "Member"
	if i.Member != nil {
		mentionText = i.Member.User.Mention()
	}

	m.config.Logger.Infof("Updating status to: %s (requested by: %s)", text, mentionText)

	// Attempt to update presence
	if err := s.UpdateGameStatus(0, text); err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ Failed to update status: " + err.Error(), Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: "✅ Status updated successfully.", Flags: discordgo.MessageFlagsEphemeral},
	})
}

// Service returns nil; this module has no background services
func (m *StatusModule) Service() types.ModuleService { return nil }
