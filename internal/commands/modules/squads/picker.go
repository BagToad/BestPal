package squads

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

// pickerCtx travels in every picker custom ID: the guild (the picker also
// works from a DM) and the intro thread the person joined from, if any.
type pickerCtx struct {
	GuildID  string
	ThreadID string
	Page     int // 0-based page of the picks list
}

func (c pickerCtx) id(action string) string {
	id := prefix + action + ":" + c.GuildID + ":" + c.ThreadID
	if c.Page > 0 {
		id += ":" + strconv.Itoa(c.Page)
	}
	return id
}

// parsePickerCtx reads a pickerCtx from custom ID parts after the action.
// IDs from before pagination have no page and open on the first page.
func parsePickerCtx(parts []string) pickerCtx {
	arg := func(n int) string {
		if n < len(parts) {
			return parts[n]
		}
		return ""
	}
	page, _ := strconv.Atoi(arg(3))
	return pickerCtx{GuildID: arg(1), ThreadID: arg(2), Page: max(page, 0)}
}

// picksPerPage is how many games the picker lists at once, keeping the
// message well under Discord's 2,000-character limit.
const picksPerPage = 10

// joinButton opens the game picker for owner (anyone, if owner is empty).
func joinButton(guildID, ownerID, threadID string) discordgo.Button {
	return discordgo.Button{
		Label:    "Join a squad",
		Emoji:    &discordgo.ComponentEmoji{Name: "🎮"},
		Style:    discordgo.SuccessButton,
		CustomID: prefix + "join:" + guildID + ":" + ownerID + ":" + threadID,
	}
}

const pickerIntro = "Add the games you play the most. You'll be matched with people in your region and age group who play the same games. If you're willing to try new games, you can also opt into matches based on shared genres."

// pickerView renders the picker: the current picks, Add/Finish buttons and a
// menu to remove a pick.
func (m *Module) pickerView(ctx pickerCtx, uid, note string) *discordgo.InteractionResponseData {
	picks, err := m.db.SquadPicks(ctx.GuildID, uid)
	if err != nil {
		m.config.Logger.Errorf("squads: load picks: %v", err)
		return ephemeral("❌ Could not load your games. Try again in a moment.")
	}
	tryNew, err := m.db.SquadTryNewGames(ctx.GuildID, uid)
	if err != nil {
		m.config.Logger.Errorf("squads: load try-new-games preference: %v", err)
		return ephemeral("❌ Could not load your squad preferences. Try again in a moment.")
	}
	anyRegion, err := m.db.SquadAnyRegion(ctx.GuildID, uid)
	if err != nil {
		m.config.Logger.Errorf("squads: load any-region preference: %v", err)
		return ephemeral("❌ Could not load your squad preferences. Try again in a moment.")
	}
	var b strings.Builder
	if note != "" {
		b.WriteString(truncate(note, 200) + "\n\n")
	}
	fmt.Fprintf(&b, "## 🎮 Your squad games (%d/%d)\n%s\n", len(picks), maxPicks, pickerIntro)
	if len(picks) == 0 {
		b.WriteString("\n*No games yet. Hit **Add a game** to search.*")
	}
	pages := max((len(picks)+picksPerPage-1)/picksPerPage, 1)
	ctx.Page = min(ctx.Page, pages-1)
	start := ctx.Page * picksPerPage
	end := min(start+picksPerPage, len(picks))
	for n := start; n < end; n++ {
		fmt.Fprintf(&b, "\n%d. %s", n+1, truncate(gameInfo{Name: picks[n].Name, Year: picks[n].Year}.label(), maxListedName))
	}
	if pages > 1 {
		fmt.Fprintf(&b, "\n-# Page %d/%d", ctx.Page+1, pages)
	}
	if tryNew {
		b.WriteString("\n\n✅ **Willing to try new games:** You may also be matched by shared genres.")
	} else {
		b.WriteString("\n\n**Willing to try new games?** Opt in to also be matched by shared genres.")
	}
	if anyRegion {
		b.WriteString("\n✅ **Any region:** You may join squads in other regions.")
	} else {
		b.WriteString("\n**Any region?** Opt in to match outside your region.")
	}

	finish := "Finish"
	if sq, ok, _ := m.db.SquadForUser(ctx.GuildID, uid); ok {
		fmt.Fprintf(&b, "\n\n-# You're in **%s**. Your games are saved for next time.", sq.Name)
		finish = "Save"
	} else if _, queued, _ := m.db.SquadQueueEntryFor(ctx.GuildID, uid); queued {
		b.WriteString("\n\n-# You're in the queue. Changes save as you go.")
		finish = "Done"
	}

	tryStyle := discordgo.SecondaryButton
	tryEmoji := "🎲"
	tryLabel := "I'm willing to try new games"
	tryAction := "trynewon"
	if tryNew {
		tryStyle = discordgo.SuccessButton
		tryEmoji = "✅"
		tryLabel = "Disable genre matching"
		tryAction = "trynewoff"
	}
	regionStyle := discordgo.SecondaryButton
	regionEmoji := "🌍"
	regionLabel := "I'll play with any region"
	regionAction := "anyregionon"
	if anyRegion {
		regionStyle = discordgo.SuccessButton
		regionEmoji = "✅"
		regionLabel = "Disable any-region matching"
		regionAction = "anyregionoff"
	}
	rows := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Add a game", Emoji: &discordgo.ComponentEmoji{Name: "➕"}, Style: discordgo.PrimaryButton,
			CustomID: ctx.id("add"), Disabled: len(picks) >= maxPicks},
		discordgo.Button{Label: finish, Emoji: &discordgo.ComponentEmoji{Name: "✅"}, Style: discordgo.SuccessButton,
			CustomID: ctx.id("done"), Disabled: len(picks) == 0},
	}}}
	if pages > 1 {
		prev, next := ctx, ctx
		prev.Page, next.Page = ctx.Page-1, ctx.Page+1
		row := rows[0].(discordgo.ActionsRow)
		row.Components = append(row.Components,
			discordgo.Button{Label: "Prev", Emoji: &discordgo.ComponentEmoji{Name: "◀️"}, Style: discordgo.SecondaryButton,
				CustomID: prev.id("pageprev"), Disabled: ctx.Page == 0},
			discordgo.Button{Label: "Next", Emoji: &discordgo.ComponentEmoji{Name: "▶️"}, Style: discordgo.SecondaryButton,
				CustomID: next.id("pagenext"), Disabled: ctx.Page >= pages-1})
		rows[0] = row
	}
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: tryLabel, Emoji: &discordgo.ComponentEmoji{Name: tryEmoji},
			Style: tryStyle, CustomID: ctx.id(tryAction)},
		discordgo.Button{Label: regionLabel, Emoji: &discordgo.ComponentEmoji{Name: regionEmoji},
			Style: regionStyle, CustomID: ctx.id(regionAction)},
	}})
	if len(picks) > 0 {
		var opts []discordgo.SelectMenuOption
		for _, p := range picks {
			opts = append(opts, discordgo.SelectMenuOption{
				Label: truncate(gameInfo{Name: p.Name, Year: p.Year}.label(), 100),
				Value: strconv.FormatInt(p.GameID, 10),
				Emoji: &discordgo.ComponentEmoji{Name: "✖️"},
			})
		}
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			CustomID: ctx.id("rm"), Placeholder: "Remove a game…", Options: opts,
		}}})
	}
	return &discordgo.InteractionResponseData{
		Content:         truncate(b.String(), 2000),
		Flags:           discordgo.MessageFlagsEphemeral,
		Components:      rows,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
}

// maxListedName bounds each game in the picker's list.
const maxListedName = 50

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// squadsOpen reports whether squads are enabled, telling the user if not.
func (m *Module) squadsOpen(s *discordgo.Session, i *discordgo.InteractionCreate, guildID string) bool {
	if guildID == "" || !m.config.ForGuild(guildID).GetSquadsEnabled() {
		m.respondText(s, i, "Squads aren't open right now. Check back later!")
		return false
	}
	return true
}

const blockedText = "This feature is currently disabled for you. Please ask a moderator for assistance."

// blockedNeedsLookup reports whether checking the blocked role needs a
// member lookup, because the interaction (e.g. from a DM) carries no roles.
// Callers defer first so a slow lookup can't miss Discord's reply deadline.
func (m *Module) blockedNeedsLookup(guildID string, i *discordgo.InteractionCreate) bool {
	return m.config.ForGuild(guildID).GetSquadsBlockedRoleID() != "" && (i.Member == nil || i.GuildID != guildID)
}

// squadsBlocked reports whether the person has the squads blocked role.
func (m *Module) squadsBlocked(guildID string, i *discordgo.InteractionCreate) (bool, error) {
	roleID := m.config.ForGuild(guildID).GetSquadsBlockedRoleID()
	if roleID == "" {
		return false, nil
	}
	if !m.blockedNeedsLookup(guildID, i) {
		return slices.Contains(i.Member.Roles, roleID), nil
	}
	mem, err := m.dc(nil).Member(guildID, userID(i))
	if err != nil {
		if discordCode(err) == discordgo.ErrCodeUnknownMember {
			return false, nil
		}
		return false, err
	}
	return slices.Contains(mem.Roles, roleID), nil
}

// joinBlockedText returns the message to show someone who can't join squads, or
// "" when they can.
func (m *Module) joinBlockedText(guildID string, i *discordgo.InteractionCreate) string {
	blocked, err := m.squadsBlocked(guildID, i)
	if err != nil {
		m.config.Logger.Warnf("squads: check blocked role for %s: %v", userID(i), err)
		return "❌ Could not check your squad access. Try again in a moment."
	}
	if blocked {
		return blockedText
	}
	return ""
}

// handleJoinButton opens the picker from an intro post or a re-join DM.
func (m *Module) handleJoinButton(s *discordgo.Session, i *discordgo.InteractionCreate, guildID, ownerID, threadID string) {
	uid := userID(i)
	if ownerID != "" && uid != ownerID {
		msg := "This button is for the person who posted this intro."
		if forum := m.config.ForGuild(guildID).GetGamerPalsIntroductionsForumChannelID(); forum != "" {
			msg += fmt.Sprintf(" Post your own intro in <#%s> to get one, or run `/squad join`.", forum)
		}
		m.respondText(s, i, msg)
		return
	}
	if !m.squadsOpen(s, i, guildID) {
		return
	}
	reply := m.respond
	if m.blockedNeedsLookup(guildID, i) {
		if !m.deferReply(s, i) {
			return
		}
		reply = m.edit
	}
	if msg := m.joinBlockedText(guildID, i); msg != "" {
		reply(s, i, ephemeral(msg))
		return
	}
	reply(s, i, m.pickerView(pickerCtx{GuildID: guildID, ThreadID: threadID}, uid, ""))
}

func (m *Module) handleAddButton(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx) {
	m.openSearchModal(s, i, ctx)
}

func (m *Module) openSearchModal(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx) {
	if err := m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: ctx.id("search"),
			Title:    "Add a game",
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{CustomID: "q", Label: "Game name", Style: discordgo.TextInputShort,
					Placeholder: "e.g. Deep Rock Galactic", Required: new(true), MinLength: 2, MaxLength: 100},
			}}},
		},
	}); err != nil {
		m.config.Logger.Warnf("squads: open search modal: %v", err)
	}
}

// handleSearchSubmit searches IGDB and shows the results as a menu.
func (m *Module) handleSearchSubmit(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx) {
	query := strings.TrimSpace(modalValue(i, "q"))
	// Show progress right away; the search makes several IGDB calls.
	if err := m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("⏳ Searching for **%s**…\n-# This can take a few seconds.", truncate(query, 80)),
			Components: []discordgo.MessageComponent{},
		},
	}); err != nil {
		m.config.Logger.Warnf("squads: search progress: %v", err)
		return
	}
	uid := userID(i)
	if m.games == nil {
		m.edit(s, i, m.pickerView(ctx, uid, "❌ Game search is unavailable right now."))
		return
	}
	results, err := m.games.Search(query)
	if err != nil {
		m.config.Logger.Warnf("squads: search %q: %v", query, err)
		m.edit(s, i, m.pickerView(ctx, uid, "❌ Game search failed. Try again in a moment."))
		return
	}
	if len(results) == 0 {
		m.edit(s, i, m.pickerView(ctx, uid, fmt.Sprintf("🔍 No games found for **%s**. Try another name.", truncate(query, 80))))
		return
	}
	if len(results) > maxPicks-1 {
		results = results[:maxPicks-1]
	}
	opts := make([]discordgo.SelectMenuOption, 0, len(results)+1)
	for _, g := range results {
		m.gameCache.Store(g.ID, g)
		opts = append(opts, discordgo.SelectMenuOption{
			Label:       truncate(g.label(), 100),
			Value:       strconv.FormatInt(g.ID, 10),
			Description: truncate(strings.Join(g.Tags, ", "), 100),
		})
	}
	opts = append(opts, discordgo.SelectMenuOption{Label: "Not here? Search again", Value: "again", Emoji: &discordgo.ComponentEmoji{Name: "🔍"}})
	m.edit(s, i, &discordgo.InteractionResponseData{
		Content: fmt.Sprintf("🔍 Results for **%s**. Pick your game:", truncate(query, 80)),
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
				CustomID: ctx.id("res"), Placeholder: "Pick a game", Options: opts,
			}}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "Back", Emoji: &discordgo.ComponentEmoji{Name: "⬅️"}, Style: discordgo.SecondaryButton, CustomID: ctx.id("back")},
			}},
		},
	})
}

func selected(i *discordgo.InteractionCreate) string {
	if v := i.MessageComponentData().Values; len(v) == 1 {
		return v[0]
	}
	return ""
}

// handleResultPick adds the chosen search result to the picks.
func (m *Module) handleResultPick(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx) {
	val := selected(i)
	if val == "again" {
		m.openSearchModal(s, i, ctx)
		return
	}
	id, ok := parseID(val)
	if !ok {
		m.update(s, i, m.pickerView(ctx, userID(i), "❌ Pick a game from the list."))
		return
	}
	if !m.deferUpdate(s, i) {
		return
	}
	uid := userID(i)
	var g gameInfo
	if cached, ok := m.gameCache.Load(id); ok {
		g = cached.(gameInfo)
	} else if m.games != nil {
		var err error
		if g, err = m.games.Get(id); err != nil {
			m.config.Logger.Warnf("squads: get game %d: %v", id, err)
			m.edit(s, i, m.pickerView(ctx, uid, "❌ Could not look that game up. Try again in a moment."))
			return
		}
	} else {
		m.edit(s, i, m.pickerView(ctx, uid, "❌ Game search is unavailable right now."))
		return
	}
	ctx.Page = maxPicks // clamped to the last page so the new game is visible
	n, added, err := m.db.AddSquadPick(ctx.GuildID, uid, database.SquadPick{GameID: g.ID, Name: g.Name, Year: g.Year, Tags: g.Tags}, maxPicks, m.now())
	var note string
	switch {
	case err != nil:
		m.config.Logger.Errorf("squads: add pick: %v", err)
		note = "❌ Could not save that game. Try again in a moment."
	case added && n >= maxPicks:
		note = fmt.Sprintf("✅ Added **%s**. That's the max of %d games, hit Finish when you're happy.", g.Name, maxPicks)
	case added:
		note = fmt.Sprintf("✅ Added **%s**.", g.Name)
	case n >= maxPicks:
		note = fmt.Sprintf("You already have %d games, the most you can pick. Remove one to add another.", maxPicks)
	default:
		note = fmt.Sprintf("You already picked **%s**.", g.Name)
	}
	m.edit(s, i, m.pickerView(ctx, uid, note))
}

func (m *Module) handleRemovePick(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx) {
	uid := userID(i)
	id, ok := parseID(selected(i))
	if !ok {
		m.update(s, i, m.pickerView(ctx, uid, ""))
		return
	}
	note := "🗑️ Removed."
	if err := m.db.RemoveSquadPick(ctx.GuildID, uid, id); err != nil {
		m.config.Logger.Errorf("squads: remove pick: %v", err)
		note = "❌ Could not remove that game. Try again in a moment."
	}
	m.update(s, i, m.pickerView(ctx, uid, note))
}

func (m *Module) handleBack(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx) {
	m.update(s, i, m.pickerView(ctx, userID(i), ""))
}

func (m *Module) handleTryNewGames(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx, enabled bool) {
	if !m.deferUpdate(s, i) {
		return
	}
	uid := userID(i)
	lock := m.guildLock(ctx.GuildID)
	lock.Lock()
	err := m.db.SetSquadTryNewGames(ctx.GuildID, uid, enabled)
	lock.Unlock()
	if err != nil {
		m.config.Logger.Errorf("squads: update try-new-games preference: %v", err)
		m.edit(s, i, m.pickerView(ctx, uid, "❌ Could not save that preference. Try again in a moment."))
		return
	}
	note := "Genre matching is off. We'll only match you by exact games."
	if enabled {
		note = "✅ You're open to trying new games. We may also match you by shared genres."
	}
	m.edit(s, i, m.pickerView(ctx, uid, note))
}

func (m *Module) handleAnyRegion(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx, enabled bool) {
	if !m.deferUpdate(s, i) {
		return
	}
	uid := userID(i)
	lock := m.guildLock(ctx.GuildID)
	lock.Lock()
	err := m.db.SetSquadAnyRegion(ctx.GuildID, uid, enabled)
	lock.Unlock()
	if err != nil {
		m.config.Logger.Errorf("squads: update any-region preference: %v", err)
		m.edit(s, i, m.pickerView(ctx, uid, "❌ Could not save that preference. Try again in a moment."))
		return
	}
	note := "Any-region matching is off. We'll match you in your region, or a nearby one if it's quiet."
	if enabled {
		note = "✅ You're open to any region. We may match you with people from other regions (expect some lag)."
	}
	m.edit(s, i, m.pickerView(ctx, uid, note))
}

// handleDone puts the person in the queue (or just saves, if they're
// already in a squad).
func (m *Module) handleDone(s *discordgo.Session, i *discordgo.InteractionCreate, ctx pickerCtx) {
	uid := userID(i)
	if !m.config.ForGuild(ctx.GuildID).GetSquadsEnabled() {
		m.update(s, i, &discordgo.InteractionResponseData{Content: "Squads aren't open right now. Your games are saved for when they are!"})
		return
	}
	if !m.deferUpdate(s, i) {
		return
	}
	if msg := m.joinBlockedText(ctx.GuildID, i); msg != "" {
		m.editText(s, i, msg)
		return
	}
	picks, err := m.db.SquadPicks(ctx.GuildID, uid)
	if err != nil {
		m.edit(s, i, m.pickerView(ctx, uid, "❌ Could not load your games. Try again in a moment."))
		return
	}
	if len(picks) == 0 {
		m.edit(s, i, m.pickerView(ctx, uid, "Add at least one game first."))
		return
	}

	lock := m.guildLock(ctx.GuildID)
	lock.Lock()
	sq, inSquad, err := m.db.SquadForUser(ctx.GuildID, uid)
	if err == nil && !inSquad {
		err = m.db.EnqueueSquadUser(ctx.GuildID, uid, ctx.ThreadID, m.now())
	}
	lock.Unlock()

	switch {
	case err != nil:
		m.config.Logger.Errorf("squads: enqueue %s: %v", uid, err)
		m.edit(s, i, m.pickerView(ctx, uid, "❌ Could not join the queue. Try again in a moment."))
	case inSquad:
		m.edit(s, i, &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ Saved your games. You're in **%s** (<#%s>).", sq.Name, sq.ChannelID),
			Components: changeGamesRow(ctx),
		})
	default:
		wait := m.config.ForGuild(ctx.GuildID).GetSquadsFallbackAfter()
		m.edit(s, i, &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("🎉 **You're in the squad queue** with %s!\nWe'll match you with people who play the same stuff, usually within %s. "+
				"You'll get pinged in your new squad's channel. You can leave the queue any time with `/squad leave`.",
				plural(len(picks), "game", "games"), humanDuration(wait)),
			Components: changeGamesRow(ctx),
		})
	}
}

func changeGamesRow(ctx pickerCtx) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Change games", Emoji: &discordgo.ComponentEmoji{Name: "✏️"}, Style: discordgo.SecondaryButton, CustomID: ctx.id("back")},
	}}}
}

// handleSquad handles /squad join and /squad leave.
func (m *Module) handleSquad(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.db == nil {
		m.respondText(s, i, "❌ Database is unavailable.")
		return
	}
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		return
	}
	uid := userID(i)
	switch opts[0].Name {
	case "join":
		if !m.squadsOpen(s, i, i.GuildID) {
			return
		}
		if msg := m.joinBlockedText(i.GuildID, i); msg != "" {
			m.respondText(s, i, msg)
			return
		}
		m.respond(s, i, m.pickerView(pickerCtx{GuildID: i.GuildID, ThreadID: m.lookupIntro(i.GuildID, uid)}, uid, ""))
	case "leave":
		// Squad members get the exit survey, which must be the first response.
		if sq, ok, err := m.db.SquadForUser(i.GuildID, uid); err == nil && ok {
			m.openExitSurvey(s, i, sq)
			return
		}
		// Hold the guild lock so a running matcher can't place someone
		// after they're told they left the queue.
		if !m.deferReply(s, i) {
			return
		}
		lock := m.guildLock(i.GuildID)
		lock.Lock()
		data := m.leaveQueue(i.GuildID, uid)
		lock.Unlock()
		m.edit(s, i, data)
	case "rename":
		var name string
		for _, o := range opts[0].Options {
			if o.Name == "name" {
				name = o.StringValue()
			}
		}
		if !m.deferReply(s, i) {
			return
		}
		m.editText(s, i, m.renameSquad(i.GuildID, uid, name))
	}
}

// leaveQueue takes someone out of the queue, or points them at leaving the
// squad they were just placed in. Callers hold the guild lock.
func (m *Module) leaveQueue(guildID, uid string) *discordgo.InteractionResponseData {
	sq, ok, err := m.db.SquadForUser(guildID, uid)
	if err != nil {
		return ephemeral("❌ Could not load your squad. Try again in a moment.")
	}
	if ok {
		return leaveConfirm(sq)
	}
	removed, err := m.db.DequeueSquadUser(guildID, uid)
	switch {
	case err != nil:
		return ephemeral("❌ Could not leave the queue. Try again in a moment.")
	case removed:
		return ephemeral("👋 You've left the squad queue. Your games are saved if you want to rejoin with `/squad join`.")
	}
	return ephemeral("You're not in a squad or the queue.")
}

func leaveConfirm(sq database.Squad) *discordgo.InteractionResponseData {
	data := ephemeral(fmt.Sprintf("Leave **%s**? You'll lose access to its channel. You can join a new squad later.", sq.Name))
	data.Components = []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Leave squad", Emoji: &discordgo.ComponentEmoji{Name: "🚪"}, Style: discordgo.DangerButton,
			CustomID: fmt.Sprintf("%sleave:%d", prefix, sq.ID)},
	}}}
	return data
}

func (m *Module) handleLeaveButton(s *discordgo.Session, i *discordgo.InteractionCreate, idStr string) {
	id, _ := parseID(idStr)
	sq, ok, err := m.db.SquadForUser(i.GuildID, userID(i))
	switch {
	case err != nil:
		m.respondText(s, i, "❌ Could not load your squad. Try again in a moment.")
	case !ok || sq.ID != id:
		m.respondText(s, i, "You're not in this squad.")
	default:
		m.openExitSurvey(s, i, sq)
	}
}

// handleLeaveConfirm leaves straight away from confirmations sent before the
// exit survey existed.
func (m *Module) handleLeaveConfirm(s *discordgo.Session, i *discordgo.InteractionCreate, idStr string) {
	id, _ := parseID(idStr)
	if !m.deferUpdate(s, i) {
		return
	}
	uid := userID(i)
	lock := m.guildLock(i.GuildID)
	lock.Lock()
	sq, err := m.removeMember(i.GuildID, uid, id, database.SquadLeaveVoluntary, "left")
	lock.Unlock()
	switch {
	case errors.Is(err, errNotMember):
		m.editText(s, i, "You're not in this squad anymore.")
	case err != nil:
		m.config.Logger.Errorf("squads: leave: %v", err)
		m.editText(s, i, "❌ Could not leave the squad. Try again in a moment.")
	default:
		m.editText(s, i, fmt.Sprintf("👋 You left **%s**. Want a new squad? Run `/squad join`.", sq.Name))
	}
}
