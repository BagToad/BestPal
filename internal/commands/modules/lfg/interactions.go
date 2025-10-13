package lfg

import (
	"fmt"
	"gamerpal/internal/games"
	"gamerpal/internal/utils"
	"strconv"
	"strings"
	"time"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Handle component interactions (button press -> show modal)
func (m *LfgModule) handleLFGComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cid := i.MessageComponentData().CustomID
	switch {
	case cid == lfgPanelCustomID:
		// Show modal to gather game name
		modal := buildLFGModal()
		if err := s.InteractionRespond(i.Interaction, modal); err != nil {
			m.config.Logger.Errorf("LFG: failed to open modal: %v", err)
		}
	case strings.HasPrefix(cid, lfgMoreSuggestionsPrefix+"::"):
		m.handleMoreSuggestions(s, i)
	case strings.HasPrefix(cid, lfgCreateSuggestionPrefix+"::"):
		m.handleCreateSuggestionThread(s, i)
	default:
		// ignore
	}
}

// Handle modal submission: look up / create thread then reply ephemerally with link.
func (m *LfgModule) handleLFGModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ModalSubmitData().CustomID != lfgModalCustomID {
		return
	}
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
		m.config.Logger.Warnf("LFG modal submit: game name input not found in components; customID=%s", i.ModalSubmitData().CustomID)
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
		m.config.Logger.Errorf("LFG: failed to defer modal submit: %v", err)
		return
	}

	if m.igdbClient == nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: fmtPtr("❌ IGDB client is not initialized. Admin intervention required"),
		})
		return
	}

	// 1. Attempt to find existing thread from cache (validated)
	exactThreadChannel, _ := m.findCachedExactThread(s, forumID, normalized)

	// 2. Perform search (exact + suggestions)
	searchRes, err := games.ExactMatchWithSuggestions(m.igdbClient, gameName)
	if err != nil {
		m.config.Logger.Errorf("LFG: failed to search IGDB for '%s': %v", gameName, err)
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
			Name: "No Results",
		})
	}

	// Add Show More Suggestions button if we likely have more IGDB suggestions (searchRes.Suggestions length > 0 after filtering duplicates/exact)
	var components []discordgo.MessageComponent
	if len(searchRes.Suggestions) > 0 || searchRes.ExactMatch != nil {
		components = []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Create a thread", CustomID: fmt.Sprintf("%s::%s", lfgMoreSuggestionsPrefix, gameName)},
			}},
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: "Click \"Create a thread\" to find more options and create a thread!",
		})
	}

	// Log the search and threads shown to user
	userMention := "Member"
	if i.Member != nil {
		userMention = i.Member.Mention()
	}

	var threadsShown []string
	if exactThreadChannel != nil {
		threadsShown = append(threadsShown, exactThreadChannel.Name)
	}
	for _, suggestion := range partialThreadSuggestions {
		threadsShown = append(threadsShown, suggestion.Name)
	}

	logDescription := fmt.Sprintf("%s searched for **\"%s\"**", userMention, gameName)
	if len(threadsShown) > 0 {
		logDescription += fmt.Sprintf("\n\n**Threads shown:**\n• %s", strings.Join(threadsShown, "\n• "))
	} else {
		logDescription += "\n\n**No threads found**"
	}

	if err := utils.LogToChannel(m.config, s, logDescription); err != nil {
		m.config.Logger.Errorf("LFG: failed to log search results: %v", err)
	}

	embed := foundThreadsEmbed(fields)

	embedSlice := []*discordgo.MessageEmbed{embed}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embedSlice, Components: &components})
}

// handleMoreSuggestions builds an embed with up to 9 IGDB title suggestions and buttons (1-5) to create threads.
func (m *LfgModule) handleMoreSuggestions(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.igdbClient == nil {
		return
	}
	cid := i.MessageComponentData().CustomID
	parts := strings.SplitN(cid, "::", 2)
	if len(parts) != 2 {
		return
	}
	gameName := parts[1]
	// Re-run search for suggestions
	searchRes, err := games.ExactMatchWithSuggestions(m.igdbClient, gameName)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("❌ error fetching suggestions: %v", err)}})
		return
	}

	var gameSuggestions []*igdb.Game
	if searchRes != nil {
		if searchRes.ExactMatch != nil {
			gameSuggestions = append(gameSuggestions, searchRes.ExactMatch)
		}
		if len(searchRes.Suggestions) > 0 {
			gameSuggestions = append(gameSuggestions, searchRes.Suggestions...)
		}
	}

	if len(gameSuggestions) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "No further suggestions available."}})
		return
	}

	// Collect up to 5 suggestion games (with potential duplicate names distinguished by year)
	var picked []*igdb.Game
	seen := make(map[string]struct{})
	for _, g := range gameSuggestions {
		if g == nil || g.Name == "" {
			continue
		}
		// Compute release year (0 if unknown) for dedupe key
		year := 0
		if g.FirstReleaseDate > 0 {
			year = time.Unix(int64(g.FirstReleaseDate), 0).UTC().Year()
		}
		key := strings.ToLower(g.Name) + "::" + strconv.Itoa(year)
		if _, exists := seen[key]; exists {
			continue // duplicate title+year, skip
		}
		seen[key] = struct{}{}
		picked = append(picked, g)
		if len(picked) >= 5 {
			break
		}
	}
	if len(picked) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "No further suggestions available."}})
		return
	}

	// Prepare button mappings using the real IGDB game ID so we can disambiguate identical titles.
	var btns []discordgo.MessageComponent
	for idx, g := range picked {
		btns = append(btns, &discordgo.Button{Style: discordgo.PrimaryButton, Label: fmt.Sprintf("%d", idx+1), CustomID: fmt.Sprintf("%s::%d", lfgCreateSuggestionPrefix, g.ID)})
	}
	components := []discordgo.MessageComponent{}
	if len(btns) > 0 {
		components = append(components, discordgo.ActionsRow{Components: btns})
	}

	// Build suggestion list text with year (from first_release_date) when available
	var listBuilder strings.Builder
	var gameNames []string
	for i, g := range picked {
		yearStr := ""
		if g.FirstReleaseDate > 0 { // epoch seconds
			y := time.Unix(int64(g.FirstReleaseDate), 0).UTC().Year()
			yearStr = fmt.Sprintf(" (%d)", y)
		}
		gameName := fmt.Sprintf("%s%s", g.Name, yearStr)
		listBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, gameName))
		gameNames = append(gameNames, gameName)
	}
	listBuilder.WriteString("\nClick a numbered button (1-5) below to create a thread for that game.")

	// Log the game suggestions shown to the user
	userMention := "Member"
	if i.Member != nil {
		userMention = i.Member.Mention()
	}
	logDescription := fmt.Sprintf("%s clicked to create a thread for **\"%s\"**\n\n**Game suggestions shown:**\n• %s",
		userMention, gameName, strings.Join(gameNames, "\n• "))
	if err := utils.LogToChannel(m.config, s, logDescription); err != nil {
		m.config.Logger.Errorf("LFG: failed to log game suggestions: %v", err)
	}

	embed := createThreadSuggestionsEmbed(listBuilder.String())
	embedSlice := []*discordgo.MessageEmbed{embed}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Embeds: embedSlice, Components: components}})
}

// handleCreateSuggestionThread creates a thread for selected suggestion and updates message with final embed.
func (m *LfgModule) handleCreateSuggestionThread(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.igdbClient == nil {
		return
	}
	cid := i.MessageComponentData().CustomID
	parts := strings.SplitN(cid, "::", 2)
	if len(parts) != 2 {
		return
	}
	gameIDStr := parts[1]
	gameID, err := strconv.Atoi(gameIDStr)
	if err != nil || gameID <= 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ Invalid suggestion."}})
		return
	}
	forumID := m.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ LFG forum channel ID not configured."}})
		return
	}

	// Fetch the specific game by ID to ensure correctness when duplicate titles exist.
	gamesList, err := m.igdbClient.Games.List([]int{gameID}, igdb.SetFields("id", "name", "summary", "websites", "multiplayer_modes", "cover", "first_release_date"))
	if err != nil || len(gamesList) == 0 || gamesList[0] == nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ Unable to fetch game details."}})
		return
	}
	game := gamesList[0]
	if game.Name == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ Game has no name."}})
		return
	}

	norm := strings.ToLower(game.Name)
	if ch, exists := m.findCachedExactThread(s, forumID, norm); exists {
		m.logThreadCreationOutcome(s, i, game.Name, ch, false)
		finalizeSuggestionThreadResponse(s, i, ch, false)
		return
	}

	ch, err := m.createLFGThreadFromExactMatch(s, forumID, norm, game)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "❌ Failed creating thread."}})
		return
	}
	m.logThreadCreationOutcome(s, i, game.Name, ch, true)
	finalizeSuggestionThreadResponse(s, i, ch, true)
}

// finalizeSuggestionThreadResponse sends the final response after thread creation
func finalizeSuggestionThreadResponse(s *discordgo.Session, i *discordgo.InteractionCreate, ch *discordgo.Channel, created bool) {
	embed := threadCreatedEmbed(ch, created)
	embedSlice := []*discordgo.MessageEmbed{embed}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Embeds: embedSlice}})
}

// logThreadCreationOutcome logs when a user selects a game and the outcome
func (m *LfgModule) logThreadCreationOutcome(s *discordgo.Session, i *discordgo.InteractionCreate, gameName string, ch *discordgo.Channel, created bool) {
	userMention := "Member"
	if i.Member != nil {
		userMention = i.Member.Mention()
	}

	outcome := "returned existing thread"
	if created {
		outcome = "created new thread"
	}

	logDescription := fmt.Sprintf("%s selected **\"%s\"**\n\n**Outcome:** %s\n**Thread:** %s",
		userMention, gameName, outcome, ch.Mention())

	if err := utils.LogToChannel(m.config, s, logDescription); err != nil {
		m.config.Logger.Errorf("LFG: failed to log thread creation outcome: %v", err)
	}
}
