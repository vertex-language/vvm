// File: format/vbyte/binary/function.go
package binary

import (
	"fmt"

	"github.com/vertex-language/vvm/ir/vir"
)

// funcCtx holds a function's local_idx / label_idx spaces (§4). labels[0]
// is always the empty-string placeholder for the (unlabeled) entry block.
type funcCtx struct {
	locals  []string
	localID map[string]int
	labels  []string
	labelID map[string]int
}

func newEncodeFuncCtx(fn *vir.Function) *funcCtx {
	fctx := &funcCtx{localID: map[string]int{}, labelID: map[string]int{}}
	addLocal := func(name string) {
		if _, ok := fctx.localID[name]; !ok {
			fctx.localID[name] = len(fctx.locals)
			fctx.locals = append(fctx.locals, name)
		}
	}
	for _, p := range fn.Params {
		addLocal(p.Name)
	}
	for _, b := range fn.AllBlocks() {
		for _, inst := range b.Lines {
			if inst.Op == vir.OpLoc {
				continue
			}
			if inst.Result != "" {
				addLocal(inst.Result)
			}
		}
	}
	addLabel := func(name string) {
		fctx.labelID[name] = len(fctx.labels)
		fctx.labels = append(fctx.labels, name)
	}
	addLabel("") // index 0: entry placeholder (§4)
	for _, b := range fn.Blocks {
		addLabel(b.Label)
	}
	return fctx
}

// opBinInfo pairs an Opcode with its §8.3 (group, ordinal) binary encoding.
type opBinInfo struct{ group, ordinal byte }

var opGroupOrdinal = map[vir.Opcode]opBinInfo{
	vir.OpAdd: {0x01, 0}, vir.OpSub: {0x01, 1}, vir.OpMul: {0x01, 2}, vir.OpNeg: {0x01, 3},
	vir.OpAbs: {0x01, 4}, vir.OpSqrt: {0x01, 5}, vir.OpUDiv: {0x01, 6}, vir.OpSDiv: {0x01, 7},
	vir.OpURem: {0x01, 8}, vir.OpSRem: {0x01, 9},

	vir.OpUAddO: {0x02, 0}, vir.OpSAddO: {0x02, 1}, vir.OpUSubO: {0x02, 2}, vir.OpSSubO: {0x02, 3},
	vir.OpUMulO: {0x02, 4}, vir.OpSMulO: {0x02, 5}, vir.OpUMulH: {0x02, 6}, vir.OpSMulH: {0x02, 7},
	vir.OpUAddSat: {0x02, 8}, vir.OpSAddSat: {0x02, 9}, vir.OpUSubSat: {0x02, 10}, vir.OpSSubSat: {0x02, 11},

	vir.OpAnd: {0x03, 0}, vir.OpOr: {0x03, 1}, vir.OpXor: {0x03, 2}, vir.OpNot: {0x03, 3},
	vir.OpShl: {0x03, 4}, vir.OpLShr: {0x03, 5}, vir.OpAShr: {0x03, 6}, vir.OpRotl: {0x03, 7},
	vir.OpRotr: {0x03, 8}, vir.OpCtlz: {0x03, 9}, vir.OpCttz: {0x03, 10}, vir.OpPopcnt: {0x03, 11},

	vir.OpMin: {0x04, 0}, vir.OpMax: {0x04, 1}, vir.OpFma: {0x04, 2}, vir.OpCopysign: {0x04, 3},
	vir.OpFloor: {0x04, 4}, vir.OpCeil: {0x04, 5}, vir.OpTruncF: {0x04, 6}, vir.OpNearest: {0x04, 7},
	vir.OpSMin: {0x04, 8}, vir.OpSMax: {0x04, 9}, vir.OpUMin: {0x04, 10}, vir.OpUMax: {0x04, 11},
	vir.OpBSwap: {0x04, 12}, vir.OpBitrev: {0x04, 13},

	vir.OpEq: {0x05, 0}, vir.OpNe: {0x05, 1}, vir.OpSlt: {0x05, 2}, vir.OpSgt: {0x05, 3},
	vir.OpSle: {0x05, 4}, vir.OpSge: {0x05, 5}, vir.OpUlt: {0x05, 6}, vir.OpUgt: {0x05, 7},
	vir.OpUle: {0x05, 8}, vir.OpUge: {0x05, 9}, vir.OpLt: {0x05, 10}, vir.OpGt: {0x05, 11},
	vir.OpLe: {0x05, 12}, vir.OpGe: {0x05, 13},

	vir.OpTrunc: {0x06, 0}, vir.OpSext: {0x06, 1}, vir.OpZext: {0x06, 2}, vir.OpFdemote: {0x06, 3},
	vir.OpFpromote: {0x06, 4}, vir.OpBitcast: {0x06, 5}, vir.OpSfromint: {0x06, 6}, vir.OpUfromint: {0x06, 7},
	vir.OpStoint: {0x06, 8}, vir.OpUtoint: {0x06, 9}, vir.OpStointSat: {0x06, 10}, vir.OpUtointSat: {0x06, 11},

	vir.OpAlloca: {0x07, 0}, vir.OpLoad: {0x07, 1}, vir.OpStore: {0x07, 2}, vir.OpLoadVol: {0x07, 3},
	vir.OpStoreVol: {0x07, 4}, vir.OpMemcopy: {0x07, 5}, vir.OpMemmove: {0x07, 6}, vir.OpMemset: {0x07, 7},
	vir.OpField: {0x07, 8}, vir.OpIndex: {0x07, 9},

	vir.OpAtomicLoad: {0x08, 0}, vir.OpAtomicStore: {0x08, 1}, vir.OpAtomicAdd: {0x08, 2},
	vir.OpAtomicSub: {0x08, 3}, vir.OpAtomicAnd: {0x08, 4}, vir.OpAtomicOr: {0x08, 5},
	vir.OpAtomicXor: {0x08, 6}, vir.OpAtomicXchg: {0x08, 7}, vir.OpCmpxchg: {0x08, 8}, vir.OpFence: {0x08, 9},

	vir.OpCall: {0x09, 0}, vir.OpSyscall: {0x09, 1},

	vir.OpVaStart: {0x0A, 0}, vir.OpVaArg: {0x0A, 1}, vir.OpVaEnd: {0x0A, 2},

	vir.OpSelect: {0x0B, 0},

	vir.OpSplat: {0x0C, 0}, vir.OpExtract: {0x0C, 1}, vir.OpInsert: {0x0C, 2}, vir.OpShuffle: {0x0C, 3},
	vir.OpMaskedLoad: {0x0C, 4}, vir.OpMaskedStore: {0x0C, 5}, vir.OpGather: {0x0C, 6}, vir.OpScatter: {0x0C, 7},

	vir.OpReduceAdd: {0x0D, 0}, vir.OpReduceMin: {0x0D, 1}, vir.OpReduceMax: {0x0D, 2},
	vir.OpReduceAnd: {0x0D, 3}, vir.OpReduceOr: {0x0D, 4}, vir.OpReduceXor: {0x0D, 5},

	vir.OpPrefetch: {0x0E, 0},
}

var opFromGroupOrdinalTable map[[2]byte]vir.Opcode

func init() {
	opFromGroupOrdinalTable = make(map[[2]byte]vir.Opcode, len(opGroupOrdinal))
	for op, info := range opGroupOrdinal {
		opFromGroupOrdinalTable[[2]byte{info.group, info.ordinal}] = op
	}
}

func opFromGroupOrdinal(g, o byte) (vir.Opcode, bool) {
	op, ok := opFromGroupOrdinalTable[[2]byte{g, o}]
	return op, ok
}

func encodeFunction(w *writer, fn *vir.Function, ec *encodeContext) error {
	w.boolean(fn.Export)
	id, err := ec.strings.id(fn.Name)
	if err != nil {
		return err
	}
	w.idx(id)

	w.uleb(uint64(len(fn.Params)))
	for _, p := range fn.Params {
		if err := encodeParam(w, p, ec); err != nil {
			return err
		}
	}
	w.boolean(fn.Variadic)
	if err := encodeType(w, fn.Ret, ec); err != nil {
		return err
	}
	bits, err := encodeAttrBits(fn.Attrs)
	if err != nil {
		return err
	}
	if (bits&(1<<5) != 0 || bits&(1<<6) != 0) && !fn.Export {
		return fmt.Errorf("vbyte: entry/extern_c requires export (§5)")
	}
	w.u8(bits)

	fctx := newEncodeFuncCtx(fn)

	w.uleb(uint64(len(fctx.locals)))
	for _, ln := range fctx.locals {
		lid, err := ec.strings.id(ln)
		if err != nil {
			return err
		}
		w.idx(lid)
	}
	w.uleb(uint64(len(fctx.labels)))
	for _, lb := range fctx.labels {
		lid, err := ec.strings.id(lb)
		if err != nil {
			return err
		}
		w.idx(lid)
	}

	blocks := fn.AllBlocks()
	w.uleb(uint64(len(blocks)))
	for _, b := range blocks {
		if err := encodeBlock(w, b, fctx, ec); err != nil {
			return err
		}
	}
	return nil
}

func encodeBlock(w *writer, b *vir.Block, fctx *funcCtx, ec *encodeContext) error {
	w.uleb(uint64(len(b.Lines)))
	for _, inst := range b.Lines {
		if inst.Op == vir.OpLoc {
			w.u8(1)
			if err := encodeLocPayload(w, inst, ec); err != nil {
				return err
			}
		} else {
			w.u8(0)
			if err := encodeInstPayload(w, inst, fctx, ec); err != nil {
				return err
			}
		}
	}
	return encodeTerminator(w, b.Term, fctx, ec)
}

func encodeLocPayload(w *writer, inst *vir.Instruction, ec *encodeContext) error {
	if len(inst.Args) < 2 {
		return fmt.Errorf("vbyte: malformed loc instruction")
	}
	file := inst.Args[0].Str
	line := inst.Args[1].Int
	fid, err := ec.strings.id(file)
	if err != nil {
		return err
	}
	w.idx(fid)
	w.uleb(uint64(line))
	hasCol := len(inst.Args) > 2
	w.boolean(hasCol)
	if hasCol {
		w.uleb(uint64(inst.Args[2].Int))
	}
	return nil
}

func encodeInstPayload(w *writer, inst *vir.Instruction, fctx *funcCtx, ec *encodeContext) error {
	hasResult := inst.Result != ""
	w.boolean(hasResult)
	if hasResult {
		lid, ok := fctx.localID[inst.Result]
		if !ok {
			return fmt.Errorf("vbyte: instruction result %q not in local_names", inst.Result)
		}
		w.idx(lid)
	}

	info, ok := opGroupOrdinal[inst.Op]
	if !ok {
		return fmt.Errorf("vbyte: opcode %s has no §8.3 group/ordinal encoding", inst.Op)
	}
	w.u8(info.group)
	w.u8(info.ordinal)

	switch {
	case inst.Sig != "":
		w.u8(2)
		if inst.Op == vir.OpVaStart {
			sid, err := ec.strings.id(inst.Sig)
			if err != nil {
				return err
			}
			w.idx(sid)
		} else {
			fsid, ok := ec.fnsigIndex[inst.Sig]
			if !ok {
				return fmt.Errorf("vbyte: instruction references unknown fnsig %q", inst.Sig)
			}
			w.idx(fsid)
		}
	case inst.Suffix != nil:
		w.u8(1)
		if err := encodeType(w, inst.Suffix, ec); err != nil {
			return err
		}
	default:
		w.u8(0)
	}

	w.uleb(uint64(len(inst.Args)))
	for i, a := range inst.Args {
		switch {
		case inst.Op == vir.OpField && i == 1:
			if err := encodeStructNameOperand(w, a, ec); err != nil {
				return err
			}
		case inst.Op == vir.OpField && i == 2:
			structName := inst.Args[1].Ident
			if err := encodeFieldNameOperand(w, a, structName, ec); err != nil {
				return err
			}
		default:
			if err := encodeOperand(w, a, fctx, ec); err != nil {
				return err
			}
		}
	}

	return encodeAlignByte(w, inst.Align)
}

func encodeStructNameOperand(w *writer, a vir.Operand, ec *encodeContext) error {
	w.u8(tagStructName)
	idx, ok := ec.structIndex[a.Ident]
	if !ok {
		return fmt.Errorf("vbyte: field.ptr references unknown struct %q", a.Ident)
	}
	w.idx(idx)
	return nil
}

func encodeFieldNameOperand(w *writer, a vir.Operand, structName string, ec *encodeContext) error {
	w.u8(tagFieldName)
	sIdx, ok := ec.structIndex[structName]
	if !ok {
		return fmt.Errorf("vbyte: field.ptr references unknown struct %q", structName)
	}
	w.idx(sIdx)
	s := ec.module.Structs[sIdx]
	ordinal := -1
	for i, f := range s.Fields {
		if f.Name == a.Ident {
			ordinal = i
			break
		}
	}
	if ordinal < 0 {
		return fmt.Errorf("vbyte: struct %q has no field %q", structName, a.Ident)
	}
	w.uleb(uint64(ordinal))
	return nil
}

func encodeLabel(w *writer, name string, fctx *funcCtx) error {
	idx, ok := fctx.labelID[name]
	if !ok {
		return fmt.Errorf("vbyte: reference to unknown label %q", name)
	}
	w.idx(idx)
	return nil
}

func encodeTerminator(w *writer, t vir.Terminator, fctx *funcCtx, ec *encodeContext) error {
	switch term := t.(type) {
	case vir.Branch:
		w.u8(1)
		return encodeLabel(w, term.Label, fctx)
	case vir.BranchIf:
		w.u8(2)
		if err := encodeOperand(w, term.Cond, fctx, ec); err != nil {
			return err
		}
		if err := encodeLabel(w, term.Then, fctx); err != nil {
			return err
		}
		return encodeLabel(w, term.Else, fctx)
	case vir.Switch:
		w.u8(3)
		if err := encodeOperand(w, term.Value, fctx, ec); err != nil {
			return err
		}
		if err := encodeLabel(w, term.Default, fctx); err != nil {
			return err
		}
		w.uleb(uint64(len(term.Cases)))
		for _, c := range term.Cases {
			w.sleb(c.Value)
			if err := encodeLabel(w, c.Label, fctx); err != nil {
				return err
			}
		}
		return nil
	case vir.Return:
		w.u8(4)
		has := term.Value != nil
		w.boolean(has)
		if has {
			return encodeOperand(w, *term.Value, fctx, ec)
		}
		return nil
	case vir.TailCall:
		if term.Sig != "" {
			w.u8(6)
			fsid, ok := ec.fnsigIndex[term.Sig]
			if !ok {
				return fmt.Errorf("vbyte: tailcall references unknown fnsig %q", term.Sig)
			}
			w.idx(fsid)
			if len(term.Args) == 0 {
				return fmt.Errorf("vbyte: indirect tailcall missing function-pointer operand")
			}
			if err := encodeOperand(w, term.Args[0], fctx, ec); err != nil {
				return err
			}
			rest := term.Args[1:]
			w.uleb(uint64(len(rest)))
			for _, a := range rest {
				if err := encodeOperand(w, a, fctx, ec); err != nil {
					return err
				}
			}
			return nil
		}
		w.u8(5)
		if idx, ok := ec.fnIndex[term.Callee]; ok {
			w.u8(0)
			w.idx(idx)
		} else if idx, ok := ec.externFnIndex[term.Callee]; ok {
			w.u8(1)
			w.idx(idx)
		} else {
			return fmt.Errorf("vbyte: tailcall to unknown callee %q", term.Callee)
		}
		w.uleb(uint64(len(term.Args)))
		for _, a := range term.Args {
			if err := encodeOperand(w, a, fctx, ec); err != nil {
				return err
			}
		}
		return nil
	case vir.Trap:
		w.u8(7)
		return nil
	case vir.Unreachable:
		w.u8(8)
		return nil
	default:
		return fmt.Errorf("vbyte: unknown terminator implementation %T", t)
	}
}

// --- decode ---

func decodeFunctions(r *reader, m *vir.Module, dc *decodeContext) error {
	n, err := r.uleb()
	if err != nil {
		return err
	}
	for i := uint64(0); i < n; i++ {
		fn, err := decodeFunction(r, dc)
		if err != nil {
			return err
		}
		m.Functions = append(m.Functions, fn)
		dc.fns = append(dc.fns, fn)
	}
	return nil
}

func decodeFunction(r *reader, dc *decodeContext) (*vir.Function, error) {
	export, err := r.boolean()
	if err != nil {
		return nil, err
	}
	nameID, err := r.idx()
	if err != nil {
		return nil, err
	}
	name, err := stringAt(dc.strings, nameID)
	if err != nil {
		return nil, err
	}
	pn, err := r.uleb()
	if err != nil {
		return nil, err
	}
	params := make([]vir.Param, 0, pn)
	for i := uint64(0); i < pn; i++ {
		p, err := decodeParam(r, dc)
		if err != nil {
			return nil, err
		}
		params = append(params, p)
	}
	variadic, err := r.boolean()
	if err != nil {
		return nil, err
	}
	ret, err := decodeType(r, dc)
	if err != nil {
		return nil, err
	}
	bits, err := r.u8()
	if err != nil {
		return nil, err
	}
	attrs, err := decodeAttrBits(bits)
	if err != nil {
		return nil, err
	}
	if (bits&(1<<5) != 0 || bits&(1<<6) != 0) && !export {
		return nil, fmt.Errorf("vbyte: entry/extern_c set without export (§5)")
	}

	ln, err := r.uleb()
	if err != nil {
		return nil, err
	}
	locals := make([]string, 0, ln)
	for i := uint64(0); i < ln; i++ {
		lid, err := r.idx()
		if err != nil {
			return nil, err
		}
		s, err := stringAt(dc.strings, lid)
		if err != nil {
			return nil, err
		}
		locals = append(locals, s)
	}

	lbln, err := r.uleb()
	if err != nil {
		return nil, err
	}
	if lbln == 0 {
		return nil, fmt.Errorf("vbyte: label_names must contain at least the entry placeholder (§4)")
	}
	labels := make([]string, 0, lbln)
	for i := uint64(0); i < lbln; i++ {
		lid, err := r.idx()
		if err != nil {
			return nil, err
		}
		s, err := stringAt(dc.strings, lid)
		if err != nil {
			return nil, err
		}
		labels = append(labels, s)
	}

	bn, err := r.uleb()
	if err != nil {
		return nil, err
	}
	if bn != lbln {
		return nil, fmt.Errorf("vbyte: vec<block> count (%d) must equal label_names count (%d) (§8)", bn, lbln)
	}

	fctx := &funcCtx{locals: locals, labels: labels}
	blocks := make([]*vir.Block, 0, bn)
	for i := uint64(0); i < bn; i++ {
		b, err := decodeBlock(r, dc, fctx, int(i))
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("vbyte: a function must have at least one block (the entry) (§8)")
	}

	fn := &vir.Function{
		Name: name, Params: params, Variadic: variadic, Ret: ret,
		Attrs: attrs, Export: export, Entry: blocks[0],
	}
	fn.Entry.Label = ""
	if len(blocks) > 1 {
		fn.Blocks = blocks[1:]
	}
	return fn, nil
}

func decodeBlock(r *reader, dc *decodeContext, fctx *funcCtx, blockIndex int) (*vir.Block, error) {
	ln, err := r.uleb()
	if err != nil {
		return nil, err
	}
	lines := make([]*vir.Instruction, 0, ln)
	for i := uint64(0); i < ln; i++ {
		inst, err := decodeBodyLine(r, dc, fctx)
		if err != nil {
			return nil, err
		}
		lines = append(lines, inst)
	}
	term, err := decodeTerminator(r, dc, fctx)
	if err != nil {
		return nil, err
	}
	label := ""
	if blockIndex > 0 && blockIndex < len(fctx.labels) {
		label = fctx.labels[blockIndex]
	}
	return &vir.Block{Label: label, Lines: lines, Term: term}, nil
}

func decodeBodyLine(r *reader, dc *decodeContext, fctx *funcCtx) (*vir.Instruction, error) {
	kind, err := r.u8()
	if err != nil {
		return nil, err
	}
	switch kind {
	case 1:
		fid, err := r.idx()
		if err != nil {
			return nil, err
		}
		file, err := stringAt(dc.strings, fid)
		if err != nil {
			return nil, err
		}
		line, err := r.uleb()
		if err != nil {
			return nil, err
		}
		hasCol, err := r.boolean()
		if err != nil {
			return nil, err
		}
		args := []vir.Operand{vir.StringLiteral(file), vir.IntLiteral(int64(line))}
		if hasCol {
			col, err := r.uleb()
			if err != nil {
				return nil, err
			}
			args = append(args, vir.IntLiteral(int64(col)))
		}
		return &vir.Instruction{Op: vir.OpLoc, Args: args}, nil
	case 0:
		return decodeInstPayload(r, dc, fctx)
	default:
		return nil, fmt.Errorf("vbyte: invalid body_line kind byte 0x%02x (§8.1)", kind)
	}
}

func decodeInstPayload(r *reader, dc *decodeContext, fctx *funcCtx) (*vir.Instruction, error) {
	hasResult, err := r.boolean()
	if err != nil {
		return nil, err
	}
	result := ""
	if hasResult {
		lid, err := r.idx()
		if err != nil {
			return nil, err
		}
		if lid < 0 || lid >= len(fctx.locals) {
			return nil, fmt.Errorf("vbyte: local index %d out of range (§1 strict rejection)", lid)
		}
		result = fctx.locals[lid]
	}

	group, err := r.u8()
	if err != nil {
		return nil, err
	}
	ordinal, err := r.u8()
	if err != nil {
		return nil, err
	}
	op, ok := opFromGroupOrdinal(group, ordinal)
	if !ok {
		return nil, fmt.Errorf("vbyte: unknown opcode group=0x%02x ordinal=0x%02x (§1 strict rejection)", group, ordinal)
	}

	sk, err := r.u8()
	if err != nil {
		return nil, err
	}
	var suffix vir.Type
	var sig string
	switch sk {
	case 0:
	case 1:
		suffix, err = decodeType(r, dc)
		if err != nil {
			return nil, err
		}
	case 2:
		if op == vir.OpVaStart {
			sid, err := r.idx()
			if err != nil {
				return nil, err
			}
			sig, err = stringAt(dc.strings, sid)
			if err != nil {
				return nil, err
			}
		} else {
			fsid, err := r.idx()
			if err != nil {
				return nil, err
			}
			if fsid < 0 || fsid >= len(dc.fnsigs) {
				return nil, fmt.Errorf("vbyte: fnsig index %d out of range", fsid)
			}
			sig = dc.fnsigs[fsid].Name
		}
	default:
		return nil, fmt.Errorf("vbyte: invalid suffix kind byte 0x%02x (§8.2)", sk)
	}

	an, err := r.uleb()
	if err != nil {
		return nil, err
	}
	args := make([]vir.Operand, 0, an)
	for i := uint64(0); i < an; i++ {
		var a vir.Operand
		switch {
		case op == vir.OpField && i == 1:
			a, err = decodeStructNameOperand(r, dc)
		case op == vir.OpField && i == 2:
			structName := ""
			if len(args) > 1 {
				structName = args[1].Ident
			}
			a, err = decodeFieldNameOperand(r, dc, structName)
		default:
			a, err = decodeOperand(r, dc, fctx)
		}
		if err != nil {
			return nil, err
		}
		args = append(args, a)
	}

	align, err := decodeAlignByte(r)
	if err != nil {
		return nil, err
	}

	return &vir.Instruction{Result: result, Op: op, Suffix: suffix, Sig: sig, Args: args, Align: align}, nil
}

func decodeStructNameOperand(r *reader, dc *decodeContext) (vir.Operand, error) {
	tag, err := r.u8()
	if err != nil {
		return vir.Operand{}, err
	}
	if tag != tagStructName {
		return vir.Operand{}, fmt.Errorf("vbyte: expected Struct Name operand tag (§8.4)")
	}
	idx, err := r.idx()
	if err != nil {
		return vir.Operand{}, err
	}
	if idx < 0 || idx >= len(dc.structs) {
		return vir.Operand{}, fmt.Errorf("vbyte: struct index %d out of range", idx)
	}
	return vir.Ident(dc.structs[idx].Name), nil
}

func decodeFieldNameOperand(r *reader, dc *decodeContext, structName string) (vir.Operand, error) {
	tag, err := r.u8()
	if err != nil {
		return vir.Operand{}, err
	}
	if tag != tagFieldName {
		return vir.Operand{}, fmt.Errorf("vbyte: expected Field Name operand tag (§8.4)")
	}
	sIdx, err := r.idx()
	if err != nil {
		return vir.Operand{}, err
	}
	if sIdx < 0 || sIdx >= len(dc.structs) {
		return vir.Operand{}, fmt.Errorf("vbyte: struct index %d out of range", sIdx)
	}
	ord, err := r.uleb()
	if err != nil {
		return vir.Operand{}, err
	}
	s := dc.structs[sIdx]
	if int(ord) >= len(s.Fields) {
		return vir.Operand{}, fmt.Errorf("vbyte: field ordinal %d out of range for struct %q (§8.4)", ord, s.Name)
	}
	_ = structName // informational only; struct identity comes from sIdx
	return vir.Ident(s.Fields[ord].Name), nil
}

func decodeLabel(r *reader, fctx *funcCtx) (string, error) {
	idx, err := r.idx()
	if err != nil {
		return "", err
	}
	if idx < 0 || idx >= len(fctx.labels) {
		return "", fmt.Errorf("vbyte: label index %d out of range (§1 strict rejection)", idx)
	}
	if idx == 0 {
		return "", fmt.Errorf("vbyte: a terminator targeting label 0 (entry) is a decode error (§4)")
	}
	return fctx.labels[idx], nil
}

func decodeTerminator(r *reader, dc *decodeContext, fctx *funcCtx) (vir.Terminator, error) {
	tag, err := r.u8()
	if err != nil {
		return nil, err
	}
	switch tag {
	case 1:
		lbl, err := decodeLabel(r, fctx)
		if err != nil {
			return nil, err
		}
		return vir.Branch{Label: lbl}, nil
	case 2:
		cond, err := decodeOperand(r, dc, fctx)
		if err != nil {
			return nil, err
		}
		then, err := decodeLabel(r, fctx)
		if err != nil {
			return nil, err
		}
		els, err := decodeLabel(r, fctx)
		if err != nil {
			return nil, err
		}
		return vir.BranchIf{Cond: cond, Then: then, Else: els}, nil
	case 3:
		val, err := decodeOperand(r, dc, fctx)
		if err != nil {
			return nil, err
		}
		def, err := decodeLabel(r, fctx)
		if err != nil {
			return nil, err
		}
		n, err := r.uleb()
		if err != nil {
			return nil, err
		}
		cases := make([]vir.SwitchCase, 0, n)
		seen := make(map[int64]bool, n)
		for i := uint64(0); i < n; i++ {
			cv, err := r.sleb()
			if err != nil {
				return nil, err
			}
			if seen[cv] {
				return nil, fmt.Errorf("vbyte: duplicate switch case value %d (§8.5)", cv)
			}
			seen[cv] = true
			lb, err := decodeLabel(r, fctx)
			if err != nil {
				return nil, err
			}
			cases = append(cases, vir.SwitchCase{Value: cv, Label: lb})
		}
		return vir.Switch{Value: val, Default: def, Cases: cases}, nil
	case 4:
		has, err := r.boolean()
		if err != nil {
			return nil, err
		}
		if !has {
			return vir.Return{}, nil
		}
		v, err := decodeOperand(r, dc, fctx)
		if err != nil {
			return nil, err
		}
		return vir.Return{Value: &v}, nil
	case 5:
		kb, err := r.u8()
		if err != nil {
			return nil, err
		}
		idx, err := r.idx()
		if err != nil {
			return nil, err
		}
		var callee string
		switch kb {
		case 0:
			if idx < 0 || idx >= len(dc.fns) {
				return nil, fmt.Errorf("vbyte: fn index %d out of range", idx)
			}
			callee = dc.fns[idx].Name
		case 1:
			if idx < 0 || idx >= len(dc.externFns) {
				return nil, fmt.Errorf("vbyte: extern_fn index %d out of range", idx)
			}
			callee = dc.externFns[idx].Name
		default:
			return nil, fmt.Errorf("vbyte: invalid tailcall kind byte 0x%02x (§8.5)", kb)
		}
		n, err := r.uleb()
		if err != nil {
			return nil, err
		}
		args := make([]vir.Operand, 0, n)
		for i := uint64(0); i < n; i++ {
			a, err := decodeOperand(r, dc, fctx)
			if err != nil {
				return nil, err
			}
			args = append(args, a)
		}
		return vir.TailCall{Callee: callee, Args: args}, nil
	case 6:
		fsid, err := r.idx()
		if err != nil {
			return nil, err
		}
		if fsid < 0 || fsid >= len(dc.fnsigs) {
			return nil, fmt.Errorf("vbyte: fnsig index %d out of range", fsid)
		}
		sig := dc.fnsigs[fsid].Name
		fp, err := decodeOperand(r, dc, fctx)
		if err != nil {
			return nil, err
		}
		n, err := r.uleb()
		if err != nil {
			return nil, err
		}
		rest := make([]vir.Operand, 0, n)
		for i := uint64(0); i < n; i++ {
			a, err := decodeOperand(r, dc, fctx)
			if err != nil {
				return nil, err
			}
			rest = append(rest, a)
		}
		args := append([]vir.Operand{fp}, rest...)
		return vir.TailCall{Sig: sig, Args: args}, nil
	case 7:
		return vir.Trap{}, nil
	case 8:
		return vir.Unreachable{}, nil
	default:
		return nil, fmt.Errorf("vbyte: unknown terminator tag 0x%02x (§1 strict rejection)", tag)
	}
}