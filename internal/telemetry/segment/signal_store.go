// Generic signal segments cover logs and metrics; spans add specialized indexes.
package segment

import (
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
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	signalMagic      = "FANSIG03"
	signalVersion    = uint32(3)
	signalHeaderSize = 64
	signalBlockSize  = 32
	signalBlockRows  = 2048
)

var segmentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type signalBlock struct {
	offset uint64
	length uint32
	rows   uint32
	min    int64
	max    int64
}

type signalSegment struct {
	id          string
	path        string
	rows        uint32
	min         int64
	max         int64
	fieldCount  uint32
	fingerprint uint64
	blocks      []signalBlock
}

type signalManifest struct {
	Version uint32   `json:"version"`
	Files   []string `json:"files"`
}

// SignalStore persists one telemetry signal as immutable, independently
// compressed columns. T must be a struct containing only string, []byte,
// int32, uint32, int64, and float64 fields.
type SignalStore[T any] struct {
	dir       string
	timeField int
	codec     structCodec[T]
	writeMu   sync.Mutex
	mu        sync.RWMutex
	manifest  signalManifest
	segments  []signalSegment
	encoder   *zstd.Encoder
	decoders  sync.Pool
}

func OpenSignalStore[T any](dir, timeField string) (*SignalStore[T], error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create signal directory: %w", err)
	}
	codec, err := newStructCodec[T]()
	if err != nil {
		return nil, err
	}
	field, found := codec.typ.FieldByName(timeField)
	if !found || field.Type.Kind() != reflect.Int64 {
		return nil, fmt.Errorf("signal time field %q must be int64", timeField)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		return nil, fmt.Errorf("create signal encoder: %w", err)
	}
	s := &SignalStore[T]{dir: dir, timeField: field.Index[0], codec: codec, encoder: enc}
	s.decoders.New = func() any {
		dec, decErr := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
		if decErr != nil {
			panic(decErr)
		}
		return dec
	}
	if err := s.load(); err != nil {
		enc.Close()
		return nil, err
	}
	return s, nil
}

func (s *SignalStore[T]) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.encoder.Close()
}

func (s *SignalStore[T]) load() error {
	path := filepath.Join(s.dir, "MANIFEST.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.manifest = signalManifest{Version: signalVersion}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read signal manifest: %w", err)
	}
	if err := json.Unmarshal(data, &s.manifest); err != nil {
		return fmt.Errorf("decode signal manifest: %w", err)
	}
	if s.manifest.Version != signalVersion {
		return fmt.Errorf("signal manifest version %d is unsupported; expected %d", s.manifest.Version, signalVersion)
	}
	for _, name := range s.manifest.Files {
		seg, err := openSignalSegment(filepath.Join(s.dir, name))
		if err != nil {
			return fmt.Errorf("open signal segment %s: %w", name, err)
		}
		if seg.fieldCount != uint32(len(s.codec.fields)) || seg.fingerprint != s.codec.fingerprint {
			return fmt.Errorf("signal segment %s schema does not match canonical telemetry row", name)
		}
		s.segments = append(s.segments, seg)
	}
	return nil
}

// Append publishes rows exactly once for id. Replaying a committed transaction
// after a crash is therefore safe.
func (s *SignalStore[T]) Append(id string, rows []T) error {
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
	for _, existing := range s.manifest.Files {
		if existing == name {
			s.mu.RUnlock()
			return nil
		}
	}
	current := s.manifest
	s.mu.RUnlock()

	tmp := filepath.Join(s.dir, name+".tmp")
	final := filepath.Join(s.dir, name)
	_ = os.Remove(tmp)
	seg, err := s.writeSegment(tmp, id, rows)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish signal segment: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	next := current
	next.Version = signalVersion
	next.Files = append(append([]string(nil), current.Files...), name)
	if err := writeSignalManifest(s.dir, next); err != nil {
		return err
	}
	seg.path = final
	s.mu.Lock()
	s.manifest = next
	s.segments = append(s.segments, seg)
	s.mu.Unlock()
	return nil
}

func (s *SignalStore[T]) writeSegment(path, id string, rows []T) (signalSegment, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return signalSegment{}, fmt.Errorf("create signal segment: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(make([]byte, signalHeaderSize)); err != nil {
		return signalSegment{}, err
	}
	seg := signalSegment{id: id, rows: uint32(len(rows)), min: math.MaxInt64, max: math.MinInt64, fieldCount: uint32(len(s.codec.fields)), fingerprint: s.codec.fingerprint}
	offset := uint64(signalHeaderSize)
	for start := 0; start < len(rows); start += signalBlockRows {
		end := min(start+signalBlockRows, len(rows))
		blockMin, blockMax := int64(math.MaxInt64), int64(math.MinInt64)
		for i := start; i < end; i++ {
			ts := reflect.ValueOf(rows[i]).Field(s.timeField).Int()
			blockMin, blockMax = min(blockMin, ts), max(blockMax, ts)
			seg.min, seg.max = min(seg.min, ts), max(seg.max, ts)
		}
		encoded, err := s.codec.encodeBlock(s.encoder, rows[start:end])
		if err != nil {
			return signalSegment{}, err
		}
		if _, err := f.Write(encoded); err != nil {
			return signalSegment{}, err
		}
		seg.blocks = append(seg.blocks, signalBlock{offset: offset, length: uint32(len(encoded)), rows: uint32(end - start), min: blockMin, max: blockMax})
		offset += uint64(len(encoded))
	}
	dirOffset := offset
	directory := make([]byte, len(seg.blocks)*signalBlockSize)
	for i, block := range seg.blocks {
		entry := directory[i*signalBlockSize:]
		binary.LittleEndian.PutUint64(entry[0:8], block.offset)
		binary.LittleEndian.PutUint32(entry[8:12], block.length)
		binary.LittleEndian.PutUint32(entry[12:16], block.rows)
		binary.LittleEndian.PutUint64(entry[16:24], uint64(block.min))
		binary.LittleEndian.PutUint64(entry[24:32], uint64(block.max))
	}
	if _, err := f.Write(directory); err != nil {
		return signalSegment{}, err
	}
	var header [signalHeaderSize]byte
	copy(header[0:8], signalMagic)
	binary.LittleEndian.PutUint32(header[8:12], signalVersion)
	binary.LittleEndian.PutUint32(header[12:16], seg.rows)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(seg.blocks)))
	binary.LittleEndian.PutUint32(header[20:24], uint32(len(s.codec.fields)))
	binary.LittleEndian.PutUint64(header[24:32], uint64(seg.min))
	binary.LittleEndian.PutUint64(header[32:40], uint64(seg.max))
	binary.LittleEndian.PutUint64(header[40:48], dirOffset)
	binary.LittleEndian.PutUint64(header[48:56], s.codec.fingerprint)
	if _, err := f.WriteAt(header[:], 0); err != nil {
		return signalSegment{}, err
	}
	if err := f.Sync(); err != nil {
		return signalSegment{}, err
	}
	if err := f.Close(); err != nil {
		return signalSegment{}, err
	}
	ok = true
	return seg, nil
}

// Scan visits rows in [start,end), using segment and block time pruning.
func (s *SignalStore[T]) Scan(start, end int64, visit func(T) bool) error {
	type openedSegment struct {
		segment signalSegment
		file    *os.File
	}
	var opened []openedSegment
	s.mu.RLock()
	for _, seg := range s.segments {
		if seg.max < start || seg.min >= end {
			continue
		}
		f, err := os.Open(seg.path)
		if err != nil {
			s.mu.RUnlock()
			for _, item := range opened {
				_ = item.file.Close()
			}
			return err
		}
		opened = append(opened, openedSegment{segment: seg, file: f})
	}
	s.mu.RUnlock()
	defer func() {
		for _, item := range opened {
			_ = item.file.Close()
		}
	}()
	dec := s.decoders.Get().(*zstd.Decoder)
	defer s.decoders.Put(dec)
	for _, item := range opened {
		seg, f := item.segment, item.file
		for _, block := range seg.blocks {
			if block.max < start || block.min >= end {
				continue
			}
			data := make([]byte, block.length)
			if _, err := f.ReadAt(data, int64(block.offset)); err != nil {
				return err
			}
			rows, err := s.codec.decodeBlock(dec, data, int(block.rows))
			if err != nil {
				return fmt.Errorf("decode %s: %w", filepath.Base(seg.path), err)
			}
			for _, row := range rows {
				ts := reflect.ValueOf(row).Field(s.timeField).Int()
				if ts >= start && ts < end && !visit(row) {
					return nil
				}
			}
		}
	}
	return nil
}

func (s *SignalStore[T]) RowCount() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total uint64
	for _, seg := range s.segments {
		total += uint64(seg.rows)
	}
	return total
}

func (s *SignalStore[T]) SegmentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.segments)
}

func (s *SignalStore[T]) PruneBefore(cutoff int64) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	current := s.manifest
	segments := append([]signalSegment(nil), s.segments...)
	s.mu.RUnlock()
	kept := make([]signalSegment, 0, len(segments))
	removed := make([]signalSegment, 0)
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
	if err := writeSignalManifest(s.dir, next); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.manifest, s.segments = next, kept
	s.mu.Unlock()
	var removeErr error
	for _, seg := range removed {
		if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
		}
	}
	return len(removed), errors.Join(removeErr, syncDir(s.dir))
}

func openSignalSegment(path string) (signalSegment, error) {
	f, err := os.Open(path)
	if err != nil {
		return signalSegment{}, err
	}
	defer f.Close()
	var header [signalHeaderSize]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return signalSegment{}, err
	}
	if string(header[0:8]) != signalMagic || binary.LittleEndian.Uint32(header[8:12]) != signalVersion {
		return signalSegment{}, errors.New("unsupported signal segment format")
	}
	seg := signalSegment{
		id: filepath.Base(path[:len(path)-len(filepath.Ext(path))]), path: path,
		rows: binary.LittleEndian.Uint32(header[12:16]), fieldCount: binary.LittleEndian.Uint32(header[20:24]),
		min: int64(binary.LittleEndian.Uint64(header[24:32])), max: int64(binary.LittleEndian.Uint64(header[32:40])),
		fingerprint: binary.LittleEndian.Uint64(header[48:56]),
	}
	count := int(binary.LittleEndian.Uint32(header[16:20]))
	dirOffset := int64(binary.LittleEndian.Uint64(header[40:48]))
	if count < 0 || dirOffset < signalHeaderSize {
		return signalSegment{}, errors.New("invalid signal directory")
	}
	directory := make([]byte, count*signalBlockSize)
	if _, err := f.ReadAt(directory, dirOffset); err != nil {
		return signalSegment{}, err
	}
	for i := range count {
		entry := directory[i*signalBlockSize:]
		seg.blocks = append(seg.blocks, signalBlock{
			offset: binary.LittleEndian.Uint64(entry[0:8]), length: binary.LittleEndian.Uint32(entry[8:12]), rows: binary.LittleEndian.Uint32(entry[12:16]),
			min: int64(binary.LittleEndian.Uint64(entry[16:24])), max: int64(binary.LittleEndian.Uint64(entry[24:32])),
		})
	}
	return seg, nil
}

func writeSignalManifest(dir string, manifest signalManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "MANIFEST.json.tmp")
	final := filepath.Join(dir, "MANIFEST.json")
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
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDir(dir)
}

type fieldKind uint8

const (
	fieldString fieldKind = iota
	fieldBytes
	fieldInt64
	fieldInt32
	fieldUint32
	fieldFloat64
)

type codecField struct {
	name  string
	index int
	kind  fieldKind
}

type structCodec[T any] struct {
	typ         reflect.Type
	fields      []codecField
	fingerprint uint64
}

func newStructCodec[T any]() (structCodec[T], error) {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.Kind() != reflect.Struct {
		return structCodec[T]{}, errors.New("signal row must be a struct")
	}
	c := structCodec[T]{typ: typ}
	var fp uint64 = 1469598103934665603
	for i := range typ.NumField() {
		field := typ.Field(i)
		var kind fieldKind
		switch {
		case field.Type.Kind() == reflect.String:
			kind = fieldString
		case field.Type == reflect.TypeOf([]byte(nil)):
			kind = fieldBytes
		case field.Type.Kind() == reflect.Int64:
			kind = fieldInt64
		case field.Type.Kind() == reflect.Int32:
			kind = fieldInt32
		case field.Type.Kind() == reflect.Uint32:
			kind = fieldUint32
		case field.Type.Kind() == reflect.Float64:
			kind = fieldFloat64
		default:
			return structCodec[T]{}, fmt.Errorf("unsupported field %s (%s)", field.Name, field.Type)
		}
		c.fields = append(c.fields, codecField{name: field.Name, index: i, kind: kind})
		for _, b := range []byte(field.Name + ":" + field.Type.String()) {
			fp ^= uint64(b)
			fp *= 1099511628211
		}
	}
	c.fingerprint = fp
	return c, nil
}

func (c structCodec[T]) encodeBlock(enc *zstd.Encoder, rows []T) ([]byte, error) {
	columns := make([][]byte, len(c.fields))
	for _, row := range rows {
		value := reflect.ValueOf(row)
		for i, field := range c.fields {
			v := value.Field(field.index)
			switch field.kind {
			case fieldString:
				columns[i] = appendBytes(columns[i], []byte(v.String()))
			case fieldBytes:
				columns[i] = appendBytes(columns[i], v.Bytes())
			case fieldInt64:
				columns[i] = binary.LittleEndian.AppendUint64(columns[i], uint64(v.Int()))
			case fieldInt32:
				columns[i] = binary.LittleEndian.AppendUint32(columns[i], uint32(v.Int()))
			case fieldUint32:
				columns[i] = binary.LittleEndian.AppendUint32(columns[i], uint32(v.Uint()))
			case fieldFloat64:
				columns[i] = binary.LittleEndian.AppendUint64(columns[i], math.Float64bits(v.Float()))
			}
		}
	}
	headerSize := 4 + len(columns)*8
	out := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(out[:4], uint32(len(columns)))
	offset := headerSize
	for i, plain := range columns {
		compressed := enc.EncodeAll(plain, nil)
		entry := out[4+i*8:]
		binary.LittleEndian.PutUint32(entry[:4], uint32(offset))
		binary.LittleEndian.PutUint32(entry[4:8], uint32(len(compressed)))
		out = append(out, compressed...)
		offset += len(compressed)
	}
	return out, nil
}

func (c structCodec[T]) decodeBlock(dec *zstd.Decoder, block []byte, count int) ([]T, error) {
	headerSize := 4 + len(c.fields)*8
	if len(block) < headerSize || int(binary.LittleEndian.Uint32(block[:4])) != len(c.fields) {
		return nil, errors.New("invalid signal block header")
	}
	columns := make([][]byte, len(c.fields))
	for i := range c.fields {
		entry := block[4+i*8:]
		offset := int(binary.LittleEndian.Uint32(entry[:4]))
		length := int(binary.LittleEndian.Uint32(entry[4:8]))
		if offset < headerSize || length < 0 || offset > len(block)-length {
			return nil, errors.New("invalid signal column extent")
		}
		plain, err := dec.DecodeAll(block[offset:offset+length], nil)
		if err != nil {
			return nil, err
		}
		columns[i] = plain
	}
	rows := make([]T, count)
	for fieldIndex, field := range c.fields {
		column := columns[fieldIndex]
		for row := range count {
			dst := reflect.ValueOf(&rows[row]).Elem().Field(field.index)
			switch field.kind {
			case fieldString, fieldBytes:
				value, rest, err := consumeByteView(column)
				if err != nil {
					return nil, err
				}
				column = rest
				if field.kind == fieldString {
					dst.SetString(string(value))
				} else {
					dst.SetBytes(append([]byte(nil), value...))
				}
			case fieldInt64, fieldFloat64:
				if len(column) < 8 {
					return nil, io.ErrUnexpectedEOF
				}
				bits := binary.LittleEndian.Uint64(column[:8])
				column = column[8:]
				if field.kind == fieldInt64 {
					dst.SetInt(int64(bits))
				} else {
					dst.SetFloat(math.Float64frombits(bits))
				}
			case fieldInt32, fieldUint32:
				if len(column) < 4 {
					return nil, io.ErrUnexpectedEOF
				}
				bits := binary.LittleEndian.Uint32(column[:4])
				column = column[4:]
				if field.kind == fieldInt32 {
					dst.SetInt(int64(int32(bits)))
				} else {
					dst.SetUint(uint64(bits))
				}
			}
		}
		if len(column) != 0 {
			return nil, fmt.Errorf("column %s has trailing data", field.name)
		}
	}
	return rows, nil
}
