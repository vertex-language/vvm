package stablehlo

// Distribution / collective ops. replicaGroups renders as a dense 2-d i64
// attribute; a nil ChannelHandle omits the attribute.

func collectiveAttrs(replicaGroups [][]int64, ch *ChannelHandle, useGlobalIDs bool) []NamedAttr {
	attrs := []NamedAttr{nattr("replica_groups", DenseI64Matrix(replicaGroups))}
	if ch != nil {
		attrs = append(attrs, nattr("channel_handle", *ch))
	}
	if useGlobalIDs {
		attrs = append(attrs, UnitAttr("use_global_device_ids"))
	}
	return attrs
}

// AllReduce emits "stablehlo.all_reduce"; results mirror input types. The
// body receives two scalar block arguments.
func (cb *CodeBuilder) AllReduce(inputs []Value, replicaGroups [][]int64, ch *ChannelHandle,
	useGlobalIDs bool, body func(*RegionBuilder, []Value)) []Value {
	s := scalarOf(inputs[0].Type())
	r := cb.region([]Type{s, s}, body)
	rts := make([]Type, len(inputs))
	for i, in := range inputs {
		rts[i] = in.Type()
	}
	return cb.emitR("stablehlo.all_reduce", inputs,
		collectiveAttrs(replicaGroups, ch, useGlobalIDs), []*Region{r}, rts...)
}

func (cb *CodeBuilder) AllGather(resultTypes []Type, inputs []Value, allGatherDim int64,
	replicaGroups [][]int64, ch *ChannelHandle, useGlobalIDs bool) []Value {
	attrs := append([]NamedAttr{nattr("all_gather_dim", I64Attr(allGatherDim))},
		collectiveAttrs(replicaGroups, ch, useGlobalIDs)...)
	return cb.emit("stablehlo.all_gather", inputs, attrs, resultTypes...)
}

func (cb *CodeBuilder) AllToAll(resultTypes []Type, inputs []Value,
	splitDim, concatDim, splitCount int64, replicaGroups [][]int64, ch *ChannelHandle) []Value {
	attrs := []NamedAttr{
		nattr("split_dimension", I64Attr(splitDim)),
		nattr("concat_dimension", I64Attr(concatDim)),
		nattr("split_count", I64Attr(splitCount)),
		nattr("replica_groups", DenseI64Matrix(replicaGroups)),
	}
	if ch != nil {
		attrs = append(attrs, nattr("channel_handle", *ch))
	}
	return cb.emit("stablehlo.all_to_all", inputs, attrs, resultTypes...)
}

// ReduceScatter emits "stablehlo.reduce_scatter" with a scalar reduction body.
func (cb *CodeBuilder) ReduceScatter(t Type, input Value, scatterDim int64,
	replicaGroups [][]int64, ch *ChannelHandle, useGlobalIDs bool,
	body func(*RegionBuilder, []Value)) Value {
	s := scalarOf(input.Type())
	r := cb.region([]Type{s, s}, body)
	attrs := append([]NamedAttr{nattr("scatter_dimension", I64Attr(scatterDim))},
		collectiveAttrs(replicaGroups, ch, useGlobalIDs)...)
	return cb.emitR("stablehlo.reduce_scatter", []Value{input}, attrs, []*Region{r}, t)[0]
}

func (cb *CodeBuilder) CollectivePermute(input Value, sourceTargetPairs [][]int64, ch *ChannelHandle) Value {
	attrs := []NamedAttr{nattr("source_target_pairs", DenseI64Matrix(sourceTargetPairs))}
	if ch != nil {
		attrs = append(attrs, nattr("channel_handle", *ch))
	}
	return cb.emit1("stablehlo.collective_permute", []Value{input}, attrs, input.Type())
}

func (cb *CodeBuilder) CollectiveBroadcast(input Value, replicaGroups [][]int64, ch *ChannelHandle) Value {
	attrs := []NamedAttr{nattr("replica_groups", DenseI64Matrix(replicaGroups))}
	if ch != nil {
		attrs = append(attrs, nattr("channel_handle", *ch))
	}
	return cb.emit1("stablehlo.collective_broadcast", []Value{input}, attrs, input.Type())
}

func (cb *CodeBuilder) PartitionId() Value {
	return cb.emit1("stablehlo.partition_id", nil, nil, Tensor(UI32))
}

func (cb *CodeBuilder) ReplicaId() Value {
	return cb.emit1("stablehlo.replica_id", nil, nil, Tensor(UI32))
}

// Infeed emits "stablehlo.infeed"; a trailing token result is appended to
// resultTypes automatically. The returned slice is (results..., token).
func (cb *CodeBuilder) Infeed(resultTypes []Type, token Value, config string) []Value {
	rts := append(append([]Type{}, resultTypes...), Token)
	return cb.emit("stablehlo.infeed", []Value{token},
		[]NamedAttr{nattr("infeed_config", StrAttr(config))}, rts...)
}

// Outfeed emits "stablehlo.outfeed" and returns the result token.
func (cb *CodeBuilder) Outfeed(inputs []Value, token Value, config string) Value {
	ops := append(append([]Value{}, inputs...), token)
	return cb.emit1("stablehlo.outfeed", ops,
		[]NamedAttr{nattr("outfeed_config", StrAttr(config))}, Token)
}

// Send emits "stablehlo.send" and returns the result token.
func (cb *CodeBuilder) Send(inputs []Value, token Value, ch ChannelHandle, isHostTransfer bool) Value {
	ops := append(append([]Value{}, inputs...), token)
	return cb.emit1("stablehlo.send", ops,
		[]NamedAttr{
			nattr("channel_handle", ch),
			nattr("is_host_transfer", BoolAttr(isHostTransfer)),
		}, Token)
}

// Recv emits "stablehlo.recv"; the returned slice is (results..., token).
func (cb *CodeBuilder) Recv(resultTypes []Type, token Value, ch ChannelHandle, isHostTransfer bool) []Value {
	rts := append(append([]Type{}, resultTypes...), Token)
	return cb.emit("stablehlo.recv", []Value{token},
		[]NamedAttr{
			nattr("channel_handle", ch),
			nattr("is_host_transfer", BoolAttr(isHostTransfer)),
		}, rts...)
}

func (cb *CodeBuilder) AfterAll(tokens ...Value) Value {
	return cb.emit1("stablehlo.after_all", tokens, nil, Token)
}

func (cb *CodeBuilder) CreateToken() Value {
	return cb.emit1("stablehlo.create_token", nil, nil, Token)
}

// OptimizationBarrier emits "stablehlo.optimization_barrier"; results
// mirror operand types.
func (cb *CodeBuilder) OptimizationBarrier(xs ...Value) []Value {
	rts := make([]Type, len(xs))
	for i, x := range xs {
		rts[i] = x.Type()
	}
	return cb.emit("stablehlo.optimization_barrier", xs, nil, rts...)
}