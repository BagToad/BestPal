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

func TestScoreItems_Leaderboard(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "g1", "u1", "Horses", 3)
	giveApplied(t, db, "g1", "u2", "horses", 7)
	giveApplied(t, db, "g1", "u3", "horses", 3)
	giveApplied(t, db, "g2", "u9", "horses", 100)
	giveApplied(t, db, "g1", "u9", "giraffe", 50)

	name, entries, err := db.GetScoreLeaderboard("g1", " HORSES ", 10)
	require.NoError(t, err)
	require.Equal(t, "Horses", name, "first-given spelling")
	require.Equal(t, []ScoreLeaderboardEntry{{"u2", 7}, {"u1", 3}, {"u3", 3}}, entries, "ties in give order, scoped to guild")

	_, entries, err = db.GetScoreLeaderboard("g1", "horses", 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	name, entries, err = db.GetScoreLeaderboard("g1", "zebras", 10)
	require.NoError(t, err)
	require.Empty(t, name)
	require.Empty(t, entries)
}

func TestScoreItems_RankAndPurgedMembers(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "g1", "u1", "horses", 9)
	giveApplied(t, db, "g1", "u2", "horses", 5)
	giveApplied(t, db, "g1", "u3", "horses", 5)
	giveApplied(t, db, "g1", "u4", "horses", 1)

	count, rank, err := db.GetScoreRank("g1", "HORSES", "u4")
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	require.Equal(t, 4, rank)

	giveApplied(t, db, "g1", "u1", "zebras", 2)
	giveApplied(t, db, "g2", "u1", "horses", 7)
	require.NoError(t, db.PurgeScoreMember("g1", "u1"))
	require.NoError(t, db.PurgeScoreMember("g1", "u1"), "idempotent")
	_, rank, err = db.GetScoreRank("g1", "horses", "u4")
	require.NoError(t, err)
	require.Equal(t, 3, rank, "purged members don't count")
	_, rank, err = db.GetScoreRank("g1", "horses", "u3")
	require.NoError(t, err)
	require.Equal(t, 1, rank, "ties share a rank")

	_, entries, err := db.GetScoreLeaderboard("g1", "horses", 10)
	require.NoError(t, err)
	require.Equal(t, []ScoreLeaderboardEntry{{"u2", 5}, {"u3", 5}, {"u4", 1}}, entries)
	items, err := db.GetScoreItems("g1", "u1")
	require.NoError(t, err)
	require.Empty(t, items, "everything they held is gone")
	names, err := db.SuggestScoreItemNames("g1", "", "")
	require.NoError(t, err)
	require.Equal(t, []string{"horses"}, names, "things only they held are forgotten")
	items, err = db.GetScoreItems("g2", "u1")
	require.NoError(t, err)
	require.Len(t, items, 1, "other servers are untouched")

	count, rank, err = db.GetScoreRank("g1", "zebras", "u1")
	require.NoError(t, err)
	require.Zero(t, count)
	require.Zero(t, rank)
}

func TestScoreItems_Rename(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "g1", "u1", "hroses", 2)
	giveApplied(t, db, "g1", "u2", "Hroses", 3)
	giveApplied(t, db, "g1", "u2", "horses", 10)
	giveApplied(t, db, "g1", "u3", "horses", 1)
	giveApplied(t, db, "g2", "u1", "hroses", 7)

	result, people, err := db.RenameScoreItem("g1", "HROSES", "Horses")
	require.NoError(t, err)
	require.Equal(t, RenameApplied, result)
	require.Equal(t, 2, people)

	for user, want := range map[string]int64{"u1": 2, "u2": 13, "u3": 1} {
		items, err := db.GetScoreItems("g1", user)
		require.NoError(t, err)
		require.Equal(t, []ScoreItem{{"Horses", want}}, items, user)
	}
	items, err := db.GetScoreItems("g2", "u1")
	require.NoError(t, err)
	require.Equal(t, []ScoreItem{{"hroses", 7}}, items, "other guilds untouched")

	result, people, err = db.RenameScoreItem("g1", "horses", "HORSES")
	require.NoError(t, err)
	require.Equal(t, RenameApplied, result, "respelling the same thing")
	require.Equal(t, 3, people)

	result, _, err = db.RenameScoreItem("g1", "zebras", "horses")
	require.NoError(t, err)
	require.Equal(t, RenameNotHeld, result)

	_, _, err = db.RenameScoreItem("g1", "horses", "  ")
	require.Error(t, err)
}

func TestScoreItems_RenameOverflowChangesNothing(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "g1", "u1", "a", 1)
	giveApplied(t, db, "g1", "u2", "a", 1)
	giveApplied(t, db, "g1", "u2", "b", math.MaxInt64)

	result, _, err := db.RenameScoreItem("g1", "a", "b")
	require.NoError(t, err)
	require.Equal(t, RenameOverflow, result)

	items, err := db.GetScoreItems("g1", "u1")
	require.NoError(t, err)
	require.Equal(t, []ScoreItem{{"a", 1}}, items, "rolled back for everyone")
}

func TestScoreItems_Wipe(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "g1", "u1", "hroses", 2)
	giveApplied(t, db, "g1", "u2", "Hroses", 3)
	giveApplied(t, db, "g1", "u2", "giraffe", 1)
	giveApplied(t, db, "g2", "u1", "hroses", 7)

	people, err := db.WipeScoreItem("g1", "hroses")
	require.NoError(t, err)
	require.Equal(t, 2, people)

	names, err := db.SuggestScoreItemNames("g1", "", "")
	require.NoError(t, err)
	require.Equal(t, []string{"giraffe"}, names)
	names, err = db.SuggestScoreItemNames("g2", "", "")
	require.NoError(t, err)
	require.Equal(t, []string{"hroses"}, names)

	people, err = db.WipeScoreItem("g1", "hroses")
	require.NoError(t, err)
	require.Zero(t, people)
}

func TestScoreItems_Totals(t *testing.T) {
	db := newTestDB(t)
	giveApplied(t, db, "g1", "u1", "Horses", 3)
	giveApplied(t, db, "g1", "u2", "cookies", 1)
	giveApplied(t, db, "g1", "u2", "horses", 4)
	giveApplied(t, db, "g1", "u3", "zebras", math.MaxInt64)
	giveApplied(t, db, "g1", "u4", "zebras", 5)
	giveApplied(t, db, "g2", "u1", "horses", 100)

	totals, err := db.GetScoreItemTotals("g1")
	require.NoError(t, err)
	require.Equal(t, []ScoreItem{{"Horses", 7}, {"cookies", 1}, {"zebras", math.MaxInt64}}, totals, "merged case-insensitively, first spelling, saturating, scoped to guild")

	totals, err = db.GetScoreItemTotals("empty")
	require.NoError(t, err)
	require.Empty(t, totals)
}
