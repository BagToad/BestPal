package database

import (
	"fmt"
	"time"
)

// Event feed reminder bookkeeping. A row records that a reminder slot was
// handled for one occurrence of a scheduled event, so reminders are not
// re-posted after a restart. Keying on the start time re-arms reminders when an
// event is rescheduled or a recurring event moves to its next occurrence.

const eventFeedSchema = `
CREATE TABLE IF NOT EXISTS event_feed_reminders (
	guild_id   TEXT NOT NULL,
	event_id   TEXT NOT NULL,
	slot       TEXT NOT NULL,
	start_unix INTEGER NOT NULL,
	sent_at    INTEGER NOT NULL,
	PRIMARY KEY (guild_id, event_id, slot, start_unix)
);

CREATE INDEX IF NOT EXISTS idx_event_feed_reminders_start ON event_feed_reminders(start_unix);
`

// EventReminderSent reports whether a reminder slot was already handled for an
// event occurrence.
func (db *DB) EventReminderSent(guildID, eventID, slot string, start time.Time) (bool, error) {
	var n int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM event_feed_reminders WHERE guild_id = ? AND event_id = ? AND slot = ? AND start_unix = ?`,
		guildID, eventID, slot, start.Unix(),
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("failed to check event reminder: %w", err)
	}
	return n > 0, nil
}

// MarkEventReminderSent records a reminder slot as handled for an event
// occurrence. Marking an already-handled slot is a no-op.
func (db *DB) MarkEventReminderSent(guildID, eventID, slot string, start, now time.Time) error {
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO event_feed_reminders (guild_id, event_id, slot, start_unix, sent_at) VALUES (?, ?, ?, ?, ?)`,
		guildID, eventID, slot, start.Unix(), now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("failed to mark event reminder: %w", err)
	}
	return nil
}

// PruneEventReminders deletes reminder records for occurrences that started
// before the cutoff. Pruning by start time (not send time) keeps records for
// still-upcoming occurrences no matter how long the reminder lead is.
func (db *DB) PruneEventReminders(before time.Time) (int64, error) {
	res, err := db.conn.Exec(`DELETE FROM event_feed_reminders WHERE start_unix < ?`, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("failed to prune event reminders: %w", err)
	}
	return res.RowsAffected()
}
