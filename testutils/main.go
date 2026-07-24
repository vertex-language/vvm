// main.go
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm"
	"github.com/vertex-language/vvm/ir/vir"
)

// testCase is one isolated, in-memory vir.Module check. Each case checks
// exactly one thing: a single printed integer, a single printed float, or
// an exit code — never a combination of several opcodes' worth of behavior
// in one build func. If a case's build func needs a loop or a branch to
// express what it's testing, it almost certainly belongs in control_flow.go
// rather than wherever you were about to put it.
//
// There is deliberately no per-case target selection (no hostArches/
// hostOSes, no arch/osName parameters to build). vir is a target-
// independent IR — that's ir.md §1's entire premise — so which target a
// case builds for is a single fact about the host running this binary,
// not something each of a hundred-plus cases should restate. arch/osName
// are resolved once in run() and read from there by every helper in
// helpers.go.
type testCase struct {
	name  string
	build func() *vir.Module

	wantValue      *int64   // checked against parsed integer stdout when non-nil
	wantFloatValue *float64 // checked against parsed float stdout when non-nil (see floatMatches)
	wantExit       int
}

var registry []testCase

// register is called from each file's own init() — a new case is "add it
// to the right grouped file, call register," nothing else to wire up.
func register(c testCase) { registry = append(registry, c) }

func val(v int64) *int64      { return &v }
func fval(v float64) *float64 { return &v }

// arch/osName are the vir-canonical target this whole run builds against.
// Set once in run(), before the registry is walked — every build func and
// every helpers.go function reads these package-level values rather than
// taking a target as a parameter.
var arch, osName string

func hostArch() (string, bool) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", true
	case "arm64":
		return "aarch64", true
	case "386":
		return "x86", true
	case "arm":
		return "arm", true
	}
	return "", false
}

func hostOS() (string, bool) {
	switch runtime.GOOS {
	case "linux":
		return "linux", true
	case "darwin":
		return "macos", true
	case "windows":
		return "windows", true
	}
	return "", false
}

// floatMatches compares a parsed float result against an expected value.
// math.NaN() as want means "expect NaN"; want == 0 additionally checks the
// sign bit, since min/max's signed-zero behavior (ir.md §4) is exactly the
// kind of thing plain == would silently paper over (-0.0 == 0.0 in IEEE).
// Everything else is an epsilon compare, since printf's "%f" only carries
// six decimal digits.
func floatMatches(got, want float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	if want == 0 {
		return got == 0 && math.Signbit(got) == math.Signbit(want)
	}
	diff := math.Abs(got - want)
	tol := 1e-6 * math.Max(1, math.Abs(want))
	return diff <= tol
}

// run executes every registered case, prints one PASS/FAIL line per case,
// and returns a process exit code. Nothing is skipped: a target this
// host's vvm build can't actually run (e.g. no crt stub registered for
// this (arch, os) yet) surfaces as a RunModule error and a FAIL, which is
// the honest outcome — quietly skipping it would hide exactly the gap
// worth knowing about.
func run() int {
	var archOK, osOK bool
	arch, archOK = hostArch()
	osName, osOK = hostOS()
	if !archOK || !osOK {
		fmt.Printf("vvmtest: unrecognized host GOARCH=%s GOOS=%s\n", runtime.GOARCH, runtime.GOOS)
		return 1
	}
	fmt.Printf("running against %s-%s\n\n", arch, osName)

	failed := 0
	for _, c := range registry {
		m := c.build()
		res, err := vvm.RunModule(m)
		switch {
		case err != nil:
			failed++
			fmt.Printf("FAIL  %-28s RunModule error: %v\n", c.name, err)
		case res.ExitCode != c.wantExit:
			failed++
			fmt.Printf("FAIL  %-28s exit = %d, want %d\n", c.name, res.ExitCode, c.wantExit)
		case c.wantValue != nil:
			got, perr := strconv.ParseInt(strings.TrimSpace(string(res.Stdout)), 10, 64)
			if perr != nil {
				failed++
				fmt.Printf("FAIL  %-28s stdout %q not a plain integer: %v\n", c.name, res.Stdout, perr)
			} else if got != *c.wantValue {
				failed++
				fmt.Printf("FAIL  %-28s value = %d, want %d\n", c.name, got, *c.wantValue)
			} else {
				fmt.Printf("PASS  %-28s = %d\n", c.name, got)
			}
		case c.wantFloatValue != nil:
			got, perr := strconv.ParseFloat(strings.TrimSpace(string(res.Stdout)), 64)
			if perr != nil {
				failed++
				fmt.Printf("FAIL  %-28s stdout %q not a plain float: %v\n", c.name, res.Stdout, perr)
			} else if !floatMatches(got, *c.wantFloatValue) {
				failed++
				fmt.Printf("FAIL  %-28s value = %v, want %v\n", c.name, got, *c.wantFloatValue)
			} else {
				fmt.Printf("PASS  %-28s = %v\n", c.name, got)
			}
		default:
			fmt.Printf("PASS  %-28s (exit %d)\n", c.name, res.ExitCode)
		}
	}

	fmt.Printf("\n%d/%d passed\n", len(registry)-failed, len(registry))
	if failed > 0 {
		return 1
	}
	return 0
}

func main() {
	os.Exit(run())
}