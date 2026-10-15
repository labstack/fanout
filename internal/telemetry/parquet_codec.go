package telemetry

import (
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go/compress"
	parquetzstd "github.com/parquet-go/parquet-go/compress/zstd"
	"github.com/parquet-go/parquet-go/format"
)

// parquetCodec compresses every Parquet page Fanout writes. Each page is an
// independent zstd frame of about parquetPageSize bytes, so a 1 MiB window
// covers a whole data page. The library's fastest level defaults to a 4 MiB
// window, and each new encoder then allocates 8 MiB of match history. Pooled
// encoders are dropped at every garbage collection, so ingest rebuilt them
// often enough that their history was most of what a commit allocated.
var parquetCodec compress.Codec = &pageCodec{}

const pageCodecWindow = 1 << 20

type pageCodec struct {
	encoders sync.Pool // *zstd.Encoder
	decoder  parquetzstd.Codec
}

func (c *pageCodec) String() string                            { return "ZSTD" }
func (c *pageCodec) CompressionCodec() format.CompressionCodec { return format.Zstd }

func (c *pageCodec) Encode(dst, src []byte) ([]byte, error) {
	e, _ := c.encoders.Get().(*zstd.Encoder)
	if e == nil {
		var err error
		e, err = zstd.NewWriter(nil,
			zstd.WithEncoderConcurrency(1),
			zstd.WithEncoderLevel(zstd.SpeedFastest),
			zstd.WithWindowSize(pageCodecWindow),
			zstd.WithLowerEncoderMem(true),
			zstd.WithZeroFrames(true),
			zstd.WithEncoderCRC(false),
		)
		if err != nil {
			return nil, err
		}
	}
	defer c.encoders.Put(e)
	return e.EncodeAll(src, dst[:0]), nil
}

func (c *pageCodec) Decode(dst, src []byte) ([]byte, error) {
	return c.decoder.Decode(dst, src)
}
