package alert

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// CompileExpression compiles an expression string against AlertEnv and
// validates that it returns a boolean.
func CompileExpression(expression string) (*vm.Program, error) {
	if expression == "" {
		return nil, fmt.Errorf("expression must not be empty")
	}
	prog, err := expr.Compile(expression, expr.Env(AlertEnv{}), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("compile expression: %w", err)
	}
	return prog, nil
}

// EvalExpression runs a compiled program with the given environment.
func EvalExpression(prog *vm.Program, env AlertEnv) (bool, error) {
	result, err := expr.Run(prog, env)
	if err != nil {
		return false, fmt.Errorf("eval expression: %w", err)
	}
	b, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("expression result is not bool: %T", result)
	}
	return b, nil
}

// SafeEval wraps EvalExpression with panic recovery, logging unexpected panics
// via slog. Returns false on panic.
func SafeEval(prog *vm.Program, env AlertEnv) (result bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("alert: expression eval panic", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("expression eval panicked: %v", r)
		}
	}()
	return EvalExpression(prog, env)
}
