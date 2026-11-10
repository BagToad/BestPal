package squads

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

var errNotMember = errors.New("not in that squad")

var squadNames = []string{
	"Axolotl", "Badger", "Capybara", "Dingo", "Echidna", "Ferret", "Gecko", "Hedgehog", "Ibis", "Jackal",
	"Koala", "Lemur", "Manatee", "Narwhal", "Ocelot", "Pangolin", "Quokka", "Raccoon", "Sloth", "Tapir",
	"Urchin", "Vulture", "Walrus", "Yak", "Zebra", "Alpaca", "Beaver", "Cheetah", "Dolphin", "Emu",
	"Flamingo", "Gorilla", "Hippo", "Iguana", "Jaguar", "Kiwi", "Llama", "Meerkat", "Newt", "Otter",
	"Penguin", "Quail", "Red Panda", "Seal", "Toucan", "Vole", "Wombat", "Armadillo", "Bison", "Chinchilla",
	"Puffin", "Fennec", "Gazelle", "Heron", "Impala", "Kingfisher", "Lynx", "Mongoose", "Nightjar", "Orca",
	"Platypus", "Raven", "Salamander", "Tortoise", "Wolverine", "Mantis", "Porcupine", "Stoat", "Marmot", "Kakapo",
}

// pickName returns a squad name not used by any live squad in the guild.
func (m *Module) pickName(existing []database.Squad) string {
	used := map[string]bool{}
	for _, sq := range existing {
		used[sq.Name] = true
	}
	var free []string
	for _, n := range squadNames {
		if !used["Squad "+n] {
			free = append(free, n)
		}
	}
	if len(free) > 0 {
		return "Squad " + free[m.randN(len(free))]
	}
	base := squadNames[m.randN(len(squadNames))]
	for n := 2; ; n++ {
		if name := fmt.Sprintf("Squad %s %d", base, n); !used[name] {
			return name
		}
	}
}

func channelName(squadName string) string {
	return strings.ReplaceAll(strings.ToLower(squadName), " ", "-")
}

// overwrites builds a squad channel's permissions. Members and mods can chat;
// everyone else can read (public_readonly) or can't see it (private).
func overwrites(guildID, roleID, modRoleID, botID, visibility string) []*discordgo.PermissionOverwrite {
	const chat = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory |
		discordgo.PermissionSendMessagesInThreads | discordgo.PermissionAddReactions | discordgo.PermissionAttachFiles |
		discordgo.PermissionEmbedLinks
	everyone := &discordgo.PermissionOverwrite{ID: guildID, Type: discordgo.PermissionOverwriteTypeRole,
		Deny: discordgo.PermissionSendMessages | discordgo.PermissionSendMessagesInThreads |
			discordgo.PermissionCreatePublicThreads | discordgo.PermissionCreatePrivateThreads}
	if visibility == config.SquadVisibilityPrivate {
		everyone.Deny |= discordgo.PermissionViewChannel
	}
	out := []*discordgo.PermissionOverwrite{
		everyone,
		{ID: roleID, Type: discordgo.PermissionOverwriteTypeRole, Allow: chat},
	}
	if modRoleID != "" {
		out = append(out, &discordgo.PermissionOverwrite{ID: modRoleID, Type: discordgo.PermissionOverwriteTypeRole,
			Allow: chat | discordgo.PermissionManageMessages})
	}
	if botID != "" {
		out = append(out, &discordgo.PermissionOverwrite{ID: botID, Type: discordgo.PermissionOverwriteTypeMember,
			Allow: chat | discordgo.PermissionManageMessages})
	}
	return out
}

func (m *Module) squadOverwrites(guildID, roleID string) []*discordgo.PermissionOverwrite {
	gc := m.config.ForGuild(guildID)
	return overwrites(guildID, roleID, gc.GetSquadsModRoleID(), m.dc(nil).BotID(), gc.GetSquadsVisibility())
}

func (m *Module) registerChannel(channelID string, id int64) {
	m.chMu.Lock()
	defer m.chMu.Unlock()
	m.channels[channelID] = id
}

func (m *Module) unregisterChannel(channelID string) {
	m.chMu.Lock()
	defer m.chMu.Unlock()
	delete(m.channels, channelID)
}

// createSquad creates the role, channel and welcome post for a new group.
// Callers hold the guild lock.
func (m *Module) createSquad(guildID string, group []*candidate) (database.Squad, error) {
	gc := m.config.ForGuild(guildID)
	category := gc.GetSquadsCategoryID()
	if category == "" {
		return database.Squad{}, errors.New("the squads category isn't set in /config → Squads")
	}
	existing, err := m.db.ListSquads(guildID)
	if err != nil {
		return database.Squad{}, err
	}
	name := m.pickName(existing)
	api := m.dc(nil)

	role, err := api.CreateRole(guildID, &discordgo.RoleParams{Name: name, Mentionable: new(true), Permissions: new(int64(0))})
	if err != nil {
		return database.Squad{}, fmt.Errorf("create role: %w", err)
	}
	ch, err := api.CreateChannel(guildID, discordgo.GuildChannelCreateData{
		Name:                 channelName(name),
		Type:                 discordgo.ChannelTypeGuildText,
		Topic:                name + ": a small group of pals matched on the games you play. Say hi!",
		ParentID:             category,
		PermissionOverwrites: m.squadOverwrites(guildID, role.ID),
	})
	if err != nil {
		_ = api.DeleteRole(guildID, role.ID)
		return database.Squad{}, fmt.Errorf("create channel: %w", err)
	}

	ids := make([]string, len(group))
	for n, c := range group {
		ids[n] = c.UserID
	}
	sq, err := m.db.CreateSquad(database.Squad{GuildID: guildID, Name: name, ChannelID: ch.ID, RoleID: role.ID}, ids, m.now())
	if err != nil {
		_ = api.DeleteChannel(ch.ID)
		_ = api.DeleteRole(guildID, role.ID)
		return database.Squad{}, fmt.Errorf("save squad: %w", err)
	}
	m.registerChannel(ch.ID, sq.ID)

	for _, uid := range ids {
		if err := api.AddRole(guildID, uid, role.ID); err != nil {
			m.config.Logger.Warnf("squads: give %s the %s role: %v", uid, name, err)
		}
	}

	msg, err := api.Send(ch.ID, &discordgo.MessageSend{
		Content:         welcomeText(name, role.ID, group, gc.GetSquadsQuietWarnAfter()+gc.GetSquadsQuietDeleteAfter()),
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: ids},
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Leave squad", Emoji: &discordgo.ComponentEmoji{Name: "🚪"}, Style: discordgo.SecondaryButton,
				CustomID: fmt.Sprintf("%sleave:%d", prefix, sq.ID)},
		}}},
	})
	if err != nil {
		m.config.Logger.Warnf("squads: welcome post in %s: %v", name, err)
		return sq, nil
	}
	if err := api.Pin(ch.ID, msg.ID); err != nil {
		m.config.Logger.Warnf("squads: pin welcome in %s: %v", name, err)
	}
	if err := m.db.SetSquadWelcome(sq.ID, msg.ID); err != nil {
		m.config.Logger.Warnf("squads: save welcome for %s: %v", name, err)
	}
	return sq, nil
}

func welcomeText(name, roleID string, group []*candidate, quietLimit time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 🎉 Welcome to %s!\n", name)
	for n, c := range group {
		if n > 0 {
			b.WriteString(" ")
		}
		b.WriteString(mention(c.UserID))
	}
	b.WriteString("\n\n" + commonGround(group))
	fmt.Fprintf(&b, "\n\nThis is your squad's channel. Ping everyone with <@&%s>.", roleID)
	fmt.Fprintf(&b, "\n-# Squads that go %s without chatting are deleted.", humanDuration(quietLimit))
	return b.String()
}

// commonGround describes what a group shares, for the welcome post.
func commonGround(group []*candidate) string {
	games, tags := sharedSummary(group)
	var parts []string
	for n, g := range games {
		if n == 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%d of you play **%s**", g.Count, g.Name))
	}
	var tagNames []string
	for n, t := range tags {
		if n == 3 {
			break
		}
		tagNames = append(tagNames, "**"+t.Name+"**")
	}
	switch {
	case len(parts) > 0 && len(tagNames) > 0:
		return "You were matched because " + joinAnd(parts) + ". You're also into " + joinAnd(tagNames) + "."
	case len(parts) > 0:
		return "You were matched because " + joinAnd(parts) + "."
	case len(tagNames) > 0:
		return "You were matched because you're all into " + joinAnd(tagNames) + "."
	}
	return "You were matched as new pals in the same part of the world."
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// addMember adds someone to an existing squad. Callers hold the guild lock.
func (m *Module) addMember(guildID string, sq database.Squad, c *candidate) error {
	if err := m.db.AddSquadMember(guildID, sq.ID, c.UserID, m.now()); err != nil {
		return err
	}
	api := m.dc(nil)
	if err := api.AddRole(guildID, c.UserID, sq.RoleID); err != nil {
		m.config.Logger.Warnf("squads: give %s the %s role: %v", c.UserID, sq.Name, err)
	}
	text := "👋 " + mention(c.UserID) + " joined the squad!"
	var names []string
	for _, name := range c.Games {
		names = append(names, "**"+name+"**")
	}
	slices.Sort(names)
	if len(names) > 5 {
		names = append(names[:5], "more")
	}
	if len(names) > 0 {
		text += " They play " + joinAnd(names) + "."
	}
	if _, err := api.Send(sq.ChannelID, &discordgo.MessageSend{
		Content:         text,
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{c.UserID}},
	}); err != nil {
		m.config.Logger.Warnf("squads: announce join in %s: %v", sq.Name, err)
	}
	return nil
}

// removeMember takes someone out of their squad (only if it's squadID, when
// non-zero), deleting the squad if fewer than two remain. Callers hold the
// guild lock.
func (m *Module) removeMember(guildID, uid string, squadID int64, why string) (database.Squad, error) {
	sq, ok, err := m.db.SquadForUser(guildID, uid)
	if err != nil {
		return sq, err
	}
	if !ok || (squadID != 0 && sq.ID != squadID) {
		return sq, errNotMember
	}
	if _, _, err := m.db.RemoveSquadMember(guildID, uid); err != nil {
		return sq, err
	}
	sq.Members--
	api := m.dc(nil)
	if err := api.RemoveRole(guildID, uid, sq.RoleID); err != nil && discordCode(err) != discordgo.ErrCodeUnknownMember {
		m.config.Logger.Warnf("squads: remove %s role from %s: %v", sq.Name, uid, err)
	}
	if sq.Members < 2 {
		m.deleteSquad(sq, "it dropped below 2 members")
		return sq, nil
	}
	if _, err := api.Send(sq.ChannelID, &discordgo.MessageSend{
		Content:         fmt.Sprintf("👋 %s %s the squad.", mention(uid), why),
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("squads: announce leave in %s: %v", sq.Name, err)
	}
	return sq, nil
}

// deleteSquad removes a squad's channel, role and records. Callers hold the
// guild lock.
func (m *Module) deleteSquad(sq database.Squad, why string) {
	api := m.dc(nil)
	if sq.ChannelID != "" {
		if err := api.DeleteChannel(sq.ChannelID); err != nil && discordCode(err) != discordgo.ErrCodeUnknownChannel {
			m.config.Logger.Warnf("squads: delete channel of %s: %v", sq.Name, err)
		}
		m.unregisterChannel(sq.ChannelID)
	}
	if sq.RoleID != "" {
		if err := api.DeleteRole(sq.GuildID, sq.RoleID); err != nil && discordCode(err) != discordgo.ErrCodeUnknownRole {
			m.config.Logger.Warnf("squads: delete role of %s: %v", sq.Name, err)
		}
	}
	if err := m.db.DeleteSquad(sq.ID); err != nil {
		m.config.Logger.Errorf("squads: delete %s: %v", sq.Name, err)
	}
	m.config.Logger.Infof("squads: deleted %s because %s", sq.Name, why)
	m.modLog(sq.GuildID, fmt.Sprintf("deleted **%s** because %s.", sq.Name, why))
}

// matchReport is what a matcher run did (or would do, for a dry run).
type matchReport struct {
	Plan      plan
	Queued    int
	Open      int
	Left      []string // queued people who left the server
	Created   []database.Squad
	Failures  []string
	OpenNames map[int64]string
}

// runMatch loads the queue and open squads, plans, and (unless dry) applies
// the plan. Callers hold the guild lock.
func (m *Module) runMatch(guildID string, dry bool) (matchReport, error) {
	gc := m.config.ForGuild(guildID)
	now := m.now()
	minSize, target, maxSize := gc.GetSquadsSizes()
	params := matchParams{Min: minSize, Target: target, Max: maxSize,
		FallbackAfter: gc.GetSquadsFallbackAfter(), UnmatchedAfter: gc.GetSquadsUnmatchedAfter()}

	queue, err := m.db.SquadQueue(guildID)
	if err != nil {
		return matchReport{}, err
	}
	picks, err := m.db.AllSquadPicks(guildID)
	if err != nil {
		return matchReport{}, err
	}
	rl := m.roleLookup(guildID)
	report := matchReport{Queued: len(queue), OpenNames: map[int64]string{}}

	var cands []*candidate
	threads := map[string]string{}
	for _, e := range queue {
		c, gone, err := m.loadCandidate(guildID, e.UserID, picks[e.UserID], rl)
		if gone {
			report.Left = append(report.Left, e.UserID)
			continue
		}
		if err != nil {
			m.config.Logger.Warnf("squads: look up queued member %s: %v", e.UserID, err)
			continue
		}
		c.QueuedAt = e.QueuedAt
		threads[e.UserID] = e.IntroThreadID
		cands = append(cands, c)
	}

	squads, err := m.db.ListSquads(guildID)
	if err != nil {
		return report, err
	}
	byID := map[int64]database.Squad{}
	var open []openSquad
	young := gc.GetSquadsYoungFor()
	for _, sq := range squads {
		if now.Sub(sq.CreatedAt) >= young || sq.Members >= maxSize {
			continue
		}
		ids, err := m.db.SquadMemberIDs(sq.ID)
		if err != nil {
			return report, err
		}
		os := openSquad{ID: sq.ID, Name: sq.Name}
		for _, uid := range ids {
			if c, _, err := m.loadCandidate(guildID, uid, picks[uid], rl); err == nil {
				os.Members = append(os.Members, c)
			}
		}
		open = append(open, os)
		byID[sq.ID] = sq
		report.OpenNames[sq.ID] = sq.Name
	}
	report.Open = len(open)
	report.Plan = buildPlan(cands, open, params, now)
	if dry {
		return report, nil
	}

	for _, uid := range report.Left {
		if _, err := m.db.DequeueSquadUser(guildID, uid); err != nil {
			m.config.Logger.Warnf("squads: dequeue departed %s: %v", uid, err)
		}
	}
	for _, group := range report.Plan.NewSquads {
		sq, err := m.createSquad(guildID, group)
		if err != nil {
			m.config.Logger.Errorf("squads: create squad: %v", err)
			report.Failures = append(report.Failures, "create squad: "+err.Error())
			// Creation fails for everyone the same way (e.g. no category),
			// so don't hammer Discord with the rest of the plan.
			break
		}
		report.Created = append(report.Created, sq)
	}
	for _, j := range report.Plan.Joins {
		sq, ok := byID[j.SquadID]
		if !ok {
			continue
		}
		if err := m.addMember(guildID, sq, j.User); err != nil {
			m.config.Logger.Errorf("squads: add %s to %s: %v", j.User.UserID, sq.Name, err)
			report.Failures = append(report.Failures, fmt.Sprintf("add %s to %s: %v", mention(j.User.UserID), sq.Name, err))
		}
	}
	for _, c := range report.Plan.Unmatched {
		if _, err := m.db.DequeueSquadUser(guildID, c.UserID); err != nil {
			m.config.Logger.Warnf("squads: dequeue unmatched %s: %v", c.UserID, err)
			continue
		}
		m.notifyUnmatched(guildID, c.UserID, threads[c.UserID])
	}
	if err := m.db.SetSquadLastMatch(guildID, now); err != nil {
		m.config.Logger.Warnf("squads: record match time: %v", err)
	}
	return report, nil
}

// loadCandidate builds a candidate from someone's picks and roles. gone is
// true when they're no longer in the server.
func (m *Module) loadCandidate(guildID, uid string, picks []database.SquadPick, rl roleLookup) (*candidate, bool, error) {
	mem, err := m.dc(nil).Member(guildID, uid)
	if err != nil {
		return nil, discordCode(err) == discordgo.ErrCodeUnknownMember, err
	}
	c := &candidate{UserID: uid, Games: map[int64]string{}, Tags: map[string]bool{}}
	for _, p := range picks {
		c.Games[p.GameID] = p.Name
		for _, t := range p.Tags {
			c.Tags[t] = true
		}
	}
	rl.apply(c, mem.Roles)
	return c, false, nil
}

const unmatchedText = "😔 **We couldn't find you a squad yet.**\n" +
	"This usually means nobody in your region and age group picked the same or similar games recently. " +
	"You've been taken out of the squad queue for now.\n\n" +
	"Want to try again? Hit the button to rejoin. You can change your games too. Adding a few more gives you a better chance."

// notifyUnmatched DMs someone who couldn't be matched, falling back to their
// intro thread if their DMs are closed.
func (m *Module) notifyUnmatched(guildID, uid, threadID string) {
	msg := func(content string) *discordgo.MessageSend {
		return &discordgo.MessageSend{
			Content:         content,
			AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{uid}},
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				joinButton(guildID, uid, threadID),
			}}},
		}
	}
	api := m.dc(nil)
	err := api.DM(uid, msg(unmatchedText))
	if err == nil {
		return
	}
	if threadID == "" {
		m.config.Logger.Warnf("squads: DM unmatched %s: %v (no intro thread to fall back to)", uid, err)
		return
	}
	if _, err := api.Send(threadID, msg(mention(uid)+" "+unmatchedText)); err != nil {
		m.config.Logger.Warnf("squads: notify unmatched %s in intro: %v", uid, err)
	}
}

// checkQuiet warns quiet squads and deletes ones still quiet after the
// warning. Callers hold the guild lock.
func (m *Module) checkQuiet(guildID string) {
	gc := m.config.ForGuild(guildID)
	warnAfter, deleteAfter := gc.GetSquadsQuietWarnAfter(), gc.GetSquadsQuietDeleteAfter()
	squads, err := m.db.ListSquads(guildID)
	if err != nil {
		m.config.Logger.Warnf("squads: list for quiet check: %v", err)
		return
	}
	now := m.now()
	for _, sq := range squads {
		switch {
		case sq.Members < 2:
			m.deleteSquad(sq, "it dropped below 2 members")
		case !sq.WarnedAt.IsZero() && now.Sub(sq.WarnedAt) >= deleteAfter:
			m.deleteSquad(sq, "it was quiet for too long")
		case sq.WarnedAt.IsZero() && now.Sub(sq.LastActivityAt) >= warnAfter:
			_, err := m.dc(nil).Send(sq.ChannelID, &discordgo.MessageSend{
				Content: fmt.Sprintf("👀 <@&%s> it's been quiet in here for %s. This squad will be deleted <t:%d:R> unless someone says something!",
					sq.RoleID, humanDuration(warnAfter), now.Add(deleteAfter).Unix()),
				AllowedMentions: &discordgo.MessageAllowedMentions{Roles: []string{sq.RoleID}},
			})
			if discordCode(err) == discordgo.ErrCodeUnknownChannel {
				m.deleteSquad(sq, "its channel was deleted")
				continue
			}
			if err != nil {
				m.config.Logger.Warnf("squads: warn %s: %v", sq.Name, err)
				continue
			}
			if err := m.db.MarkSquadWarned(sq.ID, now); err != nil {
				m.config.Logger.Warnf("squads: mark %s warned: %v", sq.Name, err)
			}
		}
	}
}

// humanDuration renders 48h as "2 days", 90m as "1 hour" etc.
func humanDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return plural(int(d.Hours()/24), "day", "days")
	case d >= time.Hour:
		return plural(int(d.Hours()), "hour", "hours")
	default:
		return plural(max(int(d.Minutes()), 1), "minute", "minutes")
	}
}
