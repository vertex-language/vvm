package ptx

// SReg is a predefined special register. Each one carries its type, so the
// IR knows that %clock64 is 64-bit and %tid.x is not.
type SReg uint8

const (
	NoSReg SReg = iota

	TidX
	TidY
	TidZ
	NTidX
	NTidY
	NTidZ

	CtaIdX
	CtaIdY
	CtaIdZ
	NCtaIdX
	NCtaIdY
	NCtaIdZ

	LaneID
	WarpID
	NWarpID
	SmID
	NSmID
	GridID

	IsExplicitCluster
	ClusterIdX
	ClusterIdY
	ClusterIdZ
	NClusterIdX
	NClusterIdY
	NClusterIdZ
	ClusterCtaIdX
	ClusterCtaIdY
	ClusterCtaIdZ
	ClusterNCtaIdX
	ClusterNCtaIdY
	ClusterNCtaIdZ
	ClusterCtaRank
	ClusterNCtaRank

	LaneMaskEq
	LaneMaskLe
	LaneMaskLt
	LaneMaskGe
	LaneMaskGt

	Clock
	ClockHi
	Clock64

	GlobalTimer
	GlobalTimerLo
	GlobalTimerHi

	TotalSmemSize
	AggrSmemSize
	DynamicSmemSize

	ReservedSmemOffsetBegin
	ReservedSmemOffsetEnd
	ReservedSmemOffsetCap

	CurrentGraphExec

	// WarpSz is the WARP_SZ run-time immediate constant. It is not a
	// register and carries no leading percent sign.
	WarpSz
)

type sregInfo struct {
	name   string
	typ    Type
	minISA ISAVersion
	minSM  int
}

var sregTable = map[SReg]sregInfo{
	TidX:  {"%tid.x", U32, ISAVersion{}, 0},
	TidY:  {"%tid.y", U32, ISAVersion{}, 0},
	TidZ:  {"%tid.z", U32, ISAVersion{}, 0},
	NTidX: {"%ntid.x", U32, ISAVersion{}, 0},
	NTidY: {"%ntid.y", U32, ISAVersion{}, 0},
	NTidZ: {"%ntid.z", U32, ISAVersion{}, 0},

	CtaIdX:  {"%ctaid.x", U32, ISAVersion{}, 0},
	CtaIdY:  {"%ctaid.y", U32, ISAVersion{}, 0},
	CtaIdZ:  {"%ctaid.z", U32, ISAVersion{}, 0},
	NCtaIdX: {"%nctaid.x", U32, ISAVersion{}, 0},
	NCtaIdY: {"%nctaid.y", U32, ISAVersion{}, 0},
	NCtaIdZ: {"%nctaid.z", U32, ISAVersion{}, 0},

	LaneID:  {"%laneid", U32, ISAVersion{}, 0},
	WarpID:  {"%warpid", U32, ISAVersion{}, 0},
	NWarpID: {"%nwarpid", U32, ISAVersion{}, 0},
	SmID:    {"%smid", U32, ISAVersion{}, 0},
	NSmID:   {"%nsmid", U32, ISAVersion{}, 0},
	GridID:  {"%gridid", U64, ISAVersion{}, 30},

	IsExplicitCluster: {"%is_explicit_cluster", Pred, ISA78, 90},
	ClusterIdX:        {"%clusterid.x", U32, ISA78, 90},
	ClusterIdY:        {"%clusterid.y", U32, ISA78, 90},
	ClusterIdZ:        {"%clusterid.z", U32, ISA78, 90},
	NClusterIdX:       {"%nclusterid.x", U32, ISA78, 90},
	NClusterIdY:       {"%nclusterid.y", U32, ISA78, 90},
	NClusterIdZ:       {"%nclusterid.z", U32, ISA78, 90},
	ClusterCtaIdX:     {"%cluster_ctaid.x", U32, ISA78, 90},
	ClusterCtaIdY:     {"%cluster_ctaid.y", U32, ISA78, 90},
	ClusterCtaIdZ:     {"%cluster_ctaid.z", U32, ISA78, 90},
	ClusterNCtaIdX:    {"%cluster_nctaid.x", U32, ISA78, 90},
	ClusterNCtaIdY:    {"%cluster_nctaid.y", U32, ISA78, 90},
	ClusterNCtaIdZ:    {"%cluster_nctaid.z", U32, ISA78, 90},
	ClusterCtaRank:    {"%cluster_ctarank", U32, ISA78, 90},
	ClusterNCtaRank:   {"%cluster_nctarank", U32, ISA78, 90},

	LaneMaskEq: {"%lanemask_eq", U32, ISAVersion{}, 20},
	LaneMaskLe: {"%lanemask_le", U32, ISAVersion{}, 20},
	LaneMaskLt: {"%lanemask_lt", U32, ISAVersion{}, 20},
	LaneMaskGe: {"%lanemask_ge", U32, ISAVersion{}, 20},
	LaneMaskGt: {"%lanemask_gt", U32, ISAVersion{}, 20},

	Clock:   {"%clock", U32, ISAVersion{}, 0},
	ClockHi: {"%clock_hi", U32, ISAVersion{}, 20},
	Clock64: {"%clock64", U64, ISAVersion{}, 20},

	GlobalTimer:   {"%globaltimer", U64, ISAVersion{}, 30},
	GlobalTimerLo: {"%globaltimer_lo", U32, ISAVersion{}, 30},
	GlobalTimerHi: {"%globaltimer_hi", U32, ISAVersion{}, 30},

	TotalSmemSize:   {"%total_smem_size", U32, ISA41, 20},
	AggrSmemSize:    {"%aggr_smem_size", U32, ISA81, 90},
	DynamicSmemSize: {"%dynamic_smem_size", U32, ISA41, 20},

	ReservedSmemOffsetBegin: {"%reserved_smem_offset_begin", U32, ISA85, 90},
	ReservedSmemOffsetEnd:   {"%reserved_smem_offset_end", U32, ISA85, 90},
	ReservedSmemOffsetCap:   {"%reserved_smem_offset_cap", U32, ISA85, 90},

	CurrentGraphExec: {"%current_graph_exec", U64, ISA80, 50},

	WarpSz: {"WARP_SZ", U32, ISAVersion{}, 0},
}

// ISA41 and ISA85 are referenced only by the special-register table.
var ISA41 = ISAVersion{4, 1}

func (s SReg) Text() string { return sregTable[s].name }
func (SReg) operand()       {}

// Type returns the register's width as a PTX type.
func (s SReg) Type() Type { return sregTable[s].typ }

// MinISA returns the ISA version that introduced the register.
func (s SReg) MinISA() ISAVersion { return sregTable[s].minISA }

// MinSM returns the sm_* number the register requires, or 0.
func (s SReg) MinSM() int { return sregTable[s].minSM }

// IsValid reports whether s names a predefined identifier.
func (s SReg) IsValid() bool { _, ok := sregTable[s]; return ok }

// EnvReg returns %envreg<n> for n in 0..31.
func EnvReg(n int) SReg { return NoSReg } // see EnvRegOf

// Indexed special-register families are exposed as constructors rather than
// as 32 or 8 separate constants.

// Env returns the %envreg<n> driver-supplied register as an operand.
func Env(n int) Operand { return Sym("%envreg" + itoa(n)) }

// PM returns the %pm<n> performance counter as an operand.
func PM(n int) Operand { return Sym("%pm" + itoa(n)) }

// PM64 returns the 64-bit %pm<n>_64 performance counter as an operand.
func PM64(n int) Operand { return Sym("%pm" + itoa(n) + "_64") }

// ReservedSmemOffset returns %reserved_smem_offset_<n>.
func ReservedSmemOffset(n int) Operand {
	return Sym("%reserved_smem_offset_" + itoa(n))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}