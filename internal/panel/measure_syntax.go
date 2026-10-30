package panel

// MeasureSyntaxCode identifies syntax failures independently of frontend copy.
type MeasureSyntaxCode uint8

const (
	MeasureMissingAtom MeasureSyntaxCode = iota
	MeasureMissingOperand
	MeasureMissingClose
)

// MeasureGrammar adapts tokens, terminals and source locations to the shared
// arithmetic grammar. Each frontend owns its field and predicate syntax only.
type MeasureGrammar struct {
	Peek     func() string
	Take     func()
	Position func() int
	CanAtom  func() bool
	Atom     func() MeasureExpr
	Node     func(MeasureExpr, int, ...MeasureExpr) MeasureExpr
	Group    func(MeasureExpr, int)
	Enter    func(int)
	Leave    func()
	Depth    func(int) bool
	OK       func() bool
	Fail     func(MeasureSyntaxCode, string, int)
}

// ReadMeasureExpression is the single precedence and grouping grammar used by
// structured measures and FanQL. The result is panel's aggregate AST directly.
func ReadMeasureExpression(g MeasureGrammar, minPrecedence, depth int) (MeasureExpr, int) {
	if !g.Depth(depth) || !g.OK() {
		return nil, 0
	}
	start := g.Position()
	var left MeasureExpr
	height := 0
	switch g.Peek() {
	case "-":
		g.Take()
		g.Enter(start)
		x, h := ReadMeasureExpression(g, 3, depth+1)
		g.Leave()
		if x == nil {
			return nil, 0
		}
		left = g.Node(MeasureNeg{x}, start, x)
		height = h + 1
	case "(":
		g.Take()
		g.Enter(start)
		x, h := ReadMeasureExpression(g, 0, depth+1)
		g.Leave()
		left = x
		height = h + 1
		if g.Peek() != ")" {
			g.Fail(MeasureMissingClose, "", start)
		} else {
			g.Take()
			if left != nil {
				g.Group(left, start)
			}
		}
	default:
		if !g.CanAtom() {
			g.Fail(MeasureMissingAtom, "", start)
			return nil, 0
		}
		left = g.Atom()
	}
	if left == nil {
		return nil, 0
	}
	for g.OK() {
		op := g.Peek()
		prec := 0
		if op == "+" || op == "-" {
			prec = 1
		}
		if op == "*" || op == "/" {
			prec = 2
		}
		if prec == 0 || prec < minPrecedence {
			break
		}
		at := g.Position()
		g.Take()
		if !g.CanAtom() && g.Peek() != "-" && g.Peek() != "(" {
			g.Fail(MeasureMissingOperand, op, at)
			return left, height
		}
		right, h := ReadMeasureExpression(g, prec+1, depth+1)
		if right == nil {
			return left, height
		}
		height = 1 + max(height, h)
		if !g.Depth(height + depth) {
			return left, height
		}
		left = g.Node(MeasureBinary{op[0], left, right}, start, left, right)
	}
	return left, height
}
