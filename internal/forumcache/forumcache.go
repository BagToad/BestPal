package forumcache

import (
	"fmt"
	"gamerpal/internal/config"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

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
	nameExact     map[string]*ThreadMeta // normalized name -> latest thread with that name
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
	config  *config.Config
}

// threadLister abstracts active + archived thread listing for RefreshForum logic.
// Returns raw slices of channels to keep the interface independent of discordgo response structs.
type threadLister interface {
	ListActiveThreads(guildID string) ([]*discordgo.Channel, error)
	ListArchivedThreads(forumID string, before *time.Time) ([]*discordgo.Channel, bool, error)
}

// sessionLister adapts a discordgo.Session to threadLister.
type sessionLister struct{ *discordgo.Session }

func (sl sessionLister) ListActiveThreads(guildID string) ([]*discordgo.Channel, error) {
	active, err := sl.GuildThreadsActive(guildID)
	if err != nil || active == nil {
		return nil, err
	}
	return active.Threads, nil
}

func (sl sessionLister) ListArchivedThreads(forumID string, before *time.Time) ([]*discordgo.Channel, bool, error) {
	// Discord docs: List Public Archived Threads are ordered by archive_timestamp desc.
	// Passing an explicit non-zero limit (max 100) reduces number of round trips versus implicit default (often 25).
	archived, err := sl.ThreadsArchived(forumID, before, 100)
	if err != nil || archived == nil {
		return nil, false, err
	}
	return archived.Threads, archived.HasMore, nil
}

// NewForumCacheService creates a new Service.
func NewForumCacheService(config *config.Config) *Service {
	return &Service{
		forums: make(map[string]*forumIndex),
		config: config,
	}
}

// NewTestForumCache constructs a mock Config and Service for tests in other packages.
// Ensures a bot_token is present unless provided, mirroring prior per-file helpers.
func NewTestForumCache(kv map[string]interface{}) (*config.Config, *Service) {
	if kv == nil {
		kv = map[string]interface{}{"bot_token": "x"}
	}
	if _, ok := kv["bot_token"]; !ok {
		kv["bot_token"] = "x"
	}
	cfg := config.NewMockConfig(kv)
	return cfg, NewForumCacheService(cfg)
}

// HydrateSession sets the Discord session reference.
func (s *Service) HydrateSession(sess *discordgo.Session) { s.session = sess }

// RegisterForum ensures a forum index exists (idempotent). guildID is inferred during builds from threads; we don't store it globally yet.
func (s *Service) RegisterForum(forumID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.forums[forumID]; !exists {
		s.forums[forumID] = &forumIndex{threads: make(map[string]*ThreadMeta), ownerLatest: make(map[string]*ThreadMeta), nameExact: make(map[string]*ThreadMeta)}
	}
}

// RefreshForum performs a full rebuild (active + archived) of a specific forum.
func (s *Service) RefreshForum(guildID, forumID string) error {
	if s.session == nil {
		return fmt.Errorf("forum cache not hydrated with session")
	}
	return s.refreshForumWithLister(guildID, forumID, sessionLister{s.session})
}

// refreshForumWithLister contains the core logic, parameterized by a threadLister for test seams.
func (s *Service) refreshForumWithLister(guildID, forumID string, l threadLister) error {
	s.RegisterForum(forumID)
	idx := s.forums[forumID]

	tempThreads := make(map[string]*ThreadMeta)
	tempOwnerLatest := make(map[string]*ThreadMeta)
	tempNameExact := make(map[string]*ThreadMeta)

	activeThreads, err := l.ListActiveThreads(guildID)
	if err != nil {
		idx.mu.Lock()
		idx.fullSyncErrs++
		idx.mu.Unlock()
		return fmt.Errorf("listing active threads failed: %w", err)
	}
	for _, th := range activeThreads {
		if th.ParentID != forumID {
			continue
		}
		s.seedMeta(tempThreads, tempOwnerLatest, tempNameExact, guildID, forumID, th)
	}

	var before *time.Time
	for page := 1; ; page++ {
		archivedThreads, hasMore, err := l.ListArchivedThreads(forumID, before)
		if err != nil {
			s.config.Logger.Errorf("ForumCache RefreshForum: error listing archived threads (page %d): %v", page, err)
			break
		}
		if len(archivedThreads) == 0 {
			s.config.Logger.Infof("ForumCache RefreshForum: no more archived pages (page %d empty)", page)
			break
		}

		for _, th := range archivedThreads { // seed each archived thread into temp maps
			s.seedMeta(tempThreads, tempOwnerLatest, tempNameExact, guildID, forumID, th)
		}
		if !hasMore { // no further pages advertised
			s.config.Logger.Infof("ForumCache RefreshForum: no more archived pages (page %d, has_more=false)", page)
			break
		}

		// Pagination cursor: Discord orders by thread_metadata.archive_timestamp desc.
		// We should pass an ISO8601 timestamp BEFORE the oldest archive_timestamp we have processed.
		// Prefer archive_timestamp over creation snowflake to avoid skipping threads whose archive time != creation time or that were later unarchived/re-archived.
		last := archivedThreads[len(archivedThreads)-1]
		var cursor time.Time
		if last.ThreadMetadata != nil && !last.ThreadMetadata.ArchiveTimestamp.IsZero() {
			cursor = last.ThreadMetadata.ArchiveTimestamp
		} else if ts, tsErr := discordgo.SnowflakeTimestamp(last.ID); tsErr == nil { // fallback: creation time (may be older than archive time)
			cursor = ts
		} else {
			s.config.Logger.Error("ForumCache RefreshForum: cannot derive pagination cursor; aborting further archived fetches")
			break
		}
		before = &cursor
	}

	now := time.Now()
	idx.mu.Lock()
	idx.threads = tempThreads
	idx.ownerLatest = tempOwnerLatest
	idx.nameExact = tempNameExact
	idx.lastFullSync = now
	idx.mu.Unlock()
	return nil
}

// normalizeName produces the canonical comparison form of a thread name.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if r == ' ' { // preserve single spaces
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		// Drop punctuation/symbols entirely (do not insert spaces) so internal punctuation like
		// "R.E.P.O" normalizes to "repo" (query "repo" matches exact) and "No-Man's" -> "nomans".
		// This favors contiguous matching; users typing without punctuation get expected hits.
	}
	return strings.TrimSpace(b.String())
}

// latestTieBreak returns true if a should replace b as latest given CreatedAt then ID lexicographic.
func latestTieBreak(a, b *ThreadMeta) bool {
	if b == nil {
		return true
	}
	if a.CreatedAt.After(b.CreatedAt) {
		return true
	}
	if a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID {
		return true
	}
	return false
}

// seedMeta converts a discordgo.Channel thread into ThreadMeta and seeds maps.
func (s *Service) seedMeta(tempThreads map[string]*ThreadMeta, tempOwnerLatest map[string]*ThreadMeta, tempNameExact map[string]*ThreadMeta, guildID, forumID string, th *discordgo.Channel) {
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
	// Owner latest selection (CreatedAt then ID tie-break)
	if prev := tempOwnerLatest[meta.OwnerID]; latestTieBreak(meta, prev) {
		tempOwnerLatest[meta.OwnerID] = meta
	}
	// Exact name selection (duplicate names allowed; pick latest)
	norm := normalizeName(meta.Name)
	if prev := tempNameExact[norm]; latestTieBreak(meta, prev) {
		tempNameExact[norm] = meta
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

// GetThreadByExactName returns the latest thread whose normalized name exactly matches.
func (s *Service) GetThreadByExactName(forumID, name string) (*ThreadMeta, bool) {
	norm := normalizeName(name)
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return nil, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	meta, ok := idx.nameExact[norm]
	return meta, ok
}

// SearchThreads performs a scored search (exact > prefix > word boundary > contains) over cached threads.
// Returns up to limit results (if limit <=0 default to 25).
func (s *Service) SearchThreads(forumID, query string, limit int) ([]*ThreadMeta, bool) {
	q := normalizeName(query)
	if q == "" {
		return nil, false
	}
	if limit <= 0 {
		limit = 25
	}
	s.mu.RLock()
	idx, exists := s.forums[forumID]
	s.mu.RUnlock()
	if !exists {
		return nil, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	// Buckets
	exact := make([]*ThreadMeta, 0)
	prefix := make([]*ThreadMeta, 0)
	boundary := make([]*ThreadMeta, 0)
	contains := make([]*ThreadMeta, 0)

	for _, meta := range idx.threads {
		norm := normalizeName(meta.Name)
		if norm == q {
			exact = append(exact, meta)
			continue
		}
		if strings.HasPrefix(norm, q) {
			prefix = append(prefix, meta)
			continue
		}
		// word boundary: any token equals query
		tokens := strings.Fields(norm)
		matchedBoundary := false
		for _, tk := range tokens {
			if tk == q {
				matchedBoundary = true
				break
			}
		}
		if matchedBoundary {
			boundary = append(boundary, meta)
			continue
		}
		if strings.Contains(norm, q) {
			contains = append(contains, meta)
		}
	}

	// Sort each bucket by CreatedAt desc then ID desc.
	sorter := func(sl []*ThreadMeta) {
		sort.Slice(sl, func(i, j int) bool {
			if sl[i].CreatedAt.Equal(sl[j].CreatedAt) {
				return sl[i].ID > sl[j].ID
			}
			return sl[i].CreatedAt.After(sl[j].CreatedAt)
		})
	}
	sorter(exact)
	sorter(prefix)
	sorter(boundary)
	sorter(contains)

	merged := make([]*ThreadMeta, 0, len(exact)+len(prefix)+len(boundary)+len(contains))
	merged = append(merged, exact...)
	merged = append(merged, prefix...)
	merged = append(merged, boundary...)
	merged = append(merged, contains...)
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, true
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
	if prev := idx.ownerLatest[meta.OwnerID]; latestTieBreak(meta, prev) {
		idx.ownerLatest[meta.OwnerID] = meta
	}
	norm := normalizeName(meta.Name)
	if prev := idx.nameExact[norm]; latestTieBreak(meta, prev) {
		idx.nameExact[norm] = meta
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
		oldNorm := normalizeName(meta.Name)
		meta.Name = thread.Name
		meta.Archived = thread.ThreadMetadata != nil && thread.ThreadMetadata.Archived
		meta.LastMessage = thread.LastMessageID
		newNorm := normalizeName(meta.Name)
		if oldNorm != newNorm {
			// If this meta was the representative of oldNorm, find replacement.
			if cur := idx.nameExact[oldNorm]; cur == meta {
				var replacement *ThreadMeta
				for _, t := range idx.threads {
					if t == meta {
						continue
					}
					if normalizeName(t.Name) != oldNorm {
						continue
					}
					if latestTieBreak(t, replacement) {
						replacement = t
					}
				}
				if replacement != nil {
					idx.nameExact[oldNorm] = replacement
				} else {
					delete(idx.nameExact, oldNorm)
				}
			}
			// Update new norm representative.
			if prev := idx.nameExact[newNorm]; latestTieBreak(meta, prev) {
				idx.nameExact[newNorm] = meta
			}
		} else {
			// Name unchanged; ensure representative tie-break still honored (e.g., metadata changed not affecting ordering).
			if prev := idx.nameExact[oldNorm]; latestTieBreak(meta, prev) {
				idx.nameExact[oldNorm] = meta
			}
		}
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
			var replacement *ThreadMeta
			for _, t := range idx.threads {
				if t.OwnerID != meta.OwnerID {
					continue
				}
				if latestTieBreak(t, replacement) {
					replacement = t
				}
			}
			if replacement != nil {
				idx.ownerLatest[meta.OwnerID] = replacement
			} else {
				delete(idx.ownerLatest, meta.OwnerID)
			}
		}
		// Name exact fallback.
		norm := normalizeName(meta.Name)
		if cur := idx.nameExact[norm]; cur == meta {
			var replacement *ThreadMeta
			for _, t := range idx.threads {
				if normalizeName(t.Name) != norm {
					continue
				}
				if latestTieBreak(t, replacement) {
					replacement = t
				}
			}
			if replacement != nil {
				idx.nameExact[norm] = replacement
			} else {
				delete(idx.nameExact, norm)
			}
		}
	} else {
		idx.anomalies++
	}
	idx.eventDeletes++
	idx.lastEventTime = time.Now()
	idx.mu.Unlock()
}

// OnThreadListSync can refresh known subset – here we just mark anomalies if forum not registered;
// otherwise treat as soft rebuild for listed threads only.
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
		tempNameExact := make(map[string]*ThreadMeta)
		for _, th := range threads {
			s.seedMeta(tempThreads, tempOwnerLatest, tempNameExact, th.GuildID, forumID, th)
		}
		idx.mu.Lock()
		for id, meta := range tempThreads {
			idx.threads[id] = meta
		}
		for owner, meta := range tempOwnerLatest {
			if prev := idx.ownerLatest[owner]; latestTieBreak(meta, prev) {
				idx.ownerLatest[owner] = meta
			}
		}
		for norm, meta := range tempNameExact {
			if prev := idx.nameExact[norm]; latestTieBreak(meta, prev) {
				idx.nameExact[norm] = meta
			}
		}
		idx.lastEventTime = time.Now()
		idx.mu.Unlock()
	}
}
