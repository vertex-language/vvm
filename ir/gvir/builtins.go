// builtins.go
package gvir

// Execution builtins (§9). They take no operands, and their result width
// depends on whether a dimension suffix is present:
//
//	dimension-suffixed  -> i32
//	unsuffixed          -> i64 for positional and extent forms,
//	                       i32 for the subgroup and dynamic_group forms
//
// The unsuffixed forms are not merely "component .x": they are the
// normative linearizations of §9, computed in i64 —
//
//	positional: px + py*ex + pz*ex*ey
//	extent:     ex*ey*ez

// BuiltinClass distinguishes a position from an extent (§9.1).
type BuiltinClass uint8

const (
	BuiltinPositional BuiltinClass = iota
	BuiltinExtent
)

type BuiltinDef struct {
	Op    Opcode
	Class BuiltinClass
	// AllowsDim is false for the builtins that reject every dimension
	// suffix (§9.1 final paragraph).
	AllowsDim bool
	// Unsuffixed is the result type with no dimension suffix.
	Unsuffixed Type
}

var builtinTable = []BuiltinDef{
	{OpThreadInGrid, BuiltinPositional, true, I64},
	{OpThreadInGroup, BuiltinPositional, true, I64},
	{OpGroupInGrid, BuiltinPositional, true, I64},
	{OpThreadInSubgroup, BuiltinPositional, false, I32},
	{OpSubgroupInGroup, BuiltinPositional, false, I32},

	{OpThreadsPerGroup, BuiltinExtent, true, I64},
	{OpGroupsPerGrid, BuiltinExtent, true, I64},
	{OpThreadsPerGrid, BuiltinExtent, true, I64},
	{OpThreadsPerSubgroup, BuiltinExtent, false, I32},
	{OpSubgroupsPerGroup, BuiltinExtent, false, I32},
	{OpDynamicGroupSize, BuiltinExtent, false, I32},
}

var builtinByOp map[Opcode]BuiltinDef

func init() {
	builtinByOp = make(map[Opcode]BuiltinDef, len(builtinTable))
	for _, d := range builtinTable {
		builtinByOp[d.Op] = d
	}
	// Every flagBuiltin opcode must have a row here, and nothing else may.
	for i := 1; i < int(opcodeCount); i++ {
		op := Opcode(i)
		_, listed := builtinByOp[op]
		if op.IsBuiltin() != listed {
			panic("gvir: builtinTable disagrees with opTable's flagBuiltin for " + op.String())
		}
	}
}

// Builtin returns the §9 definition for op.
func Builtin(op Opcode) (BuiltinDef, bool) {
	d, ok := builtinByOp[op]
	return d, ok
}

// BuiltinAllowsDim reports whether op accepts a .x/.y/.z suffix.
func BuiltinAllowsDim(op Opcode) bool {
	d, ok := builtinByOp[op]
	return ok && d.AllowsDim
}

// BuiltinResultType gives the result type of a builtin read with dim
// (DimNone for the unsuffixed form). ok is false for a non-builtin opcode
// or a suffix the builtin rejects.
func BuiltinResultType(op Opcode, dim Dim) (Type, bool) {
	d, ok := builtinByOp[op]
	if !ok {
		return nil, false
	}
	if dim == DimNone {
		return d.Unsuffixed, true
	}
	if !d.AllowsDim {
		return nil, false
	}
	return I32, true
}

// SubgroupWidthIsConstant is a standing reminder that it never is:
// threads_per_subgroup is a runtime value on every backend (§9.2), so no
// pass may constant-fold it. Kept as a named function so the rule is
// greppable rather than folklore.
func SubgroupWidthIsConstant() bool { return false }