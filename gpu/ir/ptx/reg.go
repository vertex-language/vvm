package ptx

import "strconv"

// Reg is a virtual register handed out by a RegFile. ptxas performs the
// real allocation — this package never does.
type Reg struct {
	class *RegClass
	n     int    // 1-based index within class; 0 for named registers
	name  string // non-empty for Named registers
}

func (r Reg) Text() string {
	if r.name != "" {
		return "%" + r.name
	}
	return "%" + r.class.Prefix + strconv.Itoa(r.n)
}

// RegClass is one register group (one ranged .reg declaration).
type RegClass struct {
	Type   Type
	Prefix string
	Count  int
}

// NamedReg is an explicitly named register (.reg .u32 %idx;).
type NamedReg struct {
	Type Type
	Name string
}

var regPrefixes = map[string]string{
	"pred": "p",
	"s8":   "rc", "s16": "rs", "s32": "r", "s64": "rl",
	"u8": "uc", "u16": "us", "u32": "u", "u64": "d",
	"b8": "bc", "b16": "h", "b32": "b", "b64": "rd", "b128": "q",
	"f16": "hf", "f16x2": "hh", "f32": "f", "f64": "fd",
}

// RegFile is a per-body naming allocator for virtual registers, grouped
// by type. The printer collapses each group into a single ranged .reg
// declaration (.reg .f32 %f<9>;).
type RegFile struct {
	classes []*RegClass
	byName  map[string]*RegClass
	named   []NamedReg
}

func newRegFile() *RegFile { return &RegFile{byName: map[string]*RegClass{}} }

// Of allocates the next register of type t.
func (rf *RegFile) Of(t Type) Reg {
	key := t.Name()
	c := rf.byName[key]
	if c == nil {
		prefix, ok := regPrefixes[key]
		if !ok {
			prefix = "x" + key
		}
		c = &RegClass{Type: t, Prefix: prefix}
		rf.byName[key] = c
		rf.classes = append(rf.classes, c)
	}
	c.Count++
	return Reg{class: c, n: c.Count}
}

// Named declares a register with an explicit name: .reg .u32 %idx;
func (rf *RegFile) Named(t Type, name string) Reg {
	rf.named = append(rf.named, NamedReg{Type: t, Name: name})
	return Reg{name: name}
}

func (rf *RegFile) Pred() Reg { return rf.Of(Pred) }
func (rf *RegFile) S16() Reg  { return rf.Of(S16) }
func (rf *RegFile) S32() Reg  { return rf.Of(S32) }
func (rf *RegFile) S64() Reg  { return rf.Of(S64) }
func (rf *RegFile) U16() Reg  { return rf.Of(U16) }
func (rf *RegFile) U32() Reg  { return rf.Of(U32) }
func (rf *RegFile) U64() Reg  { return rf.Of(U64) }
func (rf *RegFile) B16() Reg  { return rf.Of(B16) }
func (rf *RegFile) B32() Reg  { return rf.Of(B32) }
func (rf *RegFile) B64() Reg  { return rf.Of(B64) }
func (rf *RegFile) B128() Reg { return rf.Of(B128) }
func (rf *RegFile) F16() Reg  { return rf.Of(F16) }
func (rf *RegFile) F32() Reg  { return rf.Of(F32) }
func (rf *RegFile) F64() Reg  { return rf.Of(F64) }

// Classes returns the register groups in declaration order.
func (rf *RegFile) Classes() []*RegClass { return rf.classes }

// NamedRegs returns the explicitly named registers in declaration order.
func (rf *RegFile) NamedRegs() []NamedReg { return rf.named }