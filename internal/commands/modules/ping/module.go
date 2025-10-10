package ping

import (
	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the ping command
type Module struct{}

// New creates a new ping module
func New() *Module {
	return &Module{}
}

// Register adds the ping command to the command map
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["ping"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Check if the bot is responsive",
		},
		HandlerFunc: m.handlePing,
	}
}

// handlePing handles the ping slash command
func (m *Module) handlePing(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "🏓 Pong! Bot is online and responsive.",
		},
	})
}
