//go:build darwin

package config

import "golang.org/x/sys/unix"

func detectHostMemory() uint64 {
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return memory
}
