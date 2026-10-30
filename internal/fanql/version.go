package fanql

import "encoding/json"

// VersionRequest distinguishes omission from every explicitly supplied value.
// Both transports use this request contract before compiling text.
type VersionRequest struct {
	FanQLVersion json.RawMessage `json:"fanql_version,omitempty" jsonschema:"FanQL syntax version as an integer; omission uses the current syntax and returns a notice"`
}

// Failure is the transport-neutral FanQL failure.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (r VersionRequest) Validate() (*Notice, *Failure) {
	if len(r.FanQLVersion) == 0 {
		notice := MissingVersionNotice()
		return &notice, nil
	}
	var n int
	if json.Unmarshal(r.FanQLVersion, &n) == nil && n == Version {
		return nil, nil
	}
	return nil, &Failure{Code: "unsupported_fanql_version", Message: "This server cannot read the requested FanQL version.", Hint: "send fanql_version: 1"}
}

// MissingVersionNotice describes the default used when the syntax version is omitted.
func MissingVersionNotice() Notice {
	return Notice{Path: "fanql_version", Message: "fanql_version missing; assuming 1"}
}
