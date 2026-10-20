package fun

import (
	"encoding/json"
	"testing"

	"gamerpal/internal/commands/types"

	"github.com/bwmarrin/discordgo"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentTools_ExposesCoinflip(t *testing.T) {
	tools := (&Module{}).AgentTools()
	require.Len(t, tools, 1)
	assert.Equal(t, "coinflip", tools[0].Name)
	assert.True(t, tools[0].SkipPermission)
}

func TestCoinflipTool_ReturnsBothFaces(t *testing.T) {
	orig := coinflipIntn
	t.Cleanup(func() { coinflipIntn = orig })

	tool := newCoinflipTool()
	for i, want := range []string{"🫃", "🫄"} {
		coinflipIntn = func(n int) int {
			assert.Equal(t, 2, n)
			return i
		}
		res, err := tool.Handler(copilot.ToolInvocation{ToolName: "coinflip", Arguments: map[string]any{}})
		require.NoError(t, err)
		var out coinflipResult
		require.NoError(t, json.Unmarshal([]byte(res.TextResultForLLM), &out), res.TextResultForLLM)
		assert.Equal(t, want, out.Result)
	}
}

func TestCoinflipTool_RealRandomOnlyReturnsFaces(t *testing.T) {
	tool := newCoinflipTool()
	seen := map[string]bool{}
	for range 200 {
		res, err := tool.Handler(copilot.ToolInvocation{ToolName: "coinflip", Arguments: map[string]any{}})
		require.NoError(t, err)
		var out coinflipResult
		require.NoError(t, json.Unmarshal([]byte(res.TextResultForLLM), &out))
		seen[out.Result] = true
	}
	assert.Equal(t, map[string]bool{"🫃": true, "🫄": true}, seen)
}

func TestCoinflipCommand_Registered(t *testing.T) {
	cmds := map[string]*types.Command{}
	(&Module{}).Register(cmds, nil)
	cmd, ok := cmds["coinflip"]
	require.True(t, ok)
	assert.Equal(t, "coinflip", cmd.ApplicationCommand.Name)
	assert.Nil(t, cmd.ApplicationCommand.DefaultMemberPermissions, "everyone can flip")
	assert.NotNil(t, cmd.HandlerFunc)
}

func TestCoinflipResponse(t *testing.T) {
	resp := coinflipResponse("🫄")
	assert.Equal(t, discordgo.InteractionResponseChannelMessageWithSource, resp.Type)
	assert.Equal(t, "🫄", resp.Data.Content)
	assert.Zero(t, resp.Data.Flags, "result is public")
}
