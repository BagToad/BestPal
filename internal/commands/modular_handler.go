package commands

import (
	"gamerpal/internal/commands/modules/ping"
	"gamerpal/internal/commands/modules/say"
	"gamerpal/internal/commands/modules/time"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/pairing"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// ModularHandler is the new modular command handler
type ModularHandler struct {
	commands map[string]*types.Command
	config   *config.Config
	db       *database.DB
	deps     *types.Dependencies

	// Module instances that need to be accessed externally
	sayModule *say.Module

	// Legacy services that haven't been migrated yet
	PairingService *pairing.PairingService
}

// NewModularHandler creates a new modular command handler
func NewModularHandler(cfg *config.Config) *ModularHandler {
	// Create IGDB client
	igdbClient := igdb.NewClient(cfg.GetIGDBClientID(), cfg.GetIGDBClientToken(), nil)

	// Initialize SQLite database
	db, err := database.NewDB(cfg.GetDatabasePath())
	if err != nil {
		cfg.Logger.Warn("Warning: Failed to initialize database: %v", err)
		// Continue without database for now
	}

	h := &ModularHandler{
		commands: make(map[string]*types.Command),
		config:   cfg,
		db:       db,
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
	// Register ping module
	pingModule := &ping.Module{}
	pingModule.Register(h.commands, h.deps)

	// Register time module
	timeModule := &time.Module{}
	timeModule.Register(h.commands, h.deps)

	// Register say module (stores reference for scheduler access)
	h.sayModule = &say.Module{}
	h.sayModule.Register(h.commands, h.deps)
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

// InitializePairingService initializes the pairing service with a Discord session
func (h *ModularHandler) InitializePairingService(session *discordgo.Session) {
	h.PairingService = pairing.NewPairingService(session, h.config, h.db)
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
