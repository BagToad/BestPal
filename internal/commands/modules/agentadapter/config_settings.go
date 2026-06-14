package agentadapter

import "gamerpal/internal/config"

// ConfigSettings declares the agent's per-guild settings for the config panel
// and the settings registry. It is static (reads no Module fields), so it works
// on a zero-value Module and is independent of whether the agent runtime
// started. This satisfies config.ConfigProvider, which ModuleHandler discovers
// across the module list.
func (m *Module) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyCopilotAgentRoleID,
			Category:    config.CategoryAgent,
			Label:       "Agent role (inclusion)",
			Description: "When set, only members of this role can use the agent (the exclude role is then ignored).",
			Kind:        config.KindRole,
		},
		{
			Key:         config.KeyCopilotAgentExcludeRoleID,
			Category:    config.CategoryAgent,
			Label:       "Agent role (exclusion)",
			Description: "Honored only when no inclusion role is set: everyone except this role may use the agent.",
			Kind:        config.KindRole,
		},
		{
			Key:         config.KeyCopilotAgentReplyAllowlist,
			Category:    config.CategoryAgent,
			Label:       "Reply channel allowlist",
			Description: "Channels where the agent may reply. Empty means no channel restriction. Inclusion-role members bypass this.",
			Kind:        config.KindChannelList,
		},
		{
			Key:         config.KeyCopilotAgentModel,
			Category:    config.CategoryAgent,
			Label:       "Agent model",
			Description: "Model identifier used for the agent (default gpt-5.5).",
			Kind:        config.KindString,
			Default:     "gpt-5.5",
		},
		{
			Key:         config.KeyCopilotAgentBrainChannelID,
			Category:    config.CategoryAgent,
			Label:       "Brain channel",
			Description: "Private, mod-only channel whose messages are loaded as extra guidance for the agent. Empty disables it. Must hide @everyone or it will be skipped.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyCopilotAgentBrainRefreshInterval,
			Category:    config.CategoryAgent,
			Label:       "Brain refresh interval",
			Description: "How often the brain channel is reloaded, as a Go duration like \"5m\". Defaults to 5m. Applied at startup.",
			Kind:        config.KindDuration,
			Default:     "5m",
		},
		{
			Key:         config.KeyCopilotAgentBrainMaxItems,
			Category:    config.CategoryAgent,
			Label:       "Brain max items",
			Description: "Maximum number of brain channel messages loaded as guidance (newest kept). Defaults to 50.",
			Kind:        config.KindInt,
			Default:     50,
		},
		{
			Key:         config.KeyCopilotAgentBrainMaxChars,
			Category:    config.CategoryAgent,
			Label:       "Brain max characters",
			Description: "Maximum total characters of guidance injected into the prompt. Defaults to 4000.",
			Kind:        config.KindInt,
			Default:     4000,
		},
	}
}
