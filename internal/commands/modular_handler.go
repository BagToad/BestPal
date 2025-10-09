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
	// Phase 1: Already migrated
	// Register ping module
	pingModule := &ping.Module{}
	pingModule.Register(h.commands, h.deps)

	// Register time module
	timeModule := &time.Module{}
	timeModule.Register(h.commands, h.deps)

	// Register say module (stores reference for scheduler access)
	h.sayModule = &say.Module{}
	h.sayModule.Register(h.commands, h.deps)

	// Phase 2: Simple commands
	// Register help module
	helpModule := &help.Module{}
	helpModule.Register(h.commands, h.deps)

	// Register intro module
	introModule := &intro.Module{}
	introModule.Register(h.commands, h.deps)

	// Register config module
	configModule := &config.Module{}
	configModule.Register(h.commands, h.deps)

	// Register refresh-igdb module (store reference to update client)
	h.refreshIgdbModule = &refreshigdb.Module{}
	h.refreshIgdbModule.SetIGDBClientRef(&h.igdbClient)
	h.refreshIgdbModule.Register(h.commands, h.deps)

	// Phase 3: Medium complexity
	// Register game module
	gameModule := &game.Module{}
	gameModule.Register(h.commands, h.deps)

	// Register userstats module
	userstatsModule := &userstats.Module{}
	userstatsModule.Register(h.commands, h.deps)

	// Register log module
	logModule := &log.Module{}
	logModule.Register(h.commands, h.deps)

	// Phase 4: Complex with services
	// Register prune module
	pruneModule := &prune.Module{}
	pruneModule.Register(h.commands, h.deps)

	// Register LFG module (store reference for component/modal handling)
	h.lfgModule = &lfg.Module{}
	h.lfgModule.Register(h.commands, h.deps)

	// Register roulette module (store reference for pairing service)
	h.rouletteModule = &roulette.Module{}
	h.rouletteModule.Register(h.commands, h.deps)
}

// GetDB returns the database instance (used by scheduler)
func (h *ModularHandler) GetDB() *database.DB {
	return h.db
}

// GetSayService returns the say service for scheduler use
func (h *ModularHandler) GetSayService() *say.Service {
	if h.sayModule != nil {
		return h.sayModule.GetService()
	}
	return nil
}

// GetPairingService returns the pairing service for scheduler use
func (h *ModularHandler) GetPairingService() *roulette.Module {
	return h.rouletteModule
}

// InitializePairingService initializes the pairing service with a Discord session
func (h *ModularHandler) InitializePairingService(session *discordgo.Session) {
	// Initialize pairing service in roulette module
	if h.rouletteModule != nil {
		h.rouletteModule.InitializePairingService(session)
	}
	// Also set session in dependencies for future modules
	h.deps.Session = session
}

// RegisterCommands registers all slash commands with Discord
func (h *ModularHandler) RegisterCommands(s *discordgo.Session) error {
	// Get all existing commands from Discord
	existingCommands, err := s.ApplicationCommands(s.State.User.ID, "")
	if err != nil {
		h.config.Logger.Warn("Error fetching existing commands: %v", err)
		return err
	}

	// Index existing by name for quick lookup
	existingByName := make(map[string]*discordgo.ApplicationCommand)
	for _, ec := range existingCommands {
		existingByName[ec.Name] = ec
	}

	for _, c := range h.commands {
		// If a command is in development, we're not only going to skip it, but we'll
		// also unregister it if it exists.
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
			// Edit existing command (preserves ID so clients update faster)
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

// UnregisterCommands removes all registered commands (useful for cleanup)
func (h *ModularHandler) UnregisterCommands(s *discordgo.Session) {
	// Get all existing commands from Discord
	existingCommands, err := s.ApplicationCommands(s.State.User.ID, "")
	if err != nil {
		h.config.Logger.Warn("Error fetching existing commands: %v", err)
		return
	}

	// Delete each command that exists in our local command map
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
