package commands

import (
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/lfgpanel"
	"gamerpal/internal/pairing"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Command represents a Discord application command with its handler
type Command struct {
	ApplicationCommand *discordgo.ApplicationCommand
	HandlerFunc        func(s *discordgo.Session, i *discordgo.InteractionCreate)
	Development        bool
}

// SlashCommandHandler handles command processing
type SlashCommandHandler struct {
	igdbClient     *igdb.Client
	Commands       map[string]*Command
	config         *config.Config
	DB             *database.DB
	PairingService *pairing.PairingService

	// LFG Now panel service (extracted state)
	lfgNowSvc *lfgpanel.InMemoryService
}

// NewSlashHandler creates a new command handler
func NewSlashHandler(cfg *config.Config) *SlashCommandHandler {
	// Create IGDB client
	igdbClient := igdb.NewClient(cfg.GetIGDBClientID(), cfg.GetIGDBClientToken(), nil)

	// Initialize SQLite database
	db, err := database.NewDB(cfg.GetDatabasePath())
	if err != nil {
		cfg.Logger.Warn("Warning: Failed to initialize database: %v", err)
		// Continue without database for now
	}

	// initialize lfg now panel service
	lfgSvc := lfgpanel.NewLFGPanelService(cfg).WithLogger(
		func(msg string, args ...any) { cfg.Logger.Infof(msg, args...) },
		func(msg string, args ...any) { cfg.Logger.Warnf(msg, args...) },
	)

	h := &SlashCommandHandler{
		igdbClient: igdbClient,
		Commands:   make(map[string]*Command),
		config:     cfg,
		DB:         db,
		lfgNowSvc:  lfgSvc,
	}

	var adminPerms int64 = discordgo.PermissionAdministrator
	var modPerms int64 = discordgo.PermissionBanMembers

	// Define all commands
	commands := []*Command{
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "lfg",
				Description: "LFG (Looking For Group) utilities",
				Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
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
						},
					},
				},
			},
			HandlerFunc: h.handleLFG,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "refresh-igdb",
				Description: "Refresh the IGDB client token using stored credentials (super-admin only)",
				Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM},
			},
			HandlerFunc: h.handleRefreshIGDB,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "config",
				Description: "View or modify the bot configuration (super-admin only)",
				Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM},
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "set",
						Description: "Set a configuration value",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "key",
								Description: "The configuration key to set",
								Required:    true,
							},
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "value",
								Description: "The value to set for the key",
								Required:    true,
							},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "list-keys",
						Description: "List all available configuration keys",
					},
				},
			},
			HandlerFunc: h.handleConfig,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "log",
				Description: "Log file management commands (super-admin only)",
				Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM},
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "download",
						Description: "Download all current log files as a zip archive",
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "latest",
						Description: "Download the last 500 lines of the latest log file",
					},
				},
			},
			HandlerFunc: h.handleLog,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:                     "userstats",
				Description:              "Show member statistics for the server",
				Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
				DefaultMemberPermissions: &modPerms,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "stats",
						Description: "Which statistics to show",
						Required:    false,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{
								Name:  "Overview",
								Value: "overview",
							},
							{
								Name:  "Daily (Last 7 Days)",
								Value: "daily",
							},
						},
					},
				},
			},
			HandlerFunc: h.handleUserStats,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "ping",
				Description: "Check if the bot is responsive",
			},
			HandlerFunc: h.handlePing,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "intro",
				Description: "Look up a user's latest introduction post from the introductions forum",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "The user whose introduction to look up (defaults to yourself)",
						Required:    false,
					},
				},
			},
			HandlerFunc: h.handleIntro,
		},
		{
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
			HandlerFunc: h.handlePruneForum,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "help",
				Description: "Show all available commands",
			},
			HandlerFunc: h.handleHelp,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "game",
				Description: "Look up information about a video game",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "name",
						Description: "The name of the game to search for",
						Required:    true,
					},
				},
			},
			HandlerFunc: h.handleGame,
		},
		{
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
			HandlerFunc: h.handlePruneInactive,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:        "time",
				Description: "Time-related utilities",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "datetime",
						Description: "The date/time to parse (e.g., 'January 1, 2025 MDT', '1:45PM MDT')",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "full",
						Description: "If true, print out all discord timestamp formats",
						Required:    false,
					},
				},
			},
			HandlerFunc: h.handleTime,
		},
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:                     "say",
				Description:              "Send an anonymous message to a specified channel (admin only)",
				DefaultMemberPermissions: &modPerms,
				Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        "channel",
						Description: "The channel to send the message to",
						Required:    true,
						ChannelTypes: []discordgo.ChannelType{
							discordgo.ChannelTypeGuildText,
							discordgo.ChannelTypeGuildNews,
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message",
						Description: "The message to send",
						Required:    true,
					},
				},
			},
			HandlerFunc: h.handleSay,
		},
		{
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
			HandlerFunc: h.handleRoulette,
			Development: true, // Disabled while in development
		},
		{
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
			HandlerFunc: h.handleRouletteAdmin,
			Development: true, // Disabled while in development
		},
	}

	// Populate the commands map
	for _, cmd := range commands {
		h.Commands[cmd.ApplicationCommand.Name] = cmd
	}

	return h
}

// GetDB returns the database instance (used by scheduler)
func (h *SlashCommandHandler) GetDB() *database.DB {
	return h.DB
}

// InitializePairingService initializes the pairing service with a Discord session
func (h *SlashCommandHandler) InitializePairingService(session *discordgo.Session) {
	h.PairingService = pairing.NewPairingService(session, h.config, h.DB)
}

// RegisterCommands registers all slash commands with Discord
func (h *SlashCommandHandler) RegisterCommands(s *discordgo.Session) error {
	// Get all existing commands from Discord
	existingCommands, err := s.ApplicationCommands(s.State.User.ID, "")
	if err != nil {
		h.config.Logger.Warn("Error fetching existing commands: %v", err)
		return err
	}

	for _, c := range h.Commands {
		// If a command is in development, we're not only going to skip it, but we'll
		// also unregister it if it exists.
		if c.Development {
			for _, existingCmd := range existingCommands {
				if existingCmd.Name == c.ApplicationCommand.Name {
					err := s.ApplicationCommandDelete(s.State.User.ID, "", existingCmd.ID)
					if err != nil {
						h.config.Logger.Warn("Error deleting command %s: %v", c.ApplicationCommand.Name, err)
					} else {
						h.config.Logger.Infof("Unregistered command: %s", c.ApplicationCommand.Name)
					}
				}
			}
			continue
		}

		// Register commands globally
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, "", c.ApplicationCommand)
		if err != nil {
			return err
		}

		// Update the local command with the ID returned from Discord
		c.ApplicationCommand.ID = cmd.ID
		h.config.Logger.Infof("Registered command: %s", cmd.Name)
	}

	return nil
}

// HandleInteraction routes slash command interactions
func (h *SlashCommandHandler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name == "" {
		return
	}

	commandName := i.ApplicationCommandData().Name
	if cmd, exists := h.Commands[commandName]; exists {
		cmd.HandlerFunc(s, i)
	}
}

// UnregisterCommands removes all registered commands (useful for cleanup)
func (h *SlashCommandHandler) UnregisterCommands(s *discordgo.Session) {
	// Get all existing commands from Discord
	existingCommands, err := s.ApplicationCommands(s.State.User.ID, "")
	if err != nil {
		h.config.Logger.Warn("Error fetching existing commands: %v", err)
		return
	}

	// Delete each command that exists in our local command map
	for _, existingCmd := range existingCommands {
		if _, exists := h.Commands[existingCmd.Name]; exists {
			err := s.ApplicationCommandDelete(s.State.User.ID, "", existingCmd.ID)
			if err != nil {
				h.config.Logger.Warn("Error deleting command %s: %v", existingCmd.Name, err)
			} else {
				h.config.Logger.Infof("Unregistered command: %s", existingCmd.Name)
			}
		}
	}
}
