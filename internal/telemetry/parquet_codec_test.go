package telemetry

import (
	"encoding/hex"
	"math/rand"
	"testing"
)

// A page of random hex IDs has four bits of entropy per byte and no repeats
// for the match finder. The codec must entropy-code its literals rather than
// store them at full width.
func TestPageCodecEntropyCodesRandomHex(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	raw := make([]byte, 64<<10)
	r.Read(raw)
	page := []byte(hex.EncodeToString(raw))
	encoded, err := parquetCodec.Encode(nil, page)
	if err != nil {
		t.Fatal(err)
	}
	if ratio := float64(len(encoded)) / float64(len(page)); ratio > 0.6 {
		t.Fatalf("random hex compressed to %.2f of its size, want at most 0.6", ratio)
	}
	decoded, err := parquetCodec.Decode(nil, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(page) {
		t.Fatal("decoded page differs")
	}
}
