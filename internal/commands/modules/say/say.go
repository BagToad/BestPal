package say

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for say commands
type SayModule struct {
	config  *config.Config
	service *Service
}

// New creates a new say module
func New(deps *types.Dependencies) *SayModule {
	return &SayModule{
		config:  deps.Config,
		service: NewService(deps.Config),
	}
}

// Register adds say-related commands to the command map
func (m *SayModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var modPerms int64 = discordgo.PermissionBanMembers

	// Register /say command
	cmds["say"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "say",
			Description:              "Send an anonymous message to a channel (Admin only)",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel to send the message to",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "The message content",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "suppressmodmessage",
					Description: "If true, suppress 'On behalf of moderator' footer",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleSay,
	}

	// Register /schedulesay command
	cmds["schedulesay"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "schedulesay",
			Description:              "Schedule an anonymous message to be sent at a specific time",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel to send the message to",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "The message content",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "timestamp",
					Description: "Unix timestamp when to send (use /time to convert)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "suppressmodmessage",
					Description: "If true, suppress 'On behalf of moderator' footer",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleScheduleSay,
	}

	// Register /listscheduledsays command
	cmds["listscheduledsays"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "listscheduledsays",
			Description:              "List upcoming scheduled messages",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleListScheduledSays,
	}

	// Register /cancelscheduledsay command
	cmds["cancelscheduledsay"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "cancelscheduledsay",
			Description:              "Cancel a scheduled message",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "The ID of the scheduled message to cancel",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handleCancelScheduledSay,
	}

	cmds["directsay"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "directsay",
			Description:              "Have LillyBot directly message a user",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user to send the DM to",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "The message content",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handleDirectSay,
	}
}

// Service returns the module as the service
func (m *SayModule) Service() types.ModuleService {
	return m.service
}
func (m *SayModule) handleSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Parse command options
	options := i.ApplicationCommandData().Options
	if len(options) < 2 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Missing required parameters. Please specify both channel and message.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get channel and message from options
	var targetChannelID string
	var messageContent string
	var suppressModMessage bool

	for _, option := range options {
		switch option.Name {
		case "channel":
			targetChannelID = option.ChannelValue(s).ID
		case "message":
			messageContent = option.StringValue()
		case "suppressmodmessage":
			suppressModMessage = option.BoolValue()
		}
	}

	// Validate inputs
	if targetChannelID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Invalid channel specified.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if messageContent == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Message content cannot be empty.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get the target channel to verify it exists and get its name
	targetChannel, err := s.Channel(targetChannelID)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unable to access the specified channel. Make sure the bot has permission to view and send messages in that channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Check if the bot has permission to send messages in the target channel
	permissions, err := s.UserChannelPermissions(s.State.User.ID, targetChannelID)
	if err != nil || permissions&discordgo.PermissionSendMessages == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ I don't have permission to send messages in %s.", targetChannel.Mention()),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if !suppressModMessage {
		messageContent = fmt.Sprintf("%s\n\n**On behalf of moderator**", messageContent)
	}

	// Send the message to the target channel
	sentMessage, err := s.ChannelMessageSend(targetChannelID, messageContent)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ Failed to send message to %s: %v", targetChannel.Mention(), err),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Create confirmation embed for the admin
	embed := &discordgo.MessageEmbed{
		Title:       "✅ Message Sent Successfully",
		Description: fmt.Sprintf("Your anonymous message has been sent to %s", targetChannel.Mention()),
		Color:       utils.Colors.Ok(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Channel",
				Value:  targetChannel.Mention(),
				Inline: true,
			},
			{
				Name:   "Message ID",
				Value:  sentMessage.ID,
				Inline: true,
			},
			{
				Name:   "Message Content",
				Value:  fmt.Sprintf("```%s```", messageContent),
				Inline: false,
			},
		},
	}

	// Respond to the admin with confirmation (ephemeral)
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

// handleScheduleSay handles the /schedulesay command
func (m *SayModule) handleScheduleSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	var channelID string
	var messageContent string
	var timestampVal int64
	var suppressModMessage bool

	for _, opt := range options {
		switch opt.Name {
		case "channel":
			channelID = opt.ChannelValue(s).ID
		case "message":
			messageContent = opt.StringValue()
		case "timestamp":
			timestampVal = opt.IntValue()
		case "suppressmodmessage":
			suppressModMessage = opt.BoolValue()
		}
	}

	if channelID == "" || messageContent == "" || timestampVal == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Missing required parameters.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	fireAt := time.Unix(timestampVal, 0)
	if fireAt.Before(time.Now().Add(30 * time.Second)) { // require at least 30s lead
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Timestamp must be at least 30 seconds in the future.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	// verify channel
	ch, err := s.Channel(channelID)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Unable to access the specified channel.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}
	perms, err := s.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil || perms&discordgo.PermissionSendMessages == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("❌ I don't have permission to send messages in %s.", ch.Mention()), Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	// store scheduled message
	id := m.service.Add(ScheduledMessage{ChannelID: channelID, Content: messageContent, FireAt: fireAt, ScheduledBy: i.Member.User.ID, SuppressModMessage: suppressModMessage})

	// log scheduling
	preview := messageContent
	if len(preview) > 10 {
		preview = preview[:10]
	}
	logMsg := fmt.Sprintf("[ScheduledSay Added]\nID: %d\nChannel: %s (%s)\nModerator: %s (%s)\nFire At: %s (<t:%d:F>)\nSuppress Footer: %v\nLength: %d\nPreview: %.10q", id, ch.Mention(), ch.ID, i.Member.User.String(), i.Member.User.ID, fireAt.UTC().Format(time.RFC3339), fireAt.Unix(), suppressModMessage, len(messageContent), preview)
	if lErr := utils.LogToChannel(m.service.cfg, s, logMsg); lErr != nil {
		m.service.cfg.Logger.Errorf("failed logging schedule creation: %v", lErr)
	}
	m.service.cfg.Logger.Info(logMsg)

	footer := "(footer suppressed)"
	if !suppressModMessage {
		footer = "(footer WILL append)"
	}
	embed := &discordgo.MessageEmbed{
		Title:       "✅ Message Scheduled",
		Description: fmt.Sprintf("ID %d scheduled for %s at <t:%d:F> (<t:%d:R>) %s", id, ch.Mention(), timestampVal, timestampVal, footer),
		Color:       utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Channel", Value: ch.Mention(), Inline: true},
			{Name: "Fire Time", Value: fmt.Sprintf("<t:%d:F>", timestampVal), Inline: true},
			{Name: "Suppress Mod Msg", Value: fmt.Sprintf("%v", suppressModMessage), Inline: true},
			{Name: "Content (truncated preview)", Value: fmt.Sprintf("```%s```", strings.ReplaceAll(messageContent[:min(200, len(messageContent))], "`", "'")), Inline: false},
		},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Flags: discordgo.MessageFlagsEphemeral}})
}

// handleListScheduledSays lists up to the next 20 scheduled messages
func (m *SayModule) handleListScheduledSays(s *discordgo.Session, i *discordgo.InteractionCreate) {
	list := m.service.List(20)
	if len(list) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "No scheduled messages.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	// Build fields; ensure we don't exceed embed field limits (25) - we cap at 20 anyway.
	fields := make([]*discordgo.MessageEmbedField, 0, len(list))
	for _, msg := range list {
		preview := msg.Content
		if len(preview) > 10 {
			preview = preview[:10]
		}
		name := fmt.Sprintf("ID %d", msg.ID)
		valueBuilder := strings.Builder{}
		valueBuilder.WriteString(fmt.Sprintf("Channel: <#%s>\n", msg.ChannelID))
		valueBuilder.WriteString(fmt.Sprintf("Fire: <t:%d:F> (<t:%d:R>)\n", msg.FireAt.Unix(), msg.FireAt.Unix()))
		valueBuilder.WriteString(fmt.Sprintf("Suppress Footer: %v\n", msg.SuppressModMessage))
		valueBuilder.WriteString(fmt.Sprintf("Preview: %.10q", preview))
		val := valueBuilder.String()
		// Discord field value max length is 1024
		if len(val) > 1024 {
			val = val[:1021] + "..."
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: val, Inline: true})
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Scheduled Says (next 20)",
		Description: fmt.Sprintf("Total queued (showing up to 20): %d", len(list)),
		Color:       utils.Colors.Info(),
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: "Use /cancelscheduledsay <ID> to cancel"},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Flags: discordgo.MessageFlagsEphemeral}})
}

// handleCancelScheduledSay cancels a scheduled message by ID
func (m *SayModule) handleCancelScheduledSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var idVal int64
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "id" {
			idVal = opt.IntValue()
		}
	}
	if idVal == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Missing or invalid ID.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}
	if m.service.Cancel(idVal) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("Cancelled scheduled say %d", idVal), Flags: discordgo.MessageFlagsEphemeral}})
	} else {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("No scheduled say with ID %d found", idVal), Flags: discordgo.MessageFlagsEphemeral}})
	}
}

func (m *SayModule) handleDirectSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Parse command options
	options := i.ApplicationCommandData().Options
	if len(options) < 2 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Missing required parameters. Please specify both user and message.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	// Get user ID and message from arguments
	var targetUser *discordgo.User
	var messageContent string

	for _, option := range options {
		switch option.Name {
		case "user":
			targetUser = option.UserValue(s)
		case "message":
			messageContent = option.StringValue()
		}
	}

	targetUserChannel, err := s.UserChannelCreate(targetUser.ID)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ Could not DM %s. They may have DMs disabled.", targetUser.Username),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	messageContent = fmt.Sprintf("**On behalf of a GamerPals Moderator:**\n\n%s\n\n**Do not reply to this message, replies are not monitored**", messageContent)

	sentMessage, err := s.ChannelMessageSend(targetUserChannel.ID, messageContent)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ Failed to send DM to %s.", targetUser.Username),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	}

	// Create confirmation embed for the admin
	embed := &discordgo.MessageEmbed{
		Title:       "✅ Message Sent Successfully",
		Description: fmt.Sprintf("Your anonymous message has been sent to %s", targetUser.Username),
		Color:       utils.Colors.Ok(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "User",
				Value:  targetUser.Username,
				Inline: true,
			},
			{
				Name:   "Message ID",
				Value:  sentMessage.ID,
				Inline: true,
			},
			{
				Name:   "Message Content",
				Value:  fmt.Sprintf("```%s```", messageContent),
				Inline: false,
			},
		},
	}

	// Respond to the admin with confirmation (ephemeral)
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})

	// Log to the bestpal log channel
	logMsg := heredoc.Docf(`
		[DirectSay Sent]
		User: %s (%s)
		Moderator: %s (%s)
		Length: %d
		Preview: %.10q
	`,
		targetUser.String(),
		targetUser.ID,
		i.Member.User.String(),
		i.Member.User.ID,
		len(messageContent),
		messageContent[:min(10, len(messageContent))],
	)

	if err := utils.LogToChannel(m.service.cfg, s, logMsg); err != nil {
		m.service.cfg.Logger.Errorf("failed logging direct say: %v", err)
	}
}
