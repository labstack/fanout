package fanql

// Failure is the transport-neutral FanQL failure.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
