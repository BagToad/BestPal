package bot

import (
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"

	"gamerpal/internal/commands"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
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
	session, err := discordgo.New("Bot " + cfg.GetBotToken())
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
	session.AddHandler(bot.onChannelUpdate)

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

	// Cleanup: Unregister commands, optionally
	if os.Getenv("UNREGISTER_COMMANDS") == "true" {
		b.handler.UnregisterCommands(b.session)
	}

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

// onChannelUpdate handles channel updates
func (b *Bot) onChannelUpdate(s *discordgo.Session, c *discordgo.ChannelUpdate) {
	renamed := c.BeforeUpdate.Name != c.Name
	if !renamed {
		log.Println("Channel was not renamed, ignoring update")
		return
	}

	hasCorrectConfigs := b.config.GetGamerPalsServerID() != "" &&
		b.config.GetGamerPalsModActionLogChannelID() != "" &&
		b.config.GetGitHubModelsToken() != ""

	if !hasCorrectConfigs {
		log.Println("GamerPals server ID or mod action log channel ID not set, ignoring channel update")
		return
	}

	if strings.EqualFold(c.GuildID, b.config.GetGamerPalsServerID()) {
		closedTicketChannel := strings.HasPrefix(c.Name, "closed-")
		if !closedTicketChannel || c.IsThread() {
			log.Printf("Ignoring channel update for non-closed ticket channel: %s", c.Name)
			return
		}

		handleSupportTicketClose(s, c, b.config)
		return
	}
}

func handleSupportTicketClose(s *discordgo.Session, c *discordgo.ChannelUpdate, cfg *config.Config) {
	// Get the channel messages
	messages, err := s.ChannelMessages(c.ID, 100, "", "", "")
	if err != nil {
		log.Printf("Error getting channel messages: %v", err)
		return
	}

	var userPrompt strings.Builder

	userPrompt.WriteString("ticket number: " + c.Name + "\n\n")
	for _, msg := range messages {
		if msg.Author.ID == s.State.User.ID {
			userPrompt.WriteString(fmt.Sprintf("%s (on behalf of mod): %s\n", msg.Author.Username, msg.Content))
			continue
		}

		userPrompt.WriteString(fmt.Sprintf("%s: %s\n", msg.Author.Username, msg.Content))
	}

	systemPrompt := heredoc.Doc(`
			You are a helpful assistant that summarizes the discussion in a support ticket
			between moderators and users after a ticket has been closed.
			You will be provided with the last 100 messages in the channel.
			Your task is to summarize the discussion in a concise manner.
			Do not include any personal information or sensitive data.
			Do not respond to users, just summarize the discussion.

			The complete summary should be less than 2000 characters.
			
			Summaries here should be super crisp and concise. Don't just
			repeat messages.

			Respond in the following format template:

			This is a summary from support ticket number <ticket-id>.

			Users involved:
			- User1: <user1-id>
			- User2: <user2-id>

			Summary of problem:
			<summary of the discussion>

			Actions taken by moderators:
			<summary of actions taken by moderators>

			Other details and next steps:
			<any other relevant details>
		`)

	modelsClient := utils.NewModelsClient(cfg)

	log.Println("Generating summary for closed ticket channel:", c.ID)
	summary := modelsClient.ModelsRequest(systemPrompt, userPrompt.String(), "openai/gpt-4.1")

	if summary == "" {
		log.Println("Failed to generate summary for closed ticket channel:", c.ID)
		return
	}

	log.Println("Generated summary:", summary)

	// Create embed message
	embed := &discordgo.MessageEmbed{
		Title:       "🎫 Support Ticket Closed",
		Description: fmt.Sprintf("Ticket: `%s`", c.Name),
		Color:       0x00ff00, // Green color
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   " Closed At",
				Value:  time.Now().Format("January 2, 2006 at 3:04 PM MST"),
				Inline: true,
			},
			{
				Name:   "🔗 Channel",
				Value:  fmt.Sprintf("<#%s>", c.ID),
				Inline: true,
			},
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Ticket System",
		},
	}

	responseChannel := cfg.GetGamerPalsModActionLogChannelID()

	// Send the embed first
	_, err = s.ChannelMessageSendEmbed(responseChannel, embed)
	if err != nil {
		log.Printf("Error sending embed message to channel %s: %v", responseChannel, err)
		return
	}

	_, err = s.ChannelMessageSend(responseChannel, summary)
	if err != nil {
		log.Printf("Error sending summary message to channel %s: %v", responseChannel, err)
	}
}
