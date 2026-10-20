package igdbclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	client := NewClient("test-id", "test-secret")
	assert.NotNil(t, client)
	assert.Equal(t, "test-id", client.clientID)
	assert.Equal(t, "test-secret", client.clientSecret)
	assert.Nil(t, client.igdbClient, "client should be nil until first use (lazy init)")
}

func TestLazyTokenFetch(t *testing.T) {
	fetchCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token-" + time.Now().String(),
			"expires_in":   3600,
			"token_type":   "bearer",
		})
	}))
	defer server.Close()

	// Temporarily override the token endpoint for testing
	originalFetch := fetchTwitchAppToken
	fetchTwitchAppToken = func(clientID, clientSecret string) (string, error) {
		resp, err := http.Post(server.URL, "application/json", nil)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var result struct {
			AccessToken string `json:"access_token"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		return result.AccessToken, nil
	}
	defer func() { fetchTwitchAppToken = originalFetch }()

	client := NewClient("test-id", "test-secret")

	// Client should not have fetched yet
	assert.Equal(t, 0, fetchCount, "should not fetch token on construction")

	// First call should trigger fetch
	_, err := client.getClient()
	require.NoError(t, err)
	assert.Equal(t, 1, fetchCount, "first getClient call should fetch token")

	// Second call should reuse token
	_, err = client.getClient()
	require.NoError(t, err)
	assert.Equal(t, 1, fetchCount, "second getClient call should reuse token")
}

func TestConcurrentTokenFetchCoalescing(t *testing.T) {
	fetchCount := 0
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fetchCount++
		mu.Unlock()
		// Simulate slow token fetch
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"expires_in":   3600,
			"token_type":   "bearer",
		})
	}))
	defer server.Close()

	originalFetch := fetchTwitchAppToken
	fetchTwitchAppToken = func(clientID, clientSecret string) (string, error) {
		resp, err := http.Post(server.URL, "application/json", nil)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var result struct {
			AccessToken string `json:"access_token"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		return result.AccessToken, nil
	}
	defer func() { fetchTwitchAppToken = originalFetch }()

	client := NewClient("test-id", "test-secret")

	// Launch 10 concurrent getClient calls
	var wg sync.WaitGroup
	errCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.getClient()
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	// Should have only fetched token once despite 10 concurrent calls
	assert.Equal(t, 1, fetchCount, "concurrent getClient calls should coalesce to one fetch")
}

func TestForceRefresh(t *testing.T) {
	fetchCount := 0
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fetchCount++
		count := fetchCount
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token-" + string(rune('0'+count)),
			"expires_in":   3600,
			"token_type":   "bearer",
		})
	}))
	defer server.Close()

	originalFetch := fetchTwitchAppToken
	fetchTwitchAppToken = func(clientID, clientSecret string) (string, error) {
		resp, err := http.Post(server.URL, "application/json", nil)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var result struct {
			AccessToken string `json:"access_token"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		return result.AccessToken, nil
	}
	defer func() { fetchTwitchAppToken = originalFetch }()

	client := NewClient("test-id", "test-secret")

	// Initial fetch
	_, err := client.getClient()
	require.NoError(t, err)
	assert.Equal(t, 1, fetchCount)

	// Force refresh
	err = client.RefreshToken()
	require.NoError(t, err)
	assert.Equal(t, 2, fetchCount, "RefreshToken should fetch a new token")

	// Normal getClient should not fetch again
	_, err = client.getClient()
	require.NoError(t, err)
	assert.Equal(t, 2, fetchCount, "getClient after refresh should reuse refreshed token")
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"401 status", assert.AnError, false}, // generic error
		{"unauthorized lowercase", &testError{"unauthorized"}, true},
		{"Unauthorized capitalized", &testError{"Unauthorized"}, true},
		{"401 in message", &testError{"request failed with status 401"}, true},
		{"invalid token", &testError{"invalid token provided"}, true},
		{"other error", &testError{"network timeout"}, false},
		{"typed 401", igdb.ErrUnauthorized, true},
		{"wrapped typed 401", fmt.Errorf("cannot get index of Games: %w", igdb.ErrUnauthorized), true},
		{"typed 403", igdb.ErrForbidden, false},
		{"typed 500", igdb.ErrInternalError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAuthError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// stubTokens replaces fetchTwitchAppToken with a counter-backed stub that
// returns tok-1, tok-2, ... and restores the original on cleanup.
func stubTokens(t *testing.T, delay time.Duration, fail func(n int) bool) *fetchCounter {
	t.Helper()
	cnt := &fetchCounter{}
	orig := fetchTwitchAppToken
	fetchTwitchAppToken = func(string, string) (string, error) {
		n := cnt.inc()
		time.Sleep(delay)
		if fail != nil && fail(n) {
			return "", errors.New("twitch down")
		}
		return fmt.Sprintf("tok-%d", n), nil
	}
	t.Cleanup(func() { fetchTwitchAppToken = orig })
	return cnt
}

type fetchCounter struct {
	mu sync.Mutex
	n  int
}

func (c *fetchCounter) inc() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}

func (c *fetchCounter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func TestRetryCoalescesConcurrent401s(t *testing.T) {
	cnt := stubTokens(t, 20*time.Millisecond, nil)
	client := NewClient("id", "secret")

	const workers = 10
	var sawExpired sync.WaitGroup
	sawExpired.Add(workers)

	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			first := true
			errs[i] = client.Retry(func() error {
				tok := client.currentToken()
				if first {
					first = false
					// Make every worker observe the expired token before anyone refreshes.
					sawExpired.Done()
					sawExpired.Wait()
				}
				if tok == "tok-1" {
					return fmt.Errorf("cannot get index of Games: %w", igdb.ErrUnauthorized)
				}
				return nil
			})
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 2, cnt.get(), "initial fetch + exactly one refresh for all concurrent 401s")
	assert.Equal(t, "tok-2", client.currentToken())
}

func TestRetryDoesNotRefreshOnNonAuthError(t *testing.T) {
	cnt := stubTokens(t, 0, nil)
	client := NewClient("id", "secret")

	err := client.Retry(func() error { return igdb.ErrInternalError })
	assert.ErrorIs(t, err, igdb.ErrInternalError)
	assert.Equal(t, 1, cnt.get(), "only the initial fetch should happen")
}

func TestFailedRefreshKeepsExistingClient(t *testing.T) {
	stubTokens(t, 0, func(n int) bool { return n == 2 })
	client := NewClient("id", "secret")

	_, err := client.getClient()
	require.NoError(t, err)

	require.Error(t, client.RefreshToken())
	assert.Equal(t, "tok-1", client.currentToken(), "old token should survive a failed refresh")
	assert.NotNil(t, client.igdbClient)
}
