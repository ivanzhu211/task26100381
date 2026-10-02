package pixelforge

// numBands is the number of bit bands a 64-bit fingerprint is split
// into. With 4 bands of 16 bits, any pair within Hamming distance 8
// has at least one band within distance 2 (pigeonhole).
const numBands = 4

// bandBits is the width of each band in bits.
const bandBits = 64 / numBands

// maxBandDist is the per-band distance threshold used to generate
// candidate band keys.
const maxBandDist = 2

// maxDistance is the Hamming distance threshold answered by Query.
const maxDistance = 8

// bandKey extracts the key of band b from fingerprint.
func bandKey(fingerprint uint64, b int) uint16 {
	return uint16(fingerprint >> (uint(b) * bandBits))
}

// neighborsWithin appends every bandBits-wide key within Hamming
// distance maxBandDist of key to dst and returns the result.
// For 16-bit bands and distance 2 this yields 1 + 16 + 120 = 137 keys.
func neighborsWithin(dst []uint16, key uint16) []uint16 {
	dst = append(dst, key)
	for i := 0; i < bandBits; i++ {
		dst = append(dst, key^(1<<uint(i)))
	}
	for i := 0; i < bandBits; i++ {
		for j := i + 1; j < bandBits; j++ {
			dst = append(dst, key^(1<<uint(i))^(1<<uint(j)))
		}
	}
	return dst
}
