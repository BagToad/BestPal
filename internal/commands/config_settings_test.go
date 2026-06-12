package commands

import (
	"testing"

	"gamerpal/internal/agent"
	"gamerpal/internal/commands/modules/fun"
	"gamerpal/internal/commands/modules/intro"
	"gamerpal/internal/commands/modules/lfg"
	nineteeneightyfour "gamerpal/internal/commands/modules/nineteeneightyfour"
	"gamerpal/internal/commands/modules/scamguard"
	"gamerpal/internal/commands/modules/welcome"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/stretchr/testify/require"
)

// realProviderHandler builds a ModuleHandler whose modules map holds the real
// config-owning modules (zero-value structs are fine: ConfigSettings reads no
// fields). Used to assert the live registry covers every per-guild key.
func realProviderHandler() *ModuleHandler {
	return &ModuleHandler{
		config: config.NewMockConfig(nil),
		modules: map[string]types.CommandModule{
			"intro":     &intro.IntroModule{},
			"lfg":       &lfg.LfgModule{},
			"welcome":   &welcome.WelcomeModule{},
			"scamguard": &scamguard.Module{},
			"1984":      &nineteeneightyfour.Module{},
			"fun":       &fun.FunModule{},
		},
	}
}

func TestCollectConfigSettingsCoversAllKeys(t *testing.T) {
	h := realProviderHandler()
	reg := h.CollectConfigSettings(agent.ConfigProvider())

	expected := []string{
		config.KeyModActionLogChannelID,
		config.KeyLogChannelID,
		config.KeyPairingCategoryID,
		config.KeyVoiceSyncCategoryID,
		config.KeyHelpDeskChannelID,
		config.KeyEventFeedChannelID,
		config.KeyIntroductionsForumChannelID,
		config.KeyIntroFeedChannelID,
		config.KeyIntroFeedRateLimitHours,
		config.KeyIntroFeedBoosterRateLimit,
		config.KeyLFGForumChannelID,
		config.KeyLFGNowPanelChannelID,
		config.KeyLFGNowRoleID,
		config.KeyLFGNowRoleDuration,
		config.KeyNewPalsSystemEnabled,
		config.KeyNewPalsRoleID,
		config.KeyNewPalsChannelID,
		config.KeyNewPalsKeepRoleDuration,
		config.KeyNewPalsTimeBetweenMsgs,
		config.Key1984LogChannelID,
		config.KeyTranslateLanguage,
		config.KeyScamGuardEnabled,
		config.KeyScamGuardHashThreshold,
		config.KeyScamGuardAction,
		config.KeyScamGuardTimeoutDuration,
		config.KeyScamGuardLogChannelID,
		config.KeyCopilotAgentRoleID,
		config.KeyCopilotAgentExcludeRoleID,
		config.KeyCopilotAgentReplyAllowlist,
		config.KeyCopilotAgentModel,
	}

	require.Len(t, reg.All(), len(expected), "registry size should equal the number of declared keys (no dupes, none missing)")
	for _, key := range expected {
		_, ok := reg.Get(key)
		require.Truef(t, ok, "expected key %q to be registered", key)
	}

	// Every setting must have a category and a known Kind.
	for _, s := range reg.All() {
		require.NotEmptyf(t, s.Category, "setting %q missing category", s.Key)
		require.NotEmptyf(t, s.Kind, "setting %q missing kind", s.Key)
		if s.Kind == config.KindEnum {
			require.NotEmptyf(t, s.EnumOptions, "enum setting %q must have options", s.Key)
		}
	}
}

type dupeProvider struct{}

func (dupeProvider) ConfigSettings() []config.Setting {
	return []config.Setting{{
		Key:      config.KeyModActionLogChannelID, // collides with the core provider
		Category: config.CategoryChannels,
		Label:    "Duplicate",
		Kind:     config.KindChannel,
	}}
}

func TestCollectConfigSettingsDedupesKeys(t *testing.T) {
	h := realProviderHandler()
	base := h.CollectConfigSettings(agent.ConfigProvider())
	withDupe := h.CollectConfigSettings(agent.ConfigProvider(), dupeProvider{})

	require.Len(t, withDupe.All(), len(base.All()), "duplicate key should be skipped, not added")
}
