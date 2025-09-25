package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// handleLFGNow handles /lfg now subcommand
func (h *SlashCommandHandler) handleLFGNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer an ephemeral reply
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}})

	forumID := h.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ LFG forum channel ID not configured.")})
		return
	}
	// Must be invoked in a thread whose parent is the LFG forum
	ch, err := s.Channel(i.ChannelID)
	if err != nil || ch == nil || ch.ParentID != forumID {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ This command must be used inside an LFG thread.")})
		return
	}

	opts := i.ApplicationCommandData().Options[0].Options
	var region, message string
	var playerCount int
	for _, o := range opts {
		switch o.Name {
		case "region":
			region = strings.ToUpper(o.StringValue())
		case "message":
			message = o.StringValue()
		case "player_count":
			playerCount = int(o.IntValue())
		}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ message required")})
		return
	}
	if len(message) > 140 {
		message = message[:137] + "..."
	}
	if playerCount <= 0 || playerCount > 99 {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ invalid player_count")})
		return
	}

	userID := i.Member.User.ID

	// Public thread announcement with @here
	publicContent := fmt.Sprintf("@here: <@%s> is looking to play!\n\n_%s_", userID, message)
	if _, err := s.ChannelMessageSend(ch.ID, publicContent); err != nil {
		fallback := fmt.Sprintf("✅ Posted, but couldn't send public thread message.\n\n%s", publicContent)
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fallback)})
	} else {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Posted to Looking NOW feed.")})
	}

	// Feed channel embed
	feedChannelID := h.config.GetLFGNowPanelChannelID()
	if feedChannelID == "" { // silently skip if not set
		return
	}
	playersWord := "pals"
	if playerCount == 1 {
		playersWord = "pal"
	}
	embed := &discordgo.MessageEmbed{
		Title:       "Looking NOW",
		Description: fmt.Sprintf("<@%s> is looking to play in <#%s>!", userID, ch.ID),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Region", Value: region, Inline: true},
			{Name: "Looking For", Value: fmt.Sprintf("%d %s", playerCount, playersWord), Inline: true},
			{Name: "Message", Value: message, Inline: false},
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer:    &discordgo.MessageEmbedFooter{Text: "Run /lfg now again to post another update"},
	}
	_, _ = s.ChannelMessageSendEmbeds(feedChannelID, []*discordgo.MessageEmbed{embed})
}

// handleLFGSetupLookingNow sets the feed channel
func (h *SlashCommandHandler) handleLFGSetupLookingNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	h.config.Set("gamerpals_lfg_now_panel_channel_id", i.ChannelID)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Looking NOW feed channel set. New /lfg now posts will appear here.")})
}
