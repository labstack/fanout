// Package segment implements Fanout's append-optimized telemetry segments.
// Immutable, checksummed segment files are published through an atomically
// replaced manifest, so a process crash exposes either the old or new commit.
package segment

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/labstack/fanout/internal/telemetry"
	"github.com/zeebo/xxh3"
)

const (
	segmentMagic   = "FANSEG02"
	segmentVersion = uint32(2)
	headerSize     = 64
	blockDirSize   = 32
	traceEntrySize = 16
	rowsPerBlock   = 2048
	rollupBins     = 32
	rollupWindow   = 5 * time.Minute
)

type Span = telemetry.Span

type Endpoint struct {
	Service   string
	Method    string
	Route     string
	Calls     uint64
	Errors    uint64
	AverageMS float64
	P95MS     float64
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
	row   uint32
}

type rollupKey struct {
	bucket    int64
	namespace string
	service   string
	method    string
	route     string
}

type rollup struct {
	key      rollupKey
	calls    uint64
	errors   uint64
	duration float64
	bins     [rollupBins]uint32
}

type segment struct {
	path       string
	rows       uint32
	min        int64
	max        int64
	blocks     []blockDir
	traceIndex []traceEntry
	rollups    []rollup
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
		dec, decErr := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
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
// Rollups and the trace index are built in the same pass as block encoding.
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
	id := current.NextID
	s.mu.RUnlock()

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
	rollups := make(map[rollupKey]*rollup)
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
			key := rollupKey{
				bucket:    row.StartUnixNanos - row.StartUnixNanos%int64(rollupWindow),
				namespace: row.Namespace, service: row.ServiceName, method: row.HTTPMethod, route: row.HTTPRoute,
			}
			r := rollups[key]
			if r == nil {
				r = &rollup{key: key}
				rollups[key] = r
			}
			r.calls++
			if isErrorStatus(row.StatusCode) {
				r.errors++
			}
			r.duration += row.DurationMS
			r.bins[durationBin(row.DurationMS)]++
		}
		encoded := encodeColumnarBlock(s.encoder, rows[blockStart:blockEnd])
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
		if index[i].block != index[j].block {
			return index[i].block < index[j].block
		}
		return index[i].row < index[j].row
	})
	seg.traceIndex = index
	seg.rollups = make([]rollup, 0, len(rollups))
	for _, r := range rollups {
		seg.rollups = append(seg.rollups, *r)
	}
	sort.Slice(seg.rollups, func(i, j int) bool {
		a, b := seg.rollups[i].key, seg.rollups[j].key
		if a.bucket != b.bucket {
			return a.bucket < b.bucket
		}
		if a.namespace != b.namespace {
			return a.namespace < b.namespace
		}
		if a.service != b.service {
			return a.service < b.service
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.route < b.route
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
		binary.LittleEndian.PutUint32(buf[12:16], entry.row)
	}
	if _, err := f.Write(s.encoder.EncodeAll(indexPlain, nil)); err != nil {
		return segment{}, fmt.Errorf("write trace index: %w", err)
	}
	rollupOffset, _ := f.Seek(0, io.SeekCurrent)
	var rollupPlain bytes.Buffer
	for _, r := range seg.rollups {
		if err := writeRollup(&rollupPlain, r); err != nil {
			return segment{}, err
		}
	}
	if _, err := f.Write(s.encoder.EncodeAll(rollupPlain.Bytes(), nil)); err != nil {
		return segment{}, fmt.Errorf("write rollups: %w", err)
	}

	var header [headerSize]byte
	copy(header[0:8], segmentMagic)
	binary.LittleEndian.PutUint32(header[8:12], segmentVersion)
	binary.LittleEndian.PutUint32(header[12:16], seg.rows)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(seg.blocks)))
	binary.LittleEndian.PutUint32(header[20:24], uint32(len(seg.rollups)))
	binary.LittleEndian.PutUint64(header[24:32], uint64(seg.min))
	binary.LittleEndian.PutUint64(header[32:40], uint64(seg.max))
	binary.LittleEndian.PutUint64(header[40:48], dirOffset)
	binary.LittleEndian.PutUint64(header[48:56], uint64(indexOffset))
	binary.LittleEndian.PutUint64(header[56:64], uint64(rollupOffset))
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
	var offset = uint64(headerSize)
	for _, input := range inputs {
		source, err := os.Open(input.path)
		if err != nil {
			return segment{}, err
		}
		blockBase := uint32(len(replacement.blocks))
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
		for _, entry := range input.traceIndex {
			entry.block += blockBase
			replacement.traceIndex = append(replacement.traceIndex, entry)
		}
		replacement.rollups = append(replacement.rollups, input.rollups...)
		replacement.rows += input.rows
		replacement.min = min(replacement.min, input.min)
		replacement.max = max(replacement.max, input.max)
	}
	sort.Slice(replacement.traceIndex, func(i, j int) bool {
		a, b := replacement.traceIndex[i], replacement.traceIndex[j]
		if a.hash != b.hash {
			return a.hash < b.hash
		}
		if a.block != b.block {
			return a.block < b.block
		}
		return a.row < b.row
	})
	sort.Slice(replacement.rollups, func(i, j int) bool {
		a, b := replacement.rollups[i].key, replacement.rollups[j].key
		if a.bucket != b.bucket {
			return a.bucket < b.bucket
		}
		if a.namespace != b.namespace {
			return a.namespace < b.namespace
		}
		if a.service != b.service {
			return a.service < b.service
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.route < b.route
	})

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
	indexPlain := make([]byte, len(replacement.traceIndex)*traceEntrySize)
	for i, entry := range replacement.traceIndex {
		buf := indexPlain[i*traceEntrySize:]
		binary.LittleEndian.PutUint64(buf[0:8], entry.hash)
		binary.LittleEndian.PutUint32(buf[8:12], entry.block)
		binary.LittleEndian.PutUint32(buf[12:16], entry.row)
	}
	if _, err := f.Write(s.encoder.EncodeAll(indexPlain, nil)); err != nil {
		return segment{}, err
	}
	rollupOffset, _ := f.Seek(0, io.SeekCurrent)
	var rollupPlain bytes.Buffer
	for _, r := range replacement.rollups {
		if err := writeRollup(&rollupPlain, r); err != nil {
			return segment{}, err
		}
	}
	if _, err := f.Write(s.encoder.EncodeAll(rollupPlain.Bytes(), nil)); err != nil {
		return segment{}, err
	}
	var header [headerSize]byte
	copy(header[0:8], segmentMagic)
	binary.LittleEndian.PutUint32(header[8:12], segmentVersion)
	binary.LittleEndian.PutUint32(header[12:16], replacement.rows)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(replacement.blocks)))
	binary.LittleEndian.PutUint32(header[20:24], uint32(len(replacement.rollups)))
	binary.LittleEndian.PutUint64(header[24:32], uint64(replacement.min))
	binary.LittleEndian.PutUint64(header[32:40], uint64(replacement.max))
	binary.LittleEndian.PutUint64(header[40:48], dirOffset)
	binary.LittleEndian.PutUint64(header[48:56], uint64(indexOffset))
	binary.LittleEndian.PutUint64(header[56:64], uint64(rollupOffset))
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

// Trace performs a hash-index lookup and decompresses only the blocks that can
// contain the requested trace. The full trace ID is checked after hashing.
func (s *Store) Trace(traceID string) ([]Span, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hash := xxh3.HashString(traceID)
	var out []Span
	for i := range s.segments {
		seg := &s.segments[i]
		start := sort.Search(len(seg.traceIndex), func(j int) bool { return seg.traceIndex[j].hash >= hash })
		blocks := make(map[uint32][]uint32)
		for j := start; j < len(seg.traceIndex) && seg.traceIndex[j].hash == hash; j++ {
			entry := seg.traceIndex[j]
			blocks[entry.block] = append(blocks[entry.block], entry.row)
		}
		for blockID := range blocks {
			rows, err := s.readTraceBlock(*seg, blockID, traceID)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartUnixNanos < out[j].StartUnixNanos })
	return out, nil
}

// Endpoints answers Fanout's dashboard query entirely from ingestion-time
// rollups. Quantiles use the same bounded-histogram approximation style as the
// existing Fanout endpoint cache.
func (s *Store) Endpoints(namespace, service string, start, end int64, limit int) []Endpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type value struct {
		calls, errors uint64
		duration      float64
		bins          [rollupBins]uint32
	}
	values := make(map[string]*value)
	keys := make(map[string]rollupKey)
	for i := range s.segments {
		seg := &s.segments[i]
		if seg.max < start || seg.min >= end {
			continue
		}
		for _, r := range seg.rollups {
			if r.key.bucket < start-start%int64(rollupWindow) || r.key.bucket >= end {
				continue
			}
			if namespace != "" && r.key.namespace != namespace {
				continue
			}
			if service != "" && r.key.service != service {
				continue
			}
			key := r.key.service + "\x00" + r.key.method + "\x00" + r.key.route
			v := values[key]
			if v == nil {
				v = &value{}
				values[key] = v
				keys[key] = r.key
			}
			v.calls += r.calls
			v.errors += r.errors
			v.duration += r.duration
			for j := range v.bins {
				v.bins[j] += r.bins[j]
			}
		}
	}
	out := make([]Endpoint, 0, len(values))
	for key, v := range values {
		k := keys[key]
		average := 0.0
		if v.calls > 0 {
			average = v.duration / float64(v.calls)
		}
		out = append(out, Endpoint{Service: k.service, Method: k.method, Route: k.route, Calls: v.calls, Errors: v.errors, AverageMS: average, P95MS: histogramQuantile(v.bins, v.calls, .95)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
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

// isErrorStatus matches both status spellings the lake carries: OTLP ingest
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
func validateSegmentSections(size, dirOffset, indexOffset, rollupOffset uint64, blockCount uint32) error {
	if err := validateDirectory(size, dirOffset, blockCount, blockDirSize); err != nil {
		return err
	}
	if indexOffset < dirOffset+uint64(blockCount)*blockDirSize || indexOffset > size {
		return errors.New("corrupt trace index offset")
	}
	if rollupOffset < indexOffset || rollupOffset > size {
		return errors.New("corrupt rollup offset")
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
	rollupCount := binary.LittleEndian.Uint32(header[20:24])
	dirOffset := binary.LittleEndian.Uint64(header[40:48])
	indexOffset := binary.LittleEndian.Uint64(header[48:56])
	rollupOffset := binary.LittleEndian.Uint64(header[56:64])
	info, err := f.Stat()
	if err != nil {
		return segment{}, err
	}
	if err := validateSegmentSections(uint64(info.Size()), dirOffset, indexOffset, rollupOffset, blockCount); err != nil {
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
	indexCompressed := make([]byte, int(rollupOffset-indexOffset))
	if _, err := f.ReadAt(indexCompressed, int64(indexOffset)); err != nil {
		return segment{}, err
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return segment{}, err
	}
	indexBytes, err := dec.DecodeAll(indexCompressed, nil)
	if err != nil {
		dec.Close()
		return segment{}, fmt.Errorf("decode trace index: %w", err)
	}
	if len(indexBytes)%traceEntrySize != 0 {
		dec.Close()
		return segment{}, fmt.Errorf("trace index size %d is not entry-aligned", len(indexBytes))
	}
	seg.traceIndex = make([]traceEntry, len(indexBytes)/traceEntrySize)
	for i := range seg.traceIndex {
		b := indexBytes[i*traceEntrySize:]
		seg.traceIndex[i] = traceEntry{hash: binary.LittleEndian.Uint64(b[0:8]), block: binary.LittleEndian.Uint32(b[8:12]), row: binary.LittleEndian.Uint32(b[12:16])}
	}
	rollupCompressed := make([]byte, info.Size()-int64(rollupOffset))
	if _, err := f.ReadAt(rollupCompressed, int64(rollupOffset)); err != nil {
		dec.Close()
		return segment{}, err
	}
	rollupBytes, err := dec.DecodeAll(rollupCompressed, nil)
	dec.Close()
	if err != nil {
		return segment{}, fmt.Errorf("decode rollups: %w", err)
	}
	reader := bufio.NewReader(bytes.NewReader(rollupBytes))
	seg.rollups = make([]rollup, 0, rollupCount)
	for range rollupCount {
		r, err := readRollup(reader)
		if err != nil {
			return segment{}, err
		}
		seg.rollups = append(seg.rollups, r)
	}
	return seg, nil
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

func writeRollup(w io.Writer, r rollup) error {
	var fixed [160]byte
	binary.LittleEndian.PutUint64(fixed[0:8], uint64(r.key.bucket))
	binary.LittleEndian.PutUint64(fixed[8:16], r.calls)
	binary.LittleEndian.PutUint64(fixed[16:24], r.errors)
	binary.LittleEndian.PutUint64(fixed[24:32], math.Float64bits(r.duration))
	for i, count := range r.bins {
		binary.LittleEndian.PutUint32(fixed[32+i*4:], count)
	}
	if _, err := w.Write(fixed[:]); err != nil {
		return fmt.Errorf("write rollup: %w", err)
	}
	for _, value := range []string{r.key.namespace, r.key.service, r.key.method, r.key.route} {
		if len(value) > math.MaxUint16 {
			return errors.New("rollup key exceeds 65535 bytes")
		}
		var length [2]byte
		binary.LittleEndian.PutUint16(length[:], uint16(len(value)))
		if _, err := w.Write(length[:]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, value); err != nil {
			return err
		}
	}
	return nil
}

func readRollup(r *bufio.Reader) (rollup, error) {
	var fixed [160]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return rollup{}, err
	}
	out := rollup{key: rollupKey{bucket: int64(binary.LittleEndian.Uint64(fixed[0:8]))}, calls: binary.LittleEndian.Uint64(fixed[8:16]), errors: binary.LittleEndian.Uint64(fixed[16:24]), duration: math.Float64frombits(binary.LittleEndian.Uint64(fixed[24:32]))}
	for i := range out.bins {
		out.bins[i] = binary.LittleEndian.Uint32(fixed[32+i*4:])
	}
	for _, target := range []*string{&out.key.namespace, &out.key.service, &out.key.method, &out.key.route} {
		var length [2]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return rollup{}, err
		}
		value := make([]byte, binary.LittleEndian.Uint16(length[:]))
		if _, err := io.ReadFull(r, value); err != nil {
			return rollup{}, err
		}
		*target = string(value)
	}
	return out, nil
}

func durationBin(ms float64) int {
	if ms <= 0 {
		return 0
	}
	bin := int(math.Log2(ms*1000 + 1))
	return min(bin, rollupBins-1)
}

func histogramQuantile(bins [rollupBins]uint32, total uint64, q float64) float64 {
	if total == 0 {
		return 0
	}
	target := uint64(math.Ceil(float64(total) * q))
	var seen uint64
	for i, count := range bins {
		seen += uint64(count)
		if seen >= target {
			return (math.Pow(2, float64(i+1)) - 1) / 1000
		}
	}
	return (math.Pow(2, rollupBins) - 1) / 1000
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
