// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package qscript

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// lexToken is a token of .q source.
type lexToken struct {
	typ  Token
	line int32
	str  string // Name, Arg, Label, String, LocalString
	num  int64  // Integer, NameChecksum, ArgChecksum; Float as float64 bits; StartStruct: keepTokens
}

// keepTokensComment before a '{' keeps a THAW struct as tokens where it would otherwise
// be compiled as a binary struct (TokStruct). The decompiler writes it where the original
// compiler did that.
const keepTokensComment = "/*tokens*/"

// lineComment ("//#line 2288") sets the line number of the following line end, so a
// THAW inline script keeps its original line numbers (see compiler.inlineScript).
const lineComment = "//#line "

func (t lexToken) real() float64 { return math.Float64frombits(uint64(t.num)) }

// lexRule matches at the start of its input and returns the match length (0: no match).
type lexRule struct {
	first  string // bytes a match can start with
	match  func(s string) int
	action func(text string) (lexToken, bool) // false: skip the match
}

func tok(t Token) func(string) (lexToken, bool) {
	return func(string) (lexToken, bool) { return lexToken{typ: t}, true }
}

func str(t Token, substart, subend int) func(string) (lexToken, bool) {
	return func(text string) (lexToken, bool) {
		return lexToken{typ: t, str: parseString(text, substart, subend)}, true
	}
}

func num(t Token, skip, base int, prefix string) func(string) (lexToken, bool) {
	return func(text string) (lexToken, bool) {
		return lexToken{typ: t, num: parseNumber(text[skip:], base, prefix)}, true
	}
}

func skip(string) (lexToken, bool) { return lexToken{}, false }

// lexRules mirrors the flex lexer: the longest match wins, and the earlier rule wins a tie.
// Identifiers, labels, keywords, whitespace and newlines are matched directly in lex.
var lexRules = []lexRule{
	// Comments
	{"/", matchBlockComment, skip}, // /\*([^*]|\*[^/])*\*?/
	{"/", matchLineComment, skip},  // //.*

	// Numbers
	{"+-0123456789", func(s string) int { return matchNumber(s, "", isDigit) }, num(TokInteger, 0, 10, "")}, // [-+]?[0-9]+
	{"+-0", func(s string) int { return matchNumber(s, "0b", isBinary) }, num(TokInteger, 0, 2, "0b")},      // [-+]?0b[01]+
	{"+-0", func(s string) int { return matchNumber(s, "0x", isHex) }, num(TokInteger, 0, 16, "0x")},        // [-+]?0x[0-9a-fA-F]+
	{"+-.0123456789", matchReal, func(text string) (lexToken, bool) { // [+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)
		return lexToken{typ: TokFloat, num: int64(math.Float64bits(parseReal(text)))}, true
	}},

	// Strings
	{"<", func(s string) int { return matchQuoted(s, "<%", ">") }, str(TokArg, 3, -2)},
	{"%", func(s string) int { return matchQuoted(s, "%", "") }, str(TokName, 2, -1)},
	{"#", func(s string) int { return matchQuoted(s, "#", "") }, str(TokLocalString, 2, -1)},
	{`"`, func(s string) int { return matchQuoted(s, "", "") }, str(TokString, 1, -1)},

	// Checksums
	{"%", func(s string) int { return matchWrapped(s, "%0x", isHex, '%') }, num(tokNameChecksum, 1, 16, "0x")},
	{"<", func(s string) int { return matchWrapped(s, "<0x", isHex, '>') }, num(tokArgChecksum, 1, 16, "0x")},

	// Symbols
	sym("{", TokStartStruct),
	sym("}", TokEndStruct),
	sym("[", TokStartArray),
	sym("]", TokEndArray),
	sym("=", TokEquals),
	sym(".", TokDot),
	sym(",", TokComma),
	sym("-", TokMinus),
	sym("+", TokAdd),
	sym("/", TokDivide),
	sym("*", TokMultiply),
	sym("(", TokOpenParenth),
	sym(")", TokCloseParenth),
	sym(":", TokColon),

	// Comparisons
	sym("==", TokSameAs),
	sym("<", TokLessThan),
	sym("<=", TokLessThanEqual),
	sym(">", TokGreaterThan),
	sym(">=", TokGreaterThanEqual),

	// Logical operators
	sym("|", TokOr),
	sym("&", TokAnd),
	sym("^", TokXor),
	sym("<<", TokShiftLeft),
	sym(">>", TokShiftRight),

	// Args
	sym("<...>", TokAllArgs),
	{"<", func(s string) int { return matchWrapped(s, "<", isIdentChar, '>') }, str(TokArg, 1, -1)}, // <[a-zA-Z_][a-zA-Z_0-9]*>
}

// lexIndex lists, for each byte, the rules that can match starting with it.
var lexIndex = func() (index [256][]int) {
	for i, rule := range lexRules {
		for j := 0; j < len(rule.first); j++ {
			index[rule.first[j]] = append(index[rule.first[j]], i)
		}
	}
	return
}()

// sym is a rule for a literal symbol.
func sym(l string, t Token) lexRule { return lexRule{l[:1], lit(l), tok(t)} }

func lit(l string) func(string) int {
	return func(s string) int {
		if len(s) >= len(l) && s[:len(l)] == l {
			return len(l)
		}
		return 0
	}
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isBinary(c byte) bool { return c == '0' || c == '1' }
func isHex(c byte) bool    { return ctoi(c) >= 0 }

// run returns the end of the run of bytes matching pred starting at i.
func run(s string, i int, pred func(byte) bool) int {
	for i < len(s) && pred(s[i]) {
		i++
	}
	return i
}

func sign(s string) int {
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		return 1
	}
	return 0
}

// matchBlockComment follows flex's longest match of /\*([^*]|\*[^/])*\*?/, which can run
// past a "*/" reached through "**/".
func matchBlockComment(s string) int {
	if len(s) < 2 || s[:2] != "/*" {
		return 0
	}
	best, star := 0, false
	for i := 2; i < len(s); i++ {
		c := s[i]
		if star {
			if c == '/' {
				return i + 1
			}
			star = false
		} else if c == '*' {
			star = true
		} else if c == '/' {
			best = i + 1
		}
	}
	return best
}

func matchLineComment(s string) int {
	if len(s) < 2 || s[:2] != "//" {
		return 0
	}
	return run(s, 2, func(c byte) bool { return c != '\n' })
}

// matchNumber matches [-+]?<prefix><digit>+.
func matchNumber(s, prefix string, digit func(byte) bool) int {
	i := sign(s)
	if len(s)-i < len(prefix) || s[i:i+len(prefix)] != prefix {
		return 0
	}
	i += len(prefix)
	if end := run(s, i, digit); end > i {
		return end
	}
	return 0
}

func matchReal(s string) int {
	i := sign(s)
	if end := run(s, i, isDigit); end > i {
		if end < len(s) && s[end] == '.' {
			end = run(s, end+1, isDigit)
		}
		return end
	}
	if i < len(s) && s[i] == '.' {
		if end := run(s, i+1, isDigit); end > i+1 {
			return end
		}
	}
	return 0
}

// matchQuoted matches <prefix>"([^\\"]|\\.)*"<suffix>.
func matchQuoted(s, prefix, suffix string) int {
	i := len(prefix)
	if len(s) <= i || s[:i] != prefix || s[i] != '"' {
		return 0
	}
	for i++; i < len(s); i++ {
		switch s[i] {
		case '"':
			end := i + 1
			if len(s)-end < len(suffix) || s[end:end+len(suffix)] != suffix {
				return 0
			}
			return end + len(suffix)
		case '\\':
			if i+1 >= len(s) || s[i+1] == '\n' {
				return 0
			}
			i++
		}
	}
	return 0
}

// matchWrapped matches <prefix><char>+<close>; with prefix "<" and identifier chars it's
// an <identifier> (which can't start with a digit).
func matchWrapped(s, prefix string, char func(byte) bool, close byte) int {
	i := len(prefix)
	if len(s) <= i || s[:i] != prefix {
		return 0
	}
	if prefix == "<" && !isIdentStart(s[i]) {
		return 0
	}
	end := run(s, i, char)
	if end == i || end >= len(s) || s[end] != close {
		return 0
	}
	return end + 1
}

// lex splits .q source into tokens.
func lex(source string) ([]lexToken, error) {
	// Scripts average about one token per 6-7 bytes
	tokens := make([]lexToken, 0, len(source)/6)
	keepTokens := false
	lineNumber := int64(0) // from a //#line comment, for the next line end
	line := int32(1)
	// Skip a UTF-8 byte order mark
	if len(source) >= 3 && source[:3] == "\xEF\xBB\xBF" {
		source = source[3:]
	}
	for pos := 0; pos < len(source); {
		c := source[pos]
		switch {
		case c == ' ' || c == '\t':
			pos++
			continue
		case c == '\n' || c == '\r':
			tokens = append(tokens, lexToken{typ: TokEndOfLine, line: line, num: lineNumber})
			lineNumber = 0
			if c == '\n' {
				line++
			}
			pos++
			continue
		case isIdentStart(c):
			// Longest match: "name:" is a label, else a keyword wins its tie with an identifier
			end := pos + 1
			for end < len(source) && isIdentChar(source[end]) {
				end++
			}
			word := source[pos:end]
			t := lexToken{typ: TokName, str: word, line: line}
			if end < len(source) && source[end] == ':' {
				t.typ = tokLabel
				end++
			} else if k, ok := keywords[word]; ok {
				t = lexToken{typ: k, line: line}
			}
			tokens = append(tokens, t)
			pos = end
			continue
		}

		rest := source[pos:]
		best, bestLen := -1, 0
		for _, i := range lexIndex[c] {
			if n := lexRules[i].match(rest); n > bestLen {
				best, bestLen = i, n
			}
		}
		if best < 0 {
			return nil, fmt.Errorf("line %d: unrecognized character %q", line, rest[0])
		}
		text := rest[:bestLen]
		if t, ok := lexRules[best].action(text); ok {
			t.line = line
			if t.typ == TokStartStruct && keepTokens {
				t.num = 1
			}
			tokens = append(tokens, t)
		}
		keepTokens = text == keepTokensComment
		if strings.HasPrefix(text, lineComment) {
			lineNumber, _ = strconv.ParseInt(strings.TrimSpace(text[len(lineComment):]), 10, 64)
		}
		for i := 0; i < len(text); i++ {
			if text[i] == '\n' {
				line++
			}
		}
		pos += bestLen
	}
	return tokens, nil
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentChar(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

func ctoi(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// parseString takes text[substart : len(text)+subend] (subend <= 0) and resolves escapes.
func parseString(text string, substart, subend int) string {
	end := len(text) + subend
	if subend == 0 {
		end = len(text)
	}
	if substart >= end {
		return ""
	}
	s := text[substart:end]

	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			out = append(out, c)
			continue
		}
		i++
		switch e := s[i]; e {
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			// Up to 3 octal digits
			v := int(e - '0')
			for n := 1; n < 3 && i+1 < len(s); n++ {
				d := ctoi(s[i+1])
				if d < 0 || d >= 8 {
					break
				}
				v = v<<3 | d
				i++
			}
			out = append(out, byte(v))
		case 'x':
			// Up to 2 hex digits, as written by the decompiler
			v := 0
			for n := 0; n < 2 && i+1 < len(s); n++ {
				d := ctoi(s[i+1])
				if d < 0 {
					break
				}
				v = v<<4 | d
				i++
			}
			out = append(out, byte(v))
		default:
			out = append(out, e)
		}
	}
	return string(out)
}

// parseNumber parses an optionally signed integer with the given prefix ("0x", "0b").
func parseNumber(text string, base int, prefix string) int64 {
	i := 0
	minus := false
	if i < len(text) && (text[i] == '-' || text[i] == '+') {
		minus = text[i] == '-'
		i++
	}
	if len(text)-i < len(prefix) || text[i:i+len(prefix)] != prefix {
		return 0
	}
	i += len(prefix)
	var v int64
	for ; i < len(text); i++ {
		d := ctoi(text[i])
		if d < 0 || d >= base {
			break
		}
		v = v*int64(base) + int64(d)
	}
	if minus {
		v = -v
	}
	return v
}

// parseReal parses a decimal number the way the original compiler does (digit by
// digit in double precision), so floats compile to the same bits.
func parseReal(text string) float64 {
	i := 0
	minus := false
	if i < len(text) && (text[i] == '-' || text[i] == '+') {
		minus = text[i] == '-'
		i++
	}
	value, decimal := 0.0, 0.0
	for ; i < len(text); i++ {
		c := text[i]
		if decimal == 0 && c == '.' {
			decimal = 10
			continue
		}
		d := ctoi(c)
		if d < 0 || d >= 10 {
			break
		}
		if decimal != 0 {
			value += float64(d) / decimal
			decimal *= 10
		} else {
			value = value*10 + float64(d)
		}
	}
	if minus {
		value = -value
	}
	return value
}
