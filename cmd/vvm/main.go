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

  vvm build <file.vir|file.vbyte> --target <arch-os-abi[tiers]> [-o <output>]
      Compile to a standalone, statically-linked executable.

      --target string       required, e.g. "x86_64-linux-gnu" or
                             "aarch64-macos-none[avx2]" (see below)
      -o string              output path (default: input file's base
                              name, extension stripped)
      --min-os-version string  required for macos/ios/watchos/tvos/visionos
                                targets, e.g. "14.0"

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
`)
}

// --- vvm run --------------------------------------------------------------

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm run <file.vir|file.vbyte>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)

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
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	var (
		targetStr    string
		output       string
		minOSVersion string
	)
	fs.StringVar(&targetStr, "target", "", "target triple, e.g. x86_64-linux-gnu (required)")
	fs.StringVar(&output, "o", "", "output path (default: input file's base name)")
	fs.StringVar(&minOSVersion, "min-os-version", "", "required for macos/ios/watchos/tvos/visionos targets")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm build <file.vir|file.vbyte> --target <triple> [-o <output>] [--min-os-version <ver>]")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)

	if targetStr == "" {
		fmt.Fprintln(os.Stderr, "vvm: --target is required for build")
		fs.Usage()
		return 2
	}

	target, err := vvm.ParseTarget(targetStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 2
	}
	target.MinOSVersion = minOSVersion

	if output == "" {
		base := filepath.Base(path)
		output = strings.TrimSuffix(strings.TrimSuffix(base, ".vbyte"), ".vir")
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 1
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