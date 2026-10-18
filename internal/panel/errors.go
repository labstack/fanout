package panel

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

// ErrorKind selects the publication boundary, independently of the cause's text.
// Authored queries receive diagnostics; operational causes stay in logs.
type ErrorKind bool

const (
	ReadError ErrorKind = false
	SQLError  ErrorKind = true
)

const ReadFailure = "Could not read the requested data. Try again."

var (
	engineClass      = regexp.MustCompile(`(?i)(?:[a-z ]+ Error):\s*`)
	operationalClass = regexp.MustCompile(`(?i)(?:IO|Out of Memory|Internal|Fatal|Interrupt|Connection|Serialization|Transaction) Error:`)
	paths            = regexp.MustCompile(`(?i)(?:"(?:[a-z]:[\\/]|/)[^"]*"|'(?:[a-z]:[\\/]|/)[^']*'|(?:^|[\s:(])(?:[a-z]:[\\/]|\.{1,2}[\\/]|/)[^\s'"<>:,;()\[\]{}]*[\\/][^\s'"<>:,;()\[\]{}]+|[a-z0-9_.-]+\.(?:parquet|sqlite|duckdb|batch)\b)`)
	operationalChain = regexp.MustCompile(`(?i)^(?:read indexed|query trace|discover .+ attributes|schema discovery|exemplars:|drill logs:|storage:|open ).*:`)
	internalCast     = regexp.MustCompile(`(?i)CAST\(\s*(\$[a-z_][a-z0-9_]*|\?|[^()]+)\s+AS\s+(?:TIMESTAMPTZ_NS|TIMESTAMP_NS|VARIANT)\s*\)`)
	timestampCast    = regexp.MustCompile(`(?i)::(?:TIMESTAMPTZ_NS|TIMESTAMP_NS|VARIANT)\b`)
	internalWords    = regexp.MustCompile(`(?i)\b(?:DuckDB|Parquet|SQLite|TIMESTAMPTZ_NS|TIMESTAMP_NS|VARIANT|parquet_index)\b|\btelemetry\.`)
	sqlEcho          = regexp.MustCompile(`(?i)(?:\bLINE [0-9]+:|Candidate bindings:|\b(?:SQL|Query):\s*(?:SELECT|WITH)\s)`)
	candidateBinding = regexp.MustCompile(`^"?([\pL_][\pL\pN_$.]*)"?$`)
)

// PresentError logs the complete cause and publishes only the chosen audience's
// message. It never changes cancellation identity at the calling boundary.
func PresentError(err error, kind ErrorKind) string {
	if err == nil {
		return ""
	}
	slog.Warn("telemetry operation failed", "error", err)
	return errorMessage(err, kind)
}

func errorMessage(err error, kind ErrorKind) string {
	if kind == ReadError || isOperational(err) {
		return ReadFailure
	}
	return PresentMessage(err.Error())
}

// executionErrorKind classifies the cause, regardless of whether a panel used
// SQL or structured filters. Unknown failures remain operational.
func executionErrorKind(err error) ErrorKind {
	var engineError *duckdb.Error
	if errors.As(err, &engineError) {
		switch engineError.Type {
		case duckdb.ErrorTypeBinder, duckdb.ErrorTypeParser, duckdb.ErrorTypeCatalog,
			duckdb.ErrorTypeConversion, duckdb.ErrorTypeOutOfRange, duckdb.ErrorTypeDivideByZero,
			duckdb.ErrorTypeInvalidInput, duckdb.ErrorTypeMismatchType, duckdb.ErrorTypeInvalidType,
			duckdb.ErrorTypeSyntax, duckdb.ErrorTypeExpression:
			return SQLError
		}
	}
	var problems Problems
	if errors.As(err, &problems) {
		return SQLError
	}
	return ReadError
}

// PresentDiagnosis bounds product-authored prose without interpreting observed
// telemetry values as engine errors or filesystem paths.
func PresentDiagnosis(message string) string { return boundError(message) }

// PresentMessage presents engine and operational error text. Engine source
// excerpts are expanded server SQL. Keep only the first line and candidate names.
func PresentMessage(message string) string {
	if message == "" {
		return ""
	}
	first, rest, _ := strings.Cut(message, "\n")
	message = first
	if operationalClass.MatchString(message) || operationalChain.MatchString(message) {
		return ReadFailure
	}
	if matches := engineClass.FindAllStringIndex(message, -1); len(matches) > 0 {
		message = message[matches[len(matches)-1][1]:]
		message = paths.ReplaceAllString(message, "data")
	} else if paths.MatchString(message) {
		return ReadFailure
	}
	for _, prefix := range []string{"SQL parsing failed: ", "SQL binding failed: ", "does not bind: "} {
		message = strings.TrimPrefix(message, prefix)
	}
	if at := sqlEcho.FindStringIndex(message); at != nil {
		message = message[:at[0]]
	}
	for {
		plain := internalCast.ReplaceAllString(message, "$1")
		if plain == message {
			break
		}
		message = plain
	}
	message = timestampCast.ReplaceAllString(message, "")
	message = internalWords.ReplaceAllStringFunc(message, func(word string) string {
		switch strings.ToLower(word) {
		case "telemetry.":
			return ""
		case "timestamptz_ns", "timestamp_ns":
			return "timestamp"
		case "variant":
			return "value"
		case "duckdb", "parquet", "sqlite", "parquet_index":
			return "data"
		default:
			return word
		}
	})
	// Candidates are the only useful continuation of an engine diagnostic.
	// Accept identifier lists, never expressions from expanded server SQL.
	for _, line := range strings.Split(first+"\n"+rest, "\n") {
		_, bindings, ok := strings.Cut(line, "Candidate bindings:")
		if !ok {
			continue
		}
		var candidates []string
		for _, binding := range strings.Split(bindings, ",") {
			parts := candidateBinding.FindStringSubmatch(strings.TrimSpace(binding))
			if parts == nil {
				continue
			}
			name := candidateName(parts[1])
			candidates = append(candidates, `"`+name+`"`)
		}
		if len(candidates) > 0 {
			message += "\nCandidate bindings: " + strings.Join(candidates, ", ")
		}
		break
	}
	message = strings.TrimSpace(boundError(message))
	if message == "" {
		return "Check the query syntax and selected columns."
	}
	return message
}

// Candidate strings quote an entire binding, so a dot is not necessarily a
// qualifier. Strip only the internal schema and server wrapper alias.
func candidateName(name string) string {
	name = strings.TrimPrefix(name, "telemetry.")
	return strings.TrimPrefix(name, "_result.")
}

// PresentProblems copies diagnostics so HTTP, previews, receipts and MCP share
// the same wording without mutating a cached validation result.
func PresentProblems(problems Problems) Problems {
	out := make(Problems, len(problems))
	for i, p := range problems {
		if p.productDiagnosis {
			p.Message = PresentDiagnosis(p.Message)
			p.Hint = PresentDiagnosis(p.Hint)
		} else {
			p.Message = PresentMessage(p.Message)
			p.Hint = PresentMessage(p.Hint)
		}
		out[i] = p
	}
	return out
}

// PresentedError preserves the cause for errors.Is/As while publishing a message.
func PresentedError(err error, kind ErrorKind) error {
	var problems Problems
	if errors.As(err, &problems) {
		return PresentProblems(problems)
	}
	return presentedError{cause: err, message: PresentError(err, kind)}
}

type presentedError struct {
	cause   error
	message string
}

func (e presentedError) Error() string { return e.message }
func (e presentedError) Unwrap() error { return e.cause }

// Bound author diagnostics without splitting a Unicode character.
const maxPanelErrorBytes = 4096

func boundError(message string) string {
	if len(message) <= maxPanelErrorBytes {
		return message
	}
	cut := maxPanelErrorBytes
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut] + "…"
}
