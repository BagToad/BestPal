package prune

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for prune commands
type PruneModule struct {
	config *config.Config
}

// New creates a new prune module
func New() *PruneModule {
	return &PruneModule{}
}

// Register adds prune commands to the command map
func (m *PruneModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config

	var adminPerms int64 = discordgo.PermissionAdministrator

	// Register prune-inactive command
	cmds["prune-inactive"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "prune-inactive",
			Description:              "Remove users without any roles (dry run by default)",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "execute",
					Description: "Actually remove users (default: false for dry run)",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handlePruneInactive,
	}

	// Register prune-forum command
	cmds["prune-forum"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "prune-forum",
			Description:              "Find forum threads whose starter post was deleted; delete them (dry-run by default)",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "forum",
					Description: "The forum channel to prune",
					Required:    true,
					ChannelTypes: []discordgo.ChannelType{
						discordgo.ChannelTypeGuildForum,
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "execute",
					Description: "Actually delete the threads (default: false for dry run)",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handlePruneForum,
	}
}

// GetServices returns nil as this module has no services requiring initialization
func (m *PruneModule) Service() types.ModuleService {
return nil
}
