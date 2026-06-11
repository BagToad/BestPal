package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestScamImageHashes_AddListDedupeRemove(t *testing.T) {
	db := newTestDB(t)

	inserted, err := db.AddScamImageHash("p:ff00ff00ff00ff00", "mrbeast", "seed", "seed")
	require.NoError(t, err)
	require.True(t, inserted)

	// Duplicate hash is ignored.
	inserted, err = db.AddScamImageHash("p:ff00ff00ff00ff00", "dupe", "mod1", "command")
	require.NoError(t, err)
	require.False(t, inserted)

	inserted, err = db.AddScamImageHash("p:0123456789abcdef", "", "mod1", "command")
	require.NoError(t, err)
	require.True(t, inserted)

	hashes, err := db.GetScamImageHashes()
	require.NoError(t, err)
	require.Len(t, hashes, 2)
	require.Equal(t, "p:ff00ff00ff00ff00", hashes[0].Hash)
	require.Equal(t, "mrbeast", hashes[0].Label)
	require.Equal(t, "seed", hashes[0].Source)

	removed, err := db.RemoveScamImageHash("p:ff00ff00ff00ff00")
	require.NoError(t, err)
	require.True(t, removed)
	hashes, err = db.GetScamImageHashes()
	require.NoError(t, err)
	require.Len(t, hashes, 1)
	require.Equal(t, "p:0123456789abcdef", hashes[0].Hash)

	// Removing an absent hash reports false, not an error.
	removed, err = db.RemoveScamImageHash("p:ff00ff00ff00ff00")
	require.NoError(t, err)
	require.False(t, removed)
}
