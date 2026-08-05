//go:build !linux && !darwin

package env

func detectHostMemory() uint64 {
	return 0
}
