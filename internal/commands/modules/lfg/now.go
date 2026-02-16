package lfg

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"gamerpal/internal/utils"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const pendingNowTTL = 15 * time.Minute

// storePendingNow saves command options and returns a short key for use in button custom IDs.
func (m *LfgModule) storePendingNow(p pendingLFGNow) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	key := hex.EncodeToString(b)
	p.ExpiresAt = time.Now().Add(pendingNowTTL)
	m.pendingNow.Store(key, p)
	return key
}

// loadPendingNow retrieves and deletes pending options by key. Returns false if expired or missing.
func (m *LfgModule) loadPendingNow(key string) (pendingLFGNow, bool) {
	val, ok := m.pendingNow.LoadAndDelete(key)
	if !ok {
		return pendingLFGNow{}, false
	}
	p := val.(pendingLFGNow)
	if time.Now().After(p.ExpiresAt) {
		return pendingLFGNow{}, false
	}
	return p, true
}

// handleLFGNow handles /lfg now subcommand
func (m *LfgModule) handleLFGNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer an ephemeral reply
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}})

	forumID := m.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ LFG forum channel ID not configured.")})
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
	if voiceChannelID != "" {
		vc, err := s.Channel(voiceChannelID)
		if err != nil || vc == nil || (vc.Type != discordgo.ChannelTypeGuildVoice && vc.Type != discordgo.ChannelTypeGuildStageVoice) {
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ The provided voice_channel must be a voice or stage channel.")})
			return
		}
	}

	ch, err := s.Channel(i.ChannelID)
	inGameThread := err == nil && ch != nil && ch.ParentID == forumID

	if !inGameThread {
		key := m.storePendingNow(pendingLFGNow{
			Region:         region,
			Message:        message,
			PlayerCount:    playerCount,
			VoiceChannelID: voiceChannelID,
			UserID:         userID,
		})
		components := []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.Button{Style: discordgo.PrimaryButton, Label: "Any game", CustomID: fmt.Sprintf("%s::%s", lfgNowAnyGamePrefix, key)},
				&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Specific game", CustomID: fmt.Sprintf("%s::%s", lfgNowSpecificGamePrefix, key)},
			}},
		}
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    utils.StringPtr("Are you looking to play a **specific game** or **any game**?"),
			Components: &components,
		})
		return
	}

	var voiceChannelMention string
	if voiceChannelID != "" {
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

	m.postToFeed(s, i.GuildID, userID, region, message, playerCount, voiceChannelID, ch)
}

// postToFeed sends the Looking NOW embed to the feed channel.
// thread may be nil for "any game" posts.
func (m *LfgModule) postToFeed(s *discordgo.Session, guildID, userID, region, message string, playerCount int, voiceChannelID string, thread *discordgo.Channel) {
	feedChannelID := m.config.GetLFGNowPanelChannelID()
	if feedChannelID == "" {
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

	// Resolve display name (nickname > global name > fallback)
	displayName := ""
	if member, err := s.GuildMember(guildID, userID); err == nil && member != nil {
		if member.Nick != "" {
			displayName = member.Nick
		} else if member.User != nil && member.User.GlobalName != "" {
			displayName = member.User.GlobalName
		}
	}

	nameLabel := fmt.Sprintf("<@%s>", userID)
	if displayName != "" {
		nameLabel = fmt.Sprintf("<@%s> (**%s**)", userID, displayName)
	}

	description := fmt.Sprintf("🎮 %s is looking to play **any game**!", nameLabel)
	footer := "Run /lfg now to make a post like this!"
	if thread != nil {
		threadURL := fmt.Sprintf("https://discord.com/channels/%s/%s", guildID, thread.ID)
		description = fmt.Sprintf("🧵 %s is looking to play in [%s](%s)!", nameLabel, thread.Name, threadURL)
		footer = "Run /lfg now in a game thread to make a post like this!"
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Looking NOW",
		Description: description,
		Fields:      embedFields,
		Timestamp:   time.Now().Format(time.RFC3339),
		Color:       utils.Colors.Fancy(),
		Footer:      &discordgo.MessageEmbedFooter{Text: footer},
	}

	// Send with role mention as message content (embeds don't trigger pings)
	msgSend := &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
	}
	if roleID := m.config.GetLFGNowRoleID(); roleID != "" {
		msgSend.Content = fmt.Sprintf(":bell: <@&%s>", roleID)
	}
	_, _ = s.ChannelMessageSendComplex(feedChannelID, msgSend)
}

// handleLFGNowAnyGame handles the "Any game" button press from the /lfg now prompt.
func (m *LfgModule) handleLFGNowAnyGame(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cid := i.MessageComponentData().CustomID
	parts := strings.SplitN(cid, "::", 2)
	if len(parts) != 2 {
		return
	}
	pending, ok := m.loadPendingNow(parts[1])
	if !ok {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{Content: "❌ This prompt has expired. Please run `/lfg now` again.", Components: []discordgo.MessageComponent{}},
		})
		return
	}

	m.postToFeed(s, i.GuildID, pending.UserID, pending.Region, pending.Message, pending.PlayerCount, pending.VoiceChannelID, nil)

	// Assign the LFG Now role if configured
	confirmMsg := "✅ Posted to Looking NOW feed."
	if roleID := m.config.GetLFGNowRoleID(); roleID != "" {
		expiresAt := m.service.AssignLFGNowRole(i.GuildID, pending.UserID)
		if !expiresAt.IsZero() {
			confirmMsg = fmt.Sprintf("✅ Posted to Looking NOW feed.\nYou've also been given the <@&%s> role (expires <t:%d:R>).", roleID, expiresAt.Unix())
		}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: confirmMsg, Components: []discordgo.MessageComponent{}},
	})
}

// handleLFGNowSpecificGame handles the "Specific game" button press from the /lfg now prompt.
func (m *LfgModule) handleLFGNowSpecificGame(s *discordgo.Session, i *discordgo.InteractionCreate) {
	forumID := m.config.GetGamerPalsLFGForumChannelID()
	forumURL := fmt.Sprintf("https://discord.com/channels/%s/%s", i.GuildID, forumID)
	msg := fmt.Sprintf("Please run `/lfg now` in a [game thread](%s).", forumURL)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: msg, Components: []discordgo.MessageComponent{}},
	})
}

// handleLFGSetupLookingNow sets the feed channel
func (m *LfgModule) handleLFGSetupLookingNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	m.config.Set("gamerpals_lfg_now_panel_channel_id", i.ChannelID)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Looking NOW feed channel set. New /lfg now posts will appear here.")})
}
