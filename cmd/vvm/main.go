// cmd/vvm/main.go
//
// vvm — command-line entry point for the vvm compiler/runtime.
//
//	vvm run <file.vir|file.vbyte>
//	vvm build <file...> [--target ...] [-o <output>]
//	vvm targets <file>
//
// This file does exactly three things: parse arguments, classify each
// input as host or device source, and call the top-level `vvm` package
// (Build/BuildGraph/Run for host, BuildDevice for device). All actual
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
	case "targets":
		return cmdTargets(args[1:])
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
      execute it immediately, and stream its output. Host-only —
      a .gvir device module has no process image to run.

  vvm build <file...> [--target <t>] [-o <output>]
      Host (.vir/.vbyte): compile one or more modules into a standalone,
      statically-linked executable. Multiple files are linked via the
      import graph.

      Device (.gvir): lower one kernel module into vendor-toolchain
      source — one artifact per target the module itself declares.

      Host and device modules cannot be mixed in one build.

  vvm targets <file>
      Print the target(s) the file's own declaration names, one per
      line, without building anything.

Host build flags (.vir / .vbyte):
      --target string       target triple, e.g. "x86_64-linux-gnu" or
                             "aarch64-macos-none[avx2]". Optional if the primary
                             file carries its own in-file `+"`target`"+` declaration;
                             required otherwise.
      -o string             output path (default: first input file's base
                             name, extension stripped)
      --root string         root module name for multi-file builds, used to
                             resolve the entry point (default: "main")
      --min-os-version ver  required for macos/ios/watchos/tvos/visionos targets

Device build flags (.gvir):
      --target list         comma-separated subset of the artifacts the module
                             declares, e.g. "ptx", "amdgcn:gfx90a", "ptx,msl".
                             Selects among declared targets — it cannot add one.
                             Default: every declared artifact.
      -o path               output directory (default: current directory).
                             May name a single file only when the build
                             produces exactly one artifact.
      --kernel name         build only this kernel; repeatable.
      --debug               emit source locations where supported (amdtx only).
      --strict-gating       treat §4.3 capability exclusions as a build failure.

Host target triples (see docs/ir.md §10 for the canonical vocabulary):
  arch: x86, x86_64, arm, armeb, aarch64, aarch64_be, ...
  os:   linux, macos, ios, watchos, tvos, visionos, windows, uefi, none, ...
  abi:  gnu, musl, msvc, eabi, eabihf, ...

Device backends:
  ptx      NVIDIA PTX          -> <module>.ptx
  amdgcn   AMD GCN/RDNA        -> <module>.<arch>.amdtx  (one per declared arch)
  msl      Metal Shading Lang  -> <module>.metal

Examples:
  vvm run add.vir
  vvm build math.vir main.vir -o myapp
  vvm build app.vbyte --target x86_64-linux-gnu -o app
  vvm build reduce.gvir
  vvm build reduce.gvir --target ptx -o build/reduce.ptx
  vvm build reduce.gvir --target amdgcn --kernel sum --debug -o build/
  vvm targets reduce.gvir
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
// separate following argument as its value. --debug and --strict-gating
// are deliberately absent: they're booleans, and swallowing the next
// token would eat a file path.
func isValueFlag(a string) bool {
	switch a {
	case "-o", "--o",
		"-target", "--target",
		"-min-os-version", "--min-os-version",
		"-root", "--root",
		"-kernel", "--kernel":
		return true
	}
	return false
}

// stringList backs the repeatable --kernel flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// readSources reads every path and classifies the set as host or device.
// Mixing the two is refused here, at the front door: there is no host↔device
// link or launch path yet, so a build naming both files has no meaning
// vvm could act on — and guessing one of them was a mistake would be
// worse than saying so.
func readSources(paths []string) (srcs [][]byte, kind vvm.SourceKind, err error) {
	var firstPath string
	for i, path := range paths {
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, 0, fmt.Errorf("reading %s: %v", path, rerr)
		}
		k, kerr := vvm.SourceKindOf(src)
		if kerr != nil {
			return nil, 0, fmt.Errorf("%s: %v", path, kerr)
		}
		if i == 0 {
			kind, firstPath = k, path
		} else if k != kind {
			return nil, 0, fmt.Errorf(
				"cannot mix host and device modules in one build: %s is %s, %s is %s — "+
					"there is no host-side launch or link path for .gvir yet; build them separately",
				firstPath, kind, path, k)
		}
		srcs = append(srcs, src)
	}
	return srcs, kind, nil
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

	// vvm.Run rejects a device module itself, with a better message than
	// anything this file could produce — no pre-check needed here.
	res, err := vvm.Run(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	return res.ExitCode
}

// --- vvm targets -----------------------------------------------------------

func cmdTargets(args []string) int {
	positionals, flags := splitArgs(args)

	fs := flag.NewFlagSet("targets", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm targets <file>")
	}
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if len(positionals) != 1 {
		fs.Usage()
		return 2
	}

	src, err := os.ReadFile(positionals[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 1
	}
	kind, err := vvm.SourceKindOf(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %s: %v\n", positionals[0], err)
		return 1
	}

	if kind == vvm.SourceDevice {
		sels, err := vvm.DeviceTargets(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		for _, s := range sels {
			fmt.Println(s)
		}
		return 0
	}

	t, ok, err := vvm.ModuleTarget(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if !ok {
		// Not an error: a pure-compute module carries no target-decl and
		// stays buildable for any triple. Say so on stderr, print nothing
		// on stdout, so a script capturing stdout sees an honest empty set.
		fmt.Fprintln(os.Stderr, "vvm: no in-file target declaration (buildable for any triple via --target)")
		return 0
	}
	fmt.Println(t)
	return 0
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
		kernels      stringList
		debug        bool
		strictGating bool
	)
	fs.StringVar(&targetStr, "target", "", "target triple (host) or comma-separated backend[:arch] list (device)")
	fs.StringVar(&output, "o", "", "output path (host: file; device: directory)")
	fs.StringVar(&minOSVersion, "min-os-version", "", "required for macos/ios/watchos/tvos/visionos targets")
	fs.StringVar(&rootModule, "root", "main", "root module name for entry point resolution in multi-file builds")
	fs.Var(&kernels, "kernel", "device only: build only this kernel (repeatable)")
	fs.BoolVar(&debug, "debug", false, "device only: emit source locations where supported")
	fs.BoolVar(&strictGating, "strict-gating", false, "device only: treat capability exclusions as a build failure")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vvm build <file...> [--target <t>] [-o <output>]")
	}

	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if len(positionals) == 0 {
		fs.Usage()
		return 2
	}

	// Which flags the user actually typed, so a flag that means nothing
	// for the pipeline the inputs selected is an error rather than a
	// silently ignored instruction.
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	srcs, kind, err := readSources(positionals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 1
	}

	if kind == vvm.SourceDevice {
		for _, f := range []string{"root", "min-os-version"} {
			if given[f] {
				fmt.Fprintf(os.Stderr,
					"vvm: --%s is a host build flag and means nothing for a .gvir module "+
						"(device modules have no imports, no entry point, and no OS)\n", f)
				return 2
			}
		}
		return buildDevice(srcs, positionals, targetStr, output, kernels, debug, strictGating)
	}

	for _, f := range []string{"kernel", "debug", "strict-gating"} {
		if given[f] {
			fmt.Fprintf(os.Stderr,
				"vvm: --%s is a device build flag and means nothing for a .vir/.vbyte module\n", f)
			return 2
		}
	}
	return buildHost(srcs, positionals, targetStr, output, minOSVersion, rootModule, fs)
}

// --- host build ------------------------------------------------------------

func buildHost(srcs [][]byte, paths []string, targetStr, output, minOSVersion, rootModule string, fs *flag.FlagSet) int {
	// We sniff the *first* file for an in-file target declaration.
	// In a multi-file build, it's conventional for the root/main file
	// to dictate the target if one isn't passed via CLI.
	declared, hasDeclared, derr := vvm.ModuleTarget(srcs[0])
	if derr != nil {
		fmt.Fprintf(os.Stderr, "%v\n", derr)
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
			fmt.Fprintf(os.Stderr, "%v\n", err)
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
		fmt.Fprintf(os.Stderr, "%v\n", buildErr)
		return 1
	}

	// Default output to the first file's name
	if output == "" {
		base := filepath.Base(paths[0])
		output = strings.TrimSuffix(strings.TrimSuffix(base, ".vbyte"), ".vir")
	}

	if err := os.WriteFile(output, out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "vvm: writing %s: %v\n", output, err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "vvm: wrote %s (%s)\n", output, target)
	return 0
}

// --- device build ----------------------------------------------------------

func buildDevice(srcs [][]byte, paths []string, targetStr, output string, kernels []string, debug, strictGating bool) int {
	if len(srcs) > 1 {
		fmt.Fprintln(os.Stderr,
			"vvm: a device build takes exactly one .gvir module — .gvir has no imports "+
				"and no cross-module graph to resolve (see ir/gvir/README.md)")
		return 2
	}

	opts := vvm.DeviceOptions{
		Kernels:      kernels,
		Debug:        debug,
		StrictGating: strictGating,
	}
	if targetStr != "" {
		sels, err := vvm.ParseDeviceSelectors(targetStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 2
		}
		opts.Select = sels
	}

	arts, err := vvm.BuildDevice(srcs[0], opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	dests, err := deviceOutputPaths(arts, output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vvm: %v\n", err)
		return 2
	}

	for i, a := range arts {
		if err := os.WriteFile(dests[i], a.Source, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "vvm: writing %s: %v\n", dests[i], err)
			return 1
		}
		// Report exclusions before the "wrote" line for this artifact, so
		// the reason sits above the result it explains.
		for _, x := range a.Excluded {
			fmt.Fprintf(os.Stderr, "vvm: %s:%s: kernel %q excluded — %s unavailable\n",
				a.Backend, a.Arch, x.Kernel, x.Feature)
		}
		suffix := ""
		if n := len(a.Excluded); n > 0 {
			suffix = fmt.Sprintf(", %d kernel(s) excluded", n)
		}
		fmt.Fprintf(os.Stderr, "vvm: wrote %s (%s:%s%s)\n", dests[i], a.Backend, a.Arch, suffix)
	}
	return 0
}

// deviceOutputPaths resolves -o against a fan-out build.
//
// The rule: -o is a directory by default, and may name a single file only
// when there's exactly one artifact to put in it. Silently picking one
// artifact out of four to write to the given path — or overwriting the
// same path four times — are both worse than refusing.
func deviceOutputPaths(arts []vvm.Artifact, o string) ([]string, error) {
	if o == "" {
		dests := make([]string, len(arts))
		for i, a := range arts {
			dests[i] = a.Filename
		}
		return dests, nil
	}

	if looksLikeDir(o) {
		if err := os.MkdirAll(o, 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %v", o, err)
		}
		dests := make([]string, len(arts))
		for i, a := range arts {
			dests[i] = filepath.Join(o, a.Filename)
		}
		return dests, nil
	}

	if len(arts) == 1 {
		return []string{o}, nil
	}

	names := make([]string, len(arts))
	for i, a := range arts {
		names[i] = a.Backend + ":" + a.Arch
	}
	return nil, fmt.Errorf(
		"-o names a single file, but this build produces %d artifacts (%s) — "+
			"pass a directory, or narrow with --target",
		len(arts), strings.Join(names, ", "))
}

// looksLikeDir treats an existing directory, or any path written with a
// trailing separator, as a directory. A trailing separator is the
// unambiguous way to say "directory" for a path that doesn't exist yet —
// "-o build/" creates it, "-o build" with no existing dir is a filename.
func looksLikeDir(p string) bool {
	if strings.HasSuffix(p, "/") || strings.HasSuffix(p, string(os.PathSeparator)) {
		return true
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return true
	}
	return false
}