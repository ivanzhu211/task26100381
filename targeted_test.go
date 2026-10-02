package pixelforge

import (
	"math/bits"
	"testing"
)

func only(t *testing.T, hits []Hit, want map[uint64]int) {
	t.Helper()
	if len(hits) != len(want) {
		t.Fatalf("got %d hits %v, want %d %v", len(hits), hits, len(want), want)
	}
	for _, h := range hits {
		d, ok := want[h.VersionID]
		if !ok {
			t.Fatalf("unexpected hit %+v", h)
		}
		if h.Distance != d {
			t.Fatalf("hit %d distance=%d, want %d", h.VersionID, h.Distance, d)
		}
	}
}

// TestOneBitPerByte plants hits that differ from the query in exactly one
// bit of every byte (distance 8, spread across both 32-bit halves), plus
// distance-9 distractors, so pruning can never rely on any byte, half, or
// segment comparing equal. It then replaces, removes, and reuses version
// IDs to catch stale index entries.
func TestOneBitPerByte(t *testing.T) {
	ix := New()
	base := uint64(0x0123456789ABCDEF)
	spread := uint64(0x0101010101010101) // 1 bit per byte, 4 per half

	ix.Upsert(1, base)                // distance 0
	ix.Upsert(2, base^spread)         // distance 8, 1 bit per byte
	ix.Upsert(3, base^spread^0x02)    // distance 9: distractor
	ix.Upsert(4, base^0xFF)           // distance 8, all in low half
	ix.Upsert(5, base^(0xFF<<56))     // distance 8, all in high half
	ix.Upsert(6, base^spread^(1<<33)) // distance 9: distractor
	ix.Upsert(7, ^base)               // distance 64
	ix.Upsert(8, base^0x1FF)          // distance 9: distractor, one half

	hits, _ := ix.Query(base)
	only(t, hits, map[uint64]int{1: 0, 2: 8, 4: 8, 5: 8})

	// Replace version 2 with a far fingerprint: old value must stop matching.
	ix.Upsert(2, ^base)
	hits, _ = ix.Query(base)
	only(t, hits, map[uint64]int{1: 0, 4: 8, 5: 8})
	// The retired fingerprint of version 2 must not bring it back.
	hits, _ = ix.Query(base ^ spread)
	for _, h := range hits {
		if h.VersionID == 2 {
			t.Fatalf("stale fingerprint of replaced version 2 still matches")
		}
	}

	// Remove version 4, then reuse its ID with a new near fingerprint.
	ix.Remove(4)
	hits, _ = ix.Query(base)
	only(t, hits, map[uint64]int{1: 0, 5: 8})
	ix.Upsert(4, base^spread)
	hits, _ = ix.Query(base)
	only(t, hits, map[uint64]int{1: 0, 4: 8, 5: 8})

	// Remove version 1 (exact match) and re-add with same fingerprint:
	// bucket reuse must not duplicate or lose it.
	ix.Remove(1)
	ix.Upsert(1, base)
	hits, _ = ix.Query(base)
	only(t, hits, map[uint64]int{1: 0, 4: 8, 5: 8})

	// Every probe must exactly match a naive scan of the live records;
	// in particular distance-9 distractors (3, 6, 8) stay out of probes
	// near base, and nothing stale remains anywhere.
	model := map[uint64]uint64{
		1: base,
		2: ^base,
		3: base ^ spread ^ 0x02,
		4: base ^ spread,
		5: base ^ (0xFF << 56),
		6: base ^ spread ^ (1 << 33),
		7: ^base,
		8: base ^ 0x1FF,
	}
	for _, probe := range []uint64{base, base ^ spread, base ^ 0xFF, 0, ^uint64(0)} {
		hits, _ := ix.Query(probe)
		want := map[uint64]int{}
		for id, fp := range model {
			if d := bits.OnesCount64(probe ^ fp); d <= maxRadius {
				want[id] = d
			}
		}
		only(t, hits, want)
	}
}

// TestSharedFingerprintLifecycle covers many versions on one fingerprint
// with interleaved removals, ensuring bucket entries stay in sync.
func TestSharedFingerprintLifecycle(t *testing.T) {
	ix := New()
	fp := uint64(0xAAAAAAAAAAAAAAAA)
	for id := uint64(1); id <= 10; id++ {
		ix.Upsert(id, fp)
	}
	hits, _ := ix.Query(fp)
	if len(hits) != 10 {
		t.Fatalf("got %d hits, want 10", len(hits))
	}
	for id := uint64(1); id <= 10; id += 2 {
		ix.Remove(id)
	}
	hits, _ = ix.Query(fp)
	only(t, hits, map[uint64]int{2: 0, 4: 0, 6: 0, 8: 0, 10: 0})
	// Move one survivor away and back.
	ix.Upsert(2, 0)
	hits, _ = ix.Query(fp)
	only(t, hits, map[uint64]int{4: 0, 6: 0, 8: 0, 10: 0})
	ix.Upsert(2, fp)
	hits, _ = ix.Query(fp)
	only(t, hits, map[uint64]int{2: 0, 4: 0, 6: 0, 8: 0, 10: 0})
}
