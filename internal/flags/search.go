package flags

import (
	"sort"
	"strings"
)

// SearchResult is one fuzzy-search hit with a deterministic score. Higher is
// a better match.
type SearchResult struct {
	Flag  Flag `json:"flag"`
	Score int  `json:"score"`
}

// Search ranks catalog entries against a query. Names and aliases outweigh
// descriptions; exact and prefix matches outweigh substring and subsequence
// matches. Ties break by canonical name so results are stable across runs.
func Search(c *Catalog, query string, limit int) []SearchResult {
	query = strings.ToLower(strings.TrimSpace(strings.TrimLeft(query, "-")))
	results := make([]SearchResult, 0, len(c.Flags))
	for _, f := range c.Flags {
		score := 0
		for _, candidate := range append([]string{f.Name}, f.Aliases...) {
			if s := matchScore(strings.ToLower(candidate), query); s > score {
				score = s
			}
		}
		if s := matchScore(strings.ToLower(f.Description), query); s > score {
			score = s / 2
		}
		if s := matchScore(strings.ToLower(f.Category), query); s > score {
			score = s / 2
		}
		if query != "" && score == 0 {
			continue
		}
		results = append(results, SearchResult{Flag: f, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Flag.Name < results[j].Flag.Name
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func matchScore(text, query string) int {
	if query == "" {
		return 1
	}
	if text == query {
		return 1000
	}
	if i := strings.Index(text, query); i >= 0 {
		if i == 0 {
			return 900 - minInt(len(text)-len(query), 100)
		}
		return 700 - minInt(i, 100)
	}
	if gaps, ok := subsequenceGaps(text, query); ok {
		return 500 - minInt(gaps*2, 200)
	}
	return 0
}

// subsequenceGaps returns the number of unmatched characters in the shortest
// in-order match of query within text.
func subsequenceGaps(text, query string) (int, bool) {
	qi := 0
	gaps := 0
	for _, r := range text {
		if qi < len(query) && r == rune(query[qi]) {
			qi++
			continue
		}
		if qi > 0 {
			gaps++
		}
	}
	if qi != len(query) {
		return 0, false
	}
	return gaps, true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
