package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Squad persistence. Members pick games (squad_picks), wait in squad_queue,
// and the matcher moves them into squads (squads + squad_members). A member
// belongs to at most one squad per guild, which the squad_members primary key
// enforces. Picks outlive the queue so a rejoin can start from them.

const squadsSchema = `
CREATE TABLE IF NOT EXISTS squad_picks (
	guild_id TEXT NOT NULL,
	user_id  TEXT NOT NULL,
	game_id  INTEGER NOT NULL,
	name     TEXT NOT NULL,
	year     INTEGER NOT NULL DEFAULT 0,
	tags     TEXT NOT NULL DEFAULT '',
	added_at INTEGER NOT NULL,
	PRIMARY KEY (guild_id, user_id, game_id)
);

CREATE TABLE IF NOT EXISTS squad_queue (
	guild_id        TEXT NOT NULL,
	user_id         TEXT NOT NULL,
	queued_at       INTEGER NOT NULL,
	intro_thread_id TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (guild_id, user_id)
);

CREATE TABLE IF NOT EXISTS squad_preferences (
	guild_id      TEXT NOT NULL,
	user_id       TEXT NOT NULL,
	try_new_games INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (guild_id, user_id)
);

CREATE TABLE IF NOT EXISTS squads (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	guild_id           TEXT NOT NULL,
	name               TEXT NOT NULL,
	channel_id         TEXT NOT NULL DEFAULT '',
	role_id            TEXT NOT NULL DEFAULT '',
	welcome_message_id TEXT NOT NULL DEFAULT '',
	created_at         INTEGER NOT NULL,
	last_activity_at   INTEGER NOT NULL,
	warned_at          INTEGER NOT NULL DEFAULT 0,
	delete_reason      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_squads_guild ON squads(guild_id);

CREATE TABLE IF NOT EXISTS squad_members (
	guild_id  TEXT NOT NULL,
	user_id   TEXT NOT NULL,
	squad_id  INTEGER NOT NULL,
	joined_at INTEGER NOT NULL,
	PRIMARY KEY (guild_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_squad_members_squad ON squad_members(squad_id);

CREATE TABLE IF NOT EXISTS squad_membership_history (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	guild_id          TEXT NOT NULL,
	user_id           TEXT NOT NULL,
	squad_id          INTEGER NOT NULL,
	joined_at         INTEGER NOT NULL,
	first_message_at  INTEGER NOT NULL DEFAULT 0,
	last_reminder_at  INTEGER NOT NULL DEFAULT 0,
	left_at           INTEGER NOT NULL DEFAULT 0,
	leave_reason      TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_squad_history_active
	ON squad_membership_history(squad_id, user_id) WHERE left_at = 0;
CREATE INDEX IF NOT EXISTS idx_squad_history_reminders
	ON squad_membership_history(first_message_at, last_reminder_at, joined_at) WHERE left_at = 0;
CREATE INDEX IF NOT EXISTS idx_squad_history_user
	ON squad_membership_history(guild_id, user_id, joined_at);

CREATE TABLE IF NOT EXISTS squad_role_ops (
	guild_id  TEXT NOT NULL,
	user_id   TEXT NOT NULL,
	role_id   TEXT NOT NULL,
	add_role  INTEGER NOT NULL,
	failed_at INTEGER NOT NULL,
	PRIMARY KEY (guild_id, user_id, role_id)
);

CREATE TABLE IF NOT EXISTS squad_state (
	guild_id      TEXT PRIMARY KEY,
	last_match_at INTEGER NOT NULL DEFAULT 0
);
`

// ErrSquadMember is returned when adding someone who is already in a squad.
var ErrSquadMember = errors.New("user is already in a squad")

// SquadPick is one game a member wants to be matched on. Tags are the game's
// IGDB genre and theme names.
type SquadPick struct {
	GameID int64
	Name   string
	Year   int
	Tags   []string
}

// SquadQueueEntry is one member waiting for a squad.
type SquadQueueEntry struct {
	UserID        string
	QueuedAt      time.Time
	IntroThreadID string
}

const (
	SquadLeaveVoluntary = "voluntary"
	SquadLeaveAdmin     = "admin_removed"
	SquadLeaveServer    = "left_server"
	SquadLeaveDeleted   = "squad_deleted"
)

// SquadReminder is an inactive current member who is due a reminder.
type SquadReminder struct {
	HistoryID      int64
	GuildID        string
	UserID         string
	SquadID        int64
	SquadName      string
	ChannelID      string
	JoinedAt       time.Time
	LastReminderAt time.Time
}

type SquadUserHistory struct {
	Squads   int
	Ghosting int
}

type SquadGhoster struct {
	UserID   string
	Squads   int
	Ghosting int
}

// SquadTryNewGames reports whether a member opted into genre-based matching.
// Missing preferences default to false so existing members remain game-only.
func (db *DB) SquadTryNewGames(guildID, userID string) (bool, error) {
	var enabled bool
	err := db.conn.QueryRow(`SELECT try_new_games FROM squad_preferences WHERE guild_id = ? AND user_id = ?`,
		guildID, userID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// SetSquadTryNewGames updates a member's genre-matching preference.
func (db *DB) SetSquadTryNewGames(guildID, userID string, enabled bool) error {
	_, err := db.conn.Exec(`INSERT INTO squad_preferences (guild_id, user_id, try_new_games) VALUES (?, ?, ?)
		ON CONFLICT (guild_id, user_id) DO UPDATE SET try_new_games = excluded.try_new_games`,
		guildID, userID, enabled)
	return err
}

// Squad is one squad and its channel/role.
type Squad struct {
	ID               int64
	GuildID          string
	Name             string
	ChannelID        string
	RoleID           string
	WelcomeMessageID string
	CreatedAt        time.Time
	LastActivityAt   time.Time
	WarnedAt         time.Time // zero when no deletion warning is pending
	DeleteReason     string    // set once deletion has started; "" otherwise
	Members          int
}

// Deleting reports whether the squad is being deleted, so it must not take
// new members.
func (sq Squad) Deleting() bool { return sq.DeleteReason != "" }

const tagSep = "|"

// SquadPicks returns a member's picks in the order they were added.
func (db *DB) SquadPicks(guildID, userID string) ([]SquadPick, error) {
	rows, err := db.conn.Query(`SELECT game_id, name, year, tags FROM squad_picks
		WHERE guild_id = ? AND user_id = ? ORDER BY added_at, rowid`, guildID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquadPick
	for rows.Next() {
		p, err := scanPick(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllSquadPicks returns every member's picks in a guild.
func (db *DB) AllSquadPicks(guildID string) (map[string][]SquadPick, error) {
	rows, err := db.conn.Query(`SELECT user_id, game_id, name, year, tags FROM squad_picks
		WHERE guild_id = ? ORDER BY added_at, rowid`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]SquadPick{}
	for rows.Next() {
		var uid string
		var p SquadPick
		var tags string
		if err := rows.Scan(&uid, &p.GameID, &p.Name, &p.Year, &tags); err != nil {
			return nil, err
		}
		p.Tags = splitTags(tags)
		out[uid] = append(out[uid], p)
	}
	return out, rows.Err()
}

func scanPick(rows *sql.Rows) (SquadPick, error) {
	var p SquadPick
	var tags string
	if err := rows.Scan(&p.GameID, &p.Name, &p.Year, &tags); err != nil {
		return p, err
	}
	p.Tags = splitTags(tags)
	return p, nil
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, tagSep)
}

// AddSquadPick adds a game to a member's picks unless they already have it or
// already have limit picks. It returns the number of picks afterwards and
// whether the game was added.
func (db *DB) AddSquadPick(guildID, userID string, p SquadPick, limit int, now time.Time) (int, bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM squad_picks WHERE guild_id = ? AND user_id = ?`, guildID, userID).Scan(&n); err != nil {
		return 0, false, err
	}
	if n >= limit {
		return n, false, nil
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO squad_picks (guild_id, user_id, game_id, name, year, tags, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, guildID, userID, p.GameID, p.Name, p.Year, strings.Join(p.Tags, tagSep), now.UnixNano())
	if err != nil {
		return 0, false, err
	}
	added, _ := res.RowsAffected()
	if added > 0 {
		n++
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return n, added > 0, nil
}

// RemoveSquadPick removes one game from a member's picks.
func (db *DB) RemoveSquadPick(guildID, userID string, gameID int64) error {
	_, err := db.conn.Exec(`DELETE FROM squad_picks WHERE guild_id = ? AND user_id = ? AND game_id = ?`, guildID, userID, gameID)
	return err
}

// EnqueueSquadUser puts a member in the queue. Someone already queued keeps
// their place; a non-empty intro thread replaces the stored one.
func (db *DB) EnqueueSquadUser(guildID, userID, introThreadID string, now time.Time) error {
	_, err := db.conn.Exec(`INSERT INTO squad_queue (guild_id, user_id, queued_at, intro_thread_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (guild_id, user_id) DO UPDATE SET
		intro_thread_id = CASE WHEN excluded.intro_thread_id != '' THEN excluded.intro_thread_id ELSE intro_thread_id END`,
		guildID, userID, now.Unix(), introThreadID)
	return err
}

// DequeueSquadUser takes a member out of the queue, reporting whether they
// were in it.
func (db *DB) DequeueSquadUser(guildID, userID string) (bool, error) {
	res, err := db.conn.Exec(`DELETE FROM squad_queue WHERE guild_id = ? AND user_id = ?`, guildID, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SquadQueue returns everyone waiting in a guild, longest-waiting first.
func (db *DB) SquadQueue(guildID string) ([]SquadQueueEntry, error) {
	rows, err := db.conn.Query(`SELECT user_id, queued_at, intro_thread_id FROM squad_queue
		WHERE guild_id = ? ORDER BY queued_at, user_id`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquadQueueEntry
	for rows.Next() {
		var e SquadQueueEntry
		var at int64
		if err := rows.Scan(&e.UserID, &at, &e.IntroThreadID); err != nil {
			return nil, err
		}
		e.QueuedAt = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SquadQueueEntryFor returns a member's queue entry, if they're queued.
func (db *DB) SquadQueueEntryFor(guildID, userID string) (SquadQueueEntry, bool, error) {
	var e SquadQueueEntry
	var at int64
	err := db.conn.QueryRow(`SELECT user_id, queued_at, intro_thread_id FROM squad_queue WHERE guild_id = ? AND user_id = ?`,
		guildID, userID).Scan(&e.UserID, &at, &e.IntroThreadID)
	if errors.Is(err, sql.ErrNoRows) {
		return e, false, nil
	}
	if err != nil {
		return e, false, err
	}
	e.QueuedAt = time.Unix(at, 0)
	return e, true, nil
}

// CreateSquad records a new squad with its members and takes them out of the
// queue, all in one transaction. It fails with ErrSquadMember if any of them
// is already in a squad.
func (db *DB) CreateSquad(sq Squad, members []string, now time.Time) (Squad, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return sq, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO squads (guild_id, name, channel_id, role_id, created_at, last_activity_at)
		VALUES (?, ?, ?, ?, ?, ?)`, sq.GuildID, sq.Name, sq.ChannelID, sq.RoleID, now.Unix(), now.Unix())
	if err != nil {
		return sq, err
	}
	sq.ID, err = res.LastInsertId()
	if err != nil {
		return sq, err
	}
	for _, uid := range members {
		if err := addMemberTx(tx, sq.GuildID, uid, sq.ID, now); err != nil {
			return sq, err
		}
	}
	if err := tx.Commit(); err != nil {
		return sq, err
	}
	sq.CreatedAt = time.Unix(now.Unix(), 0)
	sq.LastActivityAt = sq.CreatedAt
	sq.Members = len(members)
	return sq, nil
}

func addMemberTx(tx *sql.Tx, guildID, userID string, squadID int64, now time.Time) error {
	res, err := tx.Exec(`INSERT OR IGNORE INTO squad_members (guild_id, user_id, squad_id, joined_at) VALUES (?, ?, ?, ?)`,
		guildID, userID, squadID, now.Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrSquadMember, userID)
	}
	if _, err := tx.Exec(`INSERT INTO squad_membership_history (guild_id, user_id, squad_id, joined_at)
		VALUES (?, ?, ?, ?)`, guildID, userID, squadID, now.Unix()); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM squad_queue WHERE guild_id = ? AND user_id = ?`, guildID, userID)
	return err
}

// AddSquadMember adds someone to an existing squad and takes them out of the
// queue. It fails with ErrSquadMember if they're already in a squad.
func (db *DB) AddSquadMember(guildID string, squadID int64, userID string, now time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := addMemberTx(tx, guildID, userID, squadID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveSquadMember takes someone out of their squad, closes their history
// entry, and returns the squad ID they were in.
func (db *DB) RemoveSquadMember(guildID, userID, reason string, now time.Time) (int64, bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRow(`DELETE FROM squad_members WHERE guild_id = ? AND user_id = ? RETURNING squad_id`, guildID, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(`UPDATE squad_membership_history SET left_at = ?, leave_reason = ?
		WHERE squad_id = ? AND user_id = ? AND left_at = 0`, now.Unix(), reason, id, userID); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// MarkSquadMemberMessaged records the first message a current member sends.
func (db *DB) MarkSquadMemberMessaged(squadID int64, userID string, now time.Time) error {
	_, err := db.conn.Exec(`UPDATE squad_membership_history SET first_message_at = ?
		WHERE squad_id = ? AND user_id = ? AND left_at = 0 AND first_message_at = 0`, now.Unix(), squadID, userID)
	return err
}

// DueSquadReminders returns current members who have never spoken and have not
// been reminded within interval.
func (db *DB) DueSquadReminders(guildID string, now time.Time, interval time.Duration) ([]SquadReminder, error) {
	cutoff := now.Add(-interval).Unix()
	rows, err := db.conn.Query(`SELECT h.id, h.guild_id, h.user_id, h.squad_id, s.name, s.channel_id, h.joined_at, h.last_reminder_at
		FROM squad_membership_history h
		JOIN squads s ON s.id = h.squad_id
		JOIN squad_members sm ON sm.squad_id = h.squad_id AND sm.user_id = h.user_id
		WHERE h.guild_id = ? AND h.left_at = 0 AND h.first_message_at = 0
			AND s.delete_reason = ''
			AND CASE
				WHEN h.last_reminder_at = 0 THEN h.joined_at
				WHEN h.last_reminder_at < 0 THEN -h.last_reminder_at
				ELSE h.last_reminder_at
			END <= ?
		ORDER BY h.joined_at, h.id`, guildID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquadReminder
	for rows.Next() {
		var r SquadReminder
		var joinedAt, lastReminderAt int64
		if err := rows.Scan(&r.HistoryID, &r.GuildID, &r.UserID, &r.SquadID, &r.SquadName, &r.ChannelID, &joinedAt, &lastReminderAt); err != nil {
			return nil, err
		}
		r.JoinedAt = time.Unix(joinedAt, 0)
		if lastReminderAt > 0 {
			r.LastReminderAt = time.Unix(lastReminderAt, 0)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkSquadReminder records an attempted reminder so closed DMs are retried
// daily instead of every scheduler tick.
func (db *DB) MarkSquadReminder(historyID int64, now time.Time) error {
	_, err := db.conn.Exec(`UPDATE squad_membership_history SET last_reminder_at = ?
		WHERE id = ? AND left_at = 0 AND first_message_at = 0`, now.Unix(), historyID)
	return err
}

// MarkSquadReminderReconcileFailed backs off a failed history scan without
// claiming that history was successfully checked through this timestamp.
func (db *DB) MarkSquadReminderReconcileFailed(historyID int64, now time.Time) error {
	_, err := db.conn.Exec(`UPDATE squad_membership_history SET last_reminder_at = ?
		WHERE id = ? AND left_at = 0 AND first_message_at = 0`, -now.Unix(), historyID)
	return err
}

func (db *DB) SquadUserHistoryStats(guildID, userID string) (SquadUserHistory, error) {
	var out SquadUserHistory
	err := db.conn.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE
		WHEN leave_reason = ? AND first_message_at = 0 THEN 1 ELSE 0 END), 0)
		FROM squad_membership_history WHERE guild_id = ? AND user_id = ?`,
		SquadLeaveVoluntary, guildID, userID).Scan(&out.Squads, &out.Ghosting)
	return out, err
}

func (db *DB) TopSquadGhosters(guildID string, limit int) ([]SquadGhoster, error) {
	rows, err := db.conn.Query(`SELECT user_id, COUNT(*), SUM(CASE
		WHEN leave_reason = ? AND first_message_at = 0 THEN 1 ELSE 0 END) AS ghosting
		FROM squad_membership_history WHERE guild_id = ?
		GROUP BY user_id HAVING ghosting > 0
		ORDER BY ghosting DESC, COUNT(*) DESC, user_id LIMIT ?`,
		SquadLeaveVoluntary, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquadGhoster
	for rows.Next() {
		var g SquadGhoster
		if err := rows.Scan(&g.UserID, &g.Squads, &g.Ghosting); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SquadMemberIDs returns a squad's members, earliest joiners first.
func (db *DB) SquadMemberIDs(squadID int64) ([]string, error) {
	rows, err := db.conn.Query(`SELECT user_id FROM squad_members WHERE squad_id = ? ORDER BY joined_at, user_id`, squadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

const squadCols = `s.id, s.guild_id, s.name, s.channel_id, s.role_id, s.welcome_message_id,
	s.created_at, s.last_activity_at, s.warned_at, s.delete_reason,
	(SELECT COUNT(*) FROM squad_members m WHERE m.squad_id = s.id)`

type rowScanner interface{ Scan(dest ...any) error }

func scanSquad(r rowScanner) (Squad, error) {
	var sq Squad
	var created, active, warned int64
	err := r.Scan(&sq.ID, &sq.GuildID, &sq.Name, &sq.ChannelID, &sq.RoleID, &sq.WelcomeMessageID,
		&created, &active, &warned, &sq.DeleteReason, &sq.Members)
	sq.CreatedAt = time.Unix(created, 0)
	sq.LastActivityAt = time.Unix(active, 0)
	if warned > 0 {
		sq.WarnedAt = time.Unix(warned, 0)
	}
	return sq, err
}

func (db *DB) oneSquad(where string, args ...any) (Squad, bool, error) {
	sq, err := scanSquad(db.conn.QueryRow(`SELECT `+squadCols+` FROM squads s WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Squad{}, false, nil
	}
	return sq, err == nil, err
}

// GetSquad returns a squad by ID.
func (db *DB) GetSquad(id int64) (Squad, bool, error) { return db.oneSquad(`s.id = ?`, id) }

// SquadByChannel returns the squad that owns a channel.
func (db *DB) SquadByChannel(channelID string) (Squad, bool, error) {
	return db.oneSquad(`s.channel_id = ? AND s.channel_id != ''`, channelID)
}

// SquadForUser returns the squad a member is in.
func (db *DB) SquadForUser(guildID, userID string) (Squad, bool, error) {
	return db.oneSquad(`s.id = (SELECT squad_id FROM squad_members WHERE guild_id = ? AND user_id = ?)`, guildID, userID)
}

// ListSquads returns a guild's squads, oldest first. An empty guild ID lists
// every guild's squads.
func (db *DB) ListSquads(guildID string) ([]Squad, error) {
	q := `SELECT ` + squadCols + ` FROM squads s`
	var args []any
	if guildID != "" {
		q += ` WHERE s.guild_id = ?`
		args = append(args, guildID)
	}
	rows, err := db.conn.Query(q+` ORDER BY s.created_at, s.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Squad
	for rows.Next() {
		sq, err := scanSquad(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sq)
	}
	return out, rows.Err()
}

// DeleteSquad removes a squad and its memberships while retaining closed
// membership history.
func (db *DB) DeleteSquad(id int64, now time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE squad_membership_history SET left_at = ?, leave_reason = ?
		WHERE squad_id = ? AND left_at = 0`, now.Unix(), SquadLeaveDeleted, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM squad_members WHERE squad_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM squad_role_ops WHERE role_id IN (SELECT role_id FROM squads WHERE id = ? AND role_id != '')`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM squads WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetSquadChannel records the channel and role created for a squad.
func (db *DB) SetSquadChannel(id int64, channelID, roleID string) error {
	_, err := db.conn.Exec(`UPDATE squads SET channel_id = ?, role_id = ? WHERE id = ?`, channelID, roleID, id)
	return err
}

// SetSquadWelcome records the pinned welcome message.
func (db *DB) SetSquadWelcome(id int64, messageID string) error {
	_, err := db.conn.Exec(`UPDATE squads SET welcome_message_id = ? WHERE id = ?`, messageID, id)
	return err
}

// TouchSquad records chat activity in a squad and cancels any pending
// deletion warning.
func (db *DB) TouchSquad(id int64, now time.Time) error {
	_, err := db.conn.Exec(`UPDATE squads SET last_activity_at = ?, warned_at = 0 WHERE id = ?`, now.Unix(), id)
	return err
}

// MarkSquadDeleting records that a squad's deletion has started, keeping the
// first reason if it was already marked.
func (db *DB) MarkSquadDeleting(id int64, reason string) error {
	_, err := db.conn.Exec(`UPDATE squads SET delete_reason = ? WHERE id = ? AND delete_reason = ''`, reason, id)
	return err
}

// migrateSquads adds columns introduced after the squads table was first
// created.
func (db *DB) migrateSquads() error {
	rows, err := db.conn.Query(`PRAGMA table_info(squads)`)
	if err != nil {
		return fmt.Errorf("failed to check squads schema: %w", err)
	}
	have := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt *string
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan squads schema: %w", err)
		}
		have = have || name == "delete_reason"
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to read squads schema: %w", err)
	}
	if !have {
		if _, err := db.conn.Exec(`ALTER TABLE squads ADD COLUMN delete_reason TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("failed to add squads.delete_reason: %w", err)
		}
	}
	// Repair open history left by rolling back to a version that predates
	// membership tracking. A changed join time means the old version removed
	// and re-added the member to the same squad.
	if _, err = db.conn.Exec(`UPDATE squad_membership_history
		SET left_at = CAST(strftime('%s', 'now') AS INTEGER), leave_reason = ?
		WHERE left_at = 0 AND NOT EXISTS (
			SELECT 1 FROM squad_members m
			WHERE m.squad_id = squad_membership_history.squad_id
				AND m.user_id = squad_membership_history.user_id
				AND m.joined_at = squad_membership_history.joined_at
		)`, SquadLeaveDeleted); err != nil {
		return err
	}
	// Existing members predate engagement tracking. Treat them as already
	// active so deploying reminders never nags established squads.
	_, err = db.conn.Exec(`INSERT OR IGNORE INTO squad_membership_history
		(guild_id, user_id, squad_id, joined_at, first_message_at)
		SELECT guild_id, user_id, squad_id, joined_at, joined_at FROM squad_members`)
	return err
}

// MarkSquadWarned records that a squad was warned it will be deleted.
func (db *DB) MarkSquadWarned(id int64, now time.Time) error {
	_, err := db.conn.Exec(`UPDATE squads SET warned_at = ? WHERE id = ?`, now.Unix(), id)
	return err
}

// SquadRoleOp is a squad role grant (Add) or revocation that failed and
// should be retried.
type SquadRoleOp struct {
	GuildID string
	UserID  string
	RoleID  string
	Add     bool
}

// SetSquadRoleOp records a role change to retry, replacing any earlier one
// for the same member and role.
func (db *DB) SetSquadRoleOp(op SquadRoleOp, now time.Time) error {
	_, err := db.conn.Exec(`INSERT INTO squad_role_ops (guild_id, user_id, role_id, add_role, failed_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (guild_id, user_id, role_id) DO UPDATE SET add_role = excluded.add_role, failed_at = excluded.failed_at`,
		op.GuildID, op.UserID, op.RoleID, op.Add, now.Unix())
	return err
}

// ClearSquadRoleOp forgets a pending role change.
func (db *DB) ClearSquadRoleOp(guildID, userID, roleID string) error {
	_, err := db.conn.Exec(`DELETE FROM squad_role_ops WHERE guild_id = ? AND user_id = ? AND role_id = ?`, guildID, userID, roleID)
	return err
}

// SquadRoleOps returns a guild's pending role changes, oldest first.
func (db *DB) SquadRoleOps(guildID string) ([]SquadRoleOp, error) {
	rows, err := db.conn.Query(`SELECT user_id, role_id, add_role FROM squad_role_ops WHERE guild_id = ? ORDER BY failed_at`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SquadRoleOp
	for rows.Next() {
		op := SquadRoleOp{GuildID: guildID}
		if err := rows.Scan(&op.UserID, &op.RoleID, &op.Add); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// SquadGuilds returns every guild with queued members, squads or pending
// role changes.
func (db *DB) SquadGuilds() ([]string, error) {
	rows, err := db.conn.Query(`SELECT guild_id FROM squad_queue UNION SELECT guild_id FROM squads UNION SELECT guild_id FROM squad_role_ops`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SquadLastMatch returns when the matcher last ran for a guild (zero if never).
func (db *DB) SquadLastMatch(guildID string) (time.Time, error) {
	var at int64
	err := db.conn.QueryRow(`SELECT last_match_at FROM squad_state WHERE guild_id = ?`, guildID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) || at == 0 {
		return time.Time{}, nil
	}
	return time.Unix(at, 0), err
}

// SetSquadLastMatch records when the matcher last ran for a guild.
func (db *DB) SetSquadLastMatch(guildID string, at time.Time) error {
	_, err := db.conn.Exec(`INSERT INTO squad_state (guild_id, last_match_at) VALUES (?, ?)
		ON CONFLICT (guild_id) DO UPDATE SET last_match_at = excluded.last_match_at`, guildID, at.Unix())
	return err
}
