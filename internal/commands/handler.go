package commands

import (
	"gamerpal/internal/utils"
	"log"
	"slices"

	"github.com/bwmarrin/discordgo"
)

// Handler handles command processing
type Handler struct {
	commands map[string]*discordgo.ApplicationCommand
}

// NewHandler creates a new command handler
func NewHandler() *Handler {
	return &Handler{
		commands: make(map[string]*discordgo.ApplicationCommand),
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

	// We only respond to users with the correct role or just straight up admin perms.
	// TODO: only some commands should be restricted to admins
	adminRoleIDs := []string{"148527996343549952", "513804949964980235"}
	if !slices.ContainsFunc(i.Member.Roles, func(role string) bool {
		return slices.Contains(adminRoleIDs, role)
	}) && !utils.HasAdminPermissions(s, i) {
		// s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		// 	Type: discordgo.InteractionResponseChannelMessageWithSource,
		// 	Data: &discordgo.InteractionResponseData{
		// 		Content: "❌ You do not have permission to use this command.",
		// 		Flags:   discordgo.MessageFlagsEphemeral,
		// 	},
		// })
		log.Printf("User %s tried to use command %s without permission", i.Member.User.Username, i.ApplicationCommandData().Name)
		log.Printf("Roles: %v", i.Member.Roles)
		return
	}

	switch i.ApplicationCommandData().Name {
	case "userstats":
		h.handleUserStats(s, i)
	case "ping":
		h.handlePing(s, i)
	case "help":
		h.handleHelp(s, i)
	case "prune-inactive":
		h.handlePruneInactive(s, i)
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
