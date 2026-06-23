package agentadapter

import (
	"gamerpal/internal/agentengine"
	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"
)

// Module adapts the LLM tool-calling agent (internal/agentengine) to the command
// module system, so its recurring tasks and per-guild settings register through
// the same scaffolding as every other module instead of being wired into bot.go
// as special cases. The agent's runtime logic lives in internal/agentengine; this
// is a thin adapter that owns construction and exposes the instance to the bot.
type Module struct {
	agent *agentengine.Agent
}

// New wraps the agent provided by the parent bot wiring as a module.
// The adapter no longer constructs its own Agent.
func New(deps *types.Dependencies) *Module {
	if deps != nil && deps.Agent != nil {
		if ag, ok := deps.Agent.(*agentengine.Agent); ok {
			return &Module{agent: ag}
		}
		if deps.Config != nil && deps.Config.Logger != nil {
			deps.Config.Logger.Warn("agentadapter: deps.Agent is not *agentengine.Agent; agent disabled")
		}
	}
	return &Module{}
}

// Register adds no slash commands. The agent is invoked by @mention, handled in
// the message-create event, not through the command router.
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {}

// Service returns the agent's scheduled-task service, or nil when the agent is
// disabled (construction failed).
func (m *Module) Service() types.ModuleService {
	if m.agent == nil {
		return nil
	}
	return &service{agent: m.agent}
}

// Agent exposes the constructed agent for the cross-cutting wiring that bot.go
// still owns: injecting other modules' tools, @mention handling, and the Copilot
// CLI lifecycle. Returns nil when the agent is disabled.
func (m *Module) Agent() *agentengine.Agent { return m.agent }

// service adapts the agent to types.ModuleService. Hydration triggers the
// one-time startup brain preload; the periodic brain refresh is contributed to
// the scheduler via ScheduledFuncs.
type service struct {
	agent *agentengine.Agent
}

// HydrateServiceDiscordSession fires the agent's best-effort initial brain load
// so guidance is present shortly after startup, without waiting a full refresh
// interval. The agent already holds the session from construction, so there is
// nothing to hydrate here; this just triggers the one-time preload at the right
// point in the lifecycle (after the Discord session is established).
func (s *service) HydrateServiceDiscordSession(*discordgo.Session) error {
	s.agent.PreloadBrain()
	return nil
}

func (s *service) ScheduledFuncs() map[string]func() error { return s.agent.ScheduledFuncs() }
