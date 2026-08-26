// Column codecs are deliberately private to the versioned segment format.
package segment

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/klauspost/compress/zstd"
)

const (
	colNamespace = iota
	colTraceID
	colSpanID
	colParentSpanID
	colServiceName
	colName
	colKind
	colStartUnixNanos
	colEndUnixNanos
	colDurationMS
	colStatusCode
	colStatusMsg
	colResourceJSON
	colAttributesJSON
	colEventsJSON
	colLinksJSON
	colTraceState
	colFlags
	colScopeName
	colScopeVersion
	colIngestedAt
	colHTTPMethod
	colHTTPStatusCode
	colHTTPRoute
	colDBSystem
	colRPCMethod
	colRPCService
	colPeerService
	colServiceVersion
	colDeploymentEnv
	colExceptionType
	colExceptionMessage
	columnCount
)

const columnarHeaderSize = 4 + columnCount*8

var allColumns = func() []int {
	out := make([]int, columnCount)
	for i := range out {
		out[i] = i
	}
	return out
}()

func encodeColumnarBlock(enc *zstd.Encoder, rows []Span) []byte {
	columns := make([][]byte, columnCount)
	for _, row := range rows {
		columns[colNamespace] = appendString(columns[colNamespace], row.Namespace)
		columns[colTraceID] = appendString(columns[colTraceID], row.TraceID)
		columns[colSpanID] = appendString(columns[colSpanID], row.SpanID)
		columns[colParentSpanID] = appendString(columns[colParentSpanID], row.ParentSpanID)
		columns[colServiceName] = appendString(columns[colServiceName], row.ServiceName)
		columns[colName] = appendString(columns[colName], row.Name)
		columns[colKind] = appendString(columns[colKind], row.Kind)
		columns[colStartUnixNanos] = binary.LittleEndian.AppendUint64(columns[colStartUnixNanos], uint64(row.StartUnixNanos))
		columns[colEndUnixNanos] = binary.LittleEndian.AppendUint64(columns[colEndUnixNanos], uint64(row.EndUnixNanos))
		columns[colDurationMS] = binary.LittleEndian.AppendUint64(columns[colDurationMS], math.Float64bits(row.DurationMS))
		columns[colStatusCode] = appendString(columns[colStatusCode], row.StatusCode)
		columns[colStatusMsg] = appendString(columns[colStatusMsg], row.StatusMsg)
		columns[colResourceJSON] = appendBytes(columns[colResourceJSON], row.ResourceJSON)
		columns[colAttributesJSON] = appendBytes(columns[colAttributesJSON], row.AttributesJSON)
		columns[colEventsJSON] = appendBytes(columns[colEventsJSON], row.EventsJSON)
		columns[colLinksJSON] = appendBytes(columns[colLinksJSON], row.LinksJSON)
		columns[colTraceState] = appendString(columns[colTraceState], row.TraceState)
		columns[colFlags] = binary.LittleEndian.AppendUint32(columns[colFlags], row.Flags)
		columns[colScopeName] = appendString(columns[colScopeName], row.ScopeName)
		columns[colScopeVersion] = appendString(columns[colScopeVersion], row.ScopeVersion)
		columns[colIngestedAt] = binary.LittleEndian.AppendUint64(columns[colIngestedAt], uint64(row.IngestedAt))
		columns[colHTTPMethod] = appendString(columns[colHTTPMethod], row.HTTPMethod)
		columns[colHTTPStatusCode] = appendString(columns[colHTTPStatusCode], row.HTTPStatusCode)
		columns[colHTTPRoute] = appendString(columns[colHTTPRoute], row.HTTPRoute)
		columns[colDBSystem] = appendString(columns[colDBSystem], row.DBSystem)
		columns[colRPCMethod] = appendString(columns[colRPCMethod], row.RPCMethod)
		columns[colRPCService] = appendString(columns[colRPCService], row.RPCService)
		columns[colPeerService] = appendString(columns[colPeerService], row.PeerService)
		columns[colServiceVersion] = appendString(columns[colServiceVersion], row.ServiceVersion)
		columns[colDeploymentEnv] = appendString(columns[colDeploymentEnv], row.DeploymentEnv)
		columns[colExceptionType] = appendString(columns[colExceptionType], row.ExceptionType)
		columns[colExceptionMessage] = appendString(columns[colExceptionMessage], row.ExceptionMessage)
	}

	out := make([]byte, columnarHeaderSize)
	binary.LittleEndian.PutUint32(out[0:4], columnCount)
	offset := columnarHeaderSize
	for id, plain := range columns {
		compressed := enc.EncodeAll(plain, nil)
		entry := out[4+id*8:]
		binary.LittleEndian.PutUint32(entry[0:4], uint32(offset))
		binary.LittleEndian.PutUint32(entry[4:8], uint32(len(compressed)))
		out = append(out, compressed...)
		offset += len(compressed)
	}
	return out
}

func decodeColumns(dec *zstd.Decoder, block []byte, wanted []int) (map[int][]byte, error) {
	if len(block) < columnarHeaderSize {
		return nil, io.ErrUnexpectedEOF
	}
	if got := binary.LittleEndian.Uint32(block[0:4]); got != columnCount {
		return nil, fmt.Errorf("column count: got %d want %d", got, columnCount)
	}
	out := make(map[int][]byte, len(wanted))
	for _, id := range wanted {
		if id < 0 || id >= columnCount {
			return nil, fmt.Errorf("column %d out of range", id)
		}
		entry := block[4+id*8:]
		offset := int(binary.LittleEndian.Uint32(entry[0:4]))
		length := int(binary.LittleEndian.Uint32(entry[4:8]))
		if offset < columnarHeaderSize || length < 0 || offset > len(block)-length {
			return nil, fmt.Errorf("column %d has invalid extent %d+%d", id, offset, length)
		}
		plain, err := dec.DecodeAll(block[offset:offset+length], nil)
		if err != nil {
			return nil, fmt.Errorf("decompress column %d: %w", id, err)
		}
		out[id] = plain
	}
	return out, nil
}

func decodeSelectedBlock(columns map[int][]byte, count int, selected []int) ([]Span, error) {
	if len(selected) == 0 {
		return nil, nil
	}
	for i, row := range selected {
		if row < 0 || row >= count || (i > 0 && selected[i-1] >= row) {
			return nil, errors.New("selected rows must be sorted, unique, and in range")
		}
	}
	stringColumns := []int{
		colNamespace, colTraceID, colSpanID, colParentSpanID, colServiceName,
		colName, colKind, colStatusCode, colStatusMsg, colTraceState,
		colScopeName, colScopeVersion, colHTTPMethod, colHTTPStatusCode,
		colHTTPRoute, colDBSystem, colRPCMethod, colRPCService, colPeerService,
		colServiceVersion, colDeploymentEnv, colExceptionType, colExceptionMessage,
	}
	stringsByColumn := make(map[int][]string, len(stringColumns))
	for _, id := range stringColumns {
		values, err := selectStrings(columns[id], count, selected)
		if err != nil {
			return nil, fmt.Errorf("select string column %d: %w", id, err)
		}
		stringsByColumn[id] = values
	}
	bytesByColumn := make(map[int][][]byte, 4)
	for _, id := range []int{colResourceJSON, colAttributesJSON, colEventsJSON, colLinksJSON} {
		values, err := selectBytes(columns[id], count, selected)
		if err != nil {
			return nil, fmt.Errorf("select bytes column %d: %w", id, err)
		}
		bytesByColumn[id] = values
	}
	for _, fixed := range []struct{ id, width int }{{colStartUnixNanos, 8}, {colEndUnixNanos, 8}, {colDurationMS, 8}, {colFlags, 4}, {colIngestedAt, 8}} {
		if err := requireFixed(columns[fixed.id], count, fixed.width); err != nil {
			return nil, err
		}
	}
	rows := make([]Span, len(selected))
	for i, sourceRow := range selected {
		rows[i] = Span{
			Namespace: stringsByColumn[colNamespace][i], TraceID: stringsByColumn[colTraceID][i], SpanID: stringsByColumn[colSpanID][i],
			ParentSpanID: stringsByColumn[colParentSpanID][i], ServiceName: stringsByColumn[colServiceName][i], Name: stringsByColumn[colName][i], Kind: stringsByColumn[colKind][i],
			StartUnixNanos: int64At(columns[colStartUnixNanos], sourceRow), EndUnixNanos: int64At(columns[colEndUnixNanos], sourceRow), DurationMS: float64At(columns[colDurationMS], sourceRow),
			StatusCode: stringsByColumn[colStatusCode][i], StatusMsg: stringsByColumn[colStatusMsg][i], ResourceJSON: bytesByColumn[colResourceJSON][i],
			AttributesJSON: bytesByColumn[colAttributesJSON][i], EventsJSON: bytesByColumn[colEventsJSON][i], LinksJSON: bytesByColumn[colLinksJSON][i],
			TraceState: stringsByColumn[colTraceState][i], Flags: uint32At(columns[colFlags], sourceRow), ScopeName: stringsByColumn[colScopeName][i],
			ScopeVersion: stringsByColumn[colScopeVersion][i], IngestedAt: int64At(columns[colIngestedAt], sourceRow), HTTPMethod: stringsByColumn[colHTTPMethod][i],
			HTTPStatusCode: stringsByColumn[colHTTPStatusCode][i], HTTPRoute: stringsByColumn[colHTTPRoute][i], DBSystem: stringsByColumn[colDBSystem][i],
			RPCMethod: stringsByColumn[colRPCMethod][i], RPCService: stringsByColumn[colRPCService][i], PeerService: stringsByColumn[colPeerService][i],
			ServiceVersion: stringsByColumn[colServiceVersion][i], DeploymentEnv: stringsByColumn[colDeploymentEnv][i], ExceptionType: stringsByColumn[colExceptionType][i],
			ExceptionMessage: stringsByColumn[colExceptionMessage][i],
		}
	}
	return rows, nil
}

func selectStrings(src []byte, count int, selected []int) ([]string, error) {
	out := make([]string, len(selected))
	target := 0
	for row := 0; row < count && target < len(selected); row++ {
		value, rest, err := consumeByteView(src)
		if err != nil {
			return nil, err
		}
		src = rest
		if row == selected[target] {
			out[target] = string(value)
			target++
		}
	}
	if target != len(selected) {
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}

func selectBytes(src []byte, count int, selected []int) ([][]byte, error) {
	out := make([][]byte, len(selected))
	target := 0
	for row := 0; row < count && target < len(selected); row++ {
		value, rest, err := consumeByteView(src)
		if err != nil {
			return nil, err
		}
		src = rest
		if row == selected[target] {
			out[target] = append([]byte(nil), value...)
			target++
		}
	}
	if target != len(selected) {
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}

func matchingStringRows(src []byte, count int, target []byte) ([]int, error) {
	var out []int
	for row := 0; row < count; row++ {
		value, rest, err := consumeByteView(src)
		if err != nil {
			return nil, err
		}
		src = rest
		if bytes.Equal(value, target) {
			out = append(out, row)
		}
	}
	return out, nil
}

func appendBytes(dst, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func requireFixed(src []byte, count, width int) error {
	if len(src) != count*width {
		return fmt.Errorf("fixed column size: got %d want %d", len(src), count*width)
	}
	return nil
}

func int64At(src []byte, row int) int64 { return int64(binary.LittleEndian.Uint64(src[row*8:])) }
func float64At(src []byte, row int) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(src[row*8:]))
}
func uint32At(src []byte, row int) uint32 { return binary.LittleEndian.Uint32(src[row*4:]) }
