package lfg

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// handleLFGNow handles /lfg now subcommand
func (m *LfgModule) handleLFGNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer an ephemeral reply
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}})

	forumID := m.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ LFG forum channel ID not configured.")})
		return
	}
	// Must be invoked in a thread whose parent is the LFG forum
	ch, err := s.Channel(i.ChannelID)
	if err != nil || ch == nil || ch.ParentID != forumID {
		forumChannelID := m.config.GetGamerPalsLFGForumChannelID()
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ This command must be used inside a thread in the game forum <#%s>.", forumChannelID))})
		return
	}

	opts := i.ApplicationCommandData().Options[0].Options
	var region, message string
	var playerCount int
	var voiceChannelID string
	for _, o := range opts {
		switch o.Name {
		case "region":
			region = strings.ToUpper(o.StringValue())
		case "message":
			message = o.StringValue()
		case "player_count":
			playerCount = int(o.IntValue())
		case "voice_channel":
			voiceChannelID = o.ChannelValue(s).ID
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

	// Validate voice channel (if provided) really is a voice/stage channel; reject if invalid
	var voiceChannelMention string
	if voiceChannelID != "" {
		vc, err := s.Channel(voiceChannelID)
		if err != nil || vc == nil || (vc.Type != discordgo.ChannelTypeGuildVoice && vc.Type != discordgo.ChannelTypeGuildStageVoice) {
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ The provided voice_channel must be a voice or stage channel.")})
			return
		}
		voiceChannelMention = fmt.Sprintf("Join voice: <#%s>\n", voiceChannelID)
	}

	// Public thread announcement with @here
	publicContent := fmt.Sprintf("@here: <@%s> is looking to play!\n%s\n_%s_", userID, voiceChannelMention, message)
	if _, err := s.ChannelMessageSend(ch.ID, publicContent); err != nil {
		fallback := fmt.Sprintf("✅ Posted, but couldn't send public thread message.\n\n%s", publicContent)
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fallback)})
	} else {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Posted to Looking NOW feed.")})
	}

	// Feed channel embed
	feedChannelID := m.config.GetLFGNowPanelChannelID()
	if feedChannelID == "" { // silently skip if not set
		return
	}
	playersWord := "pals"
	if playerCount == 1 {
		playersWord = "pal"
	}
	embedFields := []*discordgo.MessageEmbedField{
		{Name: "Region", Value: region, Inline: true},
		{Name: "Looking For", Value: fmt.Sprintf("%d %s", playerCount, playersWord), Inline: true},
		{Name: "Message", Value: message, Inline: false},
	}
	if voiceChannelID != "" {
		embedFields = append(embedFields, &discordgo.MessageEmbedField{Name: "Voice", Value: fmt.Sprintf("<#%s>", voiceChannelID), Inline: true})
	}
	embed := &discordgo.MessageEmbed{
		Title:       "Looking NOW",
		Description: fmt.Sprintf("<@%s> is looking to play in <#%s>!", userID, ch.ID),
		Fields:      embedFields,
		Timestamp:   time.Now().Format(time.RFC3339),
		Color:       utils.Colors.Fancy(),
		Footer:      &discordgo.MessageEmbedFooter{Text: "Run /lfg now in a game thread to make a post like this!"},
	}
	_, _ = s.ChannelMessageSendEmbeds(feedChannelID, []*discordgo.MessageEmbed{embed})
}

// handleLFGSetupLookingNow sets the feed channel
func (m *LfgModule) handleLFGSetupLookingNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	m.config.Set("gamerpals_lfg_now_panel_channel_id", i.ChannelID)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Looking NOW feed channel set. New /lfg now posts will appear here.")})
}
