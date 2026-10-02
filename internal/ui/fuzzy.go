package ui

import (
	"sort"
	"strings"
	"unicode"
)

// fuzzyScore matches query as a subsequence of text, case-insensitively.
// Lower scores are better: consecutive runs and word starts are rewarded,
// gaps and late matches penalised (the same heuristic as pi's fuzzy filter).
func fuzzyScore(query, text string) (int, bool) {
	q, t := []rune(strings.ToLower(query)), []rune(strings.ToLower(text))
	score, last, run := 0, -1, 0
	for _, c := range q {
		i := last + 1
		for i < len(t) && t[i] != c {
			i++
		}
		if i == len(t) {
			return 0, false
		}
		if i == last+1 && last >= 0 {
			run++
			score -= run * 50
		} else {
			run = 0
			if last >= 0 {
				score += (i - last - 1) * 20
			}
		}
		if i == 0 || !unicode.IsLetter(t[i-1]) && !unicode.IsDigit(t[i-1]) {
			score -= 100
		}
		score += i
		last = i
	}
	if string(q) == string(t) {
		score -= 1000
	}
	return score, true
}

// filterItems returns the indices of the items matching every
// whitespace-separated token of query, best match first.
func filterItems(items []Item, query string) []int {
	tokens := strings.Fields(query)
	type hit struct{ idx, score int }
	var hits []hit
	for i, it := range items {
		total, ok := 0, true
		for _, tok := range tokens {
			// The label alone first, so a label hit outranks a description one.
			s, m := fuzzyScore(tok, it.Label)
			if !m {
				s, m = fuzzyScore(tok, it.Label+" "+it.Description)
				s += 500
			}
			if !m {
				ok = false
				break
			}
			total += s
		}
		if ok {
			hits = append(hits, hit{i, total})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score < hits[b].score })
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.idx
	}
	return out
}
