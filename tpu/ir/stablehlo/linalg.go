package stablehlo

// DotGeneral emits "stablehlo.dot_general". The result type is
// caller-supplied (inference here would amount to reimplementing the spec).
func (cb *CodeBuilder) DotGeneral(t Type, lhs, rhs Value, dims DotDims) Value {
	return cb.emit1("stablehlo.dot_general", []Value{lhs, rhs},
		[]NamedAttr{nattr("dot_dimension_numbers", dims)}, t)
}

// ConvConfig carries convolution attributes. Zero group counts print as 1.
type ConvConfig struct {
	WindowStrides     []int64
	Padding           [][]int64 // one [low, high] pair per spatial dim
	LhsDilation       []int64
	RhsDilation       []int64
	WindowReversal    []bool
	Dims              Attr // ConvDims("[b,0,1,f]x[0,1,i,o]->[b,0,1,f]")
	FeatureGroupCount int64
	BatchGroupCount   int64
	PrecisionConfig   Attr // optional, e.g. RawAttrValue("[#stablehlo<precision DEFAULT>, ...]")
}

func (cb *CodeBuilder) Convolution(t Type, lhs, rhs Value, cfg ConvConfig) Value {
	fgc, bgc := cfg.FeatureGroupCount, cfg.BatchGroupCount
	if fgc == 0 {
		fgc = 1
	}
	if bgc == 0 {
		bgc = 1
	}
	attrs := []NamedAttr{}
	if len(cfg.WindowStrides) > 0 {
		attrs = append(attrs, nattr("window_strides", I64Array(cfg.WindowStrides...)))
	}
	if len(cfg.Padding) > 0 {
		attrs = append(attrs, nattr("padding", DenseI64Matrix(cfg.Padding)))
	}
	if len(cfg.LhsDilation) > 0 {
		attrs = append(attrs, nattr("lhs_dilation", I64Array(cfg.LhsDilation...)))
	}
	if len(cfg.RhsDilation) > 0 {
		attrs = append(attrs, nattr("rhs_dilation", I64Array(cfg.RhsDilation...)))
	}
	if len(cfg.WindowReversal) > 0 {
		attrs = append(attrs, nattr("window_reversal", BoolArray(cfg.WindowReversal...)))
	}
	if cfg.Dims != nil {
		attrs = append(attrs, nattr("dimension_numbers", cfg.Dims))
	}
	attrs = append(attrs,
		nattr("feature_group_count", I64Attr(fgc)),
		nattr("batch_group_count", I64Attr(bgc)))
	if cfg.PrecisionConfig != nil {
		attrs = append(attrs, nattr("precision_config", cfg.PrecisionConfig))
	}
	return cb.emit1("stablehlo.convolution", []Value{lhs, rhs}, attrs, t)
}

func (cb *CodeBuilder) Cholesky(t Type, a Value, lower bool) Value {
	return cb.emit1("stablehlo.cholesky", []Value{a},
		[]NamedAttr{nattr("lower", BoolAttr(lower))}, t)
}

// TriangularSolveConfig carries triangular_solve attributes.
type TriangularSolveConfig struct {
	LeftSide     bool
	Lower        bool
	UnitDiagonal bool
	TransposeA   TransposeKind // defaults to NO_TRANSPOSE
}

func (cb *CodeBuilder) TriangularSolve(t Type, a, b Value, cfg TriangularSolveConfig) Value {
	ta := cfg.TransposeA
	if ta == "" {
		ta = NoTranspose
	}
	return cb.emit1("stablehlo.triangular_solve", []Value{a, b},
		[]NamedAttr{
			nattr("left_side", BoolAttr(cfg.LeftSide)),
			nattr("lower", BoolAttr(cfg.Lower)),
			nattr("unit_diagonal", BoolAttr(cfg.UnitDiagonal)),
			nattr("transpose_a", enumAttr("transpose", string(ta))),
		}, t)
}

func (cb *CodeBuilder) Fft(t Type, x Value, kind FftKind, fftLength []int64) Value {
	return cb.emit1("stablehlo.fft", []Value{x},
		[]NamedAttr{
			nattr("fft_type", enumAttr("fft_type", string(kind))),
			nattr("fft_length", I64Array(fftLength...)),
		}, t)
}