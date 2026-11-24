package squads

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queueGroups queues n groups of six who only share a game within their group.
func queueGroups(t *testing.T, m *Module, api *fakeAPI, now *time.Time, n int) {
	t.Helper()
	for g := range n {
		var uids []string
		for u := range 6 {
			uids = append(uids, fmt.Sprintf("g%du%d", g, u))
		}
		queue(t, m, api, m.db, now.Add(-time.Hour), uids, []int64{int64(100 + g)}, "role-eu", "role-25")
	}
}

func TestDryRunRespectsDiscordLimits(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	queueGroups(t, m, api, now, 3)
	api.extraInCategory = 49

	dry, err := m.runMatch(guild, true)
	require.NoError(t, err)
	assert.Len(t, dry.Plan.NewSquads, 1)
	assert.Len(t, dry.Plan.Waiting, 12)
	text := reportText(dry, true)
	assert.Contains(t, text, "Only room for 1 new squad: the squads category has 49 of 50 channels")

	real, err := m.runMatch(guild, false)
	require.NoError(t, err)
	assert.Len(t, real.Created, 1)
	assert.Len(t, real.Plan.NewSquads, 1, "the real run matches the dry run")
	assert.Empty(t, real.Plan.Unmatched)
	q, _ := db.SquadQueue(guild)
	assert.Len(t, q, 12, "the rest stay queued")
}

func TestRoleLimitCapsSquads(t *testing.T) {
	m, api, _, now := newTestModule(t, nil)
	queueGroups(t, m, api, now, 3)
	api.extraRoles = 248
	dry, err := m.runMatch(guild, true)
	require.NoError(t, err)
	assert.Len(t, dry.Plan.NewSquads, 2)
	assert.Contains(t, reportText(dry, true), "the server has 248 of 250 roles")
}

func TestLimitCheckFailureDoesNotBlockMatching(t *testing.T) {
	m, api, _, now := newTestModule(t, nil)
	queueGroups(t, m, api, now, 2)
	api.countsErr = errors.New("boom")
	dry, err := m.runMatch(guild, true)
	require.NoError(t, err)
	assert.Len(t, dry.Plan.NewSquads, 2)
	assert.Empty(t, dry.Failures)
}

func TestRunNowReportsOnlyCreatedSquads(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	queueGroups(t, m, api, now, 3)
	api.channelCap = 1

	r, err := m.runMatch(guild, false)
	require.NoError(t, err)
	assert.Len(t, r.Created, 1)
	assert.Len(t, r.Plan.NewSquads, 1)
	assert.Len(t, r.Plan.Waiting, 12)
	text := reportText(r, false)
	assert.Contains(t, text, "create squad:")
	assert.Equal(t, 1, countSubstr(text, "\n• <@"), "only the created squad is listed as new: %s", text)
	q, _ := db.SquadQueue(guild)
	assert.Len(t, q, 12)
}

func countSubstr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

func TestCapacityWarningSurvivesLongReports(t *testing.T) {
	m, api, _, now := newTestModule(t, nil)
	for g := range 15 {
		var uids []string
		for u := range 6 {
			uids = append(uids, fmt.Sprintf("1%017d", g*10+u))
		}
		queue(t, m, api, m.db, now.Add(-time.Hour), uids, []int64{int64(100 + g)}, "role-eu", "role-25")
	}
	api.extraInCategory = 50
	dry, err := m.runMatch(guild, true)
	require.NoError(t, err)
	assert.Empty(t, dry.Plan.NewSquads)
	text := reportText(dry, true)
	assert.LessOrEqual(t, len([]rune(text)), 2000)
	assert.Contains(t, text, "Only room for 0 new squads: the squads category has 50 of 50 channels")
}
