package bot

import (
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"

	"gamerpal/internal/commands"
	"gamerpal/internal/config"
)

// Bot represents the Discord bot
type Bot struct {
	session *discordgo.Session
	config  *config.Config
	handler *commands.Handler
}

// New creates a new Bot instance
func New(cfg *config.Config) (*Bot, error) {
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("error creating Discord session: %w", err)
	}

	// Create command handler
	handler := commands.NewHandler(cfg)

	bot := &Bot{
		session: session,
		config:  cfg,
		handler: handler,
	}

	// Set intents - we need guild, member, message, and message content intents
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMembers | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent

	// Add event handlers
	session.AddHandler(bot.onReady)
	session.AddHandler(bot.onInteractionCreate)
	session.AddHandler(bot.onMessageCreate)

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

	// Register slash commands
	if err := b.handler.RegisterCommands(b.session); err != nil {
		return fmt.Errorf("error registering commands: %w", err)
	}

	fmt.Println("GamerPal bot is now running. Press CTRL+C to exit.")

	// Wait for interrupt signal
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Cleanup: Unregister commands
	b.handler.UnregisterCommands(b.session)

	return nil
}

// onReady handles the ready event
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	fmt.Printf("Bot is ready! Logged in as: %s#%s\n", r.User.Username, r.User.Discriminator)

	// Set bot status to something fresh every hour
	c := time.NewTicker(time.Hour)
	go func() {
		for range c.C {
			err := s.UpdateGameStatus(0, b.randomStatus())
			if err != nil {
				log.Println("Error setting status:", err)
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
		b.handler.HandleInteraction(s, i)
	}
}

// onMessageCreate handles message events
func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from bots (including ourselves)
	if m.Author.Bot {
		return
	}

	// Check if the bot is mentioned in the message & react
	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			err := s.MessageReactionAdd(m.ChannelID, m.ID, "❤️")
			if err != nil {
				log.Printf("Error adding heart reaction: %v", err)
			}
			return
		}
	}
}
