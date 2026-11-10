package squads

import (
	"errors"
	"fmt"
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
}

func (c pickerCtx) id(action string) string {
	return prefix + action + ":" + c.GuildID + ":" + c.ThreadID
}

// joinButton opens the game picker for owner (anyone, if owner is empty).
func joinButton(guildID, ownerID, threadID string) discordgo.Button {
	return discordgo.Button{
		Label:    "Join a squad",
		Emoji:    &discordgo.ComponentEmoji{Name: "🎮"},
		Style:    discordgo.SuccessButton,
		CustomID: prefix + "join:" + guildID + ":" + ownerID + ":" + threadID,
	}
}

const pickerIntro = "Add the games you play the most. You'll be matched with people in your region and age group who play the same games (or similar ones). The more you add, the better your chances."

// pickerView renders the picker: the current picks, Add/Finish buttons and a
// menu to remove a pick.
func (m *Module) pickerView(ctx pickerCtx, uid, note string) *discordgo.InteractionResponseData {
	picks, err := m.db.SquadPicks(ctx.GuildID, uid)
	if err != nil {
		m.config.Logger.Errorf("squads: load picks: %v", err)
		return ephemeral("❌ Could not load your games. Try again in a moment.")
	}
	var b strings.Builder
	if note != "" {
		b.WriteString(truncate(note, 200) + "\n\n")
	}
	fmt.Fprintf(&b, "## 🎮 Your squad games (%d/%d)\n%s\n", len(picks), maxPicks, pickerIntro)
	if len(picks) == 0 {
		b.WriteString("\n*No games yet. Hit **Add a game** to search.*")
	}
	for n, p := range picks {
		fmt.Fprintf(&b, "\n%d. %s", n+1, truncate(gameInfo{Name: p.Name, Year: p.Year}.label(), maxListedName))
	}

	finish := "Finish"
	if sq, ok, _ := m.db.SquadForUser(ctx.GuildID, uid); ok {
		fmt.Fprintf(&b, "\n\n-# You're in **%s**. Your games are saved for next time.", sq.Name)
		finish = "Save"
	} else if _, queued, _ := m.db.SquadQueueEntryFor(ctx.GuildID, uid); queued {
		b.WriteString("\n\n-# You're in the queue. Changes save as you go.")
		finish = "Done"
	}

	rows := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Add a game", Emoji: &discordgo.ComponentEmoji{Name: "➕"}, Style: discordgo.PrimaryButton,
			CustomID: ctx.id("add"), Disabled: len(picks) >= maxPicks},
		discordgo.Button{Label: finish, Emoji: &discordgo.ComponentEmoji{Name: "✅"}, Style: discordgo.SuccessButton,
			CustomID: ctx.id("done"), Disabled: len(picks) == 0},
	}}}
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

// maxListedName bounds each game in the picker's list so 25 picks fit in a
// 2,000-character message.
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
	m.respond(s, i, m.pickerView(pickerCtx{GuildID: guildID, ThreadID: threadID}, uid, ""))
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
		m.respond(s, i, m.pickerView(pickerCtx{GuildID: i.GuildID, ThreadID: m.lookupIntro(i.GuildID, uid)}, uid, ""))
	case "leave":
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
	}
}

// leaveQueue takes someone out of the queue, or asks them to confirm leaving
// their squad. Callers hold the guild lock.
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
			CustomID: fmt.Sprintf("%sleaveyes:%d", prefix, sq.ID)},
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
		m.respond(s, i, leaveConfirm(sq))
	}
}

func (m *Module) handleLeaveConfirm(s *discordgo.Session, i *discordgo.InteractionCreate, idStr string) {
	id, _ := parseID(idStr)
	if !m.deferUpdate(s, i) {
		return
	}
	uid := userID(i)
	lock := m.guildLock(i.GuildID)
	lock.Lock()
	sq, err := m.removeMember(i.GuildID, uid, id, "left")
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
