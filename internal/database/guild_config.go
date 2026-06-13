package database

import (
	"fmt"
)

// guild_config persists per-guild config overrides. Each row is one setting for
// one guild, keyed by (guild_id, key), with the value stored as text and parsed
// by the typed accessors in the config package. These methods implement the
// config.GuildStore interface (kept as an interface there to avoid an import
// cycle between config and database).

// AllGuildConfig returns every stored override for a guild as key -> value.
func (db *DB) AllGuildConfig(guildID string) (map[string]string, error) {
	rows, err := db.conn.Query(
		`SELECT key, value FROM guild_config WHERE guild_id = ?`,
		guildID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to read guild config for guild %s: %w", guildID, err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan guild config row: %w", err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate guild config rows: %w", err)
	}
	return out, nil
}

// SetGuildConfigValue upserts an override, recording the editor's user ID and
// bumping updated_at.
func (db *DB) SetGuildConfigValue(guildID, key, value, updatedBy string) error {
	_, err := db.conn.Exec(
		`INSERT INTO guild_config (guild_id, key, value, updated_by, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(guild_id, key) DO UPDATE SET
		     value = excluded.value,
		     updated_by = excluded.updated_by,
		     updated_at = CURRENT_TIMESTAMP`,
		guildID, key, value, updatedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to set guild config %q for guild %s: %w", key, guildID, err)
	}
	return nil
}

// DeleteGuildConfigValue removes an override, reverting that key to its
// env/default value. Deleting a missing row is a no-op.
func (db *DB) DeleteGuildConfigValue(guildID, key string) error {
	_, err := db.conn.Exec(
		`DELETE FROM guild_config WHERE guild_id = ? AND key = ?`,
		guildID, key,
	)
	if err != nil {
		return fmt.Errorf("failed to delete guild config %q for guild %s: %w", key, guildID, err)
	}
	return nil
}
