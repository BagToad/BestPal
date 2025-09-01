package commands

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// In-memory cache of game name (normalized lowercase) -> thread channel ID
// This is simplistic; future optimization could add eviction / persistence.
var lfgThreadCache = struct {
	sync.RWMutex
	m map[string]string
}{m: make(map[string]string)}

// LFGCacheSet allows other packages to seed the cache.
func LFGCacheSet(normalizedName, threadID string) {
	lfgThreadCache.Lock()
	defer lfgThreadCache.Unlock()
	lfgThreadCache.m[normalizedName] = threadID
}

const (
	lfgPanelCustomID      = "lfg_panel_open_modal"
	lfgModalCustomID      = "lfg_game_modal"
	lfgModalInputCustomID = "lfg_game_name"
)

// handleLFG processes /lfg commands (currently only setup)
func (h *SlashHandler) handleLFG(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.ApplicationCommandData().Options) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Missing subcommand"}})
		return
	}

	sub := i.ApplicationCommandData().Options[0]
	switch sub.Name {
	case "setup":
		h.handleLFGSetup(s, i)
	default:
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Unknown subcommand"}})
	}
}

// handleLFGSetup posts (or replaces) the LFG panel in the current channel.
func (h *SlashHandler) handleLFGSetup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	forumID := h.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ LFG forum channel ID not configured.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	// Respond with panel
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			&discordgo.Button{Style: discordgo.PrimaryButton, Label: "LFG Thread", CustomID: lfgPanelCustomID},
		}},
	}

	content := "Click the button to find or create a game LFG forum thread."
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: content, Components: components}})
}

// Handle component interactions (button press -> show modal)
func (h *SlashHandler) handleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Only one button currently
	if i.MessageComponentData().CustomID != lfgPanelCustomID {
		return
	}
	// Show modal to gather game name
	modal := &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: lfgModalCustomID,
			Title:    "Find/Create LFG Thread",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{CustomID: lfgModalInputCustomID, Label: "Game Name", Style: discordgo.TextInputShort, Placeholder: "Enter game name", Required: true, MaxLength: 100},
				}},
			},
		},
	}
	_ = s.InteractionRespond(i.Interaction, modal)
}

// Handle modal submission: look up / create thread then reply ephemerally with link.
func (h *SlashHandler) handleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ModalSubmitData().CustomID != lfgModalCustomID {
		return
	}
	forumID := h.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ LFG forum channel ID not configured.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	var gameName string
	for _, c := range i.ModalSubmitData().Components {
		for _, inner := range c.(discordgo.ActionsRow).Components {
			if ti, ok := inner.(*discordgo.TextInput); ok && ti.CustomID == lfgModalInputCustomID {
				gameName = ti.Value
			}
		}
	}
	gameName = strings.TrimSpace(gameName)
	if gameName == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Game name required.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	normalized := strings.ToLower(gameName)

	// Defer ephemeral response while we work
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}})

	threadChannel, err := h.ensureLFGThread(s, forumID, gameName, normalized)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: fmtPtr(fmt.Sprintf("❌ %v", err))})
		return
	}

	link := threadLink(threadChannel)
	msg := fmt.Sprintf("✅ LFG thread for **%s**: %s", gameName, link)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
}

// ensureLFGThread returns existing valid thread or creates a new one.
func (h *SlashHandler) ensureLFGThread(s *discordgo.Session, forumID, displayName, normalized string) (*discordgo.Channel, error) {
	// First check cache
	lfgThreadCache.RLock()
	cachedID, ok := lfgThreadCache.m[normalized]
	lfgThreadCache.RUnlock()
	if ok {
		// Validate it still exists
		ch, err := s.Channel(cachedID)
		if err == nil && ch != nil && ch.ParentID == forumID { // still valid
			return ch, nil
		}
		// Remove invalid cache entry
		lfgThreadCache.Lock()
		delete(lfgThreadCache.m, normalized)
		lfgThreadCache.Unlock()
	}

	// Search existing threads in forum (may need pagination)
	// Discord API: List active threads in forum channel via s.GuildThreadsActive? Instead iterate guild channels not ideal.
	// For simplicity, attempt to find by name using cached threads from forum's available tags (not provided) => fallback create.
	// We attempt to create; if name conflict Discord will allow duplicates; improvement: prefetch threads on startup.

	// Validate game exists using IGDB (exact or search fallback)
	if h.igdbClient != nil {
		games, err := h.igdbClient.Games.Index(igdb.SetFilter("name", igdb.OpEquals, displayName), igdb.SetLimit(1), igdb.SetFields("name"))
		if err != nil || len(games) == 0 {
			games, err = h.igdbClient.Games.Search(displayName, igdb.SetLimit(1), igdb.SetFields("name"))
			if err != nil || len(games) == 0 {
				return nil, fmt.Errorf("game not found: %s", displayName)
			}
		}
		// Use canonical name from IGDB (first result) to set thread title for consistency
		if len(games) > 0 {
			displayName = games[0].Name
		}
	}

	// Create a new thread (forum post) – supply name; Discord will create starter message with no content if omitted
	params := &discordgo.ThreadStart{Type: discordgo.ChannelTypeGuildPublicThread, Name: displayName}
	thread, err := s.ThreadStartComplex(forumID, params)
	if err != nil {
		return nil, fmt.Errorf("failed creating thread: %w", err)
	}
	lfgThreadCache.Lock()
	lfgThreadCache.m[normalized] = thread.ID
	lfgThreadCache.Unlock()
	return thread, nil
}

func threadLink(ch *discordgo.Channel) string {
	if ch == nil {
		return ""
	}
	return fmt.Sprintf("https://discord.com/channels/%s/%s", ch.GuildID, ch.ID)
}

func fmtPtr(s string) *string { return &s }

// Public wrappers used by bot interaction router
func (h *SlashHandler) HandleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.handleLFGComponent(s, i)
}

func (h *SlashHandler) HandleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.handleLFGModalSubmit(s, i)
}
