package prune

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for prune commands
type PruneModule struct {
	config     *config.Config
	forumCache *forumcache.Service
	service    *Service
}

// New creates a new prune module
func New(deps *types.Dependencies) *PruneModule {
	return &PruneModule{
		config:     deps.Config,
		forumCache: deps.ForumCache,
		service:    NewService(deps.Config, deps.ForumCache),
	}
}

// Register adds prune commands to the command map
func (m *PruneModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {

	var adminPerms int64 = discordgo.PermissionAdministrator
	var modPerms int64 = discordgo.PermissionBanMembers

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
			Description:              "Prune forum threads from departed owners and duplicate intros (dry-run by default)",
			DefaultMemberPermissions: &modPerms,
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

// Service returns the prune service for scheduled intro pruning
func (m *PruneModule) Service() types.ModuleService {
	return m.service
}
