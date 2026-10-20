package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTOTDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(filepath.Join(t.TempDir(), "tot.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var totNow = time.Unix(1_790_000_000, 0)

func noLog(TreatResult) string { return "" }

func claim(t *testing.T, db *DB, msg, user string) TreatResult {
	t.Helper()
	res, err := db.ClaimTreat(msg, user, totNow, 30*time.Minute, func(r TreatResult) string { return user + " grabbed" })
	require.NoError(t, err)
	return res
}

func emptyBowl(t *testing.T, db *DB, msg string) {
	t.Helper()
	for n := range BowlSize {
		require.Equal(t, TreatClaimed, claim(t, db, msg, "filler"+string(rune('a'+n))).Status)
	}
}

func TestTOT_TreatsEmptyTheBowlOncePerUser(t *testing.T) {
	db := newTOTDB(t)
	require.NoError(t, db.CreateBowl("g", "c", "m", totNow))

	res := claim(t, db, "m", "u1")
	assert.Equal(t, TreatClaimed, res.Status)
	assert.Equal(t, int64(1), res.Awarded)
	assert.Equal(t, int64(1), res.Candies)
	assert.Equal(t, BowlSize-1, res.Bowl.Remaining)
	assert.True(t, res.Bowl.ExpiresAt.IsZero())

	assert.Equal(t, TreatAlreadyClaimed, claim(t, db, "m", "u1").Status)

	for n := 2; n <= BowlSize; n++ {
		res = claim(t, db, "m", "u"+string(rune('0'+n)))
		require.Equal(t, TreatClaimed, res.Status)
	}
	assert.Equal(t, 0, res.Bowl.Remaining)
	assert.Equal(t, totNow.Add(30*time.Minute).Unix(), res.Bowl.ExpiresAt.Unix(), "emptying starts the trick window")
	assert.Equal(t, TreatBowlEmpty, claim(t, db, "m", "late").Status)

	b, log, ok, err := db.GetBowl("m")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 0, b.Remaining)
	require.Len(t, log, BowlSize)
	assert.Equal(t, "u1 grabbed", log[0].Text)

	assert.Equal(t, TreatBowlClosed, claim(t, db, "unknown", "u1").Status)
}

func TestTOT_TimeoutUsesOneChargePerBowl(t *testing.T) {
	db := newTOTDB(t)
	for _, m := range []string{"b1", "b2", "b3"} {
		require.NoError(t, db.CreateBowl("g", "c", m, totNow))
	}
	emptyBowl(t, db, "b1")
	_, err := db.ApplyTrick("b1", "u", totNow, func(tx TrickTx) (string, error) {
		_, err := tx.GrantTimeout("u", 2, "Barking Guard Dog")
		return "", err
	})
	require.NoError(t, err)

	res := claim(t, db, "b2", "u")
	assert.Equal(t, TreatTimedOut, res.Status)
	assert.Equal(t, 1, res.TimeoutCharges)
	res = claim(t, db, "b2", "u")
	assert.Equal(t, TreatStillTimedOut, res.Status, "clicking the same bowl again doesn't burn another charge")
	assert.Equal(t, 1, res.TimeoutCharges)

	assert.Equal(t, TreatTimedOut, claim(t, db, "b3", "u").Status)
	u, _, _, err := db.GetBucket("g", "u")
	require.NoError(t, err)
	assert.Equal(t, 0, u.TimeoutCharges)
	assert.Empty(t, u.TimeoutSource, "source clears with the last charge")

	b, _, _, err := db.GetBowl("b2")
	require.NoError(t, err)
	assert.Equal(t, BowlSize, b.Remaining, "timeouts don't take candy")

	require.NoError(t, db.CreateBowl("g", "c", "b4", totNow))
	assert.Equal(t, TreatClaimed, claim(t, db, "b4", "u").Status)
}

func TestTOT_BonusPaysOutOnNextClaim(t *testing.T) {
	db := newTOTDB(t)
	require.NoError(t, db.CreateBowl("g", "c", "b1", totNow))
	require.NoError(t, db.CreateBowl("g", "c", "b2", totNow))
	emptyBowl(t, db, "b1")
	_, err := db.ApplyTrick("b1", "u", totNow, func(tx TrickTx) (string, error) {
		if _, err := tx.GrantBonus("u", 3, "Full-Sized Bar House"); err != nil {
			return "", err
		}
		current, err := tx.GrantBonus("u", 1, "Ding Dong Ditch")
		assert.Equal(t, 3, current, "the smaller grant reports the kept bonus")
		return "", err
	})
	require.NoError(t, err)

	res := claim(t, db, "b2", "u")
	assert.Equal(t, TreatClaimed, res.Status)
	assert.Equal(t, 3, res.Bonus, "the bigger bonus is kept")
	assert.Equal(t, "Full-Sized Bar House", res.BonusSource)
	assert.Equal(t, int64(4), res.Awarded)
	assert.Equal(t, BowlSize-1, res.Bowl.Remaining, "only one candy leaves the bowl")

	u, _, _, err := db.GetBucket("g", "u")
	require.NoError(t, err)
	assert.Equal(t, int64(4), u.Candies)
	assert.Zero(t, u.BonusCandy)
}

func TestTOT_TrickRules(t *testing.T) {
	db := newTOTDB(t)
	require.NoError(t, db.CreateBowl("g", "c", "m", totNow))
	ok := func(TrickTx) (string, error) { return "log line", nil }

	res, err := db.ApplyTrick("m", "u", totNow, ok)
	require.NoError(t, err)
	assert.Equal(t, TrickBowlNotEmpty, res.Status)

	emptyBowl(t, db, "m")
	res, err = db.ApplyTrick("m", "fillera", totNow, ok)
	require.NoError(t, err)
	assert.Equal(t, TrickApplied, res.Status, "a treat doesn't block a trick")
	res, err = db.ApplyTrick("m", "fillera", totNow, ok)
	require.NoError(t, err)
	assert.Equal(t, TrickAlreadyDone, res.Status)

	res, err = db.ApplyTrick("m", "u", totNow.Add(30*time.Minute), ok)
	require.NoError(t, err)
	assert.Equal(t, TrickBowlClosed, res.Status, "window closed")

	_, log, _, err := db.GetBowl("m")
	require.NoError(t, err)
	assert.Equal(t, "log line", log[len(log)-1].Text)
	assert.Len(t, log, BowlSize+1)
}

func TestTOT_TrickTxHelpers(t *testing.T) {
	db := newTOTDB(t)
	require.NoError(t, db.CreateBowl("g", "c", "m", totNow))
	require.NoError(t, db.CreateBowl("g", "c", "other", totNow))
	emptyBowl(t, db, "other")
	// "late" clicked this bowl but got no treat; "early" got one.
	emptyBowl(t, db, "m")
	require.NoError(t, db.CreateBowl("g", "c", "m2", totNow))

	_, err := db.ApplyTrick("m", "late", totNow, func(TrickTx) (string, error) { return "", nil })
	require.NoError(t, err)

	_, err = db.ApplyTrick("m", "fillera", totNow, func(tx TrickTx) (string, error) {
		got, err := tx.EmptyHandedParticipants("fillera")
		require.NoError(t, err)
		assert.Equal(t, []string{"late"}, got)

		d, err := tx.AddCandies("fillera", -10)
		require.NoError(t, err)
		assert.Equal(t, int64(-2), d, "fillera had 2 (one per bowl); candies never go negative")

		low, found, err := tx.LowestEarner("fillera")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "late", low, "late has no candies at all")

		require.NoError(t, tx.AddSouvenir("fillera", "eggshell"))
		require.NoError(t, tx.AddSouvenir("fillera", "eggshell"))
		return "", nil
	})
	require.NoError(t, err)

	u, items, rank, err := db.GetBucket("g", "fillera")
	require.NoError(t, err)
	assert.Zero(t, u.Candies)
	assert.Equal(t, []Souvenir{{Type: "eggshell", Quantity: 2}}, items)
	assert.Equal(t, 10, rank, "9 other fillers have 2 candies each")
}

func TestTOT_LeaderboardAndRankTies(t *testing.T) {
	db := newTOTDB(t)
	require.NoError(t, db.CreateBowl("g", "c", "m", totNow))
	require.NoError(t, db.CreateBowl("g", "c", "m2", totNow))
	claim(t, db, "m", "a")
	claim(t, db, "m", "b")
	claim(t, db, "m2", "a")
	claim(t, db, "m", "c")
	claim(t, db, "m2", "c")

	lb, err := db.CandyLeaderboard("g", 10)
	require.NoError(t, err)
	require.Len(t, lb, 3)
	assert.Equal(t, CandyLeaderboardEntry{UserID: "a", Candies: 2, Rank: 1}, lb[0])
	assert.Equal(t, CandyLeaderboardEntry{UserID: "c", Candies: 2, Rank: 1}, lb[1])
	assert.Equal(t, CandyLeaderboardEntry{UserID: "b", Candies: 1, Rank: 3}, lb[2])

	_, _, rank, err := db.GetBucket("g", "b")
	require.NoError(t, err)
	assert.Equal(t, 3, rank)

	lb, err = db.CandyLeaderboard("other-guild", 10)
	require.NoError(t, err)
	assert.Empty(t, lb)
}

func TestTOT_ExpireAndReset(t *testing.T) {
	db := newTOTDB(t)
	require.NoError(t, db.CreateBowl("g", "c1", "m", totNow))
	require.NoError(t, db.CreateBowl("g", "c2", "full", totNow))
	emptyBowl(t, db, "m")

	active, err := db.ActiveBowlChannels("g")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"c1": true, "c2": true}, active)

	expired, err := db.ExpiredBowls(totNow.Add(29 * time.Minute))
	require.NoError(t, err)
	assert.Empty(t, expired)
	expired, err = db.ExpiredBowls(totNow.Add(30 * time.Minute))
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, "m", expired[0].MessageID)

	require.NoError(t, db.ArchiveBowl("m"))
	active, err = db.ActiveBowlChannels("g")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"c2": true}, active)

	_, ok, err := db.ResetBowl("other-guild", "m")
	require.NoError(t, err)
	assert.False(t, ok, "another guild can't reset this bowl")
	b, _, _, err := db.GetBowl("m")
	require.NoError(t, err)
	assert.False(t, b.Active, "a cross-guild reset leaves the bowl alone")

	b, ok, err = db.ResetBowl("g", "m")
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, b.Active)
	assert.Equal(t, BowlSize, b.Remaining)
	assert.True(t, b.ExpiresAt.IsZero())
	_, log, _, err := db.GetBowl("m")
	require.NoError(t, err)
	assert.Empty(t, log)
	assert.Equal(t, TreatClaimed, claim(t, db, "m", "fillera").Status, "participants are cleared")

	_, ok, err = db.ResetBowl("g", "nope")
	require.NoError(t, err)
	assert.False(t, ok)
}
