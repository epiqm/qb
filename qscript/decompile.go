// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package qscript

import (
	"fmt"
	"strconv"
	"strings"
)

// Decompile turns .qb bytecode (THUG1, THUG2 or THAW) into .q source. names adds
// checksum names to the file's own (THAW files carry none: they're in .dbg files) and may
// be nil. Warnings lists checksums with no known name (written as %0x...% or <0x...>).
func Decompile(data []byte, names map[uint32]string) (source string, warnings []string, err error) {
	defer recoverError(&err)
	d := &decompiler{names: names, unknown: map[uint32]bool{}}
	if IsTHAW(data) {
		source = d.thaw(data)
	} else {
		r := &reader{data: data}
		d.local = r.checksumNames()
		source = d.tokens(r)
	}
	return source, d.warnings, nil
}

type decompiler struct {
	local    map[uint32]string // the file's own checksum table
	names    map[uint32]string // given names, shared between files
	unknown  map[uint32]bool
	warnings []string
}

// name writes a checksum as a name, <name> for an argument.
func (d *decompiler) name(checksum uint32, isArg bool) string {
	name, ok := d.local[checksum]
	if !ok {
		name, ok = d.names[checksum]
	}
	switch {
	case !ok:
		if !d.unknown[checksum] {
			d.unknown[checksum] = true
			d.warnings = append(d.warnings, fmt.Sprintf("could not find name for checksum 0x%x", checksum))
		}
		if isArg {
			return fmt.Sprintf("<0x%x>", checksum)
		}
		return fmt.Sprintf("%%0x%x%%", checksum)
	case !isIdentifier(name):
		name = `%"` + EscapeString(name) + `"`
	}
	if isArg {
		return "<" + name + ">"
	}
	return name
}

// tokens prints the token stream in r.data, up to EndOfFile.
func (d *decompiler) tokens(r *reader) string {
	data := r.data
	labels := r.labels()

	var out, line strings.Builder
	tabDepth, preTabDepth, postTabDepth := 0, 0, 0
	isArg := false

	writeFloat := func(f float32) {
		line.WriteString(strconv.FormatFloat(float64(f), 'f', 11, 64))
	}

	prev := -1       // the previous token
	lineNumber := -1 // THAW inline scripts: the last EndOfLineNumber's line
	for pos := 0; pos >= 0; {
		token := Token(data[pos])
		next := r.skip(pos)

		for _, label := range labels[pos] {
			line.WriteString(label + " ")
			if strings.Contains(label, "RANDOMEND") {
				preTabDepth--
			}
		}

		switch token {
		case TokEndOfFile, TokEndOfLine, TokEndOfLineNumber:
			if token == TokEndOfLineNumber && r.thaw {
				// Keep line numbers that don't follow on (see lineComment)
				n := int(r.u32(pos + 1))
				if lineNumber < 0 || n != lineNumber+1 {
					line.WriteString(lineComment + strconv.Itoa(n))
				}
				lineNumber = n
			}
			if preTabDepth < -postTabDepth {
				tabDepth += preTabDepth + postTabDepth
			}
			out.WriteString(strings.Repeat("\t", max(tabDepth, 0)))
			if preTabDepth > -postTabDepth {
				tabDepth += postTabDepth + preTabDepth
			}
			preTabDepth, postTabDepth = 0, 0
			tabDepth = max(tabDepth, 0)

			if token == TokEndOfFile {
				// No newline unless the bytecode ended with one, so it compiles back the same
				out.WriteString(strings.TrimRight(line.String(), "\n"))
			} else {
				out.WriteString(line.String())
				out.WriteByte('\n')
			}
			line.Reset()
		case TokStartStruct:
			if r.thaw && r.binaryCandidate(pos, prev) {
				line.WriteString(keepTokensComment + " ")
			}
			line.WriteString("{ ")
			postTabDepth++
		case TokEndStruct:
			line.WriteString("} ")
			preTabDepth--
		case TokStartArray:
			line.WriteString("[ ")
			postTabDepth++
		case TokEndArray:
			line.WriteString("] ")
			preTabDepth--
		case TokEquals:
			line.WriteString("= ")
		case TokDot:
			line.WriteString(". ")
		case TokComma:
			line.WriteString(", ")
		case TokMinus:
			line.WriteString("- ")
		case TokAdd:
			line.WriteString("+ ")
		case TokDivide:
			line.WriteString("/ ")
		case TokMultiply:
			line.WriteString("* ")
		case TokOpenParenth:
			line.WriteString("( ")
		case TokCloseParenth:
			line.WriteString(") ")
		case TokSameAs:
			line.WriteString("== ")
		case TokLessThan:
			line.WriteString("< ")
		case TokLessThanEqual:
			line.WriteString("<= ")
		case TokGreaterThan:
			line.WriteString("> ")
		case TokGreaterThanEqual:
			line.WriteString(">= ")
		case TokName:
			line.WriteString(d.name(r.u32(pos+1), isArg) + " ")
			isArg = false
		case TokInteger:
			fmt.Fprintf(&line, "%d ", r.s32(pos+1))
		case TokHexInteger:
			fmt.Fprintf(&line, "0x%x ", r.u32(pos+1))
		case TokFloat:
			writeFloat(r.f32(pos + 1))
			line.WriteString(" ")
		case TokString, TokLocalString:
			length := int(r.u32(pos + 1))
			if length > 0 {
				length--
			}
			r.need(pos+5, length, "String")
			if token == TokLocalString {
				line.WriteString("#")
			}
			line.WriteString(`"` + EscapeString(string(data[pos+5:pos+5+length])) + `" `)
		case TokVector:
			line.WriteString("VECTOR(")
			writeFloat(r.f32(pos + 1))
			line.WriteString(", ")
			writeFloat(r.f32(pos + 5))
			line.WriteString(", ")
			writeFloat(r.f32(pos + 9))
			line.WriteString(") ")
		case TokPair:
			line.WriteString("PAIR(")
			writeFloat(r.f32(pos + 1))
			line.WriteString(", ")
			writeFloat(r.f32(pos + 5))
			line.WriteString(") ")
		case TokBegin:
			line.WriteString("BEGIN ")
			postTabDepth++
		case TokRepeat:
			line.WriteString("REPEAT ")
			tabDepth--
		case TokBreak:
			line.WriteString("BREAK ")
		case TokScript:
			line.WriteString("SCRIPT ")
			postTabDepth++
		case TokEndScript:
			if r.thaw {
				line.WriteString("ENDSCRIPT ")
			} else {
				line.WriteString("ENDSCRIPT\n")
			}
			preTabDepth--
		case TokIf, TokFastIf:
			line.WriteString("IF ")
			postTabDepth++
		case TokElse, TokFastElse:
			line.WriteString("ELSE ")
			tabDepth--
			postTabDepth++
		case TokElseIf:
			line.WriteString("ELSEIF ")
			preTabDepth--
			postTabDepth++
		case TokEndIf:
			line.WriteString("ENDIF ")
			preTabDepth--
		case TokReturn:
			line.WriteString("RETURN ")
		case TokAllArgs:
			line.WriteString("<...> ")
		case TokArg:
			isArg = true
		case TokRandom, TokRandom2, TokRandomNoRepeat, TokRandomPermute:
			switch token {
			case TokRandom:
				line.WriteString("RANDOM(")
			case TokRandom2:
				line.WriteString("RANDOM2(")
			case TokRandomNoRepeat:
				line.WriteString("RANDOM_NO_REPEAT(")
			case TokRandomPermute:
				line.WriteString("RANDOM_PERMUTE(")
			}
			n := int(r.u32(pos + 1))
			for i := range n {
				if i > 0 {
					line.WriteString(", ")
				}
				fmt.Fprintf(&line, "%d", r.u16(pos+5+i*2))
			}
			line.WriteString(") ")
			postTabDepth++
		case TokRandomRange:
			line.WriteString("RANDOM_RANGE ")
		case TokRandomRange2:
			line.WriteString("RANDOM_RANGE2 ")
		case TokOr:
			line.WriteString("| ")
		case TokAnd:
			line.WriteString("& ")
		case TokXor:
			line.WriteString("^ ")
		case TokShiftLeft:
			line.WriteString("<< ")
		case TokShiftRight:
			line.WriteString(">> ")
		case TokNot:
			line.WriteString("NOT ")
		case TokKeywordAnd:
			line.WriteString("AND ")
		case TokKeywordOr:
			line.WriteString("OR ")
		case TokSwitch:
			line.WriteString("SWITCH ")
			postTabDepth += 2
		case TokEndSwitch:
			line.WriteString("ENDSWITCH ")
			preTabDepth -= 2
		case TokCase:
			line.WriteString("CASE ")
			tabDepth--
			postTabDepth++
		case TokDefault:
			line.WriteString("DEFAULT ")
			tabDepth--
			postTabDepth++
		case TokColon:
			line.WriteString(": ")
		case TokStruct:
			start := r.structStart(pos)
			qr := &qbReader{reader: r, base: start}
			line.WriteString(d.value(qbValue{typ: symStruct, items: qr.structItems(start)}, -1) + " ")
		}
		// Jump, ShortJump and ChecksumName are implied by the source and print nothing

		prev, pos = pos, next
	}
	return out.String()
}

// isIdentifier reports whether a name can be written bare: it lexes as an identifier
// and isn't a keyword.
func isIdentifier(name string) bool {
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	_, reserved := keywords[name]
	return !reserved
}
