// registry.go
package vvm

// Blank-import every codegen backend this package claims to route to, the
// same way a caller assembling the pipeline by hand would have to per
// linker/elf's/macho's/pe's own READMEs ("Adding a new arch" sections).
// Without these, Linker.Supported() reports false for every target and
// Build/Run fail fast with a clear "no codegen backend registered" error
// instead of silently producing a broken binary — same "fail loudly,
// never guess" culture as vir.Verify and objectwriter's adapters.
import (
	_ "github.com/vertex-language/vvm/linker/elf/aarch64"
	_ "github.com/vertex-language/vvm/linker/elf/x86_64"

	_ "github.com/vertex-language/vvm/linker/macho/arm64"
	_ "github.com/vertex-language/vvm/linker/macho/x86_64"

	_ "github.com/vertex-language/vvm/linker/pe/aarch64"
	_ "github.com/vertex-language/vvm/linker/pe/x64"
)