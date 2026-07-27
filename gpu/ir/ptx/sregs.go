package ptx

// Predefined special registers (%sreg values), usable directly as
// instruction operands.
var (
	TidX SpecialReg = "%tid.x"
	TidY SpecialReg = "%tid.y"
	TidZ SpecialReg = "%tid.z"

	NtidX SpecialReg = "%ntid.x"
	NtidY SpecialReg = "%ntid.y"
	NtidZ SpecialReg = "%ntid.z"

	CtaIdX SpecialReg = "%ctaid.x"
	CtaIdY SpecialReg = "%ctaid.y"
	CtaIdZ SpecialReg = "%ctaid.z"

	NCtaIdX SpecialReg = "%nctaid.x"
	NCtaIdY SpecialReg = "%nctaid.y"
	NCtaIdZ SpecialReg = "%nctaid.z"

	LaneId  SpecialReg = "%laneid"
	WarpId  SpecialReg = "%warpid"
	NWarpId SpecialReg = "%nwarpid"
	SmId    SpecialReg = "%smid"
	NSmId   SpecialReg = "%nsmid"
	GridId  SpecialReg = "%gridid"

	LaneMaskEq SpecialReg = "%lanemask_eq"
	LaneMaskLe SpecialReg = "%lanemask_le"
	LaneMaskLt SpecialReg = "%lanemask_lt"
	LaneMaskGe SpecialReg = "%lanemask_ge"
	LaneMaskGt SpecialReg = "%lanemask_gt"

	Clock   SpecialReg = "%clock"
	ClockHi SpecialReg = "%clock_hi"
	Clock64 SpecialReg = "%clock64"

	GlobalTimer   SpecialReg = "%globaltimer"
	GlobalTimerLo SpecialReg = "%globaltimer_lo"
	GlobalTimerHi SpecialReg = "%globaltimer_hi"

	TotalSmemSize   SpecialReg = "%total_smem_size"
	DynamicSmemSize SpecialReg = "%dynamic_smem_size"

	// Cluster special registers (sm_90+).
	IsExplicitCluster SpecialReg = "%is_explicit_cluster"
	ClusterIdX        SpecialReg = "%clusterid.x"
	ClusterIdY        SpecialReg = "%clusterid.y"
	ClusterIdZ        SpecialReg = "%clusterid.z"
	NClusterIdX       SpecialReg = "%nclusterid.x"
	NClusterIdY       SpecialReg = "%nclusterid.y"
	NClusterIdZ       SpecialReg = "%nclusterid.z"
	ClusterCtaIdX     SpecialReg = "%cluster_ctaid.x"
	ClusterCtaIdY     SpecialReg = "%cluster_ctaid.y"
	ClusterCtaIdZ     SpecialReg = "%cluster_ctaid.z"
	ClusterNCtaIdX    SpecialReg = "%cluster_nctaid.x"
	ClusterNCtaIdY    SpecialReg = "%cluster_nctaid.y"
	ClusterNCtaIdZ    SpecialReg = "%cluster_nctaid.z"
	ClusterCtaRank    SpecialReg = "%cluster_ctarank"
	ClusterNCtaRank   SpecialReg = "%cluster_nctarank"

	// WARP_SZ run-time immediate constant.
	WarpSz SpecialReg = "WARP_SZ"
)