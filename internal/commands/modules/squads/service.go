package squads

import (
	"time"

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
	}
	return nil
}

func (m *Module) tickGuild(guildID string) {
	lock := m.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()
	m.checkQuiet(guildID)

	gc := m.config.ForGuild(guildID)
	if !gc.GetSquadsEnabled() {
		return
	}
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

// nextMatch returns when the matcher will next run (zero means the next check).
func (m *Module) nextMatch(guildID string) time.Time {
	last, err := m.db.SquadLastMatch(guildID)
	if err != nil || last.IsZero() {
		return time.Time{}
	}
	return last.Add(m.config.ForGuild(guildID).GetSquadsMatchInterval())
}

const introPrompt = "👋 **Want to meet people who play what you play?**\n" +
	"Join a squad: a small group of new pals matched by region, age group and the games you pick."

// OnIntroThread posts the Join a squad button on a new intro post.
func (m *Module) OnIntroThread(s *discordgo.Session, thread *discordgo.Channel) {
	if thread == nil || thread.OwnerID == "" {
		return
	}
	gc := m.config.ForGuild(thread.GuildID)
	if !gc.GetSquadsEnabled() {
		return
	}
	if forum := gc.GetGamerPalsIntroductionsForumChannelID(); forum == "" || thread.ParentID != forum {
		return
	}
	go m.postIntroPrompt(s, thread, 3)
}

var introRetryDelay = 3 * time.Second

// postIntroPrompt retries because a brand-new forum thread can briefly reject
// messages until its starter post exists.
func (m *Module) postIntroPrompt(s *discordgo.Session, thread *discordgo.Channel, attempts int) {
	msg := &discordgo.MessageSend{
		Content:         introPrompt,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			joinButton(thread.GuildID, thread.OwnerID, thread.ID),
		}}},
	}
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
	if mc.Author == nil || mc.Author.Bot || m.db == nil {
		return
	}
	now := m.now()
	m.chMu.Lock()
	id, ok := m.channels[mc.ChannelID]
	if ok && now.Sub(m.touched[id]) < activityThrottle {
		ok = false
	}
	if ok {
		m.touched[id] = now
	}
	m.chMu.Unlock()
	if !ok {
		return
	}
	if err := m.db.TouchSquad(id, now); err != nil {
		m.config.Logger.Warnf("squads: record activity: %v", err)
	}
}

// OnChannelDelete cleans up a squad whose channel was deleted by hand.
func (m *Module) OnChannelDelete(_ *discordgo.Session, cd *discordgo.ChannelDelete) {
	if cd.Channel == nil || m.db == nil {
		return
	}
	m.chMu.Lock()
	_, ok := m.channels[cd.ID]
	m.chMu.Unlock()
	if !ok {
		return
	}
	lock := m.guildLock(cd.GuildID)
	lock.Lock()
	defer lock.Unlock()
	if sq, found, err := m.db.SquadByChannel(cd.ID); err == nil && found {
		m.deleteSquad(sq, "its channel was deleted")
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
	if _, err := m.removeMember(r.GuildID, r.User.ID, 0, "left the server and"); err != nil && err != errNotMember {
		m.config.Logger.Warnf("squads: remove departed member: %v", err)
	}
}
