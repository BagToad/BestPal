package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Trick-or-treat persistence. Bowls are keyed by their Discord message ID;
// users, souvenirs and ranks are per guild.

const trickOrTreatSchema = `
CREATE TABLE IF NOT EXISTS tot_users (
	guild_id        TEXT NOT NULL,
	user_id         TEXT NOT NULL,
	candies         INTEGER NOT NULL DEFAULT 0,
	bonus_candy     INTEGER NOT NULL DEFAULT 0,
	bonus_source    TEXT NOT NULL DEFAULT '',
	timeout_charges INTEGER NOT NULL DEFAULT 0,
	timeout_source  TEXT NOT NULL DEFAULT '',
	created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (guild_id, user_id)
);

CREATE TABLE IF NOT EXISTS tot_souvenirs (
	guild_id  TEXT NOT NULL,
	user_id   TEXT NOT NULL,
	item_type TEXT NOT NULL,
	quantity  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (guild_id, user_id, item_type)
);

CREATE TABLE IF NOT EXISTS tot_bowls (
	message_id        TEXT PRIMARY KEY,
	guild_id          TEXT NOT NULL,
	channel_id        TEXT NOT NULL,
	candies_remaining INTEGER NOT NULL DEFAULT 10,
	is_active         BOOLEAN NOT NULL DEFAULT 1,
	created_at        INTEGER NOT NULL,
	expires_at        INTEGER
);

CREATE INDEX IF NOT EXISTS idx_tot_bowls_active ON tot_bowls(is_active, guild_id, channel_id);

CREATE TABLE IF NOT EXISTS tot_participants (
	message_id  TEXT NOT NULL,
	user_id     TEXT NOT NULL,
	action_type TEXT NOT NULL,
	created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (message_id, user_id, action_type)
);

CREATE TABLE IF NOT EXISTS tot_bowl_log (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	message_id TEXT NOT NULL,
	at         INTEGER NOT NULL,
	text       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tot_bowl_log_message ON tot_bowl_log(message_id, id);
`

// BowlSize is how many candies a fresh bowl holds.
const BowlSize = 10

// Participant actions recorded per bowl. A user may do each once per bowl.
const (
	ActionTreat = "TREAT"
	ActionTrick = "TRICK"
	// ActionTimeout marks that a bowl already used up one of the user's
	// timeout charges, so one bowl can't burn through several.
	ActionTimeout = "TIMEOUT"
)

// Bowl is one candy bowl post.
type Bowl struct {
	MessageID string
	GuildID   string
	ChannelID string
	Remaining int
	Active    bool
	CreatedAt time.Time
	// ExpiresAt is set once the bowl is emptied: the end of the TRICK window.
	ExpiresAt time.Time
}

// BowlLogEntry is one line of a bowl's action log.
type BowlLogEntry struct {
	At   time.Time
	Text string
}

// TOTUser is a user's trick-or-treat state.
type TOTUser struct {
	UserID         string
	Candies        int64
	BonusCandy     int
	BonusSource    string
	TimeoutCharges int
	TimeoutSource  string
}

// Souvenir is a count of one souvenir type.
type Souvenir struct {
	Type     string
	Quantity int64
}

// CandyLeaderboardEntry is one row of the candy leaderboard.
type CandyLeaderboardEntry struct {
	UserID  string
	Candies int64
	Rank    int
}

// TreatStatus reports what a treat click did.
type TreatStatus int

const (
	// TreatClaimed means a candy was taken from the bowl.
	TreatClaimed TreatStatus = iota
	// TreatAlreadyClaimed means the user already took a treat from this bowl.
	TreatAlreadyClaimed
	// TreatTimedOut means a timeout charge blocked the claim and was used up.
	TreatTimedOut
	// TreatStillTimedOut means this bowl already used a timeout charge.
	TreatStillTimedOut
	// TreatBowlEmpty means the bowl has no candy left.
	TreatBowlEmpty
	// TreatBowlClosed means the bowl is archived or unknown.
	TreatBowlClosed
)

// TreatResult is the outcome of ClaimTreat.
type TreatResult struct {
	Status TreatStatus
	// Bowl is the bowl after the click.
	Bowl Bowl
	// Awarded is how many candies the user got, including Bonus.
	Awarded     int64
	Bonus       int
	BonusSource string
	// Candies is the user's total after the click.
	Candies        int64
	TimeoutCharges int
}

// TrickStatus reports what a trick click did.
type TrickStatus int

const (
	// TrickApplied means a trick outcome ran.
	TrickApplied TrickStatus = iota
	// TrickAlreadyDone means the user already tricked this bowl.
	TrickAlreadyDone
	// TrickBowlNotEmpty means the bowl still has candy.
	TrickBowlNotEmpty
	// TrickBowlClosed means the bowl is archived, unknown, or its window passed.
	TrickBowlClosed
)

// TrickTx is what a trick outcome may do, inside the trick's transaction.
type TrickTx interface {
	User(userID string) (TOTUser, error)
	// AddCandies changes a user's candies by delta, never below zero, and
	// returns the change actually applied.
	AddCandies(userID string, delta int64) (int64, error)
	AddSouvenir(userID, itemType string) error
	// GrantBonus keeps the larger of the current and new bonus.
	GrantBonus(userID string, bonus int, source string) error
	// GrantTimeout keeps the larger of the current and new timeout charges.
	GrantTimeout(userID string, charges int, source string) error
	// EmptyHandedParticipants lists users who clicked this bowl without
	// getting a treat from it, excluding exclude.
	EmptyHandedParticipants(exclude string) ([]string, error)
	// LowestEarner returns the user with the fewest candies, excluding
	// exclude, breaking ties randomly. ok is false when there is nobody.
	LowestEarner(exclude string) (userID string, ok bool, err error)
}

func unixOrZero(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.Unix(v.Int64, 0)
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

func getBowl(q queryer, messageID string) (Bowl, bool, error) {
	var b Bowl
	var created int64
	var expires sql.NullInt64
	err := q.QueryRow(
		`SELECT message_id, guild_id, channel_id, candies_remaining, is_active, created_at, expires_at
		 FROM tot_bowls WHERE message_id = ?`, messageID,
	).Scan(&b.MessageID, &b.GuildID, &b.ChannelID, &b.Remaining, &b.Active, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Bowl{}, false, nil
	}
	if err != nil {
		return Bowl{}, false, fmt.Errorf("failed to load bowl: %w", err)
	}
	b.CreatedAt = time.Unix(created, 0)
	b.ExpiresAt = unixOrZero(expires)
	return b, true, nil
}

func getTOTUser(q queryer, guildID, userID string) (TOTUser, error) {
	u := TOTUser{UserID: userID}
	err := q.QueryRow(
		`SELECT candies, bonus_candy, bonus_source, timeout_charges, timeout_source
		 FROM tot_users WHERE guild_id = ? AND user_id = ?`, guildID, userID,
	).Scan(&u.Candies, &u.BonusCandy, &u.BonusSource, &u.TimeoutCharges, &u.TimeoutSource)
	if errors.Is(err, sql.ErrNoRows) {
		return u, nil
	}
	if err != nil {
		return u, fmt.Errorf("failed to load trick-or-treat user: %w", err)
	}
	return u, nil
}

func ensureTOTUser(q queryer, guildID, userID string) error {
	if _, err := q.Exec(`INSERT OR IGNORE INTO tot_users (guild_id, user_id) VALUES (?, ?)`, guildID, userID); err != nil {
		return fmt.Errorf("failed to create trick-or-treat user: %w", err)
	}
	return nil
}

func hasParticipated(q queryer, messageID, userID, action string) (bool, error) {
	var n int
	if err := q.QueryRow(
		`SELECT COUNT(*) FROM tot_participants WHERE message_id = ? AND user_id = ? AND action_type = ?`,
		messageID, userID, action,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("failed to check participation: %w", err)
	}
	return n > 0, nil
}

func recordParticipation(q queryer, messageID, userID, action string) error {
	if _, err := q.Exec(
		`INSERT INTO tot_participants (message_id, user_id, action_type) VALUES (?, ?, ?)`,
		messageID, userID, action,
	); err != nil {
		return fmt.Errorf("failed to record participation: %w", err)
	}
	return nil
}

func appendBowlLog(q queryer, messageID string, at time.Time, text string) error {
	if text == "" {
		return nil
	}
	if _, err := q.Exec(`INSERT INTO tot_bowl_log (message_id, at, text) VALUES (?, ?, ?)`, messageID, at.Unix(), text); err != nil {
		return fmt.Errorf("failed to append bowl log: %w", err)
	}
	return nil
}

// CreateBowl records a freshly posted, full bowl.
func (db *DB) CreateBowl(guildID, channelID, messageID string, now time.Time) error {
	if _, err := db.conn.Exec(
		`INSERT INTO tot_bowls (message_id, guild_id, channel_id, candies_remaining, is_active, created_at)
		 VALUES (?, ?, ?, ?, 1, ?)`,
		messageID, guildID, channelID, BowlSize, now.Unix(),
	); err != nil {
		return fmt.Errorf("failed to create bowl: %w", err)
	}
	return nil
}

// GetBowl returns a bowl and its action log, oldest first. ok is false when
// the bowl is unknown.
func (db *DB) GetBowl(messageID string) (Bowl, []BowlLogEntry, bool, error) {
	b, ok, err := getBowl(db.conn, messageID)
	if err != nil || !ok {
		return b, nil, ok, err
	}
	rows, err := db.conn.Query(`SELECT at, text FROM tot_bowl_log WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return b, nil, true, fmt.Errorf("failed to load bowl log: %w", err)
	}
	defer rows.Close()
	var log []BowlLogEntry
	for rows.Next() {
		var at int64
		var e BowlLogEntry
		if err := rows.Scan(&at, &e.Text); err != nil {
			return b, nil, true, fmt.Errorf("failed to scan bowl log: %w", err)
		}
		e.At = time.Unix(at, 0)
		log = append(log, e)
	}
	return b, log, true, rows.Err()
}

// ActiveBowlChannels returns the channels in a guild that have an active bowl.
func (db *DB) ActiveBowlChannels(guildID string) (map[string]bool, error) {
	rows, err := db.conn.Query(`SELECT DISTINCT channel_id FROM tot_bowls WHERE guild_id = ? AND is_active = 1`, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active bowls: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, fmt.Errorf("failed to scan active bowl: %w", err)
		}
		out[ch] = true
	}
	return out, rows.Err()
}

// ExpiredBowls returns active bowls whose TRICK window ended by now.
func (db *DB) ExpiredBowls(now time.Time) ([]Bowl, error) {
	rows, err := db.conn.Query(
		`SELECT message_id FROM tot_bowls WHERE is_active = 1 AND expires_at IS NOT NULL AND expires_at <= ?`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("failed to list expired bowls: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan expired bowl: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Bowl
	for _, id := range ids {
		b, ok, err := getBowl(db.conn, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, b)
		}
	}
	return out, nil
}

// ArchiveBowl marks a bowl inactive.
func (db *DB) ArchiveBowl(messageID string) error {
	if _, err := db.conn.Exec(`UPDATE tot_bowls SET is_active = 0 WHERE message_id = ?`, messageID); err != nil {
		return fmt.Errorf("failed to archive bowl: %w", err)
	}
	return nil
}

// DrainBowl is the admin debug shortcut: it takes candies out of an active
// bowl until leave remain, writing one action log line per candy removed (as
// a real grab would). logLine gets the count left after each removal. When
// leave is 0 the TRICK window starts from now. Nothing is removed when the
// bowl already has leave or fewer candies. ok is false when there is no
// active bowl with that message ID.
func (db *DB) DrainBowl(messageID string, now time.Time, trickWindow time.Duration, leave int, logLine func(remaining int) string) (Bowl, bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Bowl{}, false, fmt.Errorf("failed to begin drain: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	b, ok, err := getBowl(tx, messageID)
	if err != nil {
		return Bowl{}, false, err
	}
	if !ok || !b.Active {
		return Bowl{}, false, nil
	}
	for b.Remaining > leave {
		b.Remaining--
		if err := appendBowlLog(tx, messageID, now, logLine(b.Remaining)); err != nil {
			return Bowl{}, false, err
		}
	}
	var expires any
	if b.Remaining == 0 {
		b.ExpiresAt = now.Add(trickWindow)
		expires = b.ExpiresAt.Unix()
	}
	if _, err := tx.Exec(
		`UPDATE tot_bowls SET candies_remaining = ?, expires_at = COALESCE(?, expires_at) WHERE message_id = ?`,
		b.Remaining, expires, messageID); err != nil {
		return Bowl{}, false, fmt.Errorf("failed to drain bowl: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Bowl{}, false, fmt.Errorf("failed to commit drain: %w", err)
	}
	return b, true, nil
}

// ResetBowl refills a bowl, reopens it, and clears who clicked it and its
// log. ok is false when the bowl is unknown.
func (db *DB) ResetBowl(messageID string) (Bowl, bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Bowl{}, false, fmt.Errorf("failed to begin reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`UPDATE tot_bowls SET candies_remaining = ?, is_active = 1, expires_at = NULL WHERE message_id = ?`,
		BowlSize, messageID)
	if err != nil {
		return Bowl{}, false, fmt.Errorf("failed to reset bowl: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Bowl{}, false, nil
	}
	if _, err := tx.Exec(`DELETE FROM tot_participants WHERE message_id = ?`, messageID); err != nil {
		return Bowl{}, false, fmt.Errorf("failed to clear participants: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM tot_bowl_log WHERE message_id = ?`, messageID); err != nil {
		return Bowl{}, false, fmt.Errorf("failed to clear bowl log: %w", err)
	}
	b, _, err := getBowl(tx, messageID)
	if err != nil {
		return Bowl{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Bowl{}, false, fmt.Errorf("failed to commit reset: %w", err)
	}
	return b, true, nil
}

// ClaimTreat handles a "Grab a Treat!" click atomically. trickWindow is how
// long the TRICK phase lasts once the bowl empties. logLine formats the action
// log entry for a successful claim; it is not called otherwise.
func (db *DB) ClaimTreat(messageID, userID string, now time.Time, trickWindow time.Duration, logLine func(TreatResult) string) (TreatResult, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return TreatResult{}, fmt.Errorf("failed to begin treat: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	b, ok, err := getBowl(tx, messageID)
	if err != nil {
		return TreatResult{}, err
	}
	if !ok || !b.Active {
		return TreatResult{Status: TreatBowlClosed, Bowl: b}, nil
	}
	res := TreatResult{Bowl: b}

	done, err := hasParticipated(tx, messageID, userID, ActionTreat)
	if err != nil {
		return res, err
	}
	if done {
		res.Status = TreatAlreadyClaimed
		return res, nil
	}
	if b.Remaining <= 0 {
		res.Status = TreatBowlEmpty
		return res, nil
	}

	u, err := getTOTUser(tx, b.GuildID, userID)
	if err != nil {
		return res, err
	}
	res.Candies = u.Candies
	res.TimeoutCharges = u.TimeoutCharges

	timedOutHere, err := hasParticipated(tx, messageID, userID, ActionTimeout)
	if err != nil {
		return res, err
	}
	if timedOutHere {
		res.Status = TreatStillTimedOut
		return res, nil
	}
	if u.TimeoutCharges > 0 {
		if err := ensureTOTUser(tx, b.GuildID, userID); err != nil {
			return res, err
		}
		if _, err := tx.Exec(
			`UPDATE tot_users SET timeout_charges = timeout_charges - 1,
			   timeout_source = CASE WHEN timeout_charges - 1 > 0 THEN timeout_source ELSE '' END
			 WHERE guild_id = ? AND user_id = ?`, b.GuildID, userID); err != nil {
			return res, fmt.Errorf("failed to use timeout charge: %w", err)
		}
		if err := recordParticipation(tx, messageID, userID, ActionTimeout); err != nil {
			return res, err
		}
		if err := tx.Commit(); err != nil {
			return res, fmt.Errorf("failed to commit timeout: %w", err)
		}
		res.Status = TreatTimedOut
		res.TimeoutCharges--
		return res, nil
	}

	res.Bonus = u.BonusCandy
	res.BonusSource = u.BonusSource
	res.Awarded = 1 + int64(u.BonusCandy)
	if err := ensureTOTUser(tx, b.GuildID, userID); err != nil {
		return res, err
	}
	if _, err := tx.Exec(
		`UPDATE tot_users SET candies = candies + ?, bonus_candy = 0, bonus_source = ''
		 WHERE guild_id = ? AND user_id = ?`, res.Awarded, b.GuildID, userID); err != nil {
		return res, fmt.Errorf("failed to award candy: %w", err)
	}
	res.Candies += res.Awarded

	res.Bowl.Remaining--
	var expires any
	if res.Bowl.Remaining == 0 {
		res.Bowl.ExpiresAt = time.Unix(now.Add(trickWindow).Unix(), 0)
		expires = res.Bowl.ExpiresAt.Unix()
	}
	if _, err := tx.Exec(
		`UPDATE tot_bowls SET candies_remaining = ?, expires_at = COALESCE(?, expires_at) WHERE message_id = ?`,
		res.Bowl.Remaining, expires, messageID); err != nil {
		return res, fmt.Errorf("failed to take candy from bowl: %w", err)
	}
	if err := recordParticipation(tx, messageID, userID, ActionTreat); err != nil {
		return res, err
	}
	res.Status = TreatClaimed
	if err := appendBowlLog(tx, messageID, now, logLine(res)); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("failed to commit treat: %w", err)
	}
	return res, nil
}

type trickTx struct {
	tx        *sql.Tx
	guildID   string
	messageID string
}

func (t *trickTx) User(userID string) (TOTUser, error) { return getTOTUser(t.tx, t.guildID, userID) }

func (t *trickTx) AddCandies(userID string, delta int64) (int64, error) {
	u, err := t.User(userID)
	if err != nil {
		return 0, err
	}
	if u.Candies+delta < 0 {
		delta = -u.Candies
	}
	if delta == 0 {
		return 0, nil
	}
	if err := ensureTOTUser(t.tx, t.guildID, userID); err != nil {
		return 0, err
	}
	if _, err := t.tx.Exec(`UPDATE tot_users SET candies = candies + ? WHERE guild_id = ? AND user_id = ?`,
		delta, t.guildID, userID); err != nil {
		return 0, fmt.Errorf("failed to change candies: %w", err)
	}
	return delta, nil
}

func (t *trickTx) AddSouvenir(userID, itemType string) error {
	if err := ensureTOTUser(t.tx, t.guildID, userID); err != nil {
		return err
	}
	if _, err := t.tx.Exec(
		`INSERT INTO tot_souvenirs (guild_id, user_id, item_type, quantity) VALUES (?, ?, ?, 1)
		 ON CONFLICT (guild_id, user_id, item_type) DO UPDATE SET quantity = quantity + 1`,
		t.guildID, userID, itemType); err != nil {
		return fmt.Errorf("failed to add souvenir: %w", err)
	}
	return nil
}

func (t *trickTx) GrantBonus(userID string, bonus int, source string) error {
	if err := ensureTOTUser(t.tx, t.guildID, userID); err != nil {
		return err
	}
	if _, err := t.tx.Exec(
		`UPDATE tot_users SET bonus_candy = ?, bonus_source = ?
		 WHERE guild_id = ? AND user_id = ? AND bonus_candy < ?`,
		bonus, source, t.guildID, userID, bonus); err != nil {
		return fmt.Errorf("failed to grant bonus: %w", err)
	}
	return nil
}

func (t *trickTx) GrantTimeout(userID string, charges int, source string) error {
	if err := ensureTOTUser(t.tx, t.guildID, userID); err != nil {
		return err
	}
	if _, err := t.tx.Exec(
		`UPDATE tot_users SET timeout_charges = ?, timeout_source = ?
		 WHERE guild_id = ? AND user_id = ? AND timeout_charges < ?`,
		charges, source, t.guildID, userID, charges); err != nil {
		return fmt.Errorf("failed to grant timeout: %w", err)
	}
	return nil
}

func (t *trickTx) EmptyHandedParticipants(exclude string) ([]string, error) {
	rows, err := t.tx.Query(
		`SELECT DISTINCT user_id FROM tot_participants
		 WHERE message_id = ? AND user_id != ?
		   AND user_id NOT IN (SELECT user_id FROM tot_participants WHERE message_id = ? AND action_type = ?)
		 ORDER BY user_id`,
		t.messageID, exclude, t.messageID, ActionTreat)
	if err != nil {
		return nil, fmt.Errorf("failed to list empty-handed participants: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan participant: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (t *trickTx) LowestEarner(exclude string) (string, bool, error) {
	var id string
	err := t.tx.QueryRow(
		`SELECT user_id FROM tot_users WHERE guild_id = ? AND user_id != ?
		 ORDER BY candies ASC, RANDOM() LIMIT 1`, t.guildID, exclude).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("failed to find lowest earner: %w", err)
	}
	return id, true, nil
}

// TrickResult is the outcome of ApplyTrick.
type TrickResult struct {
	Status TrickStatus
	Bowl   Bowl
}

// ApplyTrick handles a "TRICK!" click atomically: it checks the bowl and the
// user's eligibility, runs apply inside the transaction, records the click,
// and appends apply's log line to the bowl's action log.
func (db *DB) ApplyTrick(messageID, userID string, now time.Time, apply func(TrickTx) (string, error)) (TrickResult, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return TrickResult{}, fmt.Errorf("failed to begin trick: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	b, ok, err := getBowl(tx, messageID)
	if err != nil {
		return TrickResult{}, err
	}
	res := TrickResult{Bowl: b}
	if !ok || !b.Active || (!b.ExpiresAt.IsZero() && !now.Before(b.ExpiresAt)) {
		res.Status = TrickBowlClosed
		return res, nil
	}
	if b.Remaining > 0 {
		res.Status = TrickBowlNotEmpty
		return res, nil
	}
	done, err := hasParticipated(tx, messageID, userID, ActionTrick)
	if err != nil {
		return res, err
	}
	if done {
		res.Status = TrickAlreadyDone
		return res, nil
	}

	if err := ensureTOTUser(tx, b.GuildID, userID); err != nil {
		return res, err
	}
	line, err := apply(&trickTx{tx: tx, guildID: b.GuildID, messageID: messageID})
	if err != nil {
		return res, err
	}
	if err := recordParticipation(tx, messageID, userID, ActionTrick); err != nil {
		return res, err
	}
	if err := appendBowlLog(tx, messageID, now, line); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("failed to commit trick: %w", err)
	}
	res.Status = TrickApplied
	return res, nil
}

// AdjustCandies adds delta (which may be negative) to a user's candies,
// never going below zero. It returns the amount actually applied and the new
// balance.
func (db *DB) AdjustCandies(guildID, userID string, delta int64) (int64, int64, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin candy adjustment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t := &trickTx{tx: tx, guildID: guildID}
	applied, err := t.AddCandies(userID, delta)
	if err != nil {
		return 0, 0, err
	}
	u, err := t.User(userID)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("failed to commit candy adjustment: %w", err)
	}
	return applied, u.Candies, nil
}

// GetBucket returns a user's trick-or-treat state, souvenirs, and rank by
// candies (users tied on candies share a rank).
func (db *DB) GetBucket(guildID, userID string) (TOTUser, []Souvenir, int, error) {
	u, err := getTOTUser(db.conn, guildID, userID)
	if err != nil {
		return u, nil, 0, err
	}
	var ahead int
	if err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM tot_users WHERE guild_id = ? AND candies > ?`, guildID, u.Candies,
	).Scan(&ahead); err != nil {
		return u, nil, 0, fmt.Errorf("failed to rank user: %w", err)
	}
	rows, err := db.conn.Query(
		`SELECT item_type, quantity FROM tot_souvenirs WHERE guild_id = ? AND user_id = ? AND quantity > 0 ORDER BY item_type`,
		guildID, userID)
	if err != nil {
		return u, nil, 0, fmt.Errorf("failed to load souvenirs: %w", err)
	}
	defer rows.Close()
	var souvenirs []Souvenir
	for rows.Next() {
		var s Souvenir
		if err := rows.Scan(&s.Type, &s.Quantity); err != nil {
			return u, nil, 0, fmt.Errorf("failed to scan souvenir: %w", err)
		}
		souvenirs = append(souvenirs, s)
	}
	return u, souvenirs, ahead + 1, rows.Err()
}

// CandyLeaderboard returns the users with the most candies. Users tied on
// candies share a rank.
func (db *DB) CandyLeaderboard(guildID string, limit int) ([]CandyLeaderboardEntry, error) {
	rows, err := db.conn.Query(
		`SELECT user_id, candies FROM tot_users WHERE guild_id = ? AND candies > 0
		 ORDER BY candies DESC, created_at ASC, user_id ASC LIMIT ?`, guildID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to load candy leaderboard: %w", err)
	}
	defer rows.Close()
	var out []CandyLeaderboardEntry
	for rows.Next() {
		var e CandyLeaderboardEntry
		if err := rows.Scan(&e.UserID, &e.Candies); err != nil {
			return nil, fmt.Errorf("failed to scan candy leaderboard: %w", err)
		}
		e.Rank = len(out) + 1
		if len(out) > 0 && out[len(out)-1].Candies == e.Candies {
			e.Rank = out[len(out)-1].Rank
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
