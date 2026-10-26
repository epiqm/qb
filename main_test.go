// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"qb/pak"
	"qb/qscript"
)

// TestTHAWScripts decompiles and recompiles every THAW .qb in every PAK under
// $QB_THAW_DIR (a THAW Game/data) and expects identical bytes.
func TestTHAWScripts(t *testing.T) {
	dir := os.Getenv("QB_THAW_DIR")
	if dir == "" {
		t.Skip("QB_THAW_DIR not set")
	}
	archives, scripts := 0, 0
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !pak.IsPakPath(path) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var pab []byte
		if p := pak.PabPath(path); p != "" {
			pab, _ = os.ReadFile(p)
		}
		a, err := pak.Read(data, pab)
		if err != nil {
			return nil // sound banks share the extension
		}
		archives++
		for _, e := range a.Entries {
			if !qscript.IsTHAW(e.Data) {
				continue
			}
			scripts++
			source, _, err := qscript.Decompile(e.Data, nil)
			if err != nil {
				t.Errorf("%s: %s: decompile: %v", path, e.Name, err)
				continue
			}
			code, err := qscript.CompileTHAW(source, 0)
			if err != nil {
				t.Errorf("%s: %s: compile: %v", path, e.Name, err)
				continue
			}
			if !bytes.Equal(code, e.Data) {
				t.Errorf("%s: %s: recompiled file differs", path, e.Name)
			}
		}
		return nil
	})
	if scripts == 0 {
		t.Fatalf("no THAW scripts under %s", dir)
	}
	t.Logf("%d scripts in %d archives", scripts, archives)
}
