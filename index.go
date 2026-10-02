// Package pixelforge implements an in-memory near-duplicate retrieval
// index over trusted 64-bit perceptual fingerprints.
//
// Query returns every registered version whose fingerprint is within
// Hamming distance maxRadius of the query, together with exact distances
// and the number of full fingerprint distance evaluations performed.
package pixelforge

import "math/bits"

const (
	// maxRadius is the retrieval radius: queries return all versions
	// within this Hamming distance.
	maxRadius = 8
	// halfBits is the width of each index substring (64 / 2 halves).
	halfBits = 32
	// halfRadius is floor(maxRadius / 2). If two 64-bit fingerprints
	// differ in at most maxRadius bits, then at least one of their two
	// 32-bit halves differs in at most halfRadius bits (pigeonhole).
	halfRadius = 4
)

// Hit is a single query result: a version and its exact Hamming distance
// to the query fingerprint.
type Hit struct {
	VersionID uint64
	Distance  int
}

// Index is an in-memory multi-index hash (MIH) over 64-bit fingerprints.
//
// The 64-bit fingerprint is split into a low and a high 32-bit half.
// Each half has its own hash table mapping the 32-bit substring to the
// set of version IDs carrying it. A query enumerates every 32-bit key
// within halfRadius of each query half, gathers the union of the matching
// buckets as candidates, and exactly evaluates each distinct candidate
// once. Completeness follows from the pigeonhole principle; exactness
// from the final full 64-bit distance check.
//
// It is not safe for concurrent use; callers must serialize calls.
type Index struct {
	fps map[uint64]uint64   // versionID -> current fingerprint
	lo  map[uint32][]uint64 // low-half substring -> version IDs
	hi  map[uint32][]uint64 // high-half substring -> version IDs
}

// New returns an empty Index.
func New() *Index {
	return &Index{
		fps: make(map[uint64]uint64),
		lo:  make(map[uint32][]uint64),
		hi:  make(map[uint32][]uint64),
	}
}

// Upsert registers versionID with fingerprint, replacing the previous
// fingerprint if versionID already exists. The old fingerprint stops
// matching immediately.
func (ix *Index) Upsert(versionID uint64, fingerprint uint64) {
	if old, ok := ix.fps[versionID]; ok {
		if old == fingerprint {
			return
		}
		ix.removeFromBuckets(versionID, old)
	}
	ix.fps[versionID] = fingerprint
	loKey := uint32(fingerprint)
	hiKey := uint32(fingerprint >> halfBits)
	ix.lo[loKey] = append(ix.lo[loKey], versionID)
	ix.hi[hiKey] = append(ix.hi[hiKey], versionID)
}

// Remove deletes versionID from the index. Removing an unknown ID is a no-op.
func (ix *Index) Remove(versionID uint64) {
	fp, ok := ix.fps[versionID]
	if !ok {
		return
	}
	ix.removeFromBuckets(versionID, fp)
	delete(ix.fps, versionID)
}

func (ix *Index) removeFromBuckets(versionID uint64, fp uint64) {
	ix.lo = removeFromBucket(ix.lo, uint32(fp), versionID)
	ix.hi = removeFromBucket(ix.hi, uint32(fp>>halfBits), versionID)
}

// removeFromBucket deletes one occurrence of id from table[key], dropping
// the bucket entirely when it becomes empty so memory stays proportional
// to the number of live records.
func removeFromBucket(table map[uint32][]uint64, key uint32, id uint64) map[uint32][]uint64 {
	bucket := table[key]
	for i, v := range bucket {
		if v == id {
			bucket[i] = bucket[len(bucket)-1]
			bucket = bucket[:len(bucket)-1]
			break
		}
	}
	if len(bucket) == 0 {
		delete(table, key)
	} else {
		table[key] = bucket
	}
	return table
}

// Query returns all versions within Hamming distance maxRadius of
// fingerprint, and the number of full 64-bit distance evaluations done.
// Result order is unspecified. Each candidate version is evaluated at
// most once per query.
func (ix *Index) Query(fingerprint uint64) ([]Hit, int) {
	candidates := make(map[uint64]struct{})
	eachNeighbor(uint32(fingerprint), func(key uint32) {
		for _, id := range ix.lo[key] {
			candidates[id] = struct{}{}
		}
	})
	eachNeighbor(uint32(fingerprint>>halfBits), func(key uint32) {
		for _, id := range ix.hi[key] {
			candidates[id] = struct{}{}
		}
	})

	var hits []Hit
	evals := 0
	for id := range candidates {
		fp := ix.fps[id]
		evals++
		d := bits.OnesCount64(fingerprint ^ fp)
		if d <= maxRadius {
			hits = append(hits, Hit{VersionID: id, Distance: d})
		}
	}
	return hits, evals
}

// Len returns the number of registered versions.
func (ix *Index) Len() int {
	return len(ix.fps)
}

// eachNeighbor calls yield for every 32-bit key within Hamming distance
// halfRadius of v, including v itself.
func eachNeighbor(v uint32, yield func(uint32)) {
	yield(v)
	for d := 1; d <= halfRadius; d++ {
		eachMask(d, func(mask uint32) {
			yield(v ^ mask)
		})
	}
}

// eachMask calls yield for every uint32 with exactly d bits set, using
// Gosper's hack to step through same-popcount combinations.
func eachMask(d int, yield func(uint32)) {
	// 64-bit arithmetic so the terminal overflow past bit 31 ends the loop.
	const limit = uint64(1) << 32
	mask := uint64(1)<<d - 1
	for mask < limit {
		yield(uint32(mask))
		c := mask & -mask
		r := mask + c
		mask = (((r ^ mask) >> 2) / c) | r
	}
}
