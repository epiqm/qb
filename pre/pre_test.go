// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package pre

import (
	"bytes"
	"os"
	"path/filepath"
	"qb/lzss"
	"testing"
)

// TestRoundTrip unpacks and repacks every archive in $QB_PRE_DIR (e.g. a game's
// Data/pre) and expects the same entries back. Padding is ignored: some retail
// archives have uninitialized memory there. The original encoder also read stale
// buffer contents past the end of very short files, so a compressed stream may
// differ as long as it is the same size and decodes the same.
func TestRoundTrip(t *testing.T) {
	dir := os.Getenv("QB_PRE_DIR")
	if dir == "" {
		t.Skip("QB_PRE_DIR not set")
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.pr?")) // .pre, .prx, .prf, .prg
	if len(paths) == 0 {
		t.Fatalf("no PRE archives in %s", dir)
	}
	for _, path := range paths {
		base := filepath.Base(path)
		buf, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files, err := Read(buf)
		if err != nil {
			t.Errorf("%s: %v", base, err)
			continue
		}
		out := Write(files)
		want, _ := parse(buf)
		got, err := parse(out)
		if err != nil {
			t.Errorf("%s: repacked: %v", base, err)
			continue
		}
		if len(out) != len(buf) {
			t.Errorf("%s: size %d, want %d", base, len(out), len(buf))
		}
		for i := range want {
			w, g := want[i], got[i]
			same := bytes.Equal(w.stored, g.stored)
			if !same && w.csize != 0 && w.csize == g.csize {
				wd, _ := lzss.Decompress(w.stored, w.size)
				gd, _ := lzss.Decompress(g.stored, g.size)
				same = wd != nil && bytes.Equal(wd, gd)
			}
			if w.name != g.name || w.size != g.size || w.csize != g.csize || w.checksum != g.checksum || !same {
				t.Errorf("%s: %s: got size %d csize %d checksum %08x, want %s size %d csize %d checksum %08x",
					base, g.name, g.size, g.csize, g.checksum, w.name, w.size, w.csize, w.checksum)
			}
		}
	}
}

func loadArchive(b *testing.B) []byte {
	path := os.Getenv("QB_PRE_FILE")
	if path == "" {
		b.Skip("QB_PRE_FILE not set")
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	return buf
}

// BenchmarkRead unpacks $QB_PRE_FILE in memory (throughput is unpacked bytes).
func BenchmarkRead(b *testing.B) {
	buf := loadArchive(b)
	files, _ := Read(buf)
	var size int64
	for _, f := range files {
		size += int64(len(f.Data))
	}
	b.SetBytes(size)
	b.ResetTimer()
	for range b.N {
		if _, err := Read(buf); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWrite packs the files of $QB_PRE_FILE in memory (throughput is unpacked bytes).
func BenchmarkWrite(b *testing.B) {
	files, err := Read(loadArchive(b))
	if err != nil {
		b.Fatal(err)
	}
	var size int64
	for _, f := range files {
		size += int64(len(f.Data))
	}
	b.SetBytes(size)
	b.ResetTimer()
	for range b.N {
		Write(files)
	}
}
