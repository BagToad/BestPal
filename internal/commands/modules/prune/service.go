package prune

import (
	"fmt"
	"sort"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/forumcache"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// Rate limiting delays for prune operations
const (
	// ownerCheckDelay is the delay between per-owner membership/permission checks
	ownerCheckDelay = 15 * time.Millisecond
	// threadDeleteDelay is the delay between thread deletions in execute mode
	threadDeleteDelay = 150 * time.Millisecond
)

// IntroPruneResult contains the results of an intro prune operation
type IntroPruneResult struct {
	ThreadsScanned   int
	ThreadsFlagged   int
	ThreadsDeleted   int
	DeleteFailures   int
	ModeratorSkipped int
	FlaggedThreads   []FlaggedThread
}

// FlaggedThread represents a thread flagged for pruning
type FlaggedThread struct {
	ThreadID  string
	Reason    string
	OwnerID   string
	Username  string
	CreatedAt time.Time
}

// runIntroPruneInput contains all inputs for the testable prune logic
type runIntroPruneInput struct {
	Threads        []*forumcache.ThreadMeta
	MemberPresent  map[string]bool
	ModeratorIDs   map[string]struct{}
	OwnerUsernames map[string]string // ownerID -> username
	DeleteThread   func(string) error
	ForumID        string
	Cfg            *config.Config
	DryRun         bool
}

// Service handles scheduled intro prune operations
type Service struct {
	types.BaseService
	cfg        *config.Config
	forumCache *forumcache.Service
}

// NewService creates a new prune service
func NewService(cfg *config.Config, forumCache *forumcache.Service) *Service {
	return &Service{
		cfg:        cfg,
		forumCache: forumCache,
	}
}

// ScheduledFuncs returns functions to be called on a schedule
func (s *Service) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{
		"@every 24h": s.RunScheduledIntroPrune,
	}
}

// RunScheduledIntroPrune runs the consolidated intro prune and logs results
func (s *Service) RunScheduledIntroPrune() error {
	if s.Session == nil {
		return fmt.Errorf("session not initialized")
	}

	forumID := s.cfg.GetGamerPalsIntroductionsForumChannelID()
	guildID := s.cfg.GetGamerPalsServerID()

	if forumID == "" || guildID == "" {
		s.cfg.Logger.Warn("[IntroPrune] Skipping scheduled run: forum or guild ID not configured")
		return nil
	}

	s.cfg.Logger.Info("[IntroPrune] Starting scheduled intro prune (dry run)...")

	result, err := RunIntroPrune(s.Session, s.cfg, s.forumCache, forumID, guildID, true)
	if err != nil {
		s.cfg.Logger.Errorf("[IntroPrune] Scheduled prune failed: %v", err)
		logMsg := fmt.Sprintf("[Scheduled Intro Prune Failed]\nError: %v", err)
		if logErr := utils.LogToChannel(s.cfg, s.Session, logMsg); logErr != nil {
			s.cfg.Logger.Errorf("[IntroPrune] Failed to log error to channel: %v", logErr)
		}
		return err
	}

	// Log results to bestpal log channel
	logMsg := fmt.Sprintf("[Scheduled Intro Prune - DRY RUN]\nForum: <#%s>\nThreads Scanned: %d\nThreads Flagged: %d\nThreads Deleted: %d\nDelete Failures: %d\nModerator Threads Skipped: %d",
		forumID,
		result.ThreadsScanned,
		result.ThreadsFlagged,
		result.ThreadsDeleted,
		result.DeleteFailures,
		result.ModeratorSkipped,
	)

	// Add flagged thread details (up to 20)
	if len(result.FlaggedThreads) > 0 {
		logMsg += "\n\n**Would Delete:**"
		for i, t := range result.FlaggedThreads {
			if i >= 20 {
				logMsg += fmt.Sprintf("\n...and %d more", len(result.FlaggedThreads)-20)
				break
			}
			if t.Username != "" {
				logMsg += fmt.Sprintf("\n• <#%s> by %s — %s", t.ThreadID, t.Username, t.Reason)
			} else {
				logMsg += fmt.Sprintf("\n• <#%s> — %s", t.ThreadID, t.Reason)
			}
		}
	}

	if err := utils.LogToChannel(s.cfg, s.Session, logMsg); err != nil {
		s.cfg.Logger.Errorf("[IntroPrune] Failed to log results to channel: %v", err)
	}

	s.cfg.Logger.Infof("[IntroPrune] Scheduled prune completed: %d deleted, %d failures", result.ThreadsDeleted, result.DeleteFailures)
	return nil
}

// RunIntroPrune runs the consolidated intro prune logic combining duplicates cleanup
// and departed owner detection. If dryRun is true, no deletions are performed.
func RunIntroPrune(s *discordgo.Session, cfg *config.Config, forumCache *forumcache.Service, forumID, guildID string, dryRun bool) (*IntroPruneResult, error) {
	if forumCache == nil {
		return nil, fmt.Errorf("forum cache unavailable")
	}

	// Ensure forum is registered in cache
	forumCache.RegisterForum(forumID)

	// Get all cached threads
	threads, ok := forumCache.ListThreads(forumID)
	if !ok || len(threads) == 0 {
		return nil, fmt.Errorf("forum cache not populated for forum %s", forumID)
	}

	// Build owner set from cached threads
	ownerSet := make(map[string]struct{})
	for _, tm := range threads {
		ownerSet[tm.OwnerID] = struct{}{}
	}

	// Pre-compute membership, moderator status, and usernames from Discord API
	memberPresent := make(map[string]bool, len(ownerSet))
	moderatorIDs := make(map[string]struct{})
	ownerUsernames := make(map[string]string, len(ownerSet))

	for ownerID := range ownerSet {
		// Check membership: GuildMember returns error if user not present
		if member, err := s.GuildMember(guildID, ownerID); err == nil {
			memberPresent[ownerID] = true
			if member.User != nil {
				ownerUsernames[ownerID] = member.User.Username
			}
		} else {
			memberPresent[ownerID] = false
			// Try to get username for departed user via User endpoint
			if user, err := s.User(ownerID); err == nil {
				ownerUsernames[ownerID] = user.Username
			}
		}
		// Moderator detection: Ban Members permission in forum channel
		if perms, err := s.UserChannelPermissions(ownerID, forumID); err == nil && (perms&discordgo.PermissionBanMembers) != 0 {
			moderatorIDs[ownerID] = struct{}{}
		}
		time.Sleep(ownerCheckDelay)
	}

	// Delete callback wrapping Discord API
	deleteThread := func(threadID string) error {
		_, err := s.ChannelDelete(threadID)
		return err
	}

	return runIntroPrune(runIntroPruneInput{
		Threads:        threads,
		MemberPresent:  memberPresent,
		ModeratorIDs:   moderatorIDs,
		OwnerUsernames: ownerUsernames,
		DeleteThread:   deleteThread,
		ForumID:        forumID,
		Cfg:            cfg,
		DryRun:         dryRun,
	})
}

// runIntroPrune is the testable core logic operating on pre-computed data.
func runIntroPrune(input runIntroPruneInput) (*IntroPruneResult, error) {
	result := &IntroPruneResult{
		ThreadsScanned: len(input.Threads),
	}

	var flaggedThreads []FlaggedThread

	// Group threads by owner
	byOwner := make(map[string][]*forumcache.ThreadMeta)
	for _, tm := range input.Threads {
		byOwner[tm.OwnerID] = append(byOwner[tm.OwnerID], tm)
	}

	// Process each owner's threads
	for ownerID, metas := range byOwner {
		// Skip moderator owners
		if _, isMod := input.ModeratorIDs[ownerID]; isMod {
			result.ModeratorSkipped += len(metas)
			continue
		}

		// Departed owner: flag all threads
		if !input.MemberPresent[ownerID] {
			for _, meta := range metas {
				flaggedThreads = append(flaggedThreads, FlaggedThread{
					ThreadID:  meta.ID,
					Reason:    "owner departed",
					OwnerID:   ownerID,
					Username:  input.OwnerUsernames[ownerID],
					CreatedAt: meta.CreatedAt,
				})
			}
			continue
		}

		// Single thread => nothing to dedupe
		if len(metas) <= 1 {
			continue
		}

		// Sort threads oldest -> newest using CreatedAt then ID for deterministic ordering
		// Preserve the newest thread (last element) and flag all older ones as duplicates
		sort.Slice(metas, func(i, j int) bool {
			if metas[i].CreatedAt.Equal(metas[j].CreatedAt) {
				return metas[i].ID < metas[j].ID // tie-break: lower snowflake treated as older
			}
			return metas[i].CreatedAt.Before(metas[j].CreatedAt)
		})

		for _, meta := range metas[:len(metas)-1] { // all but newest
			flaggedThreads = append(flaggedThreads, FlaggedThread{
				ThreadID:  meta.ID,
				Reason:    "duplicate (older thread)",
				OwnerID:   ownerID,
				Username:  input.OwnerUsernames[ownerID],
				CreatedAt: meta.CreatedAt,
			})
		}
	}

	// Sort flagged threads for consistent ordering (newest first by ID)
	sort.Slice(flaggedThreads, func(a, b int) bool {
		return flaggedThreads[a].ThreadID > flaggedThreads[b].ThreadID
	})

	result.ThreadsFlagged = len(flaggedThreads)

	// Execute deletions (skip in dry run mode)
	if !input.DryRun {
		for _, f := range flaggedThreads {
			if err := input.DeleteThread(f.ThreadID); err != nil {
				result.DeleteFailures++
				if input.Cfg != nil && input.Cfg.Logger != nil {
					input.Cfg.Logger.Warnf("[IntroPrune] Failed deleting thread %s: %v", f.ThreadID, err)
				}
				continue
			}
			result.ThreadsDeleted++
			time.Sleep(threadDeleteDelay)
		}
	}

	result.FlaggedThreads = flaggedThreads

	return result, nil
}
