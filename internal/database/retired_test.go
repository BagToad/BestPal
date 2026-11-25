package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const oldSlidePuzzleSchema = `
CREATE TABLE slide_puzzles (
	guild_id TEXT PRIMARY KEY, channel_id TEXT NOT NULL, message_id TEXT NOT NULL DEFAULT '',
	image_id INTEGER NOT NULL DEFAULT 0, grid INTEGER NOT NULL, board TEXT NOT NULL,
	round INTEGER NOT NULL DEFAULT 1, round_ends INTEGER NOT NULL,
	total_votes INTEGER NOT NULL DEFAULT 0, total_moves INTEGER NOT NULL DEFAULT 0,
	solved INTEGER NOT NULL DEFAULT 0, needs_redraw INTEGER NOT NULL DEFAULT 0,
	needs_refresh INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL);
CREATE TABLE slide_puzzle_votes (
	guild_id TEXT NOT NULL, round INTEGER NOT NULL, user_id TEXT NOT NULL,
	tile INTEGER NOT NULL, voted_at INTEGER NOT NULL, PRIMARY KEY (guild_id, round, user_id));
CREATE TABLE slide_puzzle_images (
	id INTEGER PRIMARY KEY AUTOINCREMENT, guild_id TEXT NOT NULL, name TEXT NOT NULL,
	data BLOB NOT NULL, added_by TEXT NOT NULL, added_at INTEGER NOT NULL);
CREATE INDEX idx_slide_puzzle_images_guild ON slide_puzzle_images(guild_id);
INSERT INTO slide_puzzles (guild_id, channel_id, grid, board, round_ends, updated_at) VALUES ('g', 'c', 2, '1,2,0,3', 1, 1);
INSERT INTO slide_puzzle_votes VALUES ('g', 1, 'u', 2, 1);
INSERT INTO slide_puzzle_images (guild_id, name, data, added_by, added_at) VALUES ('g', 'img', x'00', 'u', 1);
`

type schemaObject struct{ typ, name, sql string }

func schemaSnapshot(t *testing.T, conn *sql.DB) (map[string]schemaObject, map[string]int) {
	t.Helper()
	rows, err := conn.Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	require.NoError(t, err)
	objects := map[string]schemaObject{}
	for rows.Next() {
		var o schemaObject
		require.NoError(t, rows.Scan(&o.typ, &o.name, &o.sql))
		objects[o.name] = o
	}
	require.NoError(t, rows.Close())
	counts := map[string]int{}
	for name, o := range objects {
		if o.typ != "table" {
			continue
		}
		var n int
		require.NoError(t, conn.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %q`, name)).Scan(&n))
		counts[name] = n
	}
	return objects, counts
}

// Dropping the slide puzzle must remove exactly its three tables and index,
// leaving every other table, its schema and its rows untouched, including
// look-alike names.
func TestDropSlidePuzzleTablesTouchesNothingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	_, err = db.conn.Exec(oldSlidePuzzleSchema)
	require.NoError(t, err)
	_, err = db.conn.Exec(`CREATE TABLE slide_puzzles_archive (id INTEGER);
		INSERT INTO slide_puzzles_archive VALUES (1);
		CREATE TABLE puzzle_notes (id INTEGER);
		INSERT INTO puzzle_notes VALUES (1), (2)`)
	require.NoError(t, err)
	require.NoError(t, db.SetGuildConfigValue("g", "slide_puzzle_grid_size", "4", "u"))
	require.NoError(t, db.SetGuildConfigValue("g", "squads_enabled", "true", "u"))

	before, beforeCounts := schemaSnapshot(t, db.conn)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	after, afterCounts := schemaSnapshot(t, db.conn)

	retired := []string{"slide_puzzles", "slide_puzzle_votes", "slide_puzzle_images", "idx_slide_puzzle_images_guild"}
	for _, name := range retired {
		require.Contains(t, before, name)
		require.NotContains(t, after, name)
		delete(before, name)
		delete(beforeCounts, name)
	}
	require.Equal(t, before, after)
	require.Equal(t, beforeCounts, afterCounts)
	require.Equal(t, 1, afterCounts["slide_puzzles_archive"])
	require.Equal(t, 2, afterCounts["puzzle_notes"])

	cfg, err := db.AllGuildConfig("g")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"slide_puzzle_grid_size": "4", "squads_enabled": "true"}, cfg)

	require.NoError(t, db.dropSlidePuzzleTables(), "dropping again is a no-op")
}
