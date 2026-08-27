// Package segment implements Fanout's append-optimized telemetry segments.
// Immutable, checksummed segment files are published through an atomically
// replaced manifest, so a process crash exposes either the old or new commit.
package segment

import (
	"bufio"
	"bytes"
	"container/heap"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/labstack/fanout/internal/telemetry"
	"github.com/zeebo/xxh3"
)

// segmentDecoderMaxMemory bounds what one decompressed segment section may
// claim. A block holds at most rowsPerBlock rows and a section is decoded whole,
// so this is far above any sound file while refusing a corrupt or crafted frame
// long before zstd's 64 GiB default would.
const (
	segmentDecoderMaxMemory   = 128 << 20
	segmentMaxCompressedBytes = segmentDecoderMaxMemory + (1 << 20)
	segmentMaxBlocks          = 1 << 20
)

var segmentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ValidID reports whether id can safely name a durable segment and WAL file.
func ValidID(id string) bool { return segmentIDPattern.MatchString(id) }

// newSegmentDecoder builds a decoder bounded to segmentDecoderMaxMemory. Every
// segment read goes through it, so no on-disk frame can size an allocation.
func newSegmentDecoder() (*zstd.Decoder, error) {
	return newSegmentDecoderWithLimit(segmentDecoderMaxMemory)
}

func newSegmentDecoderWithLimit(limit uint64) (*zstd.Decoder, error) {
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(limit))
}

const (
	segmentMagic   = "FANSEG04"
	segmentVersion = uint32(4)
	headerSize     = 64
	blockDirSize   = 32
	traceEntrySize = 12
	rowsPerBlock   = 2048
)

type Span = telemetry.Span

// ValidateSpanRows rejects rows whose columnar blocks could not be reopened
// within the production decoder budget. It must run before WAL staging so a
// deterministic format error never becomes a boot-blocking WAL.
func ValidateSpanRows(rows []Span) error {
	return validateSpanRowsWithLimit(rows, segmentDecoderMaxMemory)
}

func validateSpanRowsWithLimit(rows []Span, maxBlockBytes uint64) error {
	if maxBlockBytes < columnarHeaderSize {
		return fmt.Errorf("span block budget %d is smaller than its %d-byte header", maxBlockBytes, columnarHeaderSize)
	}
	for start := 0; start < len(rows); start += rowsPerBlock {
		end := min(start+rowsPerBlock, len(rows))
		sizes := spanColumnPlainSizes(rows[start:end])
		total := uint64(columnarHeaderSize)
		for id, size := range sizes {
			if size > maxBlockBytes {
				return fmt.Errorf("span block column %d uses %d bytes; maximum is %d", id, size, maxBlockBytes)
			}
			if size > maxBlockBytes-total {
				return fmt.Errorf("span block uses more than %d bytes", maxBlockBytes)
			}
			total += size
		}
	}
	return nil
}

type Aggregate struct {
	Calls      uint64
	Errors     uint64
	DurationMS float64
}

type blockDir struct {
	offset uint64
	length uint32
	rows   uint32
	min    int64
	max    int64
}

type traceEntry struct {
	hash  uint64
	block uint32
}

type indexCursor struct {
	file      *os.File
	segment   segment
	blockBase uint32
	position  uint32
	entry     traceEntry
}

type indexCursorHeap []*indexCursor

func (h indexCursorHeap) Len() int { return len(h) }
func (h indexCursorHeap) Less(i, j int) bool {
	if h[i].entry.hash != h[j].entry.hash {
		return h[i].entry.hash < h[j].entry.hash
	}
	return h[i].entry.block < h[j].entry.block
}
func (h indexCursorHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *indexCursorHeap) Push(value any) { *h = append(*h, value.(*indexCursor)) }
func (h *indexCursorHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type segment struct {
	path        string
	rows        uint32
	min         int64
	max         int64
	blocks      []blockDir
	indexOffset uint64
	indexCount  uint32
}

type manifest struct {
	NextID uint64   `json:"next_id"`
	Files  []string `json:"files"`
}

// Store is a set of immutable segment files referenced by one atomically
// replaced manifest. Readers see either the old commit or the complete new one.
type Store struct {
	dir      string
	writeMu  sync.Mutex
	mu       sync.RWMutex
	manifest manifest
	segments []segment
	encoder  *zstd.Encoder
	decoders sync.Pool
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create segment directory: %w", err)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("create zstd encoder: %w", err)
	}
	s := &Store{dir: dir, encoder: enc}
	s.decoders.New = func() any {
		dec, decErr := newSegmentDecoder()
		if decErr != nil {
			panic(decErr)
		}
		return dec
	}
	if err := s.loadManifest(); err != nil {
		enc.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.encoder.Close()
}

func (s *Store) loadManifest() error {
	path := filepath.Join(s.dir, "MANIFEST.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.manifest = manifest{NextID: 1}
	} else if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	} else if err := json.Unmarshal(data, &s.manifest); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	// A crash can occur after a segment rename but before the manifest rename.
	// Such an orphan is intentionally invisible, but its numeric name must still
	// advance the allocator so the next append does not collide with it.
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("scan segment directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".fseg") {
			continue
		}
		id, parseErr := strconv.ParseUint(strings.TrimSuffix(name, ".fseg"), 10, 64)
		if parseErr == nil && id >= s.manifest.NextID {
			s.manifest.NextID = id + 1
		}
	}
	for _, name := range s.manifest.Files {
		seg, err := openSegment(filepath.Join(s.dir, name))
		if err != nil {
			return fmt.Errorf("open committed segment %s: %w", name, err)
		}
		s.segments = append(s.segments, seg)
	}
	return nil
}

// Append writes one crash-safe immutable segment and atomically publishes it.
// The on-disk trace index is built in the same pass as block encoding.
func (s *Store) Append(rows []Span) error {
	if len(rows) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	id := s.manifest.NextID
	current := s.manifest
	s.mu.RUnlock()
	name := fmt.Sprintf("%020d.fseg", id)
	tmp := filepath.Join(s.dir, name+".tmp")
	final := filepath.Join(s.dir, name)
	_ = os.Remove(tmp)
	seg, err := s.writeSegment(tmp, rows)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish segment: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}

	next := current
	next.NextID = id + 1
	next.Files = append(append([]string(nil), current.Files...), name)
	if err := writeManifest(s.dir, next); err != nil {
		// The segment is an unreferenced orphan and therefore invisible after a
		// restart. A later compactor can safely collect such files.
		s.mu.Lock()
		s.manifest.NextID = id + 1
		s.mu.Unlock()
		return err
	}
	seg.path = final
	s.mu.Lock()
	s.manifest = next
	s.segments = append(s.segments, seg)
	s.mu.Unlock()
	return nil
}

// AppendID publishes one idempotent span segment for a durable ingest
// transaction. Replaying the same transaction after a crash is a no-op.
func (s *Store) AppendID(id string, rows []Span) error {
	if len(rows) == 0 {
		return nil
	}
	if !segmentIDPattern.MatchString(id) {
		return fmt.Errorf("invalid segment id %q", id)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	name := id + ".fseg"
	s.mu.RLock()
	current := s.manifest
	for _, existing := range current.Files {
		if existing == name {
			s.mu.RUnlock()
			return nil
		}
	}
	s.mu.RUnlock()
	tmp := filepath.Join(s.dir, name+".tmp")
	final := filepath.Join(s.dir, name)
	_ = os.Remove(tmp)
	seg, err := s.writeSegment(tmp, rows)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish segment: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	next := current
	next.Files = append(append([]string(nil), current.Files...), name)
	if err := writeManifest(s.dir, next); err != nil {
		return err
	}
	seg.path = final
	s.mu.Lock()
	s.manifest = next
	s.segments = append(s.segments, seg)
	s.mu.Unlock()
	return nil
}

// CompactOldest rewrites the oldest committed segments as one larger segment.
// The replacement is published with the same atomic-manifest protocol as an
// append; old files are removed only after in-flight readers release the
// snapshot they were using.
func (s *Store) CompactOldest(count int) error {
	if count < 2 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	count = min(count, len(s.segments))
	if count < 2 {
		s.mu.RUnlock()
		return nil
	}
	old := append([]segment(nil), s.segments[:count]...)
	rest := append([]segment(nil), s.segments[count:]...)
	current := s.manifest
	s.mu.RUnlock()
	return s.compactSegments(old, rest, current)
}

// CompactCommitted compacts only raw ingest segments whose batch IDs are in
// the authoritative repository manifest. A partially applied WAL is therefore
// never folded into a replacement that could defeat replay idempotence.
func (s *Store) CompactCommitted(committed map[string]struct{}, maxInputs int) (int, error) {
	if maxInputs < 2 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	current := s.manifest
	selected := make([]segment, 0, maxInputs)
	rest := make([]segment, 0, len(s.segments))
	for _, seg := range s.segments {
		id := strings.TrimSuffix(filepath.Base(seg.path), ".fseg")
		if len(selected) < maxInputs {
			if _, ok := committed[id]; ok {
				selected = append(selected, seg)
				continue
			}
		}
		rest = append(rest, seg)
	}
	s.mu.RUnlock()
	if len(selected) < 2 {
		return 0, nil
	}
	if err := s.compactSegments(selected, rest, current); err != nil {
		return 0, err
	}
	return len(selected), nil
}

// compactSegments publishes a replacement while writeMu is held.
func (s *Store) compactSegments(old, rest []segment, current manifest) error {
	id := current.NextID

	name := fmt.Sprintf("%020d.fseg", id)
	tmp, final := filepath.Join(s.dir, name+".tmp"), filepath.Join(s.dir, name)
	replacement, err := s.writeCompactedSegment(tmp, old)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish compacted segment: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	next := manifest{NextID: id + 1, Files: make([]string, 0, 1+len(rest))}
	next.Files = append(next.Files, name)
	for _, seg := range rest {
		next.Files = append(next.Files, filepath.Base(seg.path))
	}
	if err := writeManifest(s.dir, next); err != nil {
		return err
	}
	replacement.path = final

	s.mu.Lock()
	s.manifest = next
	s.segments = append([]segment{replacement}, rest...)
	var removeErr error
	for _, seg := range old {
		if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
		}
	}
	s.mu.Unlock()
	if removeErr != nil {
		return fmt.Errorf("remove compacted segments: %w", removeErr)
	}
	return syncDir(s.dir)
}

// PruneBefore removes segments whose newest event is older than cutoff. A
// boundary segment is retained intact, so retention never drops a newer row.
func (s *Store) PruneBefore(cutoff int64) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	current := s.manifest
	segments := append([]segment(nil), s.segments...)
	s.mu.RUnlock()
	kept := make([]segment, 0, len(segments))
	removed := make([]segment, 0)
	for _, seg := range segments {
		if seg.max < cutoff {
			removed = append(removed, seg)
		} else {
			kept = append(kept, seg)
		}
	}
	if len(removed) == 0 {
		return 0, nil
	}
	next := current
	next.Files = make([]string, 0, len(kept))
	for _, seg := range kept {
		next.Files = append(next.Files, filepath.Base(seg.path))
	}
	if err := writeManifest(s.dir, next); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.manifest = next
	s.segments = kept
	s.mu.Unlock()
	var removeErr error
	for _, seg := range removed {
		if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
		}
	}
	return len(removed), errors.Join(removeErr, syncDir(s.dir))
}

func (s *Store) writeSegment(path string, rows []Span) (segment, error) {
	if err := ValidateSpanRows(rows); err != nil {
		return segment{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return segment{}, fmt.Errorf("create segment: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(make([]byte, headerSize)); err != nil {
		return segment{}, fmt.Errorf("reserve header: %w", err)
	}

	seg := segment{rows: uint32(len(rows)), min: math.MaxInt64, max: math.MinInt64}
	index := make([]traceEntry, 0, len(rows))
	var offset = uint64(headerSize)
	for blockStart := 0; blockStart < len(rows); blockStart += rowsPerBlock {
		blockEnd := min(blockStart+rowsPerBlock, len(rows))
		blockMin := int64(math.MaxInt64)
		blockMax := int64(math.MinInt64)
		blockTraces := make(map[uint64]struct{}, blockEnd-blockStart)
		for _, row := range rows[blockStart:blockEnd] {
			blockMin = min(blockMin, row.StartUnixNanos)
			blockMax = max(blockMax, row.StartUnixNanos)
			seg.min = min(seg.min, row.StartUnixNanos)
			seg.max = max(seg.max, row.StartUnixNanos)
			traceHash := xxh3.HashString(row.TraceID)
			if _, exists := blockTraces[traceHash]; !exists {
				index = append(index, traceEntry{hash: traceHash, block: uint32(len(seg.blocks))})
				blockTraces[traceHash] = struct{}{}
			}
		}
		encoded, err := encodeColumnarBlock(s.encoder, rows[blockStart:blockEnd])
		if err != nil {
			return segment{}, fmt.Errorf("encode block: %w", err)
		}
		if _, err := f.Write(encoded); err != nil {
			return segment{}, fmt.Errorf("write block: %w", err)
		}
		seg.blocks = append(seg.blocks, blockDir{offset: offset, length: uint32(len(encoded)), rows: uint32(blockEnd - blockStart), min: blockMin, max: blockMax})
		offset += uint64(len(encoded))
	}

	sort.Slice(index, func(i, j int) bool {
		if index[i].hash != index[j].hash {
			return index[i].hash < index[j].hash
		}
		return index[i].block < index[j].block
	})

	dirOffset := offset
	directory := make([]byte, len(seg.blocks)*blockDirSize)
	for i, block := range seg.blocks {
		buf := directory[i*blockDirSize:]
		binary.LittleEndian.PutUint64(buf[0:8], block.offset)
		binary.LittleEndian.PutUint32(buf[8:12], block.length)
		binary.LittleEndian.PutUint32(buf[12:16], block.rows)
		binary.LittleEndian.PutUint64(buf[16:24], uint64(block.min))
		binary.LittleEndian.PutUint64(buf[24:32], uint64(block.max))
	}
	if _, err := f.Write(directory); err != nil {
		return segment{}, fmt.Errorf("write block directory: %w", err)
	}
	indexOffset, _ := f.Seek(0, io.SeekCurrent)
	indexPlain := make([]byte, len(index)*traceEntrySize)
	for i, entry := range index {
		buf := indexPlain[i*traceEntrySize:]
		binary.LittleEndian.PutUint64(buf[0:8], entry.hash)
		binary.LittleEndian.PutUint32(buf[8:12], entry.block)
	}
	if _, err := f.Write(indexPlain); err != nil {
		return segment{}, fmt.Errorf("write trace index: %w", err)
	}
	indexEnd, _ := f.Seek(0, io.SeekCurrent)
	seg.indexOffset = uint64(indexOffset)
	seg.indexCount = uint32(len(index))

	var header [headerSize]byte
	copy(header[0:8], segmentMagic)
	binary.LittleEndian.PutUint32(header[8:12], segmentVersion)
	binary.LittleEndian.PutUint32(header[12:16], seg.rows)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(seg.blocks)))
	binary.LittleEndian.PutUint32(header[20:24], seg.indexCount)
	binary.LittleEndian.PutUint64(header[24:32], uint64(seg.min))
	binary.LittleEndian.PutUint64(header[32:40], uint64(seg.max))
	binary.LittleEndian.PutUint64(header[40:48], dirOffset)
	binary.LittleEndian.PutUint64(header[48:56], uint64(indexOffset))
	binary.LittleEndian.PutUint64(header[56:64], uint64(indexEnd))
	if _, err := f.WriteAt(header[:], 0); err != nil {
		return segment{}, fmt.Errorf("write header: %w", err)
	}
	if err := f.Sync(); err != nil {
		return segment{}, fmt.Errorf("sync segment: %w", err)
	}
	if err := f.Close(); err != nil {
		return segment{}, fmt.Errorf("close segment: %w", err)
	}
	ok = true
	return seg, nil
}

func (s *Store) writeCompactedSegment(path string, inputs []segment) (segment, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return segment{}, fmt.Errorf("create compacted segment: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(make([]byte, headerSize)); err != nil {
		return segment{}, err
	}
	replacement := segment{min: math.MaxInt64, max: math.MinInt64}
	blockBases := make([]uint32, len(inputs))
	var offset = uint64(headerSize)
	for i, input := range inputs {
		source, err := os.Open(input.path)
		if err != nil {
			return segment{}, err
		}
		blockBase := uint32(len(replacement.blocks))
		blockBases[i] = blockBase
		for _, block := range input.blocks {
			if _, err := io.CopyN(f, io.NewSectionReader(source, int64(block.offset), int64(block.length)), int64(block.length)); err != nil {
				_ = source.Close()
				return segment{}, fmt.Errorf("copy compressed block: %w", err)
			}
			replacement.blocks = append(replacement.blocks, blockDir{offset: offset, length: block.length, rows: block.rows, min: block.min, max: block.max})
			offset += uint64(block.length)
		}
		if err := source.Close(); err != nil {
			return segment{}, err
		}
		replacement.rows += input.rows
		replacement.min = min(replacement.min, input.min)
		replacement.max = max(replacement.max, input.max)
	}

	dirOffset := offset
	directory := make([]byte, len(replacement.blocks)*blockDirSize)
	for i, block := range replacement.blocks {
		buf := directory[i*blockDirSize:]
		binary.LittleEndian.PutUint64(buf[0:8], block.offset)
		binary.LittleEndian.PutUint32(buf[8:12], block.length)
		binary.LittleEndian.PutUint32(buf[12:16], block.rows)
		binary.LittleEndian.PutUint64(buf[16:24], uint64(block.min))
		binary.LittleEndian.PutUint64(buf[24:32], uint64(block.max))
	}
	if _, err := f.Write(directory); err != nil {
		return segment{}, err
	}
	indexOffset, _ := f.Seek(0, io.SeekCurrent)
	indexCount, err := mergeTraceIndexes(f, inputs, blockBases)
	if err != nil {
		return segment{}, err
	}
	indexEnd, _ := f.Seek(0, io.SeekCurrent)
	replacement.indexOffset = uint64(indexOffset)
	replacement.indexCount = indexCount
	var header [headerSize]byte
	copy(header[0:8], segmentMagic)
	binary.LittleEndian.PutUint32(header[8:12], segmentVersion)
	binary.LittleEndian.PutUint32(header[12:16], replacement.rows)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(replacement.blocks)))
	binary.LittleEndian.PutUint32(header[20:24], replacement.indexCount)
	binary.LittleEndian.PutUint64(header[24:32], uint64(replacement.min))
	binary.LittleEndian.PutUint64(header[32:40], uint64(replacement.max))
	binary.LittleEndian.PutUint64(header[40:48], dirOffset)
	binary.LittleEndian.PutUint64(header[48:56], uint64(indexOffset))
	binary.LittleEndian.PutUint64(header[56:64], uint64(indexEnd))
	if _, err := f.WriteAt(header[:], 0); err != nil {
		return segment{}, err
	}
	if err := f.Sync(); err != nil {
		return segment{}, err
	}
	if err := f.Close(); err != nil {
		return segment{}, err
	}
	ok = true
	return replacement, nil
}

// mergeTraceIndexes performs a bounded-memory k-way merge of fixed-width,
// sorted on-disk indexes. Compaction therefore scales with file count rather
// than retaining every trace entry from every generation in the Go heap.
func mergeTraceIndexes(dst io.Writer, inputs []segment, blockBases []uint32) (uint32, error) {
	if len(inputs) != len(blockBases) {
		return 0, errors.New("trace-index inputs and block bases disagree")
	}
	cursors := make(indexCursorHeap, 0, len(inputs))
	files := make([]*os.File, 0, len(inputs))
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	var total uint64
	for i, input := range inputs {
		total += uint64(input.indexCount)
		if total > math.MaxUint32 {
			return 0, errors.New("compacted trace index exceeds entry limit")
		}
		if input.indexCount == 0 {
			continue
		}
		f, err := os.Open(input.path)
		if err != nil {
			return 0, err
		}
		files = append(files, f)
		cursor := &indexCursor{file: f, segment: input, blockBase: blockBases[i]}
		entry, err := readTraceEntry(f, input, 0)
		if err != nil {
			_ = f.Close()
			return 0, err
		}
		if entry.block > math.MaxUint32-cursor.blockBase {
			_ = f.Close()
			return 0, errors.New("compacted trace block id overflows")
		}
		entry.block += cursor.blockBase
		cursor.entry = entry
		cursors = append(cursors, cursor)
	}
	heap.Init(&cursors)
	writer := bufio.NewWriterSize(dst, 64<<10)
	var encoded [traceEntrySize]byte
	for cursors.Len() > 0 {
		cursor := heap.Pop(&cursors).(*indexCursor)
		binary.LittleEndian.PutUint64(encoded[0:8], cursor.entry.hash)
		binary.LittleEndian.PutUint32(encoded[8:12], cursor.entry.block)
		if _, err := writer.Write(encoded[:]); err != nil {
			return 0, err
		}
		cursor.position++
		if cursor.position < cursor.segment.indexCount {
			entry, err := readTraceEntry(cursor.file, cursor.segment, cursor.position)
			if err != nil {
				return 0, err
			}
			if entry.block > math.MaxUint32-cursor.blockBase {
				return 0, errors.New("compacted trace block id overflows")
			}
			entry.block += cursor.blockBase
			cursor.entry = entry
			heap.Push(&cursors, cursor)
		}
	}
	if err := writer.Flush(); err != nil {
		return 0, err
	}
	return uint32(total), nil
}

func readTraceEntry(f *os.File, seg segment, position uint32) (traceEntry, error) {
	if position >= seg.indexCount {
		return traceEntry{}, io.EOF
	}
	var encoded [traceEntrySize]byte
	offset := seg.indexOffset + uint64(position)*traceEntrySize
	if _, err := f.ReadAt(encoded[:], int64(offset)); err != nil {
		return traceEntry{}, err
	}
	entry := traceEntry{hash: binary.LittleEndian.Uint64(encoded[0:8]), block: binary.LittleEndian.Uint32(encoded[8:12])}
	if int(entry.block) >= len(seg.blocks) {
		return traceEntry{}, errors.New("trace index references a missing block")
	}
	return entry, nil
}

func traceBlocks(seg segment, hash uint64) ([]uint32, error) {
	f, err := os.Open(seg.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	low, high := uint32(0), seg.indexCount
	for low < high {
		middle := low + (high-low)/2
		entry, err := readTraceEntry(f, seg, middle)
		if err != nil {
			return nil, err
		}
		if entry.hash < hash {
			low = middle + 1
		} else {
			high = middle
		}
	}
	blocks := make([]uint32, 0, 1)
	for position := low; position < seg.indexCount; position++ {
		entry, err := readTraceEntry(f, seg, position)
		if err != nil {
			return nil, err
		}
		if entry.hash != hash {
			break
		}
		if len(blocks) == 0 || blocks[len(blocks)-1] != entry.block {
			blocks = append(blocks, entry.block)
		}
	}
	return blocks, nil
}

// Trace performs a hash-index lookup and decompresses only the blocks that can
// contain the requested trace. The full trace ID is checked after hashing.
func (s *Store) Trace(traceID string) ([]Span, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hash := xxh3.HashString(traceID)
	var out []Span
	for i := range s.segments {
		seg := s.segments[i]
		blocks, err := traceBlocks(seg, hash)
		if err != nil {
			return nil, err
		}
		for _, blockID := range blocks {
			rows, err := s.readTraceBlock(seg, blockID, traceID)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartUnixNanos < out[j].StartUnixNanos })
	return out, nil
}

// ScanService is the deliberately expensive raw path. It demonstrates block
// time pruning and provides a fairer comparison with a general query engine.
func (s *Store) ScanService(namespace, service string, start, end int64) (Aggregate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out Aggregate
	wanted := []int{colNamespace, colServiceName, colStartUnixNanos, colDurationMS, colStatusCode}
	namespaceNeedle, serviceNeedle := []byte(namespace), []byte(service)
	for i := range s.segments {
		seg := s.segments[i]
		if seg.max < start || seg.min >= end {
			continue
		}
		f, err := os.Open(seg.path)
		if err != nil {
			return Aggregate{}, err
		}
		for blockID := range seg.blocks {
			block := seg.blocks[blockID]
			if block.max < start || block.min >= end {
				continue
			}
			columns, err := s.readColumns(f, block, wanted)
			if err != nil {
				_ = f.Close()
				return Aggregate{}, err
			}
			if err := requireFixed(columns[colStartUnixNanos], int(block.rows), 8); err != nil {
				_ = f.Close()
				return Aggregate{}, err
			}
			if err := requireFixed(columns[colDurationMS], int(block.rows), 8); err != nil {
				_ = f.Close()
				return Aggregate{}, err
			}
			namespaceColumn := columns[colNamespace]
			serviceColumn := columns[colServiceName]
			statusColumn := columns[colStatusCode]
			for row := range int(block.rows) {
				namespaceValue, rest, err := consumeByteView(namespaceColumn)
				if err != nil {
					_ = f.Close()
					return Aggregate{}, err
				}
				namespaceColumn = rest
				serviceValue, rest, err := consumeByteView(serviceColumn)
				if err != nil {
					_ = f.Close()
					return Aggregate{}, err
				}
				serviceColumn = rest
				statusValue, rest, err := consumeByteView(statusColumn)
				if err != nil {
					_ = f.Close()
					return Aggregate{}, err
				}
				statusColumn = rest
				timestamp := int64At(columns[colStartUnixNanos], row)
				if timestamp < start || timestamp >= end {
					continue
				}
				if namespace != "" && !bytes.Equal(namespaceValue, namespaceNeedle) {
					continue
				}
				if service != "" && !bytes.Equal(serviceValue, serviceNeedle) {
					continue
				}
				out.Calls++
				out.DurationMS += float64At(columns[colDurationMS], row)
				if isErrorStatus(string(statusValue)) {
					out.Errors++
				}
			}
		}
		if err := f.Close(); err != nil {
			return Aggregate{}, err
		}
	}
	return out, nil
}

func (s *Store) readTraceBlock(seg segment, blockID uint32, traceID string) ([]Span, error) {
	if int(blockID) >= len(seg.blocks) {
		return nil, fmt.Errorf("block %d out of range", blockID)
	}
	block := seg.blocks[blockID]
	f, err := os.Open(seg.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	columns, err := s.readColumns(f, block, allColumns)
	if err != nil {
		return nil, err
	}
	selected, err := matchingStringRows(columns[colTraceID], int(block.rows), []byte(traceID))
	if err != nil {
		return nil, err
	}
	return decodeSelectedBlock(columns, int(block.rows), selected)
}

func (s *Store) readColumns(f *os.File, block blockDir, wanted []int) (map[int][]byte, error) {
	encoded := make([]byte, block.length)
	if _, err := f.ReadAt(encoded, int64(block.offset)); err != nil {
		return nil, err
	}
	dec := s.decoders.Get().(*zstd.Decoder)
	columns, err := decodeColumns(dec, encoded, wanted)
	s.decoders.Put(dec)
	return columns, err
}

// isErrorStatus matches both status spellings telemetry carries: OTLP ingest
// stores Status.Code.String() ("STATUS_CODE_ERROR"), while other producers and
// older rows use the bare code. The DuckDB rollups compare against the same
// pair.
func isErrorStatus(status string) bool {
	return strings.EqualFold(status, "ERROR") || strings.EqualFold(status, "STATUS_CODE_ERROR")
}

// validateSegmentSections bounds every header offset against the file before
// any of them is used to size an allocation. All comparisons are written as
// subtractions against size so a corrupt offset near the top of the address
// space cannot wrap past the check.
func validateSegmentSections(size, dirOffset, indexOffset, indexEnd uint64, blockCount, indexCount uint32) error {
	if blockCount > segmentMaxBlocks {
		return errors.New("segment block count is out of range")
	}
	if err := validateDirectory(size, dirOffset, blockCount, blockDirSize); err != nil {
		return err
	}
	if indexOffset < dirOffset+uint64(blockCount)*blockDirSize || indexOffset > size {
		return errors.New("corrupt trace index offset")
	}
	if indexEnd < indexOffset || indexEnd > size {
		return errors.New("corrupt trace index end")
	}
	if uint64(indexCount) > (indexEnd-indexOffset)/traceEntrySize || indexEnd-indexOffset != uint64(indexCount)*traceEntrySize {
		return errors.New("trace index size disagrees with the segment header")
	}
	if indexEnd != size {
		return errors.New("segment has data past the trace index")
	}
	return nil
}

// validateSegmentBlocks bounds every decoded block entry against the payload
// region, so a torn directory cannot drive a read or an allocation the file
// could never satisfy.
func validateSegmentBlocks(size, dirOffset uint64, blocks []blockDir, rows uint32) error {
	var counted uint64
	for _, block := range blocks {
		if block.offset < headerSize || block.offset > dirOffset {
			return errors.New("block offset outside the segment payload")
		}
		if block.length == 0 || uint64(block.length) > dirOffset-block.offset || uint64(block.length) > segmentMaxCompressedBytes {
			return errors.New("block extends past the block directory")
		}
		if block.rows == 0 || block.rows > rowsPerBlock {
			return errors.New("block row count is out of range")
		}
		counted += uint64(block.rows)
	}
	if counted != uint64(rows) {
		return errors.New("block row counts disagree with the segment header")
	}
	if dirOffset > size {
		return errors.New("block directory starts past the end of the segment")
	}
	return nil
}

// validateDirectory reports whether count fixed-size entries fit in the file
// when placed at offset.
func validateDirectory(size, offset uint64, count uint32, entrySize uint64) error {
	if offset < headerSize || offset > size {
		return errors.New("corrupt directory offset")
	}
	if uint64(count) > (size-offset)/entrySize {
		return errors.New("directory does not fit in the segment")
	}
	return nil
}

func openSegment(path string) (segment, error) {
	f, err := os.Open(path)
	if err != nil {
		return segment{}, err
	}
	defer f.Close()
	var header [headerSize]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return segment{}, err
	}
	if string(header[0:8]) != segmentMagic {
		return segment{}, errors.New("invalid segment magic")
	}
	if binary.LittleEndian.Uint32(header[8:12]) != segmentVersion {
		return segment{}, errors.New("unsupported segment version")
	}
	seg := segment{path: path, rows: binary.LittleEndian.Uint32(header[12:16]), min: int64(binary.LittleEndian.Uint64(header[24:32])), max: int64(binary.LittleEndian.Uint64(header[32:40]))}
	blockCount := binary.LittleEndian.Uint32(header[16:20])
	indexCount := binary.LittleEndian.Uint32(header[20:24])
	dirOffset := binary.LittleEndian.Uint64(header[40:48])
	indexOffset := binary.LittleEndian.Uint64(header[48:56])
	indexEnd := binary.LittleEndian.Uint64(header[56:64])
	info, err := f.Stat()
	if err != nil {
		return segment{}, err
	}
	if err := validateSegmentSections(uint64(info.Size()), dirOffset, indexOffset, indexEnd, blockCount, indexCount); err != nil {
		return segment{}, fmt.Errorf("segment %s: %w", filepath.Base(path), err)
	}
	seg.blocks = make([]blockDir, blockCount)
	buf := make([]byte, int(blockCount)*blockDirSize)
	if _, err := f.ReadAt(buf, int64(dirOffset)); err != nil {
		return segment{}, err
	}
	for i := range seg.blocks {
		b := buf[i*blockDirSize:]
		seg.blocks[i] = blockDir{offset: binary.LittleEndian.Uint64(b[0:8]), length: binary.LittleEndian.Uint32(b[8:12]), rows: binary.LittleEndian.Uint32(b[12:16]), min: int64(binary.LittleEndian.Uint64(b[16:24])), max: int64(binary.LittleEndian.Uint64(b[24:32]))}
	}
	if err := validateSegmentBlocks(uint64(info.Size()), dirOffset, seg.blocks, seg.rows); err != nil {
		return segment{}, fmt.Errorf("segment %s: %w", filepath.Base(path), err)
	}
	seg.indexOffset = indexOffset
	seg.indexCount = indexCount
	if err := validateTraceIndex(f, seg); err != nil {
		return segment{}, fmt.Errorf("segment %s: %w", filepath.Base(path), err)
	}
	return seg, nil
}

func validateTraceIndex(f *os.File, seg segment) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(f, int64(seg.indexOffset), int64(seg.indexCount)*traceEntrySize), 64<<10)
	var encoded [traceEntrySize]byte
	var previous traceEntry
	for position := uint32(0); position < seg.indexCount; position++ {
		if _, err := io.ReadFull(reader, encoded[:]); err != nil {
			return err
		}
		entry := traceEntry{hash: binary.LittleEndian.Uint64(encoded[0:8]), block: binary.LittleEndian.Uint32(encoded[8:12])}
		if int(entry.block) >= len(seg.blocks) {
			return errors.New("trace index references a missing block")
		}
		if position > 0 && (entry.hash < previous.hash || entry.hash == previous.hash && entry.block < previous.block) {
			return errors.New("trace index is not sorted")
		}
		previous = entry
	}
	return nil
}

func appendString(dst []byte, value string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func consumeByteView(src []byte) ([]byte, []byte, error) {
	length, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, nil, errors.New("invalid string length")
	}
	src = src[n:]
	if length > uint64(len(src)) {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return src[:length], src[length:], nil
}

func writeManifest(dir string, m manifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "MANIFEST.json.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "MANIFEST.json")); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// DiskBytes returns the committed segment and manifest size.
func (s *Store) DiskBytes() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for _, name := range append(append([]string(nil), s.manifest.Files...), "MANIFEST.json") {
		info, err := os.Stat(filepath.Join(s.dir, name))
		if errors.Is(err, os.ErrNotExist) && name == "MANIFEST.json" {
			continue
		}
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}

// RowCount returns the number of committed rows.
func (s *Store) RowCount() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total uint64
	for i := range s.segments {
		total += uint64(s.segments[i].rows)
	}
	return total
}

func (s *Store) SegmentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.segments)
}

// EqualSpan compares every persisted field. It is intended for POC recovery
// and format-roundtrip validation, where nil and empty JSON are distinct.
func EqualSpan(a, b Span) bool {
	return reflect.DeepEqual(a, b)
}
