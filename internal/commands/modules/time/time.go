package time

import (
	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"
)

// TimeModule is now a deprecated development-only stub used to trigger Discord command unregistration.
type TimeModule struct{}

// New returns a new stub time module.
func New(deps *types.Dependencies) *TimeModule { return &TimeModule{} }

// Register adds a development-only placeholder for /time so it will be unregistered by the handler.
func (m *TimeModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["time"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "time",
			Description: "(deprecated) formerly time/date utilities",
		},
		HandlerFunc: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			// No-op: command is development-only and will not be visible.
		},
		Development: true,
	}
}

// Service returns nil (no service for deprecated module)
func (m *TimeModule) Service() types.ModuleService { return nil }
