package roulette

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// RouletteModule implements the CommandModule interface for roulette commands
type RouletteModule struct {
	config         *config.Config
	db             *database.DB
	igdbClient     *igdb.Client
	pairingService *PairingService
}

// New creates a new roulette module
func New(deps *types.Dependencies) *RouletteModule {
	return &RouletteModule{
		config:         deps.Config,
		db:             deps.DB,
		igdbClient:     deps.IGDBClient,
		pairingService: NewPairingService(deps.Config, deps.DB),
	}
}

// Register adds roulette commands to the command map
func (m *RouletteModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var adminPerms int64 = discordgo.PermissionAdministrator

	// Register roulette command
	cmds["roulette"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "roulette",
			Description: "Sign up for a pairing or manage your game list",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "help",
					Description: "Show detailed help for roulette commands",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "signup",
					Description: "Sign up for roulette pairing",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "nah",
					Description: "Remove yourself from roulette pairing",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "games-add",
					Description: "Add games to your roulette list",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "name",
							Description: "Game name or comma-separated list of games",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "games-remove",
					Description: "Remove games from your roulette list",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "name",
							Description: "Game name or comma-separated list of games",
							Required:    true,
						},
					},
				},
			},
		},
		HandlerFunc: m.handleRoulette,
		Development: true,
	}

	// Register roulette-admin command
	cmds["roulette-admin"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "roulette-admin",
			Description:              "Admin commands for managing the roulette system",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "help",
					Description: "Show detailed help for roulette admin commands",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "debug",
					Description: "Show debug information about the roulette system",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "pair",
					Description: "Schedule or execute pairing",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "time",
							Description: "Schedule pairing for this time",
							Required:    false,
						},
						{
							Type:        discordgo.ApplicationCommandOptionBoolean,
							Name:        "immediate-pair",
							Description: "Execute pairing immediately",
							Required:    false,
						},
						{
							Type:        discordgo.ApplicationCommandOptionBoolean,
							Name:        "dryrun",
							Description: "Dry run mode (default: true)",
							Required:    false,
						},
					},
				},
				{
					Name:        "simulate-pairing",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Description: "Simulate pairing with fake users for testing purposes",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "user-count",
							Description: "Number of fake users to simulate (4-50, default: 8)",
							Required:    false,
							MinValue:    &[]float64{4}[0],
							MaxValue:    50,
						},
						{
							Type:        discordgo.ApplicationCommandOptionBoolean,
							Name:        "create-channels",
							Description: "Actually create pairing channels with fake users (default: false)",
							Required:    false,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "reset",
					Description: "Delete all existing pairing channels",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "delete-schedule",
					Description: "Remove the current scheduled pairing time",
				},
			},
		},
		HandlerFunc: m.handleRouletteAdmin,
		Development: true, // Disabled while in development
	}
}

// Service returns the module's service that needs session initialization
func (m *RouletteModule) Service() types.ModuleService {
	return m.pairingService
}
