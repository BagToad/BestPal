package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func giveApplied(t *testing.T, db *DB, guildID, userID, name string, count *int64) {
	t.Helper()
	result, err := db.GiveScoreItem(guildID, userID, name, count)
	require.NoError(t, err)
	require.Equal(t, GiveApplied, result)
}

func TestScoreItems_EmptyByDefault(t *testing.T) {
	db := newTestDB(t)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestScoreItems_CountedGivesStackCaseInsensitively(t *testing.T) {
	db := newTestDB(t)

	giveApplied(t, db, "guild1", "user1", "Horses", new(int64(20)))
	giveApplied(t, db, "guild1", "user1", "  horses ", new(int64(4)))
	giveApplied(t, db, "guild1", "user1", "horse", new(int64(1)))

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "Horses", items[0].Name, "first-given spelling is kept")
	require.Equal(t, int64(24), *items[0].Count)
	require.Equal(t, "horse", items[1].Name, "different names do not stack")
	require.Equal(t, int64(1), *items[1].Count)
}

func TestScoreItems_UncountedThings(t *testing.T) {
	db := newTestDB(t)

	giveApplied(t, db, "guild1", "user1", "a can  of eggs", nil)

	result, err := db.GiveScoreItem("guild1", "user1", "A can of eggs", nil)
	require.NoError(t, err)
	require.Equal(t, GiveAlreadyHeld, result)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 1, "repeat uncounted gives don't duplicate")
	require.Equal(t, "a can of eggs", items[0].Name)
	require.Nil(t, items[0].Count)
}

func TestScoreItems_KindMismatchChangesNothing(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "guild1", "user1", "a can of eggs", nil)
	giveApplied(t, db, "guild1", "user1", "horses", new(int64(24)))

	result, err := db.GiveScoreItem("guild1", "user1", "a can of eggs", new(int64(2)))
	require.NoError(t, err)
	require.Equal(t, GiveKindMismatch, result)

	result, err = db.GiveScoreItem("guild1", "user1", "Horses", nil)
	require.NoError(t, err)
	require.Equal(t, GiveKindMismatch, result)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Nil(t, items[0].Count)
	require.Equal(t, int64(24), *items[1].Count)
}

func TestScoreItems_ScopedByGuildAndUserInGiveOrder(t *testing.T) {
	db := newTestDB(t)

	giveApplied(t, db, "guild1", "user1", "giraffe", new(int64(1)))
	giveApplied(t, db, "guild1", "user1", "a black zebra", nil)
	giveApplied(t, db, "guild1", "user2", "giraffe", new(int64(5)))
	giveApplied(t, db, "guild2", "user1", "giraffe", new(int64(9)))

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "giraffe", items[0].Name)
	require.Equal(t, int64(1), *items[0].Count)
	require.Equal(t, "a black zebra", items[1].Name)
}

func TestScoreItems_RejectsInvalidInput(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GiveScoreItem("guild1", "user1", "   ", nil)
	require.Error(t, err)
	_, err = db.GiveScoreItem("guild1", "user1", "horses", new(int64(0)))
	require.Error(t, err)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Empty(t, items)
}
