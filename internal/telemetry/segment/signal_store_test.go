package segment

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
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

func TestValidateSignalBlocksRejectsOutOfBoundsExtents(t *testing.T) {
	const size, dirOffset = 4096, uint64(2048)
	sound := []signalBlock{
		{offset: signalHeaderSize, length: 512, rows: 10},
		{offset: signalHeaderSize + 512, length: 512, rows: 10},
	}
	if err := validateSignalBlocks(size, dirOffset, sound, 20); err != nil {
		t.Fatalf("validateSignalBlocks rejected sound blocks: %v", err)
	}
	tests := []struct {
		name   string
		blocks []signalBlock
		rows   uint32
	}{
		{"length past the directory", []signalBlock{{offset: signalHeaderSize, length: 4096, rows: 10}}, 10},
		{"offset inside the header", []signalBlock{{offset: 0, length: 16, rows: 10}}, 10},
		{"extent wraps", []signalBlock{{offset: ^uint64(0) - 8, length: 64, rows: 10}}, 10},
		{"rows exceed the block cap", []signalBlock{{offset: signalHeaderSize, length: 16, rows: signalBlockRows + 1}}, signalBlockRows + 1},
		{"rows disagree with the header", []signalBlock{{offset: signalHeaderSize, length: 16, rows: 10}}, 11},
		{"empty block", []signalBlock{{offset: signalHeaderSize, length: 0, rows: 0}}, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSignalBlocks(size, dirOffset, test.blocks, test.rows); err == nil {
				t.Fatal("validateSignalBlocks accepted a corrupt block directory")
			}
		})
	}
}

func TestOpenSignalStoreRejectsCorruptBlockEntry(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenSignalStore[telemetry.Log](dir, "EventUnixNanos")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append("seg-block", []telemetry.Log{{EventUnixNanos: 100, Body: "hello"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "seg-block.fseg")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dirOffset := binary.LittleEndian.Uint64(data[40:48])
	binary.LittleEndian.PutUint32(data[int(dirOffset)+8:], 1<<30)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = OpenSignalStore[telemetry.Log](dir, "EventUnixNanos")
	if err == nil {
		t.Fatal("OpenSignalStore accepted a segment whose block extends past its directory")
	}
	if !strings.Contains(err.Error(), "block extends past the block directory") {
		t.Fatalf("OpenSignalStore error = %v, want the block-extent guard to reject it", err)
	}
}
