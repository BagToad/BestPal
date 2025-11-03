package lfg

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/forumcache"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for LFG commands
type LfgModule struct {
	config     *config.Config
	igdbClient *igdb.Client
	forumCache *forumcache.Service
}

// New creates a new LFG module
func New(deps *types.Dependencies) *LfgModule {
	return &LfgModule{config: deps.Config, igdbClient: deps.IGDBClient, forumCache: deps.ForumCache}
}

// Register adds LFG commands to the command map
func (m *LfgModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {

	var modPerms int64 = discordgo.PermissionBanMembers

	// Register lfg command
	cmds["lfg"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "lfg",
			Description: "LFG (Looking For Group) utilities",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "now",
					Description: "Mark yourself as looking now inside an LFG thread",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "region",
							Description: "Region",
							Required:    true,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "North America", Value: "North America"},
								{Name: "Europe", Value: "Europe"},
								{Name: "Asia", Value: "Asia"},
								{Name: "South America", Value: "South America"},
								{Name: "Oceania", Value: "Oceania"},
								{Name: "Any Region", Value: "Any Region"},
							},
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "message",
							Description: "Short message",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "player_count",
							Description: "Desired player count",
							Required:    true,
							MinValue:    &[]float64{1}[0],
							MaxValue:    99,
						},
						{
							Type:        discordgo.ApplicationCommandOptionChannel,
							Name:        "voice_channel",
							Description: "Optional voice channel to join",
							Required:    false,
							ChannelTypes: []discordgo.ChannelType{
								discordgo.ChannelTypeGuildVoice,
								discordgo.ChannelTypeGuildStageVoice,
							},
						},
					},
				},
			},
		},
		HandlerFunc: m.handleLFG,
	}

	// Register lfg-admin command (expanded to include cache stats)
	cmds["lfg-admin"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "lfg-admin",
			Description: "LFG admin commands",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "setup-find-a-thread",
					Description: "Set up the LFG find-a-thread panel in this channel",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "setup-looking-now",
					Description: "Set up the 'Looking NOW' panel in this channel",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "refresh-thread-cache",
					Description: "Rebuild all registered forum caches (LFG + Introductions)",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "cache-stats",
					Description: "Show forum cache stats (LFG + Introductions)",
				},
			},
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleLFG,
	}

	// Register game-thread command
	cmds["game-thread"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "game-thread",
			Description: "Find a game thread by searching the LFG forum",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "search-query",
					Description:  "Game name to search for",
					Required:     true,
					Autocomplete: true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "ephemeral",
					Description: "Whether to show the response only to you (default: true)",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleGameThread,
	}
}

// HandleComponent handles component interactions for LFG
func (m *LfgModule) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleLFGComponent(s, i)
}

// HandleModalSubmit handles modal submissions for LFG
func (m *LfgModule) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleLFGModalSubmit(s, i)
}

// HandleAutocomplete handles autocomplete interactions for LFG commands
func (m *LfgModule) HandleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleGameThreadAutocomplete(s, i)
}

// Service returns nil as this module has no services requiring initialization
func (m *LfgModule) Service() types.ModuleService {
	return nil
}
