// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package qscript

import (
	"encoding/binary"
	"fmt"
	"math"
)

// qbError is panicked by the bytecode readers and recovered at the API boundary.
type qbError struct{ err error }

func fail(format string, args ...any) {
	panic(qbError{fmt.Errorf(format, args...)})
}

func recoverError(err *error) {
	if r := recover(); r != nil {
		e, ok := r.(qbError)
		if !ok {
			panic(r)
		}
		*err = e.err
	}
}

// reader reads tokens from .qb bytecode.
type reader struct {
	data      []byte
	alignBase int  // TokStruct data is 4-byte aligned relative to this position
	thaw      bool // THAW script code
	end       int  // the furthest position read
}

func (r *reader) need(pos, n int, what string) {
	if pos < 0 {
		fail("[%s] unexpected start of file", what)
	}
	if pos+n > len(r.data) {
		fail("[%s] unexpected end of file", what)
	}
	r.end = max(r.end, pos+n)
}

func (r *reader) u32(pos int) uint32 {
	r.need(pos, 4, "GetUnsignedInteger")
	return binary.LittleEndian.Uint32(r.data[pos:])
}

func (r *reader) s32(pos int) int32 { return int32(r.u32(pos)) }

func (r *reader) u16(pos int) uint16 {
	r.need(pos, 2, "GetUnsignedShort")
	return binary.LittleEndian.Uint16(r.data[pos:])
}

func (r *reader) f32(pos int) float32 { return math.Float32frombits(r.u32(pos)) }

// address reads a 32-bit jump offset, relative to the end of the offset.
func (r *reader) address(pos int) int { return pos + 4 + int(r.s32(pos)) }

// cstring reads a NUL-terminated string.
func (r *reader) cstring(pos int) (string, int) {
	for end := pos; end < len(r.data); end++ {
		if r.data[end] == 0 {
			r.end = max(r.end, end+1)
			return string(r.data[pos:end]), end + 1
		}
	}
	fail("[SkipToken] unterminated string at %d", pos)
	return "", 0
}

// skip returns the position of the token after the one at pos, or -1 at EndOfFile.
func (r *reader) skip(pos int) int {
	r.need(pos, 1, "SkipToken")
	switch t := Token(r.data[pos]); t {
	case TokEndOfFile:
		return -1
	case TokEndOfLine, TokEquals, TokDot, TokComma, TokMinus, TokAdd, TokDivide, TokMultiply,
		TokOpenParenth, TokCloseParenth, TokSameAs, TokLessThan, TokLessThanEqual, TokGreaterThan,
		TokGreaterThanEqual, TokStartStruct, TokStartArray, TokEndStruct, TokEndArray,
		TokBegin, TokRepeat, TokBreak, TokScript, TokEndScript, TokIf, TokElse, TokElseIf,
		TokEndIf, TokReturn, TokAllArgs, TokArg, TokOr, TokAnd, TokXor, TokShiftLeft,
		TokShiftRight, TokRandomRange, TokRandomRange2, TokNot, TokKeywordAnd, TokKeywordOr,
		TokSwitch, TokEndSwitch, TokCase, TokDefault, TokColon:
		return pos + 1
	case TokName, TokInteger, TokHexInteger, TokFloat, TokEndOfLineNumber, TokJump:
		return pos + 5
	case TokVector:
		return pos + 13
	case TokPair:
		return pos + 9
	case TokString, TokLocalString:
		return pos + 5 + int(r.u32(pos+1))
	case TokChecksumName:
		r.need(pos, 5, "SkipToken")
		_, next := r.cstring(pos + 5)
		return next
	case TokRandom, TokRandom2, TokRandomNoRepeat, TokRandomPermute:
		n := int(r.u32(pos + 1))
		return pos + 5 + 2*n + 4*n
	case TokFastIf, TokFastElse, TokShortJump:
		return pos + 3
	case TokStruct:
		return r.structStart(pos) + int(r.u16(pos+1))
	default:
		fail("[SkipToken] unrecognized script token %d at %d", int(t), pos)
		return -1
	}
}

// structStart returns where the binary struct of the TokStruct at pos begins.
func (r *reader) structStart(pos int) int {
	return r.alignBase + (pos+3-r.alignBase+3)&^3
}

// labels finds the RANDOMCASE/RANDOMEND markers of every random block, by address.
func (r *reader) labels() map[int][]string {
	labels := map[int][]string{}
	for pos := 0; pos >= 0; pos = r.skip(pos) {
		switch Token(r.data[pos]) {
		case TokRandom, TokRandom2, TokRandomNoRepeat, TokRandomPermute:
			n := int(r.u32(pos + 1))
			if n == 0 {
				labels[pos+5] = append(labels[pos+5], "RANDOMEND")
				break
			}
			end := -1
			for i := range n {
				addr := r.address(pos + 5 + 2*n + 4*i)
				if n == 1 {
					labels[addr] = append(labels[addr], "RANDOMCASE RANDOMEND")
				} else {
					labels[addr] = append(labels[addr], "RANDOMCASE")
				}
				// Every case after the first is preceded by the previous case's jump to the end
				if i > 0 && end < 0 {
					p := addr - 5
					if p < 0 || p >= len(r.data) {
						fail("[GetLabels] unexpected end of file")
					}
					if Token(r.data[p]) == TokJump {
						end = r.address(p + 1)
					}
				}
			}
			if end >= 0 {
				labels[end] = append(labels[end], "RANDOMEND")
			}
		}
	}
	return labels
}

// checksumNames reads the checksum -> name table.
func (r *reader) checksumNames() map[uint32]string {
	names := map[uint32]string{}
	for pos := 0; pos >= 0; pos = r.skip(pos) {
		if Token(r.data[pos]) == TokChecksumName {
			name, _ := r.cstring(pos + 5)
			names[r.u32(pos+1)] = name
		}
	}
	return names
}
