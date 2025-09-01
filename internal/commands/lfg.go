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
	if err := s.InteractionRespond(i.Interaction, modal); err != nil {
		h.config.Logger.Errorf("LFG: failed to open modal: %v", err)
	}
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
	// Safely extract text input value; handle both value and pointer forms of ActionsRow to avoid panics.
	for _, comp := range i.ModalSubmitData().Components {
		var row *discordgo.ActionsRow
		switch v := comp.(type) {
		case discordgo.ActionsRow:
			row = &v
		case *discordgo.ActionsRow:
			row = v
		default:
			continue
		}
		for _, inner := range row.Components {
			if ti, ok := inner.(*discordgo.TextInput); ok && ti.CustomID == lfgModalInputCustomID {
				gameName = ti.Value
				break
			}
		}
		if gameName != "" { // found
			break
		}
	}
	if gameName == "" {
		// Log for diagnostics in case modal structure changes unexpectedly
		h.config.Logger.Warnf("LFG modal submit: game name input not found in components; customID=%s", i.ModalSubmitData().CustomID)
	}
	gameName = strings.TrimSpace(gameName)
	if gameName == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Game name required.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	normalized := strings.ToLower(gameName)

	// Defer ephemeral response while we work
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}}); err != nil {
		h.config.Logger.Errorf("LFG: failed to defer modal submit: %v", err)
		return
	}

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

	// Validate game exists using IGDB: fetch up to 10 candidates, pick exact (case-insensitive) or return suggestions.
	var gameSummary string
	var playerLine string
	var linksLine string
	if h.igdbClient != nil {
		inputName := displayName
		games, err := h.igdbClient.Games.Search(displayName,
			igdb.SetFields("id", "name", "summary", "websites", "multiplayer_modes"),
			igdb.SetLimit(10),
		)
		if err != nil || len(games) == 0 {
			return nil, fmt.Errorf("could not find game %s", inputName)
		}
		var exact *igdb.Game
		suggestions := make([]string, 0, len(games))
		for _, g := range games {
			if g == nil || g.Name == "" {
				continue
			}
			suggestions = append(suggestions, g.Name)
			if strings.EqualFold(g.Name, inputName) {
				exact = g
			}
		}
		if exact == nil { // no exact match; return suggestions
			if len(suggestions) > 0 {
				return nil, fmt.Errorf("could not find game %s. Did you mean: %s?", inputName, strings.Join(suggestions, ", "))
			}
			return nil, fmt.Errorf("could not find game %s", inputName)
		}
		displayName = exact.Name
		// Prepare summary (description)
		if exact.Summary != "" {
			gameSummary = exact.Summary
			if len(gameSummary) > 400 { // trim to keep message under limit
				gameSummary = gameSummary[:397] + "..."
			}
		}
		// Fetch websites for links (Steam, Official, GOG)
		if len(exact.Websites) > 0 {
			if sites, err := h.igdbClient.Websites.List(exact.Websites, igdb.SetFields("url", "category")); err == nil {
				var parts []string
				// preserve order preference
				addSite := func(label, url string) {
					if url != "" {
						parts = append(parts, fmt.Sprintf("%s: %s", label, url))
					}
				}
				var official, steam, gog string
				for _, w := range sites {
					if w == nil || w.URL == "" {
						continue
					}
					switch w.Category {
					case igdb.WebsiteSteam:
						if steam == "" {
							steam = w.URL
						}
					case igdb.WebsiteOfficial:
						if official == "" {
							official = w.URL
						}
					case 17: // GOG
						if gog == "" {
							gog = w.URL
						}
					}
				}
				addSite("Steam", steam)
				addSite("Official", official)
				addSite("GOG", gog)
				if len(parts) > 0 {
					linksLine = "Links: " + strings.Join(parts, " | ")
				}
			}
		}
		// Multiplayer modes (online player counts)
		if len(exact.MultiplayerModes) > 0 {
			if modes, err := h.igdbClient.MultiplayerModes.List(exact.MultiplayerModes, igdb.SetFields("*")); err == nil {
				var onlineMax, coopMax int
				for _, m := range modes {
					if m == nil {
						continue
					}
					if m.Onlinemax > onlineMax {
						onlineMax = m.Onlinemax
					}
					if m.Onlinecoopmax > coopMax {
						coopMax = m.Onlinecoopmax
					}
				}
				if onlineMax > 0 || coopMax > 0 {
					if onlineMax > 0 {
						playerLine = fmt.Sprintf("Players: up to %d online", onlineMax)
					}
					if coopMax > 0 {
						if playerLine != "" {
							playerLine += "; "
						}
						playerLine += fmt.Sprintf("co-op up to %d", coopMax)
					}
				}
			}
		}
	}

	// Create a new forum thread (forum post) with required initial message.
	// Build initial thread message with optional extra info.
	initialParts := []string{fmt.Sprintf("LFG thread for %s. Use the panel to get a link any time!", displayName)}
	if gameSummary != "" {
		initialParts = append(initialParts, gameSummary)
	}
	if playerLine != "" {
		initialParts = append(initialParts, playerLine)
	}
	if linksLine != "" {
		initialParts = append(initialParts, linksLine)
	}
	initialContent := strings.Join(initialParts, "\n\n")
	if len(initialContent) > 1800 { // safety truncation
		initialContent = initialContent[:1797] + "..."
	}
	// Use 4320 (3 days) auto-archive duration – acceptable standard value; adjust if needed.
	thread, err := s.ForumThreadStart(forumID, displayName, 4320, initialContent)
	if err != nil {
		h.config.Logger.Errorf("LFG: failed creating forum thread '%s' in forum %s: %v", displayName, forumID, err)
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
