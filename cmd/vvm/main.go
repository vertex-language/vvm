// cmd/vvm/main.go
//
// vvm — command-line entry point for the vvm compiler/runtime.
//
//	vvm run <file>
//	vvm build <file1> [file2...] --target <arch-os-abi[tiers]> [-o <output>]
//
// This file does exactly two things: parse arguments, and call the
// top-level `vvm` package (Build/BuildGraph/Run). All actual
// pipeline logic lives in that package, not here.
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

  vvm build <file1> [file2...] [--target <arch-os-abi[tiers]>] [-o <output>]
      Compile one or more modules into a standalone, statically-linked executable.
      Multiple files are automatically linked via the import graph.

      --target string       target triple, e.g. "x86_64-linux-gnu" or
                             "aarch64-macos-none[avx2]". Optional if the primary
                             file carries its own in-file `+"`target`"+` declaration; 
                             required otherwise.
      -o string             output path (default: first input file's base
                             name, extension stripped)
      --root string         root module name for multi-file builds, used to 
                             resolve the entry point (default: "main")
      --min-os-version ver  required for macos/ios/watchos/tvos/visionos targets

  The <file> arguments may appear anywhere relative to the flags.

Target triples (see docs/ir.md §10 for the canonical vocabulary):
  arch: x86, x86_64, arm, armeb, aarch64, aarch64_be, ...
  os:   linux, macos, ios, watchos, tvos, visionos, windows, uefi, none, ...
  abi:  gnu, musl, msvc, eabi, eabihf, ...

Examples:
  vvm run add.vir
  vvm build math.vir main.vir -o myapp
  vvm build app.vbyte --target x86_64-linux-gnu -o app
`)
}

// splitArgs separates all non-flag positional arguments (e.g., file paths)
// from the flags. This allows users to type "vvm build a.vir b.vir -o app" 
// without flag.FlagSet.Parse() halting at the very first file it sees.
func splitArgs(args []string) (positionals []string, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "" {
			continue
		}
		if a[0] == '-' {
			flags = append(flags, a)
			// If this flag takes a value, grab the next token too so it 
			// doesn't accidentally get swept up as a positional file.
			if isValueFlag(a) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positionals = append(positionals, a)
		}
	}
	return positionals, flags
}

// isValueFlag reports whether a is one of vvm's own flags that consumes a
// separate following argument as its value.
func isValueFlag(a string) bool {
	switch a {
	case "-o", "--o", "-target", "--target", "-min-os-version", "--min-os-version", "-root", "--root":
		return true
	}
	return false
}

// --- vvm run --------------------------------------------------------------

func cmdRun(args []string) int {
	positionals, flags := splitArgs(args)

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm run <file.vir|file.vbyte>")
	}
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	
	// vvm.Run currently only accepts a single file (no multi-module run yet)
	if len(positionals) != 1 || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	src, err := os.ReadFile(positionals[0])
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
	positionals, flags := splitArgs(args)

	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	var (
		targetStr    string
		output       string
		minOSVersion string
		rootModule   string
	)
	fs.StringVar(&targetStr, "target", "", "target triple, e.g. x86_64-linux-gnu")
	fs.StringVar(&output, "o", "", "output path (default: first input file's base name)")
	fs.StringVar(&minOSVersion, "min-os-version", "", "required for macos/ios/watchos/tvos/visionos targets")
	fs.StringVar(&rootModule, "root", "main", "root module name for entry point resolution in multi-file builds")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm build <file1> [file2...] [--target <triple>] [-o <output>]")
	}
	
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if len(positionals) == 0 {
		fs.Usage()
		return 2
	}

	// Read all provided source files
	var srcs [][]byte
	for _, path := range positionals {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vvm: reading %s: %v\n", path, err)
			return 1
		}
		srcs = append(srcs, src)
	}

	// We sniff the *first* file for an in-file target declaration.
	// In a multi-file build, it's conventional for the root/main file 
	// to dictate the target if one isn't passed via CLI.
	declared, hasDeclared, derr := vvm.ModuleTarget(srcs[0])
	if derr != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", derr)
		return 1
	}

	var target vvm.Target
	switch {
	case targetStr == "" && hasDeclared:
		target = declared
	case targetStr == "" && !hasDeclared:
		fmt.Fprintln(os.Stderr, "vvm: --target is required (primary file has no in-file target declaration)")
		fs.Usage()
		return 2
	default:
		parsed, err := vvm.ParseTarget(targetStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
			return 2
		}
		if hasDeclared && (parsed.Arch != declared.Arch || parsed.OS != declared.OS || parsed.ABI != declared.ABI) {
			fmt.Fprintf(os.Stderr,
				"vvm: --target %s conflicts with the primary file's own target declaration %s\n",
				parsed, declared)
			return 2
		}
		target = parsed
	}
	target.MinOSVersion = minOSVersion

	// Build the actual binary: route to BuildGraph if we have multiple files,
	// or the fast-path Build if we only have one.
	var out []byte
	var buildErr error
	if len(srcs) > 1 {
		out, buildErr = vvm.BuildGraph(srcs, rootModule, target)
	} else {
		out, buildErr = vvm.Build(srcs[0], target)
	}

	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", buildErr)
		return 1
	}

	// Default output to the first file's name
	if output == "" {
		base := filepath.Base(positionals[0])
		output = strings.TrimSuffix(strings.TrimSuffix(base, ".vbyte"), ".vir")
	}

	if err := os.WriteFile(output, out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "vvm: writing %s: %v\n", output, err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "vvm: wrote %s (%s)\n", output, target)
	return 0
}