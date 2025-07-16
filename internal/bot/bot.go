package bot

import (
	"bytes"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"sort"
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
	// Get all channel messages using pagination
	allMessages, err := getAllChannelMessages(s, c.ID)
	if err != nil {
		log.Printf("Error getting channel messages: %v", err)
		return
	}

	// Sort messages by timestamp (oldest first)
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].Timestamp.Before(allMessages[j].Timestamp)
	})

	var userPrompt strings.Builder

	userPrompt.WriteString("ticket number: " + c.Name + "\n\n")
	for _, msg := range allMessages {
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

	// Generate channel transcript
	transcript := generateChannelTranscript(allMessages, c.Name)

	responseChannel := cfg.GetGamerPalsModActionLogChannelID()

	// Create embed message
	embed := &discordgo.MessageEmbed{
		Title:       "🎫 Support Ticket Closed",
		Description: fmt.Sprintf("Ticket: `%s`", c.Name),
		Color:       0x00ff00, // Green color
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "⏰ Closed At",
				Value:  time.Now().Format("January 2, 2006 at 3:04 PM MST"),
				Inline: true,
			},
			{
				Name:   "🔗 Channel",
				Value:  fmt.Sprintf("<#%s>", c.ID),
				Inline: true,
			},
			{
				Name:   "📄 Messages",
				Value:  fmt.Sprintf("%d total messages", len(allMessages)),
				Inline: true,
			},
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Ticket System",
		},
	}

	// Send the embed first
	_, err = s.ChannelMessageSendEmbed(responseChannel, embed)
	if err != nil {
		log.Printf("Error sending embed message to channel %s: %v", responseChannel, err)
	}

	// Send in batches of 1500 characters
	const maxMessageLength = 1500
	for len(summary) > maxMessageLength {
		part := summary[:maxMessageLength]
		summary = summary[maxMessageLength:]

		// Send each part as a separate message
		_, err = s.ChannelMessageSend(responseChannel, part)
		if err != nil {
			log.Printf("Error sending summary part to channel %s: %v", responseChannel, err)
			continue
		}
	}

	// Send the remaining summary if any
	if len(summary) > 0 {
		_, err = s.ChannelMessageSend(responseChannel, summary)
		if err != nil {
			log.Printf("Error sending final summary part to channel %s: %v", responseChannel, err)
		}
	}

	// Send the transcript as an attachment
	transcriptFileName := fmt.Sprintf("transcript_%s_%s.txt", c.Name, time.Now().Format("2006-01-02_15-04-05"))

	messageData := &discordgo.MessageSend{
		Content: "📄 **Channel Transcript Attached**",
		Files: []*discordgo.File{
			{
				Name:        transcriptFileName,
				ContentType: "text/plain",
				Reader:      transcript,
			},
		},
	}

	_, err = s.ChannelMessageSendComplex(responseChannel, messageData)
	if err != nil {
		log.Printf("Error sending transcript attachment to channel %s: %v", responseChannel, err)
	}

	log.Printf("Successfully processed closed ticket %s and sent transcript", c.Name)
}

// getAllChannelMessages fetches all messages from a channel using pagination
func getAllChannelMessages(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
	var allMessages []*discordgo.Message
	var beforeID string
	const limit = 100

	for {
		messages, err := s.ChannelMessages(channelID, limit, beforeID, "", "")
		if err != nil {
			return nil, fmt.Errorf("error fetching messages: %w", err)
		}

		if len(messages) == 0 {
			break
		}

		allMessages = append(allMessages, messages...)
		beforeID = messages[len(messages)-1].ID

		// If we got fewer messages than the limit, we've reached the end
		if len(messages) < limit {
			break
		}

		// Add a small delay to avoid rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return allMessages, nil
}

// generateChannelTranscript creates a formatted transcript of all channel messages
func generateChannelTranscript(messages []*discordgo.Message, channelName string) *bytes.Buffer {
	var transcript bytes.Buffer

	transcript.WriteString(fmt.Sprintf("Channel Transcript: %s\n", channelName))
	transcript.WriteString(fmt.Sprintf("Generated on: %s\n", time.Now().Format("January 2, 2006 at 3:04 PM MST")))
	transcript.WriteString(fmt.Sprintf("Total messages: %d\n", len(messages)))
	transcript.WriteString(strings.Repeat("=", 80) + "\n\n")

	for _, msg := range messages {
		timestamp := msg.Timestamp.Format("2006-01-02 15:04:05")

		// Handle different message types
		if msg.Type == discordgo.MessageTypeDefault || msg.Type == discordgo.MessageTypeReply {
			transcript.WriteString(fmt.Sprintf("[%s] %s: %s\n",
				timestamp, msg.Author.GlobalName, msg.Content))

			// Include attachments if any
			if len(msg.Attachments) > 0 {
				for _, attachment := range msg.Attachments {
					transcript.WriteString(fmt.Sprintf("    📎 Attachment: %s (%s)\n",
						attachment.Filename, attachment.URL))
				}
			}

			// Include embeds if any
			if len(msg.Embeds) > 0 {
				transcript.WriteString(fmt.Sprintf("    📋 %d embed(s) included\n", len(msg.Embeds)))
			}
		} else {
			// Handle system messages
			transcript.WriteString(fmt.Sprintf("[%s] SYSTEM: %s\n", timestamp, getSystemMessageContent(msg)))
		}

		transcript.WriteString("\n")
	}

	return &transcript
}

// getSystemMessageContent formats system messages appropriately
func getSystemMessageContent(msg *discordgo.Message) string {
	switch msg.Type {
	case discordgo.MessageTypeChannelNameChange:
		return fmt.Sprintf("Channel name changed to: %s", msg.Content)
	case discordgo.MessageTypeGuildMemberJoin:
		return fmt.Sprintf("%s joined the server", msg.Author.Username)
	case discordgo.MessageTypeUserPremiumGuildSubscription:
		return fmt.Sprintf("%s boosted the server", msg.Author.Username)
	case discordgo.MessageTypeChannelPinnedMessage:
		return fmt.Sprintf("%s pinned a message", msg.Author.Username)
	case discordgo.MessageTypeRecipientAdd:
		return fmt.Sprintf("%s was added to the channel", msg.Author.Username)
	case discordgo.MessageTypeRecipientRemove:
		return fmt.Sprintf("%s was removed from the channel", msg.Author.Username)
	case discordgo.MessageTypeCall:
		return fmt.Sprintf("%s started a call", msg.Author.Username)
	default:
		return fmt.Sprintf("System message (type: %d): %s", msg.Type, msg.Content)
	}
}
