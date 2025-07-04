package commands

import (
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"log"
	"slices"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Handler handles command processing
type Handler struct {
	commands   map[string]*discordgo.ApplicationCommand
	igdbClient *igdb.Client
}

// NewHandler creates a new command handler
func NewHandler(cfg *config.Config) *Handler {
	// Create IGDB client
	igdbClient := igdb.NewClient(cfg.IGDBClientID, cfg.IGDBClientToken, nil)

	return &Handler{
		commands:   make(map[string]*discordgo.ApplicationCommand),
		igdbClient: igdbClient,
	}
}

// RegisterCommands registers all slash commands with Discord
func (h *Handler) RegisterCommands(s *discordgo.Session) error {
	// Define all slash commands
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "userstats",
			Description: "Show member statistics for the server",
		},
		{
			Name:        "ping",
			Description: "Check if the bot is responsive",
		},
		{
			Name:        "help",
			Description: "Show all available commands",
		},
		{
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
		{
			Name:        "prune-inactive",
			Description: "Remove users without any roles (dry run by default)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "execute",
					Description: "Actually remove users (default: false for dry run)",
					Required:    false,
				},
			},
		},
	}

	// Register commands globally
	for _, command := range commands {
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, "", command)
		if err != nil {
			return err
		}
		h.commands[cmd.Name] = cmd
		log.Printf("Registered command: %s", cmd.Name)
	}

	return nil
}

// HandleInteraction processes slash command interactions
func (h *Handler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name == "" {
		return
	}

	cmds := map[string]struct {
		requireAdmin bool
		handlerFunc  func(s *discordgo.Session, i *discordgo.InteractionCreate)
	}{
		"userstats": {
			requireAdmin: true,
			handlerFunc:  h.handleUserStats,
		},
		"prune-inactive": {
			requireAdmin: true,
			handlerFunc:  h.handlePruneInactive,
		},
		"ping": {
			requireAdmin: false,
			handlerFunc:  h.handlePing,
		},
		"help": {
			requireAdmin: false,
			handlerFunc:  h.handleHelp,
		},
		"game": {
			requireAdmin: false,
			handlerFunc:  h.handleGame,
		},
	}

	for name, cmd := range cmds {
		if i.ApplicationCommandData().Name == name {
			// Check if admin permissions are required
			if cmd.requireAdmin {
				if !h.adminCheck(s, i) {
					return
				}
			}
			// Call the appropriate handler function
			cmd.handlerFunc(s, i)
			return
		}
	}
}

// UnregisterCommands removes all registered commands (useful for cleanup)
func (h *Handler) UnregisterCommands(s *discordgo.Session) {
	for name, cmd := range h.commands {
		err := s.ApplicationCommandDelete(s.State.User.ID, "", cmd.ID)
		if err != nil {
			log.Printf("Error deleting command %s: %v", name, err)
		} else {
			log.Printf("Unregistered command: %s", name)
		}
	}
}

// adminCheck checks if the user has admin permissions for a command
func (h *Handler) adminCheck(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	// When needed, we only respond to users with the correct role or just straight up admin perms.
	isAdmin := func() bool {
		adminRoleIDs := []string{"148527996343549952", "513804949964980235"}
		hasAdminRole := slices.ContainsFunc(i.Member.Roles, func(role string) bool {
			return slices.Contains(adminRoleIDs, role)
		})

		if hasAdminRole {
			return true
		}

		if utils.HasAdminPermissions(s, i) {
			return true
		}

		return false
	}()

	if !isAdmin {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You do not have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return false
	}
	return true
}
