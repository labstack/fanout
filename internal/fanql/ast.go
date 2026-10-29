package fanql

import "github.com/labstack/fanout/internal/panel"

type Query struct {
	Source     string
	SourceSpan Span
	Where      []Expr
	WhereWord  bool
	Stage      *Stage
	Sort       *Sort
	Limit      *Limit
	Span       Span
}

type Expr interface{ Span() Span }
type And struct {
	Terms []Expr
	At    Span
}
type Or struct {
	Terms []Expr
	At    Span
}
type Not struct {
	X  Expr
	At Span
}
type Compare struct {
	Field Field
	Op    string
	Value Value
	Bands []string
	At    Span
}
type In struct {
	Field    Field
	Values   []Value
	Variable string
	Negated  bool
	At       Span
}
type Between struct {
	Field   Field
	Lo, Hi  Value
	Negated bool
	Bands   []string
	At      Span
}
type Like struct {
	Field       Field
	Pattern     Value
	Insensitive bool
	Negated     bool
	At          Span
}
type IsNull struct {
	Field   Field
	Negated bool
	At      Span
}
type Exists struct {
	Field Field
	At    Span
}
type Call struct {
	Name string
	Args []CallArg
	Cmp  *Comparison
	At   Span
}
type CallArg struct {
	Field *Field
	Value *Value
	Call  *Call
}
type Comparison struct {
	Op    string
	Value Value
}
type FreeText struct {
	Value  Value
	At     Span
	column string // resolved search column: body on logs, operation on spans
}

// RawExpr is a raw SQL filter entry. Raw names its lexical token kind.
type RawExpr struct {
	Text string
	At   Span
}

func (x And) Span() Span      { return x.At }
func (x Or) Span() Span       { return x.At }
func (x Not) Span() Span      { return x.At }
func (x Compare) Span() Span  { return x.At }
func (x In) Span() Span       { return x.At }
func (x Between) Span() Span  { return x.At }
func (x Like) Span() Span     { return x.At }
func (x IsNull) Span() Span   { return x.At }
func (x Exists) Span() Span   { return x.At }
func (x Call) Span() Span     { return x.At }
func (x FreeText) Span() Span { return x.At }
func (x RawExpr) Span() Span  { return x.At }

type Field struct {
	Scope   string
	Key     string
	Bracket bool
	Single  bool
	Ref     *FieldRef
	At      Span
}

// FieldRef is populated by resolution; parsing leaves Ref nil.
type FieldRef struct {
	Column    string
	Key       string
	Type      panel.FieldType
	Unit      string
	CompareAs string
}

type ValueKind uint8

const (
	WordValue ValueKind = iota
	StringValue
	NumberValue
	DurationValue
	BooleanValue
	VariableValue
	StarValue
)

type Value struct {
	Kind   ValueKind
	Text   string
	Stored string
	At     Span
}

type Stage struct {
	Measures  []Measure
	Histogram *Field
	By        []Field
	Bucket    string
	At        Span
}
type Measure struct {
	Expr  MExpr
	Alias string
	At    Span
}
type MExpr interface{ Span() Span }
type MCall struct {
	Name string
	Args []MArg
	At   Span
}
type MArg struct {
	Field  *Field
	Number *float64
	Star   bool
	At     Span
}
type MCountIf struct {
	Filter Expr
	At     Span
}
type MBinary struct {
	Op   byte
	L, R MExpr
	At   Span
}
type MNeg struct {
	X  MExpr
	At Span
}
type MConst struct {
	Value float64
	Text  string
	At    Span
}

func (x MCall) Span() Span    { return x.At }
func (x MCountIf) Span() Span { return x.At }
func (x MBinary) Span() Span  { return x.At }
func (x MNeg) Span() Span     { return x.At }
func (x MConst) Span() Span   { return x.At }

type Sort struct {
	Key       string
	Ascending bool
	At        Span
}
type Limit struct {
	N  int
	At Span
}
