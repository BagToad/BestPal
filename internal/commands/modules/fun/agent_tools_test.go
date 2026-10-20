package fun

import (
	"encoding/json"
	"testing"

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
