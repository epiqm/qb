# qb

Toolchain for Neversoft QScript and its PRE and PAK archives, written in Go.

Supports THUG, THUG2, THAW.

Author: **epicmaxim**, [www.thug.one](https://www.thug.one)

Unpacks and packs:
- PRE archives (`.pre`, `.prx`, `.prf`, `.prg`): Tony Hawk's Underground 1 and 2
- PAK archives (`.pak.wpc`, with or without a `.pab.wpc`): Tony Hawk's American Wasteland

Compiles/Decompiles:
- Tony Hawk's Underground
- Tony Hawk's Underground 2
- Tony Hawk's American Wasteland

## Compiling

Standard Go build process:

```bash
go build
```

This builds the `qb` binary. There are no dependencies (no Flex, no CMake).

## Usage

THUG and THUG2:

```bash
qb unpack    -input qb.pre      -output qb
qb decompile -input qb          -output qb_src
qb compile   -input qb_src      -output qb -target thug1
qb pack      -input qb          -output qb.pre
```

THAW (paths under `Game/data`):

```bash
qb unpack    -input pak/qb.pak.wpc -output qb                  # reads qb.pab.wpc too
qb decompile -input qb             -output qb_src -names pak/dbg.pak.wpc
qb compile   -input qb_src         -output qb -target thaw
qb pack      -input qb             -output qb.pak.wpc          # writes qb.pab.wpc too
```

Run `qb` or `qb <command>` without arguments for help.

| Command | `-input` | `-output` |
|---|---|---|
| `unpack` | PRE or PAK archive | directory |
| `pack` | directory | PRE or PAK archive (picked by the name: `.pak`) |
| `decompile` | `.qb` / `.qb.wpc` file or directory | `.q` file or directory |
| `compile` | `.q` file or directory | `.qb` / `.qb.wpc` file or directory |

`compile` requires `-target`:
- `thug1`: Tony Hawk's Underground
- `thug2`: Tony Hawk's Underground 2 (`IF`/`ELSE`/`CASE` get short jumps)
- `thaw`: Tony Hawk's American Wasteland (writes `.qb.wpc` files)

`decompile` doesn't need a target, because it recognises each game's bytecode. It takes an optional `-names`, which supplies checksum names from a `.dbg` file, a directory of them, or THAW's `dbg.pak.wpc`. THUG files carry their own names; THAW files don't, so without `-names` THAW names come out as `%0x1234abcd%`. That form still compiles back to the same bytecode.

If `compile` or `decompile` gets a directory, it converts every script file under it and mirrors the tree under `-output`.

### PRE archives

The archive format is the same in THUG and THUG2: a header (`size`, version `0xabcd0003`, file count) followed by entries. Each entry holds the original size, the compressed size, the name and its checksum, then the data, which is either LZSS compressed or stored.

- `unpack` turns names like `scripts\game\game.qb` into paths.
- `pack` stores files in the order the original tools used: files before subdirectories, sorted by uppercased name. Like the original tools, it compresses a file only if that saves at least 15%. The LZSS encoder is a port of the original, so the compressed data comes out the same.

### PAK archives (THAW)

A PAK is a table of entries (type, offset, size, two name checksums, flags, and an optional 160-byte name), then the data. Large archives keep the data in a separate `.pab`. Each file starts on a 32-byte boundary, padded with `0xCD`, and the archive ends with a fixed `last` entry.

- `unpack` writes the files and a `.pakinfo` manifest. The manifest records the entry order and the fields a file name can't give: the type (`.qb`, `.sqb` and `.mqb` files are all named `*.qb.wpc`), the flags, unnamed entries, and whether there's a `.pab`. `pack` uses it to rebuild the archive byte for byte.
- `pack` adds files that aren't in the manifest at the end, with the checksums the original tools use. Those are the name's checksum, and the checksum of the name under the original build tree `f:\projects\aspyr\thaw\skate7\data\`.

### THAW scripts

A THAW `.qb` isn't one token stream. It's a list of typed definitions: integers, floats, strings, pairs, vectors, names, structs, arrays and scripts. `qb` writes them as QScript:

```
//#fileid 0x5603b312

debug_CAS_show_appearance = 0
edit_graphic_layer_infos = [ ]
SCRIPT refresh_skater_model
	GetCurrentSkaterProfileIndex
	IF InFrontEnd
		<skaterIndex> = 0
	...
ENDSCRIPT
```

- **Script bodies** are THUG2 bytecode, LZSS compressed, with a checksum of the code. They add one token: a struct of constants stored in binary form. `qb` compiles a `{ ... }` that way when it holds only constants, sits on one line, and follows a command name, a `RETURN`, or `SCRIPT name` (default parameters). Otherwise the struct stays as tokens, the same as the original compiler.
- **`//#fileid`** keeps the file id: the checksum of the file's path under the original build tree. Without it, `compile` works the id out from the path relative to the input directory, e.g. `scripts\skutils.qb`.
- **`/*tokens*/`** before a `{` keeps a struct as tokens where the rule above would make it binary. In 61 places the original compiler did that for reasons that can't be recovered from the bytecode, so the decompiler marks them. Both forms build the same struct in the game.
- **`SCRIPT //#line 2288 ... ENDSCRIPT`** is a script stored inside a struct (the CAS part tables have 218 of them). Its line ends carry source line numbers, and `//#line` keeps the original numbers wherever they don't follow on from the previous line.

All three are ordinary comments to any other QScript tool.

## Comparison with QScript

QScript is the C++ toolchain this project replaces (`qcompile` and `qdecompile`).

| | QScript (C++) | qb (Go) |
|---|---|---|
| Build | CMake, C++20 compiler, Flex | `go build` |
| Programs | `qcompile`, `qdecompile` | `qb` |
| Compile THUG1 / THUG2 | yes | yes |
| Decompile THUG1 / THUG2 | yes | yes |
| Compile / decompile THAW | no | yes |
| Unpack / pack PRE archives | no | yes |
| Unpack / pack PAK archives (THAW) | no | yes |
| Checksum names from `.dbg` files | no | yes (`-names`) |
| Convert a whole directory in one run | no | yes |
| `\xHH` string escapes | byte dropped | yes |
| Compile errors | message only | message and line number |
| Checksum table order | hash-map order | first use (deterministic) |
| Tests | none | unit tests, plus round trips against retail data |
| Source lines (without tests) | 2,196 | 3,182 |
| Binary size (macOS arm64) | 127 KB + 89 KB | 3.4 MB |

### Output differences

Decompiled text matches QScript, and so does compiled bytecode apart from checksum table order. The exceptions are these fixes:

- `\xHH` escapes compile to the byte. QScript dropped the byte, so `"r\xE9f\xE9rez"` came out as `"rrez"`. Nine scripts in the THUG source tree hit this: `qb` compiles their strings to match retail, QScript doesn't.
- `<<`, `>>`, `RANDOM_RANGE2` and `EndOfLineNumber` decompile. QScript dropped them silently.
- `0x` hex integers decompile without the stray `ENUM` output.
- Names that are keywords decompile quoted (`%"OR"`), so they don't compile back as keywords.
- Nested `RANDOM` blocks that end at the same place keep both `RANDOMEND`s.
- A script with no trailing newline decompiles without one.
- An unrecognized character is a compile error. QScript stopped there and wrote out the partial output.
- Labels (`name:`) and `JUMP` are compile errors. QScript wrote invalid bytecode for them.

The lexer is hand-written but follows Flex's rules (the longest match wins, and the earlier rule wins a tie). That includes Flex's odd comment behaviour: `/* a **/ b */` is a single comment.

## Benchmarks

Measured on an Apple M4 with macOS 27.0.1, Go 1.26.1, and QScript built with Apple clang 21 at `-O3` (CMake Release). Each figure is the median of several runs, on one thread.

The inputs are the retail scripts:
- **THUG1:** the 232 scripts in `qb.pre`, 3.1 MB of bytecode, which decompile to 4.4 MB of source.
- **THUG2:** 399 scripts from the `.prx` archives, 11.5 MB of bytecode, which decompile to 21.0 MB of source.
- **THAW:** the 268 scripts in `qb.pak` + `qb.pab`, 2.4 MB of compressed bytecode, which decompile to 5.0 MB of source.

### Library (in process)

The same files are loaded into memory and converted in a loop. `qb` uses the Go benchmarks below; QScript uses a small harness linked against `libQScript.QCompile.a` and `libQScript.QDecompile.a`.

| Task | QScript | qb | Speed-up |
|---|---:|---:|---:|
| Decompile THUG1 | 39.4 ms (79 MB/s) | 32.3 ms (96 MB/s) | 1.2× |
| Compile THUG1 | 51.4 ms (85 MB/s) | 25.6 ms (170 MB/s) | 2.0× |
| Decompile THUG2 | 186.8 ms (62 MB/s) | 120.6 ms (95 MB/s) | 1.5× |
| Compile THUG2 | 218.8 ms (96 MB/s) | 102.9 ms (204 MB/s) | 2.1× |
| Decompile THAW | | 66.5 ms (36 MB/s) | |
| Compile THAW | | 124.7 ms (42 MB/s) | |

THAW is slower per byte because every script body is LZSS compressed, so decompiling and compiling it include decompression and compression.

### Command line (wall clock)

| Task | QScript, one run per file | qb, one run per file | qb, one directory run |
|---|---:|---:|---:|
| Decompile THUG1 (232 files) | 477 ms | 875 ms | 75 ms |
| Compile THUG1 (232 files) | 574 ms | 691 ms | 50 ms |
| Decompile THUG2 (399 files) | 1,045 ms | | 195 ms |
| Compile THUG2 (399 files) | 1,100 ms | | 165 ms |
| Decompile THAW (268 files, names from `dbg.pak`) | | | 113 ms |
| Compile THAW (268 files) | | | 168 ms |

Starting a Go process costs more than starting a C++ one, so one run per file is slower with `qb`. Pass a directory instead: one `qb` run converts a whole game's scripts 6–10× faster than calling QScript once per file.

### Archives

QScript has no archive support, so these are `qb` only.

| Task | Time |
|---|---:|
| Unpack `qb.pre` (THUG1, 1.3 MB, 232 files) | 21 ms |
| Pack `qb.pre` | 182 ms |
| Unpack all 182 `.pre` archives in THUG1 `Data/pre` (320 MB) | 2.1 s |
| Read `qb.pre` in memory | 4.4 ms (696 MB/s unpacked) |
| Write `qb.pre` in memory | 173 ms (18 MB/s unpacked) |
| Unpack `qb.pak` + `qb.pab` (THAW, 2.4 MB, 268 files) | 16 ms |
| Pack `qb.pak` + `qb.pab` | 8 ms |

Packing a PRE is slower than unpacking because of the compressor. It is a faithful port of the original binary-tree LZSS encoder, which is what lets packed archives match the retail compressed data. PAK archives aren't compressed.

### Running the benchmarks

```bash
QB_SCRIPT_DIR=/path/to/unpacked/qb go test -run XXX -bench . ./qscript
QB_SCRIPT_DIR=/path/to/thug2/qb QB_TARGET=thug2 go test -run XXX -bench . ./qscript
QB_SCRIPT_DIR=/path/to/unpacked/thaw/qb QB_TARGET=thaw go test -run XXX -bench . ./qscript
QB_PRE_FILE=/path/to/Data/pre/qb.pre go test -run XXX -bench . ./pre
```

## Testing

```bash
go test ./...

# Round trips with real game data
QB_PRE_DIR=/path/to/Data/pre go test ./pre
QB_SCRIPT_DIR=/path/to/unpacked/qb go test ./qscript
QB_SCRIPT_DIR=/path/to/thug2/qb QB_TARGET=thug2 go test ./qscript
QB_PAK_DIR=/path/to/THAW/Game/data go test ./pak
QB_THAW_DIR=/path/to/THAW/Game/data go test .
```

These have been checked against the retail data:

- **THUG1 archives:** all 194 archives in `Data/pre` (182 `.pre`, 12 `.prf`/`.prg`) round trip through `unpack` and `pack` with identical entries. Padding bytes differ, because the retail files have uninitialized memory there.
- **THUG2 archives:** all 93 `.prx` archives round trip the same way.
- **THUG1 scripts:** 231 of the 232 scripts in `qb.pre` decompile and recompile to identical bytecode, apart from checksum table order. The one exception, `skateshop.qb`, contains two `EndOfLine` tokens in a row, which source text can't express.
- **THUG2 scripts:** all 399 scripts recompile with `-target thug2` to equivalent bytecode. Every jump lands on the same token as in retail. The only differences are runs of consecutive `EndOfLine` tokens, which get collapsed, and `printf` names that the original compiler left in the checksum table after removing the calls.
- **Hand-written source:** the 245 `.q` files in the THUG source tree compile to the same bytecode as QScript, except the nine affected by the `\x` fix.
- **THAW archives:** all 2,296 PAK archives under `Game/data` rewrite byte for byte. The other 80 files with a `.pak.wpc` name are sound banks and 47-byte stubs, which use a different format.
- **THAW scripts:** all 4,644 `.qb` files in those archives (`.qb`, `.sqb` cutscene and `.mqb` model scripts) decompile and recompile byte for byte.
- **THAW workflow:** `unpack`, `decompile -names`, `compile -target thaw` and `pack` on `qb.pak` + `qb.pab` give back the retail archive byte for byte.

None of this has been run in the game itself; the checks are byte comparisons against retail files.
