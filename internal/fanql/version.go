package fanql

import "encoding/json"

// VersionRequest distinguishes omission from every explicitly supplied value.
// Both transports use this request contract before compiling text.
type VersionRequest struct {
	FanQLVersion json.RawMessage `json:"fanql_version,omitempty" jsonschema:"FanQL syntax version as an integer; omission uses the current syntax and returns a notice"`
}

// VersionProblem is the transport-neutral version failure.
type VersionProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (r VersionRequest) Validate() (*Notice, *VersionProblem) {
	if len(r.FanQLVersion) == 0 {
		return &Notice{Path: "fanql_version", Message: "fanql_version missing; assuming 1"}, nil
	}
	var n int
	if json.Unmarshal(r.FanQLVersion, &n) == nil && n == Version {
		return nil, nil
	}
	return nil, &VersionProblem{Code: "unsupported_fanql_version", Message: "This server cannot read the requested FanQL version.", Hint: "send fanql_version: 1"}
}
