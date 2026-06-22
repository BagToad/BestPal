package agentengine

import (
	"context"
	"encoding/json"
	_ "embed"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/agentctx"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

//go:embed prompts/sys_prompt.md
var systemPromptRaw string

var systemPrompt = strings.TrimSpace(systemPromptRaw)

const (
	defaultSessionTimeout = 60 * time.Second
	maxDiscordReplyLen    = 1900
)

const componentJSONSystemPrompt = `You are serving an internal bot component request.
Return ONLY a valid JSON object and no extra text.
Do not wrap JSON in markdown code fences.
Schema:
{
  "games": [{"game_name":"string","thread":{"name":"string","url":"string"}}],
  "missing_games": ["string"],
  "note": "string"
}
Rules:
- Always return a JSON object at the top level.
- "games" and "missing_games" must always be present (empty arrays when none).
- Include "note" only when missing games exist; otherwise omit or use empty string.
- Never include prose outside JSON.`

// Agent is the role-gated LLM tool-calling surface. One Agent per process,
// one Copilot SDK session per @mention. Standalone: tools are registered via
// AddTools so the agent package never imports feature modules. If the
// Copilot CLI fails to start, Handle becomes a no-op and the bot degrades
// to its existing emoji-reaction behavior.
type Agent struct {
	cfg     *config.Config
	session *discordgo.Session

	clientMu sync.Mutex
	client   *copilot.Client

	toolsMu sync.Mutex
	tools   []copilot.Tool

	brain          *Brain
	brainRefreshMu sync.Mutex
}

func New(cfg *config.Config, s *discordgo.Session) (*Agent, error) {
	if cfg == nil || s == nil {
		return nil, fmt.Errorf("nil config or session")
	}
	return &Agent{cfg: cfg, session: s, brain: NewBrain()}, nil
}

// AddTools registers tools. Safe to call multiple times; tools added after
// Start are only visible to sessions created after the call.
func (a *Agent) AddTools(tools ...copilot.Tool) {
	if len(tools) == 0 {
		return
	}
	a.toolsMu.Lock()
	a.tools = append(a.tools, tools...)
	a.toolsMu.Unlock()
}

// Start launches the Copilot CLI subprocess. On failure the agent is left
// disabled (client stays nil) and Start returns the underlying error.
func (a *Agent) Start(ctx context.Context) error {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client != nil {
		return nil
	}
	opts := &copilot.ClientOptions{}
	if path := a.cfg.GetCopilotAgentCLIPath(); path != "" {
		opts.Connection = &copilot.StdioConnection{Path: path}
	}
	client := copilot.NewClient(opts)
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("copilot client start: %w", err)
	}
	a.client = client
	return nil
}

func (a *Agent) Stop() {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return
	}
	if err := a.client.Stop(); err != nil {
		a.cfg.Logger.Warnf("agent: copilot client stop: %v", err)
	}
	a.client = nil
}

// UserHasAgentRole reports whether the message author is allowed to invoke the
// LLM tool-calling agent. Gates apply in this order:
//
//  1. Guild gate is absolute (even super admins cannot bypass).
//  2. Super admin bypass: super admins always pass.
//  3. Role gate: inclusion wins when both roles are set; exclusion role
//     applies only when inclusion is unset; neither set means nobody passes.
//  4. Channel allowlist (when non-empty): inclusion-role members bypass;
//     everyone else must @mention from a channel in the list.
func (a *Agent) UserHasAgentRole(m *discordgo.MessageCreate) bool {
	return userHasAgentRole(a.cfg, m)
}

// Handle processes a single @mention. Returns true if the agent took
// ownership (even on error); false if disabled, prompt empty, or
// dependencies missing so the caller can fall back to other handlers.
func (a *Agent) Handle(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	a.clientMu.Lock()
	client := a.client
	a.clientMu.Unlock()
	if client == nil || s == nil || m == nil || m.Message == nil {
		return false
	}

	prompt := stripMention(m.Content, s.State.User.ID)
	if prompt == "" {
		return false
	}

	// Caller identity is carried via host-side state (agentctx), not by
	// injecting a header into the prompt. Prompt-text headers can be
	// spoofed by anything the user types and would let an attacker hijack
	// any tool that read user_id from the model's arguments.
	caller := agentctx.Caller{}
	if m.Author != nil {
		caller.UserID = m.Author.ID
		caller.IsAdmin = utils.IsSuperAdmin(m.Author.ID, a.cfg)
	}
	caller.GuildID = m.GuildID
	caller.ChannelID = m.ChannelID

	ctx, cancel := context.WithTimeout(context.Background(), defaultSessionTimeout)
	defer cancel()

	// Discord's typing indicator lasts ~10s, so re-trigger every 8s.
	// Mirrors the pattern in the fun module.
	typingDone := make(chan struct{})
	go func() {
		_ = s.ChannelTyping(m.ChannelID)
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingDone:
				return
			case <-ticker.C:
				if err := s.ChannelTyping(m.ChannelID); err != nil {
					return
				}
			}
		}
	}()

	reply, err := a.run(ctx, client, prompt, caller)
	close(typingDone)

	if err != nil {
		a.cfg.Logger.Warnf("agent: run failed: %v", err)
		reply = "🐸 Sorry, I could not finish that request. Try again in a moment."
	}
	if len(reply) > maxDiscordReplyLen {
		reply = reply[:maxDiscordReplyLen-3] + "..."
	}
	if reply == "" {
		reply = "🐸 (no response)"
	}
	if _, err := s.ChannelMessageSendReply(m.ChannelID, reply, m.Reference()); err != nil {
		a.cfg.Logger.Warnf("agent: send reply: %v", err)
	}
	return true
}

// HandleComponent runs the agent for an internal component request and returns
// a normalized top-level JSON object string.
func (a *Agent) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, prompt string) (string, error) {
	a.clientMu.Lock()
	client := a.client
	a.clientMu.Unlock()
	if client == nil {
		return "", fmt.Errorf("agent client not started")
	}
	if s == nil || i == nil || i.Interaction == nil {
		return "", fmt.Errorf("missing interaction context")
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	caller := callerFromInteraction(i, a.cfg)
	ctx, cancel := context.WithTimeout(context.Background(), defaultSessionTimeout)
	defer cancel()

	reply, err := a.runWithExtraSystemPrompt(ctx, client, prompt, caller, componentJSONSystemPrompt)
	if err != nil {
		return "", err
	}
	return normalizeJSONObjectReply(reply)
}

func (a *Agent) run(ctx context.Context, client *copilot.Client, prompt string, caller agentctx.Caller) (string, error) {
	return a.runWithExtraSystemPrompt(ctx, client, prompt, caller, "")
}

func (a *Agent) runWithExtraSystemPrompt(ctx context.Context, client *copilot.Client, prompt string, caller agentctx.Caller, extraSystemPrompt string) (string, error) {
	a.toolsMu.Lock()
	tools := append([]copilot.Tool(nil), a.tools...)
	a.toolsMu.Unlock()

	fullSystemPrompt := assembleSystemPrompt(systemPrompt, a.brain.Guidance())
	if strings.TrimSpace(extraSystemPrompt) != "" {
		fullSystemPrompt = strings.TrimSpace(fullSystemPrompt + "\n\n" + strings.TrimSpace(extraSystemPrompt))
	}

	sessionCfg := &copilot.SessionConfig{
		ClientName: "bestpal-agent",
		Model:      a.cfg.GetCopilotAgentModel(),
		Tools:      tools,
		SystemMessage: &copilot.SystemMessageConfig{
			Mode:    "append",
			Content: fullSystemPrompt,
		},
		// Defense in depth: SkipPermission=true on tools + AvailableTools
		// allowlist below should mean we never reach this handler, but if
		// anything slips through (built-in tool, MCP tool, ...) reject it.
		OnPermissionRequest: rejectAllPermissions,
	}
	if len(tools) > 0 {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name
		}
		sessionCfg.AvailableTools = names
	}

	sess, err := client.CreateSession(ctx, sessionCfg)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	agentctx.Register(sess.SessionID, caller)
	defer func() {
		agentctx.Unregister(sess.SessionID)
		_ = sess.Disconnect()
	}()

	event, err := sess.SendAndWait(ctx, copilot.MessageOptions{Prompt: prompt})
	if err != nil {
		return "", fmt.Errorf("send and wait: %w", err)
	}
	if event == nil {
		return "", fmt.Errorf("no assistant message received")
	}
	data, ok := event.Data.(*copilot.AssistantMessageData)
	if !ok || data == nil {
		return "", fmt.Errorf("unexpected event data type %T", event.Data)
	}
	return strings.TrimSpace(data.Content), nil
}

func callerFromInteraction(i *discordgo.InteractionCreate, cfg *config.Config) agentctx.Caller {
	caller := agentctx.Caller{
		GuildID:   i.GuildID,
		ChannelID: i.ChannelID,
	}
	if i.Member != nil && i.Member.User != nil {
		caller.UserID = i.Member.User.ID
		caller.IsAdmin = utils.IsSuperAdmin(caller.UserID, cfg)
		return caller
	}
	if i.User != nil {
		caller.UserID = i.User.ID
		caller.IsAdmin = utils.IsSuperAdmin(caller.UserID, cfg)
	}
	return caller
}

func normalizeJSONObjectReply(reply string) (string, error) {
	trimmed := strings.TrimSpace(reply)
	trimmed = stripReplyCodeFence(trimmed)
	trimmed = strings.TrimSpace(trimmed)

	if !strings.HasPrefix(trimmed, "{") {
		return "", fmt.Errorf("agent reply must be a top-level JSON object")
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("agent reply is not valid JSON")
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return "", fmt.Errorf("agent reply must decode as JSON object: %w", err)
	}

	normalized, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("failed to normalize JSON reply: %w", err)
	}
	return string(normalized), nil
}

func stripReplyCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 3 {
		return s
	}
	if strings.TrimSpace(lines[0]) == "```" || strings.HasPrefix(strings.TrimSpace(lines[0]), "```json") {
		if strings.TrimSpace(lines[len(lines)-1]) == "```" {
			return strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	return s
}

// stripMention removes <@id> and <@!id> tokens for botID and trims
// whitespace. Mentions of other users are left intact.
func stripMention(content, botID string) string {
	if botID == "" {
		return strings.TrimSpace(content)
	}
	out := strings.ReplaceAll(content, fmt.Sprintf("<@%s>", botID), "")
	out = strings.ReplaceAll(out, fmt.Sprintf("<@!%s>", botID), "")
	return strings.TrimSpace(out)
}

// userHasAgentRole reports whether the message author is allowed to invoke
// the LLM tool-calling agent. Gates apply in this order:
//
//  1. Guild gate: must be in the configured guild.
//  2. Super admin bypass: super admins always pass.
//  3. Role gate: inclusion role wins when both are set; exclusion role
//     applies when only it is set; no role configured means nobody passes.
//  4. Channel allowlist gate (when non-empty): inclusion-role members
//     bypass; everyone else must be in an allowlisted channel.
func userHasAgentRole(cfg *config.Config, m *discordgo.MessageCreate) bool {
	if cfg == nil || m == nil {
		return false
	}
	guildID := cfg.GetGamerPalsServerID()
	if guildID == "" || m.GuildID != guildID {
		return false
	}

	if m.Author != nil && utils.IsSuperAdmin(m.Author.ID, cfg) {
		return true
	}

	if m.Member == nil {
		return false
	}

	includeRoleID := cfg.GetCopilotAgentRoleID()
	if includeRoleID != "" {
		if !memberHasRole(m.Member, includeRoleID) {
			return false
		}
	} else if excludeRoleID := cfg.GetCopilotAgentExcludeRoleID(); excludeRoleID != "" {
		if memberHasRole(m.Member, excludeRoleID) {
			return false
		}
	} else {
		return false
	}

	return channelAllowsAgent(cfg, m, includeRoleID)
}

// channelAllowsAgent reports whether the message is in a channel where the
// agent is allowed to reply. When no allowlist is configured everything is
// allowed. Inclusion-role members bypass the check (super admins are
// short-circuited earlier in userHasAgentRole).
func channelAllowsAgent(cfg *config.Config, m *discordgo.MessageCreate, includeRoleID string) bool {
	allowlist := cfg.GetCopilotAgentReplyChannelAllowlist()
	if len(allowlist) == 0 {
		return true
	}
	if includeRoleID != "" && memberHasRole(m.Member, includeRoleID) {
		return true
	}
	return slices.Contains(allowlist, m.ChannelID)
}

func memberHasRole(member *discordgo.Member, roleID string) bool {
	return slices.Contains(member.Roles, roleID)
}

// rejectAllPermissions is a defense-in-depth fallback for the agent session's
// OnPermissionRequest callback. The Copilot CLI normally asks the host
// (us) before invoking a tool that requires confirmation, e.g. built-in
// tools like shell/edit/write, MCP tools, or any custom tool registered
// without SkipPermission=true.
//
// Our session is locked down two other ways:
//
//   - SessionConfig.AvailableTools is set to the exact allowlist of custom
//     tool names we register. The CLI should refuse to even consider any
//     tool outside that list.
//   - Every custom tool we register sets SkipPermission=true, so a permission
//     prompt is never raised for them.
//
// Together that means this callback should never fire. If it does (SDK
// change, allowlist bug, new tool added without SkipPermission, etc.) we
// reject by default rather than silently approving something the user
// would not have approved themselves. The bot has no human-in-the-loop on
// each Discord @mention, so there is no safe interactive fallback. With
// rpc.PermissionDecisionReject the CLI surfaces the rejection to the
// model as a normal tool error, which it can recover from.
func rejectAllPermissions(_ copilot.PermissionRequest, _ copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
	return &rpc.PermissionDecisionReject{}, nil
}
