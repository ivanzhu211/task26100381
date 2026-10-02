package pixelforge

import (
	"math/bits"
	"math/rand"
	"runtime"
	"testing"
	"time"
)

const (
	scaleN       = 1_000_000
	scaleQueries = 1000
	scaleMaxAvg  = 10_000
	scaleMaxMiB  = 768
)

func heapAllocMiB() float64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapAlloc) / (1 << 20)
}

// TestScale builds the index over 1M distinct uniform fingerprints from a
// fixed seed, then queries 1000 of them after flipping 8 distinct bits.
// It enforces the evaluation-count and memory budgets, verifies every
// source version is found, and cross-checks a sample of queries against
// an exhaustive scan.
//
// Run alone with: go test -run TestScale -v
func TestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped in -short mode")
	}
	rng := rand.New(rand.NewSource(81))

	t0 := time.Now()
	fps := make([]uint64, 0, scaleN)
	seen := make(map[uint64]struct{}, scaleN)
	for len(fps) < scaleN {
		fp := rng.Uint64()
		if _, dup := seen[fp]; dup {
			continue
		}
		seen[fp] = struct{}{}
		fps = append(fps, fp)
	}
	genDur := time.Since(t0)

	peak := heapAllocMiB()
	t0 = time.Now()
	ix := New()
	for id, fp := range fps {
		ix.Upsert(uint64(id+1), fp)
		if (id+1)%(1<<17) == 0 {
			if m := heapAllocMiB(); m > peak {
				peak = m
			}
		}
	}
	buildDur := time.Since(t0)
	if m := heapAllocMiB(); m > peak {
		peak = m
	}

	// Queries: pick 1000 stored fingerprints, flip 8 distinct bits each.
	type query struct {
		sourceID uint64
		fp       uint64
	}
	queries := make([]query, 0, scaleQueries)
	perm := rng.Perm(scaleN)[:scaleQueries]
	for _, idx := range perm {
		q := fps[idx]
		for _, bit := range rng.Perm(64)[:8] {
			q ^= 1 << bit
		}
		queries = append(queries, query{sourceID: uint64(idx + 1), fp: q})
	}

	t0 = time.Now()
	totalEvals := 0
	maxEvals := 0
	for i, q := range queries {
		hits, evals := ix.Query(q.fp)
		totalEvals += evals
		if evals > maxEvals {
			maxEvals = evals
		}
		found := false
		for _, h := range hits {
			if h.VersionID == q.sourceID {
				if h.Distance != 8 {
					t.Fatalf("query %d: source hit with distance %d, want 8", i, h.Distance)
				}
				found = true
			}
			if h.Distance > maxRadius {
				t.Fatalf("query %d: hit %+v beyond radius", i, h)
			}
		}
		if !found {
			t.Fatalf("query %d: source version %d (flipped 8 bits) not found", i, q.sourceID)
		}
		if (i+1)%200 == 0 {
			if m := heapAllocMiB(); m > peak {
				peak = m
			}
		}
	}
	queryDur := time.Since(t0)

	// Cross-check a sample of queries against an exhaustive scan.
	for i := 0; i < scaleQueries; i += 97 {
		want := map[uint64]int{}
		for id, fp := range fps {
			if d := bits.OnesCount64(queries[i].fp ^ fp); d <= maxRadius {
				want[uint64(id+1)] = d
			}
		}
		hits, _ := ix.Query(queries[i].fp)
		only(t, hits, want)
	}

	avg := float64(totalEvals) / scaleQueries
	t.Logf("dataset: %d fingerprints generated in %v", scaleN, genDur)
	t.Logf("build: %v (%d upserts)", buildDur, scaleN)
	t.Logf("queries: %d in %v (avg %v/query)", scaleQueries, queryDur, queryDur/scaleQueries)
	t.Logf("full distance evaluations: total=%d avg=%.1f max=%d (budget %d)", totalEvals, avg, maxEvals, scaleMaxAvg)
	t.Logf("peak HeapAlloc: %.1f MiB (budget %d MiB)", peak, scaleMaxMiB)

	if avg > scaleMaxAvg {
		t.Fatalf("average evaluations %.1f exceeds budget %d", avg, scaleMaxAvg)
	}
	if peak > scaleMaxMiB {
		t.Fatalf("peak memory %.1f MiB exceeds budget %d MiB", peak, scaleMaxMiB)
	}
}
