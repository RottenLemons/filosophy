package shared

import "math"

// QuantizeUint8 converts a float32 embedding vector to uint8 using min-max
// scaling. Returns the quantized values plus scale and offset parameters
// needed for dequantization.
//
// Formula: uint8_val = round((float_val - min) / (max - min) * 255)
func QuantizeUint8(vec []float32) (quantized []uint8, scale float32, offset float32) {
	if len(vec) == 0 {
		return nil, 0, 0
	}

	minVal := vec[0]
	maxVal := vec[0]
	for _, v := range vec[1:] {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	offset = minVal
	rng := maxVal - minVal
	if rng == 0 {
		return make([]uint8, len(vec)), 0, offset
	}
	scale = rng

	quantized = make([]uint8, len(vec))
	for i, v := range vec {
		normalized := (v - minVal) / rng
		quantized[i] = uint8(math.Round(float64(normalized) * 255))
	}
	return quantized, scale, offset
}

// DequantizeUint8 converts a uint8 quantized vector back to float32 using the
// scale and offset produced by QuantizeUint8.
//
// Formula: float_val = uint8_val / 255 * scale + offset
func DequantizeUint8(quantized []uint8, scale float32, offset float32) []float32 {
	if len(quantized) == 0 {
		return nil
	}

	vec := make([]float32, len(quantized))
	if scale == 0 {
		for i := range vec {
			vec[i] = offset
		}
		return vec
	}

	for i, q := range quantized {
		vec[i] = float32(q)/255*scale + offset
	}
	return vec
}
