package embedding

import (
	"crypto/sha256"
	"encoding/binary"
)

const Dimension = 8

func FromText(text string) []float64 {
	sum := sha256.Sum256([]byte(text))
	out := make([]float64, Dimension)
	for i := range out {
		segment := binary.BigEndian.Uint32(sum[i*4 : i*4+4])
		out[i] = float64(segment%1000) / 1000.0
	}
	return out
}
