package database

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntroFeedEligibilityExactExpiry(t *testing.T) {
	db := newTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		require.NoError(t, db.RecordIntroFeedPost("user", "old-intro", "feed", true))
		_, err := db.conn.Exec("UPDATE intro_feed_posts SET posted_at = ?", now.Add(-48*time.Hour).UTC().Format("2006-01-02 15:04:05"))
		require.NoError(t, err)
		eligible, remaining, err := db.IsUserEligibleForIntroFeed("user", 48)
		require.NoError(t, err)
		require.True(t, eligible, "exactly equal expiry must be eligible on the scheduled run")
		require.Zero(t, remaining)
		_, err = db.conn.Exec("UPDATE intro_feed_posts SET posted_at = ?", now.Add(-48*time.Hour+time.Second).UTC().Format("2006-01-02 15:04:05"))
		require.NoError(t, err)
		eligible, remaining, err = db.IsUserEligibleForIntroFeed("user", 48)
		require.NoError(t, err)
		require.False(t, eligible)
		require.Equal(t, time.Second, remaining)
		time.Sleep(2 * time.Second)
		eligible, remaining, err = db.IsUserEligibleForIntroFeed("user", 48)
		require.NoError(t, err)
		require.True(t, eligible)
		require.Zero(t, remaining)
	})
}
