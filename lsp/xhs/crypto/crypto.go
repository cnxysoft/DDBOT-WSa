package crypto

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// CryptoProcessor handles the main signature generation logic
type CryptoProcessor struct {
	config     *CryptoConfig
	bitOps     *BitOperations
	b64Encoder *Base64Encoder
}

type b1Fingerprint struct {
	X33 string `json:"x33"`
	X34 string `json:"x34"`
	X35 string `json:"x35"`
	X36 string `json:"x36"`
	X37 string `json:"x37"`
	X38 string `json:"x38"`
	X39 int    `json:"x39"`
	X42 string `json:"x42"`
	X43 string `json:"x43"`
	X44 string `json:"x44"`
	X45 string `json:"x45"`
	X46 string `json:"x46"`
	X48 string `json:"x48"`
	X49 string `json:"x49"`
	X50 string `json:"x50"`
	X51 string `json:"x51"`
	X52 string `json:"x52"`
	X82 string `json:"x82"`
}

// NewCryptoProcessor creates a new CryptoProcessor
func NewCryptoProcessor(config *CryptoConfig) *CryptoProcessor {
	return &CryptoProcessor{
		config:     config,
		bitOps:     NewBitOperations(config),
		b64Encoder: NewBase64Encoder(config),
	}
}

// B64Encoder returns the base64 encoder (for debugging)
func (cp *CryptoProcessor) B64Encoder() *Base64Encoder {
	return cp.b64Encoder
}

func (cp *CryptoProcessor) newB1Fingerprint(x36 int, x44 int64) b1Fingerprint {
	return b1Fingerprint{
		X33: "0",
		X34: "0",
		X35: "0",
		X36: fmt.Sprintf("%d", x36),
		X37: "0|0|0|0|0|0|0|0|0|1|0|0|0|0|0|0|0|0|1|0|0|0|0|0",
		X38: "0|0|1|0|1|0|0|0|0|0|1|0|1|0|1|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0",
		X39: 0,
		X42: "3.4.4",
		X43: cp.config.CanvasHash,
		X44: fmt.Sprintf("%d", x44),
		X45: "__SEC_CAV__1-1-1-1-1|__SEC_WSA__|",
		X46: "false",
		X48: "",
		X49: "{list:[],type:}",
		X50: "",
		X51: "",
		X52: "",
		X82: "_0x17a2|_0x1954",
	}
}

// DebugGenerateB1 generates the raw b1 fingerprint (before Base64 encoding) for debugging
func (cp *CryptoProcessor) DebugGenerateB1(a1Value string, cookies map[string]string) string {
	canvasHash := cp.config.CanvasHash

	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	x36 := r.Intn(20) + 1

	x44 := time.Now().UnixMilli()

	fp := map[string]interface{}{
		"x33": "0",
		"x34": "0",
		"x35": "0",
		"x36": fmt.Sprintf("%d", x36),
		"x37": "0|0|0|0|0|0|0|0|0|1|0|0|0|0|0|0|0|0|1|0|0|0|0|0",
		"x38": "0|0|1|0|1|0|0|0|0|0|1|0|1|0|1|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0",
		"x39": 0,
		"x42": "3.4.4",
		"x43": canvasHash,
		"x44": fmt.Sprintf("%d", x44),
		"x45": "__SEC_CAV__1-1-1-1-1|__SEC_WSA__|",
		"x46": "false",
		"x48": "",
		"x49": "{list:[],type:}",
		"x50": "",
		"x51": "",
		"x52": "",
		"x82": "_0x17a2|_0x1954",
	}

	fpJSON, _ := json.Marshal(fp)
	fpStr := string(fpJSON)

	key := []byte(cp.config.B1SecretKey)
	ciphertext := cp.rc4Encrypt(key, []byte(fpStr))
	urlEncoded := urlEncodeBytes(ciphertext)
	parsed := parseURLEncoded(urlEncoded)

	// Return raw base64-encoded bytes (before x-s-common encoding)
	return string(cp.b64Encoder.Encode(parsed))
}

// intToLEBytes converts an integer to a little-endian byte array
func (cp *CryptoProcessor) intToLEBytes(val uint64, length int) []byte {
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		result[i] = byte(val & 0xFF)
		val >>= 8
	}
	return result
}

// setURILength sets the URI length field in the payload array at offset 36 (bytes 36-39)
// This field is set to 0 as a placeholder during BuildPayloadArray when no session is provided
func (cp *CryptoProcessor) setURILength(payload []byte, uriLength int) []byte {
	if len(payload) >= 40 {
		uriLenBytes := cp.intToLEBytes(uint64(uriLength), 4)
		copy(payload[36:40], uriLenBytes)
	}
	return payload
}

// rotateLeft performs 32-bit left rotation
func (cp *CryptoProcessor) rotateLeft(val uint32, n uint) uint32 {
	return ((val << n) | (val >> (32 - n))) & cp.config.Max32Bit
}

// customHashV2 implements the custom hash function for x-s generation
// Input: byte list (must be multiple of 8)
// Output: 16-byte list
func (cp *CryptoProcessor) customHashV2(inputBytes []byte) []byte {
	hashIV := cp.config.HashIV
	s0 := hashIV[0]
	s1 := hashIV[1]
	s2 := hashIV[2]
	s3 := hashIV[3]
	length := len(inputBytes)

	s0 ^= uint32(length)
	s1 ^= uint32(length) << 8
	s2 ^= uint32(length) << 16
	s3 ^= uint32(length) << 24

	for i := 0; i < length/8; i++ {
		v0 := binary.LittleEndian.Uint32(inputBytes[i*8 : i*8+4])
		v1 := binary.LittleEndian.Uint32(inputBytes[i*8+4 : i*8+8])

		s0 = cp.rotateLeft(((s0+v0)&cp.config.Max32Bit)^s2, 7)
		s1 = cp.rotateLeft(((v0^s1)+s3)&cp.config.Max32Bit, 11)
		s2 = cp.rotateLeft(((s2+v1)&cp.config.Max32Bit)^s0, 13)
		s3 = cp.rotateLeft(((s3^v1)+s1)&cp.config.Max32Bit, 17)
	}

	t0 := s0 ^ uint32(length)
	t1 := s1 ^ t0
	t2 := (s2 + t1) & cp.config.Max32Bit
	t3 := s3 ^ t2

	rot_t0 := cp.rotateLeft(t0, 9)
	rot_t1 := cp.rotateLeft(t1, 13)
	rot_t2 := cp.rotateLeft(t2, 17)
	rot_t3 := cp.rotateLeft(t3, 19)

	s0 = (rot_t0 + rot_t2) & cp.config.Max32Bit
	s1 = rot_t1 ^ rot_t3
	s2 = (rot_t2 + s0) & cp.config.Max32Bit
	s3 = rot_t3 ^ s1

	result := make([]byte, 16)
	binary.LittleEndian.PutUint32(result[0:4], s0)
	binary.LittleEndian.PutUint32(result[4:8], s1)
	binary.LittleEndian.PutUint32(result[8:12], s2)
	binary.LittleEndian.PutUint32(result[12:16], s3)
	return result
}

// randIntInclusive matches Python's random.randint(min, max) behavior.
func randIntInclusive(r *rand.Rand, min int, max int) int {
	if max <= min {
		return min
	}
	return r.Intn(max-min+1) + min
}

// BuildPayloadArray builds the 144-byte payload array for signing
func (cp *CryptoProcessor) BuildPayloadArray(
	hexParameter string,
	hexMD5Path string,
	a1Value string,
	appIdentifier string,
	stringParam string,
	timestamp float64,
	signState *SignState,
) []byte {
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	seed := r.Uint32()

	if signState != nil {
		return cp.buildPayloadArrayWithInputs(hexParameter, hexMD5Path, a1Value, appIdentifier, stringParam, timestamp, signState, seed, 0, 0, 0)
	}

	timeOffset := randIntInclusive(r, cp.config.EnvFingerprintTimeOffsetMin, cp.config.EnvFingerprintTimeOffsetMax)
	sequenceValue := randIntInclusive(r, cp.config.SequenceValueMin, cp.config.SequenceValueMax)
	windowPropsLength := randIntInclusive(r, cp.config.WindowPropsLengthMin, cp.config.WindowPropsLengthMax)

	return cp.buildPayloadArrayWithInputs(hexParameter, hexMD5Path, a1Value, appIdentifier, stringParam, timestamp, nil, seed, timeOffset, sequenceValue, windowPropsLength)
}

func (cp *CryptoProcessor) buildPayloadArrayWithInputs(
	hexParameter string,
	hexMD5Path string,
	a1Value string,
	appIdentifier string,
	stringParam string,
	timestamp float64,
	signState *SignState,
	seed uint32,
	timeOffset int,
	sequenceValue int,
	windowPropsLength int,
) []byte {
	seedByte := byte(seed & 0xFF)

	payload := make([]byte, 0, cp.config.PayloadLength)

	// Version bytes
	payload = append(payload, byte(cp.config.VersionBytes[0]))
	payload = append(payload, byte(cp.config.VersionBytes[1]))
	payload = append(payload, byte(cp.config.VersionBytes[2]))
	payload = append(payload, byte(cp.config.VersionBytes[3]))

	// Seed (4 bytes LE)
	payload = append(payload, cp.intToLEBytes(uint64(seed), 4)...)

	// Timestamp (8 bytes LE, milliseconds)
	tsBytes := cp.intToLEBytes(uint64(timestamp*1000), cp.config.TimestampLELen)
	payload = append(payload, tsBytes...)

	if signState != nil {
		payload = append(payload, cp.intToLEBytes(uint64(signState.PageLoadTimestamp), cp.config.TimestampLELen)...)
		payload = append(payload, cp.intToLEBytes(uint64(signState.SequenceValue), 4)...)
		payload = append(payload, cp.intToLEBytes(uint64(signState.WindowPropsLength), 4)...)
		payload = append(payload, cp.intToLEBytes(uint64(signState.URILength), 4)...)
	} else {
		effectiveTsMs := int64((timestamp - float64(timeOffset)) * 1000)
		payload = append(payload, cp.intToLEBytes(uint64(effectiveTsMs), cp.config.TimestampLELen)...)
		payload = append(payload, cp.intToLEBytes(uint64(sequenceValue), 4)...)
		payload = append(payload, cp.intToLEBytes(uint64(windowPropsLength), 4)...)
		payload = append(payload, cp.intToLEBytes(uint64(len([]byte(stringParam))), 4)...)
	}

	// MD5 hash XOR with seed byte (first 8 bytes)
	md5Bytes, _ := hex.DecodeString(hexParameter)
	for i := 0; i < cp.config.Md5XorLength && i < len(md5Bytes); i++ {
		payload = append(payload, md5Bytes[i]^seedByte)
	}

	// A1 value (52 bytes max)
	a1Bytes := []byte(a1Value)
	if len(a1Bytes) > cp.config.A1Length {
		a1Bytes = a1Bytes[:cp.config.A1Length]
	}
	payload = append(payload, byte(cp.config.A1Length))
	for _, b := range a1Bytes {
		payload = append(payload, b)
	}
	for i := len(a1Bytes); i < cp.config.A1Length; i++ {
		payload = append(payload, 0)
	}

	// App identifier (10 bytes)
	appBytes := []byte(appIdentifier)
	if len(appBytes) > cp.config.AppIDLength {
		appBytes = appBytes[:cp.config.AppIDLength]
	}
	payload = append(payload, byte(cp.config.AppIDLength))
	for _, b := range appBytes {
		payload = append(payload, b)
	}
	for i := len(appBytes); i < cp.config.AppIDLength; i++ {
		payload = append(payload, 0)
	}

	// Part 11
	part11 := []byte{1, byte(seedByte ^ byte(cp.config.EnvTable[0]))}
	for i := 1; i < 15; i++ {
		part11 = append(part11, byte(cp.config.EnvTable[i]^cp.config.EnvChecksDefault[i]))
	}
	payload = append(payload, part11...)

	// A3 prefix + hash
	md5PathBytes, _ := hex.DecodeString(hexMD5Path)
	a3Part := make([]byte, 4)
	a3Part[0] = byte(cp.config.A3Prefix[0])
	a3Part[1] = byte(cp.config.A3Prefix[1])
	a3Part[2] = byte(cp.config.A3Prefix[2])
	a3Part[3] = byte(cp.config.A3Prefix[3])

	hashInput := append(tsBytes, md5PathBytes...)
	hashResult := cp.customHashV2(hashInput)
	for _, b := range hashResult {
		a3Part = append(a3Part, b^seedByte)
	}
	payload = append(payload, a3Part...)

	return payload
}

// BuildSignature builds the complete XYS signature
func (cp *CryptoProcessor) BuildSignature(
	method string,
	uri string,
	a1Value string,
	xsecAppID string,
	params map[string]string,
	payload map[string]interface{},
	timestamp float64,
	session *SessionManager,
) (string, error) {
	contentString := cp.buildContentString(method, uri, params, payload)
	dValue := cp.md5Hash(contentString)

	var signState *SignState
	if session != nil {
		signState = session.GetCurrentState(uri)
	}

	mValue := dValue
	if method == "POST" {
		mValue = cp.md5Hash(uri)
	}

	payloadArray := cp.BuildPayloadArray(dValue, mValue, a1Value, xsecAppID, contentString, timestamp, signState)
	xorResult := cp.bitOps.XorTransformArray(payloadArray)

	x3Signature := cp.b64Encoder.EncodeX3(xorResult[:cp.config.PayloadLength])

	signatureData := SignData{
		X0: "4.2.6",
		X1: xsecAppID,
		X2: "Windows",
		X3: cp.config.X3Prefix + x3Signature,
		X4: "",
	}

	signatureJSON, err := json.Marshal(signatureData)
	if err != nil {
		return "", err
	}

	return cp.config.XYSPrefix + cp.b64Encoder.Encode(signatureJSON), nil
}

// buildContentString builds the content string for MD5 calculation
func (cp *CryptoProcessor) buildContentString(method string, uri string, params map[string]string, payload map[string]interface{}) string {
	if method == "POST" && payload != nil {
		payloadJSON, _ := json.Marshal(payload)
		return uri + string(payloadJSON)
	}

	if len(params) == 0 {
		return uri
	}

	var buf bytes.Buffer
	buf.WriteString(uri)
	buf.WriteString("?")
	buf.WriteString(buildOrderedQueryString(params))

	return buf.String()
}

func buildOrderedQueryString(params map[string]string) string {
	preferredOrder := []string{"room_id", "request_user_id", "source", "client_type"}
	var buf strings.Builder
	written := 0
	usedKeys := make(map[string]struct{}, len(params))

	for _, key := range preferredOrder {
		value, ok := params[key]
		if !ok {
			continue
		}
		if written > 0 {
			buf.WriteString("&")
		}
		buf.WriteString(key)
		buf.WriteString("=")
		buf.WriteString(url.QueryEscape(value))
		usedKeys[key] = struct{}{}
		written++
	}

	for key, value := range params {
		if _, ok := usedKeys[key]; ok {
			continue
		}
		if written > 0 {
			buf.WriteString("&")
		}
		buf.WriteString(key)
		buf.WriteString("=")
		buf.WriteString(url.QueryEscape(value))
		written++
	}

	return buf.String()
}

// md5Hash returns MD5 hash as hex string
func (cp *CryptoProcessor) md5Hash(data string) string {
	hash := md5.Sum([]byte(data))
	return hexEncode(hash[:])
}

func hexEncode(data []byte) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, len(data)*2)
	for i, b := range data {
		result[i*2] = hexChars[b>>4]
		result[i*2+1] = hexChars[b&0x0F]
	}
	return string(result)
}

// SignXSCommon generates the x-s-common signature
func (cp *CryptoProcessor) SignXSCommon(a1Value string, cookies map[string]string, timestamp float64) string {
	b1 := cp.generateB1(a1Value, cookies, timestamp)
	crcValue := CRC32JSInt([]byte(b1))

	signStruct := SignCommonData{
		S0:  5,
		S1:  "",
		X0:  "1",
		X1:  "4.3.3",
		X2:  "Windows",
		X3:  "xhs-pc-web",
		X4:  "4.86.0",
		X5:  a1Value,
		X6:  "",
		X7:  "",
		X8:  b1,
		X9:  crcValue,
		X10: 0,
		X11: "normal",
	}

	signJSON, _ := json.Marshal(signStruct)
	return cp.b64Encoder.Encode(signJSON)
}

// generateB1 generates the b1 parameter from fingerprint
func (cp *CryptoProcessor) generateB1(a1Value string, cookies map[string]string, timestamp float64) string {
	// Use fixed canvas hash from config (matches Python's FPData.CANVAS_HASH)
	_ = a1Value
	_ = cookies
	_ = timestamp

	// x36 is a random value between 1-20
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	x36 := r.Intn(20) + 1

	// x44 is set to current time in milliseconds (NOT the passed timestamp)
	// Python's fingerprint generator uses time.time() * 1000 for x44
	x44 := time.Now().UnixMilli()

	fp := cp.newB1Fingerprint(x36, x44)

	fpJSON, _ := json.Marshal(fp)
	fpStr := string(fpJSON)

	key := []byte(cp.config.B1SecretKey)
	ciphertext := cp.rc4Encrypt(key, []byte(fpStr))

	urlEncoded := urlEncodeBytes(ciphertext)
	parsed := parseURLEncoded(urlEncoded)

	return cp.b64Encoder.Encode(parsed)
}

// urlEncodeBytes URL-encodes bytes by first converting to UTF-8 rune encoding,
// then percent-encoding. This matches Python's urllib.parse.quote behavior
// which encodes each character as UTF-8 before percent-encoding.
func urlEncodeBytes(data []byte) string {
	// Safe characters that are NOT percent-encoded (matches Python's safe parameter)
	safeChars := [256]bool{}
	for _, c := range "!*'()~_-" {
		safeChars[c] = true
	}

	var result strings.Builder
	for _, b := range data {
		// Convert byte to rune (codepoint in Latin-1, which is unicode 0-255)
		r := rune(b)
		if safeChars[b] {
			// Safe character - pass through as-is
			result.WriteRune(r)
		} else {
			// Not safe - UTF-8 encode the rune, then percent-encode each byte
			utf8Bytes := make([]byte, 4)
			n := utf8.EncodeRune(utf8Bytes, r)
			for i := 0; i < n; i++ {
				result.WriteString(fmt.Sprintf("%%%02X", int(utf8Bytes[i])&0xFF))
			}
		}
	}
	return result.String()
}

// parseURLEncoded parses URL-encoded string back to bytes
// Handles %XX sequences where XX is hex value, and any remaining chars as literal bytes
func parseURLEncoded(encoded string) []byte {
	var result []byte
	for _, part := range strings.Split(encoded, "%")[1:] {
		if len(part) >= 2 {
			// Parse hex value of first 2 chars
			hexVal := parseHex(part[:2])
			result = append(result, byte(hexVal))
			// Append remaining chars as bytes
			for _, c := range part[2:] {
				result = append(result, byte(c))
			}
		}
	}
	return result
}

// parseHex parses a 2-character hex string
func parseHex(s string) int {
	result := 0
	for _, c := range s {
		result *= 16
		if c >= '0' && c <= '9' {
			result += int(c - '0')
		} else if c >= 'A' && c <= 'F' {
			result += int(c - 'A' + 10)
		}
	}
	return result
}

// rc4Encrypt performs RC4 encryption
func (cp *CryptoProcessor) rc4Encrypt(key, data []byte) []byte {
	// Initialize S-box
	S := make([]byte, 256)
	for i := range 256 {
		S[i] = byte(i)
	}

	j := 0
	for i := 0; i < 256; i++ {
		j = (j + int(S[i]) + int(key[i%len(key)])) % 256
		S[i], S[j] = S[j], S[i]
	}

	// Generate keystream and encrypt
	result := make([]byte, len(data))
	i := 0
	j = 0
	for idx := range data {
		i = (i + 1) % 256
		j = (j + int(S[i])) % 256
		S[i], S[j] = S[j], S[i]
		t := S[(int(S[i])+int(S[j]))%256]
		result[idx] = data[idx] ^ t
	}

	return result
}

// GetXT returns the x-t header value (Unix timestamp in milliseconds)
func (cp *CryptoProcessor) GetXT(timestamp float64) int64 {
	return int64(timestamp * 1000)
}

// GenerateB3TraceID generates a 16-character hexadecimal trace ID
func (cp *CryptoProcessor) GenerateB3TraceID() string {
	const hexChars = "abcdef0123456789"
	result := make([]byte, 16)
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	for i := range 16 {
		result[i] = hexChars[r.Intn(16)]
	}
	return string(result)
}

// GenerateXrayTraceID generates a 32-character hexadecimal trace ID
func (cp *CryptoProcessor) GenerateXrayTraceID(timestamp int64, seq int) string {
	const hexChars = "abcdef0123456789"
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	if seq == 0 {
		seq = r.Intn(cp.config.XrayTraceIdSeqMax)
	}
	if timestamp == 0 {
		timestamp = time.Now().UnixMilli()
	}

	part1 := uint64(timestamp) << 23
	part1 |= uint64(seq & 0x7FFFFF)

	result := make([]byte, 32)
	hexPart := fmt.Sprintf("%016x", part1)
	copy(result, hexPart)

	for i := 0; i < 16; i++ {
		result[16+i] = hexChars[r.Intn(16)]
	}

	return string(result)
}