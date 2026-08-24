package igdbclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
