// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

// Package lzss implements the LZSS variant of Neversoft archives and THAW scripts
// (Haruhiko Okumura's LZSS.C): a 4096-byte ring buffer pre-filled with spaces, matches
// of 3..18 bytes.
package lzss

import "errors"

const (
	ringSize  = 4096 // N, size of ring buffer
	matchMax  = 18   // F, upper limit for match length
	threshold = 2    // encode string into position and length if match length is greater than this
	nilNode   = ringSize
)

// Decompress decodes an LZSS stream into a buffer of size bytes.
func Decompress(in []byte, size int) ([]byte, error) {
	out := make([]byte, 0, size)
	var text [ringSize]byte
	for i := range ringSize - matchMax {
		text[i] = ' '
	}
	r := ringSize - matchMax
	flags := uint(0)
	for p := 0; ; {
		if flags >>= 1; flags&256 == 0 {
			if p >= len(in) {
				break
			}
			flags = uint(in[p]) | 0xff00 // uses higher byte to count eight
			p++
		}
		if flags&1 != 0 {
			if p >= len(in) {
				break
			}
			c := in[p]
			p++
			out = append(out, c)
			text[r] = c
			r = (r + 1) & (ringSize - 1)
		} else {
			if p >= len(in) {
				break
			}
			if p+1 >= len(in) {
				return nil, errors.New("lzss: truncated stream")
			}
			i := int(in[p]) | int(in[p+1]&0xf0)<<4 // 12 bit offset
			j := int(in[p+1]&0x0f) + threshold     // 4 bit length (above the threshold)
			p += 2
			for k := 0; k <= j; k++ {
				c := text[(i+k)&(ringSize-1)]
				out = append(out, c)
				text[r] = c
				r = (r + 1) & (ringSize - 1)
			}
		}
	}
	if len(out) != size {
		return nil, errors.New("lzss: decompressed size mismatch")
	}
	return out, nil
}

// encoder holds the binary search trees of the reference encoder.
type encoder struct {
	text                       [ringSize + matchMax - 1]byte
	lson                       [ringSize + 1]int
	rson                       [ringSize + 257]int
	dad                        [ringSize + 1]int
	matchPosition, matchLength int
}

// Compress encodes data with the same algorithm (and output) as the original tools.
func Compress(data []byte) []byte {
	e := &encoder{}
	for i := ringSize + 1; i <= ringSize+256; i++ {
		e.rson[i] = nilNode
	}
	for i := range ringSize {
		e.dad[i] = nilNode
	}

	var out []byte
	var code [17]byte
	codePtr, mask := 1, byte(1)
	s, r := 0, ringSize-matchMax
	for i := s; i < r; i++ {
		e.text[i] = ' '
	}
	in := 0
	length := 0
	for ; length < matchMax && in < len(data); length++ {
		e.text[r+length] = data[in]
		in++
	}
	if length == 0 {
		return out
	}
	for i := 1; i <= matchMax; i++ {
		e.insert(r - i)
	}
	e.insert(r)
	for length > 0 {
		if e.matchLength > length {
			e.matchLength = length
		}
		if e.matchLength <= threshold {
			e.matchLength = 1
			code[0] |= mask
			code[codePtr] = e.text[r]
			codePtr++
		} else {
			code[codePtr] = byte(e.matchPosition)
			code[codePtr+1] = byte((e.matchPosition>>4)&0xf0 | (e.matchLength - (threshold + 1)))
			codePtr += 2
		}
		if mask <<= 1; mask == 0 {
			out = append(out, code[:codePtr]...)
			code[0] = 0
			codePtr, mask = 1, 1
		}
		last := e.matchLength
		i := 0
		for ; i < last && in < len(data); i++ {
			c := data[in]
			in++
			e.delete(s)
			e.text[s] = c
			if s < matchMax-1 {
				e.text[s+ringSize] = c
			}
			s = (s + 1) & (ringSize - 1)
			r = (r + 1) & (ringSize - 1)
			e.insert(r)
		}
		for ; i < last; i++ {
			e.delete(s)
			s = (s + 1) & (ringSize - 1)
			r = (r + 1) & (ringSize - 1)
			if length--; length != 0 {
				e.insert(r)
			}
		}
	}
	if codePtr > 1 {
		out = append(out, code[:codePtr]...)
	}
	return out
}

// insert adds text[r..r+F-1] to its tree and records the longest match.
func (e *encoder) insert(r int) {
	cmp := 1
	key := e.text[r:]
	p := ringSize + 1 + int(key[0])
	e.rson[r], e.lson[r] = nilNode, nilNode
	e.matchLength = 0
	for {
		if cmp >= 0 {
			if e.rson[p] != nilNode {
				p = e.rson[p]
			} else {
				e.rson[p] = r
				e.dad[r] = p
				return
			}
		} else {
			if e.lson[p] != nilNode {
				p = e.lson[p]
			} else {
				e.lson[p] = r
				e.dad[r] = p
				return
			}
		}
		i := 1
		for ; i < matchMax; i++ {
			if cmp = int(key[i]) - int(e.text[p+i]); cmp != 0 {
				break
			}
		}
		if i > e.matchLength {
			e.matchPosition = p
			if e.matchLength = i; i >= matchMax {
				break
			}
		}
	}
	e.dad[r] = e.dad[p]
	e.lson[r] = e.lson[p]
	e.rson[r] = e.rson[p]
	e.dad[e.lson[p]] = r
	e.dad[e.rson[p]] = r
	if e.rson[e.dad[p]] == p {
		e.rson[e.dad[p]] = r
	} else {
		e.lson[e.dad[p]] = r
	}
	e.dad[p] = nilNode
}

// delete removes node p from its tree.
func (e *encoder) delete(p int) {
	if e.dad[p] == nilNode {
		return
	}
	var q int
	if e.rson[p] == nilNode {
		q = e.lson[p]
	} else if e.lson[p] == nilNode {
		q = e.rson[p]
	} else {
		q = e.lson[p]
		if e.rson[q] != nilNode {
			for e.rson[q] != nilNode {
				q = e.rson[q]
			}
			e.rson[e.dad[q]] = e.lson[q]
			e.dad[e.lson[q]] = e.dad[q]
			e.lson[q] = e.lson[p]
			e.dad[e.lson[p]] = q
		}
		e.rson[q] = e.rson[p]
		e.dad[e.rson[p]] = q
	}
	e.dad[q] = e.dad[p]
	if e.rson[e.dad[p]] == p {
		e.rson[e.dad[p]] = q
	} else {
		e.lson[e.dad[p]] = q
	}
	e.dad[p] = nilNode
}
