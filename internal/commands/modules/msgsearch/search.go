package msgsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	// PageSize is the maximum number of results Discord returns per search page.
	PageSize = 25
	// MaxOffset is the largest offset the search endpoint accepts.
	MaxOffset = 9975
	// MaxPages is the deepest page reachable through offset pagination.
	MaxPages = MaxOffset/PageSize + 1

	maxIndexRetries = 10
	discordEpochMs  = 1420070400000
)

// searchParams holds the filters forwarded to GET /guilds/{id}/messages/search.
type searchParams struct {
	Content     string
	AuthorID    string
	AuthorType  string
	ChannelID   string
	MentionsID  string
	Has         string
	Pinned      *bool
	MinID       string
	MaxID       string
	SortBy      string
	SortOrder   string
	IncludeNSFW bool
}

func (p searchParams) values(offset int) url.Values {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(PageSize))
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("content", p.Content)
	set("author_id", p.AuthorID)
	set("author_type", p.AuthorType)
	set("channel_id", p.ChannelID)
	set("mentions", p.MentionsID)
	set("has", p.Has)
	set("min_id", p.MinID)
	set("max_id", p.MaxID)
	set("sort_by", p.SortBy)
	set("sort_order", p.SortOrder)
	if p.Pinned != nil {
		q.Set("pinned", strconv.FormatBool(*p.Pinned))
	}
	if p.IncludeNSFW {
		q.Set("include_nsfw", "true")
	}
	return q
}

type searchResponse struct {
	TotalResults             int                    `json:"total_results"`
	Messages                 [][]*discordgo.Message `json:"messages"`
	Threads                  []*discordgo.Channel   `json:"threads"`
	DoingDeepHistoricalIndex bool                   `json:"doing_deep_historical_index"`
}

// indexNotReadyError is returned when Discord answers 202 because the guild
// has not been indexed for search yet.
type indexNotReadyError struct {
	RetryAfter time.Duration
}

func (e *indexNotReadyError) Error() string {
	return fmt.Sprintf("search index not ready, retry after %s", e.RetryAfter)
}

// searchFunc performs one search request. It is swapped out in tests.
type searchFunc func(ctx context.Context, guildID string, q url.Values) (*searchResponse, error)

func discordSearch(s *discordgo.Session) searchFunc {
	return func(ctx context.Context, guildID string, q url.Values) (*searchResponse, error) {
		endpoint := discordgo.EndpointGuild(guildID) + "/messages/search"
		body, err := s.RequestWithBucketID("GET", endpoint+"?"+q.Encode(), nil, endpoint, discordgo.WithContext(ctx))
		if err != nil {
			var restErr *discordgo.RESTError
			if errors.As(err, &restErr) && restErr.Response != nil && restErr.Response.StatusCode == http.StatusAccepted {
				var notReady struct {
					RetryAfter float64 `json:"retry_after"`
				}
				_ = json.Unmarshal(restErr.ResponseBody, &notReady)
				return nil, &indexNotReadyError{RetryAfter: time.Duration(notReady.RetryAfter * float64(time.Second))}
			}
			return nil, err
		}
		var resp searchResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode search response: %w", err)
		}
		return &resp, nil
	}
}

type pageFetcher struct {
	search searchFunc
	sleep  func(ctx context.Context, d time.Duration) error
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// fetchPage requests a 1-based page, waiting out "index not ready" responses.
func (f pageFetcher) fetchPage(ctx context.Context, guildID string, p searchParams, page int) (*searchResponse, error) {
	q := p.values((page - 1) * PageSize)
	for attempt := 0; ; attempt++ {
		resp, err := f.search(ctx, guildID, q)
		var notReady *indexNotReadyError
		if !errors.As(err, &notReady) {
			return resp, err
		}
		if attempt >= maxIndexRetries {
			return nil, errors.New("Discord is still indexing this server for search; try again in a few minutes")
		}
		wait := notReady.RetryAfter
		if wait < time.Second {
			wait = time.Second
		}
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
		if err := f.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

type exportResult struct {
	Messages     []*discordgo.Message
	Threads      map[string]*discordgo.Channel
	TotalResults int
	TotalPages   int
	FirstPage    int
	LastPage     int // last page actually fetched
	Truncated    bool
}

// totalPages converts a result count into reachable pages.
func totalPages(total int) int {
	if total <= 0 {
		return 0
	}
	pages := (total + PageSize - 1) / PageSize
	if pages > MaxPages {
		pages = MaxPages
	}
	return pages
}

// collect fetches pages pageStart..pageEnd (pageEnd 0 = through the last page).
// It stops early, marking the result truncated, if ctx expires mid-export.
func (f pageFetcher) collect(ctx context.Context, guildID string, p searchParams, pageStart, pageEnd int, progress func(page, last int)) (*exportResult, error) {
	if pageStart < 1 {
		pageStart = 1
	}
	if pageEnd != 0 && pageEnd < pageStart {
		return nil, fmt.Errorf("page_end (%d) must be greater than or equal to page_start (%d)", pageEnd, pageStart)
	}

	res := &exportResult{Threads: map[string]*discordgo.Channel{}, FirstPage: pageStart}
	seen := map[string]bool{}
	add := func(resp *searchResponse) int {
		added := 0
		for _, group := range resp.Messages {
			for _, msg := range group {
				if msg == nil || seen[msg.ID] {
					continue
				}
				seen[msg.ID] = true
				res.Messages = append(res.Messages, msg)
				added++
			}
		}
		for _, th := range resp.Threads {
			if th != nil {
				res.Threads[th.ID] = th
			}
		}
		return added
	}

	first, err := f.fetchPage(ctx, guildID, p, pageStart)
	if err != nil {
		return nil, err
	}
	res.TotalResults = first.TotalResults
	res.TotalPages = totalPages(first.TotalResults)
	if res.TotalPages == 0 {
		return res, nil
	}
	if pageStart > res.TotalPages {
		return nil, fmt.Errorf("page_start %d is past the last page (%d pages of results)", pageStart, res.TotalPages)
	}

	last := res.TotalPages
	if pageEnd != 0 && pageEnd < last {
		last = pageEnd
	}

	if add(first) == 0 {
		res.LastPage = pageStart
		return res, nil
	}
	res.LastPage = pageStart
	if progress != nil {
		progress(pageStart, last)
	}

	for page := pageStart + 1; page <= last; page++ {
		if ctx.Err() != nil {
			res.Truncated = true
			break
		}
		resp, err := f.fetchPage(ctx, guildID, p, page)
		if err != nil {
			if ctx.Err() != nil {
				res.Truncated = true
				break
			}
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		res.LastPage = page
		if progress != nil {
			progress(page, last)
		}
		// Discord can report more results than it returns; an empty page means we are done.
		if add(resp) == 0 {
			break
		}
	}
	return res, nil
}

// parseDay parses YYYY-MM-DD as a UTC day.
func parseDay(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date in YYYY-MM-DD format", s)
	}
	return t, nil
}

// snowflakeAt returns the smallest snowflake that could be created at t.
func snowflakeAt(t time.Time) string {
	ms := t.UnixMilli() - discordEpochMs
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatUint(uint64(ms)<<22, 10)
}

// dayBounds turns inclusive after/before days into exclusive min_id/max_id snowflakes.
func dayBounds(after, before string) (minID, maxID string, err error) {
	var a, b time.Time
	if after != "" {
		if a, err = parseDay(after); err != nil {
			return "", "", err
		}
		ms := a.UnixMilli() - discordEpochMs
		if ms > 0 {
			minID = strconv.FormatUint(uint64(ms)<<22-1, 10)
		}
	}
	if before != "" {
		if b, err = parseDay(before); err != nil {
			return "", "", err
		}
		maxID = snowflakeAt(b.AddDate(0, 0, 1))
	}
	if after != "" && before != "" && b.Before(a) {
		return "", "", errors.New("`before` must be on or after `after`")
	}
	return minID, maxID, nil
}
