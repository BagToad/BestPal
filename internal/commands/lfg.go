package commands

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

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
	lfgPanelCustomID          = "lfg_panel_open_modal"
	lfgModalCustomID          = "lfg_game_modal"
	lfgModalInputCustomID     = "lfg_game_name"
	lfgMoreSuggestionsPrefix  = "lfg_more_suggestions"  // lfg_more_suggestions::<normalizedQuery>
	lfgCreateSuggestionPrefix = "lfg_create_suggestion" // lfg_create_suggestion::<id>
)

// in-memory mapping for suggestion button IDs -> game title
var lfgSuggestionMap = struct {
	sync.RWMutex
	idToTitle map[string]string
}{idToTitle: make(map[string]string)}

var lfgSuggestionIDCounter uint64

func newSuggestionID() string {
	v := atomic.AddUint64(&lfgSuggestionIDCounter, 1)
	return fmt.Sprintf("%x", v)
}

func storeSuggestionTitle(title string) string {
	id := newSuggestionID()
	lfgSuggestionMap.Lock()
	lfgSuggestionMap.idToTitle[id] = title
	lfgSuggestionMap.Unlock()
	return id
}

func fetchSuggestionTitle(id string) (string, bool) {
	lfgSuggestionMap.RLock()
	t, ok := lfgSuggestionMap.idToTitle[id]
	lfgSuggestionMap.RUnlock()
	return t, ok
}

// handleLFG processes /lfg commands (currently only setup)
func (h *SlashCommandHandler) handleLFG(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.ApplicationCommandData().Options) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Missing subcommand"}})
		return
	}

	sub := i.ApplicationCommandData().Options[0]
	switch sub.Name {
	case "setup-find-a-thread":
		h.handleLFGSetup(s, i)
	case "setup-looking-now":
		h.handleLFGSetupLookingNow(s, i)
	case "now":
		h.handleLFGNow(s, i)
	default:
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Unknown subcommand"}})
	}
}

// handleLFGSetup posts (or replaces) the LFG panel in the current channel.
func (h *SlashCommandHandler) handleLFGSetup(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
				Label:    "Find a thread",
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
func (h *SlashCommandHandler) handleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cid := i.MessageComponentData().CustomID
	switch {
	case cid == lfgPanelCustomID:
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
	case strings.HasPrefix(cid, lfgMoreSuggestionsPrefix+"::"):
		h.handleMoreSuggestions(s, i)
	case strings.HasPrefix(cid, lfgCreateSuggestionPrefix+"::"):
		h.handleCreateSuggestionThread(s, i)
	default:
		// ignore
	}
}

// Handle modal submission: look up / create thread then reply ephemerally with link.
func (h *SlashCommandHandler) handleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
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

	// 3. Gather partial thread suggestions (cache partial matches) up to 3 (only existing threads shown initially)
	partialThreadSuggestions := gatherPartialThreadSuggestionsDetailed(s, forumID, normalized, idOrEmpty(exactThreadChannel), 3)

	// Print exact match threads first
	var fields []*discordgo.MessageEmbedField
	if exactThreadChannel != nil {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: exactThreadChannel.Mention(),
		})
	}

	// Print partial match threads next
	for _, suggestion := range partialThreadSuggestions {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: suggestion.Mention(),
		})
	}
	if len(fields) == 0 {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  "No Results",
			Value: "Try a more specific title or click 'Show more suggestions'.",
		})
	}

	// Add Show More Suggestions button if we likely have more IGDB suggestions (searchRes.Suggestions length > 0 after filtering duplicates/exact)
	var components []discordgo.MessageComponent
	if len(searchRes.Suggestions) > 0 {
		components = []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Show more suggestions", CustomID: fmt.Sprintf("%s::%s", lfgMoreSuggestionsPrefix, normalized)},
			}},
		}
	}

	embed := &discordgo.MessageEmbed{
		Title:  "Found LFG thread(s)",
		Color:  utils.Colors.Fancy(),
		Fields: fields,
	}
	
	embedSlice := []*discordgo.MessageEmbed{embed}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embedSlice, Components: &components})
}

// findCachedExactThread validates and returns a cached exact thread channel if still valid.
func (h *SlashCommandHandler) findCachedExactThread(s *discordgo.Session, forumID, normalized string) (*discordgo.Channel, bool) {
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
func (h *SlashCommandHandler) createLFGThreadFromExactMatch(s *discordgo.Session, forumID, normalized string, exact *igdb.Game) (*discordgo.Channel, error) {
	if exact == nil {
		return nil, fmt.Errorf("nil exact game")
	}
	displayName := exact.Name
	var gameSummary string
	var playerLine string
	var linksLine string
	var coverURL string

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

	// Fetch cover art (used by Discord as forum thread preview if placed first in initial message)
	// We intentionally keep this lightweight; a cache could be added later if needed.
	// IGDB Game struct's Cover field is an ID referencing a cover resource containing image_id.
	if exact.Cover > 0 { // Cover is present
		if covers, err := h.igdbClient.Covers.List([]int{exact.Cover}, igdb.SetFields("image_id")); err == nil {
			if len(covers) > 0 && covers[0] != nil && covers[0].ImageID != "" {
				// Use a medium/large preset; can adjust size variant if needed (t_cover_big, t_1080p, etc.)
				coverURL = fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_cover_big/%s.jpg", covers[0].ImageID)
			}
		} else {
			h.config.Logger.Debugf("LFG: failed fetching cover for '%s': %v", displayName, err)
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

	initialParts := []string{}
	initialParts = append(initialParts, fmt.Sprintf("This is the LFG thread for _%s_! Use the LFG panel anytime to get a link.", displayName))
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

	var thread *discordgo.Channel
	var err error

	// If we have a cover image URL, try to download and attach it so the forum preview shows the image.
	if coverURL != "" {
		imgBytes, fileName, dlErr := downloadCoverImage(coverURL)
		if dlErr == nil && len(imgBytes) > 0 {
			thread, err = s.ForumThreadStartComplex(
				forumID,
				&discordgo.ThreadStart{ // basic thread metadata
					Name:                displayName,
					AutoArchiveDuration: 4320,
				},
				&discordgo.MessageSend{
					Content: initialContent,
					Files:   []*discordgo.File{{Name: fileName, ContentType: "image/jpeg", Reader: bytes.NewReader(imgBytes)}},
				},
			)
			if err != nil {
				h.config.Logger.Warnf("LFG: cover attach failed for '%s' (%v); falling back to no-image thread", displayName, err)
				thread = nil // force fallback below
			}
		} else if dlErr != nil {
			h.config.Logger.Debugf("LFG: failed downloading cover image for '%s': %v", displayName, dlErr)
		}
	}

	if thread == nil { // fallback simple creation
		thread, err = s.ForumThreadStart(forumID, displayName, 4320, initialContent)
		if err != nil {
			h.config.Logger.Errorf("LFG: failed creating forum thread '%s' in forum %s: %v", displayName, forumID, err)
			return nil, err
		}
	}
	lfgThreadCache.Lock()
	lfgThreadCache.nameToThreadID[normalized] = thread.ID
	lfgThreadCache.Unlock()
	return thread, nil
}

// gatherPartialThreadSuggestionsDetailed returns up to 'limit' partial match existing thread suggestions.
func gatherPartialThreadSuggestionsDetailed(s *discordgo.Session, forumID, normalized, excludeThreadID string, limit int) []discordgo.Channel {
	var out []discordgo.Channel
	lfgThreadCache.RLock()
	for k, id := range lfgThreadCache.nameToThreadID {
		if strings.Contains(strings.ToLower(k), normalized) {
			if excludeThreadID != "" && id == excludeThreadID {
				continue
			}
			ch, err := s.Channel(id)
			if err == nil && ch != nil && ch.ParentID == forumID {
				out = append(out, *ch)
				if len(out) >= limit {
					break
				}
			}
		}
	}
	lfgThreadCache.RUnlock()
	return out
}

// ---- More Suggestions Flow ----

// handleMoreSuggestions builds an embed with up to 9 IGDB title suggestions and buttons (1-5) to create threads.
func (h *SlashCommandHandler) handleMoreSuggestions(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if h.igdbClient == nil {
		return
	}
	cid := i.MessageComponentData().CustomID
	parts := strings.SplitN(cid, "::", 2)
	if len(parts) != 2 {
		return
	}
	queryNorm := parts[1]
	// Re-run search for suggestions
	searchRes, err := games.ExactMatchWithSuggestions(h.igdbClient, queryNorm)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("❌ error fetching suggestions: %v", err)}})
		return
	}

	var gameSuggestions []*igdb.Game
	if searchRes.ExactMatch != nil {
		gameSuggestions = append(gameSuggestions, searchRes.ExactMatch)
	}
	if len(searchRes.Suggestions) > 0 {
		gameSuggestions = append(gameSuggestions, searchRes.Suggestions...)
	}

	if searchRes == nil || len(gameSuggestions) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "No further suggestions available."}})
		return
	}

	// Collect up to 5 suggestion names (unique by lowercase)
	seen := make(map[string]struct{})
	var names []string
	for _, g := range gameSuggestions {
		if g == nil || g.Name == "" {
			continue
		}
		low := strings.ToLower(g.Name)
		if _, ok := seen[low]; ok {
			continue
		}
		seen[low] = struct{}{}
		names = append(names, g.Name)
		if len(names) >= 5 {
			break
		}
	}
	if len(names) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "No further suggestions available."}})
		return
	}

	// Prepare button mappings for first 5
	var btns []discordgo.MessageComponent
	for idx := 0; idx < len(names) && idx < 5; idx++ {
		id := storeSuggestionTitle(names[idx])
		btns = append(btns, &discordgo.Button{Style: discordgo.PrimaryButton, Label: fmt.Sprintf("%d", idx+1), CustomID: fmt.Sprintf("%s::%s", lfgCreateSuggestionPrefix, id)})
	}
	components := []discordgo.MessageComponent{}
	if len(btns) > 0 {
		components = append(components, discordgo.ActionsRow{Components: btns})
	}

	// Build suggestion list text
	var listBuilder strings.Builder
	for i, name := range names {
		listBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
	}
	listBuilder.WriteString("\nClick a numbered button (1-5) below to create a thread for that game.")

	embed := &discordgo.MessageEmbed{
		Title:  "More LFG Suggestions",
		Color:  utils.Colors.Fancy(),
		Fields: []*discordgo.MessageEmbedField{{Name: "Suggestions", Value: listBuilder.String()}},
	}
	embedSlice := []*discordgo.MessageEmbed{embed}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Embeds: embedSlice, Components: components}})
}

// handleCreateSuggestionThread creates a thread for selected suggestion and updates message with final embed.
func (h *SlashCommandHandler) handleCreateSuggestionThread(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if h.igdbClient == nil {
		return
	}
	cid := i.MessageComponentData().CustomID
	parts := strings.SplitN(cid, "::", 2)
	if len(parts) != 2 {
		return
	}
	id := parts[1]
	title, ok := fetchSuggestionTitle(id)
	if !ok || title == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ Unknown suggestion."}})
		return
	}
	forumID := h.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ LFG forum channel ID not configured."}})
		return
	}

	norm := strings.ToLower(title)
	// Reuse existing if present
	if ch, exists := h.findCachedExactThread(s, forumID, norm); exists {
		finalizeSuggestionThreadResponse(s, i, ch, false)
		return
	}

	// Search exact for this title
	res, err := games.ExactMatchWithSuggestions(h.igdbClient, title)
	if err != nil || res == nil || res.ExactMatch == nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ Unable to create thread (no exact match)."}})
		return
	}

	ch, err := h.createLFGThreadFromExactMatch(s, forumID, norm, res.ExactMatch)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ Failed creating thread."}})
		return
	}
	finalizeSuggestionThreadResponse(s, i, ch, true)
}

func finalizeSuggestionThreadResponse(s *discordgo.Session, i *discordgo.InteractionCreate, ch *discordgo.Channel, created bool) {
	status := "existing thread"
	if created {
		status = "created thread"
	}
	embed := &discordgo.MessageEmbed{
		Title: "Thread Created",
		Color: utils.Colors.Fancy(),
		Fields: []*discordgo.MessageEmbedField{
			{Name: fmt.Sprintf("%s (%s)", ch.Name, status), Value: fmt.Sprintf("- %s", threadLink(ch))},
		},
	}
	embedSlice := []*discordgo.MessageEmbed{embed}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Embeds: embedSlice}})
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

// downloadCoverImage fetches the cover image bytes and returns data, suggested filename, error.
// Discord requires an attachment for forum preview; we keep it simple and assume JPEG.
func downloadCoverImage(url string) ([]byte, string, error) {
	resp, err := http.Get(url) // #nosec G107 (trusted IGDB CDN URL built earlier)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	// Derive filename from URL path's last segment (strip query) and enforce .jpg
	base := path.Base(strings.Split(url, "?")[0])
	// IGDB cover URLs like t_cover_big/<image_id>.jpg
	jpgRe := regexp.MustCompile(`(?i)\.jpe?g$`)
	if !jpgRe.MatchString(base) {
		base = base + ".jpg"
	}
	if len(base) > 64 { // keep it short
		base = base[:64]
	}
	return data, base, nil
}

// Public wrappers used by bot interaction router

// HandleLFGComponent handles the LFG component interactions.
func (h *SlashCommandHandler) HandleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.handleLFGComponent(s, i)
}

// HandleLFGModalSubmit handles the submission of the LFG modal.
func (h *SlashCommandHandler) HandleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.handleLFGModalSubmit(s, i)
}
