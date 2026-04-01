package memory

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

var tokenPattern = regexp.MustCompile(`[a-z0-9]+`)

func tokenize(input string) []string {
	matches := tokenPattern.FindAllString(strings.ToLower(input), -1)
	if len(matches) == 0 {
		return nil
	}

	return matches
}

func keywordScore(queryTokens, candidateTokens []string) float64 {
	if len(queryTokens) == 0 || len(candidateTokens) == 0 {
		return 0
	}

	candidateSet := make(map[string]struct{}, len(candidateTokens))
	for _, token := range candidateTokens {
		candidateSet[token] = struct{}{}
	}

	matches := 0
	for _, token := range queryTokens {
		if _, ok := candidateSet[token]; ok {
			matches++
		}
	}

	return float64(matches) / float64(len(queryTokens))
}

func cosineSimilarity(left, right []float64) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}

	var dotProduct float64
	var leftNorm float64
	var rightNorm float64

	for idx := range left {
		dotProduct += left[idx] * right[idx]
		leftNorm += left[idx] * left[idx]
		rightNorm += right[idx] * right[idx]
	}

	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func recencyScore(lastSeenAt, now time.Time) float64 {
	if lastSeenAt.IsZero() {
		return 0
	}

	age := now.Sub(lastSeenAt)
	if age <= 0 {
		return 1
	}

	days := age.Hours() / 24

	return math.Exp(-days / 30)
}

func topNItems[T any](items []T, limit int, scoreFn func(T) float64) []T {
	if limit <= 0 || len(items) == 0 {
		return nil
	}

	sorted := append([]T(nil), items...)
	sort.Slice(sorted, func(left, right int) bool {
		return scoreFn(sorted[left]) > scoreFn(sorted[right])
	})

	if len(sorted) > limit {
		sorted = sorted[:limit]
	}

	return sorted
}
