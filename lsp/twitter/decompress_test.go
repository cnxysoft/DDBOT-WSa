package twitter

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gzipCompress(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func zlibCompress(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, err := w.Write(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func brotliCompress(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	_, err := w.Write(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// TestDecompressResponse 覆盖线上曾经踩过的坑：响应声明支持 br，Twitter 也真的返回 br，
// 而旧实现按不存在的“brotli 魔数”判断，结果压缩体被原样交给 json.Unmarshal。
func TestDecompressResponse(t *testing.T) {
	payload := []byte(`{"data":{"home":{"home_timeline_urt":{"instructions":[]}}},"text":"推文正文"}`)

	t.Run("明文直接返回", func(t *testing.T) {
		out, err := decompressResponse(payload)
		require.NoError(t, err)
		assert.Equal(t, payload, out)
	})

	t.Run("gzip", func(t *testing.T) {
		compressed := gzipCompress(t, payload)
		require.Equal(t, byte(0x1f), compressed[0])
		out, err := decompressResponse(compressed)
		require.NoError(t, err)
		assert.Equal(t, payload, out)
	})

	t.Run("zlib", func(t *testing.T) {
		out, err := decompressResponse(zlibCompress(t, payload))
		require.NoError(t, err)
		assert.Equal(t, payload, out)
	})

	t.Run("brotli", func(t *testing.T) {
		compressed := brotliCompress(t, payload)
		// brotli 没有魔数，首字节不固定（也可能是 '{' 或 '['），只能靠解压结果判断
		out, err := decompressResponse(compressed)
		require.NoError(t, err)
		assert.Equal(t, payload, out)
	})

	t.Run("首字节恰好是方括号的brotli", func(t *testing.T) {
		// 回归：这条 60KB 正文的压缩体首字节就是 '['，旧嗅探逻辑会把它当成明文直接返回
		large := []byte(`{"data":"` + string(bytes.Repeat([]byte("推文"), 20000)) + `"}`)
		compressed := brotliCompress(t, large)
		require.Equal(t, byte('['), compressed[0])

		out, err := decompressResponse(compressed)
		require.NoError(t, err)
		assert.Equal(t, large, out)
	})

	t.Run("HTML拦截页原样返回", func(t *testing.T) {
		html := []byte("<!DOCTYPE html><html><body>blocked</body></html>")
		out, err := decompressResponse(html)
		require.NoError(t, err)
		assert.Equal(t, html, out)
	})

	t.Run("未压缩的JavaScript原样返回", func(t *testing.T) {
		js := []byte(`self.ASSETS=["https://abs.twimg.com/responsive-web/client-web/bundle.LoggedInMain.abc123.js"];`)
		out, err := decompressResponse(js)
		require.NoError(t, err)
		assert.Equal(t, js, out)
	})

	t.Run("brotli压缩的JavaScript", func(t *testing.T) {
		// sw.js 是 JavaScript，不是 JSON/HTML，旧判据会把它当作“解压不成功”拒掉
		js := []byte(`self.ASSETS=["https://abs.twimg.com/responsive-web/client-web/bundle.LoggedInMain.abc123.js"];` +
			string(bytes.Repeat([]byte(`/*pad*/`), 2000)))
		out, err := decompressResponse(brotliCompress(t, js))
		require.NoError(t, err)
		assert.Equal(t, js, out)
	})

	t.Run("空响应", func(t *testing.T) {
		out, err := decompressResponse(nil)
		require.NoError(t, err)
		assert.Empty(t, out)
	})

	t.Run("无法识别的编码返回错误并附带真实head", func(t *testing.T) {
		garbage := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
		out, err := decompressResponse(garbage)
		require.Error(t, err)
		assert.Nil(t, out)
		assert.Contains(t, err.Error(), "00 01 02 03")
	})
}

func TestIsTwitterAuthError(t *testing.T) {
	assert.True(t, isTwitterAuthError(classifyTwitterHTTPError(http.StatusUnauthorized, errors.New("bad response"))))
	assert.True(t, isTwitterAuthError(fmt.Errorf("获取用户推文失败：%w",
		classifyTwitterHTTPError(http.StatusForbidden, errors.New("denied")))))

	assert.False(t, isTwitterAuthError(nil))
	assert.False(t, isTwitterAuthError(errors.New("network unreachable")))
	assert.False(t, isTwitterAuthError(fmt.Errorf("failed to parse response: %w",
		errors.New("invalid character '\\u0083' looking for beginning of value"))))
	assert.False(t, isTwitterAuthError(classifyTwitterHTTPError(http.StatusInternalServerError, errors.New("boom"))))
}

func TestTwitterAuthErrorCode(t *testing.T) {
	for _, code := range twitterAuthErrorCodes {
		body := []byte(fmt.Sprintf(`{"errors":[{"code":%d,"message":"auth"}]}`, code))
		got, ok := twitterAuthErrorCode(body)
		assert.True(t, ok, "code %d 应识别为鉴权错误", code)
		assert.Equal(t, code, got)
	}

	// 88 是限速、不是鉴权失败
	_, ok := twitterAuthErrorCode([]byte(`{"errors":[{"code":88,"message":"rate limit"}]}`))
	assert.False(t, ok)

	// 非法 JSON 不应误判
	_, ok = twitterAuthErrorCode([]byte("<html></html>"))
	assert.False(t, ok)
}
