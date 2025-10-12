package ping

import (
	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"
)

// PingModule implements the CommandModule interface for the ping command
type PingModule struct{}

// New creates a new ping module
func New() *PingModule {
	return &PingModule{}
}

// Register adds the ping command to the command map
func (m *PingModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["ping"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Check if the bot is responsive",
		},
		HandlerFunc: m.handlePing,
	}
}

// handlePing handles the ping slash command
func (m *PingModule) handlePing(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "🏓 Pong! Bot is online and responsive.",
		},
	})
}

// GetServices returns nil as this module has no services requiring initialization
func (m *PingModule) Service() types.ModuleService {
return nil
}
