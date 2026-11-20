package twitter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	anubisWorkTimeout = 5 * time.Second
	anubisWasmMaxSize = 256 * 1024
	anubisPath        = "/.within.website/x/cmd/anubis/"
)

func parseAnubisChallenge(doc *goquery.Document, target *url.URL) (*Challenges, error) {
	var challenge AnubisChallenge
	if err := json.Unmarshal([]byte(doc.Find("#anubis_challenge").Text()), &challenge); err != nil {
		return nil, err
	}
	limit := 6
	switch challenge.Rules.Algorithm {
	case "fast", "slow":
	case "hashx":
		// HashX measures leading bits; fast/slow measure leading hex digits.
		limit = 20
	default:
		return nil, fmt.Errorf("不支持的 Anubis 算法 %q", challenge.Rules.Algorithm)
	}
	if challenge.Rules.Difficulty < 1 || challenge.Rules.Difficulty > limit {
		return nil, fmt.Errorf("Anubis 难度超出支持范围: %d", challenge.Rules.Difficulty)
	}
	for _, extension := range challenge.Rules.Extensions {
		if extension != "css-load" {
			return nil, fmt.Errorf("不支持的 Anubis 扩展 %q", extension)
		}
	}
	result := &AnubisResult{
		Host: target.Host, TargetURL: target.String(), Algorithm: challenge.Rules.Algorithm,
		Difficulty: challenge.Rules.Difficulty, Extensions: challenge.Rules.Extensions,
	}
	switch value := challenge.Challenge.(type) {
	case string:
		result.RandomData = value
	case map[string]interface{}:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var sub AnubisChallengeSub
		if err := json.Unmarshal(encoded, &sub); err != nil {
			return nil, err
		}
		result.RandomData, result.Id = sub.RandomData, sub.Id
	default:
		return nil, errors.New("Anubis 挑战格式无效")
	}
	if result.RandomData == "" || len(result.RandomData) > 8192 {
		return nil, errors.New("Anubis 挑战数据为空或过长")
	}
	if result.Algorithm == "hashx" || len(result.Extensions) > 0 {
		if result.Id == "" || len(result.Id) > 128 || strings.ContainsAny(result.Id, "/\\?#%.") {
			return nil, errors.New("Anubis 挑战 ID 无效")
		}
	}
	for id, dest := range map[string]*string{"anubis_version": &result.Version, "anubis_base_prefix": &result.BasePrefix} {
		if data := strings.TrimSpace(doc.Find("#" + id).Text()); data != "" {
			if err := json.Unmarshal([]byte(data), dest); err != nil {
				return nil, fmt.Errorf("Anubis %s 无效: %w", id, err)
			}
		}
	}
	if prefix := result.BasePrefix; prefix != "" &&
		(!strings.HasPrefix(prefix, "/") || strings.HasPrefix(prefix, "//") ||
			strings.ContainsAny(prefix, "\\?#%") || path.Clean(prefix) != prefix) {
		return nil, errors.New("Anubis 路径前缀无效")
	}
	return &Challenges{Type: "anubis", Anubis: result}, nil
}

func FreshCookie(anubis *AnubisResult) {
	if err := refreshAnubisCookie(anubis); err != nil {
		logger.WithError(err).Error("刷新 Twitter 镜像 Anubis Cookie 失败")
	}
}

func refreshAnubisCookie(anubis *AnubisResult) error {
	return refreshAnubisCookieWithOptions(anubis, append(SetRequestOptions(), requests.RetryOption(0)))
}

func refreshAnubisCookieWithOptions(anubis *AnubisResult, options []requests.Option) error {
	if anubis == nil {
		return errors.New("Anubis 挑战为空")
	}
	target := anubis.TargetURL
	if target == "" {
		target = "https://" + anubis.Host + "/"
	}
	base, err := url.Parse(target)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return errors.New("Anubis 实例地址无效")
	}
	base.RawQuery, base.Fragment = "", ""
	started := time.Now()
	for _, extension := range anubis.Extensions {
		if extension != "css-load" {
			return fmt.Errorf("不支持的 Anubis 扩展 %q", extension)
		}
		base.Path = anubis.BasePrefix + anubisPath + "ext/css-load/" + anubis.Id
		var response bytes.Buffer
		if err := requests.Get(base.String(), nil, &response, options...); err != nil {
			return fmt.Errorf("Anubis css-load 失败: %w", err)
		}
	}
	hash, nonce := anubis.Hash, anubis.Nonce
	if hash == "" {
		if anubis.Algorithm == "hashx" {
			base.Path = anubis.BasePrefix + anubisPath + "static/wasm/baseline/hashx.wasm"
			query := url.Values{"cacheBuster": {anubis.Version}}
			base.RawQuery = query.Encode()
			var wasm limitedMirrorBuffer
			wasm.limit = anubisWasmMaxSize
			opts := append(options[:len(options):len(options)], requests.HeaderOption("Accept-Encoding", "identity"))
			if err := requests.Get(base.String(), nil, &wasm, opts...); err != nil {
				return fmt.Errorf("Anubis HashX 模块下载失败: %w", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), anubisWorkTimeout)
			nonce, hash, err = solveAnubisHashX(ctx, wasm.Bytes(), anubis.RandomData, anubis.Difficulty)
			cancel()
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), anubisWorkTimeout)
			nonce, hash, err = solveAnubisSHA256(ctx, anubis.RandomData, anubis.Difficulty)
			cancel()
		}
		if err != nil {
			return err
		}
	}
	base.Path = anubis.BasePrefix + anubisPath + "api/pass-challenge"
	query := url.Values{
		"response": {hash}, "nonce": {strconv.Itoa(nonce)}, "redir": {target},
		"elapsedTime": {strconv.FormatInt(time.Since(started).Milliseconds()+int64(anubis.Time), 10)},
	}
	if anubis.Id != "" {
		query.Set("id", anubis.Id)
	}
	base.RawQuery = query.Encode()
	var response bytes.Buffer
	return requests.Get(base.String(), nil, &response, options...)
}

func solveAnubisSHA256(ctx context.Context, data string, difficulty int) (int, string, error) {
	if difficulty < 1 || difficulty > 6 {
		return 0, "", errors.New("Anubis SHA256 难度超出支持范围")
	}
	for nonce := 0; ; nonce++ {
		if nonce%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, "", err
			}
		}
		hash := sha256.Sum256([]byte(data + strconv.Itoa(nonce)))
		if checkNibbles(hash, difficulty) {
			return nonce, hex.EncodeToString(hash[:]), nil
		}
	}
}

type limitedMirrorBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedMirrorBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		return 0, errors.New("镜像响应超过大小限制")
	}
	return b.buffer.Write(data)
}

func (b *limitedMirrorBuffer) Bytes() []byte { return b.buffer.Bytes() }

// Run only the instance's baseline HashX worker, without WASI, files or network
// imports. Wazero supports pure Go builds; the deadline and 32 MiB cap bound work.
func solveAnubisHashX(ctx context.Context, wasm []byte, data string, difficulty int) (int, string, error) {
	seed, err := hex.DecodeString(data)
	if err != nil || len(seed) == 0 || len(seed) > 4096 || difficulty < 1 || difficulty > 20 {
		return 0, "", errors.New("Anubis HashX 挑战数据或难度无效")
	}
	if len(wasm) > anubisWasmMaxSize {
		return 0, "", errors.New("Anubis HashX 模块超过大小限制")
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(512).WithCloseOnContextDone(true))
	defer runtime.Close(context.Background())
	if _, err := runtime.NewHostModuleBuilder("anubis").NewFunctionBuilder().
		WithFunc(func(uint32) {}).Export("anubis_update_nonce").Instantiate(ctx); err != nil {
		return 0, "", err
	}
	module, err := runtime.InstantiateWithConfig(ctx, wasm, wazero.NewModuleConfig())
	if err != nil {
		return 0, "", fmt.Errorf("Anubis HashX 模块加载失败: %w", err)
	}
	for _, name := range []string{"data_ptr", "set_data_length", "anubis_work", "result_hash_ptr", "result_hash_size"} {
		if module.ExportedFunction(name) == nil {
			return 0, "", fmt.Errorf("Anubis HashX 模块缺少 %s", name)
		}
	}
	if module.Memory() == nil {
		return 0, "", errors.New("Anubis HashX 模块缺少内存")
	}
	ptr, err := anubisValue(ctx, module, "data_ptr")
	if err != nil {
		return 0, "", err
	}
	if !module.Memory().Write(ptr, seed) {
		return 0, "", errors.New("Anubis HashX 输入内存越界")
	}
	if _, err := module.ExportedFunction("set_data_length").Call(ctx, uint64(len(seed))); err != nil {
		return 0, "", err
	}
	nonce, err := anubisValue(ctx, module, "anubis_work", uint64(difficulty), 0, 1)
	if err != nil {
		return 0, "", fmt.Errorf("Anubis HashX 计算失败: %w", err)
	}
	ptr, err = anubisValue(ctx, module, "result_hash_ptr")
	if err != nil {
		return 0, "", err
	}
	size, err := anubisValue(ctx, module, "result_hash_size")
	if err != nil || size != sha256.Size {
		return 0, "", errors.New("Anubis HashX 结果长度无效")
	}
	hash, ok := module.Memory().Read(ptr, size)
	if !ok {
		return 0, "", errors.New("Anubis HashX 输出内存越界")
	}
	if uint64(nonce) > uint64(^uint(0)>>1) {
		return 0, "", errors.New("Anubis HashX nonce 超出支持范围")
	}
	return int(nonce), hex.EncodeToString(hash), nil
}

func anubisValue(ctx context.Context, module api.Module, name string, args ...uint64) (uint32, error) {
	values, err := module.ExportedFunction(name).Call(ctx, args...)
	if err != nil {
		return 0, err
	}
	if len(values) != 1 || values[0] > uint64(^uint32(0)) {
		return 0, fmt.Errorf("Anubis HashX %s 返回值无效", name)
	}
	return uint32(values[0]), nil
}
