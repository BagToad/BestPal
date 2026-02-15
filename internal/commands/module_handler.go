package commands

import (
	"fmt"
	"strings"

	"gamerpal/internal/commands/modules/config"
	"gamerpal/internal/commands/modules/game"
	"gamerpal/internal/commands/modules/help"
	"gamerpal/internal/commands/modules/intro"
	"gamerpal/internal/commands/modules/lfg"
	"gamerpal/internal/commands/modules/log"
	"gamerpal/internal/commands/modules/ping"
	"gamerpal/internal/commands/modules/poll"
	"gamerpal/internal/commands/modules/pomo"
	"gamerpal/internal/commands/modules/prune"
	"gamerpal/internal/commands/modules/refreshigdb"
	"gamerpal/internal/commands/modules/roulette"
	"gamerpal/internal/commands/modules/say"
	"gamerpal/internal/commands/modules/snowball"
	"gamerpal/internal/commands/modules/status"
	"gamerpal/internal/commands/modules/time"
	"gamerpal/internal/commands/modules/userstats"
	"gamerpal/internal/commands/modules/welcome"
	"gamerpal/internal/commands/types"
	internalConfig "gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/forumcache"
	internalVoice "gamerpal/internal/voice"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// ModuleHandler manages command modules, routing interactions and exposing select modules externally.
type ModuleHandler struct {
	commands   map[string]*types.Command
	modules    map[string]types.CommandModule
	config     *internalConfig.Config
	db         *database.DB
	deps       *types.Dependencies
	igdbClient *igdb.Client
	VoiceMgr   *internalVoice.Manager
}

// NewModuleHandler creates a new module-based command handler
func NewModuleHandler(cfg *internalConfig.Config, session *discordgo.Session) *ModuleHandler {
	igdbClient := igdb.NewClient(cfg.GetIGDBClientID(), cfg.GetIGDBClientToken(), nil)

	db, err := database.NewDB(cfg.GetDatabasePath())
	if err != nil {
		cfg.Logger.Warn("Warning: Failed to initialize database: %v", err)
	}

	fc := forumcache.NewForumCacheService(cfg)
	if session != nil {
		fc.HydrateSession(session)
	}

	voiceMgr := internalVoice.NewManager()

	h := &ModuleHandler{
		commands:   make(map[string]*types.Command),
		modules:    make(map[string]types.CommandModule),
		config:     cfg,
		db:         db,
		igdbClient: igdbClient,
		VoiceMgr:   voiceMgr,
		deps: &types.Dependencies{
			Config:     cfg,
			DB:         db,
			IGDBClient: igdbClient,
			Session:    session,
			ForumCache: fc,
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
		{"snowball", snowball.New(h.deps)},
		{"welcome", welcome.New(h.deps)},
		{"poll", poll.New(h.deps)},
		{"status", status.New(h.deps)},
		{"pomo", pomo.New(h.deps, h.VoiceMgr)},
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

// GetForumCache exposes the forum cache service for event handlers.
func (h *ModuleHandler) GetForumCache() *forumcache.Service { return h.deps.ForumCache }

// RegisterCommands registers all slash commands with Discord using a single bulk overwrite call.
// BulkOverwrite replaces the full command set atomically — any commands not in the list
// (including development-only commands) are automatically removed by Discord.
func (h *ModuleHandler) RegisterCommands(s *discordgo.Session) error {
	// Collect all production commands for a single bulk overwrite.
	var cmds []*discordgo.ApplicationCommand
	for _, c := range h.commands {
		if !c.Development {
			cmds = append(cmds, c.ApplicationCommand)
		}
	}

	registered, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", cmds)
	if err != nil {
		return fmt.Errorf("bulk command registration failed: %w", err)
	}

	// Map returned IDs back to the internal command map.
	for _, rc := range registered {
		if c, ok := h.commands[rc.Name]; ok {
			c.ApplicationCommand.ID = rc.ID
		}
	}
	h.config.Logger.Infof("Registered %d commands (bulk overwrite)", len(registered))

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
// based on customID prefix.
func (h *ModuleHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cid := i.MessageComponentData().CustomID

	switch {
	case strings.HasPrefix(cid, "lfg_"):
		if lfgMod, ok := h.GetModule("lfg").(*lfg.LfgModule); ok {
			lfgMod.HandleComponent(s, i)
		} else {
			h.config.Logger.Warn("Component interaction received but LFG module not available")
		}
	case strings.HasPrefix(cid, "pomo::"):
		if pomoMod, ok := h.GetModule("pomo").(*pomo.PomoModule); ok {
			pomoMod.HandleComponent(s, i)
		} else {
			h.config.Logger.Warn("Component interaction received but pomo module not available")
		}
	default:
		h.config.Logger.Warnf("Unhandled component interaction with customID: %s", cid)
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
	h.deps.Session = s
	if h.deps.ForumCache != nil {
		h.deps.ForumCache.HydrateSession(s)
	}

	// Initialize voice bridge with the live session
	h.VoiceMgr.SetSession(s)

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
	RegisterFunc(schedule, name string, fn func() error) error
}) {
	for _, module := range h.modules {
		if service := module.Service(); service != nil {
			// Register scheduled functions
			if scheduledFuncs := service.ScheduledFuncs(); scheduledFuncs != nil {
				for schedule, fn := range scheduledFuncs {
					// Name is used for logging purposes only
					name := fmt.Sprintf("%T", service)
					if err := scheduler.RegisterFunc(schedule, name, fn); err != nil {
						h.config.Logger.Errorf("Failed to register scheduled function: %v", err)
					}
				}
			}
		}
	}
}
