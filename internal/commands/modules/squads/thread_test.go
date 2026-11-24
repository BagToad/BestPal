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
	assert.Contains(t, api.locked, sq.ChannelID)
	assert.Empty(t, api.delCh)
}
