package alert

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
)

// Program is a compiled, ready-to-evaluate alert expression. Aliasing cel.Program
// keeps the expression engine an implementation detail of this file — the rest of
// the package (and callers) refer only to alert.Program.
type Program = cel.Program

// alertCELEnv is the CEL environment shared by every compile. Each Env field
// is declared as a typed variable so the type checker rejects unknown names and
// non-boolean expressions at compile time. CrossTypeNumericComparisons lets a
// double variable be compared against an integer literal (e.g. `p95 > 500`)
// instead of forcing `p95 > 500.0`.
var alertCELEnv = mustAlertCELEnv()

func mustAlertCELEnv() *cel.Env {
	env, err := cel.NewEnv(
		cel.CrossTypeNumericComparisons(true),
		cel.Variable("error_rate", cel.DoubleType),
		cel.Variable("p50", cel.DoubleType),
		cel.Variable("p95", cel.DoubleType),
		cel.Variable("throughput", cel.DoubleType),
		cel.Variable("log_count", cel.DoubleType),
		cel.Variable("z_score", cel.DoubleType),
		cel.Variable("health_score", cel.DoubleType),
		cel.Variable("error_rate_delta", cel.DoubleType),
		cel.Variable("p95_delta", cel.DoubleType),
		cel.Variable("throughput_delta", cel.DoubleType),
		cel.Variable("service", cel.StringType),
	)
	if err != nil {
		panic(fmt.Sprintf("alert: build CEL env: %v", err))
	}
	return env
}

// CompileExpression compiles a CEL expression against the alert environment and
// validates that it type-checks to a boolean.
func CompileExpression(expression string) (Program, error) {
	if expression == "" {
		return nil, fmt.Errorf("expression must not be empty")
	}
	ast, iss := alertCELEnv.Compile(expression)
	if iss != nil && iss.Err() != nil {
		return nil, errors.New(issueMessage(iss.Errors()[0]))
	}
	if ast.OutputType().Kind() != types.BoolKind {
		return nil, fmt.Errorf("expression must be a condition that is true or false, not a %s", ast.OutputType())
	}
	prog, err := alertCELEnv.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("build program: %w", err)
	}
	return prog, nil
}

var (
	undeclared = regexp.MustCompile(`^undeclared reference to '([^']*)'.*$`)
	overload   = regexp.MustCompile(`^found no matching overload for '_?([^']*?)_?' applied to '\((.*)\)'.*$`)
	expecting  = regexp.MustCompile(` expecting .*$`)
)

// issueMessage turns a CEL issue into one short sentence with its position,
// without the parser's token lists.
func issueMessage(e *cel.Error) string {
	message := expecting.ReplaceAllString(e.Message, "")
	message = strings.ReplaceAll(message, "mismatched input '<EOF>'", "the expression ends too early")
	message = undeclared.ReplaceAllString(message, "Unknown variable $1")
	message = overload.ReplaceAllString(message, "Operator $1 does not apply to $2")
	message, detail, found := strings.Cut(message, ": ")
	position := fmt.Sprintf(" at column %d", e.Location.Column()+1)
	if e.Location.Line() > 1 {
		position = fmt.Sprintf(" at line %d, column %d", e.Location.Line(), e.Location.Column()+1)
	}
	if found {
		return message + position + ": " + detail
	}
	return message + position
}

// celActivation binds the CEL variables to an Env's values. The keys must
// stay in sync with the cel.Variable declarations in mustAlertCELEnv above;
// TestEnvVariablesDeclaredAndBound enforces that against the Env `expr`
// field tags, so a drift fails the build instead of silently breaking a rule at
// eval time.
func celActivation(env Env) map[string]any {
	return map[string]any{
		"error_rate":       env.ErrorRate,
		"p50":              env.P50,
		"p95":              env.P95,
		"throughput":       env.Throughput,
		"log_count":        env.LogCount,
		"z_score":          env.ZScore,
		"health_score":     env.HealthScore,
		"error_rate_delta": env.ErrorRateDelta,
		"p95_delta":        env.P95Delta,
		"throughput_delta": env.ThroughputDelta,
		"service":          env.Service,
	}
}

// EvalExpression runs a compiled program with the given environment.
func EvalExpression(prog Program, env Env) (bool, error) {
	out, _, err := prog.Eval(celActivation(env))
	if err != nil {
		return false, fmt.Errorf("eval expression: %w", err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expression result is not bool: %T", out.Value())
	}
	return b, nil
}

// SafeEval wraps EvalExpression with panic recovery, logging unexpected panics
// via slog. Returns false on panic.
func SafeEval(prog Program, env Env) (result bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("alert: expression eval panic", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("expression eval panicked: %v", r)
		}
	}()
	return EvalExpression(prog, env)
}
