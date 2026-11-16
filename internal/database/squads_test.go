package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSquadDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSquadPicksLimitAndDuplicates(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	n, added, err := db.AddSquadPick("g", "u", SquadPick{GameID: 1, Name: "A", Year: 2020, Tags: []string{"Shooter", "Sci-fi"}}, 2, now)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.True(t, added)
	n, added, err = db.AddSquadPick("g", "u", SquadPick{GameID: 1, Name: "A"}, 2, now)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.False(t, added)
	_, _, _ = db.AddSquadPick("g", "u", SquadPick{GameID: 2, Name: "B"}, 2, now.Add(time.Second))
	n, added, err = db.AddSquadPick("g", "u", SquadPick{GameID: 3, Name: "C"}, 2, now)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.False(t, added, "limit reached")

	picks, err := db.SquadPicks("g", "u")
	require.NoError(t, err)
	require.Len(t, picks, 2)
	assert.Equal(t, []string{"Shooter", "Sci-fi"}, picks[0].Tags)
	assert.Empty(t, picks[1].Tags)

	require.NoError(t, db.RemoveSquadPick("g", "u", 1))
	all, err := db.AllSquadPicks("g")
	require.NoError(t, err)
	require.Len(t, all["u"], 1)
	assert.Equal(t, int64(2), all["u"][0].GameID)
}

func TestEnqueueKeepsPlace(t *testing.T) {
	db := newSquadDB(t)
	require.NoError(t, db.EnqueueSquadUser("g", "u", "thread", time.Unix(100, 0)))
	require.NoError(t, db.EnqueueSquadUser("g", "u", "", time.Unix(500, 0)))
	e, ok, err := db.SquadQueueEntryFor("g", "u")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(100), e.QueuedAt.Unix())
	assert.Equal(t, "thread", e.IntroThreadID)

	removed, err := db.DequeueSquadUser("g", "u")
	require.NoError(t, err)
	assert.True(t, removed)
	removed, _ = db.DequeueSquadUser("g", "u")
	assert.False(t, removed)
}

func TestSquadTryNewGamesPreference(t *testing.T) {
	db := newSquadDB(t)
	enabled, err := db.SquadTryNewGames("g", "u")
	require.NoError(t, err)
	assert.False(t, enabled, "existing members default to game-only matching")

	require.NoError(t, db.SetSquadTryNewGames("g", "u", true))
	enabled, err = db.SquadTryNewGames("g", "u")
	require.NoError(t, err)
	assert.True(t, enabled)

	require.NoError(t, db.SetSquadTryNewGames("g", "u", false))
	enabled, err = db.SquadTryNewGames("g", "u")
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestCreateSquadMembership(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	for _, u := range []string{"a", "b", "c"} {
		require.NoError(t, db.EnqueueSquadUser("g", u, "", now))
	}
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a", "b"}, now)
	require.NoError(t, err)
	assert.Equal(t, 2, sq.Members)
	q, _ := db.SquadQueue("g")
	require.Len(t, q, 1)
	assert.Equal(t, "c", q[0].UserID)

	_, err = db.CreateSquad(Squad{GuildID: "g", Name: "Squad Badger"}, []string{"c", "a"}, now)
	require.ErrorIs(t, err, ErrSquadMember)
	squads, _ := db.ListSquads("g")
	assert.Len(t, squads, 1, "failed create is rolled back")
	q, _ = db.SquadQueue("g")
	assert.Len(t, q, 1)

	require.ErrorIs(t, db.AddSquadMember("g", sq.ID, "a", now), ErrSquadMember)
	require.NoError(t, db.AddSquadMember("g", sq.ID, "c", now))
	ids, _ := db.SquadMemberIDs(sq.ID)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, ids)

	require.NoError(t, db.SetSquadChannel(sq.ID, "ch", "role"))
	got, ok, err := db.SquadByChannel("ch")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 3, got.Members)

	id, ok, err := db.RemoveSquadMember("g", "b", SquadLeaveVoluntary, now)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, sq.ID, id)

	require.NoError(t, db.MarkSquadWarned(sq.ID, now))
	require.NoError(t, db.TouchSquad(sq.ID, now.Add(time.Hour)))
	got, _, _ = db.GetSquad(sq.ID)
	assert.True(t, got.WarnedAt.IsZero(), "activity clears the warning")

	require.NoError(t, db.DeleteSquad(sq.ID, now))
	_, ok, _ = db.SquadForUser("g", "a")
	assert.False(t, ok)
}

func TestSquadInactivityRemindersStopAfterFirstMessage(t *testing.T) {
	db := newSquadDB(t)
	joined := time.Unix(1000, 0)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl", ChannelID: "ch"}, []string{"a"}, joined)
	require.NoError(t, err)

	due, err := db.DueSquadReminders("g", joined.Add(24*time.Hour-time.Second), 24*time.Hour)
	require.NoError(t, err)
	assert.Empty(t, due)
	due, err = db.DueSquadReminders("g", joined.Add(24*time.Hour), 24*time.Hour)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "a", due[0].UserID)
	assert.Equal(t, "ch", due[0].ChannelID)

	require.NoError(t, db.MarkSquadReminder(due[0].HistoryID, joined.Add(24*time.Hour)))
	due, err = db.DueSquadReminders("g", joined.Add(25*time.Hour), 24*time.Hour)
	require.NoError(t, err)
	assert.Empty(t, due)
	due, err = db.DueSquadReminders("g", joined.Add(48*time.Hour), 24*time.Hour)
	require.NoError(t, err)
	require.Len(t, due, 1)

	require.NoError(t, db.MarkSquadMemberMessaged(sq.ID, "a", joined.Add(49*time.Hour)))
	due, err = db.DueSquadReminders("g", joined.Add(96*time.Hour), 24*time.Hour)
	require.NoError(t, err)
	assert.Empty(t, due)
}

func TestSquadGhostingStatsCountOnlyVoluntarySilentLeaves(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	for n, reason := range []string{SquadLeaveVoluntary, SquadLeaveAdmin, SquadLeaveVoluntary} {
		sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad"}, []string{"u"}, now.Add(time.Duration(n)*time.Hour))
		require.NoError(t, err)
		if n == 2 {
			require.NoError(t, db.MarkSquadMemberMessaged(sq.ID, "u", now.Add(3*time.Hour)))
		}
		_, ok, err := db.RemoveSquadMember("g", "u", reason, now.Add(time.Duration(n+1)*time.Hour))
		require.NoError(t, err)
		require.True(t, ok)
	}

	stats, err := db.SquadUserHistoryStats("g", "u")
	require.NoError(t, err)
	assert.Equal(t, SquadUserHistory{Squads: 3, Ghosting: 1}, stats)
	top, err := db.TopSquadGhosters("g", 10)
	require.NoError(t, err)
	require.Len(t, top, 1)
	assert.Equal(t, SquadGhoster{UserID: "u", Squads: 3, Ghosting: 1}, top[0])
}

func TestPreviousSquadPartnersUsesOnlyLatestCompletedSquad(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	first, err := db.CreateSquad(Squad{GuildID: "g", Name: "First"}, []string{"a", "b"}, now)
	require.NoError(t, err)
	require.NoError(t, db.DeleteSquad(first.ID, now.Add(time.Hour)))
	second, err := db.CreateSquad(Squad{GuildID: "g", Name: "Second"}, []string{"a", "c"}, now.Add(2*time.Hour))
	require.NoError(t, err)
	require.NoError(t, db.DeleteSquad(second.ID, now.Add(3*time.Hour)))

	partners, err := db.PreviousSquadPartners("g", "a")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"c": true}, partners)
}

func TestPreviousSquadPartnersUsesLatestActiveSquadAndOverlap(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	first, err := db.CreateSquad(Squad{GuildID: "g", Name: "First"}, []string{"a", "b"}, now)
	require.NoError(t, err)
	_, removed, err := db.RemoveSquadMember("g", "a", SquadLeaveVoluntary, now.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, removed)
	require.NoError(t, db.AddSquadMember("g", first.ID, "d", now.Add(2*time.Hour)))

	partners, err := db.PreviousSquadPartners("g", "b")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"a": true, "d": true}, partners, "an active squad is the remaining member's latest squad")
	partners, err = db.PreviousSquadPartners("g", "d")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"b": true}, partners, "members whose time did not overlap are not previous partners")
}

func TestPreviousSquadPartnersTreatsSameSecondAsOverlap(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	_, err := db.CreateSquad(Squad{GuildID: "g", Name: "First"}, []string{"a", "b"}, now)
	require.NoError(t, err)
	_, removed, err := db.RemoveSquadMember("g", "a", SquadLeaveVoluntary, now.Add(500*time.Millisecond))
	require.NoError(t, err)
	require.True(t, removed)

	partners, err := db.PreviousSquadPartners("g", "b")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"a": true}, partners)
}

func TestSquadLastMatch(t *testing.T) {
	db := newSquadDB(t)
	at, err := db.SquadLastMatch("g")
	require.NoError(t, err)
	assert.True(t, at.IsZero())
	require.NoError(t, db.SetSquadLastMatch("g", time.Unix(42, 0)))
	at, _ = db.SquadLastMatch("g")
	assert.Equal(t, int64(42), at.Unix())
}

func TestMarkSquadDeleting(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1_800_000_000, 0)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl"}, []string{"a", "b"}, now)
	require.NoError(t, err)
	require.NoError(t, db.MarkSquadDeleting(sq.ID, "quiet"))
	require.NoError(t, db.MarkSquadDeleting(sq.ID, "later reason"))
	got, _, err := db.GetSquad(sq.ID)
	require.NoError(t, err)
	assert.True(t, got.Deleting())
	assert.Equal(t, "quiet", got.DeleteReason, "the first reason is kept")
}

func TestMigrateSquadsAddsDeleteReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	_, err = db.conn.Exec(`ALTER TABLE squads DROP COLUMN delete_reason`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl"}, []string{"a"}, time.Now())
	require.NoError(t, err)
	squads, err := db.ListSquads("g")
	require.NoError(t, err)
	require.Len(t, squads, 1)
	assert.False(t, squads[0].Deleting())
}

func TestMigrateSquadsTreatsExistingMembersAsActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	joined := time.Unix(1000, 0)
	_, err = db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl", ChannelID: "ch"}, []string{"a"}, joined)
	require.NoError(t, err)
	_, err = db.conn.Exec(`DELETE FROM squad_membership_history`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()
	reminders, err := db.DueSquadReminders("g", joined.Add(30*24*time.Hour), 24*time.Hour)
	require.NoError(t, err)
	assert.Empty(t, reminders, "deploying engagement tracking must not nag established squads")
}

func TestMigrateSquadsClosesHistoryForMembersRemovedDuringRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	joined := time.Unix(1000, 0)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl", ChannelID: "ch"}, []string{"a", "b"}, joined)
	require.NoError(t, err)
	_, err = db.conn.Exec(`DELETE FROM squad_members WHERE guild_id = 'g' AND user_id = 'a'`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()
	reminders, err := db.DueSquadReminders("g", joined.Add(30*24*time.Hour), 24*time.Hour)
	require.NoError(t, err)
	require.Len(t, reminders, 1)
	assert.Equal(t, "b", reminders[0].UserID)
	var leftAt int64
	var reason string
	require.NoError(t, db.conn.QueryRow(`SELECT left_at, leave_reason FROM squad_membership_history
		WHERE squad_id = ? AND user_id = 'a'`, sq.ID).Scan(&leftAt, &reason))
	assert.NotZero(t, leftAt)
	assert.Equal(t, SquadLeaveDeleted, reason)
}

func TestMigrateSquadsSeparatesRollbackRejoinToSameSquad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback-rejoin.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	joined := time.Unix(1000, 0)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Owl"}, []string{"a"}, joined)
	require.NoError(t, err)
	_, err = db.conn.Exec(`UPDATE squad_members SET joined_at = ? WHERE squad_id = ? AND user_id = 'a'`, joined.Add(time.Hour).Unix(), sq.ID)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()
	var active, total int
	require.NoError(t, db.conn.QueryRow(`SELECT SUM(left_at = 0), COUNT(*) FROM squad_membership_history
		WHERE squad_id = ? AND user_id = 'a'`, sq.ID).Scan(&active, &total))
	assert.Equal(t, 1, active)
	assert.Equal(t, 2, total)
}

func TestSquadDeletionTranscriptPersistsUntilDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a", "b"}, time.Unix(100, 0))
	require.NoError(t, err)
	want := SquadDeletionTranscript{Name: "transcript.txt", ContentType: "text/plain", Data: []byte("hello")}
	require.NoError(t, db.SaveSquadDeletionTranscript(sq.ID, want))

	var legacyBytes []byte
	var storedFile, checksum string
	require.NoError(t, db.conn.QueryRow(`SELECT deletion_transcript, deletion_transcript_file,
		deletion_transcript_sha256 FROM squads WHERE id = ?`, sq.ID).Scan(&legacyBytes, &storedFile, &checksum))
	assert.Empty(t, legacyBytes, "transcript payloads are not stored in SQLite")
	assert.NotEmpty(t, storedFile)
	assert.Len(t, checksum, 64)
	storedPath := filepath.Join(db.transcriptDir, storedFile)
	stored, err := os.ReadFile(storedPath)
	require.NoError(t, err)
	assert.Equal(t, want.Data, stored)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()

	got, err := db.SquadDeletionTranscript(sq.ID)
	require.NoError(t, err)
	assert.Equal(t, want.Name, got.Name)
	assert.Equal(t, want.ContentType, got.ContentType)
	assert.Equal(t, want.Data, got.Data)
	assert.False(t, got.LogSent)

	require.NoError(t, db.MarkSquadDeletionLogSent(sq.ID))
	got, err = db.SquadDeletionTranscript(sq.ID)
	require.NoError(t, err)
	assert.True(t, got.LogSent)
	require.NoError(t, db.DeleteSquad(sq.ID, time.Unix(200, 0)))
	_, err = os.Stat(storedPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMigrateSquadTranscriptBlobToDurableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a"}, time.Unix(100, 0))
	require.NoError(t, err)
	_, err = db.conn.Exec(`UPDATE squads SET deletion_transcript_name = 'legacy.txt',
		deletion_transcript_type = 'text/plain', deletion_transcript = ? WHERE id = ?`, []byte("legacy payload"), sq.ID)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()
	got, err := db.SquadDeletionTranscript(sq.ID)
	require.NoError(t, err)
	assert.Equal(t, "legacy.txt", got.Name)
	assert.Equal(t, []byte("legacy payload"), got.Data)
	var legacyBytes []byte
	require.NoError(t, db.conn.QueryRow(`SELECT deletion_transcript FROM squads WHERE id = ?`, sq.ID).Scan(&legacyBytes))
	assert.Empty(t, legacyBytes)
}

func TestMigrateDeliveredSquadTranscriptPreservesDeliveryState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-delivered.db")
	db, err := NewDB(path)
	require.NoError(t, err)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a"}, time.Unix(100, 0))
	require.NoError(t, err)
	_, err = db.conn.Exec(`UPDATE squads SET deletion_transcript_name = 'legacy.txt',
		deletion_transcript_type = 'text/plain', deletion_transcript = ?, deletion_log_sent = 1
		WHERE id = ?`, []byte("legacy payload"), sq.ID)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = NewDB(path)
	require.NoError(t, err)
	defer db.Close()
	got, err := db.SquadDeletionTranscript(sq.ID)
	require.NoError(t, err)
	assert.True(t, got.LogSent)
	var fileLogSent bool
	require.NoError(t, db.conn.QueryRow(`SELECT deletion_file_log_sent FROM squads WHERE id = ?`, sq.ID).Scan(&fileLogSent))
	assert.True(t, fileLogSent)
}

func TestPreviousBinaryCannotReplaceOrDeletePendingFileTranscript(t *testing.T) {
	db := newSquadDB(t)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a"}, time.Unix(100, 0))
	require.NoError(t, err)
	want := SquadDeletionTranscript{Name: "transcript.txt", ContentType: "text/plain", Data: []byte("real transcript")}
	require.NoError(t, db.SaveSquadDeletionTranscript(sq.ID, want))

	_, err = db.conn.Exec(`UPDATE squads SET deletion_transcript_name = 'unavailable',
		deletion_transcript_type = 'application/x-bestpal-transcript-unavailable',
		deletion_transcript = 'channel deleted' WHERE id = ?`, sq.ID)
	assert.ErrorContains(t, err, "pending file transcript requires a newer BestPal version")
	_, err = db.conn.Exec(`UPDATE squads SET deletion_log_sent = 1 WHERE id = ?`, sq.ID)
	require.NoError(t, err, "the previous binary may update its legacy flag")
	_, err = db.conn.Exec(`DELETE FROM squads WHERE id = ?`, sq.ID)
	assert.ErrorContains(t, err, "pending file transcript requires a newer BestPal version")

	got, err := db.SquadDeletionTranscript(sq.ID)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	require.NoError(t, db.MarkSquadDeletionLogSent(sq.ID))
	require.NoError(t, db.DeleteSquad(sq.ID, time.Unix(200, 0)))
}

func TestSquadTranscriptFileSurvivesDatabaseDeletionFailure(t *testing.T) {
	db := newSquadDB(t)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a"}, time.Unix(100, 0))
	require.NoError(t, err)
	require.NoError(t, db.SaveSquadDeletionTranscript(sq.ID, SquadDeletionTranscript{
		Name: "transcript.txt", ContentType: "text/plain", Data: []byte("must survive"),
	}))
	require.NoError(t, db.MarkSquadDeletionLogSent(sq.ID))
	var storedFile string
	require.NoError(t, db.conn.QueryRow(`SELECT deletion_transcript_file FROM squads WHERE id = ?`, sq.ID).Scan(&storedFile))
	_, err = db.conn.Exec(`CREATE TRIGGER fail_squad_delete BEFORE DELETE ON squads
		BEGIN SELECT RAISE(ABORT, 'simulated delete failure'); END`)
	require.NoError(t, err)

	err = db.DeleteSquad(sq.ID, time.Unix(200, 0))
	assert.ErrorContains(t, err, "simulated delete failure")
	stored, readErr := os.ReadFile(filepath.Join(db.transcriptDir, storedFile))
	require.NoError(t, readErr)
	assert.Equal(t, []byte("must survive"), stored)
}

func TestUnavailableSquadTranscriptUsesMetadataOnly(t *testing.T) {
	db := newSquadDB(t)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a"}, time.Unix(100, 0))
	require.NoError(t, err)
	want := SquadDeletionTranscript{
		Name: "unavailable", ContentType: "application/x-bestpal-transcript-unavailable", Data: []byte("too many messages"),
	}
	require.NoError(t, db.SaveSquadDeletionTranscript(sq.ID, want))
	got, err := db.SquadDeletionTranscript(sq.ID)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	entries, err := os.ReadDir(db.transcriptDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestSquadTranscriptRejectsCorruptFile(t *testing.T) {
	db := newSquadDB(t)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a"}, time.Unix(100, 0))
	require.NoError(t, err)
	require.NoError(t, db.SaveSquadDeletionTranscript(sq.ID, SquadDeletionTranscript{
		Name: "transcript.txt", ContentType: "text/plain", Data: []byte("original"),
	}))
	var storedFile string
	require.NoError(t, db.conn.QueryRow(`SELECT deletion_transcript_file FROM squads WHERE id = ?`, sq.ID).Scan(&storedFile))
	require.NoError(t, os.WriteFile(filepath.Join(db.transcriptDir, storedFile), []byte("tampered"), 0o600))

	_, err = db.SquadDeletionTranscript(sq.ID)
	assert.ErrorContains(t, err, "checksum mismatch")
}

func TestSquadTranscriptSaveFailureLeavesNoFile(t *testing.T) {
	db := newSquadDB(t)
	err := db.SaveSquadDeletionTranscript(999, SquadDeletionTranscript{
		Name: "transcript.txt", ContentType: "text/plain", Data: []byte("orphan"),
	})
	assert.ErrorIs(t, err, sql.ErrNoRows)
	entries, readErr := os.ReadDir(db.transcriptDir)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestSquadTranscriptCleanupPreservesReferencedFiles(t *testing.T) {
	db := newSquadDB(t)
	sq, err := db.CreateSquad(Squad{GuildID: "g", Name: "Squad Axolotl"}, []string{"a"}, time.Unix(100, 0))
	require.NoError(t, err)
	require.NoError(t, db.SaveSquadDeletionTranscript(sq.ID, SquadDeletionTranscript{
		Name: "transcript.txt", ContentType: "text/plain", Data: []byte("referenced"),
	}))
	orphan := filepath.Join(db.transcriptDir, "orphan.transcript")
	require.NoError(t, os.WriteFile(orphan, []byte("orphan"), 0o600))
	require.NoError(t, db.cleanupOrphanedSquadTranscripts(0))
	_, err = os.Stat(orphan)
	assert.ErrorIs(t, err, os.ErrNotExist)
	got, err := db.SquadDeletionTranscript(sq.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("referenced"), got.Data)
}
