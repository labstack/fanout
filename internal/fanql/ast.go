package fanql

import "github.com/labstack/fanout/internal/panel"

type queryTree struct {
	Source     string
	SourceSpan span
	Where      []filterExpr
	WhereWord  bool
	Stage      *aggregation
	Sort       *sortClause
	Limit      *limitClause
	Span       span
}

type filterExpr interface{ Span() span }
type andExpr struct {
	Terms []filterExpr
	At    span
}
type orExpr struct {
	Terms []filterExpr
	At    span
}
type notExpr struct {
	X  filterExpr
	At span
}
type comparison struct {
	Field fieldName
	Op    string
	Value literalValue
	Bands []string
	At    span
}
type inExpr struct {
	Field    fieldName
	Values   []literalValue
	Variable string
	Negated  bool
	At       span
}
type betweenExpr struct {
	Field   fieldName
	Lo, Hi  literalValue
	Negated bool
	Bands   []string
	At      span
}
type likeExpr struct {
	Field       fieldName
	Pattern     literalValue
	Insensitive bool
	Negated     bool
	At          span
}
type nullExpr struct {
	Field   fieldName
	Negated bool
	At      span
}
type existsExpr struct {
	Field fieldName
	At    span
}
type filterCall struct {
	Name string
	Args []callArg
	Cmp  *callComparison
	At   span
}
type callArg struct {
	Field *fieldName
	Value *literalValue
	Call  *filterCall
}
type callComparison struct {
	Op    string
	Value literalValue
}
type textExpr struct {
	Value  literalValue
	At     span
	column string // resolved search column: body on logs, operation on spans
}

// RawExpr is a raw SQL filter entry. Raw names its lexical token kind.
type rawEntry struct {
	Text string
	At   span
}

func (x andExpr) Span() span     { return x.At }
func (x orExpr) Span() span      { return x.At }
func (x notExpr) Span() span     { return x.At }
func (x comparison) Span() span  { return x.At }
func (x inExpr) Span() span      { return x.At }
func (x betweenExpr) Span() span { return x.At }
func (x likeExpr) Span() span    { return x.At }
func (x nullExpr) Span() span    { return x.At }
func (x existsExpr) Span() span  { return x.At }
func (x filterCall) Span() span  { return x.At }
func (x textExpr) Span() span    { return x.At }
func (x rawEntry) Span() span    { return x.At }

type fieldName struct {
	Scope   string
	Key     string
	Bracket bool
	Single  bool
	Ref     *fieldRef
	At      span
}

// FieldRef is populated by resolution; parsing leaves Ref nil.
type fieldRef struct {
	Column    string
	Key       string
	Type      panel.FieldType
	Unit      string
	CompareAs string
}

type valueKind uint8

const (
	wordValue valueKind = iota
	stringValue
	numberValue
	durationValue
	booleanValue
	variableValue
	starValue
)

type literalValue struct {
	Kind   valueKind
	Text   string
	Stored string
	At     span
}

type aggregation struct {
	Measures  []measureNode
	Histogram *fieldName
	By        []fieldName
	Bucket    string
	At        span
}
type measureNode struct {
	Parsed  panel.Measure
	Info    map[panel.MeasureExpr]*measureSource
	Alias   string
	At      span
	Filters map[string]filterExpr
}

func (m measureNode) Span() span { return m.At }

type sortClause struct {
	Key       string
	Ascending bool
	At        span
}
type limitClause struct {
	N  int
	At span
}

type measureSource struct {
	at     span
	args   []measureArgument
	filter filterExpr
}
type measureArgument struct {
	Field  *fieldName
	Number *float64
	Star   bool
	At     span
}
