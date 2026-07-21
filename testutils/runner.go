// runner.go
package main

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/vertex-language/vvm"
	"github.com/vertex-language/vvm/ir/vir"
)

// testCase is one isolated, in-memory vir.Module check. We only assert on
// the numeric value the module prints (or its exit code) — never literal
// stdout bytes — so a case is "what value should this opcode sequence
// produce," not a string-formatting exercise.
type testCase struct {
	name       string
	hostArches []string // vir-canonical arch names this case can run on; nil = any
	hostOSes   []string // vir-canonical os names this case can run on; nil = any
	build      func(arch, osName string) *vir.Module
	wantValue  *int64 // checked against parsed stdout when non-nil
	wantExit   int
}

var registry []testCase

// register is called from each file's own init() — a new case is "add a
// file, call register," nothing else to wire up.
func register(c testCase) { registry = append(registry, c) }

func val(v int64) *int64 { return &v }

// intPrintingModule is the shared boilerplate every arithmetic/bitwise/
// comparison/conversion/memory/vector/control-flow case reuses: declare
// printf, run body, print whatever scalar operand body returns as a plain
// decimal, return 0.
//
// Caveat: printf's variadic promotion means anything narrower than i32
// must already be sext/zext'd to i32+ before it's returned here.
func intPrintingModule(name string, body func(fb *vir.FunctionBuilder) vir.Operand) *vir.Module {
	m := vir.NewModule(name)

	// "%d\x00" is 3 bytes: '%', 'd', NUL. No implicit NUL is added (§8),
	// so the array length must match exactly.
	fmtG := m.DeclareGlobal("fmt", vir.ArrayType{Elem: vir.I8, Len: 3},
		vir.InitByteString{Data: []byte("%d\x00")})

	ext := m.DeclareExternGroup("")
	ext.DeclareFunction("printf", []vir.Param{{Name: "f", Type: vir.Ptr}}, vir.I32).SetVariadic()

	fb := m.DeclareFunction("main", nil, vir.I32, true)
	result := body(fb)
	fb.Call("r", "printf", vir.Ident(fmtG.Name), result)
	fb.Return(vir.IntLiteral(0))
	return m
}

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

func matches(list []string, v string) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// run executes every registered case applicable to the host, prints one
// PASS/FAIL/SKIP line per case, and returns a process exit code.
func run() int {
	arch, archOK := hostArch()
	osName, osOK := hostOS()
	if !archOK || !osOK {
		fmt.Printf("vvmtest: unrecognized host GOARCH=%s GOOS=%s\n", runtime.GOARCH, runtime.GOOS)
		return 1
	}

	ran, failed, skipped := 0, 0, 0
	for _, c := range registry {
		if !matches(c.hostArches, arch) || !matches(c.hostOSes, osName) {
			skipped++
			fmt.Printf("SKIP  %-28s (needs arch=%v os=%v; host is %s/%s)\n",
				c.name, c.hostArches, c.hostOSes, arch, osName)
			continue
		}
		ran++
		m := c.build(arch, osName)
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
		default:
			fmt.Printf("PASS  %-28s (exit %d)\n", c.name, res.ExitCode)
		}
	}

	fmt.Printf("\n%d/%d passed, %d skipped\n", ran-failed, ran, skipped)
	if failed > 0 {
		return 1
	}
	return 0
}