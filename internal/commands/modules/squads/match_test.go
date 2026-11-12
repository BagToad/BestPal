package squads

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Unix(1_790_000_000, 0)

func person(id, region string, age int, waited time.Duration, games ...int64) *candidate {
	c := &candidate{UserID: id, Region: region, Age: age, QueuedAt: t0.Add(-waited),
		Games: map[int64]string{}, Tags: map[string]bool{}, Platforms: map[string]bool{}}
	for _, g := range games {
		c.Games[g] = fmt.Sprintf("Game %d", g)
	}
	return c
}

func withTags(c *candidate, tags ...string) *candidate {
	for _, t := range tags {
		c.Tags[t] = true
	}
	return c
}

var params = matchParams{Min: 3, Target: 6, Max: 8, FallbackAfter: 48 * time.Hour, UnmatchedAfter: 7 * 24 * time.Hour}

func ids(group []*candidate) []string {
	out := make([]string, len(group))
	for n, c := range group {
		out[n] = c.UserID
	}
	return out
}

func TestBuildPlanFormsFullSquadsWithinRegionAndAge(t *testing.T) {
	var queue []*candidate
	for n := range 6 {
		queue = append(queue, person(fmt.Sprintf("eu%d", n), "eu", 2, time.Hour, 1))
		queue = append(queue, person(fmt.Sprintf("na%d", n), "na", 2, time.Hour, 1))
	}
	queue = append(queue, person("eu-older", "eu", 3, time.Hour, 1))

	p := buildPlan(queue, nil, params, t0)
	require.Len(t, p.NewSquads, 2)
	for _, g := range p.NewSquads {
		require.Len(t, g, 6)
		for _, c := range g {
			assert.Equal(t, g[0].Region, c.Region, "a strict squad never mixes regions")
			assert.Equal(t, 2, c.Age, "a strict squad never mixes age groups")
		}
	}
	assert.Equal(t, []string{"eu-older"}, ids(p.Waiting))
}

func TestBuildPlanRequiresSharedGamesDespiteSharedGenres(t *testing.T) {
	seed := withTags(person("seed", "eu", 1, 5*time.Hour, 10, 11, 12), "Shooter")
	queue := []*candidate{seed}
	for n := range 3 {
		queue = append(queue, withTags(person(fmt.Sprintf("genre%d", n), "eu", 1, time.Hour, 100+int64(n)), "Shooter"))
	}
	for n := range 3 {
		queue = append(queue, withTags(person(fmt.Sprintf("game%d", n), "eu", 1, time.Hour, 10, 11), "Shooter"))
	}
	p := buildPlan(queue, nil, matchParams{Min: 2, Target: 4, Max: 8, FallbackAfter: 48 * time.Hour, UnmatchedAfter: 168 * time.Hour}, t0)
	require.NotEmpty(t, p.NewSquads)
	assert.ElementsMatch(t, []string{"seed", "game0", "game1", "game2"}, ids(p.NewSquads[0]))
}

func TestOverlapIsARatio(t *testing.T) {
	a := person("a", "", 0, 0, 1, 2, 3)
	focused := person("b", "", 0, 0, 1, 2, 4)
	var many []int64
	for g := int64(1); g <= 25; g++ {
		many = append(many, g+100)
	}
	broad := person("c", "", 0, 0, append(many[:23], 1, 2)...)
	sf, _ := pairScore(a, focused)
	sb, _ := pairScore(a, broad)
	assert.Greater(t, sf, sb, "2 of 3 shared should beat 2 of 25 shared")
}

func TestSharedGenreWithoutSharedGameIsNotAMatch(t *testing.T) {
	var queue []*candidate
	for n := range 6 {
		queue = append(queue, withTags(person(fmt.Sprintf("u%d", n), "eu", 1, 3*24*time.Hour, int64(n)), "RPG"))
	}
	p := buildPlan(queue, nil, params, t0)
	assert.Empty(t, p.NewSquads)
	assert.Len(t, p.Waiting, 6)

	_, ok := pairScore(withTags(person("x", "", 0, 0, 1), "RPG"), withTags(person("y", "", 0, 0, 2), "RPG"))
	assert.False(t, ok)
}

func TestSharedGenresDoNotAffectMatchScore(t *testing.T) {
	a := withTags(person("a", "", 0, 0, 1, 2), "RPG")
	b := withTags(person("b", "", 0, 0, 1, 3), "RPG")
	c := withTags(person("c", "", 0, 0, 1, 3), "Shooter")

	withSharedGenre, ok := pairScore(a, b)
	require.True(t, ok)
	withoutSharedGenre, ok := pairScore(a, c)
	require.True(t, ok)
	assert.Equal(t, withoutSharedGenre, withSharedGenre)
}

func TestSharedGenreCannotJoinOpenSquad(t *testing.T) {
	members := []*candidate{
		withTags(person("m1", "eu", 2, 0, 1), "RPG"),
		withTags(person("m2", "eu", 2, 0, 1), "RPG"),
	}
	queue := []*candidate{withTags(person("late", "eu", 2, 50*time.Hour, 2), "RPG")}

	p := buildPlan(queue, []openSquad{{ID: 1, Members: members}}, params, t0)
	assert.Empty(t, p.Joins)
	assert.Equal(t, []string{"late"}, ids(p.Waiting))
}

func TestSquadMustShareOneGameAcrossEveryMember(t *testing.T) {
	queue := []*candidate{
		person("seed", "eu", 1, 5*time.Hour, 1, 2),
		person("game-one", "eu", 1, time.Hour, 1),
		person("game-two-a", "eu", 1, time.Hour, 2),
		person("game-two-b", "eu", 1, time.Hour, 2),
	}

	p := buildPlan(queue, nil, matchParams{Min: 2, Target: 3, Max: 8, FallbackAfter: 48 * time.Hour, UnmatchedAfter: 168 * time.Hour}, t0)
	require.Len(t, p.NewSquads, 1)
	assert.ElementsMatch(t, []string{"seed", "game-two-a", "game-two-b"}, ids(p.NewSquads[0]))
	assert.Equal(t, []string{"game-one"}, ids(p.Waiting))
}

func TestPairwiseOverlapWithoutAGroupwideGameCannotFormSquad(t *testing.T) {
	queue := []*candidate{
		person("a", "eu", 1, 5*time.Hour, 1, 2),
		person("b", "eu", 1, time.Hour, 2, 3),
		person("c", "eu", 1, time.Hour, 1, 3),
	}

	p := buildPlan(queue, nil, matchParams{Min: 2, Target: 3, Max: 8, FallbackAfter: 48 * time.Hour, UnmatchedAfter: 168 * time.Hour}, t0)
	assert.Empty(t, p.NewSquads)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, ids(p.Waiting))
}

func TestLateJoinMustShareOneGameWithWholeSquad(t *testing.T) {
	members := []*candidate{
		person("m1", "eu", 2, 0, 1, 2),
		person("m2", "eu", 2, 0, 1, 3),
	}
	queue := []*candidate{
		person("shares-with-one", "eu", 2, 50*time.Hour, 2),
		person("shares-common", "eu", 2, 50*time.Hour, 1),
	}

	p := buildPlan(queue, []openSquad{{ID: 1, Members: members}}, params, t0)
	require.Len(t, p.Joins, 1)
	assert.Equal(t, "shares-common", p.Joins[0].User.UserID)
	assert.Equal(t, []string{"shares-with-one"}, ids(p.Waiting))
}

func TestMultipleLateJoinsPreserveGroupwideSharedGame(t *testing.T) {
	members := []*candidate{
		person("m1", "eu", 2, 0, 1, 2),
		person("m2", "eu", 2, 0, 1, 3),
	}
	queue := []*candidate{
		person("first", "eu", 2, 50*time.Hour, 1, 4),
		person("second", "eu", 2, 50*time.Hour, 1, 5),
		person("breaks-common", "eu", 2, 50*time.Hour, 2, 4),
	}

	p := buildPlan(queue, []openSquad{{ID: 1, Members: members}}, params, t0)
	require.Len(t, p.Joins, 2)
	assert.Equal(t, "first", p.Joins[0].User.UserID)
	assert.Equal(t, "second", p.Joins[1].User.UserID)
	assert.Equal(t, []string{"breaks-common"}, ids(p.Waiting))
}

func TestPlatformIsOnlyABonus(t *testing.T) {
	a, b := person("a", "", 0, 0, 1), person("b", "", 0, 0, 1)
	base, _ := pairScore(a, b)
	a.Platforms["pc"], b.Platforms["pc"] = true, true
	bonus, _ := pairScore(a, b)
	assert.Greater(t, bonus, base)

	c := person("c", "", 0, 0, 2)
	c.Platforms["pc"] = true
	_, ok := pairScore(a, c)
	assert.False(t, ok, "a shared platform alone isn't a match")
}

func TestFallbackFillsOpenSquadStrictFirst(t *testing.T) {
	euSquad := openSquad{ID: 1, Members: []*candidate{person("m1", "eu", 2, 0, 5), person("m2", "eu", 2, 0, 5)}}
	afSquad := openSquad{ID: 2, Members: []*candidate{person("m3", "af", 2, 0, 5), person("m4", "af", 2, 0, 5)}}
	queue := []*candidate{
		person("eu-late", "eu", 2, 50*time.Hour, 5),
		person("eu-fresh", "eu", 2, time.Hour, 5),
		person("sa-late", "sa", 2, 50*time.Hour, 5),
	}
	p := buildPlan(queue, []openSquad{afSquad, euSquad}, params, t0)
	require.Len(t, p.Joins, 1)
	assert.Equal(t, "eu-late", p.Joins[0].User.UserID)
	assert.EqualValues(t, 1, p.Joins[0].SquadID, "same region beats a neighbouring one")
	assert.ElementsMatch(t, []string{"eu-fresh", "sa-late"}, ids(p.Waiting), "people inside the fallback wait aren't moved; SA has no NA squad")
}

func TestFallbackUsesNeighbouringRegion(t *testing.T) {
	naSquad := openSquad{ID: 7, Members: []*candidate{person("m1", "na", 3, 0, 5), person("m2", "na", 3, 0, 5)}}
	queue := []*candidate{person("sa-late", "sa", 4, 50*time.Hour, 5), person("as-late", "as", 3, 50*time.Hour, 5)}
	p := buildPlan(queue, []openSquad{naSquad}, params, t0)
	require.Len(t, p.Joins, 1)
	assert.Equal(t, "sa-late", p.Joins[0].User.UserID, "SA neighbours NA and age 4 neighbours 3")
	assert.Equal(t, []string{"as-late"}, ids(p.Waiting), "Asia isn't next to North America")
}

func TestFullOpenSquadTakesNobody(t *testing.T) {
	var members []*candidate
	for n := range 8 {
		members = append(members, person(fmt.Sprintf("m%d", n), "eu", 1, 0, 5))
	}
	p := buildPlan([]*candidate{person("late", "eu", 1, 50*time.Hour, 5)}, []openSquad{{ID: 1, Members: members}}, params, t0)
	assert.Empty(t, p.Joins)
}

func TestFallbackFormsSmallerSquadWithNeighbours(t *testing.T) {
	queue := []*candidate{
		person("oc1", "oc", 1, 50*time.Hour, 9),
		person("as1", "as", 2, time.Hour, 9),
		person("oc2", "oc", 1, 3*time.Hour, 9),
		person("oc3-age3", "oc", 3, 3*time.Hour, 9),
	}
	p := buildPlan(queue, nil, params, t0)
	require.Len(t, p.NewSquads, 1)
	assert.ElementsMatch(t, []string{"oc1", "as1", "oc2"}, ids(p.NewSquads[0]), "age group 3 is two away from 1")
	assert.Equal(t, []string{"oc3-age3"}, ids(p.Waiting))
}

func TestTooFewForAMinimumSquadWaitThenGiveUp(t *testing.T) {
	queue := []*candidate{person("a", "af", 5, 50*time.Hour, 1), person("b", "af", 5, 8*24*time.Hour, 1)}
	p := buildPlan(queue, nil, params, t0)
	assert.Empty(t, p.NewSquads)
	assert.Equal(t, []string{"b"}, ids(p.Unmatched))
	assert.Equal(t, []string{"a"}, ids(p.Waiting))
}

func TestUnknownRegionMatchesAnyone(t *testing.T) {
	assert.True(t, compatible(person("a", "", 0, 0), person("b", "eu", 2, 0), false))
	assert.False(t, compatible(person("a", "eu", 1, 0), person("b", "eu", 3, 0), true))
	assert.False(t, compatible(person("a", "na", 1, 0), person("b", "eu", 1, 0), true))
}

func TestCommonGround(t *testing.T) {
	g := []*candidate{
		withTags(person("a", "", 0, 0, 1, 2), "Shooter", "Survival"),
		withTags(person("b", "", 0, 0, 1), "Shooter"),
		withTags(person("c", "", 0, 0, 1, 2), "Survival"),
	}
	assert.Equal(t, "You were matched because 3 of you play **Game 1** and 2 of you play **Game 2**. You're also into **Shooter** and **Survival**.", commonGround(g))
	assert.Equal(t, "You were matched as new pals in the same part of the world.", commonGround([]*candidate{person("x", "", 0, 0, 1), person("y", "", 0, 0, 2)}))
}
