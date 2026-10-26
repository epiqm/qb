// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

// Package pre reads and writes Neversoft PRE archives (.pre, .prx).
//
// Layout (little endian):
//
//	u32 total file size
//	u32 version (0xabcd0003)
//	u32 number of files
//	per file, 4-byte aligned:
//	  u32 data size
//	  u32 compressed size (0 = stored)
//	  u16 name size (including NUL, padded to 4)
//	  u16 padding
//	  u32 checksum of the name
//	  name
//	  data (LZSS compressed unless stored), padded to 4
package pre

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"qb/lzss"
	"qb/qscript"
)

// Version is the only PRE version the engine accepts.
const Version = 0xabcd0003

const headerSize = 16

// File is one archive entry. Name uses backslashes, e.g. scripts\game\game.qb.
type File struct {
	Name string
	Data []byte
}

// entry is an archive entry as stored.
type entry struct {
	name        string
	size, csize int
	checksum    uint32
	stored      []byte // compressed data, or the file itself when csize is 0
}

func parse(buf []byte) ([]entry, error) {
	if len(buf) < 12 {
		return nil, fmt.Errorf("not a PRE file (too small)")
	}
	if v := binary.LittleEndian.Uint32(buf[4:]); v != Version {
		return nil, fmt.Errorf("unsupported PRE version 0x%x (expected 0x%x)", v, Version)
	}
	count := int(int32(binary.LittleEndian.Uint32(buf[8:])))
	if count < 0 {
		return nil, fmt.Errorf("bad file count %d", count)
	}

	entries := make([]entry, 0, count)
	p := 12
	for i := range count {
		if p+headerSize > len(buf) {
			return nil, fmt.Errorf("entry %d: unexpected end of file", i)
		}
		e := entry{
			size:     int(binary.LittleEndian.Uint32(buf[p:])),
			csize:    int(binary.LittleEndian.Uint32(buf[p+4:])),
			checksum: binary.LittleEndian.Uint32(buf[p+12:]),
		}
		nameSize := int(binary.LittleEndian.Uint16(buf[p+8:]))
		stored := e.size
		if e.csize != 0 {
			stored = e.csize
		}
		start := p + headerSize + nameSize
		if start+stored > len(buf) {
			return nil, fmt.Errorf("entry %d: unexpected end of file", i)
		}

		name := buf[p+headerSize : start]
		if j := bytes.IndexByte(name, 0); j >= 0 {
			name = name[:j]
		}
		e.name = string(name)
		e.stored = buf[start : start+stored]
		entries = append(entries, e)

		p = start + (stored+3)&^3
	}
	return entries, nil
}

// Read parses an archive and decompresses its files.
func Read(buf []byte) ([]File, error) {
	entries, err := parse(buf)
	if err != nil {
		return nil, err
	}
	files := make([]File, len(entries))
	for i, e := range entries {
		data := append([]byte(nil), e.stored...)
		if e.csize != 0 {
			if data, err = lzss.Decompress(e.stored, e.size); err != nil {
				return nil, fmt.Errorf("%s: %w", e.name, err)
			}
		}
		files[i] = File{Name: e.name, Data: data}
	}
	return files, nil
}

// Write builds an archive. Like the original tools, a file is compressed only when
// that saves at least 15%.
func Write(files []File) []byte {
	out := make([]byte, 12)
	binary.LittleEndian.PutUint32(out[4:], Version)
	binary.LittleEndian.PutUint32(out[8:], uint32(len(files)))

	for _, f := range files {
		data, csize := f.Data, 0
		if c := lzss.Compress(f.Data); len(c)*100 <= len(f.Data)*85 {
			data, csize = c, len(c)
		}
		nameSize := (len(f.Name) + 1 + 3) &^ 3

		var h [headerSize]byte
		binary.LittleEndian.PutUint32(h[0:], uint32(len(f.Data)))
		binary.LittleEndian.PutUint32(h[4:], uint32(csize))
		binary.LittleEndian.PutUint16(h[8:], uint16(nameSize))
		binary.LittleEndian.PutUint32(h[12:], qscript.CRC(f.Name))
		out = append(out, h[:]...)

		out = append(out, f.Name...)
		out = append(out, make([]byte, nameSize-len(f.Name))...)
		out = append(out, data...)
		out = append(out, make([]byte, (len(data)+3)&^3-len(data))...)
	}

	binary.LittleEndian.PutUint32(out[0:], uint32(len(out)))
	return out
}

// IsPre reports whether buf starts like a PRE archive.
func IsPre(buf []byte) bool {
	return len(buf) >= 12 && binary.LittleEndian.Uint32(buf[4:]) == Version
}
