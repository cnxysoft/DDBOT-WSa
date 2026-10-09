package cookiecloudclient

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// TestCookieCloudLegacyPassphrase 验证 legacy 口令 hex(md5(uuid+"-"+password))[:16]。
// 期望值由独立 Python hashlib 计算：
// md5("00000000-0000-0000-0000-000000000000-secret") = e6bd008491a612d865d3d553388d419d，
// 取前 16 个十六进制字符。
func TestCookieCloudLegacyPassphrase(t *testing.T) {
	got := cookieCloudLegacyPassphrase("00000000-0000-0000-0000-000000000000", "secret")
	want := []byte("e6bd008491a612d8")
	if !bytes.Equal(got, want) {
		t.Fatalf("legacy passphrase = %q, want %q", got, want)
	}
}

// TestEVPBytesToKeyWritesSalt 用固定 password/salt 验证 EVP_BytesToKey(MD5) 的三次 Write
// 顺序（prev + password + salt）。期望值由独立 Python hashlib 按
// d1=md5(pass+salt)、d2=md5(d1+pass+salt)、d3=md5(d2+pass+salt) 算出，不是照抄 Go 实现。
func TestEVPBytesToKeyWritesSalt(t *testing.T) {
	pass := []byte("0123456789abcdef")
	key, iv := evpBytesToKey(pass, []byte("87654321"), 32, 16)
	if len(key) != 32 || len(iv) != 16 {
		t.Fatalf("key/iv len = %d/%d, want 32/16", len(key), len(iv))
	}
	got := hex.EncodeToString(append(append([]byte{}, key...), iv...))
	const want = "be4919d71cb3d17fd254c0ce0214769948baf83517ca296cc44bc6c600c6a61a062649a0f6c8a3bb5b9ca75adfa8bb8e"
	if got != want {
		t.Fatalf("evpBytesToKey derived = %s, want %s", got, want)
	}
	// nil salt 与"空 salt"应一致，且不能等于带 salt 的结果（确认 salt 真参与派生）。
	keyNoSalt, ivNoSalt := evpBytesToKey(pass, nil, 32, 16)
	gotNoSalt := hex.EncodeToString(append(append([]byte{}, keyNoSalt...), ivNoSalt...))
	if gotNoSalt == want {
		t.Fatalf("salt 未参与 key 派生")
	}
}

// TestDecryptLegacySaltedRoundTrip 用固定 pass/uuid 生成一个 Salted__ 格式密文并回解，
// 覆盖 legacy 解析（前 8 字节判定、取 8 字节 salt、keyLen=32/ivLen=16、AES-256-CBC、PKCS7）。
func TestDecryptLegacySaltedRoundTrip(t *testing.T) {
	const uuid = "00000000-0000-0000-0000-000000000000"
	const password = "secret"
	payload := []byte(`{"cookie_data":{"bilibili.com":[{"name":"SESSDATA","value":"x"}]}}`)

	salt := []byte("87654321")
	key, iv := evpBytesToKey(cookieCloudLegacyPassphrase(uuid, password), salt, legacyEVPKeyLen, legacyEVPIVLen)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	padding := aes.BlockSize - len(payload)%aes.BlockSize
	padded := append(append([]byte{}, payload...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	enc := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(enc, padded)

	raw := append(append([]byte(legacySaltPrefix), salt...), enc...)
	encrypted := []byte(base64.StdEncoding.EncodeToString(raw))
	plain, err := decrypt(encrypted, password, uuid, CryptoTypeLegacy)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(plain, payload) {
		t.Fatalf("plain = %s, want %s", plain, payload)
	}
}

// TestDecryptFixedZeroIV 验证 aes-128-cbc-fixed：key=[]byte(hex(md5(uuid+"-"+password))[:16])、
// iv=16 字节全 0，且不走 EVP_BytesToKey（成品 decrypt @0xb91e00 fixed 分支反汇编）。
func TestDecryptFixedZeroIV(t *testing.T) {
	const uuid = "00000000-0000-0000-0000-000000000000"
	const password = "secret"
	payload := []byte(`{"cookie_data":{"weibo.com":[]}}`)
	key := cookieCloudLegacyPassphrase(uuid, password)
	iv := make([]byte, aes.BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	padding := aes.BlockSize - len(payload)%aes.BlockSize
	padded := append(append([]byte{}, payload...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	enc := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(enc, padded)
	encrypted := []byte(base64.StdEncoding.EncodeToString(enc))
	plain, err := decrypt(encrypted, password, uuid, CryptoTypeAES128CBCFixed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(plain, payload) {
		t.Fatalf("plain = %s, want %s", plain, payload)
	}
}

// TestDecryptLegacyRequiresSaltedMarker 校验缺少 Salted__ 标记或长度不足时的错误。
func TestDecryptLegacyRequiresSaltedMarker(t *testing.T) {
	const uuid = "00000000-0000-0000-0000-000000000000"
	raw := base64.StdEncoding.EncodeToString([]byte("NotSalt__0123456789abcdef"))
	if _, err := decrypt([]byte(raw), "secret", uuid, CryptoTypeLegacy); err != ErrLegacySaltMissing {
		t.Fatalf("err = %v, want ErrLegacySaltMissing", err)
	}
	// 长度不足 16 也按缺少标记处理（与成品分支一致）。
	short := base64.StdEncoding.EncodeToString([]byte("Salted__"))
	if _, err := decrypt([]byte(short), "secret", uuid, CryptoTypeLegacy); err != ErrLegacySaltMissing {
		t.Fatalf("short err = %v, want ErrLegacySaltMissing", err)
	}
}
