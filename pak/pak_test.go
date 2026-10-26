// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package pak

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qb/qscript"
)

// pakPair returns a .pak's contents and its .pab (or nil).
func pakPair(path string) (pak, pab []byte, err error) {
	if pak, err = os.ReadFile(path); err != nil {
		return nil, nil, err
	}
	if p := PabPath(path); p != "" {
		if pab, err = os.ReadFile(p); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
	return pak, pab, nil
}

// TestRoundTrip reads and rewrites every PAK under $QB_PAK_DIR (e.g. a THAW Game/data)
// and expects identical bytes. Files that aren't PAK archives (sound banks share the
// extension) are skipped.
func TestRoundTrip(t *testing.T) {
	dir := os.Getenv("QB_PAK_DIR")
	if dir == "" {
		t.Skip("QB_PAK_DIR not set")
	}
	tested, skipped := 0, 0
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !IsPakPath(path) {
			return err
		}
		pak, pab, err := pakPair(path)
		if err != nil {
			t.Fatal(err)
		}
		a, err := Read(pak, pab)
		if err != nil {
			skipped++
			return nil
		}
		tested++
		gotPak, gotPab, err := Write(a)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		if !bytes.Equal(gotPak, pak) || !bytes.Equal(gotPab, pab) {
			t.Errorf("%s: rewritten archive differs", path)
		}
		return nil
	})
	if tested == 0 {
		t.Fatalf("no PAK archives under %s", dir)
	}
	t.Logf("%d archives identical, %d other files skipped", tested, skipped)
}

func TestExt(t *testing.T) {
	for name, want := range map[string]string{
		`scripts\a.qb.wpc`: ".qb",
		`b.tex`:            ".tex",
		`dir.x\c`:          "",
	} {
		if got := Ext(name); got != want {
			t.Errorf("Ext(%q) = %q, want %q", name, got, want)
		}
	}
	if !strings.HasSuffix(BuildRoot, `\`) {
		t.Error("BuildRoot must end with a backslash")
	}
}

func TestWriteRead(t *testing.T) {
	for _, split := range []bool{false, true} {
		a := &Archive{Split: split, Last: 0x6f980dc3, Entries: []Entry{
			NewEntry(`scripts\a.qb.wpc`, []byte("hello")),
			{Type: 0x1234, Full: 0x5678, Data: bytes.Repeat([]byte{1}, 33)}, // unnamed
			NewEntry(`b.tex.wpc`, nil),
		}}
		pak, pab, err := Write(a)
		if err != nil {
			t.Fatal(err)
		}
		if (pab != nil) != split {
			t.Errorf("split %v: got .pab %v", split, pab != nil)
		}
		b, err := Read(pak, pab)
		if err != nil {
			t.Fatal(err)
		}
		if b.Split != a.Split || b.Last != a.Last || len(b.Entries) != len(a.Entries) {
			t.Fatalf("split %v: archive fields differ", split)
		}
		for i := range a.Entries {
			x, y := a.Entries[i], b.Entries[i]
			if x.Name != y.Name || x.Type != y.Type || x.Full != y.Full || x.Path != y.Path || x.Flags != y.Flags || !bytes.Equal(x.Data, y.Data) {
				t.Errorf("split %v: entry %d differs", split, i)
			}
		}
		if (len(pak)+len(pab))%32 != 0 {
			t.Errorf("archive size %d isn't 32-byte aligned", len(pak)+len(pab))
		}
	}
	if e := NewEntry(`scripts\a.qb.wpc`, nil); e.Type != qscript.CRC(".qb") || e.Path != qscript.CRC(BuildRoot+`scripts\a.qb.wpc`) {
		t.Error("NewEntry checksums")
	}
}
