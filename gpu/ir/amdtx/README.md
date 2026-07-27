# amdtx

Package `amdtx` (**AMD Thread eXecution**) is an in-memory intermediate representation for AMD GPU compute kernels, shaped like `ptx`: virtual registers only, typed generic ops, module/kernel/param structure, and a canonical text form — also called **AMDTX** (`.amdtx`) — AMD's counterpart to `.ptx`. It is a **lowering target for a lowering target**: a higher layer turns high-level kernels into `amdtx` calls, and `amdtx` itself lowers through a second, `ir/machine`-shaped pipeline to reach real bytes, because amdgcn — unlike PTX — has no downstream assembler to hand virtual registers to.

The module preamble mirrors PTX's fixed opening directive block — `.version` / `.target` / `.address_size`, in that order (NVIDIA PTX ISA §11.1) — and adds a `.wave` line for the one axis PTX has no concept of: the AMD wavefront width. Everything above the physical line is virtual and target-shaped-but-target-agnostic, like PTX text; everything below it is physical and amdgcn-specific, like a MIR lowering pass.

Module path: **`github.com/vertex-language/vvm/gpu/ir/amdtx`** (this package moved under `vvm/gpu/ir/`; see [Known gaps](#known-gaps) for two files that haven't caught up).

```
(front end — shaped like ptx: virtual regs, typed ops, text-native)
github.com/vertex-language/vvm/gpu/ir/amdtx                        # AMDTX IR + builder (this package)
github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/text/amdtx    # .amdtx printer + parser — PTX-grammar text, AMD concepts

(back end — shaped like ir/machine: lower → asm → encoding)
github.com/vertex-language/vvm/gpu/ir/amdtx/lower/amdgcn           # EXEC-mask expansion, physical regalloc, branch resolution → asm/amdgcn.Program
github.com/vertex-language/vvm/gpu/ir/amdtx/asm/amdgcn             # structured physical amdgcn assembly: Reg, Operand, Inst, Program (pure data)
github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/binary/amdgcn # shared instruction encoder: bits per format + target-neutral relocations
github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/text/amdgcn   # GAS (.s) debug printer of the physical Program

(the two final object targets)
github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/binary/hsa    # amdhsa ELF container (Linux / ROCr / AQL)
github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/binary/pal    # amdpal ELF container (Windows / PAL / PM4)
```

`amdtx` targets AMD hardware **only** — one `gfx` target per module, no NVIDIA path, no portable-VM abstraction.

---

## Quick start

```go
import (
    "github.com/vertex-language/vvm/gpu/ir/amdtx"
    amdtxtext "github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/text/amdtx"
    lower     "github.com/vertex-language/vvm/gpu/ir/amdtx/lower/amdgcn"
    gastext   "github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/text/amdgcn"
    enc       "github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/binary/amdgcn"
    "github.com/vertex-language/vvm/gpu/ir/amdtx/encoding/binary/hsa"
)

// Module for a specific GPU target. RDNA3 defaults to wave32, address_size 64.
m := amdtx.NewModule("vector_add", amdtx.GFX1100)

// Kernel: C[i] = A[i] + B[i], guarded by i < n.
k := amdtx.NewKernel("vector_add")
k.Args.Add(amdtx.KernelArg{Name: "a", Kind: amdtx.ArgGlobalBuffer, Size: 8, Offset: 0,  AddrSpace: amdtx.Global})
k.Args.Add(amdtx.KernelArg{Name: "b", Kind: amdtx.ArgGlobalBuffer, Size: 8, Offset: 8,  AddrSpace: amdtx.Global})
k.Args.Add(amdtx.KernelArg{Name: "c", Kind: amdtx.ArgGlobalBuffer, Size: 8, Offset: 16, AddrSpace: amdtx.Global})
k.Args.Add(amdtx.KernelArg{Name: "n", Kind: amdtx.ArgByValue,      Size: 4, Offset: 24})
k.KernargSize = 28

// Launch bounds — amdtx's counterpart to ptx .reqntid / .maxntid.
k.ReqdWorkGroupSize = [3]uint32{256, 1, 1}
k.WavesPerEU        = [2]uint32{0, 8}

cb := k.Code
rf := cb.Regs

kbase := cb.KernargPtr()
wgid  := cb.WorkgroupIDX()
tid   := cb.WorkitemIDX()

i := rf.VGPR()
cb.VMadU32U24(i, wgid, 256, tid)

n := rf.SGPR()
cb.SLoadDword(n, kbase, 0x18)
cb.Waitcnt(amdtx.LGKMcnt(0))

cond := cb.VCmpLtU32(i, n)

// Structured and still entirely virtual.
cb.If(cond)
{
    a := rf.SGPRPair(); b := rf.SGPRPair(); c := rf.SGPRPair()
    cb.SLoadDwordx2(a, kbase, 0x00)
    cb.SLoadDwordx2(b, kbase, 0x08)
    cb.SLoadDwordx2(c, kbase, 0x10)
    cb.Waitcnt(amdtx.LGKMcnt(0))

    off := rf.VGPR()
    cb.VLshlrevB32(off, 2, i)

    va := rf.VGPR(); vb := rf.VGPR()
    cb.GlobalLoadDword(va, off, a)
    cb.GlobalLoadDword(vb, off, b)
    cb.Waitcnt(amdtx.VMcnt(0))
    cb.VAddF32(va, va, vb)
    cb.GlobalStoreDword(off, va, c)
}
cb.End()
cb.SEndpgm()

m.Kernels.Add(k)

// Verify, then print .amdtx (debug / golden-file). Print also verifies
// internally, so the standalone amdtx.Verify call here is just an early bail-out.
if err := amdtx.Verify(m); err != nil {
    log.Fatal(err)
}
src, err := amdtxtext.Print(m)

// Lower to physical assembly, print GAS text (debug)
l, err := lower.NewLower(m)
for _, p := range l.Programs {
    fmt.Print(gastext.Print(p))
}

// Encode to bytes, pack into a container
encoded, err := enc.Assemble(l.Programs[0])
obj, err := hsa.NewEncoder().Encode(m, []enc.Encoded{encoded})
```

```amdtx
//
// Generated by vertex
//
.amdtx        1.0
.target       gfx1100
.address_size 64
.wave         32

.visible .kernel vector_add(
    .param .buffer .global .u64 a,
    .param .buffer .global .u64 b,
    .param .buffer .global .u64 c,
    .param .u32 n)
{
    .wave                32;
    .kernarg_size        28;
    .reqd_workgroup_size 256, 1, 1;
    .waves_per_eu        0, 8;

    .reg .vgpr32 %v<3>;
    .reg .sgpr32 %s<2>;
    .reg .sgpr64 %sd<4>;

    v_mad_u32_u24        %v1, %wgid.x, 256, %tid.x;
    s_load_dword         %s1, %kernarg_ptr, 0x18;
    s_waitcnt            lgkmcnt(0);
    v_cmp_lt_u32         %vcc, %v1, %s1;

    if.u32 %vcc {
        s_load_dwordx2       %sd1, %kernarg_ptr, 0x0;
        s_load_dwordx2       %sd2, %kernarg_ptr, 0x8;
        s_load_dwordx2       %sd3, %kernarg_ptr, 0x10;
        s_waitcnt            lgkmcnt(0);
        v_lshlrev_b32        %v2, 2, %v1;
        global_load_dword    %v3, %v2, %sd1;
        global_load_dword    %v4, %v2, %sd2;
        s_waitcnt            vmcnt(0);
        v_add_f32            %v3, %v3, %v4;
        global_store_dword   %v2, %v3, %sd3;
    }
    s_endpgm;
}
```

```asm
	.amdgcn_target "amdgcn-amd-amdhsa--gfx1100"
	.text
	.globl	vector_add
	.p2align	8
	.type	vector_add,@function
vector_add:
	v_mad_u32_u24	v1, s2, 256, v0
	s_load_dword	s3, s[0:1], 0x18
	s_waitcnt	lgkmcnt(0)
	v_cmp_lt_u32	vcc_lo, v1, s3
	s_and_saveexec_b32 s4, vcc_lo         ; if.u32 %vcc  (lowered)
	s_cbranch_execz	.LBB0_end
	s_load_dwordx2	s[6:7],  s[0:1], 0x0
	s_load_dwordx2	s[8:9],  s[0:1], 0x8
	s_load_dwordx2	s[10:11], s[0:1], 0x10
	s_waitcnt	lgkmcnt(0)
	v_lshlrev_b32	v2, 2, v1
	global_load_dword	v3, v2, s[6:7]
	global_load_dword	v4, v2, s[8:9]
	s_waitcnt	vmcnt(0)
	v_add_f32	v3, v3, v4
	global_store_dword	v2, v3, s[10:11]
.LBB0_end:
	s_or_b32	exec_lo, exec_lo, s4      ; End (lowered)
	s_endpgm
```

---

## Concepts

### Module

```go
m := amdtx.NewModule("mymod", amdtx.GFX942)   // CDNA3 / MI300 (wave64)
m.AMDTXVersion = amdtx.AMDTX10                // .amdtx 1.0 — front-end format version
m.AddrSize     = amdtx.Addr64                 // .address_size 64
m.Target                                      // amdtx.GFX942
```

`NewModule` fixes `AMDTXVersion` to `AMDTX10`, `AddrSize` to `Addr64`, and `Wave` to `t.DefaultWave()`. The printed preamble is always the fixed four-line block `.amdtx` / `.target` / `.address_size` / `.wave`, matching PTX's three-directive opening plus the AMD wavefront axis.

| Constant   | gfx     | Family | Default wave | Notes                          |
|------------|---------|--------|--------------|--------------------------------|
| `GFX900`   | gfx900  | gcn5   | 64           | Vega                           |
| `GFX90A`   | gfx90a  | cdna2  | 64           | MI200; AGPRs, MFMA, `rsrc3` accum-offset |
| `GFX942`   | gfx942  | cdna3  | 64           | MI300; AGPRs, MFMA             |
| `GFX1030`  | gfx1030 | rdna2  | 32           | WGP mode                       |
| `GFX1100`  | gfx1100 | rdna3  | 32           | WGP mode, dual-issue VALU      |

Target predicates and derived values, all from the target alone:

```go
t.String()      // "gfx1100"
t.Triple()      // "amdgcn-amd-amdhsa--gfx1100"  (for .amdgcn_target + metadata note)
t.Family()      // amdtx.FamRDNA3;  Family.String() -> "rdna3"
t.IsRDNA()      // WGP mode + wave32-by-default
t.HasAGPRs()    // CDNA accumulation VGPRs
t.HasMFMA()     // CDNA matrix instructions
t.DefaultWave() // amdtx.Wave32 / amdtx.Wave64
t.Mach()        // ELF EF_AMDGPU_MACH
```

### Wave size

```go
k.Wave = amdtx.Wave64           // WaveDefault inherits from module/target
w := k.Wave.Lanes(m.Target)     // concrete lane count: 32 or 64
lg := k.Wave.Log2(m.Target)     // wavefront size as a power of two (descriptor form): 5 or 6
```

The kernel descriptor stores the wavefront size as a power of two (`Wave.Log2` gives 5 for wave32, 6 for wave64); the metadata note and dispatch use the lane count.

### Kernel / Function

```go
k := amdtx.NewKernel("saxpy")   // Visible: true, KernargAlign: 8, Code attached
k.GroupSegmentFixedSize   = 4096
k.PrivateSegmentFixedSize = 0
k.KernargSize             = 32
k.KernargAlign            = 8

// Launch bounds — amdtx's .reqntid / .maxntid analogues, mapped onto AMDHSA
// metadata / attributes. Zero-valued fields are omitted from the printed text.
k.ReqdWorkGroupSize = [3]uint32{256, 1, 1}  // reqd_work_group_size
k.MaxFlatWorkGroup  = 1024                   // amdgpu-flat-work-group-size upper
k.WavesPerEU        = [2]uint32{0, 8}        // amdgpu-waves-per-eu min, max
k.EffectiveWave(m.Target)                    // resolves WaveDefault against target

f := amdtx.NewFunction("helper")
f.Ret    = &amdtx.Param{Type: amdtx.F32}
f.Params = []*amdtx.Param{{Name: "x", Type: amdtx.F32}}
```

Kernels print their ABI + occupancy directives as a column-aligned block at the top of the body (`.wave`, `.group_segment`, `.private_segment`, `.kernarg_size`, `.kernarg_align`, `.reqd_workgroup_size`, `.max_flat_workgroup`, `.waves_per_eu`), in that canonical order, with any unset field skipped. Functions print with a `.func .<ret> name(...)` signature.

### Registers

```go
rf := cb.Regs

s  := rf.SGPR()          // %s1, %s2, ...
v  := rf.VGPR()          // %v1, %v2, ...
sp := rf.SGPRPair()      // %sd1
vp := rf.VGPRPair()      // %vd1
sq := rf.SGPRTuple(4)    // %sq1
a  := rf.AGPR()          // CDNA MFMA only

x := rf.Named(amdtx.VGPR32, "idx")
```

`amdtx.EXEC`, `amdtx.VCC`, `amdtx.SCC`, `amdtx.M0`, `amdtx.FlatScratch`; `cb.ExecMask()`/`cb.VCC()`/`cb.SCC()`; `cb.KernargPtr()`/`cb.WorkgroupIDX/Y/Z()`/`cb.WorkitemIDX/Y/Z()`.

### Types, state spaces, operands

```go
amdtx.Global   // global memory
amdtx.Local    // LDS
amdtx.Private  // scratch
amdtx.Constant
amdtx.Generic  // flat
amdtx.Region   // GDS

amdtx.Pred, amdtx.B32, amdtx.B64, amdtx.U32, amdtx.S32, amdtx.U64, amdtx.S64, amdtx.F16, amdtx.F32, amdtx.F64

amdtx.Inl(7)          // inline immediate
amdtx.Lit(0xdeadbeef) // literal-dword immediate
amdtx.Off(s, 0x18)    // base+offset addressing operand
```

### Kernel arguments

```go
k.Args.Add(amdtx.KernelArg{
    Name: "out", Kind: amdtx.ArgGlobalBuffer,
    Size: 8, Offset: 0, AddrSpace: amdtx.Global, Access: amdtx.AccessReadWrite,
})

// Override the default value type (buffers default to u64, by-value to u32):
k.Args.Add(amdtx.KernelArg{Name: "scale", Kind: amdtx.ArgByValue, Size: 4}.WithType(amdtx.F32))
```

Kinds: `ArgByValue`, `ArgGlobalBuffer`, `ArgDynamicShared`, `ArgSampler`, `ArgImage`, plus hidden ABI args (`ArgHiddenGlobalOffsetX/Y/Z`, `ArgHiddenGridDims`, `ArgHiddenPrintfBuffer`, `ArgHiddenHeapV1`). `KernelArg.resolvedType()` defaults to `U64` for buffer/dynamic-shared args and `U32` otherwise unless `ValueType` was set (via `WithType`). Each arg renders one `.param` line; the printed form varies by kind:

```amdtx
.param .buffer .global .u64 a         // ArgGlobalBuffer
.param .dynshared .local .u64 lds     // ArgDynamicShared
.param .image .read_only img          // ArgImage
.param .sampler samp                  // ArgSampler
.param .f32 scale                     // ArgByValue
```

Hidden args are carried on the kernel but omitted from the printed signature.

### CodeBuilder

```go
cb.SXorB32(sd, sa, sb)
cb.VXorB32(vd, va, vb)
```

```
cb.SAndB32 / SOrB32 / SXorB32 / SNandB32 / SNorB32 / SAndn2B32
cb.VAndB32 / VOrB32  / VXorB32 / VNotB32
cb.SLshlB32 / SLshrB32 / SAshrI32
cb.VLshlrevB32 / VLshrrevB32 / VAshrrevI32
cb.SAddU32 / SSubU32 / SAddcU32 / SMulI32
cb.VAddU32 / VSubU32 / VMulLoU32 / VMadU32U24(d,a,b,c) / VMulHiU32
cb.VAddCoU32 / VAddcCoU32
cb.VAddF32(d,a,b) / VSubF32 / VMulF32 / VFmaF32(d,a,b,c) / VMaxF32 / VMinF32
cb.VRcpF32 / VRsqF32 / VSqrtF32 / VExpF32 / VLogF32
cb.VCvtF32I32(d,a) / VCvtI32F32(d,a)
cb.SMovB32(d, src) / VMovB32(d, src)
cb.VCndmaskB32(d, a, b, cond)
cb.SCselectB32(d, a, b)
```

```go
m1 := cb.VCmpLtU32(a, b)   // returns VCC
cb.VCmpEqU32(a, b) / cb.VCmpGtU32(a, b)
cb.SCmpEqU32(sa, sb)       // returns SCC
cb.SCmpLtU32(sa, sb)
```

```go
loop := cb.NewLabel("loop")
end  := cb.NewLabel("end")
cb.Bind(loop)
    cb.SCbranchSCC1(loop)
    cb.SCbranchExecz(end)
    cb.SBranch(end)
cb.Bind(end)

cond := cb.VCmpLtU32(i, n)
cb.If(cond)                // prints:  if.u32 %vcc {
cb.Else()                  //          } else {
cb.End()                   //          }

lp := cb.Loop()            // prints:  loop {
    lp.BreakIf(done)       //              breakif %vcc;
lp.End()                   //          }
```

```go
cb.SLoadDword(sd, base, 0x18) / cb.SLoadDwordx2 / cb.SLoadDwordx4
cb.GlobalLoadDword(vd, voff, sbase) / cb.GlobalStoreDword(voff, vsrc, sbase)
cb.FlatLoadDword(vd, vaddr) / cb.FlatStoreDword(vaddr, vsrc)
cb.DsReadB32(vd, vaddr) / cb.DsWriteB32(vaddr, vsrc)
cb.GlobalAtomicAddU32(vd, voff, vsrc, sbase)

cb.Waitcnt(amdtx.LGKMcnt(0))
cb.Waitcnt(amdtx.VMcnt(0))
cb.AutoWaitcnt()   // conservative pass: inserts vmcnt(0)/lgkmcnt(0) before first use of a pending load's dest

cb.SBarrier() / cb.SBarrierSignal() / cb.SBarrierWait()
cb.DsBpermuteB32(vd, vidx, vsrc)
cb.VReadlaneB32(sd, vsrc, lane) / cb.VWritelaneB32(vd, sval, lane)
cb.SEndpgm() / cb.SNop(n) / cb.STrap(id)

cb.Raw("v_mfma_f32_16x16x16f16 %aq1, %v1, %v2, %aq1")
cb.RawBytes(0x7e, 0x02, 0x02, 0x7e)
cb.Rawf("s_load_dwordx%d %sd%d, %%kernarg_ptr, 0x%x", n, id, off)

idx := m.Files.Add("kernel.cpp")
cb.Loc(idx, 42, 5)

cb.SAddU32(d, a, b).WithEncoding(amdtx.VOP3)   // pins the physical encoding of the last-emitted inst
```

`Encoding` values: `EncAuto, SOP2, SOP1, SOPK, SOPP, SOPC, SMEM, VOP1, VOP2, VOP3, VOP3P, VOPC, DS, FLAT, GLOBAL, SCRATCH, MUBUF, MTBUF, MIMG`.

### Verify

```go
if err := amdtx.Verify(m); err != nil { ... }
```

Checks, per kernel/function body: balanced `If`/`Loop` nesting, a terminating `s_endpgm`, kernarg offsets within `KernargSize`, AGPR destinations only on CDNA targets, and vector-destination class agreement for `VMadU32U24`/`VAddF32`/`GlobalLoadDword`/`GlobalStoreDword`. It does **not** currently check that branch labels resolve (see [Known gaps](#known-gaps)).

---

## `amdtx/lower/amdgcn`

```go
l, err := amdgcn.NewLower(m)
for _, p := range l.Programs {
    // *asm/amdgcn.Program — physical registers, resolved branches, EXEC sequences expanded
}
```

Does EXEC-mask expansion (`If`/`Loop` → `s_and_saveexec_{b32,b64}`/`s_cbranch_execz`/`s_or_{b32,b64}`, width from resolved `Wave`), physical register allocation (linear, tuple-aligned, VCC/FLAT_SCRATCH/XNACK reserved), and branch resolution (symbolic labels → dword offsets).

## `amdtx/asm/amdgcn`

Pure data: `Reg` (physical: `Kind`/`Num`/`Size`), `Operand` (`IsReg`/`Reg`/`Inline`/`Value`), `Inst` (`Format`, `Opcode`, `Operands`, `Label`/`BranchTo`, `Guard`, `Raw`), `Program` (`Name`, `Target`, `Insts`, register counts, `Wave`, segment sizes, `UserSGPRCount`).

## `amdtx/encoding/binary/amdgcn`

Instruction encoder covering `SOPP`, `SOP1`, `SOP2`, `SMEM`, `VOP1`, `VOP2`, `VOP3`/`VOPC`, `FLAT`/`GLOBAL`, and `DS`. The vector opcode tables (`vop1Op`/`vop2Op`/`vop3Op`) are currently a **GFX11-focused subset**, not a full per-target table. `Assemble(p) (Encoded, error)` packs a `*asm.Program` into `Encoded{Name, Text, Relocations, NumVGPR/SGPR/AGPR, Wave, segment sizes, KernargAlign, UserSGPRCount}`. Relocations are target-neutral `RelocKind` (`RelNone, RelAbs32, RelAbs64, RelAbs32Lo, RelAbs32Hi, RelRel32`); `ELFType(k)` maps them to `R_AMDGPU_*` values.

## `amdtx/encoding/binary/hsa` and `.../pal`

```go
obj, err := hsa.NewEncoder().Encode(m, encodedPrograms)
obj, err := pal.NewEncoder().Encode(m, encodedPrograms)
```

`hsa`: `ELFOSABI_AMDGPU_HSA`, code-object-v5 ABI version, 64-byte kernel descriptor (`group_segment_fixed_size`, `private_segment_fixed_size`, `kernarg_size`, entry offset, `compute_pgm_rsrc1/2/3`, `kernel_code_properties`), `NT_AMDGPU_METADATA` MessagePack note, `Elf64_Rela` relocations. Uses `Target.Triple()` for the metadata note and `Wave.Log2` for the descriptor's power-of-two wavefront field.
`pal`: `ELFOSABI_AMDGPU_PAL`, `amdpal.pipelines` note with `.hardware_stages.cs` (`.entry_point`, `.sgpr_count`, `.vgpr_count`, `.wavefront_size`, `.registers`), `Elf64_Rel` (no addend) relocations. Its RSRC1/RSRC2 packing mirrors `hsa`'s but is a separate copy in this package.

## Text

```go
src, err := amdtxtext.Print(m)        // .amdtx — virtual, structured, PTX-grammar; Print calls Verify internally
src, err := gastext.Print(program)    // .s     — physical, resolved, GAS syntax; output-only
mod, err := amdtxtext.Parse(src)      // .amdtx -> *amdtx.Module — see Known gaps, round-trip is partial today
```

The printer is a pure formatter: the AMD-kind→qualifier and ABI→directive mappings live in the root package (`directives.go`, via `Module.Directives()`, `Kernel.Directives()`, `KernelArg.ArgDirective()`), so the text package never reaches into arg or kernel internals. Print emits the fixed module preamble, column-aligned kernel signatures and launch-directive blocks, mnemonics padded to a fixed operand column, and structured `if`/`else`/`loop`/`breakif` bodies. Kernels and helper functions both print.

---

## Known gaps

Things worth knowing if you're extending this package rather than just consuming it:

- **`Parse` is a skeleton today.** It reconstructs the module header (`.target`/`.wave`) and, per kernel, only the name — it does not parse `.address_size`, the launch directives (`.reqd_workgroup_size`/`.waves_per_eu`/segment sizes), args, registers, or operands, and inside a body it recognizes nothing but a leading `s_endpgm` line. It also re-creates `m` on every `.kernel` line, so only the last kernel in a multi-kernel source survives. Full instruction/operand round-tripping — and reading back the new preamble and directive block the printer now emits — isn't implemented yet.
- **`Verify` doesn't check branch labels.** Its doc comment mentions "resolvable branch labels," but the current implementation only checks control-nesting balance, a terminating `s_endpgm`, kernarg range, AGPR/CDNA gating, and dst-class agreement for a handful of opcodes.
- **Vector encoding is GFX11-shaped.** The opcode tables in `encoding/binary/amdgcn/vector.go` are explicitly a subset tuned for the example/common ops on GFX11; encoding the same virtual ops for other targets may need additional tables.
- **`pal.go` declares unused register constants.** `regComputePgmLo` and `regComputeNumThreadX` are defined but the pipeline note currently only emits `regComputePgmRsrc1`/`regComputePgmRsrc2`.