package twitter

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A small locally authored module exercises the Anubis ABI without storing or
// shipping an instance's executable. Real HashX vectors are verified in live probes.
func anubisTestModule(loop bool, memoryPages uint32) []byte {
	leb := func(value uint32) []byte {
		var encoded []byte
		for {
			b := byte(value & 0x7f)
			value >>= 7
			if value != 0 {
				b |= 0x80
			}
			encoded = append(encoded, b)
			if value == 0 {
				return encoded
			}
		}
	}
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	section := func(id byte, payload []byte) {
		module = append(module, id)
		module = append(module, leb(uint32(len(payload)))...)
		module = append(module, payload...)
	}
	section(1, []byte{3, 0x60, 0, 1, 0x7f, 0x60, 1, 0x7f, 0, 0x60, 3, 0x7f, 0x7f, 0x7f, 1, 0x7f})
	section(3, []byte{5, 0, 1, 2, 0, 0})
	section(5, append([]byte{1, 0}, leb(memoryPages)...))
	exports := []byte{6}
	for index, name := range []string{"data_ptr", "set_data_length", "anubis_work", "result_hash_ptr", "result_hash_size", "memory"} {
		exports = append(exports, leb(uint32(len(name)))...)
		exports = append(exports, []byte(name)...)
		if name == "memory" {
			exports = append(exports, 2, 0)
		} else {
			exports = append(exports, 0, byte(index))
		}
	}
	section(7, exports)
	work := []byte{0, 0x41, 7, 0x0b} // Return nonce 7.
	if loop {
		work = []byte{0, 0x03, 0x40, 0x0c, 0, 0x0b, 0, 0x0b}
	}
	bodies := [][]byte{{0, 0x41, 0, 0x0b}, {0, 0x0b}, work, {0, 0x41, 0x80, 1, 0x0b}, {0, 0x41, 32, 0x0b}}
	code := []byte{byte(len(bodies))}
	for _, body := range bodies {
		code = append(code, leb(uint32(len(body)))...)
		code = append(code, body...)
	}
	section(10, code)
	return module
}

func TestAnubisHashXCSSAndSession(t *testing.T) {
	var cssCalls, wasmCalls, passCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, UserAgent, r.UserAgent())
		switch r.URL.Path {
		case "/testuser":
			if cookie, err := r.Cookie("proof"); err == nil && cookie.Value == "accepted" {
				fmt.Fprint(w, mirrorTestHTML)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "started", Path: "/"})
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `<title>Making sure you're not a bot!</title>
<script id="anubis_version" type="application/json">"v1.28.1"</script>
<script id="anubis_base_prefix" type="application/json">"/guard"</script>
<script id="anubis_challenge" type="application/json">{"rules":{"algorithm":"hashx","difficulty":1,"extensions":["css-load"]},"challenge":{"id":"test-challenge","randomData":"0001020304050607"}}</script>`)
		case "/guard" + anubisPath + "ext/css-load/test-challenge":
			cssCalls.Add(1)
			cookie, err := r.Cookie("session")
			require.NoError(t, err)
			require.Equal(t, "started", cookie.Value)
		case "/guard" + anubisPath + "static/wasm/baseline/hashx.wasm":
			wasmCalls.Add(1)
			require.Equal(t, "v1.28.1", r.URL.Query().Get("cacheBuster"))
			w.Header().Set("Content-Type", "application/wasm")
			w.Write(anubisTestModule(false, 1))
		case "/guard" + anubisPath + "api/pass-challenge":
			passCalls.Add(1)
			require.EqualValues(t, 1, cssCalls.Load())
			require.Equal(t, "test-challenge", r.URL.Query().Get("id"))
			require.Equal(t, "7", r.URL.Query().Get("nonce"))
			require.Equal(t, strings.Repeat("0", 64), r.URL.Query().Get("response"))
			http.SetCookie(w, &http.Cookie{Name: "proof", Value: "accepted", Path: "/"})
			http.Redirect(w, r, r.URL.Query().Get("redir"), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	useTestMirrors(t, server.URL)
	posts, err := (&twitterConcern{}).GetTweets("testuser")
	require.NoError(t, err)
	require.Len(t, posts, 1)
	require.EqualValues(t, 1, wasmCalls.Load())
	require.EqualValues(t, 1, passCalls.Load())
}

func TestAnubisHashXResourceLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := solveAnubisHashX(ctx, anubisTestModule(true, 1), "00010203", 1)
	require.ErrorContains(t, err, "deadline")

	_, _, err = solveAnubisHashX(context.Background(), anubisTestModule(false, 513), "00010203", 1)
	require.ErrorContains(t, err, "memory")
	_, _, err = solveAnubisHashX(context.Background(), make([]byte, anubisWasmMaxSize+1), "00010203", 1)
	require.ErrorContains(t, err, "大小限制")
	_, _, err = solveAnubisHashX(context.Background(), []byte{0, 'a', 's', 'm', 1, 0, 0, 0}, "00010203", 1)
	require.ErrorContains(t, err, "缺少")
	_, _, err = solveAnubisHashX(context.Background(), anubisTestModule(false, 1), "not hex", 1)
	require.ErrorContains(t, err, "数据")
}

func TestAnubisDownloadLimitCannotBeBypassedByReaderFrom(t *testing.T) {
	var response limitedMirrorBuffer
	response.limit = 16
	// Hide Reader.WriteTo so io.Copy also tests the destination's ReaderFrom path.
	input := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 17))}
	_, err := io.Copy(&response, input)
	require.ErrorContains(t, err, "大小限制")
	require.LessOrEqual(t, len(response.Bytes()), response.limit)
}

func TestAnubisSHA256VectorAndCancellation(t *testing.T) {
	nonce, hash, err := solveAnubisSHA256(context.Background(), "test_challenge", 2)
	require.NoError(t, err)
	wantNonce, wantHash := ComputePoW("test_challenge", 2, "fast")
	require.Equal(t, wantNonce, nonce)
	require.Equal(t, wantHash, hash)
	decoded, err := hex.DecodeString(hash)
	require.NoError(t, err)
	require.Zero(t, decoded[0])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = solveAnubisSHA256(ctx, "test_challenge", 6)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAnubisRejectsInvalidChallengeMetadata(t *testing.T) {
	for _, tc := range []struct{ name, rules, data, prefix string }{
		{"missing seed", `{"algorithm":"fast","difficulty":1}`, `null`, `""`},
		{"excess difficulty", `{"algorithm":"hashx","difficulty":21}`, `{"id":"id","randomData":"01"}`, `""`},
		{"missing id", `{"algorithm":"hashx","difficulty":1}`, `{"randomData":"01"}`, `""`},
		{"invalid id", `{"algorithm":"hashx","difficulty":1}`, `{"id":"../escape","randomData":"01"}`, `""`},
		{"external prefix", `{"algorithm":"fast","difficulty":1}`, `"sample"`, `"//elsewhere.example"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`<script id="anubis_base_prefix" type="application/json">%s</script><script id="anubis_challenge" type="application/json">{"rules":%s,"challenge":%s}</script>`, tc.prefix, tc.rules, tc.data)
			_, _, _, err := ParseResp([]byte(body), "https://example.org/testuser")
			require.Error(t, err)
		})
	}
}
