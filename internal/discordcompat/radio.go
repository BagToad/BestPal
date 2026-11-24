// Package discordcompat fills gaps in discordgo for newer Discord features.
package discordcompat

import (
	"encoding/json"

	"github.com/bwmarrin/discordgo"
)

// RadioGroupComponent is Discord's modal radio group, which discordgo doesn't
// support yet.
const RadioGroupComponent discordgo.ComponentType = 21

// RadioGroup is a single-choice set of options in a modal. It must be placed
// inside a discordgo.Label.
type RadioGroup struct {
	ID       int           `json:"id,omitempty"`
	CustomID string        `json:"custom_id"`
	Options  []RadioOption `json:"options"`
	Required *bool         `json:"required,omitempty"`
}

// RadioOption is one choice in a RadioGroup.
type RadioOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// Type returns the radio group component type.
func (RadioGroup) Type() discordgo.ComponentType { return RadioGroupComponent }

// MarshalJSON adds the component type.
func (r RadioGroup) MarshalJSON() ([]byte, error) {
	type radioGroup RadioGroup
	return json.Marshal(struct {
		radioGroup
		Type discordgo.ComponentType `json:"type"`
	}{radioGroup(r), r.Type()})
}

// RewriteRadioGroups turns radio groups in a submitted modal's components into
// text inputs carrying the chosen value ("" when none), which discordgo can
// decode. It reports whether anything changed.
func RewriteRadioGroups(raw []byte) ([]byte, bool) {
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		return raw, false
	}
	data, ok := ev["data"].(map[string]any)
	if !ok {
		return raw, false
	}
	if !rewrite(data["components"]) {
		return raw, false
	}
	out, err := json.Marshal(ev)
	if err != nil {
		return raw, false
	}
	return out, true
}

func rewrite(v any) bool {
	changed := false
	switch t := v.(type) {
	case []any:
		for _, c := range t {
			changed = rewrite(c) || changed
		}
	case map[string]any:
		if typ, ok := t["type"].(float64); ok && discordgo.ComponentType(typ) == RadioGroupComponent {
			value, _ := t["value"].(string)
			t["type"] = float64(discordgo.TextInputComponent)
			t["value"] = value
			return true
		}
		changed = rewrite(t["component"]) || changed
		changed = rewrite(t["components"]) || changed
	}
	return changed
}

// FixInteraction decodes an INTERACTION_CREATE whose components discordgo
// couldn't decode on its own because of radio groups. It returns false when
// discordgo can decode the event itself, so it's never handled twice.
func FixInteraction(e *discordgo.Event) (*discordgo.InteractionCreate, bool) {
	if e == nil || e.Type != "INTERACTION_CREATE" {
		return nil, false
	}
	var plain discordgo.InteractionCreate
	if err := json.Unmarshal(e.RawData, &plain); err == nil {
		return nil, false
	}
	fixed, changed := RewriteRadioGroups(e.RawData)
	if !changed {
		return nil, false
	}
	var ic discordgo.InteractionCreate
	if err := json.Unmarshal(fixed, &ic); err != nil || ic.Interaction == nil || ic.Data == nil {
		return nil, false
	}
	return &ic, true
}
