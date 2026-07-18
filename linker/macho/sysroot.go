package macho

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var platformDirs = map[SDK]string{
	SDKMacOSX:          "MacOSX.platform",
	SDKiPhoneOS:        "iPhoneOS.platform",
	SDKiPhoneSimulator: "iPhoneSimulator.platform",
	SDKAppleTVOS:       "AppleTVOS.platform",
	SDKAppleTVSimulator: "AppleTVSimulator.platform",
	SDKWatchOS:         "WatchOS.platform",
	SDKWatchSimulator:  "WatchSimulator.platform",
	SDKXROS:            "XROS.platform",
	SDKXRSimulator:     "XRSimulator.platform",
	SDKDriverKit:       "DriverKit.platform",
}

// sdkNameForXcrun is the SDK id xcrun expects, accounting for the
// simulator-as-Environment normalization ParseTarget performs.
func sdkNameForXcrun(t Target) SDK {
	if t.Environment != EnvSimulator {
		return t.SDK
	}
	switch t.SDK {
	case SDKiPhoneOS:
		return SDKiPhoneSimulator
	case SDKAppleTVOS:
		return SDKAppleTVSimulator
	case SDKWatchOS:
		return SDKWatchSimulator
	case SDKXROS:
		return SDKXRSimulator
	}
	return t.SDK
}

// resolveSysroot finds the SDK path for t: explicit override wins, then
// `xcrun --sdk <name> --show-sdk-path`, then a direct scan of the active
// developer directory.
func resolveSysroot(t Target, override string) (string, error) {
	if override != "" {
		return override, nil
	}

	sdkName := string(sdkNameForXcrun(t))
	if out, err := exec.Command("xcrun", "--sdk", sdkName, "--show-sdk-path").Output(); err == nil {
		if path := strings.TrimSpace(string(out)); path != "" {
			return path, nil
		}
	}

	devDir, err := exec.Command("xcode-select", "-p").Output()
	if err != nil {
		return "", fmt.Errorf("macho: resolve sysroot for %s: xcrun failed and xcode-select unavailable: %w", t, err)
	}
	platDir, ok := platformDirs[sdkNameForXcrun(t)]
	if !ok {
		return "", fmt.Errorf("macho: no known platform directory for sdk %q", sdkName)
	}
	sdksDir := filepath.Join(strings.TrimSpace(string(devDir)), "Platforms", platDir, "Developer", "SDKs")
	entries, err := os.ReadDir(sdksDir)
	if err != nil {
		return "", fmt.Errorf("macho: scanning %s: %w", sdksDir, err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sdk") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("macho: no .sdk directories found in %s", sdksDir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names))) // newest version first
	return filepath.Join(sdksDir, names[0]), nil
}

var _ = bytes.TrimSpace // placeholder to keep import set stable if trimmed later