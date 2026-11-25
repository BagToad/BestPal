package database

import "fmt"

// retiredSlidePuzzleObjects are the only schema objects the removed slide
// puzzle owned. They are named one by one so nothing else can match.
var retiredSlidePuzzleObjects = []string{
	`DROP INDEX IF EXISTS idx_slide_puzzle_images_guild`,
	`DROP TABLE IF EXISTS slide_puzzle_votes`,
	`DROP TABLE IF EXISTS slide_puzzle_images`,
	`DROP TABLE IF EXISTS slide_puzzles`,
}

// dropSlidePuzzleTables removes the retired slide puzzle's tables. It is a
// no-op once they are gone.
func (db *DB) dropSlidePuzzleTables() error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to drop slide puzzle tables: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range retiredSlidePuzzleObjects {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to drop slide puzzle tables: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to drop slide puzzle tables: %w", err)
	}
	return nil
}
