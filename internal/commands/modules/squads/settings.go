package squads

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gamerpal/internal/config"
)

// ConfigSettings declares the squad settings. They're split over several
// panel categories to stay within Discord's per-message component limits.
func (m *Module) ConfigSettings() []config.Setting {
	sq, timing := config.CategorySquads, config.CategorySquadTiming
	out := []config.Setting{
		{Key: config.KeySquadsEnabled, Category: sq, Label: "Squads enabled",
			Description: "Adds a Join a squad button to new intro posts and runs the matcher.", Kind: config.KindBool, Default: false},
		{Key: config.KeySquadsCategoryID, Category: sq, Label: "Squads category",
			Description: "Category new squad channels are created in.", Kind: config.KindCategory},
		{Key: config.KeySquadsVisibility, Category: sq, Label: "Channel visibility",
			Description: "Who can see squad channels. Mods always can. Use /squad-admin refresh-permissions to apply a change to existing squads.",
			Kind:        config.KindEnum, Default: config.SquadVisibilityPublicReadOnly,
			EnumOptions: []config.Option{
				{Value: config.SquadVisibilityPublicReadOnly, Label: "Everyone can read, only members can chat"},
				{Value: config.SquadVisibilityPrivate, Label: "Only members (and mods) can see"},
			}},
		{Key: config.KeySquadsModRoleID, Category: sq, Label: "Mod role",
			Description: "Role that can always see and chat in every squad channel.", Kind: config.KindRole},
		{Key: config.KeySquadsTargetSize, Category: sq, Label: "Squad size",
			Description: "How many people the matcher aims for in a new squad.", Kind: config.KindInt, Default: 6, Validate: validateSize},
		{Key: config.KeySquadsMinSize, Category: sq, Label: "Smallest squad",
			Description: "Smallest squad the fallback will create for people who have waited a while.", Kind: config.KindInt, Default: 3, Validate: validateSize},
		{Key: config.KeySquadsMaxSize, Category: sq, Label: "Largest squad",
			Description: "Young squads take late joiners up to this size.", Kind: config.KindInt, Default: 8, Validate: validateSize},
		{Key: config.KeySquadsMatchInterval, Category: sq, Label: "Pairing interval",
			Description: "How often the matcher runs (e.g. 1h).", Kind: config.KindDuration, Default: "1h", Validate: validateDuration},

		{Key: config.KeySquadsFallbackAfter, Category: timing, Label: "Fallback after",
			Description: "How long someone waits for an ideal match before joining a young squad, a nearby region/age group, or a smaller squad.",
			Kind:        config.KindDuration, Default: "48h", Validate: validateDuration},
		{Key: config.KeySquadsUnmatchedAfter, Category: timing, Label: "Give up after",
			Description: "How long someone can wait unmatched before they're taken out of the queue and DMed to pick games again.",
			Kind:        config.KindDuration, Default: "168h", Validate: validateDuration},
		{Key: config.KeySquadsYoungFor, Category: timing, Label: "Young squad window",
			Description: "How long a new squad keeps taking late joiners.", Kind: config.KindDuration, Default: "168h", Validate: validateDuration},
		{Key: config.KeySquadsQuietWarnAfter, Category: timing, Label: "Quiet warning after",
			Description: "How long a squad can go without chat before it's warned it will be deleted.", Kind: config.KindDuration, Default: "168h", Validate: validateDuration},
		{Key: config.KeySquadsQuietDeleteAfter, Category: timing, Label: "Delete after warning",
			Description: "How long after the warning a still-quiet squad is deleted.", Kind: config.KindDuration, Default: "72h", Validate: validateDuration},
	}
	for _, r := range regions {
		out = append(out, config.Setting{Key: r.Key, Category: config.CategorySquadRegions, Label: r.Name + " role",
			Description: "Onboarding role for " + r.Name + ". Falls back to " + r.Neighbour + ".", Kind: config.KindRole})
	}
	for n, k := range config.SquadAgeKeys {
		desc := "Youngest age group role."
		if n > 0 {
			desc = "Next age group up. Neighbouring groups can be matched as a fallback."
		}
		out = append(out, config.Setting{Key: k, Category: config.CategorySquadAges, Label: fmt.Sprintf("Age group %d role", n+1),
			Description: desc, Kind: config.KindRole})
	}
	for _, p := range platforms {
		out = append(out, config.Setting{Key: p.Key, Category: config.CategorySquadPlatforms, Label: p.Name + " role",
			Description: "Sharing a platform is a small bonus when matching.", Kind: config.KindRole})
	}
	return out
}

type roleSlot struct {
	Key, Code, Name, Neighbour string
}

var regions = []roleSlot{
	{config.KeySquadsRegionNARoleID, "na", "North America", "South America"},
	{config.KeySquadsRegionSARoleID, "sa", "South America", "North America"},
	{config.KeySquadsRegionEURoleID, "eu", "Europe", "Africa"},
	{config.KeySquadsRegionAFRoleID, "af", "Africa", "Europe"},
	{config.KeySquadsRegionASRoleID, "as", "Asia", "Oceania"},
	{config.KeySquadsRegionOCRoleID, "oc", "Oceania", "Asia"},
}

var platforms = []roleSlot{
	{Key: config.KeySquadsPlatformPCRoleID, Code: "pc", Name: "PC"},
	{Key: config.KeySquadsPlatformXboxRoleID, Code: "xbox", Name: "Xbox"},
	{Key: config.KeySquadsPlatformPSRoleID, Code: "ps", Name: "PlayStation"},
	{Key: config.KeySquadsPlatformSwitchRoleID, Code: "switch", Name: "Switch"},
	{Key: config.KeySquadsPlatformMobileRoleID, Code: "mobile", Name: "Mobile"},
}

func regionName(code string) string {
	for _, r := range regions {
		if r.Code == code {
			return r.Name
		}
	}
	return "Unknown region"
}

func validateSize(v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("must be a whole number")
	}
	if n < config.SquadsMinMinSize || n > config.SquadsMaxMaxSize {
		return fmt.Errorf("must be between %d and %d", config.SquadsMinMinSize, config.SquadsMaxMaxSize)
	}
	return nil
}

func validateDuration(v string) error {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("must be a duration like 48h or 30m")
	}
	if d < time.Minute {
		return fmt.Errorf("must be at least 1m")
	}
	return nil
}

// roleLookup maps the configured onboarding roles to match attributes.
type roleLookup struct {
	region   map[string]string // role ID -> region code
	age      map[string]int    // role ID -> 1-based slot
	platform map[string]string // role ID -> platform code
}

func (m *Module) roleLookup(guildID string) roleLookup {
	gc := m.config.ForGuild(guildID)
	rl := roleLookup{region: map[string]string{}, age: map[string]int{}, platform: map[string]string{}}
	regionRoles := gc.GetSquadRoles(config.SquadRegionKeys)
	for _, r := range regions {
		if id := regionRoles[r.Key]; id != "" {
			rl.region[id] = r.Code
		}
	}
	ageRoles := gc.GetSquadRoles(config.SquadAgeKeys)
	for n, k := range config.SquadAgeKeys {
		if id := ageRoles[k]; id != "" {
			rl.age[id] = n + 1
		}
	}
	platformRoles := gc.GetSquadRoles(config.SquadPlatformKeys)
	for _, p := range platforms {
		if id := platformRoles[p.Key]; id != "" {
			rl.platform[id] = p.Code
		}
	}
	return rl
}

// apply fills a candidate's region, age and platforms from member roles. The
// first configured region/age role wins if someone has several.
func (rl roleLookup) apply(c *candidate, roles []string) {
	c.Platforms = map[string]bool{}
	bestRegion := len(regions)
	for _, id := range roles {
		if code, ok := rl.region[id]; ok {
			for n, r := range regions {
				if r.Code == code && n < bestRegion {
					bestRegion, c.Region = n, code
				}
			}
		}
		if slot, ok := rl.age[id]; ok && (c.Age == 0 || slot < c.Age) {
			c.Age = slot
		}
		if p, ok := rl.platform[id]; ok {
			c.Platforms[p] = true
		}
	}
}
