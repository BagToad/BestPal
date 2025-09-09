package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// handleLFGNow handles /lfg now subcommand
func (h *SlashCommandHandler) handleLFGNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer reply
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}})

	forumID := h.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ LFG forum channel ID not configured.")})
		return
	}

	// Must be invoked inside a thread whose parent is the LFG forum
	ch, err := s.Channel(i.ChannelID)
	if err != nil || ch == nil || ch.ParentID != forumID {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ This command must be used inside an LFG thread.")})
		return
	}

	opts := i.ApplicationCommandData().Options[0].Options // subcommand options
	var region string
	var message string
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

	userID := i.Member.User.ID
	_ = h.lfgNowSvc.Upsert(ch.ID, userID, region, message, playerCount)
	if err := h.lfgNowSvc.RefreshPanel(s, h.config.GetLFGNowTTLDuration()); err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Added entry, but failed to refresh panel.")})
		// Even if panel refresh fails, still attempt to post the public message below.
		return
	}

	publicContent := fmt.Sprintf("@here: + <@%s> is looking to play!\n\n_%s_", userID, message)
	if _, err := s.ChannelMessageSend(ch.ID, publicContent); err != nil {
		// Fall back to including the announcement in the ephemeral reply if sending fails
		fallback := fmt.Sprintf("✅ Added to Looking NOW panel, but couldn't send public message.\n\n%s", publicContent)
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fallback)})
		return
	}

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Added to Looking NOW panel.")})
}

// refreshLFGNowPanel rebuilds and edits/reposts the panel.
func (h *SlashCommandHandler) refreshLFGNowPanel(s *discordgo.Session) error {
	return h.lfgNowSvc.RefreshPanel(s, h.config.GetLFGNowTTLDuration())
}

// RefreshLFGNowPanel is an exported wrapper for background tasks.
func (h *SlashCommandHandler) RefreshLFGNowPanel(s *discordgo.Session) error {
	return h.refreshLFGNowPanel(s)
}

// handleLFGSetupLookingNow sets up the panel in the current channel
func (h *SlashCommandHandler) handleLFGSetupLookingNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	chID := i.ChannelID
	// Clean out any previous panel messages (best effort)
	h.lfgNowSvc.SetupPanel(chID)
	h.config.Set("gamerpals_lfg_now_panel_channel_id", chID)

	h.lfgNowSvc.SetupPanel(chID)
	_ = h.refreshLFGNowPanel(s) // will create empty (no messages yet)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Looking NOW panel initialized. It will populate as users use /lfg now in threads.")})
}
