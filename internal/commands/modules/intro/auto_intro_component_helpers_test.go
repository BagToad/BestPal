package intro

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveLookupButtonAndLoadingPreserveComponents(t *testing.T) {
	for _, existing := range []bool{false, true} {
		message := decodedLookupMessage(t, existing)
		before := lookupSnapshot(t, message.Components)
		preamble := message.Components[0]
		row := message.Components[1].(*discordgo.ActionsRow)
		button := resolveLookupButton(message.Components)
		require.Same(t, row.Components[0], button)
		for _, loading := range []bool{true, false} {
			setLookupButtonLoading(button, loading)
			assert.Same(t, preamble, message.Components[0])
			assert.Same(t, row, message.Components[1])
			assert.Same(t, button, row.Components[0])
			assertLookupState(t, before, message, loading)
		}
	}
}

func TestUpdateGameThreadsPreservesOtherComponents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
		threads  []GameThread
	}{
		{name: "append two components", threads: []GameThread{{Name: "New", URL: "https://discord.com/channels/g/new"}}},
		{name: "replace third component", existing: true, threads: []GameThread{{Name: "New", URL: "https://discord.com/channels/g/new"}}},
		{name: "append no results"},
		{name: "replace with no results", existing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := decodedLookupMessage(t, tc.existing)
			preamble, row := message.Components[0], message.Components[1]
			before := lookupSnapshot(t, message.Components)
			var result discordgo.MessageComponent
			if tc.existing {
				result = message.Components[2]
			}
			updateGameThreads(message, tc.threads)
			require.Len(t, message.Components, 3)
			assert.Same(t, preamble, message.Components[0])
			assert.Same(t, row, message.Components[1])
			assert.Equal(t, before.Components[:2], message.Components[:2])
			if tc.existing {
				assert.Same(t, result, message.Components[2])
			}
			assert.Equal(t, formatGameThreads(tc.threads), message.Components[2].(*discordgo.TextDisplay).Content)
			if len(tc.threads) == 0 {
				assert.Contains(t, message.Components[2].(*discordgo.TextDisplay).Content, "No matching game threads found.")
			}
		})
	}
}
