package lfg

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for LFG commands
type LfgModule struct {
	config     *config.Config
	igdbClient *igdb.Client
}

// New creates a new LFG module
func New() *LfgModule {
	return &LfgModule{}
}

// Register adds LFG commands to the command map
func (m *LfgModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config
	m.igdbClient = deps.IGDBClient

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

	// Register lfg-admin command
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
					Description: "Rebuild the LFG thread name -> thread ID cache (includes archived threads)",
				},
			},
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleLFG,
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
