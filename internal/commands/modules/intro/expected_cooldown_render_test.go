package intro

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func TestExpectedCooldownResetRendering(t *testing.T) {
	reset := time.Unix(1800000000, 0)
	comment := newAutoIntroComment("guild", "feed")
	original := comment.preamble
	assert.Equal(t, original, withExpectedCooldownReset(original, time.Time{}))
	comment.preamble = withExpectedCooldownReset(original, reset)
	assert.Equal(t, original+"\nExpected cooldown reset: <t:1800000000:R>", comment.components()[0].(discordgo.TextDisplay).Content)
	assert.Len(t, comment.components(), 2)
	assert.Equal(t, original, withExpectedCooldownReset(comment.preamble, time.Time{}))
}

func TestExpectedCooldownResetPreservesDecodedLayout(t *testing.T) {
	for _, results := range []bool{false, true} {
		for _, loading := range []bool{false, true} {
			t.Run(fmt.Sprintf("results=%t/loading=%t", results, loading), func(t *testing.T) {
				message := decodedLookupMessage(t, results)
				setLookupButtonLoading(resolveLookupButton(message.Components), loading)
				before := lookupSnapshot(t, message.Components)
				preamble := message.Components[0].(*discordgo.TextDisplay)
				original := preamble.Content + "\nCustom comment text stays."
				preamble.Content = original
				for _, unix := range []int64{1800000000, 1800003600, 1800007200} {
					preamble.Content = withExpectedCooldownReset(preamble.Content, time.Unix(unix, 0))
					assert.Equal(t, original+fmt.Sprintf("\nExpected cooldown reset: <t:%d:R>", unix), preamble.Content)
					assert.Equal(t, 1, strings.Count(preamble.Content, "Expected cooldown reset: "))
					assert.Same(t, preamble, message.Components[0])
					assert.Equal(t, before.Components[1:], message.Components[1:])
				}
				// Repair duplicate timestamp lines without touching other comment text.
				preamble.Content += "\nExpected cooldown reset: <t:1:R>"
				preamble.Content = withExpectedCooldownReset(preamble.Content, time.Unix(1800007200, 0))
				assert.Equal(t, original+"\nExpected cooldown reset: <t:1800007200:R>", preamble.Content)
			})
		}
	}
}
