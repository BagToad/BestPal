package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A slide_puzzles table created before needs_redraw/needs_refresh existed
// must be upgraded in place, keeping the running puzzle.
func TestSlidePuzzleMigratesOldSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	_, err = db.conn.Exec(`DROP TABLE slide_puzzles`)
	require.NoError(t, err)
	_, err = db.conn.Exec(`CREATE TABLE slide_puzzles (
		guild_id TEXT PRIMARY KEY, channel_id TEXT NOT NULL, message_id TEXT NOT NULL DEFAULT '',
		image_id INTEGER NOT NULL DEFAULT 0, grid INTEGER NOT NULL, board TEXT NOT NULL,
		round INTEGER NOT NULL DEFAULT 1, round_ends INTEGER NOT NULL,
		total_votes INTEGER NOT NULL DEFAULT 0, total_moves INTEGER NOT NULL DEFAULT 0,
		solved INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL)`)
	require.NoError(t, err)
	_, err = db.conn.Exec(`INSERT INTO slide_puzzles (guild_id, channel_id, message_id, grid, board, round, round_ends, total_moves, updated_at)
		VALUES ('g', 'c', 'm', 2, '1,2,0,3', 4, 100, 3, 100)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	p, ok, err := db.GetSlidePuzzle("g")
	require.NoError(t, err)
	require.True(t, ok)
	require.EqualValues(t, 4, p.Round)
	require.EqualValues(t, 3, p.TotalMoves)
	require.False(t, p.NeedsRedraw)

	_, err = db.StartSlidePuzzle(SlidePuzzle{GuildID: "g", ChannelID: "c", MessageID: "m2", Grid: 2, Board: []int{1, 2, 0, 3}, RoundEnds: time.Unix(200, 0)}, time.Unix(150, 0))
	require.NoError(t, err)

	require.NoError(t, db.SetSlidePuzzleMessage("g", "c2", "m3"))
	p, _, err = db.GetSlidePuzzle("g")
	require.NoError(t, err)
	require.Equal(t, "c2", p.ChannelID)
	require.Equal(t, "m3", p.MessageID)
}
