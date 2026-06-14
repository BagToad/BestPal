package commands

import (
	"testing"

	"gamerpal/internal/commands/modules/copilotagent"
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
			"intro":        &intro.Module{},
			"lfg":          &lfg.Module{},
			"welcome":      &welcome.Module{},
			"scamguard":    &scamguard.Module{},
			"1984":         &nineteeneightyfour.Module{},
			"fun":          &fun.Module{},
			"copilotagent": &copilotagent.Module{},
		},
	}
}

func TestCollectConfigSettingsCoversAllKeys(t *testing.T) {
	h := realProviderHandler()
	reg := h.CollectConfigSettings()

	expected := []string{
		config.KeyModActionLogChannelID,
		config.KeyLogChannelID,
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
		config.KeyCopilotAgentBrainChannelID,
		config.KeyCopilotAgentBrainRefreshInterval,
		config.KeyCopilotAgentBrainMaxItems,
		config.KeyCopilotAgentBrainMaxChars,
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

// dupeModule is a test-only command module that declares a config key already
// owned by the core provider, to exercise the duplicate-skip path through the
// normal module list (the only way settings are registered).
type dupeModule struct{}

func (dupeModule) Register(map[string]*types.Command, *types.Dependencies) {}
func (dupeModule) Service() types.ModuleService                            { return nil }
func (dupeModule) ConfigSettings() []config.Setting {
	return []config.Setting{{
		Key:      config.KeyModActionLogChannelID, // collides with the core provider
		Category: config.CategoryChannels,
		Label:    "Duplicate",
		Kind:     config.KindChannel,
	}}
}

func TestCollectConfigSettingsDedupesKeys(t *testing.T) {
	base := realProviderHandler()

	withDupe := realProviderHandler()
	withDupe.modules["dupe"] = dupeModule{}

	require.Len(t, withDupe.CollectConfigSettings().All(), len(base.CollectConfigSettings().All()),
		"duplicate key should be skipped, not added")
}
