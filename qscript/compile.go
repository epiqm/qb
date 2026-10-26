// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package qscript

import (
	"encoding/binary"
	"fmt"
	"math"
)

type compiler struct {
	lineNumber int // inline scripts: the number of the line being compiled
	props      targetProps
	tokens     []lexToken
	pos        int
	code       []byte
	line       int
	checksum   map[uint32]string // checksum -> first name seen
	order      []uint32          // checksum table order (first appearance)
}

// Compile turns .q source into .qb bytecode for the target. A THAW file gets the file
// id from its //#fileid line, else 0; see CompileTHAW.
func Compile(source string, target Target) ([]byte, error) {
	return compile(source, target, 0)
}

// CompileTHAW compiles a THAW .qb file, with fileID unless the source has a //#fileid
// line. FileID gives the id the original tools would use for a path.
func CompileTHAW(source string, fileID uint32) ([]byte, error) {
	return compile(source, THAW, fileID)
}

// THAWBuildRoot is the directory the THAW tools ran in; file ids and archive entry
// checksums are taken of paths under it.
const THAWBuildRoot = `f:\projects\aspyr\thaw\skate7\data\`

// FileID returns the id of a THAW .qb file at path (e.g. scripts\game\game.qb).
func FileID(path string) uint32 { return CRC(THAWBuildRoot + path) }

func compile(source string, target Target, fileID uint32) (code []byte, err error) {
	props, ok := targetPropsTable[target]
	if !ok {
		return nil, fmt.Errorf("invalid target %d", target)
	}
	tokens, err := lex(source)
	if err != nil {
		return nil, err
	}
	c := &compiler{props: props, tokens: tokens, checksum: map[uint32]string{}}
	c.code = make([]byte, 0, len(source)/2)
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(qbError)
			if !ok {
				panic(r)
			}
			err = e.err
			if c.line > 0 {
				err = fmt.Errorf("line %d: %w", c.line, e.err)
			}
		}
	}()
	if props.thaw {
		return c.thawFile(source, fileID), nil
	}
	return c.compile(), nil
}

func (c *compiler) token(t Token) { c.code = append(c.code, byte(t)) }

func (c *compiler) short(v int64) { c.code = binary.LittleEndian.AppendUint16(c.code, uint16(v)) }

func (c *compiler) int(v int64) { c.code = binary.LittleEndian.AppendUint32(c.code, uint32(v)) }

func (c *compiler) real(v float32) { c.int(int64(math.Float32bits(v))) }

func (c *compiler) cstring(s string) {
	c.code = append(c.code, s...)
	c.code = append(c.code, 0)
}

// setAddress patches a 32-bit jump at 'at' to land on 'address' (relative to the end of the jump).
func (c *compiler) setAddress(at, address int) {
	binary.LittleEndian.PutUint32(c.code[at:], uint32(int32(address-(at+4))))
}

// setShortAddress patches a 16-bit jump at 'at' to land on 'address' (relative to 'at').
func (c *compiler) setShortAddress(at, address int) {
	binary.LittleEndian.PutUint16(c.code[at:], uint16(int16(address-at)))
}

func (c *compiler) canPop() bool { return c.pos < len(c.tokens) }

func (c *compiler) pop() lexToken {
	if !c.canPop() {
		fail("unexpected end of script")
	}
	t := c.tokens[c.pos]
	c.pos++
	c.line = int(t.line)
	return t
}

func (c *compiler) expect(t Token, what string) {
	if c.pop().typ != t {
		fail("expected '%s'", what)
	}
}

func (c *compiler) popReal() float32 {
	switch t := c.pop(); t.typ {
	case TokInteger, TokHexInteger:
		return float32(t.num)
	case TokFloat:
		return float32(t.real())
	}
	fail("expected integer or float")
	return 0
}

func (c *compiler) addName(name string) uint32 {
	crc := CRC(name)
	if prev, ok := c.checksum[crc]; ok {
		if !sameName(prev, name) {
			fail("checksum collision (%s == %s)", prev, name)
		}
	} else {
		c.checksum[crc] = name
		c.order = append(c.order, crc)
	}
	return crc
}

func (c *compiler) compile() []byte {
	type randomBlock struct {
		address, jump, numJumps int
		endJumps                []int
	}
	var randoms []*randomBlock
	var shorts []int     // FastIf/FastElse addresses
	var switches [][]int // Case/Default addresses per SWITCH

	for c.canPop() {
		t := c.pop()
		switch t.typ {
		case TokSwitch:
			if c.props.fastIfElseCase {
				switches = append(switches, nil)
			}
			c.token(TokSwitch)

		case TokEndSwitch:
			if c.props.fastIfElseCase {
				if len(switches) == 0 {
					fail("unexpected 'ENDSWITCH' (no 'SWITCH')")
				}
				cases := switches[len(switches)-1]
				switches = switches[:len(switches)-1]
				for i, at := range cases {
					// Each case's short jump goes to the next case, the last one to ENDSWITCH
					if i != len(cases)-1 {
						c.setShortAddress(at+2, cases[i+1])
					} else {
						c.setShortAddress(at+2, len(c.code))
					}
					// The jump ending the previous case goes past ENDSWITCH
					if i != 0 {
						c.setShortAddress(at-2, len(c.code)+1)
					}
				}
			}
			c.token(TokEndSwitch)

		case TokCase, TokDefault:
			if c.props.fastIfElseCase {
				if len(switches) == 0 {
					fail("unexpected 'CASE' or 'DEFAULT' (no 'SWITCH')")
				}
				top := &switches[len(switches)-1]
				if len(*top) > 0 {
					c.token(TokShortJump)
					c.short(0)
				}
				*top = append(*top, len(c.code))
				c.token(t.typ)
				c.token(TokShortJump)
				c.short(0)
			} else {
				c.token(t.typ)
			}

		case TokIf:
			if c.props.fastIfElseCase {
				shorts = append(shorts, len(c.code))
				c.token(TokFastIf)
				c.short(0)
			} else {
				c.token(TokIf)
			}

		case TokElse:
			if c.props.fastIfElseCase {
				if len(shorts) == 0 || Token(c.code[shorts[len(shorts)-1]]) != TokFastIf {
					fail("unexpected 'ELSE' (no 'IF')")
				}
				c.setShortAddress(shorts[len(shorts)-1]+1, len(c.code)+3)
				shorts[len(shorts)-1] = len(c.code)
				c.token(TokFastElse)
				c.short(0)
			} else {
				c.token(TokElse)
			}

		case TokEndIf:
			if c.props.fastIfElseCase {
				if len(shorts) == 0 {
					fail("unexpected 'ENDIF' (no 'IF' or 'ELSE')")
				}
				at := shorts[len(shorts)-1]
				shorts = shorts[:len(shorts)-1]
				c.setShortAddress(at+1, len(c.code)+1)
			}
			c.token(TokEndIf)

		case TokName, TokArg:
			crc := c.addName(t.str)
			if t.typ == TokArg {
				c.token(TokArg)
			}
			c.token(TokName)
			c.int(int64(crc))

		case tokNameChecksum, tokArgChecksum:
			if t.typ == tokArgChecksum {
				c.token(TokArg)
			}
			c.token(TokName)
			c.int(t.num)

		case TokString, TokLocalString:
			c.token(t.typ)
			c.int(int64(len(t.str) + 1))
			c.cstring(t.str)

		case TokInteger, TokHexInteger:
			c.token(t.typ)
			c.int(t.num)

		case TokFloat:
			c.token(TokFloat)
			c.real(float32(t.real()))

		case TokPair:
			c.expect(TokOpenParenth, "(")
			x := c.popReal()
			c.expect(TokComma, ",")
			y := c.popReal()
			c.expect(TokCloseParenth, ")")
			c.token(TokPair)
			c.real(x)
			c.real(y)

		case TokVector:
			c.expect(TokOpenParenth, "(")
			x := c.popReal()
			c.expect(TokComma, ",")
			y := c.popReal()
			c.expect(TokComma, ",")
			z := c.popReal()
			c.expect(TokCloseParenth, ")")
			c.token(TokVector)
			c.real(x)
			c.real(y)
			c.real(z)

		case TokEndOfLine:
			if c.props.inlineScript {
				// The number of the line that starts here: from //#line, else counted
				if c.lineNumber++; t.num != 0 {
					c.lineNumber = int(t.num)
				} else if c.lineNumber == 1 {
					c.lineNumber = int(t.line) + 1
				}
				c.token(TokEndOfLineNumber)
				c.int(int64(c.lineNumber))
				continue
			}
			// Don't add multiple end of lines in a row (THAW keeps them)
			c.token(TokEndOfLine)
			for !c.props.thaw && c.canPop() && c.tokens[c.pos].typ == TokEndOfLine {
				c.pop()
			}

		case TokRandom, TokRandom2, TokRandomNoRepeat, TokRandomPermute:
			c.expect(TokOpenParenth, "(")
			var weights []int64
			for {
				w := c.pop()
				if w.typ == TokCloseParenth {
					break
				}
				switch w.typ {
				case TokInteger, TokHexInteger:
					weights = append(weights, w.num)
				case TokFloat:
					weights = append(weights, int64(math.Floor(w.real())))
				default:
					fail("expected integer or float")
				}
				if sep := c.pop(); sep.typ == TokCloseParenth {
					break
				} else if sep.typ != TokComma {
					fail("expected ',' or ')'")
				}
			}
			randoms = append(randoms, &randomBlock{address: len(c.code), numJumps: len(weights)})
			c.token(t.typ)
			c.int(int64(len(weights)))
			for _, w := range weights {
				c.short(w)
			}
			for range weights {
				c.int(0)
			}

		case tokRandomCase:
			if len(randoms) == 0 {
				fail("unexpected 'RANDOMCASE' (no random)")
			}
			r := randoms[len(randoms)-1]
			if r.jump >= r.numJumps {
				fail("unexpected 'RANDOMCASE' (more cases than weights)")
			}
			// Every case after the first ends the previous one with a jump to RANDOMEND
			if r.jump != 0 {
				r.endJumps = append(r.endJumps, len(c.code))
				c.token(TokJump)
				c.int(0)
			}
			c.setAddress(r.address+1+4+2*r.numJumps+4*r.jump, len(c.code))
			r.jump++

		case tokRandomEnd:
			if len(randoms) == 0 {
				fail("unexpected 'RANDOMEND' (no random)")
			}
			r := randoms[len(randoms)-1]
			if r.jump != r.numJumps {
				fail("unexpected 'RANDOMEND' (random was incomplete)")
			}
			for _, at := range r.endJumps {
				c.setAddress(at+1, len(c.code))
			}
			randoms = randoms[:len(randoms)-1]

		case TokStartStruct:
			if c.props.thaw && c.binaryStruct() {
				c.binaryStructToken()
			} else {
				c.token(TokStartStruct)
			}

		case tokLabel:
			fail("labels are not supported (%s:)", t.str)

		case TokJump:
			fail("JUMP is not supported")

		default:
			c.token(t.typ)
		}
	}

	c.line = 0
	if len(switches) > 0 {
		fail("unexpected end of script (missing 'ENDSWITCH')")
	}
	if len(randoms) > 0 {
		fail("unexpected end of script (missing 'RANDOMEND')")
	}
	if len(shorts) > 0 {
		fail("unexpected end of script (missing 'ENDIF')")
	}

	if c.props.thaw {
		// THAW scripts end at ENDSCRIPT, and the names go to .dbg files
		return c.code
	}

	// Checksum -> name table
	for _, crc := range c.order {
		c.token(TokChecksumName)
		c.int(int64(crc))
		c.cstring(c.checksum[crc])
	}
	c.token(TokEndOfFile)
	return c.code
}
