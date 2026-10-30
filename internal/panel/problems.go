package panel

import (
	"fmt"
	"strings"
)

// TextSpan uses code-point offsets, with an exclusive end.
type TextSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// FixIt is an alternative text replacement the author may choose to apply.
type FixIt struct {
	Label string   `json:"label"`
	Span  TextSpan `json:"span"`
	Text  string   `json:"text"`
}

// Problem is one reason a spec cannot be saved or run, addressed by a JSON
// path so the author can fix exactly that field.
type Problem struct {
	Path    string    `json:"path"`
	Message string    `json:"message"`
	Hint    string    `json:"hint,omitempty"`
	Span    *TextSpan `json:"span,omitempty"`
	FixIts  []FixIt   `json:"fix_its,omitempty"`

	productDiagnosis bool
}

// DiagnosisProblem marks product-authored guidance so subsequent error adapters
// never reinterpret it as engine or filesystem failures.
func DiagnosisProblem(path, message, hint string) Problem {
	return Problem{Path: path, Message: PresentDiagnosis(message), Hint: PresentDiagnosis(hint), productDiagnosis: true}
}

// Problems is a validation result; empty means valid. It is an error so a
// caller can return it unchanged and an adapter can unwrap it with errors.As.
type Problems []Problem

func (p Problems) Error() string {
	parts := make([]string, 0, min(len(p), 6))
	for i, problem := range PresentProblems(p) {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more", len(p)-5))
			break
		}
		text := problem.Path + ": " + problem.Message
		if problem.Path == "" {
			text = problem.Message
		}
		if problem.Hint != "" {
			text += " (" + problem.Hint + ")"
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "; ")
}

func (p *Problems) add(path, message string) {
	*p = append(*p, Problem{Path: path, Message: PresentMessage(message)})
}

func (p *Problems) addHint(path, message, hint string) {
	*p = append(*p, Problem{Path: path, Message: PresentMessage(message), Hint: PresentMessage(hint)})
}
