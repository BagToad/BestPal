package commands

import (
	"fmt"
	"strings"
	"sync"

	"gamerpal/internal/games"

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

	threadChannel, err := h.ensureLFGThread(s, forumID, gameName, normalized)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: fmtPtr(fmt.Sprintf("❌ %v", err)),
		})
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
	cacheRes, ok := LFGCacheSearch(normalized)
	lfgThreadCache.RUnlock()

	if ok && cacheRes.ExactThreadID != "" {
		// Validate exact thread still exists
		h.config.Logger.Infof("LFG: cache hit exact for '%s' -> %s", displayName, cacheRes.ExactThreadID)
		ch, err := s.Channel(cacheRes.ExactThreadID)
		if err == nil && ch != nil && ch.ParentID == forumID { // still valid
			return ch, nil
		}

		// That exact hit is invalid/no longer exists, so remove invalid cache entry
		lfgThreadCache.Lock()
		delete(lfgThreadCache.nameToThreadID, normalized)
		lfgThreadCache.Unlock()

		// Continue onwards. We'll need to either create a new thread if
		// we can get an exact game name match from IGDB, or return some
		// suggestions.
	}

	// Ensure IGDB client is available before continuing.
	// We put this check here so that the cache logic above can continue
	// even if IGDB is not linked.
	if h.igdbClient == nil {
		return nil, fmt.Errorf("IGDB client is not initialized. Admin intervention required")
	}

	// Validate game exists using IGDB: fetch up to 10 candidates, pick exact (case-insensitive) or return suggestions.
	var gameSummary string
	var playerLine string
	var linksLine string

	igdbSearchResult, err := games.ExactMatchWithSuggestions(h.igdbClient, displayName)
	if err != nil {
		return nil, fmt.Errorf("error looking up game %s: %w", displayName, err)
	}

	h.config.Logger.Debugf("%+v", igdbSearchResult)

	// No exact match on game title.
	// The only thing we can do now is show suggestions including:
	// - any existing partial match threads
	// - any not-exact game names returned from IGDB
	if igdbSearchResult != nil && igdbSearchResult.ExactMatch == nil {
		h.config.Logger.Infof("LFG: no exact IGDB match for '%s'", displayName)
		return nil, lfgThreadSuggestionsResponseErr(s, &cacheRes, igdbSearchResult, forumID)
	}

	// We have an exact match on game title, so assume that's what the user wants.
	// At this point, there wasn't already a thread for this game, so we will
	// create it.
	if igdbSearchResult != nil && igdbSearchResult.ExactMatch != nil {
		h.config.Logger.Infof("LFG: creating new thread for exact match '%s'", displayName)
		// This logic prepares the thread post content with some game metadata.

		exact := igdbSearchResult.ExactMatch
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
	lfgThreadCache.nameToThreadID[normalized] = thread.ID
	lfgThreadCache.Unlock()
	return thread, nil
}

func lfgThreadSuggestionsResponseErr(s *discordgo.Session, cacheResponse *LFGCacheSearchResult, gameSearchResponse *games.GameSearchResult, forumID string) error {
	errString := strings.Builder{}

	haveSomethingToSay := len(cacheResponse.PartialThreadIDs) > 0 && len(gameSearchResponse.Suggestions) > 0
	if !haveSomethingToSay {
		errString.WriteString("I couldn't find any matches, sorry! Please try again\n")
		return fmt.Errorf("%s", errString.String())
	}

	errString.WriteString("I couldn't find exact matches, but maybe one of these will do?\n")

	// If we have partial match thread IDs from cache,
	// that means we have some channels to link as suggestions.
	if len(cacheResponse.PartialThreadIDs) > 0 {
		errString.WriteString("\nExisting threads:\n")
		for _, id := range cacheResponse.PartialThreadIDs {
			// Ensure the channel exists and is a child of the forum
			ch, err := s.Channel(id)
			if err == nil && ch != nil && ch.ParentID == forumID {
				errString.WriteString(threadLink(ch) + "\n")
			}
		}
	}

	// If we have suggestions in our gameSearchResponse, let's add those too.
	if len(gameSearchResponse.Suggestions) > 0 {
		errString.WriteString("\nSuggested game titles:\n")
		for _, suggestion := range gameSearchResponse.Suggestions {
			errString.WriteString(fmt.Sprintf("- \"_%s_\"\n", suggestion.Name))
		}
	}

	return fmt.Errorf("%s", errString.String())
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
