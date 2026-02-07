package events

import (
	"bytes"
	"fmt"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"sort"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// OnChannelUpdate handles channel updates
func OnChannelUpdate(s *discordgo.Session, c *discordgo.ChannelUpdate, cfg *config.Config) {
	// Handle voice permission sync (runs for all channel updates, not just renames)
	OnVoicePermissionSync(s, c, cfg)

	// Handle support ticket close (only for renamed channels)
	handleSupportTicketRename(s, c, cfg)
}

func handleSupportTicketRename(s *discordgo.Session, c *discordgo.ChannelUpdate, cfg *config.Config) {
	renamed := c.BeforeUpdate != nil && c.BeforeUpdate.Name != c.Name
	if !renamed {
		return
	}

	hasCorrectConfigs := cfg.GetGamerPalsServerID() != "" &&
		cfg.GetGamerPalsModActionLogChannelID() != "" &&
		cfg.GetGitHubModelsToken() != ""

	if !hasCorrectConfigs {
		cfg.Logger.Info("GamerPals server ID or mod action log channel ID not set, ignoring channel update")
		return
	}

	if strings.EqualFold(c.GuildID, cfg.GetGamerPalsServerID()) {
		closedTicketChannel := strings.HasPrefix(c.Name, "closed-")
		if !closedTicketChannel || c.IsThread() {
			cfg.Logger.Infof("Ignoring channel update for non-closed ticket channel: %s", c.Name)
			return
		}

		handleSupportTicketClose(s, c, cfg)
	}
}

func handleSupportTicketClose(s *discordgo.Session, c *discordgo.ChannelUpdate, cfg *config.Config) {
	// Get all channel messages using pagination
	allMessages, err := getAllChannelMessages(s, c.ID)
	if err != nil {
		cfg.Logger.Errorf("Error getting channel messages: %v", err)
		return
	}

	// Sort messages by timestamp (oldest first)
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].Timestamp.Before(allMessages[j].Timestamp)
	})

	var userPrompt strings.Builder

	// Generate channel transcript
	transcript := generateChannelTranscript(allMessages, c.Name)

	userPrompt.WriteString(transcript.String())

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

	cfg.Logger.Infof("Generating summary for closed ticket channel: %s", c.ID)
	modelsClient := utils.NewModelsClient(cfg)
	summary := modelsClient.ModelsRequest(systemPrompt, userPrompt.String(), "openai/gpt-4.1")

	if summary == "" {
		cfg.Logger.Errorf("Failed to generate summary for closed ticket channel: %s", c.ID)
	} else {
		cfg.Logger.Infof("Generated summary: %s", summary)
	}

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
		cfg.Logger.Errorf("Error sending embed message to channel %s: %v", responseChannel, err)
	}

	// Send in batches of 1500 characters
	const maxMessageLength = 1500
	for len(summary) > maxMessageLength {
		part := summary[:maxMessageLength]
		summary = summary[maxMessageLength:]

		// Send each part as a separate message
		_, err = s.ChannelMessageSend(responseChannel, part)
		if err != nil {
			cfg.Logger.Errorf("Error sending summary part to channel %s: %v", responseChannel, err)
			continue
		}
	}

	// Send the remaining summary if any
	if len(summary) > 0 {
		_, err = s.ChannelMessageSend(responseChannel, summary)
		if err != nil {
			cfg.Logger.Errorf("Error sending final summary part to channel %s: %v", responseChannel, err)
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
		cfg.Logger.Errorf("Error sending transcript attachment to channel %s: %v", responseChannel, err)
	}

	cfg.Logger.Infof("Successfully processed closed ticket %s and sent transcript", c.Name)
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

			msgContent := msg.Content
			if len(msg.Mentions) != 0 {
				for _, mention := range msg.Mentions {
					// Replace mentions with usernames
					msgContent = strings.ReplaceAll(msgContent, mention.Mention(), fmt.Sprintf("%s (%s)", mention.GlobalName, mention.Username))
				}
			}

			transcript.WriteString(fmt.Sprintf("[%s] %s: %s\n",
				timestamp, msg.Author.GlobalName, msgContent))

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
