package pixelforge

import (
	"math/bits"
	"math/rand"
	"sort"
	"testing"
)

// naive is a brute-force reference implementation used by tests.
type naive map[uint64]uint64

func (n naive) query(fp uint64) []Hit {
	var hits []Hit
	for id, f := range n {
		if d := bits.OnesCount64(f ^ fp); d <= maxDistance {
			hits = append(hits, Hit{VersionID: id, Distance: d})
		}
	}
	return hits
}

func sameHits(a, b []Hit) bool {
	key := func(h Hit) [2]uint64 { return [2]uint64{h.VersionID, uint64(h.Distance)} }
	ka := make(map[[2]uint64]int, len(a))
	for _, h := range a {
		ka[key(h)]++
	}
	for _, h := range b {
		k := key(h)
		ka[k]--
		if ka[k] < 0 {
			return false
		}
	}
	for _, c := range ka {
		if c != 0 {
			return false
		}
	}
	return true
}

// TestDifferential interleaves upserts, replacements, removals and
// queries, checking every query against a brute-force reference.
func TestDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	ix := New()
	ref := naive{}

	// Seed shared fingerprints and a non-transitive triple:
	// d(a,b)=8, d(b,c)=8, d(a,c)=16.
	var a, b, c uint64
	for i := 0; i < 8; i++ {
		b |= 1 << uint(i)
	}
	c = b
	for i := 0; i < 8; i++ {
		c |= 1 << uint(16+i)
	}
	seed := []struct {
		id uint64
		fp uint64
	}{
		{1, a}, {2, b}, {3, c},
		{4, a}, {5, a}, // versions 1,4,5 share one fingerprint
		{6, b}, // versions 2,6 share one fingerprint
	}
	for _, s := range seed {
		ix.Upsert(s.id, s.fp)
		ref[s.id] = s.fp
	}

	// Pool of fingerprints biased toward collisions and near-misses.
	pool := []uint64{a, b, c}
	for i := 0; i < 40; i++ {
		pool = append(pool, rng.Uint64())
	}
	for i := 0; i < 40; i++ {
		base := pool[rng.Intn(len(pool))]
		pool = append(pool, base^(1<<uint(rng.Intn(64))))
	}

	nextID := uint64(100)
	for step := 0; step < 20000; step++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // upsert new or existing id
			var id uint64
			if rng.Intn(3) == 0 && len(ref) > 0 {
				ids := make([]uint64, 0, len(ref))
				for k := range ref {
					ids = append(ids, k)
				}
				id = ids[rng.Intn(len(ids))]
			} else {
				id = nextID
				nextID++
			}
			fp := pool[rng.Intn(len(pool))]
			ix.Upsert(id, fp)
			ref[id] = fp
		case 4, 5: // remove
			if len(ref) == 0 {
				continue
			}
			ids := make([]uint64, 0, len(ref))
			for k := range ref {
				ids = append(ids, k)
			}
			id := ids[rng.Intn(len(ids))]
			ix.Remove(id)
			delete(ref, id)
		case 6, 7, 8, 9: // query
			fp := pool[rng.Intn(len(pool))]
			got, _ := ix.Query(fp)
			want := ref.query(fp)
			if !sameHits(got, want) {
				t.Fatalf("step %d: query %016x mismatch\ngot  %v\nwant %v", step, fp, got, want)
			}
		}
	}
	if ix.Len() != len(ref) {
		t.Fatalf("Len()=%d, want %d", ix.Len(), len(ref))
	}
}

// TestNonTransitive pins the non-transitive triple explicitly.
func TestNonTransitive(t *testing.T) {
	var a, b, c uint64
	for i := 0; i < 8; i++ {
		b |= 1 << uint(i)
	}
	c = b
	for i := 0; i < 8; i++ {
		c |= 1 << uint(16+i)
	}
	ix := New()
	ix.Upsert(1, a)
	ix.Upsert(2, b)
	ix.Upsert(3, c)

	got, _ := ix.Query(b)
	if !sameHits(got, []Hit{{1, 8}, {2, 0}, {3, 8}}) {
		t.Fatalf("query b: got %v", got)
	}
	got, _ = ix.Query(a)
	if !sameHits(got, []Hit{{1, 0}, {2, 8}}) {
		t.Fatalf("query a: got %v (c must not leak in via b)", got)
	}
}

// TestByteSpread uses hits that differ from the query by exactly one
// bit in every byte (2 bits per 16-bit band), plus distance-9
// distractors, and re-checks after replace/delete/id-reuse.
func TestByteSpread(t *testing.T) {
	var query uint64 = 0x0123456789abcdef

	spread := query
	for byteIdx := 0; byteIdx < 8; byteIdx++ {
		spread ^= 1 << uint(byteIdx*8+byteIdx%8)
	}
	if bits.OnesCount64(spread^query) != 8 {
		t.Fatal("test setup: spread must be distance 8")
	}

	distractor := spread ^ (1 << 62) // distance 9; bit 62 is untouched by spread
	if bits.OnesCount64(distractor^query) != 9 {
		t.Fatal("test setup: distractor must be distance 9")
	}

	ix := New()
	ix.Upsert(10, spread)
	ix.Upsert(11, distractor)
	ix.Upsert(12, ^query) // distance 64

	check := func(want []Hit) {
		t.Helper()
		got, _ := ix.Query(query)
		if !sameHits(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	check([]Hit{{10, 8}})

	// Replace the hit with a far fingerprint: old entry must vanish.
	ix.Upsert(10, ^query)
	check(nil)

	// Replace back, and move the distractor into range.
	ix.Upsert(10, spread)
	ix.Upsert(11, spread^1) // distance 7
	check([]Hit{{10, 8}, {11, 7}})

	// Delete both, then reuse id 10 for a fresh near fingerprint.
	ix.Remove(10)
	ix.Remove(11)
	check(nil)
	ix.Upsert(10, query^0x0101010101010101) // 1 bit per byte
	check([]Hit{{10, 8}})

	// Remove everything; the index must be empty of stale entries.
	ix.Remove(10)
	ix.Remove(12)
	if ix.Len() != 0 {
		t.Fatalf("Len()=%d, want 0", ix.Len())
	}
	check(nil)
	for b := 0; b < numBands; b++ {
		if len(ix.bands[b]) != 0 {
			t.Fatalf("band %d retains %d buckets after removals", b, len(ix.bands[b]))
		}
	}
}

// TestReplaceSameFingerprint ensures replacing with an identical
// fingerprint keeps exactly one index entry.
func TestReplaceSameFingerprint(t *testing.T) {
	ix := New()
	ix.Upsert(7, 0xdeadbeef)
	ix.Upsert(7, 0xdeadbeef)
	got, evals := ix.Query(0xdeadbeef)
	if !sameHits(got, []Hit{{7, 0}}) {
		t.Fatalf("got %v", got)
	}
	if evals != 1 {
		t.Fatalf("evaluations=%d, want 1 (no duplicate entries)", evals)
	}
	ix.Remove(7)
	if got, _ := ix.Query(0xdeadbeef); len(got) != 0 {
		t.Fatalf("got %v after remove", got)
	}
}

// TestSortedHits is a compile-time guard that Hit ordering is not
// relied upon; keeps sort imported for potential debugging helpers.
func TestSortedHits(t *testing.T) {
	hits := []Hit{{3, 2}, {1, 0}}
	sort.Slice(hits, func(i, j int) bool { return hits[i].VersionID < hits[j].VersionID })
	if hits[0].VersionID != 1 {
		t.Fatal("sort broken")
	}
}
