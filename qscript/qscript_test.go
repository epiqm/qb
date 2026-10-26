// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package qscript

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recompile checks that source compiles, and that its decompiled form compiles back to
// the same bytecode.
func recompile(t *testing.T, source string, target Target) ([]byte, string) {
	t.Helper()
	code, err := Compile(source, target)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	text, _, err := Decompile(code, nil)
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	again, err := Compile(text, target)
	if err != nil {
		t.Fatalf("recompile: %v\n%s", err, text)
	}
	if !bytes.Equal(code, again) {
		t.Fatalf("recompiled bytecode differs\n%s", text)
	}
	return code, text
}

func TestStringEscapes(t *testing.T) {
	code, text := recompile(t, `a = "x\x41\xE9\101\n\"y"`+"\n", THUG1)
	if !bytes.Contains(code, []byte("xA\xe9A\n\"y\x00")) {
		t.Errorf("escapes compiled wrong: %q", code)
	}
	if !strings.Contains(text, `"xA\xE9A\n\"y"`) {
		t.Errorf("escapes decompiled wrong: %s", text)
	}
}

func TestKeywordNames(t *testing.T) {
	_, text := recompile(t, `%"OR" = %"SCRIPT"`+"\n", THUG1)
	if !strings.Contains(text, `%"OR" = %"SCRIPT"`) {
		t.Errorf("keyword names not quoted: %s", text)
	}
}

func TestRandomNested(t *testing.T) {
	// The inner random ends where the outer one does
	recompile(t, "SCRIPT test\n"+
		"RANDOM(1, 2) RANDOMCASE a RANDOMCASE RANDOM(3, 4) RANDOMCASE b RANDOMCASE c RANDOMEND RANDOMEND\n"+
		"RANDOM_NO_REPEAT(1) RANDOMCASE d RANDOMEND\n"+
		"ENDSCRIPT\n", THUG1)
}

func TestOperators(t *testing.T) {
	_, text := recompile(t, "a = (1 << 2) + (8 >> 1) + RANDOM_RANGE2 PAIR(0, 1)\n", THUG1)
	for _, want := range []string{"<<", ">>", "RANDOM_RANGE2"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
}

func TestFastIfElseCase(t *testing.T) {
	source := "SCRIPT test\n" +
		"IF a\n b\nELSE\n c\nENDIF\n" +
		"SWITCH <x>\n CASE 1\n  d\n CASE 2\n  e\n DEFAULT\n  f\nENDSWITCH\n" +
		"ENDSCRIPT\n"
	thug1, _ := recompile(t, source, THUG1)
	thug2, _ := recompile(t, source, THUG2)
	if bytes.IndexByte(thug1, byte(TokFastIf)) >= 0 {
		t.Error("thug1 output has FastIf")
	}
	for _, tok := range []Token{TokFastIf, TokFastElse, TokShortJump} {
		if bytes.IndexByte(thug2, byte(tok)) < 0 {
			t.Errorf("thug2 output has no token %d", tok)
		}
	}
}

func TestErrors(t *testing.T) {
	for source, want := range map[string]string{
		"a\nb:\n":           "line 2: labels are not supported",
		"SCRIPT x\nIF a\n":  "missing 'ENDIF'",
		"a = $\n":           "line 1: unrecognized character",
		"RANDOMEND\n":       "no random",
		"VECTOR(1, 2)\n":    "expected ','",
		"%\"A\" = %\"a\"\n": "", // same checksum, same name: fine
	} {
		_, err := Compile(source, THUG2)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: unexpected error %v", source, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: got error %v, want %q", source, err, want)
		}
	}
}

// isBytecodePath matches x.qb (THUG) and x.qb.wpc (THAW).
func isBytecodePath(path string) bool {
	path = strings.ToLower(path)
	return strings.HasSuffix(path, ".qb") || strings.HasSuffix(path, ".qb.wpc")
}

// TestRoundTripDir decompiles and recompiles every .qb under $QB_SCRIPT_DIR for
// $QB_TARGET (default thug1). Bytecode that came from the original tools can differ
// in checksum table order and blank lines, so the decompiled text is compared.
func TestRoundTripDir(t *testing.T) {
	dir := os.Getenv("QB_SCRIPT_DIR")
	if dir == "" {
		t.Skip("QB_SCRIPT_DIR not set")
	}
	target, ok := Targets[os.Getenv("QB_TARGET")]
	if !ok {
		target = THUG1
	}
	noBlankLines := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	count := 0
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isBytecodePath(path) {
			return err
		}
		count++
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text, _, err := Decompile(data, nil)
		if err != nil {
			t.Errorf("%s: decompile: %v", path, err)
			return nil
		}
		code, err := Compile(text, target)
		if err != nil {
			t.Errorf("%s: compile: %v", path, err)
			return nil
		}
		again, _, err := Decompile(code, nil)
		if err != nil {
			t.Errorf("%s: decompile recompiled: %v", path, err)
			return nil
		}
		if noBlankLines(text) != noBlankLines(again) {
			t.Errorf("%s: recompiled script decompiles differently", path)
		}
		return nil
	})
	if count == 0 {
		t.Fatalf("no .qb files under %s", dir)
	}
	t.Logf("%d scripts", count)
}

// loadScripts reads every .qb under $QB_SCRIPT_DIR with its decompiled source.
func loadScripts(b *testing.B) (code [][]byte, source []string) {
	dir := os.Getenv("QB_SCRIPT_DIR")
	if dir == "" {
		b.Skip("QB_SCRIPT_DIR not set")
	}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isBytecodePath(path) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		text, _, err := Decompile(data, nil)
		if err != nil {
			b.Fatal(err)
		}
		code = append(code, data)
		source = append(source, text)
		return nil
	})
	if len(code) == 0 {
		b.Fatalf("no .qb files under %s", dir)
	}
	return code, source
}

// BenchmarkDecompile decompiles every script under $QB_SCRIPT_DIR per iteration
// (throughput is bytecode bytes).
func BenchmarkDecompile(b *testing.B) {
	code, _ := loadScripts(b)
	var size int64
	for _, c := range code {
		size += int64(len(c))
	}
	b.SetBytes(size)
	b.ResetTimer()
	for range b.N {
		for _, c := range code {
			if _, _, err := Decompile(c, nil); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkCompile compiles every script under $QB_SCRIPT_DIR per iteration
// (throughput is source bytes).
func BenchmarkCompile(b *testing.B) {
	_, source := loadScripts(b)
	target, ok := Targets[os.Getenv("QB_TARGET")]
	if !ok {
		target = THUG1
	}
	var size int64
	for _, s := range source {
		size += int64(len(s))
	}
	b.SetBytes(size)
	b.ResetTimer()
	for range b.N {
		for _, s := range source {
			if _, err := Compile(s, target); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// TestLexer checks longest-match corner cases against what the flex lexer does.
func TestLexer(t *testing.T) {
	for source, want := range map[string][]Token{
		"/* a */ x":                      {TokName},
		"/* a **/ x */ y":                {TokName}, // "**/" doesn't end it, "*/" does
		"/* a / b":                       {TokName}, // flex: "/* a /" is already a complete comment
		"// c\r\nx":                      {TokEndOfLine, TokName},
		"-5 - 5 5.5 .5 5.":               {TokInteger, TokMinus, TokInteger, TokFloat, TokFloat, TokFloat},
		"0x1F 0b101 %0x1F%":              {TokInteger, TokInteger, tokNameChecksum},
		"<a> <0x1> <...> < a":            {TokArg, tokArgChecksum, TokAllArgs, TokLessThan, TokName},
		`<%"a b"> %"c" #"d" "e\"f"`:      {TokArg, TokName, TokLocalString, TokString},
		"<= << >= >> == =":               {TokLessThanEqual, TokShiftLeft, TokGreaterThanEqual, TokShiftRight, TokSameAs, TokEquals},
		"IF IFX RANDOM RANDOM_RANGE2 x:": {TokIf, TokName, TokRandom, TokRandomRange2, tokLabel},
	} {
		tokens, err := lex(source)
		if err != nil {
			t.Errorf("%q: %v", source, err)
			continue
		}
		var got []Token
		for _, tk := range tokens {
			got = append(got, tk.typ)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q: got %v, want %v", source, got, want)
		}
	}
}

func TestTHAW(t *testing.T) {
	source := "//#fileid 0x12345678\n\n" +
		"int_val = 5\nfloat_val = 1.5\nstr_val = \"hi\"\nlstr = #\"local\"\n" +
		"pair_val = PAIR(1, 2.5)\nvec = VECTOR(1, 2, 3)\nname_val = something\n" +
		"empty = [ ]\nstrs = [ \"a\", \"bc\", \"def\" ]\none = [ 7 ]\n" +
		"structs = [ { a = 1 } { b = \"x\" flag } ]\n" +
		"nested = {\n\ta = { b = [ 1, 2 ] }\n\tc = VECTOR(0, 0, 1)\n}\n" +
		"cas = { disqualify_script = SCRIPT //#line 100\n\tSetPart part = Body\nENDSCRIPT\nother = 1 }\n" +
		"SCRIPT test { a = 1 }\n" +
		"\tFoo { x = 1 y = \"s\" v = [ 1, 2 ] }\n" +
		"\tBar /*tokens*/ { x = 1 }\n" +
		"\tBaz { x = <arg> }\n" +
		"\tIF a\n\t\tRETURN { result = 1 }\n\tELSE\n" +
		"\t\tRANDOM(1, 2) RANDOMCASE b RANDOMCASE c RANDOMEND\n\tENDIF\n\n" +
		"ENDSCRIPT\n"
	code, text := recompile(t, source, THAW)
	if !IsTHAW(code) {
		t.Fatal("output isn't a THAW .qb")
	}
	if got := binary.LittleEndian.Uint32(code[4:]); int(got) != len(code) {
		t.Errorf("header size %d, file is %d bytes", got, len(code))
	}
	for _, want := range []string{
		// THAW files carry no names, so names come out as %0x...%
		"//#fileid 0x12345678", `= [ "a", "bc", "def" ]`, "% /*tokens*/ {", "= <0x",
		"SCRIPT //#line 100", "RANDOMCASE", "PAIR(1.00000000000, 2.50000000000)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("decompiled text has no %q:\n%s", want, text)
		}
	}
	// The default parameters, Foo's and RETURN's structs are binary; Bar's and Baz's aren't
	_, sections := readTHAW(code)
	binaryStructs := 0
	for _, s := range sections {
		r := &reader{data: s.code}
		for pos := 0; pos < len(s.code); pos = r.skip(pos) {
			if Token(s.code[pos]) == TokStruct {
				binaryStructs++
			}
		}
	}
	if binaryStructs != 3 {
		t.Errorf("%d binary structs, want 3", binaryStructs)
	}
	// Default file id from the caller when there's no //#fileid line
	noID, err := CompileTHAW("a = 1\n", FileID(`scripts\x.qb`))
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(noID[36:]); got != FileID(`scripts\x.qb`) {
		t.Errorf("file id %08x, want %08x", got, FileID(`scripts\x.qb`))
	}
}

func TestTHAWErrors(t *testing.T) {
	for source, want := range map[string]string{
		"a = [ 1 \"x\" ]\n":        "array mixes value types",
		"SCRIPT x\n a\n":           "missing 'ENDSCRIPT'",
		"5\n":                      "expected a definition",
		"a = { b = SCRIPT\nc\n}\n": "missing 'ENDSCRIPT'",
	} {
		if _, err := Compile(source, THAW); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got error %v, want %q", source, err, want)
		}
	}
}
