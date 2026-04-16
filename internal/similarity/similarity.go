// Package similarity provides fuzzy string matching for duplicate detection.
package similarity

import "strings"

// NameThreshold is the maximum normalized Levenshtein distance (0.0–1.0) for
// two names to be considered fuzzy duplicates. Lower means stricter.
const NameThreshold = 0.3

// CommandSubstringMinLen is the minimum length a command must have before
// substring matching is applied. Very short commands produce too many false
// positives.
const CommandSubstringMinLen = 12

// LevenshteinDistance computes the edit distance between two strings using the
// classic Wagner–Fischer dynamic programming algorithm.
func LevenshteinDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Use a single-row DP to save memory.
	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				curr[j-1]+1,   // insertion
				prev[j]+1,     // deletion
				prev[j-1]+cost, // substitution
			)
		}
		prev = curr
	}

	return prev[lb]
}

// NormalizedDistance returns the Levenshtein distance divided by the length of
// the longer string. Result is 0.0 (identical) to 1.0 (completely different).
func NormalizedDistance(a, b string) float64 {
	maxLen := max(len(a), len(b))
	if maxLen == 0 {
		return 0
	}
	return float64(LevenshteinDistance(a, b)) / float64(maxLen)
}

// IsFuzzyNameMatch returns true if two names are similar enough to be
// considered near-duplicates. Empty names never match.
func IsFuzzyNameMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a == b {
		return false // exact match is handled separately
	}
	return NormalizedDistance(a, b) <= NameThreshold
}

// IsFuzzyCommandMatch returns true if two commands are similar enough to be
// considered near-duplicates. It checks whether one command is a substring of
// the other (case-insensitive). Only applied when both commands meet the
// minimum length threshold.
func IsFuzzyCommandMatch(a, b string) bool {
	if a == b {
		return false // exact match is handled separately
	}
	if len(a) < CommandSubstringMinLen || len(b) < CommandSubstringMinLen {
		return false
	}
	al := strings.ToLower(a)
	bl := strings.ToLower(b)
	return strings.Contains(al, bl) || strings.Contains(bl, al)
}
