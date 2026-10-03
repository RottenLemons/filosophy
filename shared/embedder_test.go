package shared

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestDecode16BitWeights(t *testing.T) {
	tests := []struct {
		name  string
		dtype string
		bits  []uint16
		want  []float32
	}{
		{
			name:  "float16 normal and subnormal",
			dtype: "F16",
			bits:  []uint16{0x3c00, 0xc000, 0x0001, 0x7c00},
			want:  []float32{1, -2, float32(math.Ldexp(1, -24)), float32(math.Inf(1))},
		},
		{
			name:  "bfloat16",
			dtype: "BF16",
			bits:  []uint16{0x3f80, 0xc000, 0x0001, 0x7f80},
			want:  []float32{1, -2, math.Float32frombits(0x00010000), float32(math.Inf(1))},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, len(tt.bits)*2)
			for i, bits := range tt.bits {
				binary.LittleEndian.PutUint16(data[i*2:], bits)
			}
			got, err := decode16BitWeights(data, tt.dtype)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d values, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if math.IsInf(float64(tt.want[i]), 0) {
					if got[i] != tt.want[i] {
						t.Errorf("value %d = %v, want %v", i, got[i], tt.want[i])
					}
				} else if math.Abs(float64(got[i]-tt.want[i])) > 1e-12 {
					t.Errorf("value %d = %.10g, want %.10g", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDecode16BitWeightsRejectsInvalidInput(t *testing.T) {
	if _, err := decode16BitWeights([]byte{1}, "F16"); err == nil {
		t.Fatal("expected odd byte length to fail")
	}
	if _, err := decode16BitWeights([]byte{1, 0}, "F64"); err == nil {
		t.Fatal("expected unsupported dtype to fail")
	}
}
