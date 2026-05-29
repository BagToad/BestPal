package commands

import (
	"sort"

	copilot "github.com/github/copilot-sdk/go"
)

// agentToolProvider is the optional interface a CommandModule may implement
// to expose Copilot agent tools. Defined privately here so the core
// CommandModule contract does not depend on the Copilot SDK.
type agentToolProvider interface {
	AgentTools() []copilot.Tool
}

// CollectAgentTools returns the union of agent tools from every module that
// implements AgentTools(). Modules are visited in alphabetical order for
// stable tool ordering; duplicate tool names are skipped with a warning.
func (h *ModuleHandler) CollectAgentTools() []copilot.Tool {
	if len(h.modules) == 0 {
		return nil
	}
	names := make([]string, 0, len(h.modules))
	for name := range h.modules {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []copilot.Tool
	seen := make(map[string]string)
	for _, modName := range names {
		tp, ok := h.modules[modName].(agentToolProvider)
		if !ok {
			continue
		}
		for _, tool := range tp.AgentTools() {
			if prev, exists := seen[tool.Name]; exists {
				h.config.Logger.Warnf(
					"agent tools: module %q exposes duplicate tool name %q (already provided by %q); skipping",
					modName, tool.Name, prev,
				)
				continue
			}
			seen[tool.Name] = modName
			out = append(out, tool)
		}
	}
	return out
}
