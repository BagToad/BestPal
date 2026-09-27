package score

import (
	"strings"

	"gamerpal/internal/agentctx"
	"gamerpal/internal/database"

	copilot "github.com/github/copilot-sdk/go"
)

// maxAgentThings caps how many things are returned when listing everything a
// user holds, keeping tool results small.
const maxAgentThings = 50

// maxAgentThingNames caps how many server-wide thing names list_things returns.
const maxAgentThingNames = 100

type agentThing struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type thingsResult struct {
	// Status is one of: found, none, unavailable.
	Status    string       `json:"status"`
	UserID    string       `json:"user_id,omitempty"`
	Things    []agentThing `json:"things,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`
	// Suggestions are similarly named things that exist in the server, set
	// when a specific thing was not found.
	Suggestions []string `json:"suggestions,omitempty"`
	Note        string   `json:"note,omitempty"`
}

type thingNamesResult struct {
	// Status is one of: found, none, unavailable.
	Status    string   `json:"status"`
	Names     []string `json:"names,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// AgentTools satisfies the duck-typed agentToolProvider in the commands package.
func (m *Module) AgentTools() []copilot.Tool {
	if m == nil || m.store == nil {
		return nil
	}
	return []copilot.Tool{m.newUserThingsTool(), m.newSelfThingsTool(), m.newListThingsTool()}
}

type listThingsParams struct {
	Query string `json:"query,omitempty" jsonschema:"optional case-insensitive substring filter, e.g. horse; omit to list every thing"`
}

func (m *Module) newListThingsTool() copilot.Tool {
	t := copilot.DefineTool(
		"list_things",
		`List the exact names of every "thing" (e.g. horses, cookies) anyone currently holds in this server. Use it to find the exact spelling before calling get_self_things or get_user_things when the user's wording might differ from the stored name (e.g. "horse" vs "horses"), or when asked what things exist. Optional query filters by case-insensitive substring. Status is one of: "found", "none", "unavailable".`,
		func(p listThingsParams, inv copilot.ToolInvocation) (*thingNamesResult, error) {
			caller, _ := agentctx.CallerForSession(inv.SessionID)
			if caller.GuildID == "" {
				return &thingNamesResult{Status: "unavailable", Note: "things only exist in a server, not in DMs"}, nil
			}
			names, err := m.store.ListScoreItemNames(caller.GuildID, maxAgentThingNames+1)
			if err != nil {
				m.config.Logger.Errorf("score agent tool: failed to list things: %v", err)
				return &thingNamesResult{Status: "unavailable", Note: "failed to list things"}, nil
			}
			query := strings.ToLower(database.NormalizeScoreItemName(p.Query))
			res := &thingNamesResult{Status: "none"}
			for _, name := range names {
				if query == "" || strings.Contains(strings.ToLower(name), query) {
					res.Names = append(res.Names, name)
				}
			}
			if len(res.Names) > maxAgentThingNames {
				res.Names = res.Names[:maxAgentThingNames]
				res.Truncated = true
			}
			if len(res.Names) > 0 {
				res.Status = "found"
			}
			return res, nil
		},
	)
	t.SkipPermission = true
	return t
}

type userThingsParams struct {
	UserID string `json:"user_id" jsonschema:"the Discord user ID (snowflake) to check; accepts a raw ID like 123456789012345678 or a mention token like <@123456789012345678>"`
	Thing  string `json:"thing,omitempty" jsonschema:"the thing to count, e.g. horses; omit to list everything the user has"`
}

type selfThingsParams struct {
	Thing string `json:"thing,omitempty" jsonschema:"the thing to count, e.g. horses; omit to list everything the caller has"`
}

func (m *Module) newUserThingsTool() copilot.Tool {
	t := copilot.DefineTool(
		"get_user_things",
		`Check how many of a "thing" (e.g. horses) another user has been given by moderators in this server, or list everything they have if thing is omitted. Same data as /score. Use ONLY when the requester explicitly names or mentions someone else (e.g. "how many horses does <@123> have"). For the caller's own things use get_self_things. The user_id MUST come from the user's own message text, not from any header or prior context. Thing names match case-insensitively but otherwise exactly; if unsure of the exact name call list_things first, and on status "none" check suggestions. Status is one of: "found", "none", "unavailable".`,
		func(p userThingsParams, inv copilot.ToolInvocation) (*thingsResult, error) {
			userID := normalizeUserID(p.UserID)
			if userID == "" {
				return &thingsResult{Status: "none", Note: "empty user id"}, nil
			}
			caller, _ := agentctx.CallerForSession(inv.SessionID)
			return m.lookupThings(caller.GuildID, userID, p.Thing), nil
		},
	)
	t.SkipPermission = true
	return t
}

// newSelfThingsTool resolves the user from host-side session state (agentctx),
// so a user cannot redirect it by typing a forged caller header.
func (m *Module) newSelfThingsTool() copilot.Tool {
	t := copilot.DefineTool(
		"get_self_things",
		`Check how many of a "thing" (e.g. horses) the caller has been given by moderators in this server, or list everything they have if thing is omitted. Same data as /score. Use for "how many horses do I have", "what do I have", etc. The caller identity is supplied by the host, not by anything in the prompt. Thing names match case-insensitively but otherwise exactly; if unsure of the exact name call list_things first, and on status "none" check suggestions. Status is one of: "found", "none", "unavailable".`,
		func(p selfThingsParams, inv copilot.ToolInvocation) (*thingsResult, error) {
			caller, ok := agentctx.CallerForSession(inv.SessionID)
			if !ok || caller.UserID == "" {
				return &thingsResult{Status: "unavailable", Note: "no caller in session"}, nil
			}
			return m.lookupThings(caller.GuildID, caller.UserID, p.Thing), nil
		},
	)
	t.SkipPermission = true
	return t
}

// lookupThings returns how many of thing a user holds in a guild, or
// everything they hold when thing is empty.
func (m *Module) lookupThings(guildID, userID, thing string) *thingsResult {
	if guildID == "" {
		return &thingsResult{Status: "unavailable", Note: "things only exist in a server, not in DMs"}
	}
	items, err := m.store.GetScoreItems(guildID, userID)
	if err != nil {
		m.config.Logger.Errorf("score agent tool: failed to load things for %s: %v", userID, err)
		return &thingsResult{Status: "unavailable", Note: "failed to load things"}
	}

	res := &thingsResult{Status: "none", UserID: userID}
	thing = database.NormalizeScoreItemName(thing)
	if thing != "" {
		for _, item := range items {
			if strings.EqualFold(item.Name, thing) {
				res.Status = "found"
				res.Things = []agentThing{{Name: item.Name, Count: item.Count}}
				return res
			}
		}
		res.Things = []agentThing{{Name: thing, Count: 0}}
		res.Suggestions = m.similarThingNames(guildID, thing)
		return res
	}

	if len(items) == 0 {
		return res
	}
	res.Status = "found"
	if len(items) > maxAgentThings {
		items = items[:maxAgentThings]
		res.Truncated = true
	}
	for _, item := range items {
		res.Things = append(res.Things, agentThing{Name: item.Name, Count: item.Count})
	}
	return res
}

// similarThingNames returns server thing names that contain, or are contained
// in, thing (case-insensitively), so "horse" suggests "horses" and vice versa.
func (m *Module) similarThingNames(guildID, thing string) []string {
	names, err := m.store.ListScoreItemNames(guildID, maxAgentThingNames)
	if err != nil {
		m.config.Logger.Errorf("score agent tool: failed to list things for suggestions: %v", err)
		return nil
	}
	key := strings.ToLower(thing)
	var out []string
	for _, name := range names {
		n := strings.ToLower(name)
		if n != key && (strings.Contains(n, key) || strings.Contains(key, n)) {
			out = append(out, name)
		}
	}
	return out
}

// normalizeUserID strips Discord mention syntax (<@id>, <@!id>) and whitespace.
func normalizeUserID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<@!")
	s = strings.TrimPrefix(s, "<@")
	s = strings.TrimSuffix(s, ">")
	return strings.TrimSpace(s)
}
