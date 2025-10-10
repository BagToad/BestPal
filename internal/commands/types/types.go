package types

import (
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Command represents a Discord application command with its handler
type Command struct {
	ApplicationCommand *discordgo.ApplicationCommand
	HandlerFunc        func(s *discordgo.Session, i *discordgo.InteractionCreate)
	Development        bool
}

// ModuleService represents a service that requires session initialization
type ModuleService interface {
	// InitializeService initializes the service with a Discord session
	InitializeService(s *discordgo.Session) error
}

// CommandModule represents a module that can register commands
// Each module should contain:
// - Command definition(s)
// - Handler function(s)
// - Associated service if needed (max one service per module)
type CommandModule interface {
	// Register adds the module's commands to the provided map
	Register(commands map[string]*Command, deps *Dependencies)
	
	// GetService returns the service that needs session initialization
	// Returns nil if the module has no service requiring initialization
	GetService() ModuleService
}

// Dependencies contains shared dependencies that command modules may need
type Dependencies struct {
	Config     *config.Config
	DB         *database.DB
	IGDBClient *igdb.Client
	Session    *discordgo.Session // Set after bot initialization
}
