package pitotql

// Failure is the transport-neutral PitotQL failure.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
