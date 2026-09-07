package intro

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamerpal/internal/agentengine"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/database"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round-trip the original builder through Discord's message decoder so the
// fixture has the pointer component types received in real interactions.
func decodedLookupMessage(t *testing.T, withResults bool) *discordgo.Message {
	t.Helper()
	comment := newAutoIntroComment("original-guild", "original-feed")
	if withResults {
		comment.gameThreads = []GameThread{{Name: "Old game", URL: "https://discord.com/channels/original/old"}}
	}
	data, err := json.Marshal(&discordgo.MessageSend{Components: comment.components()})
	require.NoError(t, err)
	var message discordgo.Message
	require.NoError(t, json.Unmarshal(data, &message))
	row := message.Components[1].(*discordgo.ActionsRow)
	row.ID = 22
	button := row.Components[0].(*discordgo.Button)
	button.ID = 33
	button.Style = discordgo.SecondaryButton
	return &message
}

func lookupSnapshot(t *testing.T, components []discordgo.MessageComponent) *discordgo.Message {
	t.Helper()
	data, err := json.Marshal(&discordgo.MessageSend{Components: components})
	require.NoError(t, err)
	var message discordgo.Message
	require.NoError(t, json.Unmarshal(data, &message))
	return &message
}

func assertLookupState(t *testing.T, want, got *discordgo.Message, loading bool) {
	t.Helper()
	expected := lookupSnapshot(t, want.Components)
	button := expected.Components[1].(*discordgo.ActionsRow).Components[0].(*discordgo.Button)
	button.Disabled = loading
	button.Label = lookupButtonDefaultLabel
	if loading {
		button.Label = lookupButtonLoadingLabel
	}
	assert.Equal(t, expected.Components, got.Components)
}

type lookupComponentTransport func(*http.Request) (*http.Response, error)

func (f lookupComponentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type lookupComponentAgent struct {
	reply string
	calls int
}

func (a *lookupComponentAgent) HandleInternal(_ agentengine.HandleInternalOptions) string {
	a.calls++
	return a.reply
}

func TestLookupComponentPreservation(t *testing.T) {
	for _, tc := range []struct {
		name, reply, failure               string
		existing, secondClick, editFailure bool
	}{
		{name: "append new results", reply: `{"game-threads":[{"name":"Portal","url":"https://discord.com/channels/g/new"}]}`},
		{name: "replace existing results", existing: true, reply: `{"game-threads":[{"name":"Portal","url":"https://discord.com/channels/g/new"}]}`},
		{name: "append empty results", reply: `{"game-threads":[]}`},
		{name: "replace with empty results", existing: true, reply: `{"game-threads":[]}`},
		{name: "unchanged second click retains successful results", existing: true, secondClick: true, reply: `{"game-threads":[{"name":"Portal","url":"https://discord.com/channels/g/new"}]}`},
		{name: "starter fetch failure", existing: true, failure: "fetch"},
		{name: "missing author ID", existing: true, failure: "author"},
		{name: "invalid starter snowflake", existing: true, failure: "snowflake"},
		{name: "eligibility database failure", existing: true, failure: "database"},
		{name: "empty agent reply", existing: true, failure: "empty"},
		{name: "invalid agent JSON", existing: true, failure: "json", reply: "not JSON"},
		{name: "failed reset edit still sends followup", existing: true, failure: "empty", editFailure: true},
		{name: "failed result edit still records execution", existing: true, editFailure: true, reply: `{"game-threads":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := forumcache.NewTestForumCache(map[string]any{"intro_feed_channel_id": "changed-feed"})
			db, err := database.NewDB(filepath.Join(t.TempDir(), "lookup.db"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			if tc.failure == "database" {
				require.NoError(t, db.Close())
			}
			const thread = "1500000000000000000"
			s, err := discordgo.New("")
			require.NoError(t, err)
			s.MaxRestRetries = 0
			agent := &lookupComponentAgent{reply: tc.reply}
			deps := &types.Dependencies{Config: cfg, DB: db, Session: s, Agent: agent}
			module := New(deps)
			module.Register(map[string]*types.Command{}, deps)
			var loading, final *discordgo.Message
			var followup discordgo.WebhookParams
			var events []string
			s.Client = &http.Client{Transport: lookupComponentTransport(func(r *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, `{"id":"helper"}`
				switch {
				case r.Method == http.MethodGet:
					events = append(events, "starter")
					starter := &discordgo.Message{ID: thread, Author: &discordgo.User{ID: "author"}}
					if tc.failure == "author" {
						starter.Author.ID = ""
					}
					if tc.failure == "snowflake" {
						starter.ID = "invalid"
					}
					data, err := json.Marshal(starter)
					require.NoError(t, err)
					body = string(data)
					if tc.failure == "fetch" {
						status, body = http.StatusForbidden, `{"message":"denied","code":50013}`
					}
				case strings.HasSuffix(r.URL.Path, "/callback"):
					events = append(events, "loading")
					var response struct {
						Type discordgo.InteractionResponseType `json:"type"`
						Data discordgo.Message                 `json:"data"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&response))
					require.Equal(t, discordgo.InteractionResponseUpdateMessage, response.Type)
					require.Equal(t, discordgo.MessageFlagsIsComponentsV2, response.Data.Flags)
					loading = &response.Data
				case r.Method == http.MethodPatch:
					events = append(events, "edit")
					final = &discordgo.Message{}
					require.NoError(t, json.NewDecoder(r.Body).Decode(final))
					if tc.editFailure {
						status, body = http.StatusForbidden, `{"message":"denied","code":50013}`
					}
				case r.Method == http.MethodPost:
					events = append(events, "followup")
					require.NoError(t, json.NewDecoder(r.Body).Decode(&followup))
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			message := decodedLookupMessage(t, tc.existing)
			before := lookupSnapshot(t, message.Components)
			i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: "interaction", AppID: "app", GuildID: "guild", ChannelID: thread, Message: message, Type: discordgo.InteractionMessageComponent, Data: discordgo.MessageComponentInteractionData{CustomID: LookupGameThreadsCustomID}, Member: &discordgo.Member{User: &discordgo.User{ID: "clicker"}}}}
			module.HandleComponent(s, i)
			require.NotNil(t, loading)
			require.NotNil(t, final)
			assertLookupState(t, before, loading, true)
			if tc.failure != "" {
				assertLookupState(t, before, final, false)
				assert.Equal(t, []string{"loading", "starter", "edit", "followup"}, events)
				assert.Contains(t, followup.Content, "Please try again")
				assert.Equal(t, discordgo.MessageFlagsEphemeral, followup.Flags)
				wantCalls := 0
				if tc.failure == "empty" || tc.failure == "json" {
					wantCalls = 1
				}
				assert.Equal(t, wantCalls, agent.calls)
			} else {
				assert.Equal(t, []string{"loading", "starter", "edit"}, events)
				require.Equal(t, 1, agent.calls)
				expected := lookupSnapshot(t, before.Components)
				var result GameThreadsAgentResult
				require.NoError(t, json.Unmarshal([]byte(tc.reply), &result))
				content := formatGameThreads(result.GameThreads)
				if tc.existing {
					expected.Components[2].(*discordgo.TextDisplay).Content = content
				} else {
					expected.Components = append(expected.Components, &discordgo.TextDisplay{Content: content})
				}
				assertLookupState(t, expected, final, false)
			}
			if tc.failure != "database" {
				eligible, _, err := db.IsIntroEligibleForGameThreadsLookup(thread, time.Unix(1, 0))
				require.NoError(t, err)
				assert.Equal(t, tc.failure != "", eligible)
			}
			if tc.secondClick {
				// The second interaction carries Discord's decoded successful message.
				i.Message = lookupSnapshot(t, final.Components)
				beforeSecond := lookupSnapshot(t, i.Message.Components)
				events = nil
				module.HandleComponent(s, i)
				assert.Equal(t, 1, agent.calls, "unchanged intro must not call the agent again")
				assert.Equal(t, []string{"loading", "starter", "edit", "followup"}, events)
				assertLookupState(t, beforeSecond, loading, true)
				assertLookupState(t, beforeSecond, final, false)
				assert.Contains(t, followup.Content, "no changes")
				assert.Equal(t, discordgo.MessageFlagsEphemeral, followup.Flags)
			}
		})
	}
}
