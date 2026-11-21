// Package crdt implements the server side of Actual's CRDT: the HULC
// timestamps, the murmur3 hash used by them, and the merkle trie that tracks
// which minutes a client has seen.
//
// The JS implementation in packages/crdt is the reference. Where JS relies on
// int32 wraparound or sparse objects, this package reproduces those quirks
// rather than doing what Go would consider obvious.
package crdt

import (
	"encoding/binary"
	"math/bits"
)

// Murmur3 returns the MurmurHash3 x86_32 of data with seed 0, matching
// `murmurhash@2`'s `v3()` which the JS CRDT uses.
func Murmur3(data []byte) uint32 {
	const (
		c1 = 0xcc9e2d51
		c2 = 0x1b873593
	)

	h1 := uint32(0)

	blocks := len(data) / 4
	for i := 0; i < blocks; i++ {
		k1 := binary.LittleEndian.Uint32(data[i*4:])

		k1 *= c1
		k1 = bits.RotateLeft32(k1, 15)
		k1 *= c2

		h1 ^= k1
		h1 = bits.RotateLeft32(h1, 13)
		h1 = h1*5 + 0xe6546b64
	}

	var k1 uint32
	switch len(data) % 4 {
	case 3:
		k1 ^= uint32(data[blocks*4+2]) << 16
		fallthrough
	case 2:
		k1 ^= uint32(data[blocks*4+1]) << 8
		fallthrough
	case 1:
		k1 ^= uint32(data[blocks*4])
		k1 *= c1
		k1 = bits.RotateLeft32(k1, 15)
		k1 *= c2
		h1 ^= k1
	}

	h1 ^= uint32(len(data))

	// fmix32
	h1 ^= h1 >> 16
	h1 *= 0x85ebca6b
	h1 ^= h1 >> 13
	h1 *= 0xc2b2ae35
	h1 ^= h1 >> 16

	return h1
}

// Murmur3String hashes the UTF-8 bytes of s.
func Murmur3String(s string) uint32 {
	return Murmur3([]byte(s))
}
