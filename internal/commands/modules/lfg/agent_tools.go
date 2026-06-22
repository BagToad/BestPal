package lfg

import (
	"fmt"
	"strings"

	"gamerpal/internal/agentctx"

	"github.com/bwmarrin/discordgo"
	copilot "github.com/github/copilot-sdk/go"
)

type threadInfo struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Created bool   `json:"created,omitempty"`
}

type gameSuggestion struct {
	Name   string `json:"name"`
	IGDBID int    `json:"igdb_id"`
}

type findOrCreateResult struct {
	// Status is one of: found_existing, created_new, needs_disambiguation, no_matches.
	Status      string           `json:"status"`
	Thread      *threadInfo      `json:"thread,omitempty"`
	Suggestions []gameSuggestion `json:"suggestions,omitempty"`
	Note        string           `json:"note,omitempty"`
}

type searchResult struct {
	Threads []threadInfo `json:"threads"`
	Note    string       `json:"note,omitempty"`
}

type gameThreadResult struct {
	GameName string      `json:"game_name"`
	Thread   *threadInfo `json:"thread,omitempty"`
}

type batchSearchResult struct {
	Games        []gameThreadResult `json:"games"`
	MissingGames []string           `json:"missing_games"`
	Note         string             `json:"note,omitempty"`
}

// AgentTools satisfies the duck-typed agentToolProvider in the commands package.
func (m *Module) AgentTools() []copilot.Tool {
	if m == nil || m.session == nil {
		return nil
	}
	return []copilot.Tool{
		m.newLFGSearchTool(),
		m.newLFGBatchSearchTool(),
		m.newLFGFindOrCreateTool(),
	}
}

type lfgSearchParams struct {
	Query string `json:"query" jsonschema:"the game name or partial name to search for in the LFG forum"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of results to return (1-10, default 5)"`
}

func (m *Module) newLFGSearchTool() copilot.Tool {
	t := copilot.DefineTool(
		"lfg_search",
		"Search the GamerPals LFG forum for existing game threads by name. Returns up to N results ordered by relevance. Use this to check whether a thread already exists before creating one.",
		func(p lfgSearchParams, _ copilot.ToolInvocation) (*searchResult, error) {
			limit := p.Limit
			if limit <= 0 || limit > 10 {
				limit = 5
			}
			forumID := m.config.GetGamerPalsLFGForumChannelID()
			if forumID == "" {
				return &searchResult{Note: "lfg forum not configured"}, nil
			}
			hits := m.searchForumThreads(forumID, p.Query, limit)
			if len(hits) == 0 {
				return &searchResult{Note: "no matching threads"}, nil
			}
			out := &searchResult{Threads: make([]threadInfo, 0, len(hits))}
			for _, ch := range hits {
				out.Threads = append(out.Threads, *channelToThreadInfo(ch, false))
			}
			return out, nil
		},
	)
	t.SkipPermission = true
	return t
}

type lfgBatchSearchParams struct {
	GameNames []string `json:"game_names" jsonschema:"list of game names to search for in the LFG forum"`
}

func (m *Module) newLFGBatchSearchTool() copilot.Tool {
	t := copilot.DefineTool(
		"lfg_batch_search",
		"Search the GamerPals LFG forum for multiple game names and return found thread links plus missing games.",
		func(p lfgBatchSearchParams, inv copilot.ToolInvocation) (*batchSearchResult, error) {
			return m.batchSearchGameThreads(p.GameNames, inv.SessionID), nil
		},
	)
	t.SkipPermission = true
	return t
}

type lfgFindOrCreateParams struct {
	GameName string `json:"game_name" jsonschema:"the exact game name to find or create a thread for; prefer the IGDB canonical name"`
}

func (m *Module) newLFGFindOrCreateTool() copilot.Tool {
	t := copilot.DefineTool(
		"lfg_find_or_create_thread",
		`Find an existing LFG forum thread for a game, or create one with IGDB enrichment (cover art, summary, links). If the name is ambiguous, returns IGDB suggestions instead of creating; pick one and call again with that exact name. Status is one of: "found_existing", "created_new", "needs_disambiguation", "no_matches".`,
		func(p lfgFindOrCreateParams, _ copilot.ToolInvocation) (*findOrCreateResult, error) {
			if p.GameName == "" {
				return &findOrCreateResult{Status: "no_matches", Note: "empty game name"}, nil
			}
			if m.igdbClient == nil {
				return nil, fmt.Errorf("igdb client not initialized")
			}
			forumID := m.config.GetGamerPalsLFGForumChannelID()
			if forumID == "" {
				return nil, fmt.Errorf("lfg forum channel id not configured")
			}
			ch, created, suggestions, err := m.lookupOrCreateGameThread(forumID, p.GameName)
			if err != nil {
				return nil, err
			}
			switch {
			case ch != nil && created:
				return &findOrCreateResult{Status: "created_new", Thread: channelToThreadInfo(ch, true)}, nil
			case ch != nil:
				return &findOrCreateResult{Status: "found_existing", Thread: channelToThreadInfo(ch, false)}, nil
			case len(suggestions) > 0:
				sugs := make([]gameSuggestion, 0, len(suggestions))
				for i, g := range suggestions {
					if i >= 8 || g == nil || g.Name == "" {
						continue
					}
					sugs = append(sugs, gameSuggestion{Name: g.Name, IGDBID: g.ID})
				}
				if len(sugs) == 0 {
					return &findOrCreateResult{Status: "no_matches"}, nil
				}
				return &findOrCreateResult{Status: "needs_disambiguation", Suggestions: sugs}, nil
			default:
				return &findOrCreateResult{Status: "no_matches"}, nil
			}
		},
	)
	t.SkipPermission = true
	return t
}

func channelToThreadInfo(ch *discordgo.Channel, created bool) *threadInfo {
	if ch == nil {
		return nil
	}
	return &threadInfo{
		Name:    ch.Name,
		URL:     threadLink(ch),
		Created: created,
	}
}

func (m *Module) batchSearchGameThreads(gameNames []string, sessionID string) *batchSearchResult {
	out := &batchSearchResult{
		Games:        make([]gameThreadResult, 0, len(gameNames)),
		MissingGames: make([]string, 0, len(gameNames)),
	}

	forumID := m.config.GetGamerPalsLFGForumChannelID()
	if forumID == "" {
		out.Note = "lfg forum not configured"
		return out
	}

	for _, rawName := range gameNames {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}

		hits := m.searchForumThreads(forumID, name, 1)
		if len(hits) == 0 {
			out.MissingGames = append(out.MissingGames, name)
			continue
		}
		out.Games = append(out.Games, gameThreadResult{
			GameName: name,
			Thread:   channelToThreadInfo(hits[0], false),
		})
	}

	if len(out.MissingGames) > 0 {
		out.Note = m.missingThreadNoteForSession(sessionID)
	}
	return out
}

func (m *Module) missingThreadNoteForSession(sessionID string) string {
	const fallback = "ℹ️ Missing a thread? Create one in #create-a-thread."

	if m.session == nil || m.session.State == nil {
		return fallback
	}
	caller, ok := agentctx.CallerForSession(sessionID)
	if !ok || caller.GuildID == "" {
		return fallback
	}

	guild, err := m.session.State.Guild(caller.GuildID)
	if err != nil || guild == nil {
		return fallback
	}

	for _, ch := range guild.Channels {
		if ch == nil {
			continue
		}
		if strings.EqualFold(ch.Name, "create-a-thread") {
			return fmt.Sprintf("ℹ️ Missing a thread? Create one in <#%s>.", ch.ID)
		}
	}
	return fallback
}
