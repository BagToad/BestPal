package agentengine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeJSONObjectReply(t *testing.T) {
	t.Run("accepts plain json object", func(t *testing.T) {
		got, err := normalizeJSONObjectReply(`{"games":[],"missing_games":[]}`)
		require.NoError(t, err)
		assert.JSONEq(t, `{"games":[],"missing_games":[]}`, got)
	})

	t.Run("accepts fenced json and strips fence", func(t *testing.T) {
		got, err := normalizeJSONObjectReply("```json\n{\"games\":[],\"missing_games\":[],\"note\":\"x\"}\n```")
		require.NoError(t, err)
		assert.JSONEq(t, `{"games":[],"missing_games":[],"note":"x"}`, got)
	})

	t.Run("rejects non-object json", func(t *testing.T) {
		_, err := normalizeJSONObjectReply(`[]`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "top-level JSON object")
	})

	t.Run("rejects invalid json", func(t *testing.T) {
		_, err := normalizeJSONObjectReply(`not-json`)
		require.Error(t, err)
	})
}
