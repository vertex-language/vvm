# linker/macho — Mach-O linker (Apple-native naming)

Mach-O sub-package for `github.com/vertex-language/vvm/linker`. This
package emits the container format used by every Apple OS. Naming
mirrors what `xcodebuild`, and `xcrun` actually print — a `Target`
in this package.

## Import

```go
import "github.com/vertex-language/vvm/linker/macho"

// blank-import whichever arch backends you need registered:
import (
    _ "github.com/vertex-language/vvm/linker/macho/x86_64"
    _ "github.com/vertex-language/vvm/linker/macho/arm64"
    _ "github.com/vertex-language/vvm/linker/macho/arm64e"
    _ "github.com/vertex-language/vvm/linker/macho/arm64_32"
)
```

---

## Quick start

```go
// Same shape as `clang -target arm64-apple-macosx14.0`.
t, err := macho.ParseTarget("arm64-apple-macosx14.0")
if err != nil {
    log.Fatal(err)
}

l := macho.NewLinker(t)
if !l.Supported() {
    log.Fatalf("%s: no codegen backend registered (blank-import its subpackage)", t)
}
l.SetEntryPoint("_main")

l.AddObject("main.o", mainBytes)
l.AddDynamicLibrary("libSystem.B.dylib", nil) // dyld-shared-cache-only

out, err := l.Link()
os.WriteFile("a.out", out, 0755)
```

---

## Target

Apple's own compiler driver identifies a target by a **triple**, not a
generic `(arch, os, abi)` struct — `clang -target arm64-apple-ios17.0-simulator`
is literally what a person or build system types, and `vtool -show` prints
the same information back out of a linked binary. `Target`'s
`String()`/`ParseTarget` match that shape exactly:

```
<arch>-apple-<sdk-name><min-os-version>[-<environment>]
```

```go
type Target struct {
    Arch        Arch        // ArchX86_64, ArchARM64, ArchARM64E, ArchARM64_32
    SDK         SDK         // canonical Xcode platform id: "macosx", "iphoneos", …
    MinVersion  Version     // encodes into LC_BUILD_VERSION.minos
    SDKVersion  Version     // encodes into LC_BUILD_VERSION.sdk (defaults to MinVersion)
    Environment Environment // EnvNone, EnvSimulator, EnvMacCatalyst
}

func ParseTarget(s string) (Target, error) // "arm64-apple-ios17.0-simulator"
func (t Target) String() string            // round-trips ParseTarget
func (t Target) Valid() error
```

`SDK` uses Apple's own canonical identifiers — the same strings that show
up as the `-sdk` flag to `xcodebuild`, as `"platform"` in
`xcodebuild -showsdks -json`, and as the `<Name>.platform` directory under
`Xcode.app/Contents/Developer/Platforms`:

```go
type SDK string

const (
    SDKMacOSX           SDK = "macosx"
    SDKiPhoneOS         SDK = "iphoneos"
    SDKiPhoneSimulator  SDK = "iphonesimulator"
    SDKAppleTVOS        SDK = "appletvos"
    SDKAppleTVSimulator SDK = "appletvsimulator"
    SDKWatchOS          SDK = "watchos"
    SDKWatchSimulator   SDK = "watchsimulator"
    SDKXROS             SDK = "xros"          // visionOS
    SDKXRSimulator      SDK = "xrsimulator"   // visionOS Simulator
    SDKDriverKit        SDK = "driverkit"
    SDKBridgeOS         SDK = "bridgeos"      // no public Xcode SDK; T2/Watch coprocessor only
)
```

`Environment` mirrors the triple's fourth component exactly as LLVM/clang
spell it — note Catalyst is `macabi` ("Mac ABI"), not `catalyst`:

```go
type Environment uint8

const (
    EnvNone        Environment = iota // device / native — no suffix in the triple
    EnvSimulator                      // "-simulator"
    EnvMacCatalyst                    // "-macabi"
)
```

`SDK` + `Environment` together resolve to the `LC_BUILD_VERSION.platform`
enum at emit time — e.g. `(SDKiPhoneSimulator, *)` and
`(SDKiPhoneOS, EnvSimulator)` both mean `PLATFORM_IOSSIMULATOR (7)`;
`(SDKMacOSX, EnvMacCatalyst)` means `PLATFORM_MACCATALYST (6)`. Both
spellings are accepted on parse (`ParseTarget` normalizes `iphonesimulator`
and `iphoneos-simulator` to the same `Target`), because real-world tooling
uses both conventions interchangeably.

### What's valid (`arch` × platform)

| `arch` | `macos` | `ios` (device) | `ios` (simulator) | `maccatalyst` | `tvos` | `watchos` (device) | `watchos` (sim) | `bridgeos` | `driverkit` | `visionos` |
|---|---|---|---|---|---|---|---|---|---|---|
| `x86_64` | ✓ | — | ✓ | ✓ | ✓ | — | ✓ | — | ✓ | — |
| `arm64` | ✓ (Rosetta-era Apple Silicon dev builds) | ✓ (older A-series) | ✓ | ✓ | ✓ | — | ✓ | — | ✓ | ✓ |
| `arm64e` | ✓ (default, Apple Silicon) | ✓ (default, A12+) | — | ✓ | ✓ | ✓ | — | ✓ | — | ✓ |
| `arm64_32` | — | — | — | — | — | ✓ (default, Series 4+) | — | — | — | — |

`Valid()` checks the triple is a real Apple-shipped combination but not
whether *this build* has codegen for it — `Linker.Supported()` answers that,
same split Xcode itself makes between "SDK lists this as a valid
destination" and "a toolchain is actually installed for it."

---

## Linker

```go
l := macho.NewLinker(t)
l.SetOutputType(macho.OutputExec)   // OutputExec | OutputPIE | OutputShared
l.SetEntryPoint("_main")
l.SetSoname("libfoo.dylib")
l.SetRpath("/usr/local/lib")
l.AddLibraryPath("/opt/lib")

l.AddObject("foo.o", data)
l.AddArchive("libbar.a", data)
l.AddDynamicLibrary("libSystem.B.dylib", nil)

out, err := l.Link()
```

`Linker.Supported()` reports whether a codegen backend is registered for
`Target.Arch` — i.e. whether the relevant subpackage has been
blank-imported. `Link()` fails fast with a clear error if it hasn't.

### Linking against dylibs vs. frameworks

Three ways to declare a shared-library dependency, in increasing order of
how much the linker knows about it:

```go
l.AddDynamicLibrary("libSystem.B.dylib", nil)     // stub only, dyld-shared-cache-only, no exports known
l.AddDynamicLibrary("libfoo.dylib", data)          // real parse: reads LC_ID_DYLIB, export trie, LC_SYMTAB
l.AddCachedDylib("libfoo.dylib", []string{"foo"})  // stub with pre-registered `_`-mangled exports
```

`AddFramework` is a thin convenience wrapper over `AddCachedDylib` for the
common case of linking against an Apple framework by its bare name, rather
than requiring you to spell out the `<Name>.framework/<Name>` install-name
convention yourself:

```go
l.AddFramework("Foundation", []string{"NSLog", "NSStringFromClass"})
l.AddFramework("UIKit", nil) // declares the dependency; no exports pre-registered
```

This is equivalent to:

```go
l.AddCachedDylib("Foundation.framework/Foundation", []string{"NSLog", "NSStringFromClass"})
```

Because `AddDynamicLibrary`/`AddCachedDylib` set `Soname` to the literal
name passed in (unlike parsing a real dylib, where `Soname` is derived via
`dylibBasename` and would strip everything before the last `/`), the
`.framework/` segment survives intact through to emit time. `findInstallPath`
(`builder.go`) checks for that segment and rewrites the final
`LC_LOAD_DYLIB` path to `/System/Library/Frameworks/<Name>.framework/<Name>`
automatically — you never need to hardcode the `/System/Library/Frameworks`
prefix yourself.

Prefer `AddFramework`/`AddCachedDylib` with an explicit symbol list over a
bare `AddDynamicLibrary(name, nil)` stub whenever you need specific symbols
resolved against that framework: a plain stub has no `Exports`, so any
undefined symbol left over after all other inputs are processed falls
through to the blunt "first stub lib absorbs every remaining undefined"
fallback in `SymbolTable.Ingest` (`symtab.go`), which doesn't verify the
symbol is one that library actually provides.

### `LC_BUILD_VERSION`, not `LC_VERSION_MIN_*`

Xcode 10 (macOS 10.14 / iOS 12 / tvOS 12 / watchOS 5 toolchains) replaced
the four per-platform `LC_VERSION_MIN_MACOSX` / `LC_VERSION_MIN_IPHONEOS` /
`LC_VERSION_MIN_TVOS` / `LC_VERSION_MIN_WATCHOS` commands with one
platform-tagged `LC_BUILD_VERSION`. This package emits only
`LC_BUILD_VERSION` — the legacy commands are not emitted.

| Triple (`SDK` + `Environment`) | `LC_BUILD_VERSION.platform` |
|---|---|
| `macosx` | `PLATFORM_MACOS` (1) |
| `iphoneos` | `PLATFORM_IOS` (2) |
| `appletvos` | `PLATFORM_TVOS` (3) |
| `watchos` | `PLATFORM_WATCHOS` (4) |
| `bridgeos` | `PLATFORM_BRIDGEOS` (5) |
| `macosx` + `macabi` | `PLATFORM_MACCATALYST` (6) |
| `iphonesimulator` | `PLATFORM_IOSSIMULATOR` (7) |
| `appletvsimulator` | `PLATFORM_TVOSSIMULATOR` (8) |
| `watchsimulator` | `PLATFORM_WATCHOSSIMULATOR` (9) |
| `driverkit` | `PLATFORM_DRIVERKIT` (10) |
| `xros` | `PLATFORM_VISIONOS` (11) |
| `xrsimulator` | `PLATFORM_VISIONOSSIMULATOR` (12) |

`PLATFORM_FIRMWARE` (13) / `PLATFORM_SEPOS` (14) are Apple-internal
(Secure Enclave / firmware images Apple builds itself) — `Valid()` rejects
them as targets.

### Zippered binaries (macOS + Mac Catalyst in one slice)

A macOS dylib meant to be loadable by both a native macOS process and a Mac
Catalyst process is "zippered": it carries **two** `LC_BUILD_VERSION`
commands in the same slice — one `PLATFORM_MACOS`, one
`PLATFORM_MACCATALYST` — rather than two separate slices. This is different
from a fat/universal binary (which picks one *slice* by arch) and different
from a single-platform binary (one `LC_BUILD_VERSION`).

```go
l.SetZippered(true) // emit both LC_BUILD_VERSION commands for this slice
```

Valid only when `Target.SDK == SDKMacOSX` and `Target.Environment == EnvNone`
— Catalyst-only or device-only builds get a single `LC_BUILD_VERSION` as
usual; `Link()` returns an error if `SetZippered(true)` is called on any
other target.

### Sysroot resolution mirrors `xcrun`

Apple's own tools resolve an SDK path via
`xcrun --sdk <name> --show-sdk-path`, which ultimately reads
`Xcode.app/Contents/Developer/Platforms/<PlatformDir>.platform/Developer/SDKs/<SDKName><version>.sdk`.

```go
l.SetSysroot("/path/to/some.sdk") // explicit override — always wins
```

Auto-detection order when no override is set:

1. `xcrun --sdk <SDK> --show-sdk-path` (shells out — the only place this
   package invokes an external tool, and only for path resolution, never
   for codegen).
2. Direct scan of the active developer directory (`xcode-select -p`) for
   `Platforms/<PlatformDir>.platform/Developer/SDKs/*.sdk`, newest version
   first, if `xcrun` is unavailable.

| `SDK` | `<PlatformDir>` |
|---|---|
| `macosx` | `MacOSX.platform` |
| `iphoneos` | `iPhoneOS.platform` |
| `iphonesimulator` | `iPhoneSimulator.platform` |
| `appletvos` | `AppleTVOS.platform` |
| `appletvsimulator` | `AppleTVSimulator.platform` |
| `watchos` | `WatchOS.platform` |
| `watchsimulator` | `WatchSimulator.platform` |
| `xros` | `XROS.platform` |
| `xrsimulator` | `XRSimulator.platform` |
| `driverkit` | `DriverKit.platform` |

### Default dynamic linker (`dyld`)

```go
l.SetInterp(path string) // override; otherwise resolved from Target
```

| `Target.Environment` | Default |
|---|---|
| `EnvNone` (device/native) | `/usr/lib/dyld` |
| `EnvSimulator` | `/usr/lib/dyld_sim` |
| `EnvMacCatalyst` | `/usr/lib/dyld` (Catalyst runs the native macOS dyld) |

### Universal (fat) binaries

```go
fat, err := macho.Lipo([]macho.Slice{
    {Target: mustParse("x86_64-apple-macosx14.0"), Data: amd64Bytes},
    {Target: mustParse("arm64e-apple-macosx14.0"), Data: arm64eBytes},
})

slices, err := macho.ParseFat("a.out", fat)
```

Named `Lipo` deliberately — this is the same operation `lipo -create`
performs, composing independently-linked thin slices, not a linked
multi-arch graph. `fat_arch_64` is used automatically for any slice whose
file offset would exceed 4 GiB.

`ParseFat` only recovers `Arch` from each slice's cputype/cpusubtype — a
`fat_arch` entry doesn't carry SDK, deployment target, or environment;
those live in each slice's own `LC_BUILD_VERSION`. Parse the returned
`Data` with the object/dylib parsers if you need the full `Target`.

---

## Parsers

```go
obj, err    := macho.ParseObject("foo.o", data, target)   // MH_OBJECT; errors if the file's
                                                           // cputype/cpusubtype don't match target.Arch
ar, err     := macho.ParseArchive("libfoo.a", data, macho.ParseObject)
lib, err    := macho.ParseSharedLib("libfoo.dylib", data) // MH_DYLIB
slices, err := macho.ParseFat("libfoo.a", data)           // fat wrapper → per-Arch slices
```

All parsers require 64-bit Mach-O (`MH_MAGIC_64`); 32-bit input
(`MH_MAGIC`) is out of scope — see below.

---

## Code signing (`github.com/vertex-language/vvm/linker/macho/codesign`)

A standalone, from-scratch signer (ad-hoc + production CMS) that operates
on a **finished** Mach-O binary — thin or fat, `MH_EXECUTE`/`MH_DYLIB`/
`MH_BUNDLE` — independent of `Target`. It re-parses the file itself, so it
works equally on binaries this package emitted and on ones produced by
`ld`/`clang`. The linker's own `Link()` pipeline already appends a minimal
ad-hoc signature to every executable automatically (see `builder.go`); use
this package when you need something more — production (Developer ID)
signing, entitlements, hardened runtime, or re-signing an existing binary.

### Import

```go
import "github.com/vertex-language/vvm/linker/macho/codesign"
```

### Quick start — ad-hoc, in place

```go
result, err := codesign.SignFile("a.out", codesign.Options{
    Identifier: "com.example.a-out", // defaults to the file's base name
})
if err != nil {
    log.Fatal(err)
}
fmt.Printf("%s: %s signed (%s)\n", "a.out", result.Format, result.Identifier)
```

`SignFile` reads the file, signs every slice (all architectures of a
universal binary), and writes the result back via a temp-file rename —
**not** an in-place overwrite. This matters on Apple Silicon: the kernel
caches code signatures per vnode, so overwriting bytes in place can leave a
stale cached signature behind; `rename(2)` swaps the directory entry
atomically and forces re-evaluation on the next `exec`.

If you already have the bytes in memory (e.g. straight out of
`Linker.Link()`) and don't want a round-trip through disk, use `SignImage`
instead:

```go
signed, err := codesign.SignImage(linkedBytes, codesign.Options{
    Identifier: "com.example.a-out",
})
```

### `Options`

```go
type Options struct {
    Identifier   string             // CodeDirectory ident; default: file base name
    TeamID       string             // optional team identifier
    Identity     *codesign.Identity // nil => ad-hoc; non-nil => production CMS
    Force        bool               // overwrite/rewrite even without a reserved LC_CODE_SIGNATURE
    Hardened     bool               // set CS_RUNTIME (hardened runtime)
    Entitlements []byte             // raw XML entitlements plist (optional)
    HashType     uint8              // 0 => SHA-256
    Logger       *codesign.Logger   // nil => silent
}
```

By default, `SignFile`/`SignImage` require the target slice to already
carry an `LC_CODE_SIGNATURE` load command with reserved space (which
`Linker.Link()` always emits for executables) — this avoids accidentally
growing/relocating `__LINKEDIT` on a binary that wasn't built expecting a
signature. Pass `Force: true` to sign a binary that has none, at the cost
of a full `__LINKEDIT` resize.

### Ad-hoc vs. production signing

```go
// Ad-hoc: no identity, CS_ADHOC flag, empty Requirements set — same as
// `codesign -s -` (or what the linker itself already does automatically).
codesign.SignFile("a.out", codesign.Options{})

// Production: real cert + key, CMS SignedData signature (RFC 5652),
// implicit designated requirement (`identifier "<id>" and anchor apple generic`).
id, err := codesign.LoadIdentityPEM("developer_id.pem", "developer_id_key.pem")
if err != nil {
    log.Fatal(err)
}
codesign.SignFile("a.out", codesign.Options{
    Identifier: "com.example.a-out",
    TeamID:     "ABCDE12345",
    Identity:   id,
    Hardened:   true,
})
```

`LoadIdentityPEM` reads a leaf certificate (plus any intermediates
concatenated in the same file) and a private key from PEM, accepting
PKCS#8, PKCS#1, or EC private keys. PKCS#12 (`.p12`) is intentionally not
supported directly — the package is pure-stdlib by design; convert with
`openssl pkcs12` first if that's what you have.

Production signatures embed a CMS `SignedData` blob (`csmagicBlobWrapper`)
containing the certificate chain, a `SignerInfo` over the CodeDirectory's
SHA-256 digest, and Apple's `cdhashes`-as-plist signed attribute — the same
shape `codesign`/`productsign` produce, built from scratch without shelling
out to Apple's own tools.

### Entitlements and hardened runtime

```go
plist, _ := os.ReadFile("MyApp.entitlements")
codesign.SignFile("MyApp", codesign.Options{
    Identity:     id,
    Hardened:     true,  // CS_RUNTIME
    Entitlements: plist, // embedded as special slot -5 (csslotEntitlements)
})
```

Entitlements are embedded verbatim as the XML plist you provide
(`csmagicEmbeddedEntitlement`, 0xfade7171) — this package does not
generate or validate entitlement contents, only wraps and hashes them into
the correct special slot.

### Verbose diagnostics

```go
l := codesign.NewLogger(os.Stderr, codesign.VerbosityV2)
codesign.SignFile("a.out", codesign.Options{Logger: l})
```

| Level | Constant | Shows |
|---|---|---|
| 0 (default) | `VerbosityOff` | nothing |
| 1 | `VerbosityV1` | arch, format, identifier, one-line result — like `codesign -v` |
| 2 | `VerbosityV2` | CodeDirectory fields, CS flags, blob sizes, CDHash — like `codesign -vv` |
| 3 | `VerbosityV3` | full Mach-O header, every load command, all page hashes, timing — like `codesign -vvv` |

`*Logger` is nil-safe: every method is a no-op on a nil pointer, so
`Options{}` (no `Logger` set) is always silent without extra nil checks at
call sites.

### Inspecting without signing

```go
img, err := codesign.Parse(raw)       // parses thin or fat Mach-O
fmt.Println(img.FormatString())        // "Mach-O universal (arm64 x86_64)"
for _, sl := range img.Slices {
    fmt.Println(sl.ArchString())       // "arm64", "x86_64", …
}
```

`Parse` only supports 64-bit Mach-O slices (`mhMagic64`/`mhCigam64`),
matching the rest of this package's 64-bit-only scope.

### Low-level minimal-signature API

For callers building their own emit pipeline (this is what the top-level
`macho` package's `builder.go` uses internally to size and write the
linker's automatic ad-hoc signature) rather than re-signing a finished
file, `codesign.Size`/`codesign.Sign` operate directly on raw bytes without
going through `Parse`/`SignFile`:

```go
sigSize := codesign.Size(codeLimit, identifier) // bytes to reserve
// ... lay out __LINKEDIT and LC_CODE_SIGNATURE using sigSize ...
buf := make([]byte, sigSize)
codesign.Sign(buf, dataReader, identifier, codeLimit, textFileOff, textFileSize, isMainBinary)
```

This path always emits `CS_ADHOC | CS_LINKER_SIGNED` with zero special
slots and no Requirements blob — the exact minimal layout Apple's own
linker produces, distinct from the fuller `SignFile`/`SignImage` path
(which includes a real Requirements blob and, for production identities,
optional entitlements and a CMS signature).

---

## Folder layout

```
linker/macho/
├── README.md
├── target.go        // Target, ParseTarget, SDK/Environment/Arch, Valid()
├── registry.go      // Patcher/PLTPatcher/interp factory registries, Supported()
├── linker.go        // Linker struct, NewLinker, Link() pipeline, AddFramework
├── builder.go        // Emitter: header, load commands, LINKEDIT
├── layout.go         // Layout, MergeSections, AssignLayout, ResolveSymbolAddresses
├── gc.go             // dead-section elimination
├── dynamic.go        // PLT/GOT scaffolding, dyld rebase/bind/export-trie builders
├── buildversion.go    // LC_BUILD_VERSION / zippering platform resolution
├── sysroot.go          // xcrun-based SDK path resolution
├── notes.go            // content-derived UUID, LC_SOURCE_VERSION helper
├── object.go           // parseObject (MH_OBJECT)
├── archive.go          // ParseArchive (GNU/BSD ar)
├── shared.go           // parseSharedLib (MH_DYLIB, export trie + LC_SYMTAB fallback)
├── lipo.go              // Lipo / ParseFat — universal-binary slicing
├── patch.go              // Patcher/PLTPatcher interfaces, PatchFunc adapter, PatchAll
├── reader.go              // bounds-checked reader, ULEB128/SLEB128
├── constants.go           // Mach-O magic numbers, load command IDs, PlatformType
├── symtab.go               // SymbolTable, resolution rules
├── types.go                 // Object/Section/Symbol/Reloc types
│
├── x86_64/    // register.go, patch.go, plt.go — implemented
├── arm64/     // register.go, patch.go, plt.go — implemented (plain ARMv8, no PAC)
├── arm64e/    // register.go, patch.go, plt.go — registered; PLT stubs are NOT PAC-signed (see below)
├── arm64_32/  // register.go, patch.go, plt.go — registered; not yet ILP32-correct (see below)
│
├── codesign/  // standalone ad-hoc + production Mach-O code signer:
│   ├── macho.go            // Parse: thin/fat Mach-O reader, header + load-command walk, LogHeader
│   ├── sign.go              // SignFile / SignImage / Options, per-slice signing pipeline
│   ├── codedirectory.go      // buildCodeDirectory, cdHash, sortBlobs
│   ├── codesign.go            // Size / Sign — low-level minimal ad-hoc signature (used by builder.go)
│   ├── cms.go                  // buildCMS: RFC 5652 SignedData for production identities
│   ├── identity.go              // Identity, LoadIdentityPEM (PKCS#8/PKCS#1/EC key support)
│   ├── requirements.go            // designatedRequirement, emptyRequirements
│   ├── entitlements.go             // xmlEntitlements / derEntitlements blob wrapping
│   ├── blob.go                      // genericBlob, assembleSuperBlob
│   ├── blobs.go                      // magic numbers, slot/flag constants (cs_blobs.h mirror)
│   └── logger.go                      // nil-safe leveled Logger (-v/-vv/-vvv equivalents)
│
└── (out of scope — see "Retired architectures" below)
```

`arm64e` and `arm64_32` each get their own subpackage rather than a flag on
`arm64`, for the same reason `elf` gives `riscv32`/`riscv64` separate
subpackages: PAC-signed return addresses/function pointers (`arm64e`) and
an ILP32 data model on a 64-bit instruction set (`arm64_32`) are real
codegen differences, not a byte-order swap.

### Adding a new arch

```go
// linker/macho/arm64e/register.go
package arm64e

import "github.com/vertex-language/vvm/linker/macho"

func init() {
    macho.RegisterPatcher(macho.ArchARM64E, func(t macho.Target) macho.Patcher {
        return macho.PatchFunc(applyARM64E)
    })
    macho.RegisterPLTPatcher(macho.ArchARM64E, func(t macho.Target) macho.PLTPatcher {
        return pltPatcher{}
    })
    macho.RegisterDefaultInterp(macho.ArchARM64E, func(t macho.Target) string {
        return "/usr/lib/dyld"
    })
}
```

Factories receive the whole `Target` — this is what lets one `arm64`
subpackage serve `iphonesimulator`, `appletvsimulator`, and `watchsimulator`
uniformly (same codegen, different resolved dyld path) without three
near-duplicate subpackages. `Patcher` and `PLTPatcher` are stateless per
call — the stub→GOT address mapping flows explicitly as a `StubMap`
returned from `PatchPLT` and passed into every `Patcher.Apply` call, rather
than being stashed on shared mutable state.

---

## Known limitations

Two backends are registered and will link end-to-end, but are not yet
spec-correct — treat their output as "compiles and runs on non-hardened
paths," not as a drop-in replacement for `ld`'s output for these targets:

- **`arm64e`**: PLT stubs use the same unsigned `ADRP`+`LDR`+`BR` sequence
  as plain `arm64`, rather than pointer-authenticated (`PACIA`/`BRAA`)
  stubs. The binary carries the correct `arm64e` cputype/cpusubtype so
  tooling identifies it properly, but it does not exercise arm64e's actual
  security model.
- **`arm64_32`**: GOT slots and relocation writes are still 8 bytes wide
  (the shared `gotEntrySize` constant in `dynamic.go` isn't yet
  parameterized per-arch), where a real watchOS ILP32 binary needs 4-byte
  pointers throughout. `ARM64_RELOC_UNSIGNED` in particular will write past
  a real 4-byte pointer field.
- **`codesign`**: PKCS#12 (`.p12`) identity files are not supported —
  convert to PEM with `openssl pkcs12` first. DER-encoded entitlements
  (`derEntitlements`, 0xfade7172) are implemented but not yet wired into
  `Options`/`signSlice`; only the XML entitlements slot is currently
  populated end-to-end.

## Retired / out-of-scope architectures and platforms

Mirroring the real state of Apple's own toolchains — if current Xcode can't
target it, this package doesn't either:

| Arch / platform | Status |
|---|---|
| `i386` | Retired: Xcode 10 (2018) dropped 32-bit simulator; macOS 10.15 Catalina (2019) dropped all 32-bit app support. |
| `armv7` / `armv7s` (32-bit ARM) | Retired: Xcode 11 (2019) dropped 32-bit iOS device support. |
| `ppc` / `ppc64` (PowerPC) | Retired with the Rosetta/10.6 (2009) era. Kept in `constants.go` for historical `cputype` completeness only. |
| Pre-OS X NeXTSTEP CPU types (`m68k`, `vax`, `hppa`, `sparc`, `i860`, etc.) | Dead since ~2001; documented in `constants.go` for parser completeness only. |
| 32-bit Mach-O container (`MH_MAGIC`) | Not parsed. Every supported `Arch` is 64-bit-only. |
| `PLATFORM_FIRMWARE` / `PLATFORM_SEPOS` | Apple-internal (Secure Enclave/firmware); `Valid()` rejects them as link targets. |
| `bridgeos` | No public SDK/toolchain ships for it — `Valid()`-true (real platform, real binaries exist), `Supported()`-false until Apple documents a public path. |
| `xros`/`xrsimulator` PLT specifics | `Valid()`-true; codegen hasn't been verified against a physical visionOS SDK yet, tracked like `elf` tracks unverified psABI relocation math. |