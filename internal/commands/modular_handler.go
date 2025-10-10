package commands

import (
	"gamerpal/internal/commands/modules/config"
	"gamerpal/internal/commands/modules/game"
	"gamerpal/internal/commands/modules/help"
	"gamerpal/internal/commands/modules/intro"
	"gamerpal/internal/commands/modules/lfg"
	"gamerpal/internal/commands/modules/log"
	"gamerpal/internal/commands/modules/ping"
	"gamerpal/internal/commands/modules/prune"
	"gamerpal/internal/commands/modules/refreshigdb"
	"gamerpal/internal/commands/modules/roulette"
	"gamerpal/internal/commands/modules/say"
	"gamerpal/internal/commands/modules/time"
	"gamerpal/internal/commands/modules/userstats"
	"gamerpal/internal/commands/types"
	internalConfig "gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// ModularHandler is the new modular command handler
type ModularHandler struct {
	commands   map[string]*types.Command
	config     *internalConfig.Config
	db         *database.DB
	deps       *types.Dependencies
	igdbClient *igdb.Client

	// Module instances that need to be accessed externally
	sayModule         *say.Module
	refreshIgdbModule *refreshigdb.Module
	lfgModule         *lfg.Module
	rouletteModule    *roulette.Module
}

// NewModularHandler creates a new modular command handler
func NewModularHandler(cfg *internalConfig.Config) *ModularHandler {
	// Create IGDB client
	igdbClient := igdb.NewClient(cfg.GetIGDBClientID(), cfg.GetIGDBClientToken(), nil)

	// Initialize SQLite database
	db, err := database.NewDB(cfg.GetDatabasePath())
	if err != nil {
		cfg.Logger.Warn("Warning: Failed to initialize database: %v", err)
		// Continue without database for now
	}

	h := &ModularHandler{
		commands:   make(map[string]*types.Command),
		config:     cfg,
		db:         db,
		igdbClient: igdbClient,
		deps: &types.Dependencies{
			Config:     cfg,
			DB:         db,
			IGDBClient: igdbClient,
			Session:    nil, // Will be set later
		},
	}

	// Register modular commands
	h.registerModules()

	return h
}

// registerModules registers all command modules
func (h *ModularHandler) registerModules() {
	pingModule := &ping.Module{}
	pingModule.Register(h.commands, h.deps)

	timeModule := &time.Module{}
	timeModule.Register(h.commands, h.deps)

	// Store reference for scheduler access
	h.sayModule = &say.Module{}
	h.sayModule.Register(h.commands, h.deps)

	helpModule := &help.Module{}
	helpModule.Register(h.commands, h.deps)

	introModule := &intro.Module{}
	introModule.Register(h.commands, h.deps)

	configModule := &config.Module{}
	configModule.Register(h.commands, h.deps)

	// Store reference to update IGDB client
	h.refreshIgdbModule = &refreshigdb.Module{}
	h.refreshIgdbModule.SetIGDBClientRef(&h.igdbClient)
	h.refreshIgdbModule.Register(h.commands, h.deps)

	gameModule := &game.Module{}
	gameModule.Register(h.commands, h.deps)

	userstatsModule := &userstats.Module{}
	userstatsModule.Register(h.commands, h.deps)

	logModule := &log.Module{}
	logModule.Register(h.commands, h.deps)

	pruneModule := &prune.Module{}
	pruneModule.Register(h.commands, h.deps)

	// Store reference for component/modal handling
	h.lfgModule = &lfg.Module{}
	h.lfgModule.Register(h.commands, h.deps)

	// Store reference for pairing service access
	h.rouletteModule = &roulette.Module{}
	h.rouletteModule.Register(h.commands, h.deps)
}

// GetDB returns the database instance
func (h *ModularHandler) GetDB() *database.DB {
	return h.db
}

// GetSayService returns the say service for scheduler access
func (h *ModularHandler) GetSayService() *say.Service {
	if h.sayModule != nil {
		return h.sayModule.GetService()
	}
	return nil
}

// GetPairingService returns the roulette module for pairing service access
func (h *ModularHandler) GetPairingService() *roulette.Module {
	return h.rouletteModule
}

// RegisterCommands registers all slash commands with Discord
func (h *ModularHandler) RegisterCommands(s *discordgo.Session) error {
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
		// Unregister development commands if they exist
		if c.Development {
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
			// Update existing command
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

// HandleInteraction routes slash command interactions
func (h *ModularHandler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name == "" {
		return
	}

	commandName := i.ApplicationCommandData().Name
	if cmd, exists := h.commands[commandName]; exists {
		cmd.HandlerFunc(s, i)
	}
}

// UnregisterCommands removes all registered commands
func (h *ModularHandler) UnregisterCommands(s *discordgo.Session) {
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

// HandleLFGComponent delegates to LFG module for component interactions
func (h *ModularHandler) HandleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if h.lfgModule != nil {
		h.lfgModule.HandleComponent(s, i)
	} else {
		h.config.Logger.Warn("LFG component interaction received but LFG module not initialized")
	}
}

// HandleLFGModalSubmit delegates to LFG module for modal submissions
func (h *ModularHandler) HandleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if h.lfgModule != nil {
		h.lfgModule.HandleModalSubmit(s, i)
	} else {
		h.config.Logger.Warn("LFG modal submit received but LFG module not initialized")
	}
}
