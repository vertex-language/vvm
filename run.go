// run.go
package vvm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/vertex-language/vvm/ir/vir"
)

// Run is "vvm run": build for the host, execute the result immediately,
// clean up the temp binary — nothing fancier than `go run`.
func Run(src []byte) (RunResult, error) {
	m, err := decodeModule(src)
	if err != nil {
		return RunResult{}, fmt.Errorf("vvm: decode: %w", err)
	}
	return RunModule(m)
}

func RunModule(m *vir.Module) (RunResult, error) {
	t, err := hostTarget()
	if err != nil {
		return RunResult{}, err
	}

	bin, err := BuildModule(m, t)
	if err != nil {
		return RunResult{}, err
	}

	// tempExecPattern picks the CreateTemp pattern's suffix. Windows (and
	// UEFI) identify an executable by file extension — os/exec's
	// underlying CreateProcess call won't launch a file with no
	// recognized suffix at all, even when its bytes are a perfectly
	// valid PE image, so the temp file needs ".exe" here or exec.Command
	// below fails with "executable file not found in %PATH%". ELF and
	// Mach-O carry no such requirement; the +x bit set via os.Chmod below
	// is all either of those needs.
	pattern := "vvm-run-*"
	if t.isHostedProcessOS() && (t.OS == "windows" || t.OS == "uefi") {
		pattern = "vvm-run-*.exe"
	}

	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return RunResult{}, fmt.Errorf("vvm: run: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)

	if _, err := f.Write(bin); err != nil {
		f.Close()
		return RunResult{}, fmt.Errorf("vvm: run: writing temp binary: %w", err)
	}
	f.Close()
	if err := os.Chmod(path, 0o755); err != nil {
		return RunResult{}, fmt.Errorf("vvm: run: chmod: %w", err)
	}

	cmd := exec.Command(path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	res := RunResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}

	if exitErr, ok := runErr.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	if runErr != nil {
		return res, fmt.Errorf("vvm: run: %w", runErr)
	}
	return res, nil
}

// hostTarget derives a vvm.Target for the machine vvm itself is running
// on, so Run() needs no configuration.
func hostTarget() (Target, error) {
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "386":
		arch = "x86"
	case "arm":
		arch = "arm"
	default:
		return Target{}, fmt.Errorf("vvm: run: unsupported host GOARCH %q", runtime.GOARCH)
	}

	switch runtime.GOOS {
	case "linux":
		return Target{Arch: arch, OS: "linux", ABI: "gnu"}, nil
	case "darwin":
		return Target{Arch: arch, OS: "macos", ABI: "", MinOSVersion: "14.0"}, nil
	case "windows":
		return Target{Arch: arch, OS: "windows", ABI: "msvc"}, nil
	default:
		return Target{}, fmt.Errorf("vvm: run: unsupported host GOOS %q", runtime.GOOS)
	}
}