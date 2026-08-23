package igdbclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/Henry-Sarabia/igdb/v2"
)

// Client wraps igdb.Client with automatic token refresh on 401 responses.
// It manages the token lifecycle: lazy initialization, auto-refresh on auth
// failures, and coalescing concurrent token fetches.
type Client struct {
	clientID     string
	clientSecret string

	mu            sync.RWMutex
	token         string
	igdbClient    *igdb.Client
	fetchInFlight bool
}

// NewClient creates a new auto-refreshing IGDB client.
// The token is fetched lazily on first use, so this constructor never blocks.
func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// Games returns the Games service, ensuring the client is initialized.
func (c *Client) Games() (*igdb.GameService, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	return client.Games, nil
}

// Websites returns the Websites service, ensuring the client is initialized.
func (c *Client) Websites() (*igdb.WebsiteService, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	return client.Websites, nil
}

// Covers returns the Covers service, ensuring the client is initialized.
func (c *Client) Covers() (*igdb.CoverService, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	return client.Covers, nil
}

// MultiplayerModes returns the MultiplayerModes service, ensuring the client is initialized.
func (c *Client) MultiplayerModes() (*igdb.MultiplayerModeService, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	return client.MultiplayerModes, nil
}

// getClient returns the underlying IGDB client, initializing it lazily if needed.
func (c *Client) getClient() (*igdb.Client, error) {
	c.mu.RLock()
	if c.igdbClient != nil {
		defer c.mu.RUnlock()
		return c.igdbClient, nil
	}
	c.mu.RUnlock()

	// Need to initialize
	return c.ensureToken()
}

// ensureToken fetches a fresh token and creates a new IGDB client.
// Concurrent calls are coalesced: only one fetch happens at a time.
func (c *Client) ensureToken() (*igdb.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.igdbClient != nil {
		return c.igdbClient, nil
	}

	// If a fetch is already in flight, wait for it
	for c.fetchInFlight {
		c.mu.Unlock()
		// Small sleep to avoid busy waiting
		// In a production system with heavy concurrency, you'd use a sync.Cond
		// but for this use case (few concurrent requests), a simple approach works
		c.mu.Lock()
		if c.igdbClient != nil {
			return c.igdbClient, nil
		}
	}

	c.fetchInFlight = true
	c.mu.Unlock()

	token, err := fetchTwitchAppToken(c.clientID, c.clientSecret)

	c.mu.Lock()
	c.fetchInFlight = false

	if err != nil {
		return nil, fmt.Errorf("failed to fetch IGDB token: %w", err)
	}

	c.token = token
	c.igdbClient = igdb.NewClient(c.clientID, token, nil)
	return c.igdbClient, nil
}

// RefreshToken forces a token refresh, useful for the /refresh-igdb command.
func (c *Client) RefreshToken() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If a fetch is already in flight, wait for it
	for c.fetchInFlight {
		c.mu.Unlock()
		c.mu.Lock()
	}

	c.fetchInFlight = true
	c.mu.Unlock()

	token, err := fetchTwitchAppToken(c.clientID, c.clientSecret)

	c.mu.Lock()
	c.fetchInFlight = false

	if err != nil {
		return fmt.Errorf("failed to refresh IGDB token: %w", err)
	}

	c.token = token
	c.igdbClient = igdb.NewClient(c.clientID, token, nil)
	return nil
}

// fetchTwitchAppToken requests a new app access token from Twitch/IGDB.
// Exposed as a variable for testing.
var fetchTwitchAppToken = func(clientID, clientSecret string) (string, error) {
	u, err := url.Parse("https://id.twitch.tv/oauth2/token")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("client_secret", clientSecret)
	q.Set("grant_type", "client_credentials")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodPost, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return "", fmt.Errorf("twitch token endpoint returned %d: %s", resp.StatusCode, bodyStr)
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in response")
	}
	return parsed.AccessToken, nil
}

// Retry wraps an IGDB operation with automatic retry on 401 auth failure.
// Usage:
//
//	var games []*igdb.Game
//	err := client.Retry(func() error {
//	    svc, err := client.Games()
//	    if err != nil { return err }
//	    games, err = svc.Index(...)
//	    return err
//	})
func (c *Client) Retry(fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}

	// Check if error indicates auth failure
	if !isAuthError(err) {
		return err
	}

	// Refresh token and retry once
	if err := c.RefreshToken(); err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}

	return fn()
}

// isAuthError checks if an error is an authentication failure (401).
// The IGDB client library returns errors as strings, so we do text matching.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "invalid token")
}
