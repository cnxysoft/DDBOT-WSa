package qqofficial

// 扫码绑定的协议解析与 AES-GCM 解密单测。
//
// 全程离线：AES 用测试侧自造的向量（自己生成 key、自己加密再解），协议用
// httptest 起的假平台（回环地址），不碰真机、不联网。

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testBindSecret = "TEST_SECRET_NOT_A_REAL_CREDENTIAL"

// encryptBindSecret 是测试侧的加密：按协议拼 IV + ciphertext + tag，再 base64。
// 它与 DecryptBindSecret 是同一份格式约定的两端 —— 只要参数顺序搞错
// （比如手动把 16 字节 tag 从密文尾部切出去），这个测试立刻红。
func encryptBindSecret(t *testing.T, key, plaintext []byte) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("NewGCM: %v", err)
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("rand: %v", err)
	}
	sealed := gcm.Seal(nil, iv, plaintext, nil) // ciphertext || tag
	return base64.StdEncoding.EncodeToString(append(iv, sealed...))
}

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, qrBindKeyLen)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return key
}

func TestDecryptBindSecretRoundTrip(t *testing.T) {
	key := testKey(t)
	encrypted := encryptBindSecret(t, key, []byte(testBindSecret))

	got, err := DecryptBindSecret(encrypted, key)
	if err != nil {
		t.Fatalf("解密失败：%v", err)
	}
	if got != testBindSecret {
		t.Fatalf("明文不符：%q", got)
	}
}

func TestDecryptBindSecretRejectsBadInput(t *testing.T) {
	key := testKey(t)
	valid := encryptBindSecret(t, key, []byte(testBindSecret))

	shortBlob := base64.StdEncoding.EncodeToString(make([]byte, qrBindIVLen+qrBindGCMTagLen))

	cases := []struct {
		name      string
		encrypted string
		key       []byte
	}{
		{"密钥长度不对", valid, key[:16]},
		{"不是 base64", "!!!not-base64!!!", key},
		{"密文太短", shortBlob, key},
		{"空密文", "", key},
	}
	for _, c := range cases {
		if _, err := DecryptBindSecret(c.encrypted, c.key); err == nil {
			t.Fatalf("%s：应当报错", c.name)
		}
	}
}

func TestDecryptBindSecretWrongKeyFails(t *testing.T) {
	key := testKey(t)
	encrypted := encryptBindSecret(t, key, []byte(testBindSecret))

	if _, err := DecryptBindSecret(encrypted, testKey(t)); err == nil {
		t.Fatal("换了密钥应当解不开")
	}
}

func TestDecryptBindSecretWrongIVFails(t *testing.T) {
	// 把 IV 首字节改掉：GCM 必须校验失败，不能返回脏明文
	key := testKey(t)
	blob, err := base64.StdEncoding.DecodeString(encryptBindSecret(t, key, []byte(testBindSecret)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	blob[0] ^= 0xFF
	if _, err := DecryptBindSecret(base64.StdEncoding.EncodeToString(blob), key); err == nil {
		t.Fatal("IV 被改应当解不开")
	}
}

// fakeBindServer 起一个假平台，按协议回响应。
func fakeBindServer(t *testing.T, createBody, pollBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(qrBindPathPrefix+qrBindCreateTaskPath, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, err := base64.StdEncoding.DecodeString(body["key"])
		if err != nil || len(raw) != qrBindKeyLen {
			t.Errorf("create_bind_task 的 key 不合法：err=%v len=%d", err, len(raw))
		}
		_, _ = w.Write([]byte(createBody))
	})
	mux.HandleFunc(qrBindPathPrefix+qrBindPollResultPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pollBody))
	})
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func fakeOptions(ts *httptest.Server, key []byte) *QRBindOptions {
	return &QRBindOptions{
		Host:       strings.TrimPrefix(ts.URL, "https://"),
		Source:     "ddbot",
		Key:        key,
		HTTPClient: ts.Client(),
	}
}

func TestQRBindSessionCreateAndPollSuccess(t *testing.T) {
	key := testKey(t)
	encrypted := encryptBindSecret(t, key, []byte(testBindSecret))
	ts := fakeBindServer(t,
		`{"retcode":0,"data":{"task_id":"task-123"}}`,
		`{"retcode":0,"data":{"status":2,"bot_appid":"1900000000","bot_encrypt_secret":"`+encrypted+`"}}`)

	opts := fakeOptions(ts, key)
	sess, err := NewQRBindSession(context.Background(), opts)
	if err != nil {
		t.Fatalf("NewQRBindSession: %v", err)
	}
	if sess.TaskID() != "task-123" {
		t.Fatalf("task_id 不符：%q", sess.TaskID())
	}

	wantPrefix := "https://" + opts.Host + qrBindConnectURLPath + "?"
	if !strings.HasPrefix(sess.URL(), wantPrefix) {
		t.Fatalf("二维码链接前缀不符：%s", sess.URL())
	}
	for _, part := range []string{"task_id=task-123", "source=ddbot", "_wv=2"} {
		if !strings.Contains(sess.URL(), part) {
			t.Fatalf("二维码链接缺少 %s：%s", part, sess.URL())
		}
	}

	res, err := sess.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !res.Success() {
		t.Fatalf("应判定为成功：%+v", res)
	}
	if res.AppID != "1900000000" || res.Secret != testBindSecret {
		t.Fatalf("凭据不符：appID=%q secret=%q", res.AppID, res.Secret)
	}
	if res.Secret == encrypted {
		t.Fatal("返回的是密文，说明没走解密")
	}
}

func TestQRBindPollStatusAsStringAndPending(t *testing.T) {
	// status 用字符串形态（平台上别处就摇过类型），且是「继续轮询」的状态。
	// 真机实测：1 是「等扫码」的稳态，不是「已扫码」
	ts := fakeBindServer(t,
		`{"retcode":0,"data":{"task_id":"t1"}}`,
		`{"retcode":0,"data":{"status":"1"}}`)

	sess, err := NewQRBindSession(context.Background(), fakeOptions(ts, testKey(t)))
	if err != nil {
		t.Fatalf("NewQRBindSession: %v", err)
	}
	res, err := sess.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Status != QRBindStatusWaiting {
		t.Fatalf("字符串状态解析错：%d", res.Status)
	}
	if res.Success() {
		t.Fatal("未成功不该判成功")
	}
}

func TestQRBindPollStatusInitIsNotScanned(t *testing.T) {
	// 0 是建任务后的短暂中间态，同样按「等待」处理
	ts := fakeBindServer(t,
		`{"retcode":0,"data":{"task_id":"t1"}}`,
		`{"retcode":0,"data":{"status":0}}`)

	sess, err := NewQRBindSession(context.Background(), fakeOptions(ts, testKey(t)))
	if err != nil {
		t.Fatalf("NewQRBindSession: %v", err)
	}
	res, err := sess.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Status != QRBindStatusInit || res.Success() {
		t.Fatalf("0 应解析为初始态且不判成功：%+v", res)
	}
}

func TestQRBindPollStatusFailed(t *testing.T) {
	ts := fakeBindServer(t,
		`{"retcode":0,"data":{"task_id":"t1"}}`,
		`{"retcode":0,"data":{"status":3}}`)

	sess, err := NewQRBindSession(context.Background(), fakeOptions(ts, testKey(t)))
	if err != nil {
		t.Fatalf("NewQRBindSession: %v", err)
	}
	res, err := sess.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Status != QRBindStatusFailed || res.Success() {
		t.Fatalf("失败状态判定错：%+v", res)
	}
}

func TestQRBindRejectsBadEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		create string
	}{
		{"retcode 非 0", `{"retcode":10001,"msg":"boom","data":{}}`},
		{"缺 retcode", `{"data":{"task_id":"t1"}}`},
		{"data 不是对象", `{"retcode":0,"data":"nope"}`},
		{"没有 task_id", `{"retcode":0,"data":{}}`},
	}
	for _, c := range cases {
		ts := fakeBindServer(t, c.create, `{"retcode":0,"data":{"status":0}}`)
		if _, err := NewQRBindSession(context.Background(), fakeOptions(ts, testKey(t))); err == nil {
			t.Fatalf("%s：应当报错", c.name)
		}
	}
}

func TestQRBindPollRejectsIncompleteCredentials(t *testing.T) {
	// 声称成功但没带密文 / AppID 非纯数字，都必须报错而不是写入脏配置
	cases := []struct {
		name string
		poll string
	}{
		{"缺密文", `{"retcode":0,"data":{"status":2,"bot_appid":"1900000000"}}`},
		{"缺 AppID", `{"retcode":0,"data":{"status":2,"bot_encrypt_secret":"YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo="}}`},
		{"AppID 非数字", `{"retcode":0,"data":{"status":2,"bot_appid":"abc","bot_encrypt_secret":"YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo="}}`},
	}
	for _, c := range cases {
		ts := fakeBindServer(t, `{"retcode":0,"data":{"task_id":"t1"}}`, c.poll)
		sess, err := NewQRBindSession(context.Background(), fakeOptions(ts, testKey(t)))
		if err != nil {
			t.Fatalf("%s: NewQRBindSession: %v", c.name, err)
		}
		if _, err := sess.Poll(context.Background()); err == nil {
			t.Fatalf("%s：应当报错", c.name)
		}
	}
}

func TestQRBindOptionsDefaultsAndHostNormalization(t *testing.T) {
	opts := &QRBindOptions{Host: "https://example.com/"}
	opts.ApplyDefaults()
	if opts.Host != "example.com" {
		t.Fatalf("host 规范化失败：%q", opts.Host)
	}
	if opts.Source != QRBindSourceDefault {
		t.Fatalf("source 默认值不对：%q", opts.Source)
	}
	if opts.PollInterval != QRBindPollInterval || opts.Timeout != QRBindTimeout {
		t.Fatalf("轮询参数默认值不对：%+v", opts)
	}
	if !strings.HasPrefix(opts.baseURL(), "https://") {
		t.Fatalf("baseURL 应带 https：%q", opts.baseURL())
	}
}

func TestQRBindNewSessionRejectsBadKeyLength(t *testing.T) {
	ts := fakeBindServer(t, `{"retcode":0,"data":{"task_id":"t1"}}`, `{"retcode":0,"data":{"status":0}}`)
	opts := fakeOptions(ts, []byte("short"))
	if _, err := NewQRBindSession(context.Background(), opts); err == nil {
		t.Fatal("密钥长度不对应当报错")
	}
}

func TestQRBindSessionWaitReturnsOnSuccess(t *testing.T) {
	key := testKey(t)
	encrypted := encryptBindSecret(t, key, []byte(testBindSecret))
	ts := fakeBindServer(t,
		`{"retcode":0,"data":{"task_id":"t1"}}`,
		`{"retcode":0,"data":{"status":2,"bot_appid":"1900000000","bot_encrypt_secret":"`+encrypted+`"}}`)

	opts := fakeOptions(ts, key)
	opts.PollInterval = time.Millisecond
	sess, err := NewQRBindSession(context.Background(), opts)
	if err != nil {
		t.Fatalf("NewQRBindSession: %v", err)
	}
	res, err := sess.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !res.Success() {
		t.Fatalf("Wait 应返回成功：%+v", res)
	}
}
