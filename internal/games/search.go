package games

import (
	"fmt"
	"gamerpal/internal/igdbclient"
	"strings"

	"github.com/Henry-Sarabia/igdb/v2"
)

type GameSearchResult struct {
	ExactMatch  *igdb.Game
	Suggestions []*igdb.Game
}

// ExactMatchWithSuggestions searches for a game by name and returns an exact match if found,
// along with a list of suggested games for use if an exact match is not found.
func ExactMatchWithSuggestions(igdbClient *igdbclient.Client, gameName string) (*GameSearchResult, error) {
	if igdbClient == nil {
		return nil, fmt.Errorf("igdb client is nil")
	}

	gameName = strings.TrimSpace(gameName)
	if gameName == "" {
		return nil, fmt.Errorf("empty game name")
	}

	var games []*igdb.Game
	var searchErr error

	// Wrap IGDB calls with auto-retry on auth failure
	err := igdbClient.Retry(func() error {
		gamesSvc, err := igdbClient.Games()
		if err != nil {
			return err
		}

		exacts, _ := gamesSvc.Index(
			igdb.SetFields("id", "name", "summary", "websites", "multiplayer_modes", "cover", "release_dates", "first_release_date"),
			igdb.SetFilter("name", igdb.OpEqualsCaseInsensitive, fmt.Sprintf(`"%s"`, gameName)),
			igdb.SetLimit(10),
		)
		games = append(games, exacts...)

		searchGames, err := gamesSvc.Search(gameName,
			igdb.SetFields("id", "name", "summary", "websites", "multiplayer_modes", "cover", "release_dates", "first_release_date"),
			igdb.SetLimit(10),
			igdb.SetFilter("name", igdb.OpEqualsCaseInsensitive, fmt.Sprintf(`*"%s"*`, gameName)),
		)
		if err != nil {
			searchErr = fmt.Errorf("igdb search error: %w", err)
			return err
		}

		games = append(games, searchGames...)
		return nil
	})

	if err != nil {
		return nil, err
	}
	if searchErr != nil {
		return nil, searchErr
	}

	var exact *igdb.Game
	suggestions := make([]*igdb.Game, 0, len(games))
	for _, g := range games {
		if g == nil || g.Name == "" {
			continue
		}

		// Case sensitive match - these are more important.
		if g.Name == gameName && exact == nil {
			exact = g
			continue
		}

		// Case insensitive match - these are less important.
		// Use only when no case sensitive match is found.
		if strings.EqualFold(g.Name, gameName) && exact == nil {
			exact = g
			continue
		}

		// The rest are not exacts, add to suggestions
		suggestions = append(suggestions, g)
	}

	if exact == nil && len(suggestions) == 0 {
		return &GameSearchResult{ExactMatch: nil, Suggestions: nil}, nil
	}

	return &GameSearchResult{ExactMatch: exact, Suggestions: suggestions}, nil
}
