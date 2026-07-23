// mainsig.go
package vir

// MainSignature identifies which of the recognized main()-style shapes
// (§9.4a) an entry-attributed function's signature matches, if any.
// vvm's entrypoint.go uses this to decide whether an `entry` fn can be
// auto-wired to a synthesized crt stub (see crt.BuildArgs.Signature) or
// whether the author is hand-managing the raw ABI themselves.
type MainSignature int

const (
	// MainSignatureNone means the fn's signature doesn't match any
	// recognized shape below — callers should wire the fn's own name
	// unwrapped rather than synthesizing a stub.
	MainSignatureNone MainSignature = iota
	// MainSignatureBare is fn () -> i32.
	MainSignatureBare
	// MainSignatureArgcArgv is fn (argc i32, argv ptr) -> i32.
	MainSignatureArgcArgv
	// MainSignatureArgcArgvEnvp is fn (argc i32, argv ptr, envp ptr) -> i32.
	MainSignatureArgcArgvEnvp
)

func (s MainSignature) String() string {
	switch s {
	case MainSignatureNone:
		return "none"
	case MainSignatureBare:
		return "bare"
	case MainSignatureArgcArgv:
		return "argc_argv"
	case MainSignatureArgcArgvEnvp:
		return "argc_argv_envp"
	}
	return "main_signature?"
}

// RecognizedMainSignature checks f's parameter list and return type
// against the three recognized main() shapes (§9.4a), each of which
// returns i32:
//
//	fn () -> i32
//	fn (argc i32, argv ptr) -> i32
//	fn (argc i32, argv ptr, envp ptr) -> i32
//
// Anything else — wrong return type, wrong arity, wrong parameter types —
// reports MainSignatureNone, which tells the caller to assume the author
// is hand-managing the raw entry ABI themselves rather than opting into
// automatic argc/argv/envp staging.
func RecognizedMainSignature(f *Function) MainSignature {
	if f == nil || !isI32(f.Ret) {
		return MainSignatureNone
	}
	switch len(f.Params) {
	case 0:
		return MainSignatureBare
	case 2:
		if isI32(f.Params[0].Type) && IsPtr(f.Params[1].Type) {
			return MainSignatureArgcArgv
		}
	case 3:
		if isI32(f.Params[0].Type) && IsPtr(f.Params[1].Type) && IsPtr(f.Params[2].Type) {
			return MainSignatureArgcArgvEnvp
		}
	}
	return MainSignatureNone
}

func isI32(t Type) bool {
	it, ok := t.(IntType)
	return ok && it.Bits == 32
}