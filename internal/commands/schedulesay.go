package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"time"

	"github.com/bwmarrin/discordgo"
)

// handleScheduleSay handles the /schedulesay command
func (h *SlashCommandHandler) handleScheduleSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	var channelID string
	var messageContent string
	var timestampVal int64

	for _, opt := range options {
		switch opt.Name {
		case "channel":
			channelID = opt.ChannelValue(s).ID
		case "message":
			messageContent = opt.StringValue()
		case "timestamp":
			timestampVal = opt.IntValue()
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
	h.ScheduleSayService.Add(ScheduledMessage{ChannelID: channelID, Content: messageContent, FireAt: fireAt, ScheduledBy: i.Member.User.ID})

	// log scheduling
	logMsg := fmt.Sprintf("ScheduledSay Added: moderator=%s channel=%s fire_at=%s content_len=%d", i.Member.User.String(), ch.Mention(), fireAt.UTC().Format(time.RFC3339), len(messageContent))
	if lErr := utils.LogToChannel(h.config, s, logMsg); lErr != nil {
		h.config.Logger.Errorf("failed logging schedule creation: %v", lErr)
	}
	h.config.Logger.Info(logMsg)

	embed := &discordgo.MessageEmbed{
		Title:       "✅ Message Scheduled",
		Description: fmt.Sprintf("Your message will be sent to %s at <t:%d:F> (<t:%d:R>)", ch.Mention(), timestampVal, timestampVal),
		Color:       utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Channel", Value: ch.Mention(), Inline: true},
			{Name: "Fire Time", Value: fmt.Sprintf("<t:%d:F>", timestampVal), Inline: true},
			{Name: "Content", Value: fmt.Sprintf("```%s```", messageContent), Inline: false},
		},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Flags: discordgo.MessageFlagsEphemeral}})
}
