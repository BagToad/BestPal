package commands

import (
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"log"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Command represents a Discord application command with its handler
type Command struct {
	ApplicationCommand *discordgo.ApplicationCommand
	HandlerFunc        func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

// Handler handles command processing
type Handler struct {
	igdbClient *igdb.Client
	Commands   map[string]*Command
	CryptoSalt string
}

// NewHandler creates a new command handler
func NewHandler(cfg *config.Config) *Handler {
	// Create IGDB client
	igdbClient := igdb.NewClient(cfg.IGDBClientID, cfg.IGDBClientToken, nil)

	h := &Handler{
		igdbClient: igdbClient,
		Commands:   make(map[string]*Command),
		CryptoSalt: cfg.IGDBClientToken[len(cfg.IGDBClientToken)-5:],
	}

	var adminPerms int64 = discordgo.PermissionAdministrator
	var modPerms int64 = discordgo.PermissionBanMembers

	// Define all commands
	commands := []*Command{
		{
			ApplicationCommand: &discordgo.ApplicationCommand{
				Name:                     "userstats",
				Description:              "Show member statistics for the server",
				Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
				DefaultMemberPermissions: &modPerms,
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
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "parse",
						Description: "Parse a date/time and convert it to Discord timestamp format",
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
				Name:                     "welcome",
				Description:              "Generate a welcome message for new members (admin only)",
				DefaultMemberPermissions: &modPerms,
				Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionInteger,
						Name:        "minutes",
						Description: "How many minutes back to look for new members",
						Required:    true,
						MinValue:    utils.Float64Ptr(1),
						MaxValue:    1440, // 24 hours
					},
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "execute",
						Description: "Actually send the message (default: false for preview only)",
						Required:    false,
					},
				},
			},
			HandlerFunc: h.handleWelcome,
		},
	}

	// Populate the commands map
	for _, cmd := range commands {
		h.Commands[cmd.ApplicationCommand.Name] = cmd
	}

	return h
}

// RegisterCommands registers all slash commands with Discord
func (h *Handler) RegisterCommands(s *discordgo.Session) error {
	// Register commands globally
	for _, c := range h.Commands {
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, "", c.ApplicationCommand)
		if err != nil {
			return err
		}
		// Update the local command with the ID returned from Discord
		c.ApplicationCommand.ID = cmd.ID
		log.Printf("Registered command: %s", cmd.Name)
	}

	return nil
}

// HandleInteraction processes slash command interactions
func (h *Handler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name == "" {
		return
	}

	commandName := i.ApplicationCommandData().Name
	if cmd, exists := h.Commands[commandName]; exists {
		cmd.HandlerFunc(s, i)
	}
}

// UnregisterCommands removes all registered commands (useful for cleanup)
func (h *Handler) UnregisterCommands(s *discordgo.Session) {
	// Get all existing commands from Discord
	existingCommands, err := s.ApplicationCommands(s.State.User.ID, "")
	if err != nil {
		log.Printf("Error fetching existing commands: %v", err)
		return
	}

	// Delete each command that exists in our local command map
	for _, existingCmd := range existingCommands {
		if _, exists := h.Commands[existingCmd.Name]; exists {
			err := s.ApplicationCommandDelete(s.State.User.ID, "", existingCmd.ID)
			if err != nil {
				log.Printf("Error deleting command %s: %v", existingCmd.Name, err)
			} else {
				log.Printf("Unregistered command: %s", existingCmd.Name)
			}
		}
	}
}
