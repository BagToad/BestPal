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
	config  *config.Config
}

// New creates a new WelcomeModule instance
func New(deps *types.Dependencies) types.CommandModule {
	return &WelcomeModule{
		config: deps.Config,
	}
}

// Register registers the module (no commands for this module)
func (m *WelcomeModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	// No commands to register - this module only provides scheduled services
}

// welcomeServiceWrapper wraps WelcomeService to implement ModuleService
type welcomeServiceWrapper struct {
	module *WelcomeModule
}

func (w *welcomeServiceWrapper) InitializeService(s *discordgo.Session) error {
	w.module.service = NewWelcomeService(s, w.module.config)
	return nil
}

// GetServices returns services that need session initialization
func (m *WelcomeModule) GetServices() []types.ModuleService {
	return []types.ModuleService{
		&welcomeServiceWrapper{module: m},
	}
}

// GetService returns the welcome service for scheduler access
func (m *WelcomeModule) GetService() *WelcomeService {
	return m.service
}
