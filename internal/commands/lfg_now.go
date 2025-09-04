package commands

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// Represents a single active "looking now" entry
// One per user per thread.
type lfgNowEntry struct {
	UserID      string
	ThreadID    string
	Region      string
	Message     string
	PlayerCount int
	UpdatedAt   time.Time
}

var allowedRegions = []string{"NA", "EU", "ASIA", "SA", "OCE"}

func isAllowedRegion(r string) bool {
	r = strings.ToUpper(strings.TrimSpace(r))
	for _, ar := range allowedRegions {
		if r == ar {
			return true
		}
	}
	return false
}

// handleLFGNow handles /lfg now subcommand
func (h *SlashHandler) handleLFGNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
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

	if !isAllowedRegion(region) {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ Invalid region. Valid: NA, EU, ASIA, SA, OCE")})
		return
	}
	if playerCount <= 0 || playerCount > 99 {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ player_count must be 1-99")})
		return
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
	// Upsert entry
	h.lfgNowMu.Lock()
	perThread := h.lfgNowEntries[ch.ID]
	if perThread == nil {
		perThread = make(map[string]*lfgNowEntry)
		h.lfgNowEntries[ch.ID] = perThread
	}
	perThread[userID] = &lfgNowEntry{UserID: userID, ThreadID: ch.ID, Region: region, Message: message, PlayerCount: playerCount, UpdatedAt: time.Now()}
	h.lfgNowMu.Unlock()

	// Refresh panel
	if err := h.refreshLFGNowPanel(s); err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Added entry, but failed to refresh panel.")})
		return
	}

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Added to Looking NOW panel.")})
}

// refreshLFGNowPanel rebuilds and edits/reposts the panel.
func (h *SlashHandler) refreshLFGNowPanel(s *discordgo.Session) error {
	h.lfgNowMu.Lock()
	defer h.lfgNowMu.Unlock()

	if h.lfgNowPanelChannel == "" { // panel not set up
		return nil
	}

	// Purge expired entries first
	ttl := h.config.GetLFGNowTTLDuration()
	cutoff := time.Now().Add(-ttl)
	for threadID, users := range h.lfgNowEntries {
		for uid, e := range users {
			if e.UpdatedAt.Before(cutoff) {
				delete(users, uid)
			}
		}
		if len(users) == 0 {
			delete(h.lfgNowEntries, threadID)
		}
	}

	// Build fields
	type gameSection struct {
		ThreadID string
		Name     string
		Lines    []string
	}
	var sections []gameSection
	for threadID, users := range h.lfgNowEntries {
		if len(users) == 0 {
			continue
		}
		// fetch thread channel name
		ch, err := s.Channel(threadID)
		if err != nil || ch == nil {
			continue
		}
		var lines []string
		for _, e := range users {
			expUnix := e.UpdatedAt.Add(ttl).Unix()
			lines = append(lines, fmt.Sprintf("<@%s> [%s] (%d) - %s (expires <t:%d:R>)", e.UserID, e.Region, e.PlayerCount, e.Message, expUnix))
		}
		// stable order: user mention alphabetical
		sort.Strings(lines)
		sections = append(sections, gameSection{ThreadID: threadID, Name: ch.Name, Lines: lines})
	}

	// Sort sections by name for consistency
	sort.Slice(sections, func(i, j int) bool { return sections[i].Name < sections[j].Name })

	// Split into multiple embeds if >25 fields or size risk
	var embeds []*discordgo.MessageEmbed
	refreshInterval := 5 * time.Minute // keep in sync with scheduler cadence
	nextRefreshUnix := time.Now().Add(refreshInterval).Unix()
	current := &discordgo.MessageEmbed{Title: "Looking NOW", Color: utils.Colors.Fancy()}
	for _, sec := range sections {
		value := strings.Join(sec.Lines, "\n")
		if len(value) > 1024 { // truncate per-field
			value = value[:1019] + "..."
		}
		field := &discordgo.MessageEmbedField{Name: sec.Name, Value: value}
		if len(current.Fields) >= 25 { // start new embed
			embeds = append(embeds, current)
			current = &discordgo.MessageEmbed{Title: "Looking NOW (cont)", Color: utils.Colors.Fancy()}
		}
		current.Fields = append(current.Fields, field)
	}
	if len(current.Fields) > 0 || len(embeds) == 0 {
		embeds = append(embeds, current)
	}

	// Add footer with next refresh relative timestamp to each embed
	for _, emb := range embeds {
		emb.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Next refresh <t:%d:R>", nextRefreshUnix)}
	}

	// If no sections, clear existing panel messages and reset state
	if len(sections) == 0 {
		for _, mid := range h.lfgNowPanelMessages {
			_ = s.ChannelMessageDelete(h.lfgNowPanelChannel, mid)
		}
		h.lfgNowPanelMessages = nil
		return nil
	}

	// Either edit existing messages (count match) or delete & repost all
	if len(h.lfgNowPanelMessages) == len(embeds) {
		for idx, mid := range h.lfgNowPanelMessages {
			_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: mid, Channel: h.lfgNowPanelChannel, Embeds: &[]*discordgo.MessageEmbed{embeds[idx]}})
		}
		return nil
	}
	// count changed -> replace
	for _, mid := range h.lfgNowPanelMessages {
		_ = s.ChannelMessageDelete(h.lfgNowPanelChannel, mid)
	}
	var newIDs []string
	for _, emb := range embeds {
		msg, err := s.ChannelMessageSendEmbeds(h.lfgNowPanelChannel, []*discordgo.MessageEmbed{emb})
		if err != nil {
			continue
		}
		newIDs = append(newIDs, msg.ID)
	}
	h.lfgNowPanelMessages = newIDs
	return nil
}

// RefreshLFGNowPanel is an exported wrapper for background tasks.
func (h *SlashHandler) RefreshLFGNowPanel(s *discordgo.Session) error { return h.refreshLFGNowPanel(s) }

// handleLFGSetupLookingNow sets up the panel in the current channel
func (h *SlashHandler) handleLFGSetupLookingNow(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	chID := i.ChannelID
	// Clean out any previous panel messages (best effort)
	h.lfgNowMu.Lock()
	if h.lfgNowPanelChannel == chID {
		for _, mid := range h.lfgNowPanelMessages {
			_ = s.ChannelMessageDelete(chID, mid)
		}
	}
	h.lfgNowPanelChannel = chID
	h.lfgNowPanelMessages = nil
	h.config.Set("gamerpals_lfg_now_panel_channel_id", chID)
	h.lfgNowMu.Unlock()

	_ = h.refreshLFGNowPanel(s) // will create empty (no messages yet)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("✅ Looking NOW panel initialized. It will populate as users use /lfg now in threads.")})
}

// rename original setup -> find-a-thread; adjust routing in handleLFG
