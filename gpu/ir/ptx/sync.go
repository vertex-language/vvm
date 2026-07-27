package ptx

// BarSync emits bar.sync id; (CTA-wide barrier).
func (cb *CodeBuilder) BarSync(id int) *CodeBuilder {
	return cb.emit("bar.sync", Imm(id))
}

// BarrierSync emits barrier.sync id[, count];
func (cb *CodeBuilder) BarrierSync(id int, count ...int) *CodeBuilder {
	if len(count) > 0 {
		return cb.emit("barrier.sync", Imm(id), Imm(count[0]))
	}
	return cb.emit("barrier.sync", Imm(id))
}

// BarrierClusterArrive / Wait emit cluster barriers (sm_90+).
func (cb *CodeBuilder) BarrierClusterArrive() *CodeBuilder { return cb.emit("barrier.cluster.arrive") }
func (cb *CodeBuilder) BarrierClusterWait() *CodeBuilder   { return cb.emit("barrier.cluster.wait") }

// Membar emits the legacy membar.<level>;
func (cb *CodeBuilder) Membar(level MembarLevel) *CodeBuilder {
	return cb.emit("membar." + string(level))
}

// Fence emits fence.<order>.<scope>;
func (cb *CodeBuilder) Fence(order Order, scope Scope) *CodeBuilder {
	return cb.emit("fence" + string(order) + string(scope))
}

// ShflSync emits shfl.sync.<mode>.b32 d, a, b, c, mask;
func (cb *CodeBuilder) ShflSync(mode ShflMode, d Reg, a, b, c, mask any) *CodeBuilder {
	return cb.emit("shfl.sync."+string(mode)+".b32",
		d, toOperand(a), toOperand(b), toOperand(c), toOperand(mask))
}

// ShflSyncPred is ShflSync with the optional predicate destination:
// shfl.sync.<mode>.b32 d|p, a, b, c, mask;
func (cb *CodeBuilder) ShflSyncPred(mode ShflMode, d, p Reg, a, b, c, mask any) *CodeBuilder {
	return cb.emit("shfl.sync."+string(mode)+".b32",
		Sym(d.Text()+"|"+p.Text()), toOperand(a), toOperand(b), toOperand(c), toOperand(mask))
}

// VoteSync emits vote.sync.<mode>.pred p, q, mask; (or .ballot.b32).
func (cb *CodeBuilder) VoteSync(mode VoteMode, d Reg, a, mask any) *CodeBuilder {
	t := ".pred"
	if mode == VoteBallot {
		t = ".b32"
	}
	return cb.emit("vote.sync."+string(mode)+t, d, toOperand(a), toOperand(mask))
}

// MatchSync emits match.sync.any.b32 d, a, mask; (variant "any" or "all").
func (cb *CodeBuilder) MatchSync(variant string, t Type, d Reg, a, mask any) *CodeBuilder {
	return cb.emit("match.sync."+variant+t.String(), d, toOperand(a), toOperand(mask))
}

// Activemask emits activemask.b32 d;
func (cb *CodeBuilder) Activemask(d Reg) *CodeBuilder { return cb.emit("activemask.b32", d) }

// ReduxSync emits redux.sync.<op>.<t> d, a, mask; (sm_80+).
func (cb *CodeBuilder) ReduxSync(op ReduxOp, t Type, d Reg, a, mask any) *CodeBuilder {
	return cb.emit("redux.sync."+string(op)+t.String(), d, toOperand(a), toOperand(mask))
}

// BarWarpSync emits bar.warp.sync mask;
func (cb *CodeBuilder) BarWarpSync(mask any) *CodeBuilder {
	return cb.emit("bar.warp.sync", toOperand(mask))
}