// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package qscript

import (
	"hash/crc32"
	"strings"
)

// CRC returns the QScript checksum of a name: CRC-32 (polynomial 0xedb88320) of the
// lowercased name with '/' mapped to '\', without the final inversion.
func CRC(name string) uint32 {
	crc := uint32(0xffffffff)
	for i := 0; i < len(name) && name[i] != 0; i++ {
		crc = crc32.IEEETable[byte(crc)^simpleChar(name[i])] ^ crc>>8
	}
	return crc
}

func simpleChar(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c - 'A' + 'a'
	}
	if c == '/' {
		return '\\'
	}
	return c
}

// sameName reports whether two names are equal after SimpleString.
func sameName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if simpleChar(a[i]) != simpleChar(b[i]) {
			return false
		}
	}
	return true
}

// SimpleString lowercases a name and converts forward slashes to backslashes, so names
// that differ only in case or slashes compare (and checksum) equal.
func SimpleString(s string) string {
	b := []byte(s)
	for i, c := range b {
		b[i] = simpleChar(c)
	}
	return string(b)
}

// EscapeString escapes a string for a QScript string literal.
func EscapeString(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\a':
			b.WriteString(`\a`)
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\'':
			b.WriteString(`\'`)
		default:
			if c >= 0x20 && c <= 0x7E {
				b.WriteByte(c)
			} else {
				b.WriteString(`\x`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0xF])
			}
		}
	}
	return b.String()
}
