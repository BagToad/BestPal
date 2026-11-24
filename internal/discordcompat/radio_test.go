package discordcompat

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRadioGroupMarshal(t *testing.T) {
	b, err := json.Marshal(discordgo.Label{Label: "Why?", Component: RadioGroup{CustomID: "r", Required: new(false),
		Options: []RadioOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":18,"label":"Why?","component":{"type":21,"custom_id":"r","required":false,
		"options":[{"value":"a","label":"A"},{"value":"b","label":"B"}]}}`, string(b))
}

const submitted = `{"id":"1","type":5,"guild_id":"g","member":{"user":{"id":"u"}},"data":{"custom_id":"sq:survey:7","components":[
	{"id":1,"type":10},
	{"id":2,"type":18,"component":{"id":3,"type":21,"custom_id":"reason","value":%s}},
	{"id":4,"type":18,"component":{"id":5,"type":4,"custom_id":"feedback","value":"meh"}}]}}`

func value(t *testing.T, ic *discordgo.InteractionCreate, id string) string {
	t.Helper()
	for _, c := range ic.ModalSubmitData().Components {
		if l, ok := c.(*discordgo.Label); ok {
			if ti, ok := l.Component.(*discordgo.TextInput); ok && ti.CustomID == id {
				return ti.Value
			}
		}
	}
	t.Fatalf("no %s", id)
	return ""
}

func TestFixInteraction(t *testing.T) {
	for in, want := range map[string]string{`"no_response"`: "no_response", `null`: ""} {
		raw := []byte(fmt.Sprintf(submitted, in))
		var plain discordgo.InteractionCreate
		require.Error(t, json.Unmarshal(raw, &plain), "discordgo can't decode radio groups yet")

		ic, ok := FixInteraction(&discordgo.Event{Type: "INTERACTION_CREATE", RawData: raw})
		require.True(t, ok)
		assert.Equal(t, discordgo.InteractionModalSubmit, ic.Type)
		assert.Equal(t, "sq:survey:7", ic.ModalSubmitData().CustomID)
		assert.Equal(t, want, value(t, ic, "reason"))
		assert.Equal(t, "meh", value(t, ic, "feedback"))
	}
}

func TestFixInteractionLeavesOthersAlone(t *testing.T) {
	_, ok := FixInteraction(&discordgo.Event{Type: "INTERACTION_CREATE",
		RawData: []byte(`{"id":"1","type":5,"data":{"custom_id":"x","components":[]}}`)})
	assert.False(t, ok, "decodable events are handled by discordgo")
	_, ok = FixInteraction(&discordgo.Event{Type: "MESSAGE_CREATE", RawData: []byte(`{}`)})
	assert.False(t, ok)
}
