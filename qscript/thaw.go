// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package qscript

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"regexp"
	"strconv"
	"strings"

	"qb/lzss"
)

// THAW .qb files are not one token stream. They hold typed sections, little endian:
//
//	u32 flags (0), u32 file size, 20 magic bytes
//	sections: [00 04 type 00], u32 name, u32 file id, u32 value or pointer, u32 0
//
// Scripts (type 7) point at u32 crc, u32 size, u32 compressed size, then the bytecode,
// LZSS compressed unless that doesn't make it smaller. The bytecode is THUG2's, plus
// TokStruct: a binary struct in the same format as the data sections. The file id is the
// checksum of the file's path under the original build tree.

var thawMagic = []byte{0x1C, 0x08, 0x02, 0x04, 0x10, 0x04, 0x08, 0x0C, 0x0C, 0x08, 0x02, 0x04, 0x14, 0x02, 0x04, 0x0C, 0x10, 0x10, 0x0C, 0x00}

const thawHeaderSize = 28

// IsTHAW reports whether data is a THAW .qb file.
func IsTHAW(data []byte) bool {
	return len(data) >= thawHeaderSize && bytes.Equal(data[8:28], thawMagic)
}

// Symbol types of THAW values
const (
	symNone        = 0
	symInteger     = 1
	symFloat       = 2
	symString      = 3
	symLocalString = 4
	symPair        = 5
	symVector      = 6
	symScript      = 7
	symStruct      = 10
	symArray       = 12
	symName        = 13
)

// qbValue is a THAW value.
type qbValue struct {
	typ    byte
	num    uint32 // Integer, Float bits, Name checksum
	str    string // String, LocalString
	floats []float32
	items  []qbItem  // Struct
	elems  []qbValue // Array
	elem   byte      // Array element type
	code   []byte    // Script (inline, in a struct): bytecode padded to 4 bytes
}

type qbItem struct {
	name uint32 // 0: unnamed
	val  qbValue
}

// inline reports whether a value of this type is stored in place of its pointer.
func inline(typ byte) bool { return typ == symInteger || typ == symFloat || typ == symName }

// qbReader reads THAW values; pointers are relative to base.
type qbReader struct {
	*reader
	base int
}

func (r *qbReader) ptr(pos int) int { return r.base + int(r.u32(pos)) }

// value reads the value of type typ whose value/pointer dword is at pos.
func (r *qbReader) value(typ byte, pos int) qbValue {
	v := qbValue{typ: typ}
	if inline(typ) {
		v.num = r.u32(pos)
		return v
	}
	p := r.ptr(pos)
	switch typ {
	case symString, symLocalString:
		v.str, _ = r.cstring(p)
	case symPair, symVector:
		n := 2
		if typ == symVector {
			n = 3
		}
		for i := range n {
			v.floats = append(v.floats, r.f32(p+4+4*i))
		}
	case symStruct:
		v.items = r.structItems(p)
	case symArray:
		v = r.array(p)
	default:
		fail("unsupported value type %d at %d", typ, pos)
	}
	return v
}

func (r *qbReader) structItems(p int) []qbItem {
	var items []qbItem
	for q := r.u32(p + 4); q != 0; {
		at := r.base + int(q)
		r.need(at, 16, "struct item")
		it := qbItem{name: r.u32(at + 4)}
		if typ := r.data[at+1] >> 1; typ == symScript {
			// An inline script: its size is in the item's type
			p, size := r.ptr(at+8), int(r.u16(at+2))
			r.need(p, size, "script")
			it.val = qbValue{typ: symScript, code: r.data[p : p+size]}
		} else {
			it.val = r.value(typ, at+8)
		}
		items = append(items, it)
		q = r.u32(at + 12)
	}
	return items
}

func (r *qbReader) array(p int) qbValue {
	r.need(p, 12, "array")
	v := qbValue{typ: symArray, elem: r.data[p+2]}
	n := int(r.u32(p + 4))
	switch {
	case n == 0:
	case n == 1:
		// A single value (or pointer) is stored in place of the list pointer
		v.elems = []qbValue{r.value(v.elem, p+8)}
	default:
		list := r.ptr(p + 8)
		for i := range n {
			v.elems = append(v.elems, r.value(v.elem, list+4*i))
		}
	}
	return v
}

// qbWriter lays out THAW values the way the original compiler does: depth first, each
// value's data right after whatever points at it. Pointers are relative to base.
type qbWriter struct {
	buf  []byte
	base int
}

func (w *qbWriter) u32(v uint32) { w.buf = binary.LittleEndian.AppendUint32(w.buf, v) }

func (w *qbWriter) put(at int, v uint32) { binary.LittleEndian.PutUint32(w.buf[at:], v) }

func (w *qbWriter) here() uint32 { return uint32(len(w.buf) - w.base) }

func (w *qbWriter) align4() {
	for len(w.buf)%4 != 0 {
		w.buf = append(w.buf, 0)
	}
}

// slot writes the value or pointer dword for v, then (for pointer types) its data.
func (w *qbWriter) slot(v qbValue) {
	if inline(v.typ) {
		w.u32(v.num)
		return
	}
	w.u32(w.here() + 4)
	w.data(v)
}

func (w *qbWriter) data(v qbValue) {
	switch v.typ {
	case symString, symLocalString:
		// Not padded: strings in an array are packed, padding comes after the array
		w.buf = append(w.buf, v.str...)
		w.buf = append(w.buf, 0)
	case symPair, symVector:
		w.u32(0x100)
		for _, f := range v.floats {
			w.u32(math.Float32bits(f))
		}
	case symStruct:
		w.structData(v.items)
	case symArray:
		w.arrayData(v)
	case symScript:
		w.buf = append(w.buf, v.code...)
	}
}

func (w *qbWriter) structData(items []qbItem) {
	w.u32(0x10000)
	if len(items) == 0 {
		w.u32(0)
		return
	}
	w.u32(w.here() + 4)
	for i, it := range items {
		// Item: type, name, value or pointer, next; pointed data follows the item
		at := len(w.buf)
		w.buf = append(w.buf, 0, it.val.typ<<1|1, 0, 0)
		if it.val.typ == symScript {
			binary.LittleEndian.PutUint16(w.buf[at+2:], uint16(len(it.val.code)))
		}
		w.u32(it.name)
		w.u32(it.val.num)
		w.u32(0)
		if !inline(it.val.typ) {
			w.put(at+8, w.here())
			w.data(it.val)
			w.align4()
		}
		if i+1 < len(items) {
			w.put(at+12, w.here())
		}
	}
}

func (w *qbWriter) arrayData(v qbValue) {
	w.buf = append(w.buf, 0, 1, v.elem, 0)
	w.u32(uint32(len(v.elems)))
	switch n := len(v.elems); {
	case n == 0:
		w.u32(0)
	case n == 1:
		w.slot(v.elems[0])
	case inline(v.elem):
		w.u32(w.here() + 4)
		for _, e := range v.elems {
			w.u32(e.num)
		}
	default:
		w.u32(w.here() + 4)
		list := len(w.buf)
		for range v.elems {
			w.u32(0)
		}
		for i, e := range v.elems {
			w.put(list+4*i, w.here())
			w.data(e)
		}
	}
	w.align4()
}

// qbSection is a top-level definition of a THAW .qb file.
type qbSection struct {
	typ  byte
	name uint32
	val  qbValue // data sections
	code []byte  // scripts: bytecode, uncompressed
}

// readTHAW parses a THAW .qb file.
func readTHAW(data []byte) (fileID uint32, sections []qbSection) {
	r := &qbReader{reader: &reader{data: data}}
	if size := int(r.u32(4)); size != len(data) {
		fail("file size %d doesn't match header (%d)", len(data), size)
	}
	for at := thawHeaderSize; at < len(data); {
		r.need(at, 20, "section")
		s := qbSection{typ: data[at+2], name: r.u32(at + 4)}
		fileID = r.u32(at + 8)
		r.end = at + 20
		if s.typ == symScript {
			p := r.ptr(at + 12)
			size, stored := int(r.u32(p+4)), int(r.u32(p+8))
			r.need(p+12, stored, "script")
			s.code = data[p+12 : p+12+stored]
			if stored < size {
				var err error
				if s.code, err = lzss.Decompress(s.code, size); err != nil {
					fail("script: %v", err)
				}
			}
		} else {
			s.val = r.value(s.typ, at+12)
		}
		sections = append(sections, s)
		at = (r.end + 3) &^ 3
	}
	return fileID, sections
}

// writeTHAW builds a THAW .qb file.
func writeTHAW(fileID uint32, sections []qbSection) []byte {
	w := &qbWriter{buf: make([]byte, 8, 4096)}
	w.buf = append(w.buf, thawMagic...)
	for _, s := range sections {
		w.buf = append(w.buf, 0, 4, s.typ, 0)
		w.u32(s.name)
		w.u32(fileID)
		switch {
		case s.typ == symScript:
			w.u32(w.here() + 8)
			w.u32(0)
			stored := lzss.Compress(s.code)
			if len(stored) >= len(s.code) {
				stored = s.code
			}
			w.u32(scriptCRC(s.code))
			w.u32(uint32(len(s.code)))
			w.u32(uint32(len(stored)))
			w.buf = append(w.buf, stored...)
			w.align4()
		case inline(s.typ):
			w.u32(s.val.num)
			w.u32(0)
		default:
			w.u32(w.here() + 8)
			w.u32(0)
			w.data(s.val)
			w.align4()
		}
	}
	binary.LittleEndian.PutUint32(w.buf[4:], uint32(len(w.buf)))
	return w.buf
}

// scriptCRC is the checksum THAW stores with a script: the CRC of its bytecode without
// EndOfLine tokens and the final EndScript.
func scriptCRC(code []byte) uint32 {
	r := &reader{data: code}
	crc := uint32(0xffffffff)
	for pos := 0; pos < len(code); {
		next := r.skip(pos)
		if Token(code[pos]) != TokEndOfLine && next < len(code) {
			crc = ^crc32.Update(^crc, crc32.IEEETable, code[pos:next])
		}
		pos = next
	}
	return crc
}

// thaw decompiles a THAW .qb file.
func (d *decompiler) thaw(data []byte) string {
	fileID, sections := readTHAW(data)
	var out strings.Builder
	fmt.Fprintf(&out, "%s 0x%08x\n\n", fileIDPragma, fileID)
	for _, s := range sections {
		if s.typ != symScript {
			out.WriteString(d.name(s.name, false) + " = " + d.value(s.val, 0) + "\n")
			continue
		}
		// Print SCRIPT name + bytecode as one token stream
		buf := []byte{byte(TokScript), byte(TokName)}
		buf = binary.LittleEndian.AppendUint32(buf, s.name)
		buf = append(buf, s.code...)
		buf = append(buf, byte(TokEndOfFile))
		out.WriteString(d.tokens(&reader{data: buf, alignBase: 6, thaw: true}))
		out.WriteString("\n\n")
	}
	return out.String()
}

// value prints a THAW value: on one line when indent < 0, else indented by indent tabs
// and split over lines when long.
func (d *decompiler) value(v qbValue, indent int) string {
	switch v.typ {
	case symInteger:
		return strconv.Itoa(int(int32(v.num)))
	case symFloat:
		return formatFloat(math.Float32frombits(v.num))
	case symString:
		return `"` + EscapeString(v.str) + `"`
	case symLocalString:
		return `#"` + EscapeString(v.str) + `"`
	case symName:
		return d.name(v.num, false)
	case symPair, symVector:
		parts := make([]string, len(v.floats))
		for i, f := range v.floats {
			parts[i] = formatFloat(f)
		}
		kind := "PAIR"
		if v.typ == symVector {
			kind = "VECTOR"
		}
		return kind + "(" + strings.Join(parts, ", ") + ")"
	case symScript:
		return d.inlineScript(v.code, max(indent, 0))
	}

	// Struct or array
	var parts []string
	open, close, sep := "[", "]", ", "
	if v.typ == symStruct {
		open, close, sep = "{", "}", " "
		for _, it := range v.items {
			text := d.value(it.val, -1)
			if it.name != 0 {
				text = d.name(it.name, false) + " = " + text
			}
			parts = append(parts, text)
		}
	} else {
		for _, e := range v.elems {
			parts = append(parts, d.value(e, -1))
		}
	}
	if len(parts) == 0 {
		return open + " " + close
	}
	line := open + " " + strings.Join(parts, sep) + " " + close
	if indent < 0 || (4*indent+len(line) <= 100 && !strings.Contains(line, "\n")) {
		return line
	}

	// One item per line, nested values indented
	tabs := strings.Repeat("\t", indent+1)
	var b strings.Builder
	b.WriteString(open + "\n")
	if v.typ == symStruct {
		for _, it := range v.items {
			b.WriteString(tabs)
			if it.name != 0 {
				b.WriteString(d.name(it.name, false) + " = ")
			}
			b.WriteString(d.value(it.val, indent+1) + "\n")
		}
	} else {
		for _, e := range v.elems {
			b.WriteString(tabs + d.value(e, indent+1) + "\n")
		}
	}
	b.WriteString(strings.Repeat("\t", indent) + close)
	return b.String()
}

// inlineScript prints a script stored in a struct as SCRIPT ... ENDSCRIPT, with a
// //#line comment that keeps its line numbers.
func (d *decompiler) inlineScript(code []byte, indent int) string {
	buf := append([]byte{byte(TokScript)}, code...)
	buf = append(buf, byte(TokEndOfFile)) // also ends the padding
	text := strings.TrimRight(d.tokens(&reader{data: buf, thaw: true}), "\n")
	return strings.ReplaceAll(text, "\n", "\n"+strings.Repeat("\t", indent+1))
}

func formatFloat(f float32) string { return strconv.FormatFloat(float64(f), 'f', 11, 64) }

// fileIDPragma starts the comment line that keeps a THAW file's id in its source.
const fileIDPragma = "//#fileid"

var fileIDLine = regexp.MustCompile(`(?m)^//#fileid[ \t]+0x([0-9a-fA-F]{1,8})`)

// thawFile compiles a whole source file into THAW sections.
func (c *compiler) thawFile(source string, fileID uint32) []byte {
	if m := fileIDLine.FindStringSubmatch(source); m != nil {
		id, _ := strconv.ParseUint(m[1], 16, 32)
		fileID = uint32(id)
	}
	var sections []qbSection
	for c.canPop() {
		t := c.pop()
		switch t.typ {
		case TokEndOfLine:
		case TokScript:
			name := c.popName()
			start := c.pos
			for c.canPop() && c.tokens[c.pos].typ != TokEndScript {
				c.pos++
			}
			if !c.canPop() {
				fail("missing 'ENDSCRIPT'")
			}
			c.pos++
			body := &compiler{props: c.props, tokens: c.tokens[start:c.pos], checksum: c.checksum}
			body.code = make([]byte, 0, 8*(c.pos-start))
			func() {
				defer func() { c.line = body.line }() // report errors at the body's line
				body.compile()
			}()
			sections = append(sections, qbSection{typ: symScript, name: name, code: body.code})
		case TokName, tokNameChecksum:
			c.pos--
			name := c.popName()
			c.expect(TokEquals, "=")
			v := c.value()
			sections = append(sections, qbSection{typ: v.typ, name: name, val: v})
		default:
			fail("expected a definition (name = value) or SCRIPT")
		}
	}
	return writeTHAW(fileID, sections)
}

func (c *compiler) popName() uint32 {
	switch t := c.pop(); t.typ {
	case TokName:
		return c.addName(t.str)
	case tokNameChecksum:
		return uint32(t.num)
	}
	fail("expected a name")
	return 0
}

// value parses a THAW value.
func (c *compiler) value() qbValue {
	t := c.pop()
	switch t.typ {
	case TokInteger, TokHexInteger:
		return qbValue{typ: symInteger, num: uint32(t.num)}
	case TokFloat:
		return qbValue{typ: symFloat, num: math.Float32bits(float32(t.real()))}
	case TokString:
		return qbValue{typ: symString, str: t.str}
	case TokLocalString:
		return qbValue{typ: symLocalString, str: t.str}
	case TokName, tokNameChecksum:
		c.pos--
		return qbValue{typ: symName, num: c.popName()}
	case TokPair, TokVector:
		v := qbValue{typ: symPair}
		n := 2
		if t.typ == TokVector {
			v.typ, n = symVector, 3
		}
		c.expect(TokOpenParenth, "(")
		for i := range n {
			if i > 0 {
				c.expect(TokComma, ",")
			}
			v.floats = append(v.floats, c.popReal())
		}
		c.expect(TokCloseParenth, ")")
		return v
	case TokStartStruct:
		v := qbValue{typ: symStruct}
		for c.skipSeparators() != TokEndStruct {
			var it qbItem
			if next := c.tokens[c.pos]; (next.typ == TokName || next.typ == tokNameChecksum) &&
				c.pos+1 < len(c.tokens) && c.tokens[c.pos+1].typ == TokEquals {
				it.name = c.popName()
				c.pop()
			}
			it.val = c.value()
			v.items = append(v.items, it)
		}
		c.pop()
		return v
	case TokScript:
		return qbValue{typ: symScript, code: c.inlineScript()}
	case TokStartArray:
		v := qbValue{typ: symArray}
		for c.skipSeparators() != TokEndArray {
			e := c.value()
			if len(v.elems) == 0 {
				v.elem = e.typ
			} else if e.typ != v.elem {
				fail("array mixes value types")
			}
			v.elems = append(v.elems, e)
		}
		c.pop()
		return v
	}
	fail("expected a value")
	return qbValue{}
}

// inlineScript compiles a script inside a struct, after its SCRIPT: up to ENDSCRIPT and
// the line end after it. Its line ends carry line numbers, and it's padded to 4 bytes.
func (c *compiler) inlineScript() []byte {
	start := c.pos
	for c.canPop() && c.tokens[c.pos].typ != TokEndScript {
		c.pos++
	}
	if !c.canPop() {
		fail("missing 'ENDSCRIPT'")
	}
	if c.pos++; c.canPop() && c.tokens[c.pos].typ == TokEndOfLine {
		c.pos++
	}
	props := c.props
	props.inlineScript = true
	body := &compiler{props: props, tokens: c.tokens[start:c.pos], checksum: c.checksum}
	func() {
		defer func() { c.line = body.line }()
		body.compile()
	}()
	for len(body.code)%4 != 0 {
		body.code = append(body.code, 0)
	}
	return body.code
}

// skipSeparators skips line ends and commas and returns the next token's type.
func (c *compiler) skipSeparators() Token {
	for {
		if !c.canPop() {
			fail("unexpected end of script")
		}
		switch t := c.tokens[c.pos].typ; t {
		case TokEndOfLine, TokComma:
			c.pos++
		default:
			return t
		}
	}
}

// binaryStruct reports whether the struct starting at the '{' just popped is compiled
// as a TokStruct: a struct of constants on one line, passed to a command or RETURN or
// given as the script's default parameters, and not marked /*tokens*/.
func (c *compiler) binaryStruct() bool {
	open := c.pos - 1
	if c.tokens[open].num != 0 {
		return false
	}
	if open > 0 {
		switch c.tokens[open-1].typ {
		case TokName, tokNameChecksum, TokReturn:
		default:
			return false
		}
	}
	types := make([]Token, 0, 16)
	depth := 0
	for _, t := range c.tokens[open:] {
		switch t.typ {
		case TokOpenParenth, TokCloseParenth:
			// Only as PAIR(...) and VECTOR(...)
			if n := len(types); t.typ == TokOpenParenth && (n == 0 || (types[n-1] != TokPair && types[n-1] != TokVector)) {
				return false
			}
		case TokStartStruct, TokStartArray:
			depth++
		case TokEndStruct, TokEndArray:
			if depth--; depth == 0 {
				return true
			}
		case TokName, tokNameChecksum, TokInteger, TokHexInteger, TokFloat, TokString,
			TokLocalString, TokPair, TokVector, TokComma, TokEquals:
		default:
			return false
		}
		types = append(types, t.typ)
	}
	return false
}

// binaryCandidate reports whether the original compiler would have made the token struct
// starting at pos a TokStruct (see binaryStruct), so the decompiler marks it /*tokens*/.
func (r *reader) binaryCandidate(pos, prev int) bool {
	if prev >= 0 {
		switch Token(r.data[prev]) {
		case TokName, TokReturn:
		default:
			return false
		}
	}
	depth := 0
	for p := pos; p >= 0 && p < len(r.data); p = r.skip(p) {
		switch Token(r.data[p]) {
		case TokStartStruct, TokStartArray:
			depth++
		case TokEndStruct, TokEndArray:
			if depth--; depth == 0 {
				return true
			}
		case TokName, TokInteger, TokHexInteger, TokFloat, TokString, TokLocalString,
			TokPair, TokVector, TokComma, TokEquals:
		default:
			return false
		}
	}
	return false
}

// binaryStructToken compiles the struct starting at the '{' just popped as a TokStruct.
func (c *compiler) binaryStructToken() {
	c.pos--
	v := c.value()
	c.token(TokStruct)
	size := len(c.code)
	c.short(0)
	for len(c.code)%4 != 0 {
		c.code = append(c.code, 0)
	}
	w := &qbWriter{buf: c.code, base: len(c.code)}
	w.structData(v.items)
	c.code = w.buf
	binary.LittleEndian.PutUint16(c.code[size:], uint16(len(c.code)-w.base))
}
