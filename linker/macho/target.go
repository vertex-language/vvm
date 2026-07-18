package macho

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Arch identifies the target CPU architecture / ABI variant for codegen.
type Arch uint8

const (
	ArchX86_64 Arch = iota + 1
	ArchARM64
	ArchARM64E
	ArchARM64_32
)

func (a Arch) String() string {
	switch a {
	case ArchX86_64:
		return "x86_64"
	case ArchARM64:
		return "arm64"
	case ArchARM64E:
		return "arm64e"
	case ArchARM64_32:
		return "arm64_32"
	}
	return "unknown"
}

// SDK is Apple's canonical platform identifier (matches `xcodebuild -showsdks`,
// `-sdk` flags, and `<Name>.platform` directory names).
type SDK string

const (
	SDKMacOSX           SDK = "macosx"
	SDKiPhoneOS         SDK = "iphoneos"
	SDKiPhoneSimulator  SDK = "iphonesimulator"
	SDKAppleTVOS        SDK = "appletvos"
	SDKAppleTVSimulator SDK = "appletvsimulator"
	SDKWatchOS          SDK = "watchos"
	SDKWatchSimulator   SDK = "watchsimulator"
	SDKXROS             SDK = "xros"
	SDKXRSimulator      SDK = "xrsimulator"
	SDKDriverKit        SDK = "driverkit"
	SDKBridgeOS         SDK = "bridgeos"
)

// tripleAlpha is the OS component clang prints in a triple, which differs
// from the Xcode SDK id for a few platforms (e.g. "ios" vs "iphoneos").
func (s SDK) tripleAlpha() string {
	switch s {
	case SDKMacOSX:
		return "macosx"
	case SDKiPhoneOS, SDKiPhoneSimulator:
		return "ios"
	case SDKAppleTVOS, SDKAppleTVSimulator:
		return "tvos"
	case SDKWatchOS, SDKWatchSimulator:
		return "watchos"
	case SDKXROS, SDKXRSimulator:
		return "xros"
	case SDKDriverKit:
		return "driverkit"
	case SDKBridgeOS:
		return "bridgeos"
	}
	return string(s)
}

// baseSDK collapses a *Simulator SDK constant to its device counterpart —
// Environment carries the "-simulator"-ness, not the SDK field, once parsed.
func (s SDK) baseSDK() SDK {
	switch s {
	case SDKiPhoneSimulator:
		return SDKiPhoneOS
	case SDKAppleTVSimulator:
		return SDKAppleTVOS
	case SDKWatchSimulator:
		return SDKWatchOS
	case SDKXRSimulator:
		return SDKXROS
	}
	return s
}

// Environment is the triple's optional fourth component.
type Environment uint8

const (
	EnvNone Environment = iota
	EnvSimulator
	EnvMacCatalyst
)

func (e Environment) suffix() string {
	switch e {
	case EnvSimulator:
		return "-simulator"
	case EnvMacCatalyst:
		return "-macabi"
	}
	return ""
}

// Version is a dotted X.Y[.Z] version, e.g. an LC_BUILD_VERSION minos/sdk field.
type Version struct {
	Major, Minor, Patch uint8
}

func (v Version) String() string {
	if v.Patch == 0 {
		return fmt.Sprintf("%d.%d", v.Major, v.Minor)
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Encode packs the version the way LC_BUILD_VERSION/LC_VERSION_MIN_* store it:
// (major<<16)|(minor<<8)|patch.
func (v Version) Encode() uint32 {
	return uint32(v.Major)<<16 | uint32(v.Minor)<<8 | uint32(v.Patch)
}

func ParseVersion(s string) (Version, error) {
	parts := strings.SplitN(s, ".", 3)
	var v Version
	if len(parts) == 0 || parts[0] == "" {
		return v, fmt.Errorf("empty version")
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("bad version %q: %w", s, err)
		}
		nums[i] = n
	}
	v.Major = uint8(nums[0])
	if len(nums) > 1 {
		v.Minor = uint8(nums[1])
	}
	if len(nums) > 2 {
		v.Patch = uint8(nums[2])
	}
	return v, nil
}

// Target is the (arch, platform, versions, environment) tuple that
// identifies a Mach-O link destination — the same shape as a clang triple.
type Target struct {
	Arch        Arch
	SDK         SDK
	MinVersion  Version
	SDKVersion  Version // defaults to MinVersion if unset
	Environment Environment
}

var sdkAlphaAliases = map[string]SDK{
	"macosx":           SDKMacOSX,
	"ios":              SDKiPhoneOS,
	"iphoneos":         SDKiPhoneOS,
	"iphonesimulator":  SDKiPhoneSimulator,
	"tvos":             SDKAppleTVOS,
	"appletvos":        SDKAppleTVOS,
	"appletvsimulator": SDKAppleTVSimulator,
	"watchos":          SDKWatchOS,
	"watchsimulator":   SDKWatchSimulator,
	"xros":             SDKXROS,
	"visionos":         SDKXROS,
	"xrsimulator":      SDKXRSimulator,
	"visionossimulator": SDKXRSimulator,
	"driverkit":        SDKDriverKit,
	"bridgeos":         SDKBridgeOS,
}

var archAliases = map[string]Arch{
	"x86_64":   ArchX86_64,
	"arm64":    ArchARM64,
	"arm64e":   ArchARM64E,
	"arm64_32": ArchARM64_32,
}

var sdkVerRe = regexp.MustCompile(`^([a-zA-Z]+)([0-9][0-9.]*)?$`)

// ParseTarget parses a clang-style triple: "<arch>-apple-<sdk><ver>[-<env>]".
func ParseTarget(s string) (Target, error) {
	var t Target
	fields := strings.Split(s, "-")
	if len(fields) < 3 || fields[1] != "apple" {
		return t, fmt.Errorf("macho: invalid triple %q: expected <arch>-apple-<sdk><ver>[-<env>]", s)
	}

	arch, ok := archAliases[fields[0]]
	if !ok {
		return t, fmt.Errorf("macho: unknown arch %q in triple %q", fields[0], s)
	}
	t.Arch = arch

	m := sdkVerRe.FindStringSubmatch(fields[2])
	if m == nil {
		return t, fmt.Errorf("macho: cannot parse sdk/version %q in triple %q", fields[2], s)
	}
	sdk, ok := sdkAlphaAliases[strings.ToLower(m[1])]
	if !ok {
		return t, fmt.Errorf("macho: unknown sdk %q in triple %q", m[1], s)
	}
	if m[2] != "" {
		v, err := ParseVersion(m[2])
		if err != nil {
			return t, fmt.Errorf("macho: %w", err)
		}
		t.MinVersion = v
	}
	t.SDKVersion = t.MinVersion

	// Environment can come from an explicit 4th field, or be implied by a
	// "*simulator" SDK alias — either spelling normalizes to the same Target.
	if sdk == SDKiPhoneSimulator || sdk == SDKAppleTVSimulator ||
		sdk == SDKWatchSimulator || sdk == SDKXRSimulator {
		t.Environment = EnvSimulator
	}
	t.SDK = sdk.baseSDK()

	if len(fields) >= 4 {
		switch fields[3] {
		case "simulator":
			t.Environment = EnvSimulator
		case "macabi":
			t.Environment = EnvMacCatalyst
		default:
			return t, fmt.Errorf("macho: unknown environment %q in triple %q", fields[3], s)
		}
	}

	if err := t.Valid(); err != nil {
		return t, err
	}
	return t, nil
}

// String round-trips ParseTarget, in canonical clang-triple form.
func (t Target) String() string {
	s := fmt.Sprintf("%s-apple-%s%s", t.Arch, t.SDK.tripleAlpha(), t.MinVersion)
	return s + t.Environment.suffix()
}

// validCombo rows mirror the README's arch × platform table exactly.
type comboKey struct {
	Arch Arch
	SDK  SDK
	Env  Environment
}

var validCombos = map[comboKey]bool{
	// x86_64
	{ArchX86_64, SDKMacOSX, EnvNone}: true,
	{ArchX86_64, SDKMacOSX, EnvMacCatalyst}: true,
	{ArchX86_64, SDKiPhoneOS, EnvSimulator}: true,
	{ArchX86_64, SDKAppleTVOS, EnvNone}: true,
	{ArchX86_64, SDKWatchOS, EnvSimulator}: true,
	{ArchX86_64, SDKDriverKit, EnvNone}: true,

	// arm64
	{ArchARM64, SDKMacOSX, EnvNone}: true,
	{ArchARM64, SDKMacOSX, EnvMacCatalyst}: true,
	{ArchARM64, SDKiPhoneOS, EnvNone}: true,
	{ArchARM64, SDKiPhoneOS, EnvSimulator}: true,
	{ArchARM64, SDKAppleTVOS, EnvNone}: true,
	{ArchARM64, SDKWatchOS, EnvSimulator}: true,
	{ArchARM64, SDKDriverKit, EnvNone}: true,
	{ArchARM64, SDKXROS, EnvNone}: true,
	{ArchARM64, SDKXROS, EnvSimulator}: true,

	// arm64e
	{ArchARM64E, SDKMacOSX, EnvNone}: true,
	{ArchARM64E, SDKMacOSX, EnvMacCatalyst}: true,
	{ArchARM64E, SDKiPhoneOS, EnvNone}: true,
	{ArchARM64E, SDKAppleTVOS, EnvNone}: true,
	{ArchARM64E, SDKWatchOS, EnvNone}: true,
	{ArchARM64E, SDKBridgeOS, EnvNone}: true,
	{ArchARM64E, SDKXROS, EnvNone}: true,

	// arm64_32
	{ArchARM64_32, SDKWatchOS, EnvNone}: true,
}

// Valid reports whether t is a real Apple-shipped (arch, platform) combination.
// It does not say whether a codegen backend is registered — see Linker.Supported().
func (t Target) Valid() error {
	if validCombos[comboKey{t.Arch, t.SDK, t.Environment}] {
		return nil
	}
	if t.SDK == SDKBridgeOS && t.Arch != ArchARM64E {
		return fmt.Errorf("macho: bridgeos has no public SDK/toolchain for %s", t.Arch)
	}
	return fmt.Errorf("macho: %s is not a valid Apple-shipped target", t)
}