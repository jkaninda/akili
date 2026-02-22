package agent

import (
	"sort"
	"strings"
	"unicode"
)

// matchRunbooks scores each runbook against the query using keyword overlap
// and returns the top N matches. Avoids importing skillloader to prevent cycles.
func matchRunbooks(query string, runbooks []Runbook, topN int) []Runbook {
	if len(runbooks) == 0 || topN <= 0 {
		return nil
	}

	queryTokens := tokenizeQuery(query)
	if len(queryTokens) == 0 {
		return nil
	}

	type scored struct {
		rb    Runbook
		score float64
	}
	var results []scored

	for _, rb := range runbooks {
		corpus := rb.Name + " " + rb.Category + " " + rb.Description
		docTokens := tokenizeQuery(corpus)
		if len(docTokens) == 0 {
			continue
		}

		// Count overlapping tokens.
		docSet := make(map[string]bool, len(docTokens))
		for _, t := range docTokens {
			docSet[t] = true
		}
		hits := 0
		for _, qt := range queryTokens {
			if docSet[qt] {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		score := float64(hits) / float64(len(queryTokens))

		// Boost for name match.
		if strings.Contains(strings.ToLower(query), strings.ToLower(rb.Name)) {
			score *= 2.0
		}

		if score > 0.05 {
			results = append(results, scored{rb: rb, score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if len(results) > topN {
		results = results[:topN]
	}

	matched := make([]Runbook, len(results))
	for i, r := range results {
		matched[i] = r.rb
	}
	return matched
}

// tokenizeQuery splits text into lowercase word tokens, filtering stop words.
func tokenizeQuery(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	result := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) > 2 && !agentStopWords[w] {
			result = append(result, w)
		}
	}
	return result
}

var agentStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "has": true,
	"was": true, "one": true, "our": true, "out": true, "with": true,
	"that": true, "this": true, "from": true, "have": true, "been": true,
	"will": true, "they": true, "when": true, "what": true, "your": true,
	"which": true, "their": true, "about": true, "would": true,
	"there": true, "should": true, "each": true, "make": true,
}
