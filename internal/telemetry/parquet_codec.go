package telemetry

import (
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go/compress"
	parquetzstd "github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/parquet-go/parquet-go/format"
)

// parquetCodec compresses every Parquet page Pitot writes. Each page is an
// independent zstd frame of about parquetPageSize bytes, so a 1 MiB window
// covers a whole data page. The library's fastest level defaults to a 4 MiB
// window, and each new encoder then allocates 8 MiB of match history. Pooled
// encoders are dropped at every garbage collection, so ingest rebuilt them
// often enough that their history was most of what a commit allocated.
//
// The fastest level also stores a block's literals raw when it finds few
// matches. Random trace and span IDs are mostly literals, so their hex text
// would be stored at its full width; entropy-coding every literal block halves
// it.
//
// A commit encodes a page per column, and the format's wide schema has many
// columns, so a small file's commit rebuilt encoders dropped by the last
// garbage collection: on a 2,000-row file their history was a fifth of what
// the commit allocated. Enough encoders for the lanes of the files committed
// concurrently are kept outside the pool, about a megabyte each.
var parquetCodec compress.Codec = &pageCodec{level: zstd.SpeedFastest, kept: make(chan *zstd.Encoder, 16)}

// parquetDenseCodec compresses integer count lists, histogram bucket counts,
// at zstd's default level. Their plain pages hold numbers of one or two
// significant bytes in eight-byte slots. Once counts reach the hundreds, the
// fastest level's match finder misses most of their repeats: on cumulative
// exponential histograms of about 90 buckets a point, the default level
// stored a third of the fastest level's bytes, and less than dictionary
// encoding, whose per-row-group dictionary of distinct counts cost more than
// it saved. Counts below 256 compress about as well at either level. Applied
// to every column the default level slowed compaction 1.8 times for 6%
// smaller logs, so only these columns use it.
//
// An encoder of this level allocates larger match tables than the fastest
// level's. A merge rebuilt them after each garbage collection, a fifth of
// what it allocated, so a few are kept outside the pool, one per merge lane.
var parquetDenseCodec compress.Codec = &pageCodec{level: zstd.SpeedDefault, kept: make(chan *zstd.Encoder, 8)}

const pageCodecWindow = 1 << 20

type pageCodec struct {
	level    zstd.EncoderLevel
	kept     chan *zstd.Encoder // encoders that survive garbage collection
	encoders sync.Pool          // *zstd.Encoder
	decoder  parquetzstd.Codec
}

func (c *pageCodec) String() string                            { return "ZSTD" }
func (c *pageCodec) CompressionCodec() format.CompressionCodec { return format.Zstd }

func (c *pageCodec) Encode(dst, src []byte) ([]byte, error) {
	var e *zstd.Encoder
	select {
	case e = <-c.kept:
	default:
		e, _ = c.encoders.Get().(*zstd.Encoder)
	}
	if e == nil {
		var err error
		e, err = zstd.NewWriter(nil,
			zstd.WithEncoderConcurrency(1),
			zstd.WithEncoderLevel(c.level),
			zstd.WithWindowSize(pageCodecWindow),
			zstd.WithLowerEncoderMem(true),
			zstd.WithZeroFrames(true),
			zstd.WithAllLitEntropyCompression(true),
			zstd.WithEncoderCRC(false),
		)
		if err != nil {
			return nil, err
		}
	}
	defer c.put(e)
	return e.EncodeAll(src, dst[:0]), nil
}

func (c *pageCodec) put(e *zstd.Encoder) {
	select {
	case c.kept <- e:
	default:
		c.encoders.Put(e)
	}
}

func (c *pageCodec) Decode(dst, src []byte) ([]byte, error) {
	return c.decoder.Decode(dst, src)
}
