//go:build race

package engine

// raceDetector reports a -race build, whose instrumentation distorts timing.
const raceDetector = true
