package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSquadDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSquadPicksLimitAndDuplicates(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	n, added, err := db.AddSquadPick("g", "u", SquadPick{GameID: 1, Name: "A", Year: 2020, Tags: []string{"Shooter", "Sci-fi"}}, 2, now)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.True(t, added)
	n, added, err = db.AddSquadPick("g", "u", SquadPick{GameID: 1, Name: "A"}, 2, now)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.False(t, added)
	_, _, _ = db.AddSquadPick("g", "u", SquadPick{GameID: 2, Name: "B"}, 2, now.Add(time.Second))
	n, added, err = db.AddSquadPick("g", "u", SquadPick{GameID: 3, Name: "C"}, 2, now)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.False(t, added, "limit reached")

	picks, err := db.SquadPicks("g", "u")
	require.NoError(t, err)
	require.Len(t, picks, 2)
	assert.Equal(t, []string{"Shooter", "Sci-fi"}, picks[0].Tags)
	assert.Empty(t, picks[1].Tags)

	require.NoError(t, db.RemoveSquadPick("g", "u", 1))
	all, err := db.AllSquadPicks("g")
	require.NoError(t, err)
	require.Len(t, all["u"], 1)
	assert.Equal(t, int64(2), all["u"][0].GameID)
}

func TestEnqueueKeepsPlace(t *testing.T) {
	db := newSquadDB(t)
	require.NoError(t, db.EnqueueSquadUser("g", "u", "thread", time.Unix(100, 0)))
	require.NoError(t, db.EnqueueSquadUser("g", "u", "", time.Unix(500, 0)))
	e, ok, err := db.SquadQueueEntryFor("g", "u")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(100), e.QueuedAt.Unix())
	assert.Equal(t, "thread", e.IntroThreadID)

	removed, err := db.DequeueSquadUser("g", "u")
	require.NoError(t, err)
	assert.True(t, removed)
	removed, _ = db.DequeueSquadUser("g", "u")
	assert.False(t, removed)
}

func TestCreateSquadMembership(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	for _, u := range []string{"a", "b", "c"} {
		require.NoError(t, db.EnqueueSquadUser("g", u, "", now))
	}
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a", "b"}, now)
	require.NoError(t, err)
	assert.Equal(t, 2, sq.Members)
	q, _ := db.SquadQueue("g")
	require.Len(t, q, 1)
	assert.Equal(t, "c", q[0].UserID)

	_, err = db.CreateSquad(Squad{GuildID: "g", Name: "Squad Badger"}, []string{"c", "a"}, now)
	require.ErrorIs(t, err, ErrSquadMember)
	squads, _ := db.ListSquads("g")
	assert.Len(t, squads, 1, "failed create is rolled back")
	q, _ = db.SquadQueue("g")
	assert.Len(t, q, 1)

	require.ErrorIs(t, db.AddSquadMember("g", sq.ID, "a", now), ErrSquadMember)
	require.NoError(t, db.AddSquadMember("g", sq.ID, "c", now))
	ids, _ := db.SquadMemberIDs(sq.ID)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, ids)

	require.NoError(t, db.SetSquadChannel(sq.ID, "ch", "role"))
	got, ok, err := db.SquadByChannel("ch")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 3, got.Members)

	id, ok, err := db.RemoveSquadMember("g", "b")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, sq.ID, id)

	require.NoError(t, db.MarkSquadWarned(sq.ID, now))
	require.NoError(t, db.TouchSquad(sq.ID, now.Add(time.Hour)))
	got, _, _ = db.GetSquad(sq.ID)
	assert.True(t, got.WarnedAt.IsZero(), "activity clears the warning")

	require.NoError(t, db.DeleteSquad(sq.ID))
	_, ok, _ = db.SquadForUser("g", "a")
	assert.False(t, ok)
}

func TestSquadLastMatch(t *testing.T) {
	db := newSquadDB(t)
	at, err := db.SquadLastMatch("g")
	require.NoError(t, err)
	assert.True(t, at.IsZero())
	require.NoError(t, db.SetSquadLastMatch("g", time.Unix(42, 0)))
	at, _ = db.SquadLastMatch("g")
	assert.Equal(t, int64(42), at.Unix())
}

func TestMarkSquadDeleting(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1_800_000_000, 0)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl"}, []string{"a", "b"}, now)
	require.NoError(t, err)
	require.NoError(t, db.MarkSquadDeleting(sq.ID, "quiet"))
	require.NoError(t, db.MarkSquadDeleting(sq.ID, "later reason"))
	got, _, err := db.GetSquad(sq.ID)
	require.NoError(t, err)
	assert.True(t, got.Deleting())
	assert.Equal(t, "quiet", got.DeleteReason, "the first reason is kept")
}

func TestMigrateSquadsAddsDeleteReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	_, err = db.conn.Exec(`ALTER TABLE squads DROP COLUMN delete_reason`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl"}, []string{"a"}, time.Now())
	require.NoError(t, err)
	squads, err := db.ListSquads("g")
	require.NoError(t, err)
	require.Len(t, squads, 1)
	assert.False(t, squads[0].Deleting())
}
