// cmd/vvm/main.go
//
// vvm — command-line entry point for the vvm compiler/runtime.
//
//	vvm run <file>
//	vvm build <file> --target <arch-os-abi[tiers]> [-o <output>] [--min-os-version <ver>]
//
// This file does exactly two things: parse arguments, and call the
// top-level `vvm` package (Build/BuildModule/Run/RunModule). All actual
// pipeline logic — decode, verify, lower, object, objectwriter, link —
// lives in that package, not here.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vertex-language/vvm"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "build":
		return cmdBuild(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "vvm: unknown command %q\n\n", args[0])
		printUsage()
		return 2
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `vvm — Vertex Virtual Machine & Compiler Framework

Usage:
  vvm run <file.vir|file.vbyte>
      Compile to a temporary native binary for the host platform,
      execute it immediately, and stream its output.

  vvm build <file.vir|file.vbyte> [--target <arch-os-abi[tiers]>] [-o <output>]
      Compile to a standalone, statically-linked executable.

      --target string       target triple, e.g. "x86_64-linux-gnu" or
                             "aarch64-macos-none[avx2]" (see below).
                             Optional if the file carries its own in-file
                             `+"`target`"+` declaration (ir.md §10.6); required
                             otherwise. If both are present they must agree.
      -o string              output path (default: input file's base
                              name, extension stripped)
      --min-os-version string  required for macos/ios/watchos/tvos/visionos
                                targets, e.g. "14.0"

  The <file> argument may appear anywhere relative to the flags — before,
  after, or between them.

Target triples (see docs/ir.md §10 for the canonical vocabulary):
  arch: x86, x86_64, arm, armeb, aarch64, aarch64_be, ...
  os:   linux, macos, ios, watchos, tvos, visionos, windows, uefi, none, ...
  abi:  gnu, musl, msvc, eabi, eabihf, ...
  e.g.  x86_64-linux-gnu
        aarch64-windows-msvc
        x86_64-macos-none --min-os-version 14.0

Examples:
  vvm run add.vir
  vvm build add.vbyte --target x86_64-linux-gnu -o add
  vvm build add.vir --target aarch64-macos-none --min-os-version 14.0 -o add
  vvm build hastarget.vir -o hastarget   // target read from the file itself
  vvm build main.vir -o main             // file first, flags after — also fine
`)
}

// splitPositional pulls the single expected positional argument (the input
// file path) out of args, wherever it falls relative to the flags, and
// returns it along with the remaining arguments in their original relative
// order for flag.FlagSet to parse.
//
// This exists because flag.FlagSet.Parse stops scanning for flags at the
// first argument that doesn't itself look like a flag (i.e. doesn't start
// with "-") — it does not permute a mixed positional/flag command line the
// way a getopt-style parser would. "vvm build main.vir -o main" therefore
// cannot be handed to fs.Parse as-is: it would see "main.vir" first, stop
// immediately, and report NArg()==3 with -o/main never recognized as a
// flag at all. Extracting the lone positional first, and only ever calling
// fs.Parse on the flag-only remainder, sidesteps that entirely regardless
// of where the file path was typed.
//
// This only works because vvm's subcommands take exactly one positional
// argument and no flag takes a bare (unattached) value that could be
// confused for it; a subcommand needing repeated or optional positionals
// would need a different scheme.
func splitPositional(args []string) (positional string, rest []string, ok bool) {
	for i, a := range args {
		if a == "" || a[0] == '-' {
			continue
		}
		// Not a flag itself — but if the immediately preceding argument
		// was a flag expecting a separate value (e.g. "-o main"), this
		// token belongs to that flag, not to us. We can't know here
		// which flags take values without duplicating the FlagSet's own
		// definitions, so the caller is required to only pass flags that
		// use "=" or are boolean when mixing with a positional... this
		// package instead sidesteps the ambiguity by only having flags
		// that vvm's own flag set defines, checked below via a lookahead
		// against known value-taking flag names.
		if i > 0 && isValueFlag(args[i-1]) {
			continue
		}
		positional = a
		rest = append(append([]string{}, args[:i]...), args[i+1:]...)
		return positional, rest, true
	}
	return "", args, false
}

// isValueFlag reports whether a is one of vvm's own flags that consumes a
// separate following argument as its value (as opposed to "--flag=value"
// form, which splitPositional already leaves alone since the value is
// fused into the same token and never mistaken for the positional).
func isValueFlag(a string) bool {
	switch a {
	case "-o", "--o", "-target", "--target", "-min-os-version", "--min-os-version":
		return true
	}
	return false
}

// --- vvm run --------------------------------------------------------------

func cmdRun(args []string) int {
	path, rest, ok := splitPositional(args)

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm run <file.vir|file.vbyte>")
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if !ok || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 1
	}

	res, err := vvm.Run(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 1
	}

	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	return res.ExitCode
}

// --- vvm build -------------------------------------------------------------

func cmdBuild(args []string) int {
	path, rest, ok := splitPositional(args)

	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	var (
		targetStr    string
		output       string
		minOSVersion string
	)
	fs.StringVar(&targetStr, "target", "", "target triple, e.g. x86_64-linux-gnu (optional if the file declares its own)")
	fs.StringVar(&output, "o", "", "output path (default: input file's base name)")
	fs.StringVar(&minOSVersion, "min-os-version", "", "required for macos/ios/watchos/tvos/visionos targets")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm build <file.vir|file.vbyte> [--target <triple>] [-o <output>] [--min-os-version <ver>]")
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if !ok || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 1
	}

	// A module with a link section or asm block is per-target by
	// construction and must carry a target-decl (ir.md §10.6); such a
	// file's own declaration is authoritative unless the invocation
	// explicitly overrides it, in which case the two must agree. Sniff
	// before deciding whether --target was actually required.
	declared, hasDeclared, derr := vvm.ModuleTarget(src)
	if derr != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", derr)
		return 1
	}

	var target vvm.Target
	switch {
	case targetStr == "" && hasDeclared:
		target = declared
	case targetStr == "" && !hasDeclared:
		fmt.Fprintln(os.Stderr, "vvm: --target is required (file has no in-file target declaration)")
		fs.Usage()
		return 2
	default: // targetStr != ""
		parsed, err := vvm.ParseTarget(targetStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
			return 2
		}
		if hasDeclared && (parsed.Arch != declared.Arch || parsed.OS != declared.OS || parsed.ABI != declared.ABI) {
			fmt.Fprintf(os.Stderr,
				"vvm: --target %s conflicts with the file's own target declaration %s (ir.md §10.6)\n",
				parsed, declared)
			return 2
		}
		target = parsed
	}
	target.MinOSVersion = minOSVersion

	if output == "" {
		base := filepath.Base(path)
		output = strings.TrimSuffix(strings.TrimSuffix(base, ".vbyte"), ".vir")
	}

	out, err := vvm.Build(src, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 1
	}

	if err := os.WriteFile(output, out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "vvm: writing %s: %v\n", output, err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "vvm: wrote %s (%s)\n", output, target)
	return 0
}