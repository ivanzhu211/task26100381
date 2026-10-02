package pixelforge

import (
	"math/bits"
	"math/rand"
	"sort"
	"testing"
)

// naive is the reference model: a plain map scanned exhaustively.
type naive map[uint64]uint64

func (n naive) query(fp uint64) []Hit {
	var hits []Hit
	for id, f := range n {
		if d := bits.OnesCount64(fp ^ f); d <= maxRadius {
			hits = append(hits, Hit{VersionID: id, Distance: d})
		}
	}
	return hits
}

func sorted(hits []Hit) []Hit {
	sort.Slice(hits, func(i, j int) bool { return hits[i].VersionID < hits[j].VersionID })
	return hits
}

func checkQuery(t *testing.T, ix *Index, model naive, fp uint64) {
	t.Helper()
	got, evals := ix.Query(fp)
	if evals < len(got) {
		t.Fatalf("evals %d < hits %d", evals, len(got))
	}
	want := model.query(fp)
	gotS, wantS := sorted(got), sorted(want)
	if len(gotS) != len(wantS) {
		t.Fatalf("query %016x: got %d hits, want %d\ngot:  %v\nwant: %v", fp, len(gotS), len(wantS), gotS, wantS)
	}
	for i := range gotS {
		if gotS[i] != wantS[i] {
			t.Fatalf("query %016x: hit %d = %+v, want %+v", fp, i, gotS[i], wantS[i])
		}
	}
}

// TestDifferential interleaves upserts, replacements, removals and queries
// against a naive reference, with a fixed seed.
func TestDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	ix := New()
	model := naive{}

	// Seed the workload with structure the property requires:
	// several versions sharing one fingerprint, and a non-transitive
	// triple a-b (d=8), b-c (d=8), a-c (d=16).
	shared := uint64(0xDEADBEEFCAFEF00D)
	for id := uint64(1); id <= 5; id++ {
		ix.Upsert(id, shared)
		model[id] = shared
	}
	a := uint64(0)
	b := uint64(0xFF)   // d(a,b)=8, one half differs by 8
	c := uint64(0xFFFF) // d(b,c)=8, d(a,c)=16: not transitive
	ix.Upsert(100, a)
	ix.Upsert(101, b)
	ix.Upsert(102, c)
	model[100], model[101], model[102] = a, b, c

	checkQuery(t, ix, model, a)
	checkQuery(t, ix, model, b)
	checkQuery(t, ix, model, c)
	checkQuery(t, ix, model, shared)

	var liveIDs []uint64
	nextID := uint64(1000)
	for step := 0; step < 4000; step++ {
		switch op := rng.Intn(10); {
		case op < 4: // upsert new
			id := nextID
			nextID++
			fp := rng.Uint64()
			ix.Upsert(id, fp)
			model[id] = fp
			liveIDs = append(liveIDs, id)
		case op < 6 && len(liveIDs) > 0: // replace existing
			id := liveIDs[rng.Intn(len(liveIDs))]
			fp := rng.Uint64()
			if rng.Intn(4) == 0 {
				fp = model[id] ^ (1 << rng.Intn(64)) // near-duplicate
			}
			ix.Upsert(id, fp)
			model[id] = fp
		case op < 8 && len(liveIDs) > 0: // remove existing
			i := rng.Intn(len(liveIDs))
			id := liveIDs[i]
			liveIDs = append(liveIDs[:i], liveIDs[i+1:]...)
			ix.Remove(id)
			delete(model, id)
		case op < 9: // remove unknown id: no-op
			ix.Remove(nextID + uint64(rng.Intn(1000)))
		default: // query
			var q uint64
			switch rng.Intn(3) {
			case 0:
				q = rng.Uint64()
			case 1: // near a live fingerprint
				if len(liveIDs) > 0 {
					base := model[liveIDs[rng.Intn(len(liveIDs))]]
					q = base
					for j := 0; j < 1+rng.Intn(9); j++ {
						q ^= 1 << rng.Intn(64)
					}
				}
			case 2: // reuse a previously seen fingerprint value
				q = shared ^ uint64(rng.Intn(256))
			}
			checkQuery(t, ix, model, q)
		}
		if ix.Len() != len(model) {
			t.Fatalf("step %d: Len=%d, model=%d", step, ix.Len(), len(model))
		}
	}
	// Final sweep: queries around the non-transitive triple and shared fp.
	for _, q := range []uint64{a, b, c, shared, 0, ^uint64(0)} {
		checkQuery(t, ix, model, q)
	}
}
