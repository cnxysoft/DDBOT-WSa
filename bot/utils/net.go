package utils

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"io"
)

var logger = GetModuleLogger("bot-utils")

func HtmlDecoder(ContentEncoding string, resp bytes.Buffer) ([]byte, error) {
	var body []byte
	if encoding := ContentEncoding; encoding != "" {
		body = resp.Bytes()
		switch encoding {
		case "gzip":
			body, _ = decompressGzip(body)
		case "deflate":
			body, _ = decompressDeflate(body)
		case "br":
			body, _ = decompressBrotli(body)
		case "zstd":
			body, _ = decompressZstd(body)
		default:
			logger.Warnf("不支持的压缩格式: %s", encoding)
		}
	} else {
		body = resp.Bytes()
	}
	return body, nil
}

// 解压HTTP数据
func decompressGzip(data []byte) ([]byte, error) {
	var b bytes.Buffer
	r, _ := gzip.NewReader(bytes.NewReader(data))
	_, _ = io.Copy(&b, r)
	r.Close()
	return b.Bytes(), nil
}

func decompressDeflate(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func decompressBrotli(data []byte) ([]byte, error) {
	reader := brotli.NewReader(bytes.NewReader(data))
	return io.ReadAll(reader)
}

func decompressZstd(data []byte) ([]byte, error) {
	dctx, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer dctx.Close()
	return io.ReadAll(dctx)
}
