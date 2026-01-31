package types

import (
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/forumcache"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Command represents a Discord application command with its handler
type Command struct {
	ApplicationCommand *discordgo.ApplicationCommand
	HandlerFunc        func(s *discordgo.Session, i *discordgo.InteractionCreate)
	Development        bool
}

// BaseService provides common session hydration functionality for all services
type BaseService struct {
	Session *discordgo.Session // Exported for external hydration
}

// HydrateServiceDiscordSession hydrates the service with a Discord session
func (b *BaseService) HydrateServiceDiscordSession(s *discordgo.Session) error {
	b.Session = s
	return nil
}

// ModuleService represents a service that requires session initialization
// and may have recurring scheduled tasks
type ModuleService interface {
	// HydrateServiceDiscordSession hydrates the service with a Discord session
	// This is called after the Discord session is established
	HydrateServiceDiscordSession(s *discordgo.Session) error

	// ScheduledFuncs returns a map of cron schedules to functions to be called on that schedule.
	// Map keys are cron expressions (e.g., "@every 1m", "@hourly", "*/5 * * * *")
	// Map values are functions to execute on that schedule
	// Returns nil if no scheduled tasks are needed
	ScheduledFuncs() map[string]func() error
}

// CommandModule represents a module that can register commands
// Each module should contain:
// - Command definition(s)
// - Handler function(s)
// - Associated service if needed (max one service per module)
type CommandModule interface {
	// Register adds the module's commands to the provided map
	Register(commands map[string]*Command, deps *Dependencies)

	// Service returns the service that needs session initialization
	// Returns nil if the module has no service requiring initialization
	Service() ModuleService
}

// Dependencies contains shared dependencies that command modules may need
type Dependencies struct {
	Config     *config.Config
	DB         *database.DB
	IGDBClient *igdb.Client
	Session    *discordgo.Session
	ForumCache *forumcache.Service
}
