package segment

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
)

type countedSignalFile struct {
	*os.File
	open *atomic.Int64
}

func (f *countedSignalFile) Close() error {
	f.open.Add(-1)
	return f.File.Close()
}

func TestSignalStoreScanKeepsFileDescriptorsBounded(t *testing.T) {
	store, err := OpenSignalStore[telemetry.Log](t.TempDir(), "EventUnixNanos")
	if err != nil {
		t.Fatalf("OpenSignalStore: %v", err)
	}
	defer store.Close()
	for i := range 32 {
		if err := store.Append(stringID(i), []telemetry.Log{{EventUnixNanos: int64(i + 1)}}); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	var open, maximum atomic.Int64
	store.openFile = func(path string) (signalFile, error) {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		current := open.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		return &countedSignalFile{File: file, open: &open}, nil
	}
	visited := 0
	if err := store.Scan(0, 100, func(telemetry.Log) bool {
		visited++
		return true
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if visited != 32 {
		t.Fatalf("visited = %d, want 32", visited)
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum simultaneous descriptors = %d, want 1", got)
	}
	if got := open.Load(); got != 0 {
		t.Fatalf("descriptors left open = %d, want 0", got)
	}
}

func stringID(i int) string {
	const digits = "0123456789abcdef"
	return "batch-" + string([]byte{digits[(i>>4)&15], digits[i&15]})
}

func TestValidateSignalDirectoryRejectsOutOfBoundsCount(t *testing.T) {
	const size = 4096
	if err := validateSignalDirectory(size, signalHeaderSize, 0xFFFFFFFF); err == nil {
		t.Fatal("validateSignalDirectory accepted a block count larger than the file")
	}
	if err := validateSignalDirectory(size, ^uint64(0)-1024, 64); err == nil {
		t.Fatal("validateSignalDirectory accepted a wrapping directory offset")
	}
	if err := validateSignalDirectory(size, 0, 1); err == nil {
		t.Fatal("validateSignalDirectory accepted a directory inside the header")
	}
	if err := validateSignalDirectory(size, signalHeaderSize, 8); err != nil {
		t.Fatalf("validateSignalDirectory rejected a sound header: %v", err)
	}
}
