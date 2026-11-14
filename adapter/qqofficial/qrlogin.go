package qqofficial

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// QQ 机器人扫码绑定（官方「Agent 接入」协议）。
//
// 这是腾讯官方公开能力，不是私有通道：@tencent-connect/qqbot-connector 导出
// startQrConnect，openclaw-qqbot（官方仓库）/ MaiBot / AstrBot 都是照这套协议各写一遍。
// 本实现依据该公开协议独立实现，未借用任何私有接口。
//
// 流程：
//  1. 本地生成 32 字节随机 key（base64 编码）；
//  2. POST /lite/create_bind_task {key} -> data.task_id（retcode == 0 才算成功）；
//  3. 二维码内容 https://<host>/qqbot/openclaw/connect.html?task_id=..&source=..&_wv=2；
//  4. POST /lite/poll_bind_result {task_id}：status 2 = 成功（bot_appid + bot_encrypt_secret），
//     status 3 = 失败/过期，status 0/1 = 继续轮询；
//  5. 密文 base64 解出后是 **12 字节 IV + ciphertext + 16 字节 GCM tag**，用第 1 步那把 key
//     做 AES-256-GCM 解密，得到 AppSecret。
//
// 两条约定：
//   - **凭据不进日志**：本文件的日志与错误信息都不含 key / 密文 / AppSecret；
//   - 远端 message 不回显（响应细节可能带凭据，一律换成自己的措辞）。
//
// 这条链路拿到的是一对「AppID + AppSecret」，等于顺手创建/绑定一个机器人；
// 已有机器人的用户仍然手填，两条路并存。

const (
	// ConfigKeyQRBindHost / ConfigKeyQRBindSource 是扫码绑定的可配项
	// （完整键名是 qqofficial.qr-bind-host / qqofficial.qr-bind-source）。
	ConfigKeyQRBindHost   = "qr-bind-host"
	ConfigKeyQRBindSource = "qr-bind-source"

	// QRBindHostDefault 是绑定服务的默认域名。
	QRBindHostDefault = "q.qq.com"
	// QRBindSourceDefault 是二维码链接里的来源标识。官方没有取值白名单：
	// openclaw 用 openclaw、MaiBot 用 maibot，DDBOT 取自己的标识。
	QRBindSourceDefault = "ddbot"
	// QRBindPollInterval 是轮询间隔（参考实现取 2 秒）。
	QRBindPollInterval = 2 * time.Second
	// QRBindTimeout 是一次扫码会话的总时长，与面板的 loginSessionTTL 一致。
	QRBindTimeout = 5 * time.Minute
	// QRBindRequestTimeout 是单次绑定接口请求的超时。
	QRBindRequestTimeout = 10 * time.Second

	qrBindPathPrefix     = "/lite/"
	qrBindConnectURLPath = "/qqbot/openclaw/connect.html"
	qrBindCreateTaskPath = "create_bind_task"
	qrBindPollResultPath = "poll_bind_result"

	// 协议里写死的长度。
	qrBindKeyLen    = 32
	qrBindIVLen     = 12
	qrBindGCMTagLen = 16
)

// 平台返回的 status 取值。
//
// 只有 2（成功）与 3（失败/过期）是协议明确、参考实现（MaiBot / AstrBot）共用的判定；
// 0 / 1 在它们那里都只被当作「继续轮询」，协议没有说明差异。
//
// **实测纠正（2026-11-11，真机 create_bind_task 后持续轮询 12 秒，全程无人扫码）**：
// 状态先短暂为 0，随后稳定在 1 并长期不动。也就是说 **1 不是「已扫码」** ——
// 谁都没扫就显示「已扫码，请在手机上确认」是错的。0 与 1 一律按「等待扫码」处理。
const (
	QRBindStatusInit    = 0 // 绑定任务刚创建（短暂中间态）
	QRBindStatusWaiting = 1 // 等待用户扫码 / 授权（实测的稳态）
	QRBindStatusSuccess = 2 // 授权完成，带回 bot_appid + bot_encrypt_secret
	QRBindStatusFailed  = 3 // 失败 / 过期
)

// QRBindOptions 是绑定会话的可调参数。
type QRBindOptions struct {
	// Host 是绑定服务域名（不含 scheme），默认 q.qq.com。
	Host string
	// Source 是二维码链接的 source 参数。
	Source string
	// PollInterval / Timeout 是轮询节奏与总时长。
	PollInterval time.Duration
	Timeout      time.Duration
	// HTTPClient 留空则建一个默认超时的客户端。
	HTTPClient *http.Client
	// Key 仅供测试注入固定密钥；为空时随机生成 32 字节。
	Key []byte
}

// DefaultQRBindOptions 读配置并补齐默认值。
func DefaultQRBindOptions() *QRBindOptions {
	opts := &QRBindOptions{
		Host:   getString(ConfigKeyQRBindHost),
		Source: getString(ConfigKeyQRBindSource),
	}
	opts.ApplyDefaults()
	return opts
}

// ApplyDefaults 把零值补成默认值。
func (o *QRBindOptions) ApplyDefaults() {
	if o.Host == "" {
		o.Host = QRBindHostDefault
	}
	o.Host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(o.Host, "https://"), "http://"), "/")
	if o.Source == "" {
		o.Source = QRBindSourceDefault
	}
	if o.PollInterval <= 0 {
		o.PollInterval = QRBindPollInterval
	}
	if o.Timeout <= 0 {
		o.Timeout = QRBindTimeout
	}
}

func (o *QRBindOptions) client() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: QRBindRequestTimeout}
}

func (o *QRBindOptions) baseURL() string {
	return "https://" + o.Host
}

// connectURL 拼二维码内容。_wv=2 是 QQ 内置浏览器的参数，照抄协议。
func (o *QRBindOptions) connectURL(taskID string) string {
	q := url.Values{}
	q.Set("task_id", taskID)
	q.Set("source", o.Source)
	q.Set("_wv", "2")
	return o.baseURL() + qrBindConnectURLPath + "?" + q.Encode()
}

// QRBindResult 是一次轮询的结果。
type QRBindResult struct {
	// Status 是平台返回的原始 status。
	Status int
	// AppID / Secret 只在 Status == QRBindStatusSuccess 时有值。
	// Secret 是解密后的 AppSecret，只应进配置，不要写日志。
	AppID  string
	Secret string
	// Expired 是 Wait 在超时后打的标记（平台侧没有专门的过期状态码）。
	Expired bool
}

// Success 判断这次轮询是否拿到了完整可用的凭据。
func (r *QRBindResult) Success() bool {
	return r != nil && r.Status == QRBindStatusSuccess && r.AppID != "" && r.Secret != ""
}

// QRBindSession 是一次扫码绑定会话：只在内存里持有 key 与 task_id。
type QRBindSession struct {
	opts     *QRBindOptions
	key      []byte
	taskID   string
	url      string
	deadline time.Time
}

// NewQRBindSession 生成密钥、创建绑定任务，并拼出二维码内容。
func NewQRBindSession(ctx context.Context, opts *QRBindOptions) (*QRBindSession, error) {
	if opts == nil {
		opts = DefaultQRBindOptions()
	}
	opts.ApplyDefaults()

	key := opts.Key
	if len(key) == 0 {
		key = make([]byte, qrBindKeyLen)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("生成绑定密钥失败：%w", err)
		}
	}
	if len(key) != qrBindKeyLen {
		return nil, fmt.Errorf("绑定密钥长度必须是 %d 字节，当前 %d", qrBindKeyLen, len(key))
	}

	s := &QRBindSession{
		opts:     opts,
		key:      key,
		deadline: time.Now().Add(opts.Timeout),
	}

	data, err := s.post(ctx, qrBindCreateTaskPath, map[string]string{
		"key": base64.StdEncoding.EncodeToString(key),
	})
	if err != nil {
		return nil, err
	}
	taskID, _ := jsonStringValue(data["task_id"])
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("QQ 没有返回绑定任务 ID")
	}
	s.taskID = strings.TrimSpace(taskID)
	s.url = opts.connectURL(s.taskID)
	return s, nil
}

// URL 是二维码内容。
func (s *QRBindSession) URL() string { return s.url }

// TaskID 是这次绑定任务在平台的 id。
func (s *QRBindSession) TaskID() string { return s.taskID }

// Deadline 是这次会话的截止时间。
func (s *QRBindSession) Deadline() time.Time { return s.deadline }

// Poll 轮询一次绑定结果。status == 2 时会顺手解密出 AppSecret。
func (s *QRBindSession) Poll(ctx context.Context) (*QRBindResult, error) {
	data, err := s.post(ctx, qrBindPollResultPath, map[string]string{"task_id": s.taskID})
	if err != nil {
		return nil, err
	}

	status, err := jsonIntValue(data["status"])
	if err != nil {
		return nil, fmt.Errorf("绑定状态不是数字：%w", err)
	}
	res := &QRBindResult{Status: status}
	if status != QRBindStatusSuccess {
		return res, nil
	}

	appID, _ := jsonStringValue(data["bot_appid"])
	appID = strings.TrimSpace(appID)
	encrypted, _ := jsonStringValue(data["bot_encrypt_secret"])
	if appID == "" || strings.TrimSpace(encrypted) == "" {
		return nil, fmt.Errorf("QQ 返回的机器人凭据不完整")
	}
	if !isAllDigits(appID) {
		// 参考实现同样做这个校验：AppID 一定是纯数字，否则是脏数据
		return nil, fmt.Errorf("QQ 返回的 AppID 格式异常")
	}

	secret, err := DecryptBindSecret(encrypted, s.key)
	if err != nil {
		return nil, err
	}
	res.AppID, res.Secret = appID, secret
	return res, nil
}

// Wait 一直轮询到成功 / 失败 / 超时 / ctx 取消。
func (s *QRBindSession) Wait(ctx context.Context) (*QRBindResult, error) {
	deadline := s.deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(QRBindTimeout)
	}
	for {
		res, err := s.Poll(ctx)
		if err != nil {
			return nil, err
		}
		if res.Status == QRBindStatusSuccess || res.Status == QRBindStatusFailed {
			return res, nil
		}
		if !time.Now().Add(s.opts.PollInterval).Before(deadline) {
			return &QRBindResult{Status: QRBindStatusFailed, Expired: true}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.opts.PollInterval):
		}
	}
}

// post 调一次绑定接口，返回 data 里的字段表。
func (s *QRBindSession) post(ctx context.Context, endpoint string, payload any) (map[string]json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化扫码请求失败：%w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.baseURL()+qrBindPathPrefix+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造扫码请求失败：%w", err)
	}
	req.Header.Set("Content-Type", ContentTypeJSON)
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.opts.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("扫码接口 %s 请求失败：%w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("读扫码接口 %s 响应失败：%w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("扫码接口 %s 返回 HTTP %d", endpoint, resp.StatusCode)
	}

	var envelope struct {
		RetCode json.RawMessage `json:"retcode"`
		Data    json.RawMessage `json:"data"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &envelope)
	}
	if len(envelope.RetCode) == 0 {
		return nil, fmt.Errorf("扫码接口 %s 响应缺少 retcode", endpoint)
	}
	retcode, err := jsonIntValue(envelope.RetCode)
	if err != nil {
		return nil, fmt.Errorf("扫码接口 %s 的 retcode 不是数字：%w", endpoint, err)
	}
	if retcode != 0 {
		// 远端 message 可能带响应细节，不回显
		return nil, fmt.Errorf("扫码接口 %s 返回失败（retcode=%d）", endpoint, retcode)
	}

	var data map[string]json.RawMessage
	if len(envelope.Data) > 0 {
		_ = json.Unmarshal(envelope.Data, &data)
	}
	if data == nil {
		return nil, fmt.Errorf("扫码接口 %s 返回格式无效", endpoint)
	}
	return data, nil
}

// DecryptBindSecret 解出 AppSecret。
//
// 密文结构（协议固定）：base64(12 字节 IV + ciphertext + 16 字节 GCM tag)。
// AES-GCM 的 tag 本来就在密文尾部，所以解出后直接 Iv=前 12 字节、其余整体交给 Open，
// **不要**手动切 tag —— 切了反而解不开。
func DecryptBindSecret(encryptedB64 string, key []byte) (string, error) {
	if len(key) != qrBindKeyLen {
		return "", fmt.Errorf("绑定密钥长度必须是 %d 字节", qrBindKeyLen)
	}

	blob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptedB64))
	if err != nil {
		// 容错：少数实现会省掉 padding
		blob, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(encryptedB64))
		if err != nil {
			return "", fmt.Errorf("绑定密文不是合法 base64")
		}
	}
	if len(blob) <= qrBindIVLen+qrBindGCMTagLen {
		return "", fmt.Errorf("绑定密文长度不足（%d 字节）", len(blob))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化 AES 失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("初始化 GCM 失败：%w", err)
	}
	if gcm.NonceSize() != qrBindIVLen {
		return "", fmt.Errorf("GCM nonce 长度异常：%d", gcm.NonceSize())
	}

	plain, err := gcm.Open(nil, blob[:qrBindIVLen], blob[qrBindIVLen:], nil)
	if err != nil {
		// 解密失败多半是 key 不匹配或密文被截断；错误里不含密钥与明文
		return "", fmt.Errorf("解密绑定密文失败（密钥不匹配或密文损坏）")
	}

	secret := strings.TrimSpace(string(plain))
	if secret == "" {
		return "", fmt.Errorf("扫码返回了空凭据")
	}
	return secret, nil
}

// jsonStringValue 取 JSON 字符串字段（数字也容忍）。
func jsonStringValue(raw json.RawMessage) (string, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		var out string
		if err := json.Unmarshal(raw, &out); err != nil {
			return "", err
		}
		return out, nil
	}
	return strings.Trim(s, `"`), nil
}

// jsonIntValue 取 JSON 整数字段，兼容「数字」与「数字字符串」两种形态。
// （平台上别处就摇过类型：token 的 expires_in 时而是 7200、时而是 "4442"，见 auth.go。）
func jsonIntValue(raw json.RawMessage) (int, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, nil
	}
	s = strings.Trim(s, `"`)
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%q", s)
	}
	return int(f), nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
