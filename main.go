// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

// Command qb unpacks/packs Neversoft PRE and PAK archives and compiles/decompiles QScript.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"qb/pak"
	"qb/pre"
	"qb/qscript"
)

// version is set by release builds: go build -ldflags "-X main.version=v1.0.0"
var version = "dev"

type args struct {
	input, output, names string
	target               qscript.Target
}

type command struct {
	name, desc string
	input      string // what -input takes
	output     string // what -output takes
	target     bool   // takes -target
	names      bool   // takes -names
	run        func(args) error
}

var commands = []command{
	{"unpack", "Extract a PRE or PAK archive into a directory", "<*.pre|*.prx|*.pak.wpc>", "<dir>", false, false, unpack},
	{"pack", "Build a PRE or PAK archive from a directory", "<dir>", "<*.pre|*.prx|*.pak.wpc>", false, false, pack},
	{"decompile", "Decompile QScript bytecode to source", "<*.qb|*.qb.wpc|dir>", "<*.q|dir>", false, true, decompile},
	{"compile", "Compile QScript source to bytecode", "<*.q|dir>", "<*.qb|*.qb.wpc|dir>", true, false, compile},
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: qb <command> [arguments]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", c.name, c.desc)
	}
	fmt.Fprintf(os.Stderr, "  %-10s %s\n", "version", "Print the version")
	fmt.Fprintf(os.Stderr, "\nRun 'qb <command>' for its arguments.\n")
	fmt.Fprintf(os.Stderr, "\nqb by epicmaxim, www.thug.one\n")
}

func commandUsage(c command) {
	fmt.Fprintf(os.Stderr, "Usage: qb %s\n", c.name)
	fmt.Fprintf(os.Stderr, "  -input %s\n    Input\n", c.input)
	fmt.Fprintf(os.Stderr, "  -output %s\n    Output\n", c.output)
	if c.target {
		fmt.Fprintf(os.Stderr, "  -target\n    Script target\n    Options:\n")
		fmt.Fprintf(os.Stderr, "      thug1\n        Tony Hawk's Underground\n")
		fmt.Fprintf(os.Stderr, "      thug2\n        Tony Hawk's Underground 2\n")
		fmt.Fprintf(os.Stderr, "      thaw\n        Tony Hawk's American Wasteland\n")
	}
	if c.names {
		fmt.Fprintf(os.Stderr, "  (optional) -names <*.dbg|dbg.pak.wpc|dir>\n    Checksum names (THAW keeps them in .dbg files)\n")
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "-version", "--version":
		fmt.Printf("qb %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}
	var cmd *command
	for i := range commands {
		if commands[i].name == os.Args[1] {
			cmd = &commands[i]
		}
	}
	if cmd == nil {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	flags := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
	flags.Usage = func() { commandUsage(*cmd) }
	input := flags.String("input", "", "")
	output := flags.String("output", "", "")
	targetName := flags.String("target", "", "")
	names := flags.String("names", "", "")
	if len(os.Args) == 2 {
		commandUsage(*cmd)
		os.Exit(2)
	}
	if err := flags.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	if flags.NArg() > 0 {
		fatal(fmt.Errorf("unexpected argument: %s", flags.Arg(0)))
	}
	if *input == "" {
		fatal(errors.New("missing required argument: input"))
	}
	if *output == "" {
		fatal(errors.New("missing required argument: output"))
	}
	if *names != "" && !cmd.names {
		fatal(fmt.Errorf("%s doesn't take -names", cmd.name))
	}

	a := args{input: *input, output: *output, names: *names}
	if cmd.target {
		t, ok := qscript.Targets[*targetName]
		if *targetName == "" {
			fatal(errors.New("missing required argument: target"))
		} else if !ok {
			fatal(fmt.Errorf("invalid option for argument target: %s", *targetName))
		}
		a.target = t
	} else if *targetName != "" {
		fatal(fmt.Errorf("%s doesn't take -target", cmd.name))
	}

	if err := cmd.run(a); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "qb: %v\n", err)
	os.Exit(1)
}

// writeFile writes a file, creating its directory.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// localPath turns an archive name (backslashes) into a path under dir.
func localPath(dir, name string) (string, error) {
	rel := filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("unsafe file name %q", name)
	}
	return filepath.Join(dir, rel), nil
}

func unpack(a args) error {
	buf, err := os.ReadFile(a.input)
	if err != nil {
		return err
	}
	if !pre.IsPre(buf) {
		return unpackPak(a, buf)
	}
	files, err := pre.Read(buf)
	if err != nil {
		return fmt.Errorf("%s: %w", a.input, err)
	}
	for _, f := range files {
		path, err := localPath(a.output, f.Name)
		if err != nil {
			return fmt.Errorf("%s: %w", a.input, err)
		}
		if err := writeFile(path, f.Data); err != nil {
			return err
		}
	}
	fmt.Printf("%s: %d files\n", a.input, len(files))
	return nil
}

// pakInfo is the manifest unpack writes next to a PAK's files, so pack can rebuild the
// archive exactly: the entry order, and what can't be derived from a file name.
const pakInfo = ".pakinfo"

func unpackPak(a args, buf []byte) error {
	var pab []byte
	if p := pak.PabPath(a.input); p != "" {
		var err error
		if pab, err = os.ReadFile(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	archive, err := pak.Read(buf, pab)
	if err != nil {
		return fmt.Errorf("%s: not a PRE or PAK archive (%w)", a.input, err)
	}

	var info bytes.Buffer
	fmt.Fprintf(&info, "# qb PAK manifest: entry order and fields; pack uses it to rebuild the archive\n")
	fmt.Fprintf(&info, "split %t\nlast 0x%08x\n", archive.Split, archive.Last)
	fmt.Fprintf(&info, "# type flags full path unk1 unk2 file\n")
	for i, e := range archive.Entries {
		name := e.Name
		if name == "" {
			name = fmt.Sprintf(`_unnamed\%04d.0x%08x`, i, e.Type)
		}
		path, err := localPath(a.output, name)
		if err != nil {
			return fmt.Errorf("%s: %w", a.input, err)
		}
		if err := writeFile(path, e.Data); err != nil {
			return err
		}
		fmt.Fprintf(&info, "0x%08x 0x%x 0x%08x 0x%08x 0x%08x 0x%08x %s\n", e.Type, e.Flags, e.Full, e.Path, e.Unk1, e.Unk2, name)
	}
	if err := writeFile(filepath.Join(a.output, pakInfo), info.Bytes()); err != nil {
		return err
	}
	fmt.Printf("%s: %d files\n", a.input, len(archive.Entries))
	return nil
}

func pack(a args) error {
	if pak.IsPakPath(a.output) {
		return packPak(a)
	}
	var files []pre.File
	if err := collect(a.input, "", &files); err != nil {
		return err
	}
	if err := os.WriteFile(a.output, pre.Write(files), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d files\n", a.output, len(files))
	return nil
}

func packPak(a args) error {
	var files []pre.File
	if err := collect(a.input, "", &files); err != nil {
		return err
	}
	data := map[string][]byte{}
	for _, f := range files {
		data[strings.ToLower(f.Name)] = f.Data
	}

	// Entries listed in the manifest first, in its order, then any new files
	archive := &pak.Archive{}
	listed := map[string]bool{}
	if info, err := os.ReadFile(filepath.Join(a.input, pakInfo)); err == nil {
		if err := readPakInfo(info, archive, data, listed); err != nil {
			return fmt.Errorf("%s: %w", pakInfo, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, f := range files {
		if !listed[strings.ToLower(f.Name)] {
			archive.Entries = append(archive.Entries, pak.NewEntry(f.Name, f.Data))
		}
	}

	pakData, pabData, err := pak.Write(archive)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.output, pakData, 0o644); err != nil {
		return err
	}
	if archive.Split {
		pabPath := pak.PabPath(a.output)
		if pabPath == "" {
			return fmt.Errorf("%s: can't name the .pab for this archive", a.output)
		}
		if err := os.WriteFile(pabPath, pabData, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("%s: %d files\n", a.output, len(archive.Entries))
	return nil
}

func readPakInfo(info []byte, archive *pak.Archive, data map[string][]byte, listed map[string]bool) error {
	sc := bufio.NewScanner(bytes.NewReader(info))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Fields(line)
		switch {
		case fields[0] == "split" && len(fields) == 2:
			archive.Split = fields[1] == "true"
			continue
		case fields[0] == "last" && len(fields) == 2:
			v, err := strconv.ParseUint(fields[1], 0, 32)
			if err != nil {
				return fmt.Errorf("line %d: %v", n, err)
			}
			archive.Last = uint32(v)
			continue
		case len(fields) < 7:
			return fmt.Errorf("line %d: expected 7 fields", n)
		}
		var v [6]uint32
		for i := range v {
			x, err := strconv.ParseUint(fields[i], 0, 32)
			if err != nil {
				return fmt.Errorf("line %d: %v", n, err)
			}
			v[i] = uint32(x)
		}
		name := strings.Join(fields[6:], " ")
		key := strings.ToLower(name)
		d, ok := data[key]
		if !ok {
			return fmt.Errorf("line %d: %s is missing", n, name)
		}
		e := pak.Entry{Type: v[0], Flags: v[1], Full: v[2], Path: v[3], Unk1: v[4], Unk2: v[5], Data: d}
		if v[1]&pak.FlagName != 0 {
			e.Name = name
		}
		archive.Entries = append(archive.Entries, e)
		listed[key] = true
	}
	return sc.Err()
}

// collect adds a directory's files in the order the original tools stored them:
// files before subdirectories, each sorted by uppercased name (Windows directory order).
func collect(dir, prefix string, files *[]pre.File) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return !entries[i].IsDir()
		}
		return strings.ToUpper(entries[i].Name()) < strings.ToUpper(entries[j].Name())
	})
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err := collect(path, prefix+e.Name()+`\`, files); err != nil {
				return err
			}
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		*files = append(*files, pre.File{Name: prefix + e.Name(), Data: data})
	}
	return nil
}

// Script file names: bytecode is x.qb (THUG) or x.qb.wpc (THAW), source is x.q.
var (
	bytecodeExts = []string{".qb.wpc", ".qb"}
	sourceExts   = []string{".q"}
)

func decompile(a args) error {
	names, err := loadNames(a.names)
	if err != nil {
		return err
	}
	return convert(a.input, a.output, bytecodeExts, ".q", func(path, _ string, data []byte) ([]byte, error) {
		source, warnings, err := qscript.Decompile(data, names)
		if a.names == "" || len(warnings) < 20 {
			for _, w := range warnings {
				fmt.Fprintf(os.Stderr, "%s: warning: %s\n", path, w)
			}
		} else {
			fmt.Fprintf(os.Stderr, "%s: warning: %d checksums without a name\n", path, len(warnings))
		}
		return []byte(source), err
	})
}

func compile(a args) error {
	ext := ".qb"
	if a.target == qscript.THAW {
		ext = ".qb.wpc"
	}
	return convert(a.input, a.output, sourceExts, ext, func(_, rel string, data []byte) ([]byte, error) {
		if a.target != qscript.THAW {
			return qscript.Compile(string(data), a.target)
		}
		// The file id the original tools would give this path, unless the source has one
		if rel == "" {
			rel = filepath.Base(a.output)
		}
		rel = strings.TrimSuffix(trimExt(rel, sourceExts), ".qb") + ".qb"
		return qscript.CompileTHAW(string(data), qscript.FileID(strings.ReplaceAll(rel, "/", `\`)))
	})
}

// trimExt removes the first of exts that name ends with.
func trimExt(name string, exts []string) string {
	for _, ext := range exts {
		if len(name) >= len(ext) && strings.EqualFold(name[len(name)-len(ext):], ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

func hasExt(name string, exts []string) bool { return trimExt(name, exts) != name }

// convert runs fn on one file, or on every file with one of exts under a directory,
// mirroring the tree under output with extension to. fn gets the path relative to the
// input directory ("" for a single file).
func convert(input, output string, exts []string, to string, fn func(path, rel string, data []byte) ([]byte, error)) error {
	info, err := os.Stat(input)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return convertFile(input, output, "", fn)
	}

	count, failed := 0, 0
	err = filepath.WalkDir(input, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !hasExt(path, exts) {
			return err
		}
		rel, err := filepath.Rel(input, path)
		if err != nil {
			return err
		}
		count++
		if err := convertFile(path, filepath.Join(output, trimExt(rel, exts)+to), rel, fn); err != nil {
			fmt.Fprintf(os.Stderr, "qb: %v\n", err)
			failed++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d files failed", failed, count)
	}
	fmt.Printf("%s: %d files\n", input, count)
	return nil
}

func convertFile(input, output, rel string, fn func(string, string, []byte) ([]byte, error)) error {
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	out, err := fn(input, rel, data)
	if err != nil {
		return fmt.Errorf("%s: %w", input, err)
	}
	return writeFile(output, out)
}

// loadNames reads checksum names from "0x12345678 name" lines: a .dbg file, a directory
// of them, or a PAK of them (THAW's dbg.pak.wpc).
func loadNames(path string) (map[uint32]string, error) {
	if path == "" {
		return nil, nil
	}
	names := map[uint32]string{}
	add := func(data []byte) {
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "0x") {
				continue
			}
			crc, name, ok := strings.Cut(strings.TrimRight(line, "\r"), " ")
			v, err := strconv.ParseUint(crc, 0, 32)
			if !ok || err != nil || name == "" {
				continue
			}
			// Files spell some names differently; prefer one with capitals (SetTags, not settags)
			if old, seen := names[uint32(v)]; !seen || (old == strings.ToLower(old) && name != old) {
				names[uint32(v)] = name
			}
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if strings.Contains(strings.ToLower(d.Name()), ".dbg") {
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				add(data)
			}
			return nil
		})
		return names, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if pak.IsPakPath(path) {
		archive, err := pak.Read(data, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, e := range archive.Entries {
			add(e.Data)
		}
		return names, nil
	}
	add(data)
	return names, nil
}
