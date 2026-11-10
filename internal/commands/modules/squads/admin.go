package squads

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

// handleAdmin handles /squad-admin.
func (m *Module) handleAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		m.respondText(s, i, "❌ You need the Ban Members permission to manage squads.")
		return
	}
	if m.db == nil {
		m.respondText(s, i, "❌ Database is unavailable.")
		return
	}
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		return
	}
	sub := opts[0]
	args := map[string]*discordgo.ApplicationCommandInteractionDataOption{}
	for _, o := range sub.Options {
		args[o.Name] = o
	}
	userArg := func() string {
		if o := args["user"]; o != nil {
			return fmt.Sprint(o.Value)
		}
		return ""
	}
	squadArg := func() int64 {
		if o := args["squad"]; o != nil {
			id, _ := parseID(o.StringValue())
			return id
		}
		return 0
	}

	switch sub.Name {
	case "status":
		m.respondText(s, i, m.statusText(i.GuildID))
	case "user":
		m.respondText(s, i, m.userText(i.GuildID, userArg()))
	case "dryrun", "run-now":
		m.adminRun(s, i, sub.Name == "dryrun")
	case "add":
		m.adminLocked(s, i, func() string { return m.adminAdd(i, userArg(), squadArg()) })
	case "remove":
		m.adminLocked(s, i, func() string { return m.adminRemove(i, userArg()) })
	case "queue-remove":
		uid := userArg()
		removed, err := m.db.DequeueSquadUser(i.GuildID, uid)
		switch {
		case err != nil:
			m.respondText(s, i, "❌ Could not update the queue.")
		case !removed:
			m.respondText(s, i, mention(uid)+" isn't in the queue.")
		default:
			m.modLog(i.GuildID, fmt.Sprintf("%s took %s out of the queue.", mention(userID(i)), mention(uid)))
			m.respondText(s, i, "✅ Took "+mention(uid)+" out of the queue.")
		}
	case "delete":
		sq, ok, err := m.db.GetSquad(squadArg())
		if err != nil || !ok || sq.GuildID != i.GuildID {
			m.respondText(s, i, "❌ Pick a squad from the list.")
			return
		}
		data := ephemeral(fmt.Sprintf("Delete **%s** (<#%s>, %s)? Its channel and role are deleted for good.",
			sq.Name, sq.ChannelID, plural(sq.Members, "member", "members")))
		data.Components = []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Delete squad", Style: discordgo.DangerButton, CustomID: fmt.Sprintf("%sadel:%d", prefix, sq.ID)},
		}}}
		m.respond(s, i, data)
	case "refresh-permissions":
		m.adminLocked(s, i, func() string { return m.refreshPermissions(i.GuildID) })
	}
}

// adminLocked defers, runs fn under the guild lock and shows its result.
func (m *Module) adminLocked(s *discordgo.Session, i *discordgo.InteractionCreate, fn func() string) {
	if !m.deferReply(s, i) {
		return
	}
	lock := m.guildLock(i.GuildID)
	lock.Lock()
	out := fn()
	lock.Unlock()
	m.editText(s, i, out)
}

func (m *Module) adminAdd(i *discordgo.InteractionCreate, uid string, squadID int64) string {
	sq, ok, err := m.db.GetSquad(squadID)
	if err != nil || !ok || sq.GuildID != i.GuildID {
		return "❌ Pick a squad from the list."
	}
	if cur, in, _ := m.db.SquadForUser(i.GuildID, uid); in {
		return fmt.Sprintf("❌ %s is already in **%s**. Remove them first.", mention(uid), cur.Name)
	}
	picks, _ := m.db.SquadPicks(i.GuildID, uid)
	c, _, err := m.loadCandidate(i.GuildID, uid, picks, m.roleLookup(i.GuildID))
	if err != nil {
		return "❌ Could not find that member in the server."
	}
	if err := m.addMember(i.GuildID, sq, c); err != nil {
		if errors.Is(err, database.ErrSquadMember) {
			return "❌ They're already in a squad."
		}
		m.config.Logger.Errorf("squads: admin add: %v", err)
		return "❌ Could not add them: " + err.Error()
	}
	m.modLog(i.GuildID, fmt.Sprintf("%s added %s to **%s**.", mention(userID(i)), mention(uid), sq.Name))
	return fmt.Sprintf("✅ Added %s to **%s**.", mention(uid), sq.Name)
}

func (m *Module) adminRemove(i *discordgo.InteractionCreate, uid string) string {
	sq, err := m.removeMember(i.GuildID, uid, 0, "was removed from")
	if errors.Is(err, errNotMember) {
		return mention(uid) + " isn't in a squad."
	}
	if err != nil {
		m.config.Logger.Errorf("squads: admin remove: %v", err)
		return "❌ Could not remove them: " + err.Error()
	}
	m.modLog(i.GuildID, fmt.Sprintf("%s removed %s from **%s**.", mention(userID(i)), mention(uid), sq.Name))
	return fmt.Sprintf("✅ Removed %s from **%s**.", mention(uid), sq.Name)
}

func (m *Module) handleAdminDeleteConfirm(s *discordgo.Session, i *discordgo.InteractionCreate, idStr string) {
	if !m.canManage(i) {
		m.respondText(s, i, "❌ You need the Ban Members permission to manage squads.")
		return
	}
	id, _ := parseID(idStr)
	if !m.deferUpdate(s, i) {
		return
	}
	lock := m.guildLock(i.GuildID)
	lock.Lock()
	sq, ok, err := m.db.GetSquad(id)
	if err == nil && ok && sq.GuildID == i.GuildID {
		m.deleteSquad(sq, "a mod ("+mention(userID(i))+") deleted it")
	}
	lock.Unlock()
	if err != nil || !ok {
		m.editText(s, i, "That squad is already gone.")
		return
	}
	m.editText(s, i, fmt.Sprintf("🗑️ Deleted **%s**.", sq.Name))
}

func (m *Module) refreshPermissions(guildID string) string {
	squads, err := m.db.ListSquads(guildID)
	if err != nil {
		return "❌ Could not load squads."
	}
	failed := 0
	for _, sq := range squads {
		if err := m.dc(nil).EditChannelPerms(sq.ChannelID, m.squadOverwrites(guildID, sq.RoleID)); err != nil {
			m.config.Logger.Warnf("squads: refresh perms of %s: %v", sq.Name, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Sprintf("⚠️ Updated %d of %d squads. Check the logs for the rest.", len(squads)-failed, len(squads))
	}
	return fmt.Sprintf("✅ Updated permissions on %s.", plural(len(squads), "squad", "squads"))
}

func (m *Module) adminRun(s *discordgo.Session, i *discordgo.InteractionCreate, dry bool) {
	if !m.deferReply(s, i) {
		return
	}
	lock := m.guildLock(i.GuildID)
	lock.Lock()
	report, err := m.runMatch(i.GuildID, dry)
	lock.Unlock()
	if err != nil {
		m.config.Logger.Errorf("squads: admin run: %v", err)
		m.editText(s, i, "❌ Matching failed: "+err.Error())
		return
	}
	if !dry {
		m.modLog(i.GuildID, fmt.Sprintf("%s ran pairing: %d new squads, %d late joins, %d unmatched.",
			mention(userID(i)), len(report.Created), len(report.Plan.Joins), len(report.Plan.Unmatched)))
	}
	m.editText(s, i, reportText(report, dry))
}

func names(group []*candidate) string {
	parts := make([]string, len(group))
	for n, c := range group {
		parts[n] = mention(c.UserID)
	}
	return strings.Join(parts, " ")
}

func reportText(r matchReport, dry bool) string {
	var b strings.Builder
	if dry {
		b.WriteString("🧪 **Dry run**: nothing was changed.\n")
	} else {
		b.WriteString("✅ **Pairing ran.**\n")
	}
	fmt.Fprintf(&b, "Queue: %d · open young squads: %d\n", r.Queued, r.Open)
	if len(r.Plan.NewSquads) > 0 {
		b.WriteString("\n**New squads**\n")
		for _, g := range r.Plan.NewSquads {
			fmt.Fprintf(&b, "• %s\n  -# %s\n", names(g), commonGround(g))
		}
	}
	if len(r.Plan.Joins) > 0 {
		b.WriteString("\n**Late joins**\n")
		for _, j := range r.Plan.Joins {
			fmt.Fprintf(&b, "• %s → %s\n", mention(j.User.UserID), r.OpenNames[j.SquadID])
		}
	}
	if len(r.Plan.Unmatched) > 0 {
		verb := "taken out of the queue and DMed"
		if dry {
			verb = "would be taken out of the queue and DMed"
		}
		fmt.Fprintf(&b, "\n**Unmatched** (%s)\n%s\n", verb, names(r.Plan.Unmatched))
	}
	if len(r.Left) > 0 {
		fmt.Fprintf(&b, "\n%s left the server and %s the queue.\n", plural(len(r.Left), "person", "people"),
			map[bool]string{true: "would be dropped from", false: "were dropped from"}[dry])
	}
	if len(r.Plan.Waiting) > 0 {
		fmt.Fprintf(&b, "\n**Still waiting:** %s\n", names(r.Plan.Waiting))
	}
	if len(r.Failures) > 0 {
		b.WriteString("\n⚠️ **Problems**\n")
		for _, f := range r.Failures {
			b.WriteString("• " + f + "\n")
		}
	}
	if len(r.Plan.NewSquads)+len(r.Plan.Joins)+len(r.Plan.Unmatched)+len(r.Plan.Waiting) == 0 {
		b.WriteString("\nNobody to match.")
	}
	return truncate(b.String(), 2000)
}

func (m *Module) statusText(guildID string) string {
	gc := m.config.ForGuild(guildID)
	now := m.now()
	var b strings.Builder
	b.WriteString("## 🧑‍🤝‍🧑 Squads status\n")

	var problems []string
	if !gc.GetSquadsEnabled() {
		problems = append(problems, "Squads are **off** (no intro buttons, no pairing).")
	}
	if gc.GetSquadsCategoryID() == "" {
		problems = append(problems, "No squads category set, so squads can't be created.")
	}
	if len(gc.GetSquadRoles(config.SquadRegionKeys)) == 0 {
		problems = append(problems, "No region roles set, so everyone counts as the same region.")
	}
	if len(gc.GetSquadRoles(config.SquadAgeKeys)) == 0 {
		problems = append(problems, "No age group roles set, so age isn't used.")
	}
	if gc.GetSquadsModRoleID() == "" {
		problems = append(problems, "No mod role set, so mods can't see private squads.")
	}
	for _, p := range problems {
		b.WriteString("⚠️ " + p + "\n")
	}

	queue, err := m.db.SquadQueue(guildID)
	if err != nil {
		return "❌ Could not load the queue."
	}
	fmt.Fprintf(&b, "\n**Queue:** %d", len(queue))
	if len(queue) > 0 {
		rl := m.roleLookup(guildID)
		counts := map[string]int{}
		for _, e := range queue {
			region := "unknown"
			if mem, err := m.dc(nil).Member(guildID, e.UserID); err == nil {
				c := &candidate{}
				rl.apply(c, mem.Roles)
				if c.Region != "" {
					region = regionName(c.Region)
				}
			}
			counts[region]++
		}
		var parts []string
		for r, n := range counts {
			parts = append(parts, fmt.Sprintf("%s %d", r, n))
		}
		sort.Strings(parts)
		fmt.Fprintf(&b, " (%s) · longest wait %s", strings.Join(parts, ", "), humanDuration(now.Sub(queue[0].QueuedAt)))
	}

	if next := m.nextMatch(guildID); next.IsZero() || !next.After(now) {
		b.WriteString("\n**Next pairing:** at the next check (within 5 minutes)")
	} else {
		fmt.Fprintf(&b, "\n**Next pairing:** <t:%d:R>", next.Unix())
	}

	squads, err := m.db.ListSquads(guildID)
	if err != nil {
		return "❌ Could not load squads."
	}
	fmt.Fprintf(&b, "\n**Live squads:** %d", len(squads))
	young := gc.GetSquadsYoungFor()
	_, _, maxSize := gc.GetSquadsSizes()
	for _, sq := range squads {
		fmt.Fprintf(&b, "\n• %s <#%s>: %s, last chat <t:%d:R>", sq.Name, sq.ChannelID, plural(sq.Members, "member", "members"), sq.LastActivityAt.Unix())
		if now.Sub(sq.CreatedAt) < young && sq.Members < maxSize {
			b.WriteString(" · taking late joiners")
		}
		if !sq.WarnedAt.IsZero() {
			fmt.Fprintf(&b, " · ⚠️ deleting <t:%d:R>", sq.WarnedAt.Add(gc.GetSquadsQuietDeleteAfter()).Unix())
		}
	}
	return truncate(b.String(), 2000)
}

func (m *Module) userText(guildID, uid string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n", mention(uid))
	if sq, ok, _ := m.db.SquadForUser(guildID, uid); ok {
		fmt.Fprintf(&b, "**Squad:** %s <#%s>\n", sq.Name, sq.ChannelID)
	} else if e, ok, _ := m.db.SquadQueueEntryFor(guildID, uid); ok {
		fmt.Fprintf(&b, "**Queue:** waiting since <t:%d:R> (%s)\n", e.QueuedAt.Unix(), humanDuration(m.now().Sub(e.QueuedAt)))
	} else {
		b.WriteString("Not in a squad or the queue.\n")
	}
	if mem, err := m.dc(nil).Member(guildID, uid); err == nil {
		c := &candidate{}
		m.roleLookup(guildID).apply(c, mem.Roles)
		region, age := "unknown", "unknown"
		if c.Region != "" {
			region = regionName(c.Region)
		}
		if c.Age > 0 {
			age = fmt.Sprintf("group %d", c.Age)
		}
		var plats []string
		for _, p := range platforms {
			if c.Platforms[p.Code] {
				plats = append(plats, p.Name)
			}
		}
		if len(plats) == 0 {
			plats = []string{"none"}
		}
		fmt.Fprintf(&b, "**Region:** %s · **Age:** %s · **Platforms:** %s\n", region, age, strings.Join(plats, ", "))
	}
	picks, err := m.db.SquadPicks(guildID, uid)
	if err != nil {
		return "❌ Could not load their games."
	}
	fmt.Fprintf(&b, "**Games (%d):**", len(picks))
	if len(picks) == 0 {
		b.WriteString(" none")
	}
	for _, p := range picks {
		fmt.Fprintf(&b, "\n• %s", gameInfo{Name: p.Name, Year: p.Year}.label())
		if len(p.Tags) > 0 {
			fmt.Fprintf(&b, " *(%s)*", strings.Join(p.Tags, ", "))
		}
	}
	return truncate(b.String(), 2000)
}

// HandleAutocomplete suggests squads for /squad-admin.
func (m *Module) HandleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var query string
	for _, sub := range i.ApplicationCommandData().Options {
		for _, o := range sub.Options {
			if o.Focused {
				query = strings.ToLower(o.StringValue())
			}
		}
	}
	choices := []*discordgo.ApplicationCommandOptionChoice{}
	if m.db != nil && m.canManage(i) {
		squads, _ := m.db.ListSquads(i.GuildID)
		for _, sq := range squads {
			if query != "" && !strings.Contains(strings.ToLower(sq.Name), query) {
				continue
			}
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  fmt.Sprintf("%s (%s)", sq.Name, plural(sq.Members, "member", "members")),
				Value: fmt.Sprint(sq.ID),
			})
			if len(choices) == 25 {
				break
			}
		}
	}
	_ = m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}
