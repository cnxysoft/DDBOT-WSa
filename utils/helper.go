package utils

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"github.com/andybalholm/brotli"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/guonaihong/gout"
	jsoniter "github.com/json-iterator/go"
	"github.com/klauspost/compress/zstd"
	"github.com/sirupsen/logrus"
	"golang.org/x/text/encoding/simplifiedchinese"
	"io"
	"io/fs"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

func FilePathWalkDir(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func reflectToString(v reflect.Value) (string, error) {
	if !v.IsValid() || v.IsZero() {
		return "", nil
	}
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), nil
	default:
		return "", fmt.Errorf("not support type %v", v.Type().Kind().String())
	}
}

func ToDatas(data interface{}) (map[string]string, error) {
	params := make(map[string]string)

	if m, ok := data.(map[string]string); ok {
		return m, nil
	}

	rg := reflect.ValueOf(data)
	for rg.Kind() == reflect.Ptr || rg.Kind() == reflect.Interface {
		rg = rg.Elem()
	}
	if rg.Kind() == reflect.Map {
		iter := rg.MapRange()
		for iter.Next() {
			key := iter.Key()
			value := iter.Value()
			k1, err := reflectToString(key)
			if err != nil {
				return nil, err
			}
			v1, err := reflectToString(value)
			if err != nil {
				return nil, err
			}
			params[k1] = v1
		}
	} else if rg.Kind() == reflect.Struct {
		for i := 0; ; i++ {
			if i >= rg.Type().NumField() {
				break
			}
			field := rg.Type().Field(i)
			fillname, found := field.Tag.Lookup("json")
			if !found {
				fillname = toCamel(field.Name)
			} else {
				if pos := strings.Index(fillname, ","); pos != -1 {
					fillname = fillname[:pos]
				}
			}
			if fillname == "-" {
				continue
			}
			s, err := reflectToString(rg.Field(i))
			if err != nil {
				return nil, err
			}
			params[fillname] = s
		}
	}
	return params, nil
}

func ToParams(data interface{}) (gout.H, error) {
	if p, ok := data.(gout.H); ok {
		return p, nil
	}
	params := make(gout.H)
	m, err := ToDatas(data)
	if err != nil {
		return nil, err
	}
	for k, v := range m {
		params[k] = v
	}
	return params, nil
}

func UrlEncode(data map[string]string) string {
	params := url.Values{}
	for k, v := range data {
		params.Add(k, v)
	}
	return params.Encode()
}

func toCamel(name string) string {
	if len(name) == 0 {
		return ""
	}
	sb := strings.Builder{}
	sb.WriteString(strings.ToLower(name[:1]))
	for _, c := range name[1:] {
		if c >= 'A' && c <= 'Z' {
			sb.WriteRune('_')
			sb.WriteRune(c - 'A' + 'a')
		} else {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func FuncName() string {
	pc := make([]uintptr, 15)
	n := runtime.Callers(2, pc)
	frames := runtime.CallersFrames(pc[:n])
	frame, _ := frames.Next()
	return frame.Function
}

// PrefixMatch 从 opts 中选择一个前缀是 prefix 的字符串，如果有多个选项，则返回 false
func PrefixMatch(opts []string, prefix string) (string, bool) {
	if len(opts) == 0 {
		return "", false
	}
	var (
		found  = false
		result = ""
	)
	for _, opt := range opts {
		if strings.HasPrefix(opt, prefix) {
			if found == true {
				return "", false
			}
			found = true
			result = opt
		}
	}
	return result, found
}

func UnquoteString(s string) (string, error) {
	return strconv.Unquote(fmt.Sprintf(`"%s"`, strings.Trim(s, `"`)))
}

func TimestampFormat(ts int64) string {
	t := time.Unix(ts, 0)
	return t.Format("2006-01-02 15:04:05")
}

func NTimestampFormat(ts int64) string {
	sec := ts / 1000
	nsec := (ts % 1000) * int64(time.Millisecond)
	t := time.Unix(sec, nsec)
	return t.Format("2006-01-02 15:04:05")
}
func Retry(count int, interval time.Duration, f func() bool) bool {
	for retry := 0; retry < count; retry++ {
		if f() {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

func ArgSplit(str string) (result []string) {
	r := regexp.MustCompile(`[^\s"]+|"([^"]*)"`)
	match := r.FindAllString(str, -1)
	for _, s := range match {
		result = append(result, strings.Trim(strings.TrimSpace(s), `" `))
	}
	return
}

func GroupLogFields(groupCode int64) logrus.Fields {
	var fields = make(logrus.Fields)
	fields["GroupCode"] = groupCode
	if groupInfo := GetBot().FindGroup(groupCode); groupInfo != nil {
		fields["GroupName"] = groupInfo.Name
	}
	return fields
}

func FriendLogFields(uin int64) logrus.Fields {
	var fields = make(logrus.Fields)
	fields["FriendUin"] = uin
	if info := GetBot().FindFriend(uin); info != nil {
		fields["FriendName"] = info.Nickname
	}
	return fields
}

func Switch2Bool(s string) bool {
	return s == "on"
}

func JoinInt64(ele []int64, sep string) string {
	var s []string
	for _, e := range ele {
		s = append(s, strconv.FormatInt(e, 10))
	}
	return strings.Join(s, sep)
}

var reHtmlTag = regexp.MustCompile(`<[^>]+>`)

func RemoveHtmlTag(s string) string {
	return reHtmlTag.ReplaceAllString(s, "")
}

func ParseTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation(time.DateTime, s, time.Local)
	return t, err
}

func ParseRespBody(resp bytes.Buffer, header requests.RespHeader) ([]byte, error) {
	// 解压缩HTML
	body, err := HtmlDecoder(header.ContentEncoding, resp)
	if err != nil {
		logger.WithField("FuncName", FuncName()).Errorf("解压缩HTML失败：%v", err)
		return nil, err
	}
	return body, nil
}

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

// ExecWithOption 执行命令，可以选择是否等待执行完成
func ExecWithOption(cmd string, args []string, wait bool) ([]byte, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, errors.New("command is empty")
	}

	// 查找命令的完整路径
	cmdPath, err := exec.LookPath(cmd)
	if err != nil {
		return nil, err
	}

	// 创建命令
	command := exec.Command(cmdPath, args...)

	if !wait {
		// 不等待执行完成，直接启动进程
		err := command.Start()
		if err != nil {
			return nil, err
		}
		// 直接返回空结果和nil错误，表示已成功启动
		return []byte("command started in background"), nil
	}

	// 获取命令输出
	output, err := command.CombinedOutput()

	// 处理 Windows 中文编码问题
	if runtime.GOOS == "windows" {
		// 尝试将 GBK/GB2312 编码转换为 UTF-8
		if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
			output = decoded
		}
	}

	return output, err
}

// ExecSilently 执行命令并静默运行（不显示进度条等信息）
func ExecSilently(cmd string, args []string) ([]byte, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, errors.New("command is empty")
	}

	// 查找命令的完整路径
	cmdPath, err := exec.LookPath(cmd)
	if err != nil {
		return nil, err
	}

	// 对于某些命令，添加静默参数
	silentArgs := make([]string, len(args))
	copy(silentArgs, args)

	switch cmd {
	case "curl":
		// 检查是否已经存在静默参数
		hasSilentFlag := false
		for _, arg := range args {
			if arg == "-s" || arg == "--silent" || arg == "-q" || arg == "--quiet" {
				hasSilentFlag = true
				break
			}
		}

		// 如果没有静默参数，则添加 -s
		if !hasSilentFlag {
			silentArgs = append([]string{"-s"}, silentArgs...)
		}
	case "wget":
		// 检查是否已经存在静默参数
		hasQuietFlag := false
		for _, arg := range args {
			if arg == "-q" || arg == "--quiet" {
				hasQuietFlag = true
				break
			}
		}

		// 如果没有静默参数，则添加 -q
		if !hasQuietFlag {
			silentArgs = append([]string{"-q"}, silentArgs...)
		}
	}

	// 创建命令
	command := exec.Command(cmdPath, silentArgs...)

	// 获取命令输出
	output, err := command.CombinedOutput()

	// 处理 Windows 中文编码问题
	if runtime.GOOS == "windows" {
		// 尝试将 GBK/GB2312 编码转换为 UTF-8
		if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
			output = decoded
		}
	}

	return output, err
}

// ExecWithElevation 在 Windows 上以管理员权限执行命令
func ExecWithElevation(cmd string, args []string, wait bool) ([]byte, error) {
	if runtime.GOOS != "windows" {
		// 非 Windows 系统不支持此功能
		return nil, errors.New("elevation is only supported on Windows")
	}

	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, errors.New("command is empty")
	}

	// 查找命令的完整路径
	cmdPath, err := exec.LookPath(cmd)
	if err != nil {
		// 如果找不到命令，可能是一个完整路径
		cmdPath = cmd
	}

	// 首先尝试使用 PowerShell 执行（适用于 Win7 SP1 及以上版本）
	output, psErr := execWithPowerShell(cmdPath, args, wait)
	if psErr == nil {
		return output, nil
	}

	// 如果 PowerShell 执行失败，尝试使用 runas 命令（兼容更老的系统）
	output, runasErr := execWithRunas(cmdPath, args, wait)
	if runasErr == nil {
		return output, nil
	}

	// 如果两种方式都失败，返回详细的错误信息
	return []byte(fmt.Sprintf("Failed to execute elevated command with PowerShell: %v, with runas: %v", psErr, runasErr)),
		errors.New("failed to execute elevated command with all available methods")
}

// execWithPowerShell 使用 PowerShell 执行需要提升权限的命令
func execWithPowerShell(cmdPath string, args []string, wait bool) ([]byte, error) {
	// 构建参数字符串，正确处理带空格的参数
	var escapedArgs []string
	for _, arg := range args {
		// 简单处理参数中的特殊字符
		escapedArg := strings.ReplaceAll(arg, "\"", "\\\"")
		if strings.Contains(arg, " ") {
			escapedArgs = append(escapedArgs, "\""+escapedArg+"\"")
		} else {
			escapedArgs = append(escapedArgs, escapedArg)
		}
	}

	argLine := strings.Join(escapedArgs, " ")

	// 构建 PowerShell 命令
	// 注意：在 PowerShell 中，Go 的 true/false 需要转换为 $true/$false
	psWait := "$false"
	if wait {
		psWait = "$true"
	}
	
	psCommand := fmt.Sprintf(`
		$ErrorActionPreference = "Stop"
		try {
			$psi = New-Object System.Diagnostics.ProcessStartInfo
			$psi.FileName = "%s"
			$psi.Arguments = "%s"
			$psi.Verb = "runas"
			$psi.WindowStyle = "Hidden"
			$psi.UseShellExecute = $true
			$proc = [System.Diagnostics.Process]::Start($psi)
			if ($true -eq %s) {
				$proc.WaitForExit()
				Exit $proc.ExitCode
			} else {
				Exit 0
			}
		} catch {
			Write-Output "Error: $($_.Exception.Message)"
			Exit 1
		}
	`, strings.ReplaceAll(cmdPath, "\"", "\\\""), argLine, psWait)

	// 使用 PowerShell 启动一个提升权限的进程
	psCmd := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command", psCommand)

	// 如果不需要等待，直接启动进程
	if !wait {
		err := psCmd.Start()
		if err != nil {
			return nil, err
		}
		return []byte("elevated command started in background"), nil
	}

	output, err := psCmd.CombinedOutput()

	// 处理 Windows 中文编码问题
	if runtime.GOOS == "windows" {
		// 尝试将 GBK/GB2312 编码转换为 UTF-8
		if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
			output = decoded
		}
	}

	return output, err
}

// execWithRunas 使用 runas 命令执行需要提升权限的命令（兼容更老的系统）
func execWithRunas(cmdPath string, args []string, wait bool) ([]byte, error) {
	// 构建参数字符串
	argLine := ""
	if len(args) > 0 {
		var escapedArgs []string
		for _, arg := range args {
			// 简单处理参数中的特殊字符
			escapedArg := strings.ReplaceAll(arg, "\"", "\\\"")
			if strings.Contains(arg, " ") {
				escapedArgs = append(escapedArgs, "\""+escapedArg+"\"")
			} else {
				escapedArgs = append(escapedArgs, escapedArg)
			}
		}
		argLine = strings.Join(escapedArgs, " ")
	}

	// 构建完整的命令行
	fullCommand := cmdPath
	if argLine != "" {
		fullCommand += " " + argLine
	}

	// 使用 runas 命令启动一个提升权限的进程
	// 注意：runas 需要用户手动输入密码，这里我们使用 /trustlevel:0x20000 参数尝试绕过
	runasCmd := exec.Command("runas", "/trustlevel:0x20000", fullCommand)

	// 如果不需要等待，直接启动进程
	if !wait {
		err := runasCmd.Start()
		if err != nil {
			return nil, err
		}
		return []byte("elevated command started in background"), nil
	}

	output, err := runasCmd.CombinedOutput()

	// 处理 Windows 中文编码问题
	if runtime.GOOS == "windows" {
		// 尝试将 GBK/GB2312 编码转换为 UTF-8
		if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
			output = decoded
		}
	}

	return output, err
}

// ExecWithShell 在 shell 中执行命令（兼容 Linux）
func ExecWithShell(cmd string, args []string, wait bool) ([]byte, error) {
	// 检查是否在类 Unix 环境中运行（Linux、macOS、Unix 等）
	isUnix := runtime.GOOS != "windows"

	// 在类 Unix 系统上使用 /bin/sh，在 Windows 上使用 cmd.exe
	shell := "/bin/sh"
	shellArg := "-c"
	if !isUnix {
		shell = "cmd.exe"
		shellArg = "/C"
	}

	// 构建完整的命令行
	// 对于类 Unix 系统，我们需要正确处理参数
	fullCommand := cmd
	if isUnix && len(args) > 0 {
		// 在 Unix 系统中，我们需要对参数进行适当的转义
		for _, arg := range args {
			// 对参数进行基本的 shell 转义
			if strings.ContainsAny(arg, " \t\n|&;()<>") {
				// 如果参数包含特殊字符，用引号包围并转义内部引号
				escapedArg := "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
				fullCommand += " " + escapedArg
			} else {
				fullCommand += " " + arg
			}
		}
	} else if !isUnix && len(args) > 0 {
		// 在 Windows 系统中，简单地用空格连接参数
		// 但需要对参数进行适当的转义
		for _, arg := range args {
			// Windows cmd.exe 转义处理
			if strings.ContainsAny(arg, " \t\n\v\"") {
				// 如果参数包含特殊字符，用双引号包围并转义内部双引号
				escapedArg := "\"" + strings.ReplaceAll(arg, "\"", "\"\"") + "\""
				fullCommand += " " + escapedArg
			} else {
				fullCommand += " " + arg
			}
		}
	}

	// 创建命令
	command := exec.Command(shell, shellArg, fullCommand)

	if !wait {
		// 不等待执行完成，直接启动进程
		err := command.Start()
		if err != nil {
			return nil, err
		}
		// 直接返回空结果和nil错误，表示已成功启动
		if isUnix {
			return []byte("shell command started in background"), nil
		} else {
			return []byte("cmd command started in background"), nil
		}
	}

	// 获取命令输出
	output, err := command.CombinedOutput()

	// 处理 Windows 中文编码问题
	if runtime.GOOS == "windows" {
		// 尝试将 GBK/GB2312 编码转换为 UTF-8
		if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
			output = decoded
		}
	}

	return output, err
}
