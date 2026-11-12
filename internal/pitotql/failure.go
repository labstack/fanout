package pitotql

import "encoding/json"

// Failure is the transport-neutral PitotQL failure.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// UnmarshalJSON accepts recorded responses from before the language rename.
func (f *Failure) UnmarshalJSON(data []byte) error {
	type plain Failure
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Code == "invalid_fanql" {
		value.Code = "invalid_pitotql"
	}
	*f = Failure(value)
	return nil
}
