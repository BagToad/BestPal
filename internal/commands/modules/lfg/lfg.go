package lfg

import (
	"bytes"
	"fmt"
	"gamerpal/internal/utils"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Legacy in-memory cache removed in favor of centralized forumcache service.
// All lookups now delegate to forumCache.GetThreadByExactName / SearchThreads.

const (
	lfgPanelCustomID          = "lfg_panel_open_modal"
	lfgModalCustomID          = "lfg_game_modal"
	lfgModalInputCustomID     = "lfg_game_name"
	lfgMoreSuggestionsPrefix  = "lfg_more_suggestions"  // lfg_more_suggestions::<normalizedQuery>
	lfgCreateSuggestionPrefix = "lfg_create_suggestion" // lfg_create_suggestion::<id>
)

// handleLFG processes /lfg and /lfg-admin commands
func (m *LfgModule) handleLFG(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if len(i.ApplicationCommandData().Options) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Missing subcommand"}})
		return
	}

	sub := i.ApplicationCommandData().Options[0]
	switch sub.Name {
	case "setup-find-a-thread":
		m.handleLFGSetup(s, i)
	case "setup-looking-now":
		m.handleLFGSetupLookingNow(s, i)
	case "now":
		m.handleLFGNow(s, i)
	case "refresh-thread-cache":
		m.handleLFGRefreshCache(s, i)
	default:
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "❌ Unknown subcommand"}})
	}
}

// handleLFGRefreshCache rebuilds the in-memory LFG thread cache (admin only command path).
func (m *LfgModule) handleLFGRefreshCache(s *discordgo.Session, i *discordgo.InteractionCreate) {
	forumID := m.config.GetGamerPalsLFGForumChannelID()
	introForum := m.config.GetGamerPalsIntroductionsForumChannelID() // optional second forum
	guildID := m.config.GetGamerPalsServerID()
	if forumID == "" || guildID == "" {
		_ = s.InteractionRespond(i.Interaction,
			&discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{Content: "❌ Missing guild or LFG forum config.", Flags: discordgo.MessageFlagsEphemeral},
			},
		)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}})

	// Perform refreshes (best-effort) using centralized forum cache service.
	var lfgErr, introErr error
	m.forumCache.RegisterForum(forumID)
	lfgErr = m.forumCache.RefreshForum(guildID, forumID)
	if introForum != "" {
		m.forumCache.RegisterForum(introForum)
		introErr = m.forumCache.RefreshForum(guildID, introForum)
	}

	lfgStats, _ := m.forumCache.Stats(forumID)
	introStats, _ := m.forumCache.Stats(introForum)

	var lines []string
	if lfgErr == nil {
		lines = append(lines, fmt.Sprintf("LFG: threads=%d owners=%d", lfgStats.Threads, lfgStats.OwnersTracked))
	} else {
		lines = append(lines, "LFG: failed refresh")
	}
	if introForum != "" {
		if introErr == nil {
			lines = append(lines, fmt.Sprintf("Intro: threads=%d owners=%d", introStats.Threads, introStats.OwnersTracked))
		} else {
			lines = append(lines, "Intro: failed refresh")
		}
	}

	content := "✅ Forum cache refresh complete.\n" + strings.Join(lines, "\n")
	if lfgErr != nil || introErr != nil {
		content = "⚠️ Partial forum cache refresh.\n" + strings.Join(lines, "\n")
	}

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})

	// Log summary
	if i.Member != nil {
		logMsg := fmt.Sprintf("%s triggered forum cache refresh. %s", i.Member.User.Mention(), strings.Join(lines, " | "))
		if err := utils.LogToChannel(m.config, s, logMsg); err != nil {
			m.config.Logger.Warnf("Failed to log forum cache refresh: %v", err)
		}
	}
}

// handleGameThread searches for a game thread in the cache and returns a link or not found message.
func (m *LfgModule) handleGameThread(s *discordgo.Session, i *discordgo.InteractionCreate) {
	forumID := m.config.GetGamerPalsLFGForumChannelID()
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

	// Extract command options
	var searchQuery string
	ephemeral := true // Default to true

	for _, opt := range i.ApplicationCommandData().Options {
		switch opt.Name {
		case "search-query":
			searchQuery = opt.StringValue()
		case "ephemeral":
			ephemeral = opt.BoolValue()
		}
	}

	searchQuery = strings.TrimSpace(searchQuery)
	if searchQuery == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Search query required.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	normalized := strings.ToLower(searchQuery)

	// Use forum cache exact + search
	m.forumCache.RegisterForum(forumID) // idempotent
	var threadID string
	if exact, ok := m.forumCache.GetThreadByExactName(forumID, normalized); ok {
		threadID = exact.ID
	} else {
		// fallback to scored search buckets
		results, ok2 := m.forumCache.SearchThreads(forumID, normalized, 5)
		if ok2 && len(results) > 0 {
			threadID = results[0].ID // best candidate
		}
	}

	// Determine flags based on ephemeral setting
	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	if threadID == "" {
		embed := utils.NewNoResultsEmbed(fmt.Sprintf("No game thread found for **\"%s\"**", searchQuery))
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{embed},
				Flags:  flags,
			},
		})

		// Log to log channel
		userMention := "Member"
		if i.Member != nil {
			userMention = i.Member.Mention()
		}
		logMsg := fmt.Sprintf("%s used `/game-thread` with query **\"%s\"** (ephemeral: %t)\n\n**Result:** No thread found", userMention, searchQuery, ephemeral)
		if err := utils.LogToChannel(m.config, s, logMsg); err != nil {
			m.config.Logger.Errorf("Failed to log game-thread result: %v", err)
		}

		return
	}

	// Get the thread channel to verify and get details
	// threadID already set above

	ch, err := s.Channel(threadID)
	if err != nil || ch == nil || ch.ParentID != forumID {
		// Thread stale; forum cache will reconcile on next refresh/event automatically.

		embed := utils.NewNoResultsEmbed(fmt.Sprintf("No game thread found for **\"%s\"**", searchQuery))
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{embed},
				Flags:  flags,
			},
		})

		// Log to log channel
		userMention := "Member"
		if i.Member != nil {
			userMention = i.Member.Mention()
		}
		logMsg := fmt.Sprintf("%s used `/game-thread` with query **\"%s\"** (ephemeral: %t)\n\n**Result:** No thread found (stale cache entry)", userMention, searchQuery, ephemeral)
		if err := utils.LogToChannel(m.config, s, logMsg); err != nil {
			m.config.Logger.Errorf("Failed to log game-thread result: %v", err)
		}

		return
	}

	// Return the thread link
	embed := utils.NewOKEmbed("🎮 Game Thread Lookup", threadLink(ch))
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  flags,
		},
	})

	// Log to log channel
	userMention := "Member"
	if i.Member != nil {
		userMention = i.Member.Mention()
	}
	logMsg := fmt.Sprintf("%s used `/game-thread` with query **\"%s\"** (ephemeral: %t)\n\n**Result:** Found thread %s", userMention, searchQuery, ephemeral, ch.Mention())
	if err := utils.LogToChannel(m.config, s, logMsg); err != nil {
		m.config.Logger.Errorf("Failed to log game-thread result: %v", err)
	}
}

// handleGameThreadAutocomplete handles autocomplete requests for the game-thread command
func (m *LfgModule) handleGameThreadAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Get the current input value
	var currentInput string
	if len(data.Options) > 0 {
		if opt := data.Options[0]; opt.Focused {
			currentInput = opt.StringValue()
		}
	}

	currentInput = strings.TrimSpace(strings.ToLower(currentInput))

	var choices []*discordgo.ApplicationCommandOptionChoice
	forumID := m.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionApplicationCommandAutocompleteResult})
		return
	}
	m.forumCache.RegisterForum(forumID)

	if currentInput == "" {
		// List threads (up to 25 newest) via forum cache
		threads, ok := m.forumCache.ListThreads(forumID)
		if ok {
			sort.Slice(threads, func(i, j int) bool {
				if threads[i].CreatedAt.Equal(threads[j].CreatedAt) {
					return threads[i].ID > threads[j].ID
				}
				return threads[i].CreatedAt.After(threads[j].CreatedAt)
			})
			for i, tm := range threads {
				if i >= 25 {
					break
				}
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: tm.Name, Value: tm.Name})
			}
		}
	} else {
		results, ok := m.forumCache.SearchThreads(forumID, currentInput, 25)
		if ok {
			for _, tm := range results {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: tm.Name, Value: tm.Name})
			}
		}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionApplicationCommandAutocompleteResult, Data: &discordgo.InteractionResponseData{Choices: choices}})
}

// handleLFGSetup posts (or replaces) the LFG panel in the current channel.
func (m *LfgModule) handleLFGSetup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	forumID := m.config.GetGamerPalsLFGForumChannelID()
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

// createLFGThreadFromExactMatch builds metadata + creates the forum thread for an exact IGDB match.
func (m *LfgModule) createLFGThreadFromExactMatch(s *discordgo.Session, forumID string, exact *igdb.Game) (*discordgo.Channel, error) {
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
		if sites, err := m.igdbClient.Websites.List(exact.Websites, igdb.SetFields("url", "category")); err == nil {
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
		if covers, err := m.igdbClient.Covers.List([]int{exact.Cover}, igdb.SetFields("image_id")); err == nil {
			if len(covers) > 0 && covers[0] != nil && covers[0].ImageID != "" {
				// Use a medium/large preset; can adjust size variant if needed (t_cover_big, t_1080p, etc.)
				coverURL = fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_cover_big/%s.jpg", covers[0].ImageID)
			}
		} else {
			m.config.Logger.Debugf("LFG: failed fetching cover for '%s': %v", displayName, err)
		}
	}

	if len(exact.MultiplayerModes) > 0 {
		if modes, err := m.igdbClient.MultiplayerModes.List(exact.MultiplayerModes, igdb.SetFields("*")); err == nil {
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
				m.config.Logger.Warnf("LFG: cover attach failed for '%s' (%v); falling back to no-image thread", displayName, err)
				thread = nil // force fallback below
			}
		} else if dlErr != nil {
			m.config.Logger.Debugf("LFG: failed downloading cover image for '%s': %v", displayName, dlErr)
		}
	}

	if thread == nil { // fallback simple creation
		thread, err = s.ForumThreadStart(forumID, displayName, 4320, initialContent)
		if err != nil {
			m.config.Logger.Errorf("LFG: failed creating forum thread '%s' in forum %s: %v", displayName, forumID, err)
			return nil, err
		}
	}
	return thread, nil
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

func (m *LfgModule) findCachedExactThread(s *discordgo.Session, forumID, normalized string) (*discordgo.Channel, bool) {
	if forumID == "" || normalized == "" {
		return nil, false
	}
	m.forumCache.RegisterForum(forumID)
	meta, ok := m.forumCache.GetThreadByExactName(forumID, normalized)
	if !ok || meta == nil {
		return nil, false
	}
	ch, err := s.Channel(meta.ID)
	if err != nil || ch == nil || ch.ParentID != forumID {
		return nil, false // stale or not found
	}
	return ch, true
}

func (m *LfgModule) gatherPartialThreadSuggestionsDetailed(s *discordgo.Session, forumID, searchTerm, excludeThreadID string, limit int) []discordgo.Channel {
	searchTerm = strings.TrimSpace(strings.ToLower(searchTerm))
	if forumID == "" || searchTerm == "" || limit <= 0 {
		return nil
	}
	m.forumCache.RegisterForum(forumID)
	results, ok := m.forumCache.SearchThreads(forumID, searchTerm, limit+5) // fetch a little extra for exclusion filtering
	if !ok || len(results) == 0 {
		return nil
	}
	var out []discordgo.Channel
	for _, meta := range results {
		if meta.ID == excludeThreadID { // skip exact already shown
			continue
		}
		ch, err := s.Channel(meta.ID)
		if err != nil || ch == nil || ch.ParentID != forumID {
			continue
		}
		out = append(out, *ch)
		if len(out) >= limit {
			break
		}
	}
	return out
}

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
func (m *LfgModule) HandleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleLFGComponent(s, i)
}

// HandleLFGModalSubmit handles the submission of the LFG modal.
func (m *LfgModule) HandleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleLFGModalSubmit(s, i)
}
