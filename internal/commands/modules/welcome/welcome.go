package welcome

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// WelcomeModule handles welcome-related scheduled tasks
// Note: This module has no slash commands, only scheduled background services
type WelcomeModule struct {
	service *WelcomeService
}

// New creates a new WelcomeModule instance
func New(deps *types.Dependencies) types.CommandModule {
	return &WelcomeModule{}
}

// Register registers the module (no commands for this module)
func (m *WelcomeModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	// No commands to register - this module only provides scheduled services
}

// InitializeServices initializes the welcome service with session
func (m *WelcomeModule) InitializeServices(session *discordgo.Session, config *config.Config) {
	m.service = NewWelcomeService(session, config)
}

// GetService returns the welcome service for scheduler access
func (m *WelcomeModule) GetService() *WelcomeService {
	return m.service
}
