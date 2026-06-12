package agent

import "gamerpal/internal/config"

// agentConfigProvider exposes the agent's gating settings to the config panel
// registry. The agent is not a command module, so bot.go passes this provider
// to ModuleHandler.CollectConfigSettings explicitly.
type agentConfigProvider struct{}

// ConfigProvider returns a config.ConfigProvider for the agent's settings.
func ConfigProvider() config.ConfigProvider { return agentConfigProvider{} }

func (agentConfigProvider) ConfigSettings() []config.Setting {
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
	}
}
