package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// helper for inline min without pulling math
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// handleScheduleSay handles the /schedulesay command
func (h *SlashCommandHandler) handleScheduleSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
	id := h.ScheduleSayService.Add(ScheduledMessage{ChannelID: channelID, Content: messageContent, FireAt: fireAt, ScheduledBy: i.Member.User.ID, SuppressModMessage: suppressModMessage})

	// log scheduling
	preview := messageContent
	if len(preview) > 10 {
		preview = preview[:10]
	}
	logMsg := fmt.Sprintf("[ScheduledSay Added]\nID: %d\nChannel: %s (%s)\nModerator: %s (%s)\nFire At: %s (<t:%d:F>)\nSuppress Footer: %v\nLength: %d\nPreview: %.10q", id, ch.Mention(), ch.ID, i.Member.User.String(), i.Member.User.ID, fireAt.UTC().Format(time.RFC3339), fireAt.Unix(), suppressModMessage, len(messageContent), preview)
	if lErr := utils.LogToChannel(h.config, s, logMsg); lErr != nil {
		h.config.Logger.Errorf("failed logging schedule creation: %v", lErr)
	}
	h.config.Logger.Info(logMsg)

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
