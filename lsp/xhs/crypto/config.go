package crypto

import "encoding/json"

// CryptoConfig holds all cryptographic constants from xhshow Python library
type CryptoConfig struct {
	// Bitwise operation constants
	Max32Bit       uint32
	MaxSigned32Bit int32
	MaxByte        uint8

	// Base64 encoding alphabets
	StandardBase64Alphabet string
	CustomBase64Alphabet   string
	X3Base64Alphabet      string

	// XOR key for payload transformation (144 bytes)
	HexKey string

	// Payload construction constants
	VersionBytes     []int
	PayloadLength    int
	A1Length         int
	AppIDLength      int
	Md5XorLength     int
	A3Prefix         []int
	TimestampLELen   int

	// Random value ranges
	SequenceValueMin        int
	SequenceValueMax        int
	WindowPropsLengthMin    int
	WindowPropsLengthMax    int

	SessionWindowPropsInitMin int
	SessionWindowPropsInitMax int

	SessionSequenceInitMin int
	SessionSequenceInitMax int
	SessionSequenceStepMin int
	SessionSequenceStepMax int
	SessionWindowPropsStepMin int
	SessionWindowPropsStepMax int

	// Checksum constants
	ChecksumVersion  int
	ChecksumXorKey   int
	ChecksumFixedTail []int

	// Environment detection constants
	EnvTable        []int
	EnvChecksDefault []int

	// Hash IV
	HashIV [4]uint32

	// Environment fingerprint generation parameters
	EnvFingerprintXorKey      int
	EnvFingerprintTimeOffsetMin int
	EnvFingerprintTimeOffsetMax int

	// Signature data template
	SignatureDataTemplate map[string]string

	// Prefix constants
	X3Prefix string
	XYSPrefix string

	// Trace ID generation constants
	HexChars               string
	XrayTraceIdSeqMax      int
	XrayTraceIdTimestampShift int
	XrayTraceIdPart1Len    int
	XrayTraceIdPart2Len    int
	B3TraceIdLength        int

	// b1 secret key
	B1SecretKey string

	// Canvas hash for fingerprint
	CanvasHash string

	// Signature x-s-common template
	SignatureXSCommonTemplate map[string]interface{}

	// Public user agent
	PublicUserAgent string
}

// DefaultCryptoConfig returns a new CryptoConfig with default values
func DefaultCryptoConfig() *CryptoConfig {
	cfg := &CryptoConfig{
		Max32Bit:               0xFFFFFFFF,
		MaxSigned32Bit:         0x7FFFFFFF,
		MaxByte:                255,
		StandardBase64Alphabet: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/",
		CustomBase64Alphabet:   "ZmserbBoHQtNP+wOcza/LpngG8yJq42KWYj0DSfdikx3VT16IlUAFM97hECvuRX5",
		X3Base64Alphabet:      "MfgqrsbcyzPQRStuvC7mn501HIJBo2DEFTKdeNOwxWXYZap89+/A4UVLhijkl63G",
		HexKey: "71a302257793271ddd273bcee3e4b98d9d7935e1da33f5765e2ea8afb6dc77a51a499d23b67c20660025860cbf13d4540d92497f58686c574e508f46e1956344f39139bf4faf22a3eef120b79258145b2feb5193b6478669961298e79bedca646e1a693a926154a5a7a1bd1cf0dedb742f917a747a1e388b234f2277516db7116035439730fa61e9822a0eca7bff72d8",

		PayloadLength:  144,
		A1Length:       52,
		AppIDLength:    10,
		Md5XorLength:   8,
		TimestampLELen: 8,

		SequenceValueMin:     15,
		SequenceValueMax:     50,
		WindowPropsLengthMin: 1000,
		WindowPropsLengthMax: 1200,

		SessionWindowPropsInitMin: 1000,
		SessionWindowPropsInitMax: 2000,
		SessionSequenceInitMin: 15,
		SessionSequenceInitMax: 17,
		SessionSequenceStepMin: 0,
		SessionSequenceStepMax: 1,
		SessionWindowPropsStepMin: 1,
		SessionWindowPropsStepMax: 10,

		ChecksumVersion: 1,
		ChecksumXorKey:   115,
		ChecksumFixedTail: []int{249, 65, 103, 103, 201, 181, 131, 99, 94, 7, 68, 250, 132, 21},

		EnvTable:        []int{115, 248, 83, 102, 103, 201, 181, 131, 99, 94, 4, 68, 250, 132, 21},
		EnvChecksDefault: []int{0, 1, 18, 1, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0, 0},

		HashIV: [4]uint32{1831565813, 461845907, 2246822507, 3266489909},

		EnvFingerprintXorKey:      41,
		EnvFingerprintTimeOffsetMin: 10,
		EnvFingerprintTimeOffsetMax: 50,

		X3Prefix: "mns0301_",
		XYSPrefix: "XYS_",

		HexChars:               "abcdef0123456789",
		XrayTraceIdSeqMax:      8388607,
		XrayTraceIdTimestampShift: 23,
		XrayTraceIdPart1Len:    16,
		XrayTraceIdPart2Len:    16,
		B3TraceIdLength:        16,

		B1SecretKey: "xhswebmplfbt",
		CanvasHash: "742cc32c",

		PublicUserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36 Edg/142.0.0.0",
	}

	cfg.VersionBytes = []int{121, 104, 96, 41}
	cfg.A3Prefix = []int{2, 97, 51, 16}

	cfg.SignatureDataTemplate = map[string]string{
		"x0": "4.2.6",
		"x1": "xhs-pc-web",
		"x2": "Windows",
		"x3": "",
		"x4": "",
	}

	cfg.SignatureXSCommonTemplate = map[string]interface{}{
		"s0":   5,
		"s1":   "",
		"x0":   "1",
		"x1":   "4.3.3",
		"x2":   "Windows",
		"x3":   "xhs-pc-web",
		"x4":   "4.86.0",
		"x5":   "",
		"x6":   "",
		"x7":   "",
		"x8":   "",
		"x9":   -596800761,
		"x10":  0,
		"x11":  "normal",
	}

	return cfg
}

// SignData represents the JSON structure for XYS signature
type SignData struct {
	X0 string `json:"x0"`
	X1 string `json:"x1"`
	X2 string `json:"x2"`
	X3 string `json:"x3"`
	X4 string `json:"x4"`
}

// SignCommonData represents the JSON structure for x-s-common
type SignCommonData struct {
	S0  int         `json:"s0"`
	S1  string      `json:"s1"`
	X0  string      `json:"x0"`
	X1  string      `json:"x1"`
	X2  string      `json:"x2"`
	X3  string      `json:"x3"`
	X4  string      `json:"x4"`
	X5  string      `json:"x5"`
	X6  string      `json:"x6"`
	X7  string      `json:"x7"`
	X8  string      `json:"x8"`
	X9  int         `json:"x9"`
	X10 int         `json:"x10"`
	X11 string      `json:"x11"`
}

// MarshalJSON serializes SignCommonData for x-s-common
func (s *SignCommonData) MarshalJSON() ([]byte, error) {
	type Alias SignCommonData
	return json.Marshal((*Alias)(s))
}