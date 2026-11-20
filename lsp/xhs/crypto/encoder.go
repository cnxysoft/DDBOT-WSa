package crypto

import (
	"encoding/base64"
	"strings"
)

// Base64Encoder handles encoding with custom alphabets
type Base64Encoder struct {
	config *CryptoConfig

	// Translation tables
	customEncodeTable [256]byte
	customDecodeTable [256]byte
	x3EncodeTable     [256]byte
	x3DecodeTable     [256]byte
}

// NewBase64Encoder creates a new Base64Encoder
func NewBase64Encoder(config *CryptoConfig) *Base64Encoder {
	enc := &Base64Encoder{config: config}

	// Build custom Base64 translation table
	for i := range 256 {
		enc.customEncodeTable[i] = 0
		enc.customDecodeTable[i] = 0
	}
	for i, c := range config.StandardBase64Alphabet {
		if i < 256 {
			enc.customEncodeTable[byte(c)] = byte(config.CustomBase64Alphabet[i])
			enc.customDecodeTable[byte(config.CustomBase64Alphabet[i])] = byte(c)
		}
	}
	for i, c := range config.StandardBase64Alphabet {
		if i < 256 {
			enc.x3EncodeTable[byte(c)] = byte(config.X3Base64Alphabet[i])
			enc.x3DecodeTable[byte(config.X3Base64Alphabet[i])] = byte(c)
		}
	}

	return enc
}

// encode transforms standard base64 to custom base64
func (e *Base64Encoder) encode(data []byte) []byte {
	result := make([]byte, len(data))
	for i, b := range data {
		if mapped := e.customEncodeTable[b]; mapped != 0 {
			result[i] = mapped
		} else {
			result[i] = b // Passthrough for characters not in table (like =)
		}
	}
	return result
}

// decode transforms custom base64 to standard base64
func (e *Base64Encoder) decode(data []byte) []byte {
	result := make([]byte, len(data))
	for i, b := range data {
		if b == '=' { // Pass through padding character
			result[i] = '='
		} else {
			result[i] = e.customDecodeTable[b&0xFF]
		}
	}
	return result
}

// Encode encodes data using custom Base64 alphabet
func (e *Base64Encoder) Encode(dataToEncode []byte) string {
	standardEncoded := base64.StdEncoding.EncodeToString(dataToEncode)
	return string(e.encode([]byte(standardEncoded)))
}

// StandardEncode returns standard base64 encoding without custom alphabet
func (e *Base64Encoder) StandardEncode(dataToEncode []byte) string {
	return base64.StdEncoding.EncodeToString(dataToEncode)
}

// Decode decodes string using custom Base64 alphabet
func (e *Base64Encoder) Decode(encodedString string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(string(e.decode([]byte(encodedString))))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// encodeX3 transforms standard base64 to X3 base64
func (e *Base64Encoder) encodeX3(data []byte) []byte {
	result := make([]byte, len(data))
	for i, b := range data {
		result[i] = e.x3EncodeTable[b&0xFF]
	}
	return result
}

// decodeX3 transforms X3 base64 to standard base64
func (e *Base64Encoder) decodeX3(data []byte) []byte {
	result := make([]byte, len(data))
	for i, b := range data {
		result[i] = e.x3DecodeTable[b&0xFF]
	}
	return result
}

// EncodeX3 encodes data using X3 Base64 alphabet (no prefix - prefix is added by caller)
func (e *Base64Encoder) EncodeX3(inputBytes []byte) string {
	standardEncoded := base64.StdEncoding.EncodeToString(inputBytes)
	return string(e.encodeX3([]byte(standardEncoded)))
}

// DecodeX3 decodes x3 signature using X3_BASE64_ALPHABET
func (e *Base64Encoder) DecodeX3(encodedString string) ([]byte, error) {
	if strings.HasPrefix(encodedString, e.config.X3Prefix) {
		encodedString = encodedString[len(e.config.X3Prefix):]
	}
	decoded, err := base64.StdEncoding.DecodeString(string(e.decodeX3([]byte(encodedString))))
	if err != nil {
		return nil, err
	}
	return decoded, nil
}