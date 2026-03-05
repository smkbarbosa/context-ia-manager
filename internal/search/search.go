// Package search provides hybrid semantic + keyword search.
package search

import (
	"math"
	"sort"
	"strings"

	"github.com/smkbarbosa/context-ia-manager/internal/storage"
)

// Result wraps a chunk with its final hybrid score.
type Result struct {
	storage.Chunk
	BM25Score float32
	VecScore  float32
	RRFScore  float32
}

// Hybrid merges vector search results with BM25 keyword scoring via
// Reciprocal Rank Fusion (RRF), the same strategy used by th0th.
//
// vecResults must already be sorted by cosine similarity (desc).
// query is the raw user query used for BM25 scoring.
func Hybrid(query string, vecResults []storage.Chunk) []Result {
	terms := tokenise(query)
	results := make([]Result, len(vecResults))

	for i, c := range vecResults {
		bm25 := bm25Score(terms, c.Content)
		results[i] = Result{
			Chunk:     c,
			VecScore:  c.Score,
			BM25Score: bm25,
		}
	}

	// RRF rank fusion: score = Σ 1 / (k + rank_i)
	const k = 60.0

	// Rank by vec score
	vecRanked := make([]Result, len(results))
	copy(vecRanked, results)
	sort.Slice(vecRanked, func(i, j int) bool {
		return vecRanked[i].VecScore > vecRanked[j].VecScore
	})

	// Rank by BM25
	bm25Ranked := make([]Result, len(results))
	copy(bm25Ranked, results)
	sort.Slice(bm25Ranked, func(i, j int) bool {
		return bm25Ranked[i].BM25Score > bm25Ranked[j].BM25Score
	})

	// Accumulate RRF scores by chunk ID
	rrf := map[int64]float32{}
	for rank, r := range vecRanked {
		rrf[r.ID] += 1.0 / float32(k+float64(rank+1))
	}
	for rank, r := range bm25Ranked {
		rrf[r.ID] += 1.0 / float32(k+float64(rank+1))
	}

	for i := range results {
		results[i].RRFScore = rrf[results[i].ID]
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].RRFScore > results[j].RRFScore
	})
	return results
}

// Compress reduces text to its structural skeleton, keeping class/function
// signatures and removing docstrings / long comment blocks.
func Compress(content string) string {
	lines := strings.Split(content, "\n")
	var kept []string
	inDocstring := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Toggle docstring blocks
		if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
			inDocstring = !inDocstring
			continue
		}
		if inDocstring {
			continue
		}
		// Skip pure comment lines and blank lines after first pass
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// --- BM25 helpers (simplified, no IDF corpus needed for single-chunk scoring) ---

func tokenise(text string) []string {
	lower := strings.ToLower(text)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9') && r != '_'
	})
	return fields
}

// bm25Score computes a single-document BM25-like term frequency score.
func bm25Score(terms []string, doc string) float32 {
	docTerms := tokenise(doc)
	docLen := float64(len(docTerms))
	avgLen := 150.0 // rough average chunk size in tokens

	const k1 = 1.5
	const b = 0.75

	freq := map[string]int{}
	for _, t := range docTerms {
		freq[t]++
	}

	var score float64
	for _, term := range terms {
		tf := float64(freq[term])
		if tf == 0 {
			continue
		}
		norm := tf * (k1 + 1) / (tf + k1*(1-b+b*docLen/avgLen))
		score += norm // IDF simplified to 1 for single-doc scoring
	}

	// Normalise to [0, 1] range approximately
	return float32(math.Tanh(score / float64(len(terms)+1)))
}
