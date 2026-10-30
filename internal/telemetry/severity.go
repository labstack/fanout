package telemetry

import (
	"maps"
	"slices"
	"strings"
)

// SeverityBands lists the six standard severity bands in ascending rank.
var SeverityBands = [...]string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"}

var severityAliases = map[string]string{
	"fatal": "FATAL", "crit": "FATAL", "critical": "FATAL", "emerg": "FATAL", "emergency": "FATAL", "alert": "FATAL", "panic": "FATAL",
	"error": "ERROR", "err": "ERROR", "severe": "ERROR", "exception": "ERROR",
	"warn": "WARN", "warning": "WARN",
	"info": "INFO", "information": "INFO", "informational": "INFO", "notice": "INFO",
	"debug": "DEBUG", "dbg": "DEBUG", "fine": "DEBUG",
	"trace": "TRACE", "verbose": "TRACE", "finest": "TRACE",
}

// SeverityAliases returns a copy of the lowercase synonym-to-band table.
func SeverityAliases() map[string]string { return maps.Clone(severityAliases) }

// SeverityRank returns a standard band's ascending rank; unknown bands have none.
func SeverityRank(band string) (int, bool) {
	rank := slices.Index(SeverityBands[:], band)
	return rank, rank >= 0
}

// NormalizeSeverity maps a record onto the six OTel severity names, so that a
// search for ERROR finds every error however its emitter spelled it.
//
// Passing SeverityText through unchanged is what produced a store holding
// INFO, INFORMATION and "" for the same level: .NET writes "Information",
// Envoy's access logs carry no severity at all, and each spelling then answered
// a different query. severity_number is preferred because the specification
// makes it the authoritative field and the text the display form, and a record
// that carries neither is stored as UNSPECIFIED rather than as an empty string
// — the log-bucket query already labels that case UNSPECIFIED, and a level you
// can see in the chart should be one you can filter on.
func NormalizeSeverity(text string, number int32) string {
	if number > 0 {
		rank := min(int((number-1)/4), len(SeverityBands)-1)
		return SeverityBands[rank]
	}
	return SeverityFromText(text)
}

// SeverityFromText canonicalizes the spellings emitters use when they send no
// severity_number: OTel's own numbered short names (INFO2, WARN3), the syslog
// names, and the long forms favoured by .NET and Python.
func SeverityFromText(text string) string {
	name := strings.ToUpper(strings.TrimSpace(text))
	name = strings.TrimRight(name, "0123456789")
	if band, ok := severityAliases[strings.ToLower(name)]; ok {
		return band
	}
	if name == "" {
		return "UNSPECIFIED"
	}
	// An unrecognised level is still the emitter's own word for it, and
	// discarding it would lose the only grading the record carries.
	return name
}
