package squads

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func threadModule(t *testing.T) (*Module, *fakeAPI, *database.DB, *time.Time) {
	t.Helper()
	return newTestModule(t, map[string]any{config.KeySquadsThreadChannelID: "hall"})
}

func sentTo(api *fakeAPI, ch string) []*discordgo.MessageSend {
	var out []*discordgo.MessageSend
	for _, s := range api.sent {
		if s.ChannelID == ch {
			out = append(out, s.Msg)
		}
	}
	return out
}

func TestThreadSquadIsAPrivateThread(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")

	assert.True(t, sq.Thread)
	assert.Empty(t, sq.RoleID)
	assert.Equal(t, "hall", api.threads[sq.ChannelID])
	assert.Empty(t, api.channels, "no squad channel")
	assert.Empty(t, api.roles, "no squad role")
	assert.Empty(t, api.roleAdds)
	assert.Equal(t, []string{"+a:" + sq.ChannelID, "+b:" + sq.ChannelID, "+c:" + sq.ChannelID}, api.threadMems)

	msgs := sentTo(api, sq.ChannelID)
	require.NotEmpty(t, msgs)
	welcome := msgs[0]
	assert.Contains(t, welcome.Content, "private thread")
	assert.NotContains(t, welcome.Content, "<@&")
	assert.ElementsMatch(t, []string{"a", "b", "c"}, welcome.AllowedMentions.Users)
	button := welcome.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	assert.Equal(t, "Leave squad", button.Label)
	assert.True(t, strings.HasSuffix(button.CustomID, "leave:"+strconv.FormatInt(sq.ID, 10)))
	assert.Len(t, api.pins, 1)
	for _, dm := range api.dms {
		assert.Contains(t, dm.Msg.Content, sq.ChannelID)
	}
}

func TestThreadSquadMembersLeaveAndJoinTheThread(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.threadMems = nil

	member(api, "d", "role-eu")
	require.NoError(t, m.addMember(guild, sq, &candidate{UserID: "d", Games: map[int64]string{1: "Game 1"}}))
	_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
	require.NoError(t, err)

	assert.Equal(t, []string{"+d:" + sq.ChannelID, "-a:" + sq.ChannelID}, api.threadMems)
	assert.Empty(t, api.roleAdds)
	assert.Empty(t, api.roleRems)
}

func TestThreadSquadDeletionClosesTheThread(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")

	_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
	require.NoError(t, err)

	require.NotEmpty(t, api.locked)
	assert.Equal(t, sq.ChannelID, api.locked[0], "locked before the transcript is read")
	assert.Empty(t, api.delCh, "the thread is archived, not deleted")
	assert.Empty(t, api.delRoles)
	_, ok, err := db.GetSquad(sq.ID)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestThreadMembershipFailuresAreRetried(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.threadMems = nil
	api.removeErr = errors.New("discord is down")

	_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
	require.NoError(t, err)
	ops, err := db.SquadRoleOps(guild)
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, database.ThreadAccessPrefix+sq.ChannelID, ops[0].RoleID)

	api.removeErr = nil
	m.retryRoleOps(guild)
	assert.Equal(t, []string{"-a:" + sq.ChannelID}, api.threadMems)
	ops, err = db.SquadRoleOps(guild)
	require.NoError(t, err)
	assert.Empty(t, ops)
}

func TestDeletingThreadSquadDropsPendingThreadRetries(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.removeErr = errors.New("discord is down")
	_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
	require.NoError(t, err)
	api.removeErr = nil

	require.NoError(t, m.deleteSquad(sq, "a mod deleted it"))
	ops, err := db.SquadRoleOps(guild)
	require.NoError(t, err)
	assert.Empty(t, ops)
}

func TestThreadModeIgnoresChannelAndRoleLimits(t *testing.T) {
	m, api, _, now := threadModule(t)
	queueGroups(t, m, api, now, 3)
	api.extraInCategory, api.extraRoles = 50, 250

	dry, err := m.runMatch(guild, true)
	require.NoError(t, err)
	assert.Len(t, dry.Plan.NewSquads, 3)
	assert.Empty(t, dry.Failures)

	run, err := m.runMatch(guild, false)
	require.NoError(t, err)
	assert.Len(t, run.Created, 3)
	assert.Len(t, api.threads, 3)
	assert.Empty(t, api.channels)
}

func TestThreadCreateFailureSavesNothing(t *testing.T) {
	m, api, db, _ := threadModule(t)
	api.threadErr = errors.New("missing access")
	member(api, "a", "role-eu")
	member(api, "b", "role-eu")
	_, err := m.createSquad(guild, []*candidate{{UserID: "a"}, {UserID: "b"}}, false)
	require.ErrorContains(t, err, "create thread")
	squads, err := db.ListSquads(guild)
	require.NoError(t, err)
	assert.Empty(t, squads)
}

func TestQuietThreadSquadPingsMembers(t *testing.T) {
	m, api, db, now := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.sent = nil

	*now = now.Add(8 * 24 * time.Hour)
	require.NoError(t, m.tick())

	msgs := sentTo(api, sq.ChannelID)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "<@a> <@b> it's been quiet")
	assert.ElementsMatch(t, []string{"a", "b"}, msgs[0].AllowedMentions.Users)
	assert.Empty(t, msgs[0].AllowedMentions.Roles)
}

func TestRefreshPermissionsSkipsThreadSquads(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	legacy := makeSquad(t, m, api, db, "a", "b")
	_, err := db.CreateSquad(database.Squad{GuildID: guild, Name: "Thread Squad", ChannelID: "thread-x", Thread: true}, []string{"c", "d"}, *now)
	require.NoError(t, err)

	assert.Contains(t, m.refreshPermissions(guild), "1 squad")
	assert.Contains(t, api.permEdits, legacy.ChannelID)
	assert.NotContains(t, api.permEdits, "thread-x")
}

func TestDeletedThreadDeletesSquad(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")

	m.OnThreadDelete(nil, &discordgo.ThreadDelete{Channel: &discordgo.Channel{ID: "someone-elses", GuildID: guild}})
	_, ok, _ := db.GetSquad(sq.ID)
	assert.True(t, ok)

	m.OnThreadDelete(nil, &discordgo.ThreadDelete{Channel: &discordgo.Channel{ID: sq.ChannelID, GuildID: guild}})
	_, ok, _ = db.GetSquad(sq.ID)
	assert.False(t, ok)
	_, inSquad, err := db.SquadForUser(guild, "a")
	require.NoError(t, err)
	assert.False(t, inSquad, "members are free to queue again")
}

func TestLockedThreadsAreNotReopened(t *testing.T) {
	assert.True(t, reopenable(&discordgo.Channel{}))
	assert.True(t, reopenable(&discordgo.Channel{ThreadMetadata: &discordgo.ThreadMetadata{Archived: true}}))
	assert.False(t, reopenable(&discordgo.Channel{ThreadMetadata: &discordgo.ThreadMetadata{Archived: true, Locked: true}}))
}

func TestThreadSquadClosedWithoutModLog(t *testing.T) {
	m, api, db, _ := newTestModule(t, map[string]any{config.KeySquadsThreadChannelID: "hall", config.KeyModActionLogChannelID: ""})
	sq := makeSquad(t, m, api, db, "a", "b")
	require.NoError(t, m.deleteSquad(sq, "a mod deleted it"))
	assert.Equal(t, []string{sq.ChannelID}, api.locked)
	assert.Empty(t, api.delCh)
}

func TestLeavingSquadThreadLeavesTheSquad(t *testing.T) {
	m, api, db, _ := newTestModule(t, map[string]any{config.KeySquadsThreadChannelID: "hall", config.KeySquadsFeedbackChannelID: "feedback"})
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.sent = nil

	api.threadMems = append(api.threadMems, "-a:"+sq.ChannelID)
	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: sq.ChannelID, GuildID: guild, RemovedMembers: []string{"a", "bot"}})
	_, inSquad, err := db.SquadForUser(guild, "a")
	require.NoError(t, err)
	assert.False(t, inSquad)
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, 2, got.Members)
	msgs := sentTo(api, sq.ChannelID)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "<@a> left the squad")
	assert.Empty(t, sentTo(api, "feedback"), "no exit survey")

	// The bot's own removals (and unrelated threads) are ignored.
	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: sq.ChannelID, GuildID: guild, RemovedMembers: []string{"a"}})
	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: "other", GuildID: guild, RemovedMembers: []string{"b"}})
	got, _, _ = db.GetSquad(sq.ID)
	assert.Equal(t, 2, got.Members)

	api.threadMems = append(api.threadMems, "-b:"+sq.ChannelID)
	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: sq.ChannelID, GuildID: guild, RemovedMembers: []string{"b"}})
	_, ok, _ := db.GetSquad(sq.ID)
	assert.False(t, ok, "dropping below 2 closes the squad")
}

func TestClosedSquadThreadIsModOnly(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, api.threadMembers(sq.ChannelID))

	_, err = m.removeMember(guild, "b", sq.ID, database.SquadLeaveVoluntary, "left")
	require.NoError(t, err)
	assert.Empty(t, api.threadMembers(sq.ChannelID), "the last member is removed too")
	assert.Equal(t, 1, countOf(api.locked, sq.ChannelID), "closed once, not again after the transcript")
	assert.Empty(t, api.delCh)
}

func countOf(list []string, v string) int {
	n := 0
	for _, x := range list {
		if x == v {
			n++
		}
	}
	return n
}

// Turning on thread squads leaves existing channel squads working exactly as
// before: roles, role pings, channel deletion, and no thread handling.
func TestChannelSquadsUnchangedInThreadMode(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	require.False(t, sq.Thread)
	require.NotEmpty(t, sq.RoleID)
	m.config.Set(config.KeySquadsThreadChannelID, "hall")
	api.threadMems, api.roleAdds, api.roleRems, api.dms = nil, nil, nil, nil

	member(api, "d", "role-eu")
	require.NoError(t, m.addMember(guild, sq, &candidate{UserID: "d"}))
	assert.Equal(t, []string{"d:" + sq.RoleID}, api.roleAdds)

	ping, mentions, err := m.squadPing(sq)
	require.NoError(t, err)
	assert.Equal(t, "<@&"+sq.RoleID+">", ping)
	assert.Equal(t, []string{sq.RoleID}, mentions.Roles)

	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: sq.ChannelID, GuildID: guild, RemovedMembers: []string{"a"}})
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad, "thread events don't touch channel squads")

	for _, uid := range []string{"a", "b", "c"} {
		_, err := m.removeMember(guild, uid, sq.ID, database.SquadLeaveVoluntary, "left")
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"a:" + sq.RoleID, "b:" + sq.RoleID, "c:" + sq.RoleID}, api.roleRems)
	_, ok, _ := db.GetSquad(sq.ID)
	assert.False(t, ok)
	assert.Contains(t, api.delCh, sq.ChannelID, "the channel is deleted, not closed like a thread")
	assert.Contains(t, api.delRoles, sq.RoleID)
	assert.Empty(t, api.threadMems)
	assert.Empty(t, api.locked)
	require.Len(t, api.dms, 1)
	assert.Equal(t, "d", api.dms[0].ChannelID)
	assert.Contains(t, api.dms[0].Msg.Content, "was deleted** because it no longer had enough members")

	next := makeSquad(t, m, api, db, "e", "f")
	assert.True(t, next.Thread, "new squads are threads")
}

// Someone who left a squad thread while the bot was offline is caught by the
// nightly check, but a member still waiting on a retried add isn't.
func TestThreadLeaveWhileOfflineIsReconciledNightly(t *testing.T) {
	m, api, db, now := threadModule(t)
	*now = time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	member(api, "d", "role-eu")
	api.addErr = errors.New("discord hiccup")
	require.NoError(t, m.addMember(guild, sq, &candidate{UserID: "d"}))
	api.addErr = nil
	api.threadMems = append(api.threadMems, "-a:"+sq.ChannelID)
	api.sent = nil

	m.reconcileThreadsNightly(guild)
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad, "not before the nightly hour")

	*now = now.Add(time.Hour)
	m.reconcileThreadsNightly(guild)
	_, inSquad, _ = db.SquadForUser(guild, "a")
	assert.False(t, inSquad, "a left the thread, so the squad")
	_, inSquad, _ = db.SquadForUser(guild, "d")
	assert.True(t, inSquad, "d's thread add is still pending")
	checked, err := db.SquadThreadCheck(guild)
	require.NoError(t, err)
	assert.Equal(t, "2026-01-02", checked, "the date is saved so a restart doesn't rerun it")
	msgs := sentTo(api, sq.ChannelID)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "<@a> left the squad")

	// Once a day: a later leave waits for tomorrow.
	api.threadMems = append(api.threadMems, "-b:"+sq.ChannelID)
	*now = now.Add(time.Hour)
	m.reconcileThreadsNightly(guild)
	_, inSquad, _ = db.SquadForUser(guild, "b")
	assert.True(t, inSquad)
	*now = now.Add(24 * time.Hour)
	m.reconcileThreadsNightly(guild)
	_, inSquad, _ = db.SquadForUser(guild, "b")
	assert.False(t, inSquad)
}

func TestNightlyThreadCheckSkipsListWithoutBot(t *testing.T) {
	m, api, db, now := threadModule(t)
	*now = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.threadMems = append(api.threadMems, "-a:"+sq.ChannelID, "-b:"+sq.ChannelID, "-c:"+sq.ChannelID)
	api.threadListNoBot = true

	m.reconcileThreadsNightly(guild)
	for _, uid := range []string{"a", "b", "c"} {
		_, inSquad, _ := db.SquadForUser(guild, uid)
		assert.True(t, inSquad, "an incomplete member list mustn't evict %s", uid)
	}
}

func TestThreadLeaveFromLeavingServerCountsAsServerLeave(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	delete(api.members, "a")
	api.sent = nil

	api.threadMems = append(api.threadMems, "-a:"+sq.ChannelID)
	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: sq.ChannelID, GuildID: guild, RemovedMembers: []string{"a"}})
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.False(t, inSquad)
	msgs := sentTo(api, sq.ChannelID)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].Content, "left the server")
}

func TestNightlyThreadCheckRetriesFailedThreads(t *testing.T) {
	m, api, db, now := threadModule(t)
	*now = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.threadMems = append(api.threadMems, "-a:"+sq.ChannelID)
	api.threadListErr = errors.New("discord is down")

	m.reconcileThreadsNightly(guild)
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad)

	api.threadListErr = nil
	*now = now.Add(time.Hour)
	m.reconcileThreadsNightly(guild)
	_, inSquad, _ = db.SquadForUser(guild, "a")
	assert.False(t, inSquad, "the failed thread is retried the same day")
}

func TestNightlyThreadCheckSparesMemberWhoRejoinedMidCheck(t *testing.T) {
	m, api, db, now := threadModule(t)
	*now = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.threadMems = append(api.threadMems, "-a:"+sq.ChannelID)
	api.onThreadList = func() {
		api.onThreadList = nil
		lock := m.guildLock(guild)
		lock.Lock()
		defer lock.Unlock()
		_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
		require.NoError(t, err)
		cur, _, _ := db.GetSquad(sq.ID)
		require.NoError(t, m.addMember(guild, cur, &candidate{UserID: "a"}))
	}

	m.reconcileThreadsNightly(guild)
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad, "a's new membership isn't the one that was missing")
}

func TestLockedThreadAccessChangeIsNotRetried(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b")
	member(api, "d", "role-eu")
	api.addErr = errThreadLocked
	require.NoError(t, m.addMember(guild, sq, &candidate{UserID: "d"}))
	ops, err := db.SquadRoleOps(guild)
	require.NoError(t, err)
	assert.Empty(t, ops)
}

func TestStaleThreadLeaveEventSparesMemberAddedBack(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	// a was removed and added back; the removal event arrives afterwards.
	_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
	require.NoError(t, err)
	cur, _, _ := db.GetSquad(sq.ID)
	member(api, "a", "role-eu")
	require.NoError(t, m.addMember(guild, cur, &candidate{UserID: "a"}))

	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: sq.ChannelID, GuildID: guild, RemovedMembers: []string{"a"}})
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad)
	assert.Contains(t, api.threadMembers(sq.ChannelID), "a")
}

func TestThreadLeaveEventSparesMemberAddedBackMidCheck(t *testing.T) {
	m, api, db, _ := threadModule(t)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.threadMems = append(api.threadMems, "-a:"+sq.ChannelID)
	api.onThreadList = func() {
		api.onThreadList = nil
		_, err := m.removeMember(guild, "a", sq.ID, database.SquadLeaveVoluntary, "left")
		require.NoError(t, err)
		cur, _, _ := db.GetSquad(sq.ID)
		member(api, "a", "role-eu")
		require.NoError(t, m.addMember(guild, cur, &candidate{UserID: "a"}))
	}
	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: sq.ChannelID, GuildID: guild, RemovedMembers: []string{"a"}})
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad, "the new membership is kept")
}
