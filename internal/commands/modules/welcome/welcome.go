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

// InitializeService initializes the welcome service with a Discord session
func (m *WelcomeModule) InitializeService(s *discordgo.Session) error {
	m.service = NewWelcomeService(s, m.config)
	return nil
}

// MinuteFuncs returns functions to be called every minute
func (m *WelcomeModule) MinuteFuncs() []func() error {
	return []func() error{
		func() error {
			m.service.CleanNewPalsRoleFromOldMembers()
			m.service.CheckAndWelcomeNewPals()
			return nil
		},
	}
}

// HourFuncs returns nil as this module has no hourly tasks
func (m *WelcomeModule) HourFuncs() []func() error {
	return nil
}

// Service returns the module's service that needs session initialization
func (m *WelcomeModule) Service() types.ModuleService {
	return m
}

// WelcomeService returns the welcome service for scheduler access
func (m *WelcomeModule) WelcomeService() *WelcomeService {
	return m.service
}
