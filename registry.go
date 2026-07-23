// registry.go
package vvm

// Blank-import every codegen backend this package claims to route to.
// Without these, Linker.Supported() reports false for every target and
// Build/Run fail fast with a clear "no codegen backend registered" error
// instead of silently producing a broken binary.
import (
	_ "github.com/vertex-language/vvm/linker/elf/aarch64"
	_ "github.com/vertex-language/vvm/linker/elf/x86_64"

	_ "github.com/vertex-language/vvm/linker/macho/arm64"
	_ "github.com/vertex-language/vvm/linker/macho/x86_64"

	_ "github.com/vertex-language/vvm/linker/pe/aarch64"
	_ "github.com/vertex-language/vvm/linker/pe/x64"
)