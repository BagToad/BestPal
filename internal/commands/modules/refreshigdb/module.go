package refreshigdb

import (
	"encoding/json"
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the refresh-igdb command
type RefreshigdbModule struct {
	config     *config.Config
	igdbClient **igdb.Client // Pointer to the client pointer so we can update it
}

// New creates a new refresh-igdb module
func New() *RefreshigdbModule {
	return &RefreshigdbModule{}
}

// Register adds the refresh-igdb command to the command map
func (m *RefreshigdbModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config

	var adminPerms int64 = discordgo.PermissionAdministrator

	cmds["refresh-igdb"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "refresh-igdb",
			Description:              "Refresh the IGDB access token (SuperAdmin only)",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM, discordgo.InteractionContextPrivateChannel},
		},
		HandlerFunc: m.handleRefreshIGDB,
	}
}

// SetIGDBClientRef sets a reference to the IGDB client pointer
func (m *RefreshigdbModule) SetIGDBClientRef(client **igdb.Client) {
	m.igdbClient = client
}

// handleRefreshIGDB refreshes the IGDB access token using the stored client ID and client secret.
// Only usable in bot DM context by super admins.
func (m *RefreshigdbModule) handleRefreshIGDB(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, m.config) {
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

	clientID := m.config.GetIGDBClientID()
	secret := m.config.GetIGDBClientSecret()
	if clientID == "" || secret == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ Missing igdb_client_id or igdb_client_secret in configuration.")})
		return
	}

	token, expiresIn, err := m.fetchTwitchAppToken(clientID, secret)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ Failed to refresh token: %v", err))})
		return
	}

	// Persist new token
	m.config.Set("igdb_client_token", token)

	// Recreate IGDB client with new token if we have a reference
	if m.igdbClient != nil {
		*m.igdbClient = igdb.NewClient(clientID, token, nil)
	}

	msg := fmt.Sprintf("✅ IGDB token refreshed. Stored value updated.\nExpires In: %.2f hours", (time.Duration(expiresIn) * time.Second).Hours())
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(msg)})
}

// fetchTwitchAppToken requests a new app access token from Twitch/IGDB.
func (m *RefreshigdbModule) fetchTwitchAppToken(clientID, clientSecret string) (token string, expiresIn int, err error) {
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
