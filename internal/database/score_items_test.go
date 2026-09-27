package database

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func giveApplied(t *testing.T, db *DB, guildID, userID, name string, count int64) {
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

func TestScoreItems_GivesStackCaseInsensitively(t *testing.T) {
	db := newTestDB(t)

	giveApplied(t, db, "guild1", "user1", "Horses", 20)
	giveApplied(t, db, "guild1", "user1", "  horses ", 4)
	giveApplied(t, db, "guild1", "user1", "horse", 1)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, ScoreItem{Name: "Horses", Count: 24}, items[0], "first-given spelling is kept")
	require.Equal(t, ScoreItem{Name: "horse", Count: 1}, items[1], "different names do not stack")
}

func TestScoreItems_GiveOverflowChangesNothing(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "guild1", "user1", "horses", math.MaxInt64-1)
	giveApplied(t, db, "guild1", "user1", "horses", 1)

	result, err := db.GiveScoreItem("guild1", "user1", "horses", 1)
	require.NoError(t, err)
	require.Equal(t, GiveOverflow, result)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64), items[0].Count)
}

func TestScoreItems_Take(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "guild1", "user1", "Horses", 5)
	giveApplied(t, db, "guild1", "user1", "giraffe", 1)

	result, held, err := db.TakeScoreItem("guild1", "user1", "horses", 3)
	require.NoError(t, err)
	require.Equal(t, TakeApplied, result)
	require.Equal(t, int64(5), held)

	result, held, err = db.TakeScoreItem("guild1", "user1", "horses", 3)
	require.NoError(t, err)
	require.Equal(t, TakeInsufficient, result)
	require.Equal(t, int64(2), held)

	result, _, err = db.TakeScoreItem("guild1", "user1", "zebra", 1)
	require.NoError(t, err)
	require.Equal(t, TakeNotHeld, result)

	result, _, err = db.TakeScoreItem("guild1", "user1", "GIRAFFE", 1)
	require.NoError(t, err)
	require.Equal(t, TakeApplied, result)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Equal(t, []ScoreItem{{Name: "Horses", Count: 2}}, items, "things taken to zero are removed")
}

func TestScoreItems_ScopedByGuildAndUserInGiveOrder(t *testing.T) {
	db := newTestDB(t)

	giveApplied(t, db, "guild1", "user1", "giraffe", 1)
	giveApplied(t, db, "guild1", "user1", "black zebras", 2)
	giveApplied(t, db, "guild1", "user2", "giraffe", 5)
	giveApplied(t, db, "guild2", "user1", "giraffe", 9)

	result, _, err := db.TakeScoreItem("guild2", "user2", "giraffe", 1)
	require.NoError(t, err)
	require.Equal(t, TakeNotHeld, result)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Equal(t, []ScoreItem{{Name: "giraffe", Count: 1}, {Name: "black zebras", Count: 2}}, items)
}

func TestScoreItems_RejectsInvalidInput(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GiveScoreItem("guild1", "user1", "   ", 1)
	require.Error(t, err)
	_, err = db.GiveScoreItem("guild1", "user1", "horses", 0)
	require.Error(t, err)
	_, _, err = db.TakeScoreItem("guild1", "user1", "horses", -1)
	require.Error(t, err)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestScoreItems_TakeAll(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "guild1", "user1", "Horses", 24)

	result, held, err := db.TakeAllScoreItem("guild1", "user1", "horses")
	require.NoError(t, err)
	require.Equal(t, TakeApplied, result)
	require.Equal(t, int64(24), held)

	result, _, err = db.TakeAllScoreItem("guild1", "user1", "horses")
	require.NoError(t, err)
	require.Equal(t, TakeNotHeld, result)

	items, err := db.GetScoreItems("guild1", "user1")
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestScoreItems_Suggest(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "guild1", "user1", "Black Zebras", 1)
	giveApplied(t, db, "guild1", "user2", "black zebras", 1)
	giveApplied(t, db, "guild1", "user2", "Émus", 1)
	giveApplied(t, db, "guild2", "user1", "giraffe", 1)

	names, err := db.SuggestScoreItemNames("guild1", "", "")
	require.NoError(t, err)
	require.Equal(t, []string{"Black Zebras", "Émus"}, names)

	names, err = db.SuggestScoreItemNames("guild1", "", "ému")
	require.NoError(t, err)
	require.Equal(t, []string{"Émus"}, names, "matching is Unicode case-insensitive")

	names, err = db.SuggestScoreItemNames("guild1", "user1", "")
	require.NoError(t, err)
	require.Equal(t, []string{"Black Zebras"}, names)

	for n := range 30 {
		giveApplied(t, db, "guild3", "user1", fmt.Sprintf("thing %02d", n), 1)
	}
	names, err = db.SuggestScoreItemNames("guild3", "", "thing")
	require.NoError(t, err)
	require.Len(t, names, 25)
}
