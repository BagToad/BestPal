package bot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"

	"gamerpal/internal/agentengine"
	"gamerpal/internal/commands"
	"gamerpal/internal/commands/modules/agentadapter"
	"gamerpal/internal/commands/modules/intro"
	nineteeneightyfour "gamerpal/internal/commands/modules/nineteeneightyfour"
	"gamerpal/internal/commands/modules/scamguard"
	"gamerpal/internal/commands/modules/tts"
	"gamerpal/internal/config"
	"gamerpal/internal/events"
	"gamerpal/internal/scheduler"
)

// Bot represents the Discord bot
type Bot struct {
	session              *discordgo.Session
	config               *config.Config
	commandModuleHandler *commands.ModuleHandler
	scheduler            *scheduler.Scheduler
	agent                *agentengine.Agent
	ready                atomic.Bool // guards interaction handling until startup completes
}

// New creates a new Bot instance
func New(cfg *config.Config) (*Bot, error) {
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.GetBotToken())
	if err != nil {
		return nil, fmt.Errorf("error creating Discord session: %w", err)
	}

	// Create modular command handler
	handler := commands.NewModuleHandler(cfg, session)

	bot := &Bot{
		session:              session,
		config:               cfg,
		commandModuleHandler: handler,
	}

	// The LLM tool-calling agent is a command module (agentadapter); grab the
	// constructed instance for the cross-cutting wiring bot.go owns: injecting
	// other modules' tools, @mention handling, and the Copilot CLI lifecycle.
	// Feature modules contribute tools via the optional AgentTools() method on
	// CommandModule. If the Copilot CLI fails to start later in Start(), the
	// agent disables itself and the bot keeps running without it.
	if am, ok := handler.GetModule("agentadapter").(*agentadapter.Module); ok {
		bot.agent = am.Agent()
	}
	if bot.agent != nil {
		bot.agent.AddTools(handler.CollectAgentTools()...)
	}

	// Collect module-declared config settings (plus the core provider) into a
	// registry, then make it available for per-guild reads and the config panel.
	// Done after modules exist; all per-guild reads happen later during
	// event/interaction handling, so the registry is always populated before
	// first use.
	cfg.ApplyRegistry(handler.CollectConfigSettings())

	// mark not ready yet (zero value false, explicit for clarity)
	bot.ready.Store(false)

	// Set intents - we need guild, member, message, message content, direct message, message reaction, and guild scheduled event intents
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMembers | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent | discordgo.IntentDirectMessages | discordgo.IntentsGuildMessageReactions | discordgo.IntentsGuildScheduledEvents | discordgo.IntentsGuildVoiceStates

	// Enable per-channel message caching so the 1984 module can show the
	// "before" version of edited and deleted messages. discordgo populates
	// MessageUpdate.BeforeUpdate / MessageDelete.BeforeDelete from this cache.
	if session.State != nil {
		session.State.MaxMessageCount = 500
	}

	// Add event handlers
	session.AddHandler(bot.onReady)

	// Slash commands
	session.AddHandler(bot.onInteractionCreate)

	// Other events
	// Wrapped with anonymous function to pass config and (optional) agent.
	session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		events.OnMessageCreate(s, m, cfg, bot.agent)
	})

	// 1984 module - surveillance/audit logging across all channels.
	if mod, ok := handler.GetModule("1984").(*nineteeneightyfour.Module); ok {
		session.AddHandler(mod.OnMessageCreate)
		session.AddHandler(mod.OnMessageUpdate)
		session.AddHandler(mod.OnMessageDelete)
		session.AddHandler(mod.OnMessageReactionAdd)
		session.AddHandler(mod.OnMessageReactionRemove)
		session.AddHandler(mod.OnChannelCreate)
		session.AddHandler(mod.OnChannelUpdate)
		// Raw event handler catches gateway events that discordgo doesn't
		// model as typed events (e.g. VOICE_CHANNEL_STATUS_UPDATE).
		session.AddHandler(mod.OnRawEvent)
	}

	// scamguard module - perceptual-hash scam image detection.
	if mod, ok := handler.GetModule("scamguard").(*scamguard.Module); ok {
		session.AddHandler(mod.OnMessageCreate)
	}

	// tts module - drives the voice handshake from the main gateway's voice
	// server / state updates (discordgo's own voice path is unusable).
	if mod, ok := handler.GetModule("tts").(*tts.Module); ok {
		session.AddHandler(mod.OnVoiceServerUpdate)
		session.AddHandler(mod.OnVoiceStateUpdate)
	}
	session.AddHandler(func(s *discordgo.Session, c *discordgo.ChannelUpdate) {
		events.OnChannelUpdate(s, c, cfg)
	})
	session.AddHandler(func(s *discordgo.Session, c *discordgo.ChannelCreate) {
		events.HandleVoicePermissionSyncCreate(s, c, cfg)
	})
	session.AddHandler(func(s *discordgo.Session, r *discordgo.GuildMemberAdd) {
		events.OnGuildMemberAdd(s, r, cfg)
	})
	session.AddHandler(func(s *discordgo.Session, e *discordgo.GuildScheduledEventCreate) {
		events.OnGuildScheduledEventCreate(s, e, cfg)
	})

	// Forum thread lifecycle events wired into cache service and intro feed
	session.AddHandler(func(s *discordgo.Session, e *discordgo.ThreadCreate) {
		handler.GetForumCache().OnThreadCreate(s, e)
		// Forward new intro threads to the feed channel
		if e.NewlyCreated {
			if introMod, ok := handler.GetModule("intro").(*intro.Module); ok {
				if feedService := introMod.GetFeedService(); feedService != nil {
					feedService.HandleNewIntroThread(e.Channel)
				}
			}
		}
	})
	session.AddHandler(func(s *discordgo.Session, e *discordgo.ThreadUpdate) {
		handler.GetForumCache().OnThreadUpdate(s, e)
	})
	session.AddHandler(func(s *discordgo.Session, e *discordgo.ThreadDelete) {
		handler.GetForumCache().OnThreadDelete(s, e)
	})
	session.AddHandler(func(s *discordgo.Session, e *discordgo.ThreadListSync) {
		handler.GetForumCache().OnThreadListSync(s, e)
	})

	// Reaction events (used by Connect 4 and other reaction-based features)
	session.AddHandler(func(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
		handler.HandleReactionAdd(s, r)
	})
	session.AddHandler(func(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
		handler.HandleReactionRemove(s, r)
	})

	return bot, nil
}

// Start starts the bot
func (b *Bot) Start() error {
	// Start the LLM agent's Copilot CLI subprocess (best-effort). If this
	// fails, the agent is left disabled and the bot continues without it.
	if b.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := b.agent.Start(ctx); err != nil {
			b.config.Logger.Warnf("agent: start failed, continuing without it: %v", err)
		}
		cancel()
		defer b.agent.Stop()
	}

	// Open connection
	err := b.session.Open()
	if err != nil {
		return fmt.Errorf("error opening Discord connection: %w", err)
	}
	defer func() {
		if err := b.session.Close(); err != nil {
			b.config.Logger.Warn("error closing Discord session:", err)
		}
	}()

	// Set bot status to "initializing"
	if err := b.session.UpdateGameStatus(0, "Rolling out of bed..."); err != nil {
		b.config.Logger.Warn("error updating bot status:", err)
	}

	// Register slash commands
	if err := b.commandModuleHandler.RegisterCommands(b.session); err != nil {
		return fmt.Errorf("error registering commands: %w", err)
	}

	// Register forums with cache service (from config)
	if introForum := b.config.GetGamerPalsIntroductionsForumChannelID(); introForum != "" {
		b.commandModuleHandler.GetForumCache().RegisterForum(introForum)
	}
	if lfgForum := b.config.GetGamerPalsLFGForumChannelID(); lfgForum != "" {
		b.commandModuleHandler.GetForumCache().RegisterForum(lfgForum)
	}

	// Initialize module services that need the Discord session
	if err := b.commandModuleHandler.InitializeModuleServices(b.session); err != nil {
		return fmt.Errorf("error initializing module services: %w", err)
	}

	// Create and initialize scheduler
	b.scheduler = scheduler.NewScheduler(b.session, b.config, b.commandModuleHandler.GetDB())

	// Register recurring tasks declared by modules (including the agent module's
	// brain refresh) with the scheduler.
	b.commandModuleHandler.RegisterModuleSchedulers(b.scheduler)

	// Register config log rotation (not part of a module)
	if err := b.scheduler.RegisterFunc("@hourly", "log-rotation", func() error {
		return b.config.RotateAndPruneLogs()
	}); err != nil {
		b.config.Logger.Errorf("Failed to register log rotation: %v", err)
	}

	b.scheduler.Start()
	defer b.scheduler.Stop()

	// Update status to indicate the bot is awake
	if err := b.session.UpdateGameStatus(0, "OK OK I'm awake!"); err != nil {
		b.config.Logger.Warn("error updating bot status:", err)
	}

	// Signal readiness after all initialization steps complete.
	b.ready.Store(true)
	b.config.Logger.Info("Initialization complete; interactions enabled")
	b.config.Logger.Info("GamerPal bot is now running. Press CTRL+C to exit.")

	// Wait for interrupt signal
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Cleanup: Unregister commands, optionally
	if os.Getenv("UNREGISTER_COMMANDS") == "true" {
		b.commandModuleHandler.UnregisterCommands(b.session)
	}

	return nil
}

// onReady handles the ready event
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.config.Logger.Infof("Bot received ready signal! Logged in as: %s#%s\n", r.User.Username, r.User.Discriminator)

	// Preload registered forums into generic cache (best-effort)
	go func() {
		guildID := b.config.GetGamerPalsServerID()
		if guildID == "" {
			return
		}
		if introForum := b.config.GetGamerPalsIntroductionsForumChannelID(); introForum != "" {
			if err := b.commandModuleHandler.GetForumCache().RefreshForum(guildID, introForum); err != nil {
				b.config.Logger.Warnf("Intro forum preload failed: %v", err)
			} else {
				b.config.Logger.Infof("Intro forum preload complete")
			}
		}
		if lfgForum := b.config.GetGamerPalsLFGForumChannelID(); lfgForum != "" {
			if err := b.commandModuleHandler.GetForumCache().RefreshForum(guildID, lfgForum); err != nil {
				b.config.Logger.Warnf("LFG forum preload failed: %v", err)
			} else {
				b.config.Logger.Infof("LFG forum preload complete")
			}
		}
	}()

	// Set bot status to something fresh every hour
	c := time.NewTicker(time.Hour)
	go func() {
		for range c.C {
			err := s.UpdateGameStatus(0, b.randomStatus())
			if err != nil {
				b.config.Logger.Warn("Error setting status:", err)
			}
		}
	}()
}

func (b *Bot) randomStatus() string {
	randomStuff := []string{
		"Helping gamers connect!",
		"Use /help for commands",
		"Destroying evil...",
		"Counting bits and bytes...",
		"Trying not to cry...",
		"Join r/GamerPals!",
		"Trying to delete myself...",
		"Making friends...",
		"Eating bugs...",
	}

	return randomStuff[rand.IntN(len(randomStuff))]
}

// onInteractionCreate handles slash command interactions
func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Initialization guard: reject interactions until startup has completed.
	if !b.ready.Load() {
		// Use the correct response type per interaction.
		switch i.Type {
		case discordgo.InteractionApplicationCommand, discordgo.InteractionMessageComponent, discordgo.InteractionModalSubmit:
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "⏳ Bot is starting up, try again in a few seconds.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
		case discordgo.InteractionApplicationCommandAutocomplete:
			// Autocomplete must return an autocomplete result type, empty list is fine while starting up.
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionApplicationCommandAutocompleteResult,
				Data: &discordgo.InteractionResponseData{Choices: []*discordgo.ApplicationCommandOptionChoice{}},
			})
		case discordgo.InteractionPing:
			// Reply with a Pong to satisfy handshake, though this is rare here.
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponsePong})
		default:
			// Fallback: generic ephemeral message.
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "⏳ Bot is starting up, try again shortly.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
		}
		return
	}
	// Slash commands
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "" {
			b.commandModuleHandler.HandleInteraction(s, i)
		}
		return
	}
	// Component interactions
	if i.Type == discordgo.InteractionMessageComponent {
		b.commandModuleHandler.HandleComponentInteraction(s, i)
		return
	}
	// Modal submit
	if i.Type == discordgo.InteractionModalSubmit {
		b.commandModuleHandler.HandleModalSubmit(s, i)
		return
	}
	// Autocomplete
	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		b.commandModuleHandler.HandleAutocomplete(s, i)
		return
	}
}
