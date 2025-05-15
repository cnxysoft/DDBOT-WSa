package utils

import (
	"bytes"
	"errors"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils/blockCache"
	"net/url"
	"strings"
	"time"
)

var FileGetCache = blockCache.NewBlockCache(16, 25)

func FileGet(url string, opt ...requests.Option) ([]byte, *requests.RespHeader, error) {
	if url == "" {
		return nil, nil, errors.New("empty url")
	}
	var respHeader requests.RespHeader
	result := FileGetCache.WithCacheDo(url, func() blockCache.ActionResult {
		opts := []requests.Option{
			requests.TimeoutOption(time.Second * 15),
			requests.RetryOption(3),
		}
		opts = append(opts, opt...)

		var body = new(bytes.Buffer)

		err := requests.GetWithHeader(url, nil, body, &respHeader, opts...)
		if err != nil {
			return blockCache.NewResultWrapper(nil, err)
		}
		return blockCache.NewResultWrapper(body.Bytes(), nil)
	})
	if result.Err() != nil {
		return nil, nil, result.Err()
	}
	return result.Result().([]byte), &respHeader, nil
}

func FileGetWithoutCache(url string, opt ...requests.Option) ([]byte, *requests.RespHeader, error) {
	if url == "" {
		return nil, nil, errors.New("empty url")
	}
	var result []byte
	var err error
	opts := []requests.Option{
		requests.TimeoutOption(time.Second * 15),
		requests.RetryOption(3),
	}
	opts = append(opts, opt...)
	var body = new(bytes.Buffer)
	var rspHeader requests.RespHeader
	err = requests.GetWithHeader(url, nil, body, &rspHeader, opts...)
	if err != nil {
		return nil, nil, err
	}
	result, err = body.Bytes(), nil
	return result, &rspHeader, err
}

// ParseDisposition 解析 Content-Disposition 字符串，返回文件名以及可能的错误。
// 当同时存在 filename 和 filename* 参数时，优先返回 filename* 的值。
// 对于 filename* 参数，会解析其 RFC 5987 格式，去除 charset 和语言部分，并进行 URL 解码。
func ParseDisposition(contentDisposition string) (string, error) {
	var filename, filenameStar string
	var foundFilename, foundFilenameStar bool

	// 按分号分割，第一个部分通常为 disposition type（如 attachment 或 inline）
	parts := strings.Split(contentDisposition, ";")
	if len(parts) == 0 {
		return "", errors.New("invalid content disposition")
	}

	// 遍历各个参数部分
	for i := 1; i < len(parts); i++ {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}

		// 拆分键值对（只拆分第一个 "=" 号）
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.TrimSpace(kv[1])
		// 如果值被双引号包围，则去掉引号
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}

		// 根据参数名判断
		if key == "filename*" {
			filenameStar = value
			foundFilenameStar = true
		} else if key == "filename" {
			filename = value
			foundFilename = true
		}
	}

	// 优先返回 filename*
	if foundFilenameStar {
		// 如果符合 RFC 5987 格式（charset'lang'encodedValue），则去除 charset 和 lang 信息
		if i := strings.Index(filenameStar, "''"); i != -1 {
			encoded := filenameStar[i+2:]
			if decoded, err := url.QueryUnescape(encoded); err == nil {
				filenameStar = decoded
			} else {
				filenameStar = encoded
			}
		}
		return filenameStar, nil
	}
	if foundFilename {
		return filename, nil
	}
	return "", errors.New("filename parameter not found")
}
