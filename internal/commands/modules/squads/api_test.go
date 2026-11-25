package squads

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnowflakeAfter(t *testing.T) {
	assert.True(t, snowflakeAfter("10", ""))
	assert.True(t, snowflakeAfter("100", "99"))
	assert.True(t, snowflakeAfter("124", "123"))
	assert.False(t, snowflakeAfter("123", "123"))
	assert.False(t, snowflakeAfter("99", "100"))
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A used-up rename limit refuses further renames without a request, and
// doesn't hold up closing the thread.
func TestUsedUpRenameLimitDoesNotDelayClosing(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	s, err := discordgo.New("Bot x")
	require.NoError(t, err)
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		h := http.Header{"Content-Type": {"application/json"}}
		body := `{"id":"101","type":12}`
		if r.Method == "GET" {
			body = "[]"
		} else if strings.Contains(readBody(r), `"name"`) {
			h.Set("X-RateLimit-Remaining", "0")
			h.Set("X-RateLimit-Reset-After", "600")
		}
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	a := sessionAPI{s}
	noReopen := func(func() error) error { t.Fatal("unexpected reopen"); return nil }

	require.NoError(t, a.RenameThread("101", "One", noReopen))
	var rl *discordgo.RateLimitError
	start := time.Now()
	assert.ErrorAs(t, a.RenameThread("101", "Two", noReopen), &rl)
	assert.Len(t, requests, 1, "a refused rename makes no request")

	require.NoError(t, a.closeThread("101"))
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Len(t, requests, 4)
}

func readBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// A bot-wide rate limit is refused without counting against the renames.
func TestGlobalRateLimitIsNotARenameLimit(t *testing.T) {
	s, err := discordgo.New("Bot x")
	require.NoError(t, err)
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		t.Fatal("unexpected request")
		return nil, nil
	})}
	// Make discordgo record a global limit.
	resp := &http.Response{Header: http.Header{"X-Ratelimit-Global": {"true"}, "X-Ratelimit-Reset-After": {"60"}}}
	b := s.Ratelimiter.LockBucket("x")
	require.NoError(t, b.Release(resp.Header))
	err = sessionAPI{s}.RenameThread("101", "One", nil)
	assert.ErrorIs(t, err, errBotRateLimited)
}
