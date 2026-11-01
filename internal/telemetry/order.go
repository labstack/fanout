package telemetry

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"slices"
	"strings"
	"unsafe"

	"github.com/parquet-go/parquet-go"
)

// Order is a signal's row order. Ingest sorts each batch by it, compaction
// merges sorted inputs by it, and offline verification checks it. Ties keep
// their input order.
type Order []Part

// Part is one component of a sort key: a stored column's value or, with
// Bucket, that integer value floored to a multiple of Bucket. Buckets let the
// order name time buckets that no column stores.
type Part struct {
	Column string
	Bucket int64
}

// claim is the footer's sorting_columns. Parquet can declare only stored
// columns, so an order with a bucket part claims nothing; it is still
// enforced by the writer, compaction and verification.
func (o Order) claim() []string {
	var columns []string
	for _, p := range o {
		if p.Bucket != 0 {
			return nil
		}
		columns = append(columns, p.Column)
	}
	return columns
}

// sortKey is one row's position in its signal's order. Part i fills n[i]
// with an integer mapped to an unsigned value that sorts the same way, or
// with a text part's rank among a batch's distinct values. Text without a
// rank fills its s slot, referencing the row's string or the bytes of a
// borrowed Parquet page.
type sortKey struct {
	n [6]uint64
	s [3]string
}

// keyPart is a compiled Part: the slots it fills and the reader that feeds it.
type keyPart struct {
	column int // position in the order's distinct columns
	index  int // n slot, the part's position in the order
	text   bool
	signed bool
	slot   int // s slot of a text part
	bucket int64
}

// keyOrder is a compiled Order.
type keyOrder struct {
	columns []string // distinct stored columns, read once each
	parts   []keyPart
}

func compileOrder(s *Signal) keyOrder {
	var o keyOrder
	var texts int
	if len(s.Order) > len(sortKey{}.n) {
		panic(fmt.Sprintf("%s order has more parts than a sort key", s.Name))
	}
	for i, p := range s.Order {
		c, ok := s.Column(p.Column)
		if !ok || !c.Stored() {
			panic(fmt.Sprintf("%s order column %s is not stored", s.Name, p.Column))
		}
		column := slices.Index(o.columns, p.Column)
		if column < 0 {
			column = len(o.columns)
			o.columns = append(o.columns, p.Column)
		}
		part := keyPart{column: column, index: i, bucket: p.Bucket}
		switch c.Type {
		case String:
			part.text, part.slot = true, texts
			texts++
		case Int64, Time:
			part.signed = true
		case Uint64:
		default:
			panic(fmt.Sprintf("%s order column %s has no sortable type", s.Name, p.Column))
		}
		if p.Bucket < 0 || p.Bucket > 0 && !part.signed || texts > len(sortKey{}.s) {
			panic(fmt.Sprintf("%s order part %s does not fit a sort key", s.Name, p.Column))
		}
		o.parts = append(o.parts, part)
	}
	return o
}

// integer maps a column value to its order-preserving unsigned key.
func (p keyPart) integer(v int64) uint64 {
	if p.bucket > 0 {
		q := v / p.bucket
		if v%p.bucket < 0 {
			q--
		}
		v = q
	}
	if p.signed {
		return uint64(v) ^ 1<<63
	}
	return uint64(v)
}

func (o *keyOrder) compare(a, b *sortKey) int {
	for _, p := range o.parts {
		var c int
		if p.text {
			c = strings.Compare(a.s[p.slot], b.s[p.slot])
		} else {
			c = cmp.Compare(a.n[p.index], b.n[p.index])
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// set fills a key part from a Parquet value. Text borrows the value's bytes,
// which stay valid until its reader moves past the page.
func (o *keyOrder) set(key *sortKey, part keyPart, v parquet.Value) {
	if part.text {
		b := v.ByteArray()
		key.s[part.slot] = unsafe.String(unsafe.SliceData(b), len(b))
		return
	}
	key.n[part.index] = part.integer(v.Int64())
}

// maxRanked bounds the distinct values of a text part that sortRows ranks.
// Namespaces, services and metric names fall under it; per-row IDs do not,
// and are compared as text.
const maxRanked = 4096

// sortRows returns the permutation that puts rows in the signal's order and
// every row's key, indexed by input position. The rows are not moved. Text
// parts with few distinct values are replaced by their rank, so most
// comparisons are integer comparisons.
func sortRows[R any](s *Signal, rows []R) ([]int32, []sortKey) {
	keys := make([]sortKey, len(rows))
	var texts []keyPart // text parts compared as text
	for _, p := range s.order.parts {
		c, _ := s.Column(s.order.columns[p.column])
		switch get := c.get.(type) {
		case func(*R) string:
			if !rankText(rows, keys, p, get) {
				for i := range rows {
					keys[i].s[p.slot] = get(&rows[i])
				}
				texts = append(texts, p)
			}
		case func(*R) int64:
			for i := range rows {
				keys[i].n[p.index] = p.integer(get(&rows[i]))
			}
		case func(*R) uint64:
			for i := range rows {
				keys[i].n[p.index] = p.integer(int64(get(&rows[i])))
			}
		default:
			panic(fmt.Sprintf("%s order column %s has no accessor for %T", s.Name, c.Name, *new(R)))
		}
	}
	perm := make([]int32, len(rows))
	for i := range perm {
		perm[i] = int32(i)
	}
	parts := len(s.order.parts)
	if len(texts) == 0 && radixSort(keys, parts, perm) {
		return perm, keys
	}
	compare := func(a, b int32) int {
		x, y := &keys[a], &keys[b]
		for i := range parts {
			if x.n[i] != y.n[i] {
				return cmp.Compare(x.n[i], y.n[i])
			}
		}
		return cmp.Compare(a, b)
	}
	if len(texts) > 0 {
		compare = func(a, b int32) int {
			x, y := &keys[a], &keys[b]
			for _, p := range s.order.parts {
				var c int
				if p.text && slices.Contains(texts, p) {
					c = strings.Compare(x.s[p.slot], y.s[p.slot])
				} else {
					c = cmp.Compare(x.n[p.index], y.n[p.index])
				}
				if c != 0 {
					return c
				}
			}
			return cmp.Compare(a, b)
		}
	}
	slices.SortFunc(perm, compare)
	return perm, keys
}

// radixSort sorts perm by the keys' integer parts when, each offset by its
// minimum, they pack into 128 bits, and reports whether they did. Least
// significant 16-bit digits come first and every pass is stable, so ties keep
// their input order, as the comparison sort's index tiebreak does.
func radixSort(keys []sortKey, parts int, perm []int32) bool {
	if len(keys) < 2 {
		return true
	}
	var low [len(sortKey{}.n)]uint64
	var width [len(sortKey{}.n)]int
	total := 0
	for p := range parts {
		lo, hi := keys[0].n[p], keys[0].n[p]
		for i := range keys {
			lo, hi = min(lo, keys[i].n[p]), max(hi, keys[i].n[p])
		}
		low[p], width[p] = lo, bits.Len64(hi-lo)
		total += width[p]
	}
	if total > 128 {
		return false
	}
	// packed[i] is row i's key, its first part most significant.
	type packed struct{ hi, lo uint64 }
	key := make([]packed, len(keys))
	for i := range keys {
		var k packed
		for p := range parts {
			w := width[p]
			v := keys[i].n[p] - low[p]
			switch {
			case w == 0:
			case w == 64:
				k = packed{k.lo, v}
			default:
				k = packed{k.hi<<w | k.lo>>(64-w), k.lo<<w | v}
			}
		}
		key[i] = k
	}
	digit := func(k packed, pass int) int {
		if pass < 4 {
			return int(k.lo >> (16 * pass) & 0xffff)
		}
		return int(k.hi >> (16 * (pass - 4)) & 0xffff)
	}
	src, dst := perm, make([]int32, len(perm))
	count := make([]int32, 1<<16)
	for pass := 0; pass*16 < total; pass++ {
		clear(count)
		for _, row := range src {
			count[digit(key[row], pass)]++
		}
		// A digit shared by every row leaves the order as it is.
		if int(count[digit(key[src[0]], pass)]) == len(src) {
			continue
		}
		var sum int32
		for d, n := range count {
			count[d] = sum
			sum += n
		}
		for _, row := range src {
			d := digit(key[row], pass)
			dst[count[d]] = row
			count[d]++
		}
		src, dst = dst, src
	}
	copy(perm, src)
	return true
}

// rankText fills a text part's n slot with each row's rank among the batch's
// distinct values, or reports false when there are more than maxRanked.
// Rows of one resource share their strings, so a value equal in identity to
// the previous row's skips the map.
func rankText[R any](rows []R, keys []sortKey, p keyPart, get func(*R) string) bool {
	ranks := map[string]uint64{}
	var last string
	var rank uint64
	for i := range rows {
		v := get(&rows[i])
		if i == 0 || len(v) != len(last) || unsafe.StringData(v) != unsafe.StringData(last) {
			r, ok := ranks[v]
			if !ok {
				if len(ranks) == maxRanked {
					return false
				}
				r = uint64(len(ranks))
				ranks[v] = r
			}
			last, rank = v, r
		}
		keys[i].n[p.index] = rank
	}
	// Ranks were assigned in first-seen order; remap them to sorted order.
	values := make([]string, len(ranks))
	for v, r := range ranks {
		values[r] = v
	}
	order := make([]uint64, len(values))
	for i := range order {
		order[i] = uint64(i)
	}
	slices.SortFunc(order, func(a, b uint64) int { return strings.Compare(values[a], values[b]) })
	sorted := make([]uint64, len(values))
	for position, r := range order {
		sorted[r] = uint64(position)
	}
	for i := range keys {
		keys[i].n[p.index] = sorted[keys[i].n[p.index]]
	}
	return true
}

// keyCursor reads the sort keys of one row group in file order. Keys borrow
// their readers' pages, and each reader advances only after the current key
// has been consumed, so no per-row copy is made.
type keyCursor struct {
	order     *keyOrder
	input     int
	readers   []parquet.ColumnChunkValueReader
	buffers   [][256]parquet.Value
	pos, n    []int
	remaining int64
	key       sortKey
}

func newKeyCursor(s *Signal, input int, group parquet.RowGroup) *keyCursor {
	o := &s.order
	c := &keyCursor{order: o, input: input, remaining: group.NumRows(),
		readers: make([]parquet.ColumnChunkValueReader, len(o.columns)), buffers: make([][256]parquet.Value, len(o.columns)),
		pos: make([]int, len(o.columns)), n: make([]int, len(o.columns))}
	for i, name := range o.columns {
		leaf, _ := s.schema.Lookup(name)
		c.readers[i] = parquet.NewColumnChunkValueReader(group.ColumnChunks()[leaf.ColumnIndex])
	}
	return c
}

func (c *keyCursor) close() (err error) {
	for i, r := range c.readers {
		if r != nil {
			err = errors.Join(err, r.Close())
			c.readers[i] = nil
		}
	}
	clear(c.buffers)
	c.key = sortKey{}
	return err
}

func (c *keyCursor) next() (bool, error) {
	if c.remaining == 0 {
		return false, c.close()
	}
	for i, r := range c.readers {
		if c.pos[i] == c.n[i] {
			clear(c.buffers[i][:])
			n, e := r.ReadValues(c.buffers[i][:])
			c.pos[i], c.n[i] = 0, n
			if e != nil && e != io.EOF {
				return false, e
			}
			if n == 0 {
				if e == io.EOF {
					return false, io.ErrUnexpectedEOF
				}
				return false, io.ErrNoProgress
			}
		}
	}
	for _, p := range c.order.parts {
		c.order.set(&c.key, p, c.buffers[p.column][c.pos[p.column]])
	}
	for i := range c.pos {
		c.pos[i]++
	}
	c.remaining--
	return true, nil
}

type keyHeap []*keyCursor

func (h keyHeap) Len() int { return len(h) }
func (h keyHeap) Less(i, j int) bool {
	if c := h[i].order.compare(&h[i].key, &h[j].key); c != 0 {
		return c < 0
	}
	return h[i].input < h[j].input
}
func (h keyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *keyHeap) Push(x any)   { *h = append(*h, x.(*keyCursor)) }
func (h *keyHeap) Pop() any     { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }

// verifyOrder decodes a file's key columns and checks that its rows follow
// the signal's order across every row group.
func verifyOrder(path string, s *Signal, expected int) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	file, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return err
	}
	var previous sortKey
	var rows int64
	for i, group := range file.RowGroups() {
		c := newKeyCursor(s, i, group)
		for {
			ok, err := c.next()
			if err != nil {
				return errors.Join(err, c.close())
			}
			if !ok {
				break
			}
			if rows > 0 && s.order.compare(&previous, &c.key) > 0 {
				return errors.Join(fmt.Errorf("%s row %d is out of order", s.Name, rows), c.close())
			}
			previous = c.key
			for slot := range previous.s {
				previous.s[slot] = strings.Clone(previous.s[slot])
			}
			rows++
		}
	}
	if rows != int64(expected) {
		return fmt.Errorf("decoded %d rows; metadata declares %d", rows, expected)
	}
	return nil
}
