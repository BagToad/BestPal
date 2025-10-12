package welcome

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
)

// WelcomeModule handles welcome-related scheduled tasks
// Note: This module has no slash commands, only scheduled background services
type WelcomeModule struct {
	service *WelcomeService
	config  *config.Config
}

// New creates a new WelcomeModule instance
func New(deps *types.Dependencies) types.CommandModule {
	return &WelcomeModule{
		config:  deps.Config,
		service: NewWelcomeService(nil, deps.Config),
	}
}

// Register registers the module (no commands for this module)
func (m *WelcomeModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	// No commands to register - this module only provides scheduled services
}

// Service returns the module's service that needs session initialization
func (m *WelcomeModule) Service() types.ModuleService {
	return m.service
}
