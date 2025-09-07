package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"gamerpal/internal/utils"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// handleRefreshIGDB refreshes the IGDB access token using the stored client ID and client secret.
// Only usable in bot DM context by super admins.
func (h *SlashHandler) handleRefreshIGDB(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, h.config) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ You do not have permission to use this command.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	// Immediate deferred response (ephemeral) in case HTTP call takes longer.
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	clientID := h.config.GetIGDBClientID()
	secret := h.config.GetIGDBClientSecret()
	if clientID == "" || secret == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ Missing igdb_client_id or igdb_client_secret in configuration.")})
		return
	}

	token, expiresIn, err := fetchTwitchAppToken(clientID, secret)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ Failed to refresh token: %v", err))})
		return
	}

	// Persist new token
	h.config.Set("igdb_client_token", token)

	// Recreate IGDB client with new token so subsequent calls use it
	h.igdbClient = igdb.NewClient(clientID, token, nil)

	msg := fmt.Sprintf("✅ IGDB token refreshed. Stored value updated.\nExpires In: %.2f hours", (time.Duration(expiresIn) * time.Second).Hours())
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(msg)})
}

// fetchTwitchAppToken requests a new app access token from Twitch/IGDB.
func fetchTwitchAppToken(clientID, clientSecret string) (token string, expiresIn int, err error) {
	u, err := url.Parse("https://id.twitch.tv/oauth2/token")
	if err != nil {
		return "", 0, err
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("client_secret", clientSecret)
	q.Set("grant_type", "client_credentials")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodPost, u.String(), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return "", 0, fmt.Errorf("twitch token endpoint returned %d: %s", resp.StatusCode, bodyStr)
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, err
	}
	if parsed.AccessToken == "" {
		return "", 0, fmt.Errorf("empty access_token in response")
	}
	return parsed.AccessToken, parsed.ExpiresIn, nil
}
