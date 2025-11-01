package forumcache

import (
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ThreadMeta holds minimal metadata we care about for cached forum threads.
type ThreadMeta struct {
	ID          string
	ForumID     string
	GuildID     string
	OwnerID     string
	Name        string
	CreatedAt   time.Time
	Archived    bool
	LastMessage string // last message ID (optional quick activity indicator)
}

// ForumStats exposes lightweight observability data.
type ForumStats struct {
	ForumID        string
	GuildID        string
	Threads        int
	OwnersTracked  int
	LastFullSync   time.Time
	LastEventTime  time.Time
	FullSyncErrors int
	EventAdds      int
	EventUpdates   int
	EventDeletes   int
	Anomalies      int
}

// forumIndex maintains thread + secondary owner index for a single forum.
type forumIndex struct {
	mu            sync.RWMutex
	threads       map[string]*ThreadMeta // threadID -> meta
	ownerLatest   map[string]*ThreadMeta // ownerID -> latest thread
	lastFullSync  time.Time
	lastEventTime time.Time
	fullSyncErrs  int
	eventAdds     int
	eventUpdates  int
	eventDeletes  int
	anomalies     int
}

// Service manages multiple forum indexes and provides lookup APIs.
type Service struct {
	mu      sync.RWMutex
	forums  map[string]*forumIndex // forumID -> index
	session *discordgo.Session     // hydrated after bot connects
}

// New creates a new Service.
func New() *Service {
	return &Service{forums: make(map[string]*forumIndex)}
}

// HydrateSession sets the Discord session reference.
func (s *Service) HydrateSession(sess *discordgo.Session) { s.session = sess }

// RegisterForum ensures a forum index exists (idempotent). guildID is inferred during builds from threads; we don't store it globally yet.
func (s *Service) RegisterForum(forumID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.forums[forumID]; !exists {
		s.forums[forumID] = &forumIndex{threads: make(map[string]*ThreadMeta), ownerLatest: make(map[string]*ThreadMeta)}
	}
}

// RefreshForum performs a full rebuild (active + archived) of a specific forum.
func (s *Service) RefreshForum(guildID, forumID string) error {
	if s.session == nil {
		return fmt.Errorf("forum cache not hydrated with session")
	}
	s.RegisterForum(forumID) // ensure exists
	idx := s.forums[forumID]

	// Local temp maps to avoid partial writes.
	tempThreads := make(map[string]*ThreadMeta)
	tempOwnerLatest := make(map[string]*ThreadMeta)

	// 1. Active threads (guild-wide list) filter by ParentID.
	active, err := s.session.GuildThreadsActive(guildID)
	if err != nil {
		idx.mu.Lock()
		idx.fullSyncErrs++
		idx.mu.Unlock()
		return fmt.Errorf("listing active threads failed: %w", err)
	}
	for _, th := range active.Threads {
		if th.ParentID != forumID {
			continue
		}
		s.seedMeta(tempThreads, tempOwnerLatest, guildID, forumID, th)
	}

	// 2. Archived threads (paginate) – best effort, ignore errors mid-way.
	var before *time.Time
	for {
		archived, aErr := s.session.ThreadsArchived(forumID, before, 50)
		if aErr != nil || archived == nil || len(archived.Threads) == 0 {
			break
		}
		for _, th := range archived.Threads {
			s.seedMeta(tempThreads, tempOwnerLatest, guildID, forumID, th)
		}
		if !archived.HasMore {
			break
		}
		last := archived.Threads[len(archived.Threads)-1]
		if ts, tsErr := discordgo.SnowflakeTimestamp(last.ID); tsErr == nil {
			t := ts
			before = &t
		} else {
			break
		}
	}

	// Commit under lock.
	now := time.Now()
	idx.mu.Lock()
	idx.threads = tempThreads
	idx.ownerLatest = tempOwnerLatest
	idx.lastFullSync = now
	idx.mu.Unlock()
	return nil
}

// seedMeta converts a discordgo.Channel thread into ThreadMeta and seeds maps.
func (s *Service) seedMeta(tempThreads map[string]*ThreadMeta, tempOwnerLatest map[string]*ThreadMeta, guildID, forumID string, th *discordgo.Channel) {
	if th == nil {
		return
	}
	created, _ := discordgo.SnowflakeTimestamp(th.ID)
	meta := &ThreadMeta{
		ID:          th.ID,
		ForumID:     forumID,
		GuildID:     guildID,
		OwnerID:     th.OwnerID,
		Name:        th.Name,
		CreatedAt:   created,
		Archived:    th.ThreadMetadata != nil && th.ThreadMetadata.Archived,
		LastMessage: th.LastMessageID,
	}
	tempThreads[th.ID] = meta
	// Owner latest selection – choose newest CreatedAt (or lexicographically larger ID fallback).
	if prev, ok := tempOwnerLatest[meta.OwnerID]; !ok || meta.CreatedAt.After(prev.CreatedAt) || meta.ID > prev.ID {
		tempOwnerLatest[meta.OwnerID] = meta
	}
}

// GetLatestUserThread returns the latest thread for a user within a forum.
func (s *Service) GetLatestUserThread(forumID, userID string) (*ThreadMeta, bool) {
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return nil, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	meta, ok := idx.ownerLatest[userID]
	return meta, ok
}

// ListThreads returns all cached threads for a forum.
func (s *Service) ListThreads(forumID string) ([]*ThreadMeta, bool) {
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return nil, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]*ThreadMeta, 0, len(idx.threads))
	for _, v := range idx.threads {
		out = append(out, v)
	}
	return out, true
}

// Stats returns ForumStats for a given forum.
func (s *Service) Stats(forumID string) (ForumStats, bool) {
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return ForumStats{}, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return ForumStats{
		ForumID:        forumID,
		Threads:        len(idx.threads),
		OwnersTracked:  len(idx.ownerLatest),
		LastFullSync:   idx.lastFullSync,
		LastEventTime:  idx.lastEventTime,
		FullSyncErrors: idx.fullSyncErrs,
		EventAdds:      idx.eventAdds,
		EventUpdates:   idx.eventUpdates,
		EventDeletes:   idx.eventDeletes,
		Anomalies:      idx.anomalies,
	}, true
}

// --- Event Handlers (called from bot) ---

// OnThreadCreate updates cache with new thread if forum registered.
func (s *Service) OnThreadCreate(_ *discordgo.Session, e *discordgo.ThreadCreate) {
	if e == nil || e.Channel == nil {
		return
	}
	thread := e.Channel
	forumID := thread.ParentID
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return
	}
	created, _ := discordgo.SnowflakeTimestamp(thread.ID)
	meta := &ThreadMeta{
		ID:          thread.ID,
		ForumID:     forumID,
		GuildID:     thread.GuildID,
		OwnerID:     thread.OwnerID,
		Name:        thread.Name,
		CreatedAt:   created,
		Archived:    thread.ThreadMetadata != nil && thread.ThreadMetadata.Archived,
		LastMessage: thread.LastMessageID,
	}
	idx.mu.Lock()
	idx.threads[meta.ID] = meta
	if prev, ok := idx.ownerLatest[meta.OwnerID]; !ok || meta.CreatedAt.After(prev.CreatedAt) || meta.ID > prev.ID {
		idx.ownerLatest[meta.OwnerID] = meta
	}
	idx.eventAdds++
	idx.lastEventTime = time.Now()
	idx.mu.Unlock()
}

// OnThreadUpdate adjusts metadata & owner latest if required.
func (s *Service) OnThreadUpdate(_ *discordgo.Session, e *discordgo.ThreadUpdate) {
	if e == nil || e.Channel == nil {
		return
	}
	thread := e.Channel
	forumID := thread.ParentID
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return
	}
	idx.mu.Lock()
	if meta, ok := idx.threads[thread.ID]; ok {
		meta.Name = thread.Name
		meta.Archived = thread.ThreadMetadata != nil && thread.ThreadMetadata.Archived
		meta.LastMessage = thread.LastMessageID
		// Owner latest recalculation only if IDs differ (rare if ownership changes) – simple approach: re-evaluate.
		// If ownership changes we would recompute latest; current implementation assumes stable ownership.
	} else {
		// anomaly: update for unknown thread
		idx.anomalies++
	}
	idx.eventUpdates++
	idx.lastEventTime = time.Now()
	idx.mu.Unlock()
}

// OnThreadDelete removes thread + fixes ownerLatest.
func (s *Service) OnThreadDelete(_ *discordgo.Session, e *discordgo.ThreadDelete) {
	if e == nil || e.Channel == nil {
		return
	}
	thread := e.Channel
	forumID := thread.ParentID
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return
	}
	idx.mu.Lock()
	if meta, ok := idx.threads[thread.ID]; ok {
		delete(idx.threads, thread.ID)
		if cur, ok2 := idx.ownerLatest[meta.OwnerID]; ok2 && cur.ID == meta.ID {
			// Need to find next latest for this owner.
			var replacement *ThreadMeta
			for _, t := range idx.threads {
				if t.OwnerID != meta.OwnerID {
					continue
				}
				if replacement == nil || t.CreatedAt.After(replacement.CreatedAt) || t.ID > replacement.ID {
					replacement = t
				}
			}
			if replacement != nil {
				idx.ownerLatest[meta.OwnerID] = replacement
			} else {
				delete(idx.ownerLatest, meta.OwnerID)
			}
		}
	} else {
		idx.anomalies++
	}
	idx.eventDeletes++
	idx.lastEventTime = time.Now()
	idx.mu.Unlock()
}

// OnThreadListSync can refresh known subset – here we just mark anomalies if forum not registered; otherwise treat as soft rebuild for listed threads only.
func (s *Service) OnThreadListSync(_ *discordgo.Session, e *discordgo.ThreadListSync) {
	if e == nil {
		return
	}
	// Iterate threads; group updates by forum parent.
	forumBuckets := make(map[string][]*discordgo.Channel)
	for _, th := range e.Threads {
		if th != nil {
			forumBuckets[th.ParentID] = append(forumBuckets[th.ParentID], th)
		}
	}
	for forumID, threads := range forumBuckets {
		s.mu.RLock()
		idx, exists := s.forums[forumID]
		s.mu.RUnlock()
		if !exists {
			continue
		}
		tempThreads := make(map[string]*ThreadMeta)
		tempOwnerLatest := make(map[string]*ThreadMeta)
		for _, th := range threads {
			s.seedMeta(tempThreads, tempOwnerLatest, th.GuildID, forumID, th)
		}
		idx.mu.Lock()
		for id, meta := range tempThreads {
			idx.threads[id] = meta
		}
		for owner, meta := range tempOwnerLatest {
			if prev, ok := idx.ownerLatest[owner]; !ok || meta.CreatedAt.After(prev.CreatedAt) || meta.ID > prev.ID {
				idx.ownerLatest[owner] = meta
			}
		}
		idx.lastEventTime = time.Now()
		idx.mu.Unlock()
	}
}
