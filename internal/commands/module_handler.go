package commands

import (
	"fmt"
	"gamerpal/internal/commands/modules/config"
	"gamerpal/internal/commands/modules/game"
	"gamerpal/internal/commands/modules/help"
	"gamerpal/internal/commands/modules/intro"
	"gamerpal/internal/commands/modules/lfg"
	"gamerpal/internal/commands/modules/log"
	"gamerpal/internal/commands/modules/ping"
	"gamerpal/internal/commands/modules/poll"
	"gamerpal/internal/commands/modules/prune"
	"gamerpal/internal/commands/modules/refreshigdb"
	"gamerpal/internal/commands/modules/roulette"
	"gamerpal/internal/commands/modules/say"
	"gamerpal/internal/commands/modules/status"
	"gamerpal/internal/commands/modules/time"
	"gamerpal/internal/commands/modules/userstats"
	"gamerpal/internal/commands/modules/welcome"
	"gamerpal/internal/commands/types"
	internalConfig "gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// ModuleHandler manages command modules and routes interactions.
//
// External Access Requirements:
// Some modules need to be accessed from outside the command system:
//
// 1. Say Module - Accessed by scheduler (bot.go:124-131) for scheduled messages
// 2. Roulette Module - Accessed by scheduler (bot.go:111-122) for automated pairing
// 3. LFG Module - Accessed by bot event handler (bot.go:216-224) for modal/component interactions
// 4. Welcome Module - Accessed by scheduler (bot.go) for new member welcoming
//
// These are accessed via GetModule[T]() for type-safe access.
type ModuleHandler struct {
	commands   map[string]*types.Command
	modules    map[string]types.CommandModule
	config     *internalConfig.Config
	db         *database.DB
	deps       *types.Dependencies
	igdbClient *igdb.Client
}

// NewModuleHandler creates a new module-based command handler
func NewModuleHandler(cfg *internalConfig.Config) *ModuleHandler {
	igdbClient := igdb.NewClient(cfg.GetIGDBClientID(), cfg.GetIGDBClientToken(), nil)

	db, err := database.NewDB(cfg.GetDatabasePath())
	if err != nil {
		cfg.Logger.Warn("Warning: Failed to initialize database: %v", err)
	}

	h := &ModuleHandler{
		commands:   make(map[string]*types.Command),
		modules:    make(map[string]types.CommandModule),
		config:     cfg,
		db:         db,
		igdbClient: igdbClient,
		deps: &types.Dependencies{
			Config:     cfg,
			DB:         db,
			IGDBClient: igdbClient,
			Session:    nil, // Set later
		},
	}

	h.registerModules()

	return h
}

// registerModules registers all command modules
func (h *ModuleHandler) registerModules() {
	// Define modules with their constructors and names
	modules := []struct {
		name   string
		module types.CommandModule
	}{
		{"ping", ping.New(h.deps)},
		{"time", time.New(h.deps)},
		{"say", say.New(h.deps)},
		{"help", help.New(h.deps)},
		{"intro", intro.New(h.deps)},
		{"config", config.New(h.deps)},
		{"refreshigdb", refreshigdb.New(h.deps)},
		{"game", game.New(h.deps)},
		{"userstats", userstats.New(h.deps)},
		{"log", log.New(h.deps)},
		{"prune", prune.New(h.deps)},
		{"lfg", lfg.New(h.deps)},
		{"roulette", roulette.New(h.deps)},
		{"welcome", welcome.New(h.deps)},
		{"poll", poll.New(h.deps)},
		{"status", status.New(h.deps)},
	}

	for _, m := range modules {
		// Special handling for refreshigdb module to update IGDB client
		if m.name == "refreshigdb" {
			if rm, ok := m.module.(*refreshigdb.RefreshigdbModule); ok {
				rm.SetIGDBClientRef(&h.igdbClient)
			}
		}

		m.module.Register(h.commands, h.deps)
		h.modules[m.name] = m.module
	}
}

// GetModule returns a module by name with type assertion.
// This is used for external access (scheduler, bot event handlers).
//
// Example usage:
//
//	sayMod, ok := handler.GetModule("say").(*say.SayModule)
func (h *ModuleHandler) GetModule(name string) types.CommandModule {
	return h.modules[name]
}

// GetDB returns the database instance
func (h *ModuleHandler) GetDB() *database.DB {
	return h.db
}

// RegisterCommands registers all slash commands with Discord
func (h *ModuleHandler) RegisterCommands(s *discordgo.Session) error {
	existingCommands, err := s.ApplicationCommands(s.State.User.ID, "")
	if err != nil {
		h.config.Logger.Warn("Error fetching existing commands: %v", err)
		return err
	}

	existingByName := make(map[string]*discordgo.ApplicationCommand)
	for _, ec := range existingCommands {
		existingByName[ec.Name] = ec
	}

	for _, c := range h.commands {
		if c.Development {
			// Unregister development commands if they exist
			for _, existingCmd := range existingCommands {
				if existingCmd.Name == c.ApplicationCommand.Name {
					err := s.ApplicationCommandDelete(s.State.User.ID, "", existingCmd.ID)
					if err != nil {
						h.config.Logger.Warn("Error deleting command %s: %v", c.ApplicationCommand.Name, err)
					} else {
						h.config.Logger.Infof("Unregistered command: %s", c.ApplicationCommand.Name)
					}
				}
			}
			continue
		}

		if existing := existingByName[c.ApplicationCommand.Name]; existing != nil {
			cmd, err := s.ApplicationCommandEdit(s.State.User.ID, "", existing.ID, c.ApplicationCommand)
			if err != nil {
				return err
			}
			c.ApplicationCommand.ID = cmd.ID
			h.config.Logger.Infof("Updated command: %s", cmd.Name)
		} else {
			cmd, err := s.ApplicationCommandCreate(s.State.User.ID, "", c.ApplicationCommand)
			if err != nil {
				return err
			}
			c.ApplicationCommand.ID = cmd.ID
			h.config.Logger.Infof("Registered command: %s", cmd.Name)
		}
	}

	return nil
}

// HandleInteraction routes slash command interactions to appropriate handlers
func (h *ModuleHandler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name == "" {
		return
	}

	commandName := i.ApplicationCommandData().Name
	if cmd, exists := h.commands[commandName]; exists {
		cmd.HandlerFunc(s, i)
	}
}

// HandleComponentInteraction routes component interactions to appropriate module handlers
func (h *ModuleHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Currently only LFG module uses component interactions
	if lfgMod, ok := h.GetModule("lfg").(*lfg.LfgModule); ok {
		lfgMod.HandleComponent(s, i)
	} else {
		h.config.Logger.Warn("Component interaction received but LFG module not available")
	}
}

// HandleModalSubmit routes modal submissions to appropriate module handlers
func (h *ModuleHandler) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Currently only LFG module uses modal submissions
	if lfgMod, ok := h.GetModule("lfg").(*lfg.LfgModule); ok {
		lfgMod.HandleModalSubmit(s, i)
	} else {
		h.config.Logger.Warn("Modal submit received but LFG module not available")
	}
}

// HandleAutocomplete routes autocomplete requests to appropriate module handlers
func (h *ModuleHandler) HandleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check which command is being autocompleted
	commandName := i.ApplicationCommandData().Name

	// Currently only game-thread command (in LFG module) uses autocomplete
	if commandName == "game-thread" {
		if lfgMod, ok := h.GetModule("lfg").(*lfg.LfgModule); ok {
			lfgMod.HandleAutocomplete(s, i)
		} else {
			h.config.Logger.Warn("Autocomplete received for game-thread but LFG module not available")
		}
	}
}

// UnregisterCommands removes all registered commands
func (h *ModuleHandler) UnregisterCommands(s *discordgo.Session) {
	existingCommands, err := s.ApplicationCommands(s.State.User.ID, "")
	if err != nil {
		h.config.Logger.Warn("Error fetching existing commands: %v", err)
		return
	}

	for _, existingCmd := range existingCommands {
		if _, exists := h.commands[existingCmd.Name]; exists {
			err := s.ApplicationCommandDelete(s.State.User.ID, "", existingCmd.ID)
			if err != nil {
				h.config.Logger.Warn("Error deleting command %s: %v", existingCmd.Name, err)
			} else {
				h.config.Logger.Infof("Unregistered command: %s", existingCmd.Name)
			}
		}
	}
}

// InitializeModuleServices hydrates services with the Discord session.
// Called after the Discord session is established.
func (h *ModuleHandler) InitializeModuleServices(s *discordgo.Session) error {
	// Update dependencies with session
	h.deps.Session = s

	// Hydrate services for all modules with the Discord session
	for _, module := range h.modules {
		if service := module.Service(); service != nil {
			if err := service.HydrateServiceDiscordSession(s); err != nil {
				return fmt.Errorf("failed to hydrate service with Discord session: %w", err)
			}
		}
	}

	return nil
}

// RegisterModuleSchedulers registers recurring tasks from all modules with the scheduler.
// Called after services are initialized.
func (h *ModuleHandler) RegisterModuleSchedulers(scheduler interface {
	RegisterNewMinuteFunc(fn func() error)
	RegisterNewHourFunc(fn func() error)
}) {
	for _, module := range h.modules {
		if service := module.Service(); service != nil {
			// Register minute functions
			if minuteFuncs := service.MinuteFuncs(); minuteFuncs != nil {
				for _, fn := range minuteFuncs {
					scheduler.RegisterNewMinuteFunc(fn)
				}
			}

			// Register hour functions
			if hourFuncs := service.HourFuncs(); hourFuncs != nil {
				for _, fn := range hourFuncs {
					scheduler.RegisterNewHourFunc(fn)
				}
			}
		}
	}
}
