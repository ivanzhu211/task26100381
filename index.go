// Package pixelforge implements exact near-duplicate retrieval over
// 64-bit perceptual fingerprints using multi-index hashing.
//
// A fingerprint is split into 4 bands of 16 bits. Any two
// fingerprints within Hamming distance 8 share at least one band
// within distance 2 (pigeonhole), so a query enumerates every band
// key within distance 2 of each query band and fully evaluates only
// the versions in the matching buckets. The index is exact: results
// always match a brute-force scan of all current versions.
package pixelforge

import "math/bits"

// Hit is a single query result: a version and its exact Hamming
// distance to the query fingerprint.
type Hit struct {
	VersionID uint64
	Distance  int
}

// Index stores version fingerprints in memory and answers
// Hamming-distance range queries. It is not safe for concurrent use.
type Index struct {
	fingerprints map[uint64]uint64
	bands        [numBands]map[uint16][]uint64
	visited      map[uint64]struct{}
	neighbors    []uint16
}

// New returns an empty Index.
func New() *Index {
	ix := &Index{
		fingerprints: make(map[uint64]uint64),
		visited:      make(map[uint64]struct{}),
	}
	for b := 0; b < numBands; b++ {
		ix.bands[b] = make(map[uint16][]uint64)
	}
	return ix
}

// Len reports the number of registered versions.
func (ix *Index) Len() int {
	return len(ix.fingerprints)
}

// Upsert registers versionID with fingerprint, replacing the previous
// fingerprint if versionID already exists.
func (ix *Index) Upsert(versionID uint64, fingerprint uint64) {
	if old, ok := ix.fingerprints[versionID]; ok {
		if old == fingerprint {
			return
		}
		ix.removeFromBands(versionID, old)
	}
	ix.fingerprints[versionID] = fingerprint
	for b := 0; b < numBands; b++ {
		key := bandKey(fingerprint, b)
		ix.bands[b][key] = append(ix.bands[b][key], versionID)
	}
}

// Remove deletes versionID from the index. Removing an unknown
// versionID is a no-op.
func (ix *Index) Remove(versionID uint64) {
	fp, ok := ix.fingerprints[versionID]
	if !ok {
		return
	}
	ix.removeFromBands(versionID, fp)
	delete(ix.fingerprints, versionID)
}

func (ix *Index) removeFromBands(versionID uint64, fingerprint uint64) {
	for b := 0; b < numBands; b++ {
		key := bandKey(fingerprint, b)
		bucket := ix.bands[b][key]
		for i, id := range bucket {
			if id == versionID {
				bucket[i] = bucket[len(bucket)-1]
				bucket = bucket[:len(bucket)-1]
				break
			}
		}
		if len(bucket) == 0 {
			delete(ix.bands[b], key)
		} else {
			ix.bands[b][key] = bucket
		}
	}
}

// Query returns every version whose fingerprint is within maxDistance
// Hamming distance of fingerprint, together with the number of full
// 64-bit distance evaluations performed. Result order is unspecified.
func (ix *Index) Query(fingerprint uint64) (hits []Hit, evaluations uint64) {
	for id := range ix.visited {
		delete(ix.visited, id)
	}
	ix.neighbors = ix.neighbors[:0]
	for b := 0; b < numBands; b++ {
		ix.neighbors = neighborsWithin(ix.neighbors, bandKey(fingerprint, b))
		for _, key := range ix.neighbors {
			for _, id := range ix.bands[b][key] {
				if _, seen := ix.visited[id]; seen {
					continue
				}
				ix.visited[id] = struct{}{}
				evaluations++
				d := bits.OnesCount64(ix.fingerprints[id] ^ fingerprint)
				if d <= maxDistance {
					hits = append(hits, Hit{VersionID: id, Distance: d})
				}
			}
		}
		ix.neighbors = ix.neighbors[:0]
	}
	return hits, evaluations
}
