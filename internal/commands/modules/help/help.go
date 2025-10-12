package help

import (
	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the help command
type HelpModule struct{}

// New creates a new help module
func New(deps *types.Dependencies) *HelpModule {
	return &HelpModule{}
}

// Register adds the help command to the command map
func (m *HelpModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["help"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "help",
			Description: "Show all available commands",
		},
		HandlerFunc: m.handleHelp,
	}
}

// handleHelp handles the help slash command
func (m *HelpModule) handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := helpCommandsEmbed()

	// Respond immediately with the embed
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

// Service returns nil as this module has no services requiring initialization
func (m *HelpModule) Service() types.ModuleService {
return nil
}
