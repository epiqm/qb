// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

// Package pak reads and writes Tony Hawk's American Wasteland PAK archives
// (.pak, optionally with the data in a separate .pab).
//
// Layout (little endian), the .pab (if any) continuing the .pak:
//
//	entries, 32 bytes each (+160 for a name when flags&0x20):
//	  u32 type        checksum of the extension, e.g. ".qb"
//	  u32 offset      of the data, relative to this entry
//	  u32 size
//	  u32 full        checksum of the name
//	  u32 unk1
//	  u32 path        checksum of the name under the original build tree (BuildRoot)
//	  u32 unk2
//	  u32 flags
//	  [160]byte name  when flags&0x20
//	a "last" entry (type checksum "last"), pointing at the end of the data
//	32 bytes of 0xCD
//	the data of each file, 32-byte aligned, padded with 0xCD
//	the "last" data: AB AB AB AB, then CD x12, AB x16
package pak

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"qb/qscript"
)

// BuildRoot is the directory the original tools ran in; entry checksums are taken of
// paths under it.
const BuildRoot = qscript.THAWBuildRoot

// FlagName marks an entry that carries its file name.
const FlagName = 0x20

const (
	entrySize = 32
	nameSize  = 160
	align     = 32
)

var (
	typeLast = qscript.CRC("last")
	lastTail = []byte{
		0xAB, 0xAB, 0xAB, 0xAB, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD, 0xCD,
		0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB, 0xAB,
	}
)

// Entry is a file in an archive. Name is empty for unnamed entries.
type Entry struct {
	Type, Full, Path, Unk1, Unk2, Flags uint32
	Name                                string
	Data                                []byte
}

// Archive is a whole archive: its entries plus what's needed to rebuild it exactly.
type Archive struct {
	Entries []Entry
	Split   bool   // data in a separate .pab
	Last    uint32 // the "last" entry's full/unk1 fields (0 or 0x6f980dc3)
}

// NewEntry returns an entry for a named file, with the checksums the original tools use.
// The type is the checksum of the extension before the platform suffix (".qb" for
// "x.qb.wpc").
func NewEntry(name string, data []byte) Entry {
	return Entry{
		Type:  qscript.CRC(Ext(name)),
		Full:  qscript.CRC(name),
		Path:  qscript.CRC(BuildRoot + name),
		Flags: FlagName,
		Name:  name,
		Data:  data,
	}
}

// Ext returns a name's file type extension: ".qb" for "scripts\a.qb.wpc", ".tex" for "b.tex".
func Ext(name string) string {
	base := name[bytes.LastIndexAny([]byte(name), `\/`)+1:]
	parts := bytes.Split([]byte(base), []byte("."))
	switch {
	case len(parts) >= 3:
		return "." + string(parts[len(parts)-2])
	case len(parts) == 2:
		return "." + string(parts[1])
	}
	return ""
}

// Read parses an archive; pab is the .pab contents or nil.
func Read(pak, pab []byte) (*Archive, error) {
	data := pak
	if pab != nil {
		data = append(append([]byte(nil), pak...), pab...)
	}
	a := &Archive{Split: pab != nil}
	for p := 0; ; {
		if p+entrySize > len(pak) {
			return nil, errors.New("no end of archive entry")
		}
		var h [8]uint32
		for i := range h {
			h[i] = binary.LittleEndian.Uint32(pak[p+4*i:])
		}
		e := Entry{Type: h[0], Full: h[3], Unk1: h[4], Path: h[5], Unk2: h[6], Flags: h[7]}
		if e.Type == typeLast {
			a.Last = e.Full
			return a, nil
		}
		off, size := p+int(h[1]), int(h[2])
		if off < 0 || off+size > len(data) {
			return nil, fmt.Errorf("entry %d: data out of range", len(a.Entries))
		}
		next := p + entrySize
		if e.Flags&FlagName != 0 {
			if next+nameSize > len(pak) {
				return nil, fmt.Errorf("entry %d: name out of range", len(a.Entries))
			}
			name := pak[next : next+nameSize]
			if i := bytes.IndexByte(name, 0); i >= 0 {
				name = name[:i]
			}
			e.Name = string(name)
			next += nameSize
		}
		e.Data = data[off : off+size]
		a.Entries = append(a.Entries, e)
		p = next
	}
}

// IsPak reports whether buf looks like a PAK archive.
func IsPak(buf []byte) bool {
	_, err := Read(buf, nil)
	return err == nil
}

// Write builds an archive, returning the .pak and (when Split) the .pab.
func Write(a *Archive) (pak, pab []byte, err error) {
	headers := 0
	for _, e := range a.Entries {
		headers += entrySize
		if e.Flags&FlagName != 0 {
			if len(e.Name) >= nameSize {
				return nil, nil, fmt.Errorf("%s: name longer than %d bytes", e.Name, nameSize-1)
			}
			headers += nameSize
		}
	}
	headers += entrySize // "last"

	// Data positions, from the start of the archive
	pos := headers + align
	offsets := make([]int, len(a.Entries))
	for i, e := range a.Entries {
		offsets[i] = pos
		pos = (pos + len(e.Data) + align - 1) &^ (align - 1)
	}
	end := pos

	out := make([]byte, end+len(lastTail))
	p := 0
	for i, e := range a.Entries {
		h := []uint32{e.Type, uint32(offsets[i] - p), uint32(len(e.Data)), e.Full, e.Unk1, e.Path, e.Unk2, e.Flags}
		for j, v := range h {
			binary.LittleEndian.PutUint32(out[p+4*j:], v)
		}
		p += entrySize
		if e.Flags&FlagName != 0 {
			copy(out[p:], e.Name)
			p += nameSize
		}
	}
	for j, v := range []uint32{typeLast, uint32(end - p), 4, a.Last, a.Last, 0, 0, 0} {
		binary.LittleEndian.PutUint32(out[p+4*j:], v)
	}
	p += entrySize

	fill := func(from, to int) {
		for ; from < to; from++ {
			out[from] = 0xCD
		}
	}
	fill(p, p+align)
	for i, e := range a.Entries {
		copy(out[offsets[i]:], e.Data)
		next := end
		if i+1 < len(a.Entries) {
			next = offsets[i+1]
		}
		fill(offsets[i]+len(e.Data), next)
	}
	copy(out[end:], lastTail)

	if a.Split {
		return out[:headers+align], out[headers+align:], nil
	}
	return out, nil, nil
}

// IsPakPath reports whether a file name is a PAK archive's: x.pak or x.pak.<platform>.
func IsPakPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(base, ".pak") || strings.Contains(base, ".pak.")
}

// PabPath returns the .pab file that goes with a .pak path (x.pak.wpc -> x.pab.wpc).
func PabPath(path string) string {
	dir, base := filepath.Split(path)
	i := strings.LastIndex(strings.ToLower(base), ".pak")
	if i < 0 {
		return ""
	}
	return dir + base[:i] + ".pab" + base[i+4:]
}
