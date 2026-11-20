package crypto

import "encoding/hex"

// BitOperations provides bit manipulation operations for signature generation
type BitOperations struct {
	config *CryptoConfig
}

// NewBitOperations creates a new BitOperations instance
func NewBitOperations(config *CryptoConfig) *BitOperations {
	return &BitOperations{config: config}
}

// NormalizeTo32bit normalizes a value to 32-bit
func (b *BitOperations) NormalizeTo32bit(value uint32) uint32 {
	return value & b.config.Max32Bit
}

// ToSigned32bit converts an unsigned 32-bit value to a signed 32-bit value
func (b *BitOperations) ToSigned32bit(unsignedValue uint32) int32 {
	v := uint64(unsignedValue)
	if v > 0x7FFFFFFF {
		return int32(v - 0x100000000)
	}
	return int32(v)
}

// ComputeSeedValue computes the seed value transformation
func (b *BitOperations) ComputeSeedValue(seed32bit uint32) int32 {
	normalizedSeed := b.NormalizeTo32bit(seed32bit)

	shift15bits := normalizedSeed >> 15
	shift13bits := normalizedSeed >> 13
	shift12bits := normalizedSeed >> 12
	shift10bits := normalizedSeed >> 10

	xorMaskedResult := (shift15bits &^ shift13bits) | (shift13bits &^ shift15bits)
	shiftedResult := ((xorMaskedResult ^ shift12bits ^ shift10bits) << 31) & b.config.Max32Bit

	return b.ToSigned32bit(shiftedResult)
}

// XorTransformArray performs XOR transformation on a byte array using HEX_KEY
func (b *BitOperations) XorTransformArray(sourceIntegers []byte) []byte {
	resultBytes := make([]byte, len(sourceIntegers))
	keyBytes, err := hex.DecodeString(b.config.HexKey)
	if err != nil {
		return sourceIntegers
	}
	keyLength := len(keyBytes)

	for index := range sourceIntegers {
		if index < keyLength {
			resultBytes[index] = sourceIntegers[index] ^ keyBytes[index]
		} else {
			resultBytes[index] = sourceIntegers[index]
		}
	}

	return resultBytes
}