package refreshigdb

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/igdbclient"
	"gamerpal/internal/utils"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the refresh-igdb command
type Module struct {
	config     *config.Config
	igdbClient *igdbclient.Client
}

// New creates a new refresh-igdb module
func New(deps *types.Dependencies) *Module {
	return &Module{
		config:     deps.Config,
		igdbClient: deps.IGDBClient,
	}
}

// Register adds the refresh-igdb command to the command map
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config
	m.igdbClient = deps.IGDBClient

	var adminPerms int64 = discordgo.PermissionAdministrator

	cmds["refresh-igdb"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "refresh-igdb",
			Description:              "Force rotate the IGDB access token (SuperAdmin only)",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM, discordgo.InteractionContextPrivateChannel},
		},
		HandlerFunc: m.handleRefreshIGDB,
	}
}

// handleRefreshIGDB forces a token refresh for the IGDB client.
// Only usable in bot DM context by super admins.
func (m *Module) handleRefreshIGDB(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, m.config) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ You do not have permission to use this command.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	// Immediate deferred response (ephemeral) in case refresh takes longer.
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	if m.igdbClient == nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: new("❌ IGDB client is not initialized.")})
		return
	}

	startTime := time.Now()
	err := m.igdbClient.RefreshToken()
	elapsed := time.Since(startTime)

	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: new(fmt.Sprintf("❌ Failed to force-refresh token: %v", err))})
		return
	}

	msg := fmt.Sprintf("✅ IGDB token force-rotated in %.2f seconds.\n\nThe new token is now active for all IGDB operations.", elapsed.Seconds())
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: new(msg)})
}

// Service returns nil as this module has no services requiring initialization
func (m *Module) Service() types.ModuleService {
	return nil
}
