package bilibili

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// 动态异常响应落盘调试：
// 网页动态接口请求失败、非 200、空响应或非 JSON 时，把响应原文保存为 TXT，
// 交给开发者分析；文件名带时间戳，避免相互覆盖。
const (
	// dynamicResponseDumpDir 是异常响应的保存目录。
	dynamicResponseDumpDir = ".bilibili-dynamic-debug"
)

// requestDynamicJSON 请求网页动态接口并解析 JSON 响应：
// 记录响应字节数与 Content-Length，非 200、空响应、非 JSON 响应都会落盘调试。
// params 为 query 参数，opts 携带 UA/Referer/设备头与登录 Cookie。
func requestDynamicJSON(rawURL string, params map[string]string, out interface{}, opts []requests.Option) error {
	var body bytes.Buffer
	var respHeaders requests.RespHeader
	var statusCode int
	allOpts := append(opts, requests.HttpCodeOption(&statusCode))
	err := requests.GetWithHeader(rawURL, params, &body, &respHeaders, allOpts...)
	if err != nil {
		writeDynamicResponseErrorDump(rawURL, statusCode, respHeaders, body.Bytes(), err)
		if statusCode != 0 {
			return &webDynamicAPIError{
				Code:    int32(statusCode),
				Message: fmt.Sprintf("HTTP %d", statusCode),
			}
		}
		return fmt.Errorf("请求B站动态接口失败: %w", err)
	}
	if len(body.Bytes()) == 0 {
		writeDynamicResponseErrorDump(rawURL, statusCode, respHeaders, body.Bytes(), nil)
		logger.WithFields(map[string]interface{}{
			"raw_body_bytes":        0,
			"decoded_body_bytes":    0,
			"content_length_header": respHeaders.ContentLength,
		}).Warn("B站动态接口返回空响应")
		return fmt.Errorf("B站动态接口返回空响应")
	}
	if first := body.Bytes()[0]; first != '{' && first != '[' {
		writeDynamicResponseErrorDump(rawURL, statusCode, respHeaders, body.Bytes(), nil)
		logger.Warnf("B站动态接口返回非JSON内容，首字节=0x%02x", first)
		return fmt.Errorf("B站动态接口返回非JSON内容，首字节=0x%02x", first)
	}
	if err := json.Unmarshal(body.Bytes(), out); err != nil {
		writeDynamicResponseErrorDump(rawURL, statusCode, respHeaders, body.Bytes(), err)
		return fmt.Errorf("解析B站动态JSON失败: %w", err)
	}
	return nil
}

// writeDynamicResponseErrorDump 把异常响应写入 TXT 文件并提示开发者。
// 响应原文（含状态与头）与错误一起保存；失败只记日志，不影响主流程错误返回。
func writeDynamicResponseErrorDump(target string, statusCode int, respHeaders requests.RespHeader, body []byte, parseErr error) {
	name := fmt.Sprintf("%s/bilibili_dynamic_%s.txt", dynamicResponseDumpDir, time.Now().Format("20060102_150405.000000000"))
	if err := dumpDynamicResponseError(name, target, statusCode, respHeaders, body, parseErr); err != nil {
		logger.WithField("error", err.Error()).Error("保存B站动态异常响应失败")
		return
	}
	logger.WithField("dump_file", name).
		Info("B站动态接口异常响应已保存为TXT，请将该文件交给开发者分析")
}

// dumpDynamicResponseError 组装并落盘：请求地址、状态、响应头、错误与响应原文。
func dumpDynamicResponseError(name string, target string, statusCode int, respHeaders requests.RespHeader, body []byte, parseErr error) error {
	var builder strings.Builder
	fmt.Fprintf(&builder, "time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&builder, "url: %s\n", target)
	fmt.Fprintf(&builder, "status: %d\n", statusCode)
	fmt.Fprintf(&builder, "raw_body_bytes: %d\n", len(body))
	fmt.Fprintf(&builder, "decoded_body_bytes: %d\n", len(body))
	fmt.Fprintf(&builder, "content_length_header: %s\n", respHeaders.ContentLength)
	fmt.Fprintf(&builder, "content_type_header: %s\n", respHeaders.ContentType)
	fmt.Fprintf(&builder, "content_encoding_header: %s\n", respHeaders.ContentEncoding)
	if parseErr != nil {
		fmt.Fprintf(&builder, "error: %v\n", parseErr)
	}
	builder.WriteString("\n---- body ----\n")
	builder.Write(body)
	builder.WriteString("\n")
	if err := os.MkdirAll(dynamicResponseDumpDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, []byte(builder.String()), 0o644)
}
