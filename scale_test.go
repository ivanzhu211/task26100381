package pixelforge

import (
	"math/bits"
	"math/rand"
	"runtime"
	"syscall"
	"testing"
	"time"
)

const (
	scaleN       = 1_000_000
	scaleQueries = 1_000
	scaleSeed    = 810081
	maxAvgEvals  = 10_000
	maxPeakMiB   = 768
)

func peakRSSBytes() uint64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	if runtime.GOOS == "darwin" {
		return uint64(ru.Maxrss) // bytes on macOS
	}
	return uint64(ru.Maxrss) * 1024 // KiB on Linux
}

// TestScale builds an index of 1M distinct uniform fingerprints and
// runs 1000 queries, each the fingerprint of a stored version with 8
// distinct bits flipped. It verifies correctness (the flipped version
// must be found; sampled queries are diffed against a full scan) and
// enforces the evaluation-count and memory budgets.
func TestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped in -short mode")
	}
	rng := rand.New(rand.NewSource(scaleSeed))

	start := time.Now()
	ix := New()
	fps := make([]uint64, 0, scaleN)
	seen := make(map[uint64]struct{}, scaleN)
	for len(fps) < scaleN {
		fp := rng.Uint64()
		if _, dup := seen[fp]; dup {
			continue
		}
		seen[fp] = struct{}{}
		id := uint64(len(fps))
		fps = append(fps, fp)
		ix.Upsert(id, fp)
	}
	buildDur := time.Since(start)

	// Pick 1000 distinct stored versions and flip 8 distinct bits.
	perm := rng.Perm(scaleN)[:scaleQueries]
	type queryCase struct {
		id uint64
		q  uint64
	}
	cases := make([]queryCase, 0, scaleQueries)
	for _, idx := range perm {
		q := fps[idx]
		bitsSet := rng.Perm(64)[:8]
		for _, bit := range bitsSet {
			q ^= 1 << uint(bit)
		}
		cases = append(cases, queryCase{id: uint64(idx), q: q})
	}

	var totalEvals uint64
	var totalHits int
	qstart := time.Now()
	for i, qc := range cases {
		hits, evals := ix.Query(qc.q)
		totalEvals += evals
		totalHits += len(hits)

		found := false
		for _, h := range hits {
			if h.VersionID == qc.id {
				if h.Distance != 8 {
					t.Fatalf("query %d: hit distance=%d, want 8", i, h.Distance)
				}
				found = true
			}
			if got, want := h.Distance, bits.OnesCount64(fps[h.VersionID]^qc.q); got != want {
				t.Fatalf("query %d: reported distance %d, true %d", i, got, want)
			}
		}
		if !found {
			t.Fatalf("query %d: flipped version %d not found", i, qc.id)
		}

		// Diff every 50th query against a full brute-force scan.
		if i%50 == 0 {
			want := make(map[uint64]int)
			for id, fp := range fps {
				if d := bits.OnesCount64(fp ^ qc.q); d <= maxDistance {
					want[uint64(id)] = d
				}
			}
			if len(hits) != len(want) {
				t.Fatalf("query %d: %d hits, brute force says %d", i, len(hits), len(want))
			}
			for _, h := range hits {
				if d, ok := want[h.VersionID]; !ok || d != h.Distance {
					t.Fatalf("query %d: unexpected hit %+v", i, h)
				}
			}
		}
	}
	queryDur := time.Since(qstart)

	avgEvals := float64(totalEvals) / float64(len(cases))
	peakMiB := float64(peakRSSBytes()) / (1 << 20)
	t.Logf("build: %d fingerprints in %v", scaleN, buildDur)
	t.Logf("queries: %d in %v (avg %v)", len(cases), queryDur, queryDur/time.Duration(len(cases)))
	t.Logf("evaluations: total=%d avg=%.0f per query (budget %d)", totalEvals, avgEvals, maxAvgEvals)
	t.Logf("hits: total=%d avg=%.2f per query", totalHits, float64(totalHits)/float64(len(cases)))
	t.Logf("peak RSS: %.1f MiB (budget %d MiB)", peakMiB, maxPeakMiB)

	if avgEvals > maxAvgEvals {
		t.Fatalf("avg evaluations %.0f exceeds budget %d", avgEvals, maxAvgEvals)
	}
	if peakMiB > maxPeakMiB {
		t.Fatalf("peak RSS %.1f MiB exceeds budget %d MiB", peakMiB, maxPeakMiB)
	}
}
