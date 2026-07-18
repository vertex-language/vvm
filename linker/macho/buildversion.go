package macho

import "fmt"

// resolvePlatform maps (SDK, Environment) to the LC_BUILD_VERSION.platform
// enum, per the table in README.md.
func resolvePlatform(sdk SDK, env Environment) (uint32, error) {
	switch {
	case sdk == SDKMacOSX && env == EnvMacCatalyst:
		return PLATFORM_MACCATALYST, nil
	case sdk == SDKMacOSX:
		return PLATFORM_MACOS, nil
	case sdk == SDKiPhoneOS && env == EnvSimulator:
		return PLATFORM_IOSSIMULATOR, nil
	case sdk == SDKiPhoneOS:
		return PLATFORM_IOS, nil
	case sdk == SDKAppleTVOS && env == EnvSimulator:
		return PLATFORM_TVOSSIMULATOR, nil
	case sdk == SDKAppleTVOS:
		return PLATFORM_TVOS, nil
	case sdk == SDKWatchOS && env == EnvSimulator:
		return PLATFORM_WATCHOSSIMULATOR, nil
	case sdk == SDKWatchOS:
		return PLATFORM_WATCHOS, nil
	case sdk == SDKBridgeOS:
		return PLATFORM_BRIDGEOS, nil
	case sdk == SDKXROS && env == EnvSimulator:
		return PLATFORM_VISIONOSSIMULATOR, nil
	case sdk == SDKXROS:
		return PLATFORM_VISIONOS, nil
	case sdk == SDKDriverKit:
		return PLATFORM_DRIVERKIT, nil
	}
	return 0, fmt.Errorf("macho: no LC_BUILD_VERSION.platform for sdk=%s env=%d", sdk, env)
}

// buildVersionEntry is one LC_BUILD_VERSION command's payload.
type buildVersionEntry struct {
	platform uint32
	minos    uint32
	sdk      uint32
}

// buildVersionEntries returns one entry normally, or two for a zippered
// macOS+Catalyst slice.
func buildVersionEntries(t Target, zippered bool) ([]buildVersionEntry, error) {
	p, err := resolvePlatform(t.SDK, t.Environment)
	if err != nil {
		return nil, err
	}
	entries := []buildVersionEntry{{platform: p, minos: t.MinVersion.Encode(), sdk: t.SDKVersion.Encode()}}

	if zippered {
		if t.SDK != SDKMacOSX || t.Environment != EnvNone {
			return nil, fmt.Errorf("macho: SetZippered(true) is only valid for macosx device targets (SDK=%s env=%d)", t.SDK, t.Environment)
		}
		entries = append(entries, buildVersionEntry{
			platform: PLATFORM_MACCATALYST,
			minos:    t.MinVersion.Encode(),
			sdk:      t.SDKVersion.Encode(),
		})
	}
	return entries, nil
}