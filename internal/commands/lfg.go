package commands

import (
	"fmt"
	"strings"
	"sync"

	"gamerpal/internal/games"
	"gamerpal/internal/utils"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// In-memory cache of game name (normalized lowercase) -> thread channel ID
// This is simplistic; future optimization could add eviction / persistence.
var lfgThreadCache = struct {
	sync.RWMutex
	nameToThreadID map[string]string
}{
	nameToThreadID: make(map[string]string),
}

// LFGCacheSet allows other packages to seed the cache.
func LFGCacheSet(normalizedName, threadID string) {
	lfgThreadCache.Lock()
	defer lfgThreadCache.Unlock()
	lfgThreadCache.nameToThreadID[normalizedName] = threadID
}

type LFGCacheSearchResult struct {
	ExactThreadID    string
	PartialThreadIDs []string
}

func LFGCacheSearch(name string) (LFGCacheSearchResult, bool) {
	lfgThreadCache.RLock()
	defer lfgThreadCache.RUnlock()
	if threadID, ok := lfgThreadCache.nameToThreadID[name]; ok {
		return LFGCacheSearchResult{
			ExactThreadID: threadID,
		}, ok
	}

	// We want to support a partial match search as well
	// So if a user searches "league", want to find the
	// "League of Legends" thread
	var partialHitThreadIDs []string
	for k, v := range lfgThreadCache.nameToThreadID {
		if strings.Contains(strings.ToLower(k), strings.ToLower(name)) {
			partialHitThreadIDs = append(partialHitThreadIDs, v)
		}
	}
	if len(partialHitThreadIDs) > 0 {
		return LFGCacheSearchResult{
			PartialThreadIDs: partialHitThreadIDs,
		}, true
	}

	return LFGCacheSearchResult{}, false
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
			&discordgo.Button{
				Style:    discordgo.PrimaryButton,
				Label:    "LFG Thread",
				CustomID: lfgPanelCustomID,
			},
		}},
	}

	content := "Click the button to find or create a game LFG forum thread."
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: components,
		}})
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
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						&discordgo.TextInput{
							CustomID:    lfgModalInputCustomID,
							Label:       "Game Name",
							Style:       discordgo.TextInputShort,
							Placeholder: "Enter game name",
							Required:    true,
							MaxLength:   100,
						},
					},
				},
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
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ LFG forum channel ID not configured.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
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
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Game name required.", Flags: discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	normalized := strings.ToLower(gameName)

	// Defer ephemeral response while we work
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		h.config.Logger.Errorf("LFG: failed to defer modal submit: %v", err)
		return
	}

	if h.igdbClient == nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: fmtPtr("❌ IGDB client is not initialized. Admin intervention required"),
		})
		return
	}

	// 1. Attempt to find existing thread from cache (validated)
	exactThreadChannel, _ := h.findCachedExactThread(s, forumID, normalized)

	// 2. Perform search (exact + suggestions)
	searchRes, err := games.ExactMatchWithSuggestions(h.igdbClient, gameName)
	if err != nil {
		h.config.Logger.Errorf("LFG: failed to search IGDB for '%s': %v", gameName, err)
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: fmtPtr(fmt.Sprintf("❌ error looking up game _\"%s\"_", gameName)),
		})
		return
	}
	if searchRes == nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: fmtPtr("❌ unexpected empty search result")})
		return
	}

	// 3. Create thread if needed (exact match found but no existing thread)
	var createdNew bool
	if exactThreadChannel == nil && searchRes.ExactMatch != nil {
		ch, err := h.createLFGThreadFromExactMatch(s, forumID, normalized, searchRes.ExactMatch)
		if err != nil {
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: fmtPtr("❌ failed creating thread")})
			return
		}
		exactThreadChannel = ch
		createdNew = true
	}

	// 4. Gather partial thread suggestions (cache partial matches) up to 3
	partialThreadSuggestions := gatherPartialThreadSuggestionsDetailed(s, forumID, normalized, idOrEmpty(exactThreadChannel), 3)

	// 5. Gather IGDB title suggestions (excluding duplicates & exact). Up to 3.
	var igdbSuggestionSections []suggestionSection
	if searchRes.Suggestions != nil {
		for _, g := range searchRes.Suggestions {
			if g == nil || g.Name == "" {
				continue
			}
			if exactThreadChannel != nil && strings.EqualFold(g.Name, exactThreadChannel.Name) {
				continue
			}
			dup := false
			for _, pts := range partialThreadSuggestions {
				if strings.EqualFold(pts.Title, g.Name) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			igdbSuggestionSections = append(igdbSuggestionSections, buildSuggestionSectionFromName(s, forumID, g.Name))
			if len(igdbSuggestionSections) >= 3 {
				break
			}
		}
	}

	// 6. Build embed fields list: exact (if any), then partial thread suggestions, then IGDB suggestions.
	var fields []*discordgo.MessageEmbedField
	if exactThreadChannel != nil {
		status := "existing thread"
		if createdNew {
			status = "created thread"
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("%s (exact match, %s)", exactThreadChannel.Name, status),
			Value: fmt.Sprintf("- %s", threadLink(exactThreadChannel)),
		})
	}
	for _, sec := range partialThreadSuggestions {
		fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("%s (suggestion)", sec.Title), Value: sec.Value})
	}
	for _, sec := range igdbSuggestionSections {
		fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("%s (suggestion)", sec.Title), Value: sec.Value})
	}
	if len(fields) == 0 { // fallback when nothing at all
		fields = append(fields, &discordgo.MessageEmbedField{Name: "No Results", Value: "Try a more specific title."})
	}

	embed := &discordgo.MessageEmbed{
		Title:  "LFG Thread Lookup",
		Color:  utils.Colors.Fancy(),
		Fields: fields,
	}
	if createdNew && exactThreadChannel != nil {
		h.config.Logger.Infof("LFG: created new thread for '%s'", exactThreadChannel.Name)
	}
	embedSlice := []*discordgo.MessageEmbed{embed}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embedSlice})
}

// findCachedExactThread validates and returns a cached exact thread channel if still valid.
func (h *SlashHandler) findCachedExactThread(s *discordgo.Session, forumID, normalized string) (*discordgo.Channel, bool) {
	lfgThreadCache.RLock()
	cacheRes, cacheHit := LFGCacheSearch(normalized)
	lfgThreadCache.RUnlock()
	if !cacheHit || cacheRes.ExactThreadID == "" {
		return nil, false
	}
	ch, err := s.Channel(cacheRes.ExactThreadID)
	if err == nil && ch != nil && ch.ParentID == forumID {
		return ch, true
	}
	// stale entry
	lfgThreadCache.Lock()
	delete(lfgThreadCache.nameToThreadID, normalized)
	lfgThreadCache.Unlock()
	return nil, false
}

// createLFGThreadFromExactMatch builds metadata + creates the forum thread for an exact IGDB match.
func (h *SlashHandler) createLFGThreadFromExactMatch(s *discordgo.Session, forumID, normalized string, exact *igdb.Game) (*discordgo.Channel, error) {
	if exact == nil {
		return nil, fmt.Errorf("nil exact game")
	}
	displayName := exact.Name
	var gameSummary string
	var playerLine string
	var linksLine string

	if exact.Summary != "" {
		gameSummary = exact.Summary
		if len(gameSummary) > 400 {
			gameSummary = gameSummary[:397] + "..."
		}
	}

	if len(exact.Websites) > 0 {
		if sites, err := h.igdbClient.Websites.List(exact.Websites, igdb.SetFields("url", "category")); err == nil {
			var parts []string
			addSite := func(label, url string) {
				if url != "" {
					parts = append(parts, fmt.Sprintf("[%s](%s)", label, url))
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
				linksLine = strings.Join(parts, " | ")
			}
		}
	}

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

	initialParts := []string{fmt.Sprintf("This is the LFG thread for _%s_! Use the LFG panel anytime to get a link.", displayName)}
	if gameSummary != "" {
		initialParts = append(initialParts, "_"+gameSummary+"_")
	}
	if playerLine != "" {
		initialParts = append(initialParts, playerLine)
	}
	if linksLine != "" {
		initialParts = append(initialParts, linksLine)
	}
	initialContent := strings.Join(initialParts, "\n\n")
	if len(initialContent) > 1800 {
		initialContent = initialContent[:1797] + "..."
	}

	thread, err := s.ForumThreadStart(forumID, displayName, 4320, initialContent)
	if err != nil {
		h.config.Logger.Errorf("LFG: failed creating forum thread '%s' in forum %s: %v", displayName, forumID, err)
		return nil, err
	}
	lfgThreadCache.Lock()
	lfgThreadCache.nameToThreadID[normalized] = thread.ID
	lfgThreadCache.Unlock()
	return thread, nil
}

// gatherThreadSuggestions scans cached names for partial matches (excluding exact thread) and returns up to 3 links.
// suggestionSection represents info needed to render an embed field for a suggestion.
type suggestionSection struct {
	Title string
	Value string
}

// gatherPartialThreadSuggestionsDetailed returns up to 'limit' partial match existing thread suggestions.
func gatherPartialThreadSuggestionsDetailed(s *discordgo.Session, forumID, normalized, excludeThreadID string, limit int) []suggestionSection {
	var out []suggestionSection
	lfgThreadCache.RLock()
	for k, id := range lfgThreadCache.nameToThreadID {
		if strings.Contains(strings.ToLower(k), normalized) {
			if excludeThreadID != "" && id == excludeThreadID {
				continue
			}
			ch, err := s.Channel(id)
			if err == nil && ch != nil && ch.ParentID == forumID {
				out = append(out, suggestionSection{Title: ch.Name, Value: fmt.Sprintf("- %s", threadLink(ch))})
				if len(out) >= limit {
					break
				}
			}
		}
	}
	lfgThreadCache.RUnlock()
	return out
}

// buildSuggestionSectionFromName checks for existing thread of a given title; if absent returns placeholder.
func buildSuggestionSectionFromName(s *discordgo.Session, forumID, gameTitle string) suggestionSection {
	norm := strings.ToLower(gameTitle)
	lfgThreadCache.RLock()
	threadID, ok := lfgThreadCache.nameToThreadID[norm]
	lfgThreadCache.RUnlock()
	if ok {
		ch, err := s.Channel(threadID)
		if err == nil && ch != nil && ch.ParentID == forumID {
			return suggestionSection{Title: gameTitle, Value: fmt.Sprintf("- %s", threadLink(ch))}
		}
	}
	return suggestionSection{Title: gameTitle, Value: fmt.Sprintf("- No thread created yet. Lookup this `%s` exactly to create one.", gameTitle)}
}

func idOrEmpty(ch *discordgo.Channel) string {
	if ch == nil {
		return ""
	}
	return ch.ID
}

func threadLink(ch *discordgo.Channel) string {
	if ch == nil {
		return ""
	}
	return fmt.Sprintf("https://discord.com/channels/%s/%s", ch.GuildID, ch.ID)
}

func fmtPtr(s string) *string { return &s }

// Public wrappers used by bot interaction router

// HandleLFGComponent handles the LFG component interactions.
func (h *SlashHandler) HandleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.handleLFGComponent(s, i)
}

// HandleLFGModalSubmit handles the submission of the LFG modal.
func (h *SlashHandler) HandleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.handleLFGModalSubmit(s, i)
}
