package telemetry

import (
	"io"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
)

// chunkReadAhead bounds the bytes a merge reads ahead of one column chunk's
// pages in a single read.
//
// parquet-go reads a page header through a buffered reader of a fixed size
// and then reads a larger page body straight from the file, so a page cost
// two reads, and every reader held a full buffer even for a chunk of a few
// hundred bytes, as most columns of an ingest file are. A merge reader
// instead reads up to this many bytes of its chunk at once, no more than the
// chunk holds, and serves headers and bodies from them through a small
// buffer (chunkReadBuffer).
const chunkReadAhead = 32 << 10

// chunkReadBuffer is the page reader's buffer over the read-ahead bytes; it
// copies from memory, so it can be small.
const chunkReadBuffer = 4 << 10

// openChunk returns a value reader of a file's column chunk that reads its
// bytes through a chunkWindow.
func openChunk(file io.ReaderAt, chunk parquet.ColumnChunk, meta *format.ColumnMetaData) parquet.ColumnChunkValueReader {
	fc, ok := chunk.(*parquet.FileColumnChunk)
	if !ok {
		return parquet.NewColumnChunkValueReader(chunk)
	}
	start := meta.DataPageOffset
	if meta.DictionaryPageOffset != 0 && meta.DictionaryPageOffset < start {
		start = meta.DictionaryPageOffset
	}
	end := start + meta.TotalCompressedSize
	w := &chunkWindow{file: file, end: end, buf: make([]byte, 0, min(int64(chunkReadAhead), max(end-start, 0)))}
	return parquet.NewColumnChunkValueReader(windowChunk{fc, w})
}

// windowChunk reads its pages through its window.
type windowChunk struct {
	*parquet.FileColumnChunk
	window *chunkWindow
}

func (c windowChunk) Pages() parquet.Pages { return c.FileColumnChunk.PagesFrom(c.window) }

// chunkWindow serves reads of one column chunk from the last bytes it read
// ahead, refilling from the requested offset when a read leaves them.
type chunkWindow struct {
	file io.ReaderAt
	end  int64 // the chunk's end in the file
	off  int64 // the file offset of buf
	buf  []byte
}

func (w *chunkWindow) ReadAt(p []byte, off int64) (int, error) {
	if off >= w.off && off+int64(len(p)) <= w.off+int64(len(w.buf)) {
		return copy(p, w.buf[off-w.off:]), nil
	}
	if len(p) > cap(w.buf) {
		return w.file.ReadAt(p, off)
	}
	n := min(int64(cap(w.buf)), w.end-off)
	if n < int64(len(p)) {
		// Past the chunk: read only what was asked.
		return w.file.ReadAt(p, off)
	}
	w.buf = w.buf[:n]
	read, err := w.file.ReadAt(w.buf, off)
	w.buf, w.off = w.buf[:read], off
	if read >= len(p) {
		return copy(p, w.buf), nil
	}
	copy(p, w.buf)
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return read, err
}
