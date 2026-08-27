package telemetry

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	traceIndexMagic      = "FANIDX02"
	traceIndexHeaderSize = 16
	traceIndexEntrySize  = 24
)

type traceRange struct {
	hash  uint64
	row   uint64
	count uint64
}

// traceIndex keeps only fixed-size index metadata in memory. Entries stay in
// the sidecar and are located with binary search, so retained trace cardinality
// does not become retained Go heap.
type traceIndex struct {
	path    string
	entries uint64
}

type traceIndexWriter struct {
	file    *os.File
	path    string
	last    traceRange
	rows    uint64
	entries uint64
	have    bool
	closed  bool
}

func newTraceIndexWriter(path string) (*traceIndexWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(make([]byte, traceIndexHeaderSize)); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &traceIndexWriter{file: file, path: path}, nil
}

// Append adds one Parquet row hash. Input must be ordered by hash, which lets
// even very large compactions build the fixed-width index in constant memory.
func (w *traceIndexWriter) Append(hash uint64) error {
	if w.closed {
		return errors.New("append to closed trace index")
	}
	row := w.rows
	w.rows++
	if !w.have {
		w.last = traceRange{hash: hash, row: row, count: 1}
		w.have = true
		return nil
	}
	if hash < w.last.hash {
		return errors.New("trace index input is not sorted by hash")
	}
	if hash == w.last.hash {
		w.last.count++
		return nil
	}
	if err := w.flush(); err != nil {
		return err
	}
	w.last = traceRange{hash: hash, row: row, count: 1}
	w.have = true
	return nil
}

func (w *traceIndexWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.have {
		if err := w.flush(); err != nil {
			return w.fail(err)
		}
	}
	var header [traceIndexHeaderSize]byte
	copy(header[:], traceIndexMagic)
	binary.LittleEndian.PutUint64(header[8:], w.entries)
	if _, err := w.file.WriteAt(header[:], 0); err != nil {
		return w.fail(err)
	}
	if err := w.file.Sync(); err != nil {
		return w.fail(err)
	}
	if err := w.file.Close(); err != nil {
		_ = os.Remove(w.path)
		return err
	}
	return nil
}

func (w *traceIndexWriter) Abort() {
	if w.closed {
		return
	}
	w.closed = true
	_ = w.file.Close()
	_ = os.Remove(w.path)
}

func (w *traceIndexWriter) flush() error {
	var encoded [traceIndexEntrySize]byte
	binary.LittleEndian.PutUint64(encoded[0:8], w.last.hash)
	binary.LittleEndian.PutUint64(encoded[8:16], w.last.row)
	binary.LittleEndian.PutUint64(encoded[16:24], w.last.count)
	if _, err := w.file.Write(encoded[:]); err != nil {
		return err
	}
	w.entries++
	w.have = false
	return nil
}

func (w *traceIndexWriter) fail(err error) error {
	_ = w.file.Close()
	_ = os.Remove(w.path)
	return err
}

func writeTraceIndex(path string, rows []spanParquetRow) error {
	writer, err := newTraceIndexWriter(path)
	if err != nil {
		return err
	}
	for i := range rows {
		if err := writer.Append(rows[i].TraceHash); err != nil {
			writer.Abort()
			return err
		}
	}
	return writer.Close()
}

func loadTraceIndex(path string, spanCount int) (index traceIndex, err error) {
	file, err := os.Open(path)
	if err != nil {
		return traceIndex{}, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return traceIndex{}, err
	}
	if !info.Mode().IsRegular() {
		return traceIndex{}, errors.New("trace index is not a regular file")
	}
	var header [traceIndexHeaderSize]byte
	if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:8]) != traceIndexMagic {
		return traceIndex{}, errors.New("invalid trace index header")
	}
	count := binary.LittleEndian.Uint64(header[8:])
	if count > uint64((math.MaxInt64-traceIndexHeaderSize)/traceIndexEntrySize) ||
		info.Size() != traceIndexHeaderSize+int64(count)*traceIndexEntrySize {
		return traceIndex{}, errors.New("invalid trace index length")
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	var encoded [traceIndexEntrySize]byte
	var previous traceRange
	for i := uint64(0); i < count; i++ {
		if _, err := io.ReadFull(reader, encoded[:]); err != nil {
			return traceIndex{}, err
		}
		entry := decodeTraceRange(encoded[:])
		if entry.count == 0 || entry.row > uint64(spanCount) || entry.count > uint64(spanCount)-entry.row {
			return traceIndex{}, errors.New("trace index entry exceeds span file")
		}
		if i == 0 && entry.row != 0 {
			return traceIndex{}, errors.New("trace index does not start at row zero")
		}
		if i > 0 && (previous.hash >= entry.hash || previous.row+previous.count != entry.row) {
			return traceIndex{}, errors.New("trace index is not strictly sorted and contiguous")
		}
		previous = entry
	}
	if count > 0 {
		if previous.row+previous.count != uint64(spanCount) {
			return traceIndex{}, fmt.Errorf("trace index covers %d of %d span rows", previous.row+previous.count, spanCount)
		}
	} else if spanCount != 0 {
		return traceIndex{}, errors.New("trace index is empty for a non-empty span file")
	}
	return traceIndex{path: path, entries: count}, nil
}

func (index traceIndex) Lookup(hash uint64) (match traceRange, found bool, err error) {
	if index.entries == 0 {
		return traceRange{}, false, nil
	}
	file, err := os.Open(index.path)
	if err != nil {
		return traceRange{}, false, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	low, high := uint64(0), index.entries
	for low < high {
		middle := low + (high-low)/2
		entry, readErr := readTraceRangeAt(file, middle)
		if readErr != nil {
			return traceRange{}, false, readErr
		}
		if entry.hash < hash {
			low = middle + 1
		} else {
			high = middle
		}
	}
	if low == index.entries {
		return traceRange{}, false, nil
	}
	entry, err := readTraceRangeAt(file, low)
	if err != nil {
		return traceRange{}, false, err
	}
	return entry, entry.hash == hash, nil
}

func readTraceRangeAt(file *os.File, position uint64) (traceRange, error) {
	var encoded [traceIndexEntrySize]byte
	offset := int64(traceIndexHeaderSize) + int64(position)*traceIndexEntrySize
	if _, err := file.ReadAt(encoded[:], offset); err != nil {
		return traceRange{}, err
	}
	return decodeTraceRange(encoded[:]), nil
}

func decodeTraceRange(encoded []byte) traceRange {
	return traceRange{
		hash:  binary.LittleEndian.Uint64(encoded[0:8]),
		row:   binary.LittleEndian.Uint64(encoded[8:16]),
		count: binary.LittleEndian.Uint64(encoded[16:24]),
	}
}
