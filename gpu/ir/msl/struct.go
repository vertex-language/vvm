package msl

// Field is a struct field with an optional [[...]] attribute
// ([[position]], [[attribute(0)]], [[color(0)]], ...).
type Field struct {
	Name string
	Type Type
	Attr Attr
}

// FieldList is an append-only field collection.
type FieldList struct{ items []Field }

// Add appends a field.
func (l *FieldList) Add(f Field) { l.items = append(l.items, f) }

// Items returns the fields in declaration order.
func (l *FieldList) Items() []Field { return l.items }

// Struct is a struct definition. Vertex/fragment IO and argument buffers
// are structs with attributed fields.
type Struct struct {
	Name   string
	Fields FieldList
}

// NewStruct creates an empty struct definition.
func NewStruct(name string) *Struct { return &Struct{Name: name} }

// StructList is an append-only struct collection. Structs print in
// declaration order — no dependency sorting; declare before use.
type StructList struct{ items []*Struct }

// Add appends a struct definition.
func (l *StructList) Add(s *Struct) { l.items = append(l.items, s) }

// Items returns the structs in declaration order.
func (l *StructList) Items() []*Struct { return l.items }