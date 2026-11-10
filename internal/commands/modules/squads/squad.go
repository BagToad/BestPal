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

// overwrites builds a squad channel's permissions on top of its category's
// (inherited), so whoever can't see the category can't see squads either.
// Members and mods can chat; nobody else can post, and in private mode nobody
// else can see the channel.
func overwrites(guildID, roleID, modRoleID, botID, visibility string, inherited []*discordgo.PermissionOverwrite) []*discordgo.PermissionOverwrite {
	const chat = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory |
		discordgo.PermissionSendMessagesInThreads | discordgo.PermissionAddReactions | discordgo.PermissionAttachFiles |
		discordgo.PermissionEmbedLinks
	mask := int64(discordgo.PermissionSendMessages | discordgo.PermissionSendMessagesInThreads |
		discordgo.PermissionCreatePublicThreads | discordgo.PermissionCreatePrivateThreads)
	if visibility == config.SquadVisibilityPrivate {
		mask |= discordgo.PermissionViewChannel
	}
	everyone := &discordgo.PermissionOverwrite{ID: guildID, Type: discordgo.PermissionOverwriteTypeRole}
	var rest []*discordgo.PermissionOverwrite
	for _, o := range inherited {
		if o == nil || o.ID == roleID || (modRoleID != "" && o.ID == modRoleID) || (botID != "" && o.ID == botID) {
			continue
		}
		c := *o
		// Stripping the allows is enough for other roles and members: the
		// everyone deny below then applies to them, while the squad role's
		// allow still wins for members.
		c.Allow &^= mask
		if c.ID == guildID {
			everyone = &c
			continue
		}
		rest = append(rest, &c)
	}
	everyone.Deny |= mask
	out := append([]*discordgo.PermissionOverwrite{everyone,
		{ID: roleID, Type: discordgo.PermissionOverwriteTypeRole, Allow: chat}}, rest...)
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

// squadOverwrites builds a squad channel's permissions from the configured
// category's.
func (m *Module) squadOverwrites(guildID, roleID string) ([]*discordgo.PermissionOverwrite, error) {
	gc := m.config.ForGuild(guildID)
	api := m.dc(nil)
	category := gc.GetSquadsCategoryID()
	if category == "" {
		return nil, errors.New("the squads category isn't set in /config → Squads")
	}
	cat, err := api.Channel(category)
	if err != nil {
		return nil, fmt.Errorf("load the squads category: %w", err)
	}
	return overwrites(guildID, roleID, gc.GetSquadsModRoleID(), api.BotID(), gc.GetSquadsVisibility(), cat.PermissionOverwrites), nil
}

// grantRole gives someone their squad role, recording it for retry if
// Discord refuses.
func (m *Module) grantRole(guildID, uid, roleID string) {
	err := m.dc(nil).AddRole(guildID, uid, roleID)
	m.recordRoleOp(database.SquadRoleOp{GuildID: guildID, UserID: uid, RoleID: roleID, Add: true}, err)
}

// revokeRole takes someone's squad role away, recording it for retry if
// Discord refuses.
func (m *Module) revokeRole(guildID, uid, roleID string) {
	err := m.dc(nil).RemoveRole(guildID, uid, roleID)
	m.recordRoleOp(database.SquadRoleOp{GuildID: guildID, UserID: uid, RoleID: roleID}, err)
}

// recordRoleOp clears a pending role change once it's done (or moot because
// the member or role is gone) and saves it for retry otherwise.
func (m *Module) recordRoleOp(op database.SquadRoleOp, err error) {
	code := discordCode(err)
	if err == nil || code == discordgo.ErrCodeUnknownMember || code == discordgo.ErrCodeUnknownRole {
		if err := m.db.ClearSquadRoleOp(op.GuildID, op.UserID, op.RoleID); err != nil {
			m.config.Logger.Warnf("squads: clear role retry: %v", err)
		}
		return
	}
	verb := "remove"
	if op.Add {
		verb = "give"
	}
	m.config.Logger.Warnf("squads: %s role %s for %s (will retry): %v", verb, op.RoleID, op.UserID, err)
	if err := m.db.SetSquadRoleOp(op, m.now()); err != nil {
		m.config.Logger.Errorf("squads: save role retry: %v", err)
	}
}

// retryRoleOps retries failed squad role changes. Callers hold the guild lock.
func (m *Module) retryRoleOps(guildID string) {
	ops, err := m.db.SquadRoleOps(guildID)
	if err != nil {
		m.config.Logger.Warnf("squads: load role retries: %v", err)
		return
	}
	for _, op := range ops {
		if op.Add {
			m.grantRole(op.GuildID, op.UserID, op.RoleID)
		} else {
			m.revokeRole(op.GuildID, op.UserID, op.RoleID)
		}
	}
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
	// Read the category before creating anything so a bad category fails
	// cleanly.
	perms, err := m.squadOverwrites(guildID, "")
	if err != nil {
		return database.Squad{}, err
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
	perms[1].ID = role.ID
	ch, err := api.CreateChannel(guildID, discordgo.GuildChannelCreateData{
		Name:                 channelName(name),
		Type:                 discordgo.ChannelTypeGuildText,
		Topic:                name + ": a small group of pals matched on the games you play. Say hi!",
		ParentID:             category,
		PermissionOverwrites: perms,
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
		m.grantRole(guildID, uid, role.ID)
	}
	for _, c := range group {
		if c.IntroID == "" {
			c.IntroID = m.lookupIntro(guildID, c.UserID)
		}
	}

	msg, err := api.Send(ch.ID, &discordgo.MessageSend{
		Content:         welcomeText(guildID, name, role.ID, group, gc.GetSquadsQuietWarnAfter()+gc.GetSquadsQuietDeleteAfter()),
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

func welcomeText(guildID, name, roleID string, group []*candidate, quietLimit time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 🎉 Welcome to %s!\n", name)
	var intros []string
	for n, c := range group {
		if n > 0 {
			b.WriteString(" ")
		}
		b.WriteString(mention(c.UserID))
		if link := introLink(guildID, c); link != "" {
			intros = append(intros, link)
		}
	}
	if len(intros) > 0 {
		b.WriteString("\n📝 Intros: " + strings.Join(intros, " · "))
	}
	b.WriteString("\n\n" + commonGround(group))
	fmt.Fprintf(&b, "\n\nThis is your squad's channel. Ping everyone with <@&%s>.", roleID)
	fmt.Fprintf(&b, "\n-# Squads that go %s without chatting are deleted.", humanDuration(quietLimit))
	return b.String()
}

func memberName(mem *discordgo.Member) string {
	if mem == nil || (mem.Nick == "" && mem.User == nil) {
		return ""
	}
	return mem.DisplayName()
}

// linkTextEscaper keeps a display name from breaking out of masked-link text
// or adding formatting.
var linkTextEscaper = strings.NewReplacer(
	"\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_",
	"~", "\\~", "`", "\\`", "|", "\\|", "<", "\\<", ">", "\\>",
)

// introLink is a [name](url) link to someone's intro post, or "" without one.
func introLink(guildID string, c *candidate) string {
	if c.IntroID == "" {
		return ""
	}
	name := strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(c.Name))
	if name == "" {
		name = "intro"
	}
	return fmt.Sprintf("[%s](https://discord.com/channels/%s/%s)", linkTextEscaper.Replace(name), guildID, c.IntroID)
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
	m.grantRole(guildID, c.UserID, sq.RoleID)
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
	if c.IntroID == "" {
		c.IntroID = m.lookupIntro(guildID, c.UserID)
	}
	if link := introLink(guildID, c); link != "" {
		text += "\n📝 Intro: " + link
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
	m.revokeRole(guildID, uid, sq.RoleID)
	if sq.Members < 2 {
		// A failed delete is retried by the quiet check.
		_ = m.deleteSquad(sq, "it dropped below 2 members")
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

// deleteSquad removes a squad's channel, role and records. If Discord
// refuses to delete the channel or role, the records are kept so a later
// attempt can finish the job. Callers hold the guild lock.
func (m *Module) deleteSquad(sq database.Squad, why string) error {
	api := m.dc(nil)
	if sq.ChannelID != "" {
		if err := api.DeleteChannel(sq.ChannelID); err != nil && discordCode(err) != discordgo.ErrCodeUnknownChannel {
			m.config.Logger.Warnf("squads: delete channel of %s: %v", sq.Name, err)
			return fmt.Errorf("delete channel: %w", err)
		}
	}
	if sq.RoleID != "" {
		if err := api.DeleteRole(sq.GuildID, sq.RoleID); err != nil && discordCode(err) != discordgo.ErrCodeUnknownRole {
			m.config.Logger.Warnf("squads: delete role of %s: %v", sq.Name, err)
			return fmt.Errorf("delete role: %w", err)
		}
	}
	if err := m.db.DeleteSquad(sq.ID); err != nil {
		m.config.Logger.Errorf("squads: delete %s: %v", sq.Name, err)
		return err
	}
	if sq.ChannelID != "" {
		m.unregisterChannel(sq.ChannelID)
	}
	m.config.Logger.Infof("squads: deleted %s because %s", sq.Name, why)
	m.modLog(sq.GuildID, fmt.Sprintf("deleted **%s** because %s.", sq.Name, why))
	return nil
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
		c.IntroID = e.IntroThreadID
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
		// Skip a squad this run unless every member loads, so its size and
		// compatibility are never judged on a partial roster.
		os := openSquad{ID: sq.ID, Name: sq.Name}
		complete := true
		for _, uid := range ids {
			c, gone, err := m.loadCandidate(guildID, uid, picks[uid], rl)
			if err != nil {
				complete = false
				if gone && !dry {
					if _, err := m.removeMember(guildID, uid, sq.ID, "left the server and"); err != nil && !errors.Is(err, errNotMember) {
						m.config.Logger.Warnf("squads: remove departed %s: %v", uid, err)
					}
				} else if !gone {
					m.config.Logger.Warnf("squads: look up %s in %s: %v", uid, sq.Name, err)
				}
				break
			}
			os.Members = append(os.Members, c)
		}
		if !complete {
			continue
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
	c := &candidate{UserID: uid, Name: memberName(mem), Games: map[int64]string{}, Tags: map[string]bool{}}
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
	if threadID == "" {
		threadID = m.lookupIntro(guildID, uid)
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
			_ = m.deleteSquad(sq, "it dropped below 2 members")
		case !sq.WarnedAt.IsZero() && now.Sub(sq.WarnedAt) >= deleteAfter:
			if m.missedChat(sq) {
				continue
			}
			_ = m.deleteSquad(sq, "it was quiet for too long")
		case sq.WarnedAt.IsZero() && now.Sub(sq.LastActivityAt) >= warnAfter:
			if m.missedChat(sq) {
				continue
			}
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

// missedChat checks the channel itself for chat the bot didn't see (e.g.
// while it was offline) before a squad is warned or deleted. It records any
// it finds and reports true when the squad should be left alone this round,
// including when the channel can't be checked.
func (m *Module) missedChat(sq database.Squad) bool {
	last, err := m.dc(nil).LastHumanMessage(sq.ChannelID)
	if discordCode(err) == discordgo.ErrCodeUnknownChannel {
		return false
	}
	if err != nil {
		m.config.Logger.Warnf("squads: check recent chat in %s: %v", sq.Name, err)
		return true
	}
	if last.Unix() <= sq.LastActivityAt.Unix() {
		return false
	}
	if err := m.db.TouchSquad(sq.ID, last); err != nil {
		m.config.Logger.Warnf("squads: record missed activity in %s: %v", sq.Name, err)
	}
	return true
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
