// target.go — (arch, os, abi[, tier]) triple shared with VIR's target-decl grammar.
package elf

import (
	"fmt"
	"strings"
)

type OS int

const (
	OSNone OS = iota
	OSLinux
	OSFreeBSD
	OSNetBSD
	OSOpenBSD
	OSAndroid
)

func (o OS) String() string {
	switch o {
	case OSLinux:
		return "linux"
	case OSFreeBSD:
		return "freebsd"
	case OSNetBSD:
		return "netbsd"
	case OSOpenBSD:
		return "openbsd"
	case OSAndroid:
		return "android"
	case OSNone:
		return "none"
	}
	return "unknown"
}

type ABI int

const (
	ABINone ABI = iota
	ABIGNU
	ABIMusl
	ABIEABI
	ABIEABIHF
	// Non-ELF ABIs. Valid() rejects these against any ELF-format OS; kept
	// here so a misrouted triple produces a clear error, not a parse failure.
	ABIMSVC
	ABIMachO
	ABIAAPCS64
)

func (a ABI) String() string {
	switch a {
	case ABIGNU:
		return "gnu"
	case ABIMusl:
		return "musl"
	case ABIEABI:
		return "eabi"
	case ABIEABIHF:
		return "eabihf"
	case ABIMSVC:
		return "msvc"
	case ABIMachO:
		return "macho"
	case ABIAAPCS64:
		return "aapcs64"
	}
	return "unknown"
}

// Tier is an optional feature-set restriction, e.g. "avx2". Zero value means
// no tier restriction and formats with no bracket suffix.
type Tier string

const TierNone Tier = ""

// Target is the (arch, os, abi[, tier]) triple. Arch is the same uint16
// e_machine-derived space used throughout this package (ArchX86_64, …).
type Target struct {
	Arch      Arch
	OS        OS
	ABI       ABI
	Tier      Tier
	BigEndian bool // derived from the parsed arch string (armeb, aarch64_be)
}

type archInfo struct {
	arch Arch
	be   bool
}

// archSpelling accepts only canonical spellings — no amd64/arm64/x64/i686
// aliases. Alias resolution belongs one layer up, at the CLI/build boundary.
var archSpelling = map[string]archInfo{
	"x86_64":      {ArchX86_64, false},
	"x86":         {ArchX86, false},
	"arm":         {ArchARM, false},
	"armeb":       {ArchARM, true},
	"aarch64":     {ArchARM64, false},
	"aarch64_be":  {ArchARM64, true},
	"riscv32":     {ArchRISCV32, false},
	"riscv64":     {ArchRISCV64, false},
	"powerpc":     {ArchPowerPC, false},
	"powerpc64":   {ArchPowerPC64, true},
	"powerpc64le": {ArchPowerPC64, false},
	"mips32":      {ArchMIPS, true},
	"mips32el":    {ArchMIPS, false},
	"mips64":      {ArchMIPS64, true},
	"mips64el":    {ArchMIPS64, false},
	"loongarch64": {ArchLoongArch64, false},
	"s390x":       {ArchS390X, true},
}

// archSpellingRev gives the canonical little-endian (or only) spelling for
// String(); big-endian variants are special-cased there.
var archSpellingRev = map[Arch]string{
	ArchX86_64:      "x86_64",
	ArchX86:         "x86",
	ArchARM:         "arm",
	ArchARM64:       "aarch64",
	ArchRISCV32:     "riscv32",
	ArchRISCV64:     "riscv64",
	ArchPowerPC:     "powerpc",
	ArchPowerPC64:   "powerpc64le",
	ArchMIPS:        "mips32el",
	ArchMIPS64:      "mips64el",
	ArchLoongArch64: "loongarch64",
	ArchS390X:       "s390x",
}

var osSpelling = map[string]OS{
	"linux": OSLinux, "freebsd": OSFreeBSD, "netbsd": OSNetBSD,
	"openbsd": OSOpenBSD, "android": OSAndroid, "none": OSNone,
}

var abiSpelling = map[string]ABI{
	"gnu": ABIGNU, "musl": ABIMusl, "eabi": ABIEABI, "eabihf": ABIEABIHF,
	"msvc": ABIMSVC, "macho": ABIMachO, "aapcs64": ABIAAPCS64,
}

// ParseTarget parses a canonical triple string, e.g. "x86_64-linux-gnu",
// "armeb-linux-eabihf", or "x86_64-linux-gnu[avx2]".
func ParseTarget(s string) (Target, error) {
	orig := s
	var tier Tier
	if i := strings.IndexByte(s, '['); i >= 0 {
		if !strings.HasSuffix(s, "]") {
			return Target{}, fmt.Errorf("target %q: unterminated tier suffix", orig)
		}
		tier = Tier(s[i+1 : len(s)-1])
		s = s[:i]
	}

	parts := strings.SplitN(s, "-", 3)
	if len(parts) != 3 {
		return Target{}, fmt.Errorf("target %q: expected arch-os-abi", orig)
	}
	ai, ok := archSpelling[parts[0]]
	if !ok {
		return Target{}, fmt.Errorf("target %q: unknown arch %q", orig, parts[0])
	}
	o, ok := osSpelling[parts[1]]
	if !ok {
		return Target{}, fmt.Errorf("target %q: unknown os %q", orig, parts[1])
	}
	ab, ok := abiSpelling[parts[2]]
	if !ok {
		return Target{}, fmt.Errorf("target %q: unknown abi %q", orig, parts[2])
	}

	t := Target{Arch: ai.arch, OS: o, ABI: ab, Tier: tier, BigEndian: ai.be}
	if err := t.Valid(); err != nil {
		return Target{}, err
	}
	return t, nil
}

// String round-trips ParseTarget's canonical spelling.
func (t Target) String() string {
	arch := archSpellingRev[t.Arch]
	if t.BigEndian {
		switch t.Arch {
		case ArchARM64:
			arch = "aarch64_be"
		case ArchARM:
			arch = "armeb"
		case ArchMIPS:
			arch = "mips32"
		case ArchMIPS64:
			arch = "mips64"
		case ArchPowerPC64:
			arch = "powerpc64"
		}
	}
	s := fmt.Sprintf("%s-%s-%s", arch, t.OS, t.ABI)
	if t.Tier != TierNone {
		s += "[" + string(t.Tier) + "]"
	}
	return s
}

func (t Target) WithTier(tier Tier) Target {
	t.Tier = tier
	return t
}

// elfMatrix enumerates every (arch, os) → allowed ABI set for the ELF format.
var elfMatrix = map[Arch]map[OS][]ABI{
	ArchX86_64: {
		OSLinux: {ABIGNU, ABIMusl}, OSFreeBSD: {ABIGNU}, OSNetBSD: {ABIGNU}, OSOpenBSD: {ABIGNU}, OSAndroid: {ABIGNU},
	},
	ArchX86: {
		OSLinux: {ABIGNU, ABIMusl}, OSFreeBSD: {ABIGNU}, OSNetBSD: {ABIGNU}, OSOpenBSD: {ABIGNU}, OSAndroid: {ABIGNU},
	},
	ArchARM: {
		OSLinux: {ABIEABI, ABIEABIHF}, OSFreeBSD: {ABIEABI, ABIEABIHF}, OSNetBSD: {ABIEABI, ABIEABIHF},
		OSOpenBSD: {ABIEABI, ABIEABIHF}, OSAndroid: {ABIEABI, ABIEABIHF}, OSNone: {ABIEABI, ABIEABIHF},
	},
	ArchARM64: {
		OSLinux: {ABIGNU, ABIMusl}, OSFreeBSD: {ABIGNU}, OSAndroid: {ABIGNU},
	},
	ArchRISCV32:     {OSLinux: {ABIGNU, ABIMusl}},
	ArchRISCV64:     {OSLinux: {ABIGNU, ABIMusl}, OSFreeBSD: {ABIGNU}},
	ArchPowerPC:     {OSLinux: {ABIGNU, ABIMusl}},
	ArchPowerPC64:   {OSLinux: {ABIGNU, ABIMusl}},
	ArchMIPS:        {OSLinux: {ABIGNU, ABIMusl}},
	ArchMIPS64:      {OSLinux: {ABIGNU, ABIMusl}},
	ArchLoongArch64: {OSLinux: {ABIGNU, ABIMusl}},
	ArchS390X:       {OSLinux: {ABIGNU, ABIMusl}},
}

// Valid checks that (arch, os, abi) is a real, spec-legal ELF combination.
// It does NOT check whether this build has codegen registered for it —
// see Linker.Supported() for that.
func (t Target) Valid() error {
	if t.ABI == ABIMSVC || t.ABI == ABIMachO || t.ABI == ABIAAPCS64 {
		return fmt.Errorf("target %s: abi %q is not an ELF-format ABI", t, t.ABI)
	}
	if t.BigEndian && t.Arch != ArchARM && t.Arch != ArchARM64 &&
		t.Arch != ArchMIPS && t.Arch != ArchMIPS64 && t.Arch != ArchPowerPC64 {
		return fmt.Errorf("target %s: arch has no big-endian variant", t)
	}
	byOS, ok := elfMatrix[t.Arch]
	if !ok {
		return fmt.Errorf("target %s: arch not valid for the ELF format", t)
	}
	abis, ok := byOS[t.OS]
	if !ok {
		return fmt.Errorf("target %s: os not valid for this arch under ELF", t)
	}
	for _, a := range abis {
		if a != t.ABI {
			continue
		}
		if t.OS == OSNone && t.Tier == TierNone {
			return fmt.Errorf("target %s: os=none requires a Tier that supplies a TLS convention", t)
		}
		return nil
	}
	return fmt.Errorf("target %s: abi %q not valid for this (arch, os)", t, t.ABI)
}