package squads

import (
	"maps"
	"math"
	"slices"
	"sort"
	"time"
)

// candidate is one person the matcher can place: someone in the queue, or a
// member of an existing squad (used to score late joiners against it).
type candidate struct {
	UserID    string
	Name      string // display name, for intro links
	IntroID   string // intro thread ID, "" when unknown
	QueuedAt  time.Time
	Region    string // region key, "" when unknown (matches any region)
	Age       int    // 1-based age group slot, 0 when unknown (matches any age)
	Platforms map[string]bool
	Games     map[int64]string // IGDB game ID -> name
	Tags      map[string]bool  // IGDB genre and theme names
	TryNew    bool             // opted into genre/theme matching
}

// openSquad is an existing squad that can still take late joiners.
type openSquad struct {
	ID      int64
	Name    string
	Members []*candidate
}

type matchParams struct {
	Min, Target, Max int
	FallbackAfter    time.Duration // when to relax the rules for someone
	UnmatchedAfter   time.Duration // when to give up and ask them to re-pick
}

type squadJoin struct {
	User    *candidate
	SquadID int64
}

// plan is the matcher's output. Nothing is changed until it's applied.
type plan struct {
	NewSquads [][]*candidate
	Joins     []squadJoin
	Unmatched []*candidate
	Waiting   []*candidate
}

// Score weights. Exact games remain the strongest signal. Genres/themes are
// considered only when both people opted into trying new games.
const (
	gameWeight     = 3.0
	tagWeight      = 1.0
	platformWeight = 0.5
)

// regionNeighbours are the regions the fallback may pair up.
var regionNeighbours = map[string][]string{
	"na": {"sa"}, "sa": {"na"},
	"eu": {"af"}, "af": {"eu"},
	"as": {"oc"}, "oc": {"as"},
}

// compatible reports whether two people may share a squad. Strict mode needs
// the same region and age group; relaxed mode also allows neighbours.
// Unknown region or age matches anything.
func compatible(a, b *candidate, relaxed bool) bool {
	if a.Region != "" && b.Region != "" && a.Region != b.Region {
		if !relaxed || !slices.Contains(regionNeighbours[a.Region], b.Region) {
			return false
		}
	}
	if a.Age != 0 && b.Age != 0 && a.Age != b.Age {
		if !relaxed || abs(a.Age-b.Age) > 1 {
			return false
		}
	}
	return true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// cosine is the overlap ratio of two sets: shared / sqrt(|a|*|b|). Sharing 2
// of your 3 games scores far higher than sharing 2 of 25.
func cosine[K comparable, V any](a, b map[K]V) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for k := range a {
		if _, ok := b[k]; ok {
			shared++
		}
	}
	return float64(shared) / math.Sqrt(float64(len(a)*len(b)))
}

// pairScore rates how well two people fit. Exact shared games always count;
// shared genres/themes count only when both people opted in.
func pairScore(a, b *candidate) (score float64, ok bool) {
	g := cosine(a.Games, b.Games)
	t := 0.0
	if a.TryNew && b.TryNew {
		t = cosine(a.Tags, b.Tags)
	}
	if g == 0 && t == 0 {
		return 0, false
	}
	score = gameWeight*g + tagWeight*t
	for p := range a.Platforms {
		if b.Platforms[p] {
			score += platformWeight
			break
		}
	}
	return score, true
}

// groupScore is c's average score against a group. ok is true when the whole
// prospective squad shares an exact game, or when everyone opted in and the
// whole squad shares a genre/theme.
func groupScore(c *candidate, group []*candidate) (float64, bool) {
	if len(group) == 0 {
		return 0, false
	}
	common := maps.Clone(c.Games)
	commonTags := maps.Clone(c.Tags)
	allTryNew := c.TryNew
	total := 0.0
	for _, m := range group {
		s, ok := pairScore(c, m)
		if !ok {
			return 0, false
		}
		total += s
		maps.DeleteFunc(common, func(id int64, _ string) bool {
			_, shared := m.Games[id]
			return !shared
		})
		allTryNew = allTryNew && m.TryNew
		maps.DeleteFunc(commonTags, func(tag string, _ bool) bool {
			return !m.Tags[tag]
		})
	}
	return total / float64(len(group)), len(common) > 0 || allTryNew && len(commonTags) > 0
}

func fitsGroup(c *candidate, group []*candidate, relaxed bool) bool {
	for _, m := range group {
		if !compatible(c, m, relaxed) {
			return false
		}
	}
	return true
}

// grow adds the best-fitting people from pool to group until it has limit
// members or nobody else fits.
func grow(group, pool []*candidate, limit int, relaxed bool) []*candidate {
	used := map[*candidate]bool{}
	for _, g := range group {
		used[g] = true
	}
	for len(group) < limit {
		var best *candidate
		bestScore := -1.0
		for _, c := range pool {
			if used[c] || !fitsGroup(c, group, relaxed) {
				continue
			}
			s, ok := groupScore(c, group)
			if ok && s > bestScore {
				best, bestScore = c, s
			}
		}
		if best == nil {
			break
		}
		group = append(group, best)
		used[best] = true
	}
	return group
}

// buildPlan decides who goes where:
//
//  1. Full squads (target size) of people with the same region and age group
//     who share an exact game, or a genre/theme when everyone opted in,
//     seeded by whoever has waited longest.
//  2. People past the fallback wait join the best open squad (same region and
//     age first, then neighbours), or seed a smaller squad of at least the
//     minimum size with neighbours.
//  3. People past the unmatched wait are given up on.
func buildPlan(queue []*candidate, open []openSquad, p matchParams, now time.Time) plan {
	remaining := slices.Clone(queue)
	sort.SliceStable(remaining, func(i, j int) bool { return remaining[i].QueuedAt.Before(remaining[j].QueuedAt) })
	var out plan

	take := func(group []*candidate) {
		remaining = slices.DeleteFunc(remaining, func(c *candidate) bool { return slices.Contains(group, c) })
	}

	// 1. Ideal full squads.
	for i := 0; i < len(remaining); {
		group := grow([]*candidate{remaining[i]}, remaining, p.Target, false)
		if len(group) == p.Target {
			out.NewSquads = append(out.NewSquads, group)
			take(group)
			continue
		}
		i++
	}

	overdue := func(c *candidate) bool { return now.Sub(c.QueuedAt) >= p.FallbackAfter }

	// 2a. Overdue people fill open squads.
	open = slices.Clone(open)
	for _, c := range slices.Clone(remaining) {
		if !overdue(c) {
			continue
		}
		if idx := bestOpenSquad(c, open, p.Max); idx >= 0 {
			out.Joins = append(out.Joins, squadJoin{User: c, SquadID: open[idx].ID})
			open[idx].Members = append(slices.Clone(open[idx].Members), c)
			take([]*candidate{c})
		}
	}

	// 2b. Overdue people seed smaller squads with neighbours.
	for i := 0; i < len(remaining); {
		c := remaining[i]
		if overdue(c) {
			group := grow([]*candidate{c}, remaining, p.Target, true)
			if len(group) >= p.Min {
				out.NewSquads = append(out.NewSquads, group)
				take(group)
				continue
			}
		}
		i++
	}

	// 3. Give up on people who have waited too long.
	for _, c := range remaining {
		if now.Sub(c.QueuedAt) >= p.UnmatchedAfter {
			out.Unmatched = append(out.Unmatched, c)
		} else {
			out.Waiting = append(out.Waiting, c)
		}
	}
	return out
}

// bestOpenSquad returns the index of the best open squad for c, preferring a
// strict fit over a neighbour fit, or -1 if none fits.
func bestOpenSquad(c *candidate, open []openSquad, maxSize int) int {
	for _, relaxed := range []bool{false, true} {
		best, bestScore := -1, -1.0
		for i, sq := range open {
			if len(sq.Members) >= maxSize || len(sq.Members) == 0 || !fitsGroup(c, sq.Members, relaxed) {
				continue
			}
			if s, ok := groupScore(c, sq.Members); ok && s > bestScore {
				best, bestScore = i, s
			}
		}
		if best >= 0 {
			return best
		}
	}
	return -1
}

// sharedSummary describes what a group has in common: games and tags that at
// least two of them picked, most popular first.
func sharedSummary(group []*candidate) (games []sharedItem, tags []sharedItem) {
	gameCount := map[int64]int{}
	gameName := map[int64]string{}
	tagCount := map[string]int{}
	for _, c := range group {
		for id, name := range c.Games {
			gameCount[id]++
			gameName[id] = name
		}
		for t := range c.Tags {
			tagCount[t]++
		}
	}
	for id, n := range gameCount {
		if n >= 2 {
			games = append(games, sharedItem{Name: gameName[id], Count: n})
		}
	}
	for t, n := range tagCount {
		if n >= 2 {
			tags = append(tags, sharedItem{Name: t, Count: n})
		}
	}
	sortShared(games)
	sortShared(tags)
	return games, tags
}

type sharedItem struct {
	Name  string
	Count int
}

func sortShared(items []sharedItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Name < items[j].Name
	})
}
