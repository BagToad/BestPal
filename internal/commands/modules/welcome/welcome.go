package welcome

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

// WelcomeModule handles welcome-related scheduled tasks
// Note: This module has no slash commands, only scheduled background services
type WelcomeModule struct {
	service *WelcomeService
	config  *config.Config
	db      *database.DB
}

// New creates a new WelcomeModule instance
func New(deps *types.Dependencies) types.CommandModule {
	return &WelcomeModule{
		config:  deps.Config,
		service: NewWelcomeService(deps),
		db:      deps.DB,
	}
}

// Register registers the module (no commands for this module)
func (m *WelcomeModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var modPerms int64 = discordgo.PermissionBanMembers
	cmds["setwelcomemsg"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "setwelcomemsg",
			Type:                     discordgo.MessageApplicationCommand,
			DefaultMemberPermissions: &modPerms,
			NameLocalizations: &map[discordgo.Locale]string{
				discordgo.EnglishUS: "Set as automatic welcome message",
				discordgo.EnglishGB: "Set as automatic welcome message",
			},
		},
		
		HandlerFunc: m.handleSetWelcomeMsg,
	}
}

func (m *WelcomeModule) handleSetWelcomeMsg(s *discordgo.Session, i *discordgo.InteractionCreate) {
	msg := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID].Content
	userID := i.Member.User.ID

	err := m.db.AddWelcomeMessage(userID, msg)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: func() string {
					return "❌ Failed to set message as the welcome message!"
				}(),
				Flags: discordgo.MessageFlagsEphemeral,
			},
		})
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: func() string {
				return "🔥 Message has been successfully set as the welcome message!."
			}(),
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

// Service returns the module's service that needs session initialization
func (m *WelcomeModule) Service() types.ModuleService {
	return m.service
}
