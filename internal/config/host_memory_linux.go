//go:build linux

package config

import (
	"os"
	"strconv"
	"strings"
)

func detectHostMemory() uint64 {
	return readMemTotal("/proc/meminfo")
}

// readMemTotal reads MemTotal from a /proc/meminfo-formatted file, in bytes.
func readMemTotal(path string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb << 10
	}
	return 0
}
