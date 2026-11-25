package squads

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

// Service runs the matcher and the quiet-squad cleanup on a schedule.
type Service struct {
	m *Module
}

// HydrateServiceDiscordSession stores the session and loads the squad
// channels so chat activity can be tracked.
func (sv *Service) HydrateServiceDiscordSession(s *discordgo.Session) error {
	sv.m.session = s
	sv.m.loadChannels()
	return nil
}

// ScheduledFuncs checks every few minutes; the matcher itself only runs
// once its configured interval has passed.
func (sv *Service) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{"@every 5m": sv.m.tick}
}

func (m *Module) loadChannels() {
	if m.db == nil {
		return
	}
	squads, err := m.db.ListSquads("")
	if err != nil {
		m.config.Logger.Warnf("squads: load channels: %v", err)
		return
	}
	for _, sq := range squads {
		if sq.ChannelID != "" {
			m.registerChannel(sq.ChannelID, sq.ID)
		}
	}
}

// tick runs due matchers and the quiet check for every guild with squads or
// a queue.
func (m *Module) tick() error {
	if m.db == nil {
		return nil
	}
	guilds, err := m.db.SquadGuilds()
	if err != nil {
		return err
	}
	for _, g := range guilds {
		m.tickGuild(g)
		m.reconcileThreadsNightly(g)
	}
	return nil
}

func (m *Module) tickGuild(guildID string) {
	lock := m.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()
	m.retryRoleOps(guildID)
	m.reconcileDepartures(guildID)
	m.checkQuiet(guildID)

	gc := m.config.ForGuild(guildID)
	if !gc.GetSquadsEnabled() {
		return
	}
	m.sendInactivityReminders(guildID)
	last, err := m.db.SquadLastMatch(guildID)
	if err != nil {
		m.config.Logger.Warnf("squads: read last match: %v", err)
		return
	}
	if !last.IsZero() && m.now().Sub(last) < gc.GetSquadsMatchInterval() {
		return
	}
	report, err := m.runMatch(guildID, false)
	if err != nil {
		m.config.Logger.Errorf("squads: match: %v", err)
		return
	}
	if len(report.Created)+len(report.Plan.Joins)+len(report.Plan.Unmatched) > 0 {
		m.config.Logger.Infof("squads: matched in %s: %d new squads, %d late joins, %d unmatched",
			guildID, len(report.Created), len(report.Plan.Joins), len(report.Plan.Unmatched))
	}
}

const squadReminderInterval = 24 * time.Hour

// sendInactivityReminders nudges current members daily until they speak in
// their squad. Callers hold the guild lock.
func (m *Module) sendInactivityReminders(guildID string) {
	now := m.now()
	reminders, err := m.db.DueSquadReminders(guildID, now, squadReminderInterval)
	if err != nil {
		m.config.Logger.Warnf("squads: load inactivity reminders: %v", err)
		return
	}
	for _, reminder := range reminders {
		since := reminder.JoinedAt
		if reminder.LastReminderAt.After(since) {
			since = reminder.LastReminderAt
		}
		spoke, err := m.dc(nil).UserMessagedAfter(reminder.ChannelID, reminder.UserID, since)
		if err != nil {
			m.config.Logger.Warnf("squads: reconcile activity for %s: %v", reminder.UserID, err)
			if markErr := m.db.MarkSquadReminderReconcileFailed(reminder.HistoryID, now); markErr != nil {
				m.config.Logger.Warnf("squads: back off activity reconciliation for %s: %v", reminder.UserID, markErr)
			}
			continue
		}
		if spoke {
			if err := m.db.MarkSquadMemberMessaged(reminder.SquadID, reminder.UserID, now); err != nil {
				m.config.Logger.Warnf("squads: record reconciled activity for %s: %v", reminder.UserID, err)
			}
			continue
		}
		// Claim before the network call so a timeout or closed DM cannot cause
		// another attempt on the next five-minute tick.
		if err := m.db.MarkSquadReminder(reminder.HistoryID, now); err != nil {
			m.config.Logger.Warnf("squads: mark inactivity reminder for %s: %v", reminder.UserID, err)
			continue
		}
		msg := &discordgo.MessageSend{
			Content:         fmt.Sprintf("👋 **Your squad is waiting for you!**\nSay hello in **%s**: <#%s>. Once you send a message there, these reminders stop.", reminder.SquadName, reminder.ChannelID),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
		if err := m.dc(nil).DM(reminder.UserID, msg); err != nil {
			m.config.Logger.Warnf("squads: inactivity reminder to %s: %v", reminder.UserID, err)
		}
	}
}

// reconcileEvery is how often every squad's roster is checked for people who
// left while the bot wasn't watching.
const reconcileEvery = time.Hour

// reconcileDepartures removes squad members who are confirmed to have left
// the server. Lookup failures keep the membership. Callers hold the guild
// lock.
func (m *Module) reconcileDepartures(guildID string) {
	now := m.now()
	m.mu.Lock()
	if m.reconciled == nil {
		m.reconciled = map[string]time.Time{}
	}
	due := now.Sub(m.reconciled[guildID]) >= reconcileEvery
	if due {
		m.reconciled[guildID] = now
	}
	m.mu.Unlock()
	if !due {
		return
	}
	squads, err := m.db.ListSquads(guildID)
	if err != nil {
		m.config.Logger.Warnf("squads: list for departure check: %v", err)
		return
	}
	api := m.dc(nil)
	for _, sq := range squads {
		if sq.Deleting() {
			continue
		}
		ids, err := m.db.SquadMemberIDs(sq.ID)
		if err != nil {
			m.config.Logger.Warnf("squads: members of %s: %v", sq.Name, err)
			continue
		}
		for _, uid := range ids {
			_, err := api.Member(guildID, uid)
			if err == nil {
				continue
			}
			if discordCode(err) != discordgo.ErrCodeUnknownMember {
				m.config.Logger.Warnf("squads: look up %s in %s: %v", uid, sq.Name, err)
				continue
			}
			if _, err := m.removeMember(guildID, uid, sq.ID, database.SquadLeaveServer, "left the server and"); err != nil && !errors.Is(err, errNotMember) {
				m.config.Logger.Warnf("squads: remove departed %s: %v", uid, err)
			}
		}
	}
}

// threadCheckHour is the UTC hour from which the nightly thread check runs
// (overnight in the Americas).
const threadCheckHour = 8

// reconcileThreadsNightly runs reconcileThreadLeaves for every thread squad
// once a day. Leaving a thread while the bot is offline is rare, so it isn't
// worth listing every thread's members more often. Threads that couldn't be
// checked are retried on later ticks that day. It takes the guild lock
// itself, and only briefly, so listing threads doesn't stall joins and leaves.
func (m *Module) reconcileThreadsNightly(guildID string) {
	now := m.now().UTC()
	if now.Hour() < threadCheckHour {
		return
	}
	today := now.Format(time.DateOnly)
	last, err := m.db.SquadThreadCheck(guildID)
	if err != nil {
		m.config.Logger.Warnf("squads: load thread check date: %v", err)
		return
	}
	m.mu.Lock()
	retry := m.threadRetry[guildID]
	m.mu.Unlock()
	if last == today && len(retry) == 0 {
		return
	}

	type check struct {
		sq     database.Squad
		roster map[string]int64
	}
	var checks []check
	var ops []database.SquadRoleOp
	lock := m.guildLock(guildID)
	lock.Lock()
	squads, err := m.db.ListSquads(guildID)
	if err == nil {
		ops, err = m.db.SquadRoleOps(guildID)
	}
	for _, sq := range squads {
		if err != nil {
			break
		}
		if !sq.Thread || sq.Deleting() || (last == today && !retry[sq.ID]) {
			continue
		}
		var roster map[string]int64
		if roster, err = m.db.SquadMemberships(sq.ID); err == nil {
			checks = append(checks, check{sq, roster})
		}
	}
	lock.Unlock()
	if err != nil {
		m.config.Logger.Warnf("squads: load squads for thread check: %v", err)
		return
	}
	failed := map[int64]bool{}
	for _, c := range checks {
		if !m.reconcileThreadLeaves(c.sq, c.roster, ops) {
			failed[c.sq.ID] = true
		}
	}
	m.mu.Lock()
	if m.threadRetry == nil {
		m.threadRetry = map[string]map[int64]bool{}
	}
	m.threadRetry[guildID] = failed
	m.mu.Unlock()
	if last != today {
		if err := m.db.SetSquadThreadCheck(guildID, today); err != nil {
			m.config.Logger.Warnf("squads: save thread check date: %v", err)
		}
	}
}

// reconcileThreadLeaves treats members of roster (a snapshot of sq's
// memberships) missing from its thread as having left it, like
// OnThreadMembersUpdate does, unless ops shows the bot still owes them a
// thread add. It reports false if the thread couldn't be checked. Callers
// must not hold the guild lock.
func (m *Module) reconcileThreadLeaves(sq database.Squad, roster map[string]int64, ops []database.SquadRoleOp) bool {
	api := m.dc(nil)
	inThread, err := api.ThreadMemberIDs(sq.ChannelID)
	if err != nil {
		// The thread was deleted while the bot was offline.
		if discordCode(err) == discordgo.ErrCodeUnknownChannel {
			m.channelDeleted(sq.GuildID, sq.ChannelID, "its thread was deleted")
			return true
		}
		m.config.Logger.Warnf("squads: list thread members of %s: %v", sq.Name, err)
		return false
	}
	// The bot is always in its threads, so a list without it is incomplete
	// and would evict everyone.
	if !slices.Contains(inThread, api.BotID()) {
		m.config.Logger.Warnf("squads: member list of the %s thread is missing the bot; skipping", sq.Name)
		return false
	}
	key := accessKey(sq)
	owed := func(ops []database.SquadRoleOp, uid string) bool { return addOwed(ops, key, uid) }
	var gone []string
	for uid := range roster {
		if !slices.Contains(inThread, uid) && !owed(ops, uid) {
			gone = append(gone, uid)
		}
	}
	if len(gone) == 0 {
		return true
	}
	slices.Sort(gone)
	reasons := make([]threadLeave, len(gone))
	for i, uid := range gone {
		reasons[i] = m.threadLeaveReason(sq.GuildID, uid)
	}
	lock := m.guildLock(sq.GuildID)
	lock.Lock()
	defer lock.Unlock()
	cur, found, err := m.db.SquadByChannel(sq.ChannelID)
	if err != nil || !found || cur.ID != sq.ID || cur.Deleting() {
		return err == nil
	}
	// Someone who left and rejoined while the thread was listed has a new
	// membership (and maybe a new pending add); leave them be.
	now, err := m.db.SquadMemberships(sq.ID)
	if err == nil {
		ops, err = m.db.SquadRoleOps(sq.GuildID)
	}
	if err != nil {
		m.config.Logger.Warnf("squads: reload %s for thread check: %v", sq.Name, err)
		return false
	}
	ok := true
	for i, uid := range gone {
		if id, in := now[uid]; !in || id != roster[uid] || owed(ops, uid) {
			continue
		}
		if _, err := m.removeMember(sq.GuildID, uid, sq.ID, reasons[i].reason, reasons[i].verb); err != nil && !errors.Is(err, errNotMember) {
			m.config.Logger.Warnf("squads: %s left the %s thread: %v", uid, sq.Name, err)
			ok = false
		}
	}
	return ok
}

// addOwed reports whether giving uid access to key is still queued.
func addOwed(ops []database.SquadRoleOp, key, uid string) bool {
	return slices.ContainsFunc(ops, func(op database.SquadRoleOp) bool {
		return op.Add && op.UserID == uid && op.RoleID == key
	})
}

type threadLeave struct {
	reason string
	verb   string
}

// threadLeaveReason tells leaving a squad thread apart from leaving the
// server, which also drops someone from their threads.
func (m *Module) threadLeaveReason(guildID, uid string) threadLeave {
	// The cache may still hold someone who just left the server.
	if _, err := m.dc(nil).FetchMember(guildID, uid); discordCode(err) == discordgo.ErrCodeUnknownMember {
		return threadLeave{database.SquadLeaveServer, "left the server and"}
	}
	return threadLeave{database.SquadLeaveVoluntary, "left"}
}

// nextMatch returns when the matcher will next run (zero means the next check).
func (m *Module) nextMatch(guildID string) time.Time {
	last, err := m.db.SquadLastMatch(guildID)
	if err != nil || last.IsZero() {
		return time.Time{}
	}
	return last.Add(m.config.ForGuild(guildID).GetSquadsMatchInterval())
}

const introPrompt = "**Want to meet people who play what you play?**\n" +
	"Join a squad: a small group of new pals matched by region, age group and the games you pick."

// introPromptMsg is the Join a squad post for an intro, pinging its author.
func introPromptMsg(guildID, ownerID, threadID string) *discordgo.MessageSend {
	return &discordgo.MessageSend{
		Content:         "👋 " + mention(ownerID) + " " + introPrompt,
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{ownerID}},
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			joinButton(guildID, ownerID, threadID),
		}}},
	}
}

// channelPromptMsg is a standalone Join a squad post that anyone can use.
func channelPromptMsg(guildID string) *discordgo.MessageSend {
	return &discordgo.MessageSend{
		Content:         introPrompt,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			joinButton(guildID, "", ""),
		}}},
	}
}

// OnIntroThread posts the Join a squad button on a new intro post.
func (m *Module) OnIntroThread(s *discordgo.Session, thread *discordgo.Channel) {
	if m.wantsIntroButton(thread) {
		go m.postIntroPrompt(s, thread, 3)
	}
}

func (m *Module) wantsIntroButton(thread *discordgo.Channel) bool {
	if thread == nil || thread.OwnerID == "" {
		return false
	}
	gc := m.config.ForGuild(thread.GuildID)
	if !gc.GetSquadsEnabled() || !gc.GetSquadsIntroButton() {
		return false
	}
	forum := gc.GetGamerPalsIntroductionsForumChannelID()
	return forum != "" && thread.ParentID == forum
}

var introRetryDelay = 3 * time.Second

// postIntroPrompt retries because a brand-new forum thread can briefly reject
// messages until its starter post exists.
func (m *Module) postIntroPrompt(s *discordgo.Session, thread *discordgo.Channel, attempts int) {
	msg := introPromptMsg(thread.GuildID, thread.OwnerID, thread.ID)
	var err error
	for n := range attempts {
		if n > 0 {
			time.Sleep(introRetryDelay)
		}
		if _, err = m.dc(s).Send(thread.ID, msg); err == nil {
			return
		}
	}
	m.config.Logger.Warnf("squads: post join button in intro %s: %v", thread.ID, err)
}

// activityThrottle limits how often chat in a squad is written to the database.
const activityThrottle = time.Minute

// OnMessageCreate records chat in squad channels, which keeps them alive.
func (m *Module) OnMessageCreate(_ *discordgo.Session, mc *discordgo.MessageCreate) {
	if mc == nil || mc.Message == nil || mc.Author == nil || mc.Author.Bot || m.db == nil {
		return
	}
	now := m.now()
	m.chMu.Lock()
	id, ok := m.channels[mc.ChannelID]
	touch := ok && now.Sub(m.touched[id]) >= activityThrottle
	if touch {
		m.touched[id] = now
	}
	m.chMu.Unlock()
	if !ok {
		return
	}
	// Serialize with the quiet check so a reply can't land between its
	// warning and recording that warning.
	lock := m.guildLock(mc.GuildID)
	lock.Lock()
	defer lock.Unlock()
	if err := m.db.MarkSquadMemberMessaged(id, mc.Author.ID, now); err != nil {
		m.config.Logger.Warnf("squads: record member activity: %v", err)
	}
	if touch {
		if err := m.db.TouchSquad(id, now); err != nil {
			m.config.Logger.Warnf("squads: record activity: %v", err)
		}
	}
}

// OnChannelDelete cleans up a squad whose channel was deleted by hand.
func (m *Module) OnChannelDelete(_ *discordgo.Session, cd *discordgo.ChannelDelete) {
	if cd.Channel != nil {
		m.channelDeleted(cd.GuildID, cd.ID, "its channel was deleted")
	}
}

// OnThreadMembersUpdate treats someone leaving a squad thread by hand as
// leaving the squad, without an exit survey. Removals the bot made itself
// are already gone from the squad by the time the event arrives.
func (m *Module) OnThreadMembersUpdate(_ *discordgo.Session, u *discordgo.ThreadMembersUpdate) {
	if m.db == nil || len(u.RemovedMembers) == 0 {
		return
	}
	m.chMu.Lock()
	_, ok := m.channels[u.ID]
	m.chMu.Unlock()
	if !ok {
		return
	}
	var left []string
	for _, uid := range u.RemovedMembers {
		if uid != m.dc(nil).BotID() {
			left = append(left, uid)
		}
	}
	if len(left) == 0 {
		return
	}
	lock := m.guildLock(u.GuildID)
	// Note who's in the squad now, so a membership made while Discord is
	// checked below is left alone.
	lock.Lock()
	sq, found, err := m.db.SquadByChannel(u.ID)
	var before map[string]int64
	if err == nil && found && sq.Thread && !sq.Deleting() {
		before, err = m.db.SquadMemberships(sq.ID)
	}
	lock.Unlock()
	if err != nil || !found || !sq.Thread || sq.Deleting() {
		return
	}
	left = slices.DeleteFunc(left, func(uid string) bool { _, in := before[uid]; return !in })
	if len(left) == 0 {
		return
	}
	// Events can arrive late: someone removed and added back has a stale
	// removal event, so only act on people still missing from the thread.
	inThread, err := m.dc(nil).ThreadMemberIDs(u.ID)
	if err != nil {
		// The nightly check catches the leave if it was real.
		m.config.Logger.Warnf("squads: list the %s thread after members left: %v", sq.Name, err)
		return
	}
	var reasons []threadLeave
	for _, uid := range left {
		reasons = append(reasons, m.threadLeaveReason(u.GuildID, uid))
	}
	lock.Lock()
	defer lock.Unlock()
	cur, found, err := m.db.SquadByChannel(u.ID)
	if err != nil || !found || cur.ID != sq.ID || cur.Deleting() {
		return
	}
	now, err := m.db.SquadMemberships(sq.ID)
	var ops []database.SquadRoleOp
	if err == nil {
		ops, err = m.db.SquadRoleOps(sq.GuildID)
	}
	if err != nil {
		m.config.Logger.Warnf("squads: reload %s after members left its thread: %v", sq.Name, err)
		return
	}
	for i, uid := range left {
		if slices.Contains(inThread, uid) || now[uid] != before[uid] || addOwed(ops, accessKey(cur), uid) {
			continue
		}
		if _, err := m.removeMember(u.GuildID, uid, sq.ID, reasons[i].reason, reasons[i].verb); err != nil && !errors.Is(err, errNotMember) {
			m.config.Logger.Warnf("squads: %s left the %s thread: %v", uid, sq.Name, err)
		}
	}
}

// OnThreadDelete cleans up a squad whose thread was deleted by hand.
func (m *Module) OnThreadDelete(_ *discordgo.Session, td *discordgo.ThreadDelete) {
	if td.Channel != nil {
		m.channelDeleted(td.GuildID, td.ID, "its thread was deleted")
	}
}

func (m *Module) channelDeleted(guildID, channelID, why string) {
	if m.db == nil {
		return
	}
	m.chMu.Lock()
	_, ok := m.channels[channelID]
	m.chMu.Unlock()
	if !ok {
		return
	}
	lock := m.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()
	if sq, found, err := m.db.SquadByChannel(channelID); err == nil && found {
		m.deleteSquad(sq, why)
	}
}

// OnGuildMemberRemove drops someone who left the server from the queue and
// their squad.
func (m *Module) OnGuildMemberRemove(_ *discordgo.Session, r *discordgo.GuildMemberRemove) {
	if r.Member == nil || r.User == nil || m.db == nil {
		return
	}
	lock := m.guildLock(r.GuildID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := m.db.DequeueSquadUser(r.GuildID, r.User.ID); err != nil {
		m.config.Logger.Warnf("squads: dequeue departed member: %v", err)
	}
	if _, err := m.removeMember(r.GuildID, r.User.ID, 0, database.SquadLeaveServer, "left the server and"); err != nil && err != errNotMember {
		m.config.Logger.Warnf("squads: remove departed member: %v", err)
	}
}
