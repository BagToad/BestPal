package config

import (
	"fmt"
	"gamerpal/internal/config"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// customID scheme for the config panel. The router in HandleComponent /
// HandleModalSubmit dispatches on these prefixes.
const (
	customIDPrefix = "config:"
	actHome        = "home"
	actCategory    = "cat"
	actPick        = "pick"
	actToggle      = "toggle"
	actEdit        = "edit"
	actModal       = "modal"
)

// Discord limits that still constrain the panel: max 5 buttons per action row,
// max 5 text inputs per modal, and (Components V2) max 40 components per
// message. Category panels render one labeled select per row, so they stay well
// within these.
const maxButtonsPerRow = 5

// panelColor is the accent stripe for the panel container (Discord blurple),
// replacing the embed color the panel used before moving to Components V2.
const panelColor = 0x5865F2

// pickID/categoryID/etc. build the namespaced customIDs.
func pickID(key string) string    { return customIDPrefix + actPick + ":" + key }
func toggleID(key string) string  { return customIDPrefix + actToggle + ":" + key }
func categoryID(c string) string  { return customIDPrefix + actCategory + ":" + c }
func editID(c string) string      { return customIDPrefix + actEdit + ":" + c }
func editModalID(c string) string { return customIDPrefix + actModal + ":" + c }

// renderHome builds the ephemeral home panel: a status summary plus a button
// per registered category. Built with Components V2 (a Container of TextDisplay
// + button rows) so it stays consistent with the category panels, which need V2
// to label each select.
func (m *Module) renderHome(guildID string) *discordgo.InteractionResponseData {
	reg := m.config.Registry()
	cats := reg.Categories()
	if len(cats) == 0 {
		return &discordgo.InteractionResponseData{
			Content: "⚠️ No configurable settings are registered.",
			Flags:   discordgo.MessageFlagsEphemeral,
		}
	}

	gc := m.config.ForGuild(guildID)

	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: "## ⚙️ Server Configuration\n" +
			"Pick a category to edit.\n\n" +
			m.statusLine(gc)},
	}

	var btns []discordgo.MessageComponent
	for _, c := range cats {
		btns = append(btns, discordgo.Button{
			Label:    string(c),
			Style:    discordgo.SecondaryButton,
			CustomID: categoryID(string(c)),
		})
		if len(btns) == maxButtonsPerRow {
			inner = append(inner, discordgo.ActionsRow{Components: btns})
			btns = nil
		}
	}
	if len(btns) > 0 {
		inner = append(inner, discordgo.ActionsRow{Components: btns})
	}

	return &discordgo.InteractionResponseData{
		Flags: discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsIsComponentsV2,
		Components: []discordgo.MessageComponent{
			discordgo.Container{AccentColor: new(panelColor), Components: inner},
		},
	}
}

// statusLine produces a one-line live-status summary for the home panel.
func (m *Module) statusLine(gc *config.GuildConfig) string {
	return fmt.Sprintf("**ScamGuard:** %s\u2002·\u2002**New Pals:** %s\u2002·\u2002**Agent:** %s",
		onOff(effBool(gc, config.KeyScamGuardEnabled)),
		onOff(effBool(gc, config.KeyNewPalsSystemEnabled)),
		m.agentStatus(gc),
	)
}

// agentStatus summarizes the agent gating in one line and flags the lockout
// footgun (allowlist set with no role gate).
func (m *Module) agentStatus(gc *config.GuildConfig) string {
	role := effRaw(gc, config.KeyCopilotAgentRoleID) != ""
	exclude := effRaw(gc, config.KeyCopilotAgentExcludeRoleID) != ""
	allowlist := effRaw(gc, config.KeyCopilotAgentReplyAllowlist) != ""
	if allowlist && !role && !exclude {
		return "⚠️ locked out"
	}
	if !role && !exclude {
		return "closed"
	}
	return "gated"
}

// updateToHome re-renders the home panel in place.
func (m *Module) updateToHome(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := m.renderHome(i.GuildID)
	m.updateMessage(s, i, data)
}

// updateToCategory renders a category panel in place.
func (m *Module) updateToCategory(s *discordgo.Session, i *discordgo.InteractionCreate, catStr string) {
	data := m.renderCategory(i.GuildID, config.Category(catStr), "")
	m.updateMessage(s, i, data)
}

// renderCategory builds the panel for one category. Each setting renders as a
// TextDisplay label (so you can tell which setting a control changes) followed
// by its control: a select for picker settings, a toggle button for bools, or
// nothing inline for modal-entered numeric/text settings (edited via the single
// "Edit values…" button). An optional note shows save feedback. Built with
// Components V2 because classic action rows have no per-select label.
func (m *Module) renderCategory(guildID string, cat config.Category, note string) *discordgo.InteractionResponseData {
	reg := m.config.Registry()
	settings := reg.ByCategory(cat)
	gc := m.config.ForGuild(guildID)

	header := "## ⚙️ " + string(cat)
	if note != "" {
		header += "\n" + note
	}
	inner := []discordgo.MessageComponent{discordgo.TextDisplay{Content: header}}

	hasModalFields := false
	for _, st := range settings {
		raw, overridden := effectiveRaw(gc, st)

		label := fmt.Sprintf("**%s:** %s", st.Label, formatValue(st, raw))
		switch {
		case st.Description != "" && overridden:
			label += "\n-# " + st.Description + " · customized"
		case st.Description != "":
			label += "\n-# " + st.Description
		case overridden:
			label += "\n-# customized"
		}
		inner = append(inner, discordgo.TextDisplay{Content: label})

		switch {
		case isSelectKind(st.Kind):
			inner = append(inner, discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{buildSelect(st, raw)},
			})
		case st.Kind == config.KindBool:
			inner = append(inner, discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{discordgo.Button{
					Label:    toggleLabel(st.Label, effBool(gc, st.Key)),
					Style:    toggleStyle(effBool(gc, st.Key)),
					CustomID: toggleID(st.Key),
				}},
			})
		default:
			hasModalFields = true
		}

		inner = append(inner, discordgo.Separator{})
	}

	if cat == config.CategoryAgent {
		if w := m.agentWarning(gc); w != "" {
			inner = append(inner, discordgo.TextDisplay{Content: w})
		}
	}

	var navBtns []discordgo.MessageComponent
	if hasModalFields {
		navBtns = append(navBtns, discordgo.Button{
			Label:    "Edit values…",
			Style:    discordgo.PrimaryButton,
			CustomID: editID(string(cat)),
		})
	}
	navBtns = append(navBtns, discordgo.Button{
		Label:    "⬅ Back",
		Style:    discordgo.SecondaryButton,
		CustomID: customIDPrefix + actHome,
	})
	inner = append(inner, discordgo.ActionsRow{Components: navBtns})

	return &discordgo.InteractionResponseData{
		Flags: discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsIsComponentsV2,
		Components: []discordgo.MessageComponent{
			discordgo.Container{AccentColor: new(panelColor), Components: inner},
		},
	}
}

// agentWarning returns the lockout-footgun warning text, or "" if safe.
func (m *Module) agentWarning(gc *config.GuildConfig) string {
	role := effRaw(gc, config.KeyCopilotAgentRoleID) != ""
	exclude := effRaw(gc, config.KeyCopilotAgentExcludeRoleID) != ""
	allowlist := effRaw(gc, config.KeyCopilotAgentReplyAllowlist) != ""
	if allowlist && !role && !exclude {
		return "⚠️ **Lockout warning:** a channel allowlist is set but no inclusion or exclusion role is configured. The role gate runs first, so only super admins can use the agent. Set an inclusion or exclusion role to open it up."
	}
	return ""
}

// handlePick persists a select choice (or clears it when nothing is selected).
func (m *Module) handlePick(s *discordgo.Session, i *discordgo.InteractionCreate, key string) {
	reg := m.config.Registry()
	st, ok := reg.Get(key)
	if !ok {
		respondEphemeral(s, i, "❌ Unknown setting. Run /config again.")
		return
	}
	gc := m.config.ForGuild(i.GuildID)
	vals := i.MessageComponentData().Values
	var note string
	if len(vals) == 0 {
		if err := gc.ClearOverride(key); err != nil {
			m.config.Logger.Warnf("config panel: clear %s: %v", key, err)
			note = "❌ Could not clear " + st.Label
		} else {
			note = "✅ Cleared " + st.Label
		}
	} else {
		raw := vals[0]
		if st.Kind == config.KindChannelList {
			raw = strings.Join(vals, ",")
		}
		if err := gc.SetOverride(key, raw, interactionUserID(i)); err != nil {
			m.config.Logger.Warnf("config panel: set %s: %v", key, err)
			note = "❌ Could not save " + st.Label
		} else {
			note = "✅ Saved " + st.Label
		}
	}
	m.updateMessage(s, i, m.renderCategory(i.GuildID, st.Category, note))
}

// handleToggle flips a boolean setting and saves it.
func (m *Module) handleToggle(s *discordgo.Session, i *discordgo.InteractionCreate, key string) {
	reg := m.config.Registry()
	st, ok := reg.Get(key)
	if !ok {
		respondEphemeral(s, i, "❌ Unknown setting. Run /config again.")
		return
	}
	gc := m.config.ForGuild(i.GuildID)
	next := !effBool(gc, key)
	note := "✅ " + st.Label + " " + onOff(next)
	if err := gc.SetOverride(key, strconv.FormatBool(next), interactionUserID(i)); err != nil {
		m.config.Logger.Warnf("config panel: toggle %s: %v", key, err)
		note = "❌ Could not update " + st.Label
	}
	m.updateMessage(s, i, m.renderCategory(i.GuildID, st.Category, note))
}

// handleOpenEditModal opens a modal holding every modal-entered setting in the
// category (int/duration/string), prefilled with current values.
func (m *Module) handleOpenEditModal(s *discordgo.Session, i *discordgo.InteractionCreate, catStr string) {
	cat := config.Category(catStr)
	reg := m.config.Registry()
	gc := m.config.ForGuild(i.GuildID)

	var rows []discordgo.MessageComponent
	for _, st := range reg.ByCategory(cat) {
		if isSelectKind(st.Kind) || st.Kind == config.KindBool {
			continue
		}
		raw, _ := effectiveRaw(gc, st)
		rows = append(rows, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{&discordgo.TextInput{
				CustomID:    st.Key,
				Label:       truncate(st.Label, 45),
				Style:       discordgo.TextInputShort,
				Value:       raw,
				Placeholder: placeholderFor(st),
				Required:    false,
				MaxLength:   200,
			}},
		})
	}
	if len(rows) == 0 {
		respondEphemeral(s, i, "Nothing to edit here.")
		return
	}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID:   editModalID(catStr),
			Title:      truncate("Edit "+catStr, 45),
			Components: rows,
		},
	})
}

// handleEditModalSubmit validates and persists modal values, then re-renders the
// category. Blank inputs clear the override; invalid inputs are reported and the
// previous value is kept for that field.
func (m *Module) handleEditModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate, catStr string) {
	cat := config.Category(catStr)
	reg := m.config.Registry()
	gc := m.config.ForGuild(i.GuildID)
	userID := interactionUserID(i)

	var saved, errs []string
	for _, comp := range i.ModalSubmitData().Components {
		var row *discordgo.ActionsRow
		switch v := comp.(type) {
		case discordgo.ActionsRow:
			row = &v
		case *discordgo.ActionsRow:
			row = v
		default:
			continue
		}
		for _, inner := range row.Components {
			ti, ok := inner.(*discordgo.TextInput)
			if !ok {
				continue
			}
			st, ok := reg.Get(ti.CustomID)
			if !ok {
				continue
			}
			raw := strings.TrimSpace(ti.Value)
			if raw == "" {
				if err := gc.ClearOverride(st.Key); err != nil {
					m.config.Logger.Warnf("config panel: clear %s: %v", st.Key, err)
				}
				continue
			}
			if err := config.ValidateValue(st, raw); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %s", st.Label, err.Error()))
				continue
			}
			if err := gc.SetOverride(st.Key, raw, userID); err != nil {
				m.config.Logger.Warnf("config panel: set %s: %v", st.Key, err)
				errs = append(errs, fmt.Sprintf("%s: could not save", st.Label))
				continue
			}
			saved = append(saved, st.Label)
		}
	}

	note := "✅ Saved"
	if len(saved) > 0 {
		note = "✅ Saved: " + strings.Join(saved, ", ")
	}
	if len(errs) > 0 {
		note += "\n⚠️ " + strings.Join(errs, "; ")
	}
	m.updateMessage(s, i, m.renderCategory(i.GuildID, cat, note))
}

// updateMessage edits the originating panel message in place.
func (m *Module) updateMessage(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: data,
	}); err != nil {
		m.config.Logger.Warnf("config panel: update message: %v", err)
	}
}

// ---- value helpers ----

// effectiveRaw returns the value in effect for a setting (override else
// env/default) and whether a per-guild override is set.
func effectiveRaw(gc *config.GuildConfig, st config.Setting) (string, bool) {
	if ov, ok := gc.OverrideValue(st.Key); ok {
		return ov, true
	}
	v := gc.EnvString(st.Key)
	if v == "" && st.Default != nil {
		v = fmt.Sprint(st.Default)
	}
	return v, false
}

// effRaw is effectiveRaw by key, ignoring the override flag.
func effRaw(gc *config.GuildConfig, key string) string {
	if ov, ok := gc.OverrideValue(key); ok {
		return ov
	}
	return gc.EnvString(key)
}

// effBool resolves a boolean setting in effect.
func effBool(gc *config.GuildConfig, key string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(effRaw(gc, key)))
	return b
}

// formatValue renders a setting's current value for display in an embed.
func formatValue(st config.Setting, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "_not set_"
	}
	switch st.Kind {
	case config.KindChannel, config.KindCategory:
		return "<#" + raw + ">"
	case config.KindRole:
		return "<@&" + raw + ">"
	case config.KindChannelList:
		parts := splitCSV(raw)
		for idx, p := range parts {
			parts[idx] = "<#" + p + ">"
		}
		return strings.Join(parts, ", ")
	case config.KindBool:
		b, _ := strconv.ParseBool(raw)
		return onOff(b)
	case config.KindEnum:
		for _, o := range st.EnumOptions {
			if o.Value == raw {
				return o.Label
			}
		}
		return raw
	default:
		return "`" + raw + "`"
	}
}

// buildSelect returns the select component for a picker-kind setting, with the
// current value pre-selected. MinValues 0 lets the admin deselect to clear.
func buildSelect(st config.Setting, raw string) discordgo.MessageComponent {
	switch st.Kind {
	case config.KindRole:
		sm := discordgo.SelectMenu{
			MenuType:    discordgo.RoleSelectMenu,
			CustomID:    pickID(st.Key),
			Placeholder: "Select a role (deselect to clear)",
			MinValues:   new(0),
			MaxValues:   1,
		}
		if raw != "" {
			sm.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: raw, Type: discordgo.SelectMenuDefaultValueRole}}
		}
		return sm
	case config.KindChannelList:
		ids := splitCSV(raw)
		sm := discordgo.SelectMenu{
			MenuType:     discordgo.ChannelSelectMenu,
			CustomID:     pickID(st.Key),
			Placeholder:  "Select channels (deselect all to clear)",
			MinValues:    new(0),
			MaxValues:    25,
			ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews},
		}
		for _, id := range ids {
			sm.DefaultValues = append(sm.DefaultValues, discordgo.SelectMenuDefaultValue{ID: id, Type: discordgo.SelectMenuDefaultValueChannel})
		}
		return sm
	case config.KindEnum:
		opts := make([]discordgo.SelectMenuOption, 0, len(st.EnumOptions))
		for _, o := range st.EnumOptions {
			opts = append(opts, discordgo.SelectMenuOption{Label: o.Label, Value: o.Value, Default: o.Value == raw})
		}
		return discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    pickID(st.Key),
			Placeholder: "Select an option (deselect to reset)",
			MinValues:   new(0),
			MaxValues:   1,
			Options:     opts,
		}
	default: // KindChannel / KindCategory
		sm := discordgo.SelectMenu{
			MenuType:     discordgo.ChannelSelectMenu,
			CustomID:     pickID(st.Key),
			Placeholder:  "Select (deselect to clear)",
			MinValues:    new(0),
			MaxValues:    1,
			ChannelTypes: channelTypesFor(st.Kind),
		}
		if raw != "" {
			sm.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: raw, Type: discordgo.SelectMenuDefaultValueChannel}}
		}
		return sm
	}
}

func channelTypesFor(k config.Kind) []discordgo.ChannelType {
	if k == config.KindCategory {
		return []discordgo.ChannelType{discordgo.ChannelTypeGuildCategory}
	}
	return []discordgo.ChannelType{
		discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildForum,
		discordgo.ChannelTypeGuildVoice,
	}
}

func isSelectKind(k config.Kind) bool {
	switch k {
	case config.KindChannel, config.KindCategory, config.KindRole, config.KindChannelList, config.KindEnum:
		return true
	default:
		return false
	}
}

func placeholderFor(st config.Setting) string {
	switch st.Kind {
	case config.KindInt:
		return "whole number (blank to reset)"
	case config.KindDuration:
		return "duration like 48h or 30m (blank to reset)"
	default:
		return "blank to reset"
	}
}

func toggleLabel(label string, on bool) string {
	if on {
		return "Disable " + label
	}
	return "Enable " + label
}

func toggleStyle(on bool) discordgo.ButtonStyle {
	if on {
		return discordgo.DangerButton
	}
	return discordgo.SuccessButton
}

func onOff(b bool) string {
	if b {
		return "✅ On"
	}
	return "❌ Off"
}

func splitCSV(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
