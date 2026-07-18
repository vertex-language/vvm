# linker

`github.com/vertex-language/vvm/linker` — container-format backends
for the VVM linker. This directory has no package of its own: there
is no `package linker`, no top-level `.go` file, and nothing to
import at this path. It exists purely to hold the three sibling
sub-packages and to say, once, how they relate to each other.

```
linker/
├── README.md
├── elf/     // ELF64 — linux, freebsd, netbsd, openbsd, android, none
├── macho/   // Mach-O — macos, ios, tvos, watchos, bridgeos, driverkit, visionos, maccatalyst
└── pe/      // PE32+ — windows, uefi
```

Pick your sub-package by `os`, not by anything at this level:

| `os` | Import |
|---|---|
| `linux`, `freebsd`, `netbsd`, `openbsd`, `android`, `none` | `github.com/vertex-language/vvm/linker/elf` |
| `macos`, `ios`, `ios-simulator`, `maccatalyst`, `tvos`, `watchos`, `watchos-simulator`, `bridgeos`, `driverkit`, `visionos`, `visionos-simulator` | `github.com/vertex-language/vvm/linker/macho` |
| `windows`, `uefi` | `github.com/vertex-language/vvm/linker/pe` |

Each sub-package is a complete, independent linker for its format —
its own `Target`, `ParseTarget`, `Linker`, `Builder`, registry, and
arch subpackages. None of them import each other, and none of them
import anything at this level. `os` selects the format at the call
site (in whatever builds a `Target` from a `.vir` module's `target`
declaration, §10.2); this directory doesn't do that selection for you.

## Why one folder, no shared code

The three formats don't share an implementation, only a *shape*:

- Each has its own `Target` struct, but spelled the way that format's
  own native tooling spells it — `linker/elf`'s `Target` matches VIR's
  `(arch, os, abi[, tier])` grammar directly; `linker/macho`'s matches
  a Clang/`vtool` triple (`SDK`, `Environment`); `linker/pe`'s matches
  `link.exe`/`clang-cl` naming (`/MACHINE:X64`, not `AMD64`).
- Each has its own `Patcher`/`PLTPatcher` registry and its own set of
  arch subpackages, registered via blank-import and `init()` —
  `linker/elf/x86_64`, `linker/macho/arm64e`, `linker/pe/arm64ec`, etc.
  Adding an arch to one format never touches another.
- Each has its own `Linker.Link()` pipeline (parse → symbol resolution
  → section merge → GC → PLT injection → layout → patch → emit), and
  the pipelines diverge in real, format-specific ways — Mach-O's
  zippering, PE's `.reloc`/base-relocation post-pass, ELF's
  interpreter/search-dir registry — not just naming.

Given that, a shared façade package would mostly be converting between
three already-similar-looking APIs for no functional gain, while
hiding the format-specific configuration (`SetZippered`, `SetSoname`,
`SetSubsystem`, `AddFramework`, sysroot probing, codesigning) that
callers actually need. So there isn't one — this README is the only
thing that lives above the three sub-packages, and its only job is to
route you to the right one.

## See also

- `linker/elf/README.md` — ELF64, target grammar, arch registry, GC,
  dynamic-linking helpers
- `linker/macho/README.md` — Mach-O, Apple triple grammar, universal
  binaries, `linker/macho/codesign`
- `linker/pe/README.md` — PE32+, COFF object/archive parsing, import
  thunks, base relocations