package prune

import (
	"errors"
	"testing"
	"time"

	"gamerpal/internal/forumcache"
)

func TestRunIntroPrune(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		input          runIntroPruneInput
		failOnIDs      map[string]bool
		wantScanned    int
		wantFlagged    int
		wantDeleted    int
		wantFailures   int
		wantModSkipped int
		wantDeletedIDs []string
		wantKeptIDs    []string
		wantReasons    []string // expected reasons for flagged threads
	}{
		{
			name: "empty threads",
			input: runIntroPruneInput{
				Threads:       []*forumcache.ThreadMeta{},
				MemberPresent: map[string]bool{},
				ModeratorIDs:  map[string]struct{}{},
				ForumID:       "forum1",
			},
			wantScanned:    0,
			wantFlagged:    0,
			wantDeleted:    0,
			wantDeletedIDs: []string{},
		},
		{
			name: "departed owner",
			input: runIntroPruneInput{
				Threads: []*forumcache.ThreadMeta{
					{ID: "thread1", ForumID: "forum1", OwnerID: "departed_user", CreatedAt: now},
				},
				MemberPresent: map[string]bool{"departed_user": false},
				ModeratorIDs:  map[string]struct{}{},
				ForumID:       "forum1",
			},
			wantScanned:    1,
			wantFlagged:    1,
			wantDeleted:    1,
			wantDeletedIDs: []string{"thread1"},
			wantReasons:    []string{"owner departed"},
		},
		{
			name: "duplicate threads - keeps newest",
			input: runIntroPruneInput{
				Threads: []*forumcache.ThreadMeta{
					{ID: "thread1", ForumID: "forum1", OwnerID: "user1", CreatedAt: now.Add(-2 * time.Hour)}, // oldest
					{ID: "thread2", ForumID: "forum1", OwnerID: "user1", CreatedAt: now.Add(-1 * time.Hour)}, // middle
					{ID: "thread3", ForumID: "forum1", OwnerID: "user1", CreatedAt: now},                     // newest
				},
				MemberPresent: map[string]bool{"user1": true},
				ModeratorIDs:  map[string]struct{}{},
				ForumID:       "forum1",
			},
			wantScanned:    3,
			wantFlagged:    2,
			wantDeleted:    2,
			wantDeletedIDs: []string{"thread1", "thread2"},
			wantKeptIDs:    []string{"thread3"},
			wantReasons:    []string{"duplicate (older thread)"},
		},
		{
			name: "moderator threads skipped",
			input: runIntroPruneInput{
				Threads: []*forumcache.ThreadMeta{
					{ID: "mod_thread1", ForumID: "forum1", OwnerID: "mod_user", CreatedAt: now.Add(-1 * time.Hour)},
					{ID: "mod_thread2", ForumID: "forum1", OwnerID: "mod_user", CreatedAt: now},
				},
				MemberPresent: map[string]bool{"mod_user": true},
				ModeratorIDs:  map[string]struct{}{"mod_user": {}},
				ForumID:       "forum1",
			},
			wantScanned:    2,
			wantFlagged:    0,
			wantDeleted:    0,
			wantModSkipped: 2,
			wantDeletedIDs: []string{},
		},
		{
			name: "single thread not flagged",
			input: runIntroPruneInput{
				Threads: []*forumcache.ThreadMeta{
					{ID: "single_thread", ForumID: "forum1", OwnerID: "user1", CreatedAt: now},
				},
				MemberPresent: map[string]bool{"user1": true},
				ModeratorIDs:  map[string]struct{}{},
				ForumID:       "forum1",
			},
			wantScanned:    1,
			wantFlagged:    0,
			wantDeleted:    0,
			wantDeletedIDs: []string{},
		},
		{
			name: "delete failure tracked",
			input: runIntroPruneInput{
				Threads: []*forumcache.ThreadMeta{
					{ID: "fail_thread", ForumID: "forum1", OwnerID: "departed_user", CreatedAt: now},
				},
				MemberPresent: map[string]bool{"departed_user": false},
				ModeratorIDs:  map[string]struct{}{},
				ForumID:       "forum1",
			},
			failOnIDs:      map[string]bool{"fail_thread": true},
			wantScanned:    1,
			wantFlagged:    1,
			wantDeleted:    0,
			wantFailures:   1,
			wantDeletedIDs: []string{},
		},
		{
			name: "tie-break by ID when same timestamp",
			input: runIntroPruneInput{
				Threads: []*forumcache.ThreadMeta{
					{ID: "222", ForumID: "forum1", OwnerID: "user1", CreatedAt: now}, // higher ID = newer
					{ID: "111", ForumID: "forum1", OwnerID: "user1", CreatedAt: now}, // lower ID = older
				},
				MemberPresent: map[string]bool{"user1": true},
				ModeratorIDs:  map[string]struct{}{},
				ForumID:       "forum1",
			},
			wantScanned:    2,
			wantFlagged:    1,
			wantDeleted:    1,
			wantDeletedIDs: []string{"111"},
			wantKeptIDs:    []string{"222"},
		},
		{
			name: "mixed scenario",
			input: runIntroPruneInput{
				Threads: []*forumcache.ThreadMeta{
					// Departed user
					{ID: "departed1", ForumID: "forum1", OwnerID: "departed_user", CreatedAt: now},
					// Regular user with duplicates
					{ID: "dup1", ForumID: "forum1", OwnerID: "regular_user", CreatedAt: now.Add(-2 * time.Hour)},
					{ID: "dup2", ForumID: "forum1", OwnerID: "regular_user", CreatedAt: now.Add(-1 * time.Hour)},
					{ID: "dup3", ForumID: "forum1", OwnerID: "regular_user", CreatedAt: now}, // kept
					// Moderator with duplicates
					{ID: "mod1", ForumID: "forum1", OwnerID: "mod_user", CreatedAt: now.Add(-1 * time.Hour)},
					{ID: "mod2", ForumID: "forum1", OwnerID: "mod_user", CreatedAt: now},
					// Single thread user
					{ID: "single1", ForumID: "forum1", OwnerID: "single_user", CreatedAt: now},
				},
				MemberPresent: map[string]bool{
					"departed_user": false,
					"regular_user":  true,
					"mod_user":      true,
					"single_user":   true,
				},
				ModeratorIDs: map[string]struct{}{"mod_user": {}},
				ForumID:      "forum1",
			},
			wantScanned:    7,
			wantFlagged:    3, // 1 departed + 2 duplicates
			wantDeleted:    3,
			wantModSkipped: 2,
			wantDeletedIDs: []string{"departed1", "dup1", "dup2"},
			wantKeptIDs:    []string{"dup3", "mod1", "mod2", "single1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var deleted []string
			tt.input.DeleteThread = func(threadID string) error {
				if tt.failOnIDs[threadID] {
					return errors.New("delete failed")
				}
				deleted = append(deleted, threadID)
				return nil
			}

			result, err := runIntroPrune(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.ThreadsScanned != tt.wantScanned {
				t.Errorf("ThreadsScanned = %d, want %d", result.ThreadsScanned, tt.wantScanned)
			}
			if result.ThreadsFlagged != tt.wantFlagged {
				t.Errorf("ThreadsFlagged = %d, want %d", result.ThreadsFlagged, tt.wantFlagged)
			}
			if result.ThreadsDeleted != tt.wantDeleted {
				t.Errorf("ThreadsDeleted = %d, want %d", result.ThreadsDeleted, tt.wantDeleted)
			}
			if result.DeleteFailures != tt.wantFailures {
				t.Errorf("DeleteFailures = %d, want %d", result.DeleteFailures, tt.wantFailures)
			}
			if result.ModeratorSkipped != tt.wantModSkipped {
				t.Errorf("ModeratorSkipped = %d, want %d", result.ModeratorSkipped, tt.wantModSkipped)
			}

			// Check expected deletions
			deletedSet := make(map[string]bool)
			for _, id := range deleted {
				deletedSet[id] = true
			}
			for _, id := range tt.wantDeletedIDs {
				if !deletedSet[id] {
					t.Errorf("expected %s to be deleted", id)
				}
			}
			for _, id := range tt.wantKeptIDs {
				if deletedSet[id] {
					t.Errorf("expected %s to NOT be deleted", id)
				}
			}

			// Check expected reasons
			if len(tt.wantReasons) > 0 {
				for _, ft := range result.FlaggedThreads {
					found := false
					for _, wantReason := range tt.wantReasons {
						if ft.Reason == wantReason {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("unexpected reason %q, want one of %v", ft.Reason, tt.wantReasons)
					}
				}
			}
		})
	}
}
