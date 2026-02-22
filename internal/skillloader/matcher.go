package skillloader

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// RunbookMatch pairs a skill definition with its relevance score.
type RunbookMatch struct {
	Definition SkillDefinition
	Score      float64
}

// MatchRunbooks scores each runbook against the query and returns the top N
// matches that exceed the minimum score threshold. Uses keyword-based
// TF-IDF-lite scoring without external dependencies.
func MatchRunbooks(query string, defs []SkillDefinition, topN int) []RunbookMatch {
	if len(defs) == 0 || topN <= 0 {
		return nil
	}

	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	// Build document frequency map across all runbooks.
	df := make(map[string]int)
	allTokens := make([]map[string]int, len(defs))
	for i, def := range defs {
		corpus := def.Name + " " + def.Category + " " + def.Description
		tokens := tokenizeFreq(corpus)
		allTokens[i] = tokens
		for token := range tokens {
			df[token]++
		}
	}

	numDocs := float64(len(defs))
	var matches []RunbookMatch

	for i, def := range defs {
		score := 0.0
		docTokens := allTokens[i]
		docLen := 0
		for _, count := range docTokens {
			docLen += count
		}
		if docLen == 0 {
			continue
		}

		for _, qt := range queryTokens {
			tf := float64(docTokens[qt]) / float64(docLen)
			idf := math.Log(1 + numDocs/float64(1+df[qt]))
			score += tf * idf
		}

		// Boost for exact name match.
		nameLower := strings.ToLower(def.Name)
		queryLower := strings.ToLower(query)
		if strings.Contains(queryLower, nameLower) || strings.Contains(nameLower, queryLower) {
			score *= 2.0
		}

		// Boost for category match.
		catLower := strings.ToLower(def.Category)
		if strings.Contains(queryLower, catLower) {
			score *= 1.5
		}

		if score > 0.01 {
			matches = append(matches, RunbookMatch{Definition: def, Score: score})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})

	if len(matches) > topN {
		matches = matches[:topN]
	}
	return matches
}

// tokenize splits text into lowercase word tokens.
func tokenize(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	// Filter stop words.
	result := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) > 2 && !stopWords[w] {
			result = append(result, w)
		}
	}
	return result
}

// tokenizeFreq returns token frequencies from text.
func tokenizeFreq(text string) map[string]int {
	freq := make(map[string]int)
	for _, token := range tokenize(text) {
		freq[token]++
	}
	return freq
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "has": true,
	"her": true, "was": true, "one": true, "our": true, "out": true,
	"with": true, "that": true, "this": true, "from": true, "have": true,
	"been": true, "will": true, "they": true, "when": true, "what": true,
	"your": true, "which": true, "their": true, "about": true, "would": true,
	"there": true, "should": true, "each": true, "make": true, "like": true,
	"than": true, "them": true, "then": true, "into": true, "some": true,
}
