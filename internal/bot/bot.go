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
	"gamerpal/internal/config"
	"gamerpal/internal/events"
	"gamerpal/internal/scheduler"
)

// Bot represents the Discord bot
type Bot struct {
	session      *discordgo.Session
	config       *config.Config
	slashHandler *commands.SlashHandler
	scheduler    *scheduler.Scheduler
}

// New creates a new Bot instance
func New(cfg *config.Config) (*Bot, error) {
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.GetBotToken())
	if err != nil {
		return nil, fmt.Errorf("error creating Discord session: %w", err)
	}

	// Create command handler
	handler := commands.NewSlashHandler(cfg)

	bot := &Bot{
		session:      session,
		config:       cfg,
		slashHandler: handler,
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

	return bot, nil
}

// Start starts the bot
func (b *Bot) Start() error {
	// Open connection
	err := b.session.Open()
	if err != nil {
		return fmt.Errorf("error opening Discord connection: %w", err)
	}
	defer b.session.Close()

	// Set bot status to "initializing"
	if err := b.session.UpdateGameStatus(0, "Rolling out of bed..."); err != nil {
		b.config.Logger.Warn("error updating bot status:", err)
	}

	// Register slash commands
	if err := b.slashHandler.RegisterCommands(b.session); err != nil {
		return fmt.Errorf("error registering commands: %w", err)
	}

	// Initialize pairing service with session
	b.slashHandler.InitializePairingService(b.session)

	// Create and initialize scheduler
	b.scheduler = scheduler.NewScheduler(b.session, b.config, b.slashHandler.GetDB(), b.slashHandler.PairingService)

	// Start the scheduler
	b.scheduler.StartMinuteScheduler()
	defer b.scheduler.StopMinuteScheduler()

	// Start the hourly scheduler
	b.scheduler.StartHourScheduler()
	defer b.scheduler.StopHourScheduler()

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
		b.slashHandler.UnregisterCommands(b.session)
	}

	return nil
}

// onReady handles the ready event
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.config.Logger.Infof("Bot received ready signal! Logged in as: %s#%s\n", r.User.Username, r.User.Discriminator)

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
	if i.ApplicationCommandData().Name != "" {
		b.slashHandler.HandleInteraction(s, i)
	}
}
