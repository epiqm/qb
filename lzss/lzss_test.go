// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package lzss

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	inputs := [][]byte{nil, []byte("a"), []byte("   "), bytes.Repeat([]byte("abc"), 5000)}
	for _, n := range []int{17, 18, 19, 4095, 4096, 4097, 70000} {
		b := make([]byte, n)
		for i := range b {
			b[i] = "ab \x00\xff"[rng.Intn(5)] // compressible, with matches across the ring buffer
		}
		inputs = append(inputs, b)
	}
	for _, in := range inputs {
		out, err := Decompress(Compress(in), len(in))
		if err != nil || !bytes.Equal(out, in) {
			t.Errorf("%d bytes: round trip failed (%v)", len(in), err)
		}
	}
	if _, err := Decompress([]byte{0x00, 0x01}, 10); err == nil {
		t.Error("truncated stream decoded without an error")
	}
}
