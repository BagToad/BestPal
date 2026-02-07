package intro

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"
)

// parsedMessageLink holds the IDs extracted from a Discord message link.
type parsedMessageLink struct {
	GuildID   string
	ChannelID string
	MessageID string
}

// parseMessageLink extracts guild, channel, and message IDs from a Discord message URL.
func parseMessageLink(link string) (*parsedMessageLink, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Host != "discord.com" || u.Scheme != "https" {
		return nil, fmt.Errorf("not a valid Discord message link")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/channels/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("not a valid Discord message link")
	}
	return &parsedMessageLink{GuildID: parts[0], ChannelID: parts[1], MessageID: parts[2]}, nil
}

// pinMessageInIntroThread validates ownership and pins the message.
func (m *IntroModule) pinMessageInIntroThread(s *discordgo.Session, i *discordgo.InteractionCreate, channelID, messageID string) {
	// Resolve invoking user
	var userID string
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}
	if userID == "" {
		respondEphemeral(s, i, "❌ Unable to identify you.")
		return
	}

	// Check intro forum is configured
	introForumID := m.config.Config.GetGamerPalsIntroductionsForumChannelID()
	if introForumID == "" {
		respondEphemeral(s, i, "❌ Introductions forum is not configured.")
		return
	}

	// Fetch the channel/thread the message is in
	ch, err := s.Channel(channelID)
	if err != nil {
		respondEphemeral(s, i, "❌ Could not find that channel. Make sure the link is correct.")
		return
	}

	// Check that this thread belongs to the introductions forum
	if ch.ParentID != introForumID {
		respondEphemeral(s, i, "❌ That message is not in an introduction thread.")
		return
	}

	// Check that the invoking user owns this thread
	if ch.OwnerID != userID {
		respondEphemeral(s, i, "❌ You can only pin/unpin messages in your own introduction thread.")
		return
	}

	// Fetch the message to check if it's already pinned
	msg, err := s.ChannelMessage(channelID, messageID)
	if err != nil {
		respondEphemeral(s, i, "❌ Could not find that message. Make sure the link is correct.")
		return
	}

	if msg.Pinned {
		// Unpin
		err = s.ChannelMessageUnpin(channelID, messageID)
		if err != nil {
			m.config.Config.Logger.Errorf("Failed to unpin message %s in channel %s: %v", messageID, channelID, err)
			respondEphemeral(s, i, "❌ Failed to unpin the message. Please try again later.")
			return
		}
		respondEphemeral(s, i, "✅ Message unpinned from your introduction thread!")
		return
	}

	// Pin the message
	err = s.ChannelMessagePin(channelID, messageID)
	if err != nil {
		// Check for pin limit
		var restErr *discordgo.RESTError
		if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == discordgo.ErrCodeMaximumPinsReached {
			respondEphemeral(s, i, "❌ This thread has reached the maximum number of pinned messages (50). Unpin a message first.")
			return
		}
		m.config.Config.Logger.Errorf("Failed to pin message %s in channel %s: %v", messageID, channelID, err)
		respondEphemeral(s, i, "❌ Failed to pin the message. Please try again later.")
		return
	}

	respondEphemeral(s, i, "✅ Message pinned in your introduction thread!")
}

// handlePinSlash handles the /pin slash command.
func (m *IntroModule) handlePinSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	var link string
	for _, opt := range options {
		if opt.Name == "message_link" {
			link = opt.StringValue()
		}
	}

	parsed, err := parseMessageLink(link)
	if err != nil {
		respondEphemeral(s, i, "❌ Invalid message link. Please provide a valid Discord message link (right-click a message → Copy Message Link).")
		return
	}

	m.pinMessageInIntroThread(s, i, parsed.ChannelID, parsed.MessageID)
}

// handlePinContext handles the "Pin to intro" message context menu command.
func (m *IntroModule) handlePinContext(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	messageID := data.TargetID
	channelID := i.ChannelID

	if messageID == "" {
		respondEphemeral(s, i, "❌ Unable to identify the selected message.")
		return
	}

	m.pinMessageInIntroThread(s, i, channelID, messageID)
}

// respondEphemeral sends a simple ephemeral text response.
func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// registerPinCommands registers the /pin and "Pin to intro" commands.
func (m *IntroModule) registerPinCommands(cmds map[string]*types.Command) {
	cmds["pin"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "pin",
			Description: "Pin or unpin a message in your introduction thread",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message_link",
					Description: "Link to the message to pin or unpin (right-click message → Copy Message Link)",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handlePinSlash,
	}

	cmds["Pin to intro"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name: "Pin to intro",
			Type: discordgo.MessageApplicationCommand,
		},
		HandlerFunc: m.handlePinContext,
	}
}
