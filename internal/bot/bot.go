package bot

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"

	"gamerpal/internal/commands"
	"gamerpal/internal/commands/modules/lfg"
	"gamerpal/internal/config"
	"gamerpal/internal/events"
	"gamerpal/internal/scheduler"
	"gamerpal/internal/welcome"
)

// Bot represents the Discord bot
type Bot struct {
	session             *discordgo.Session
	config              *config.Config
	slashCommandHandler *commands.ModularHandler
	scheduler           *scheduler.Scheduler
}

// New creates a new Bot instance
func New(cfg *config.Config) (*Bot, error) {
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.GetBotToken())
	if err != nil {
		return nil, fmt.Errorf("error creating Discord session: %w", err)
	}

	// Create modular command handler
	handler := commands.NewModularHandler(cfg)

	bot := &Bot{
		session:             session,
		config:              cfg,
		slashCommandHandler: handler,
	}

	// Set intents - we need guild, member, message, and message content intents
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMembers | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent

	// Add event handlers
	session.AddHandler(bot.onReady)

	// Slash commands
	session.AddHandler(bot.onInteractionCreate)

	// Other events
	// Wrapped with anonymous function to pass config
	session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		events.OnMessageCreate(s, m, cfg)
	})
	session.AddHandler(func(s *discordgo.Session, c *discordgo.ChannelUpdate) {
		events.OnChannelUpdate(s, c, cfg)
	})
	session.AddHandler(func(s *discordgo.Session, r *discordgo.GuildMemberAdd) {
		events.OnGuildMemberAdd(s, r, cfg)
	})

	return bot, nil
}

// Start starts the bot
func (b *Bot) Start() error {
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
	if err := b.slashCommandHandler.RegisterCommands(b.session); err != nil {
		return fmt.Errorf("error registering commands: %w", err)
	}

	// Initialize pairing service in roulette module with session
	rouletteModule := b.slashCommandHandler.GetPairingService()
	if rouletteModule != nil {
		rouletteModule.InitializePairingService(b.session)
	}

	// Create and initialize scheduler
	b.scheduler = scheduler.NewScheduler(b.session, b.config, b.slashCommandHandler.GetDB())

	// Add services to scheduler
	welcomeService := welcome.NewWelcomeService(b.session, b.config)
	b.scheduler.RegisterNewMinuteFunc(func() error {
		// TODO: These services should return errors
		welcomeService.CleanNewPalsRoleFromOldMembers()
		welcomeService.CheckAndWelcomeNewPals()
		return nil
	})

	// Pairing service minute check (via roulette module)
	b.scheduler.RegisterNewMinuteFunc(func() error {
		rouletteModule := b.slashCommandHandler.GetPairingService()
		if rouletteModule != nil {
			pairingService := rouletteModule.GetPairingService()
			if pairingService != nil {
				// TODO: These services should return errors
				pairingService.CheckAndExecuteScheduledPairings()
			}
		}
		return nil
	})

	// Scheduled say service minute check
	b.scheduler.RegisterNewMinuteFunc(func() error {
		sayService := b.slashCommandHandler.GetSayService()
		if sayService != nil {
			return sayService.CheckAndSendDue(b.session)
		}
		return nil
	})

	b.scheduler.RegisterNewHourFunc(func() error {
		return b.config.RotateAndPruneLogs()
	})

	b.scheduler.Start()
	defer b.scheduler.Stop()

	// Update status to indicate the bot is awake
	if err := b.session.UpdateGameStatus(0, "OK OK I'm awake!"); err != nil {
		b.config.Logger.Warn("error updating bot status:", err)
	}

	b.config.Logger.Info("GamerPal bot is now running. Press CTRL+C to exit.")

	// Wait for interrupt signal
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Cleanup: Unregister commands, optionally
	if os.Getenv("UNREGISTER_COMMANDS") == "true" {
		b.slashCommandHandler.UnregisterCommands(b.session)
	}

	return nil
}

// onReady handles the ready event
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.config.Logger.Infof("Bot received ready signal! Logged in as: %s#%s\n", r.User.Username, r.User.Discriminator)

	// Preload LFG forum threads into cache (best-effort)
	go func() {
		forumID := b.config.GetGamerPalsLFGForumChannelID()
		if forumID == "" {
			return
		}
		// Use shared rebuild helper (includes archived threads)
		if total, active, archived, err := lfg.RebuildLFGThreadCacheWrapper(s, b.config.GetGamerPalsServerID(), forumID); err != nil {
			b.config.Logger.Warnf("LFG preload: %v", err)
		} else {
			b.config.Logger.Infof("LFG preload: cached %d threads (active=%d, archived=%d)", total, active, archived)
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
	}

	return randomStuff[rand.IntN(len(randomStuff))]
}

// onInteractionCreate handles slash command interactions
func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Slash commands
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "" {
			b.slashCommandHandler.HandleInteraction(s, i)
		}
		return
	}
	// Component interactions
	if i.Type == discordgo.InteractionMessageComponent {
		// Delegate to LFG handler (currently only component using)
		b.slashCommandHandler.HandleLFGComponent(s, i)
		return
	}
	// Modal submit
	if i.Type == discordgo.InteractionModalSubmit {
		b.slashCommandHandler.HandleLFGModalSubmit(s, i)
		return
	}
}
