package forumcache

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockThread creates a minimal discordgo.Channel representing a forum thread.
func mockThread(id, parentID, ownerID, name string, archived bool) *discordgo.Channel {
	return &discordgo.Channel{
		ID:             id,
		ParentID:       parentID,
		OwnerID:        ownerID,
		Name:           name,
		ThreadMetadata: &discordgo.ThreadMetadata{Archived: archived},
	}
}

// TestSeedMetaOwnerLatest ensures seedMeta selects the newest thread per owner.
func TestSeedMetaOwnerLatest(t *testing.T) {
	service := New()
	forumID := "forum-1"
	guildID := "guild-1"

	service.RegisterForum(forumID)

	// Use temp maps like RefreshForum would.
	tempThreads := make(map[string]*ThreadMeta)
	tempOwnerLatest := make(map[string]*ThreadMeta)

	service.seedMeta(tempThreads, tempOwnerLatest, guildID, forumID, mockThread("100", forumID, "ownerA", "first", false))
	service.seedMeta(tempThreads, tempOwnerLatest, guildID, forumID, mockThread("200", forumID, "ownerA", "second", false))

	require.Len(t, tempThreads, 2)
	latest, ok := tempOwnerLatest["ownerA"]
	require.True(t, ok, "expected ownerA to be tracked")
	assert.Equal(t, "200", latest.ID)

	service.mu.RLock()
	idx := service.forums[forumID]
	service.mu.RUnlock()
	idx.mu.Lock()
	idx.threads = tempThreads
	idx.ownerLatest = tempOwnerLatest
	idx.mu.Unlock()

	res, ok := service.GetLatestUserThread(forumID, "ownerA")
	require.True(t, ok, "expected cache hit")
	require.NotNil(t, res)
	assert.Equal(t, "200", res.ID)
}

// TestOnThreadCreate verifies create event adds a thread and updates owner latest.
func TestOnThreadCreate(t *testing.T) {
	svc := New()
	forumID := "forum-ct"
	svc.RegisterForum(forumID)
	thread := mockThread("300", forumID, "ownerX", "hello", false)
	svc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: thread})

	meta, ok := svc.GetLatestUserThread(forumID, "ownerX")
	require.True(t, ok)
	require.NotNil(t, meta)
	assert.Equal(t, "300", meta.ID)
	stats, _ := svc.Stats(forumID)
	assert.Equal(t, 1, stats.EventAdds)
}

// TestOnThreadUpdateUnknown ensures an update for an unknown thread increments anomalies.
func TestOnThreadUpdateUnknown(t *testing.T) {
	svc := New()
	forumID := "forum-up"
	svc.RegisterForum(forumID)
	unknown := mockThread("999", forumID, "ownerY", "ghost", false)
	svc.OnThreadUpdate(nil, &discordgo.ThreadUpdate{Channel: unknown})
	stats, _ := svc.Stats(forumID)
	assert.Equal(t, 1, stats.Anomalies)
	assert.Equal(t, 1, stats.EventUpdates)
}

// TestOnThreadDeleteFallback verifies deletion recalculates owner latest correctly.
func TestOnThreadDeleteFallback(t *testing.T) {
	svc := New()
	forumID := "forum-del"
	svc.RegisterForum(forumID)
	first := mockThread("100", forumID, "ownerZ", "old", false)
	second := mockThread("200", forumID, "ownerZ", "new", false)
	svc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: first})
	svc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: second})
	meta, _ := svc.GetLatestUserThread(forumID, "ownerZ")
	assert.Equal(t, "200", meta.ID)
	svc.OnThreadDelete(nil, &discordgo.ThreadDelete{Channel: second})
	meta2, ok := svc.GetLatestUserThread(forumID, "ownerZ")
	require.True(t, ok)
	assert.Equal(t, "100", meta2.ID)
	stats, _ := svc.Stats(forumID)
	assert.Equal(t, 1, stats.EventDeletes)
}

// TestOnThreadListSync merges provided subset of threads.
func TestOnThreadListSync(t *testing.T) {
	svc := New()
	forumID := "forum-ls"
	svc.RegisterForum(forumID)
	existing := mockThread("10", forumID, "ownerA", "ex", false)
	svc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: existing})
	newer := mockThread("20", forumID, "ownerA", "newer", false)
	other := mockThread("30", forumID, "ownerB", "other", false)
	evt := &discordgo.ThreadListSync{Threads: []*discordgo.Channel{newer, other}}
	svc.OnThreadListSync(nil, evt)
	latestA, _ := svc.GetLatestUserThread(forumID, "ownerA")
	assert.Equal(t, "20", latestA.ID)
	latestB, okB := svc.GetLatestUserThread(forumID, "ownerB")
	require.True(t, okB)
	assert.Equal(t, "30", latestB.ID)
}

// TestStatsAccumulation ensures counters reflect sequence of events.
func TestStatsAccumulation(t *testing.T) {
	svc := New()
	forumID := "forum-stats"
	svc.RegisterForum(forumID)
	a := mockThread("1", forumID, "u1", "a", false)
	b := mockThread("2", forumID, "u1", "b", false)
	c := mockThread("3", forumID, "u2", "c", false)

	svc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: a})
	svc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: b})
	svc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: c})
	unknown := mockThread("999", forumID, "uX", "x", false)
	svc.OnThreadUpdate(nil, &discordgo.ThreadUpdate{Channel: unknown})
	svc.OnThreadDelete(nil, &discordgo.ThreadDelete{Channel: b})

	stats, _ := svc.Stats(forumID)
	assert.Equal(t, 3, stats.EventAdds)
	assert.Equal(t, 1, stats.EventUpdates)
	assert.Equal(t, 1, stats.EventDeletes)
	assert.Equal(t, 1, stats.Anomalies)
	assert.Equal(t, 2, stats.Threads) // b deleted
}

// --- RefreshForum tests (using mock lister seam) ---

// mockLister implements threadLister for deterministic RefreshForum tests.
type mockLister struct {
	activeErr       error
	active          []*discordgo.Channel
	archivedBatches [][]*discordgo.Channel
	archivedHasMore []bool
	archivedErrs    []error
	archivedCall    int
}

func (m *mockLister) ListActiveThreads(guildID string) ([]*discordgo.Channel, error) {
	return m.active, m.activeErr
}

func (m *mockLister) ListArchivedThreads(forumID string, before *time.Time, limit int) ([]*discordgo.Channel, bool, error) {
	i := m.archivedCall
	m.archivedCall++
	if i >= len(m.archivedBatches) {
		return nil, false, nil
	}
	return m.archivedBatches[i], m.archivedHasMore[i], m.archivedErrs[i]
}

func TestRefreshForum_SinglePage(t *testing.T) {
	svc := New()
	guildID := "g1"
	forumID := "f1"
	l := &mockLister{
		active: []*discordgo.Channel{
			mockThread("10", forumID, "u1", "t10", false), // belongs
			mockThread("99", "other", "uX", "t99", false), // filtered out
		},
		archivedBatches: [][]*discordgo.Channel{
			{mockThread("20", forumID, "u2", "t20", false)},
		},
		archivedHasMore: []bool{false},
		archivedErrs:    []error{nil},
	}
	err := svc.refreshForumWithLister(guildID, forumID, l)
	require.NoError(t, err)
	// u1 latest 10, u2 latest 20.
	m1, ok1 := svc.GetLatestUserThread(forumID, "u1")
	require.True(t, ok1)
	assert.Equal(t, "10", m1.ID)
	m2, ok2 := svc.GetLatestUserThread(forumID, "u2")
	require.True(t, ok2)
	assert.Equal(t, "20", m2.ID)
	stats, _ := svc.Stats(forumID)
	assert.Equal(t, 2, stats.Threads)
	assert.Equal(t, 2, stats.OwnersTracked)
	assert.True(t, !stats.LastFullSync.IsZero())
}

func TestRefreshForum_ActiveError(t *testing.T) {
	svc := New()
	guildID := "g2"
	forumID := "f2"
	l := &mockLister{activeErr: assert.AnError}
	err := svc.refreshForumWithLister(guildID, forumID, l)
	require.Error(t, err)
	stats, ok := svc.Stats(forumID)
	require.True(t, ok)
	assert.Equal(t, 1, stats.FullSyncErrors)
	assert.Equal(t, 0, stats.Threads)
}

func TestRefreshForum_MultiPageArchived(t *testing.T) {
	svc := New()
	guildID := "g3"
	forumID := "f3"
	l := &mockLister{
		active: []*discordgo.Channel{mockThread("1", forumID, "uA", "t1", false)},
		archivedBatches: [][]*discordgo.Channel{
			{mockThread("2", forumID, "uB", "t2", false), mockThread("3", forumID, "uB", "t3", false)},
			{mockThread("4", forumID, "uC", "t4", false)},
		},
		archivedHasMore: []bool{true, false},
		archivedErrs:    []error{nil, nil},
	}
	err := svc.refreshForumWithLister(guildID, forumID, l)
	require.NoError(t, err)
	// owner uB latest should be 3 since higher ID.
	mb, okb := svc.GetLatestUserThread(forumID, "uB")
	require.True(t, okb)
	assert.Equal(t, "3", mb.ID)
	stats, _ := svc.Stats(forumID)
	assert.Equal(t, 4, stats.Threads)
	assert.Equal(t, 3, stats.OwnersTracked)
}

func TestRefreshForum_ArchivedEarlyErrorStops(t *testing.T) {
	svc := New()
	guildID := "g4"
	forumID := "f4"
	l := &mockLister{
		active: []*discordgo.Channel{mockThread("1", forumID, "uA", "t1", false)},
		archivedBatches: [][]*discordgo.Channel{
			{mockThread("2", forumID, "uB", "t2", false)},     // first page ok
			{mockThread("999", forumID, "uX", "t999", false)}, // would be second page but error triggers
		},
		archivedHasMore: []bool{true, true},
		archivedErrs:    []error{nil, assert.AnError},
	}
	err := svc.refreshForumWithLister(guildID, forumID, l)
	// Early archived error should be swallowed (best-effort) => no error returned.
	require.NoError(t, err)
	// Thread 999 should NOT appear due to error.
	_, existsX := svc.GetLatestUserThread(forumID, "uX")
	assert.False(t, existsX)
	stats, _ := svc.Stats(forumID)
	assert.Equal(t, 2, stats.Threads)
}
