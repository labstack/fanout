//go:build !linux && !darwin

package config

func detectHostMemory() uint64 {
	return 0
}
