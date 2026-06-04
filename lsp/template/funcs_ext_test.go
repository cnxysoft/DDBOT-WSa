package template

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/stretchr/testify/assert"
)

func TestExtRoll(t *testing.T) {
	var a = roll(0, 10)
	assert.True(t, a >= 0)
	assert.True(t, a <= 10)

	a = roll(-5, 5)
	assert.True(t, a >= -5)
	assert.True(t, a <= 5)

	a = roll(100, 100)
	assert.EqualValues(t, 100, a)
}

func TestPrefix(t *testing.T) {
	// 测试默认前缀
	p := prefix()
	assert.Equal(t, "/", p)

	// 测试特定命令前缀
	p = prefix("help")
	assert.Equal(t, "/help", p)
}

func TestCut(t *testing.T) {
	c := cut()
	assert.NotNil(t, c)
	assert.IsType(t, &mmsg.CutElement{}, c)
}

func TestTimeFuncs(t *testing.T) {
	// 测试getTime函数
	now := time.Now()
	s := getTime(now, "dateonly")
	assert.Equal(t, now.Format(time.DateOnly), s)

	s = getTime(now, "timeonly")
	assert.Equal(t, now.Format(time.TimeOnly), s)

	s = getTime(now, "stamp")
	assert.Equal(t, now.Format(time.Stamp), s)

	s = getTime(now, "unix")
	assert.Equal(t, fmt.Sprintf("%d", now.Unix()), s)

	s = getTime(now, "")
	assert.Equal(t, now.Format(time.DateTime), s)

	// 测试字符串时间
	s = getTime("now", "dateonly")
	_, err := time.Parse(time.DateOnly, s)
	assert.NoError(t, err)

	s = getTime("2021-01-01 12:00:00", "dateonly")
	assert.Equal(t, "2021-01-01", s)

	// 测试时间戳
	s = getTime(now.Unix(), "dateonly")
	assert.Equal(t, now.Format(time.DateOnly), s)

	// 测试时间过程
	s = getTime(1757511476, "elapsed")
	assert.NotNil(t, s)

	// 测试getUnixTime
	s = getUnixTime(now.Unix(), "dateonly")
	assert.Equal(t, now.Format(time.DateOnly), s)

	// 测试getTimeStamp
	ts := getTimeStamp("2021-01-01 12:00:00")
	expected, _ := time.ParseInLocation(time.DateTime, "2021-01-01 12:00:00", time.Local)
	assert.Equal(t, expected.Unix(), ts)
}

func TestFormatDuration(t *testing.T) {
	assert.Equal(t, "", formatDuration(0))
	assert.Equal(t, "59秒", formatDuration(59))
	assert.Equal(t, "2分钟27秒", formatDuration(int64(147)))
	assert.Equal(t, "3小时13分钟0秒", formatDuration("11580"))
}

func TestFileFuncs(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	// 创建临时目录和文件用于测试
	tempDir, err := os.MkdirTemp("", "ddbot_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	tempFile := filepath.Join(tempDir, "test.txt")
	content := "hello world"

	// 测试writeFile
	err = writeFile(tempFile, content)
	assert.NoError(t, err)

	// 测试openFile
	data := openFile(tempFile)
	assert.Equal(t, []byte(content), data)

	// 测试updateFile
	err = updateFile(tempFile, "\nnew line")
	assert.NoError(t, err)

	// 验证追加内容
	data = openFile(tempFile)
	assert.Equal(t, []byte(content+"\nnew line"), data)

	// 测试delFile
	err = delFile(tempFile)
	assert.NoError(t, err)
	_, err = os.Stat(tempFile)
	assert.True(t, os.IsNotExist(err))

	// 测试renameFile
	oldFile := filepath.Join(tempDir, "old.txt")
	newFile := filepath.Join(tempDir, "new.txt")
	err = writeFile(oldFile, content)
	assert.NoError(t, err)

	err = renameFile(oldFile, newFile)
	assert.NoError(t, err)
	_, err = os.Stat(oldFile)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(newFile)
	assert.NoError(t, err)

	// 测试readLine
	multiLineFile := filepath.Join(tempDir, "multiline.txt")
	multiLineContent := "line1\nline2\nline3\n"
	err = writeFile(multiLineFile, multiLineContent)
	assert.NoError(t, err)

	line := readLine(multiLineFile, 1)
	assert.Equal(t, "line1\n", line)

	line = readLine(multiLineFile, 2)
	assert.Equal(t, "line2\n", line)

	// 测试findReadLine
	line = findReadLine(multiLineFile, "line2")
	assert.Equal(t, "line2\n", line)

	// 测试uriEncode/uriDecode
	encoded := uriEncode("hello world")
	assert.Equal(t, "hello+world", encoded)

	decoded, err := uriDecode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, "hello world", decoded)
}

func TestPicFuncs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ddbot_pic_test")
	assert.Nil(t, err)
	defer os.RemoveAll(tempDir)

	// 创建测试图片文件
	imgFile := filepath.Join(tempDir, "test.jpg")
	f, err := os.Create(imgFile)
	assert.Nil(t, err)
	f.Write([]byte{0, 1, 2, 3})
	f.Close()

	// 测试pic函数
	e := pic(imgFile)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{0, 1, 2, 3}, e.Buf)

	// 测试picUri函数
	e = picUri(tempDir)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{0, 1, 2, 3}, e.Buf)

	// 测试base64图片
	b64 := "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7" // 1x1透明gif的base64
	e = pic(b64)
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)

	// 测试icon函数
	e = icon(10000)
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)
}

func TestPicFuncs_WithParamsAndAlternative(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"))
	}))
	defer server.Close()

	e := pic(server.URL, "alt text", map[string]interface{}{
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.Equal(t, server.URL, e.Url)
	assert.Nil(t, e.Buf)

	e = pic(server.URL, "alt text", map[string]interface{}{
		DDBOT_REQ_FETCH: "local",
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)
}

func TestPicxFuncs(t *testing.T) {
	var hitCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"))
	}))
	defer server.Close()

	e := picx(server.URL, map[string]interface{}{
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)

	e = picx(server.URL, "alt text")
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)

	e = pic(server.URL, "alt text", fetchLocal(), proxyNone())
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)

	e = picx(server.URL, proxyNone())
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)
	assert.EqualValues(t, 1, atomic.LoadInt32(&hitCount))
}

func TestPicmFuncs_MergeImageList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"))
	}))
	defer server.Close()

	e := picm([]string{
		server.URL + "/1",
		server.URL + "/2",
		server.URL + "/3",
		server.URL + "/4",
	}, "merged")
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)

	e = picmx([]interface{}{
		server.URL + "/1",
		server.URL + "/2",
		server.URL + "/3",
		server.URL + "/4",
	}, "merged", proxyNone())
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)
}

func TestVideoFuncs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ddbot_video_test")
	assert.Nil(t, err)
	defer os.RemoveAll(tempDir)

	// 创建测试视频文件
	videoFile := filepath.Join(tempDir, "test.mp4")
	f, err := os.Create(videoFile)
	assert.Nil(t, err)
	f.Write([]byte{4, 5, 6, 7})
	f.Close()

	// 测试video函数
	e := video(videoFile)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{4, 5, 6, 7}, e.Buf)

	// 测试videoUri函数
	e = videoUri(tempDir)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{4, 5, 6, 7}, e.Buf)

	// 测试base64视频
	b64 := "AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29tYXZjMQAAAAAAAQAAAABJ//9tZGF0AAACngYJ//9sVQAA"
	e = video(b64)
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte{4, 5, 6, 7})
	}))
	defer server.Close()

	e = video(server.URL, map[string]interface{}{
		DDBOT_REQ_FETCH: "local",
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{4, 5, 6, 7}, e.Buf)

	e = video(server.URL, "alt text", map[string]interface{}{
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.Equal(t, server.URL, e.Url)
	assert.Nil(t, e.Buf)

	e = videox(server.URL, "alt text")
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{4, 5, 6, 7}, e.Buf)
}

func TestRecordFuncs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ddbot_record_test")
	assert.Nil(t, err)
	defer os.RemoveAll(tempDir)

	// 创建测试音频文件
	recordFile := filepath.Join(tempDir, "test.mp3")
	f, err := os.Create(recordFile)
	assert.Nil(t, err)
	f.Write([]byte{8, 9, 10, 11})
	f.Close()

	// 测试record函数
	e := record(recordFile)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{8, 9, 10, 11}, e.Buf)

	// 测试recordUri函数
	e = recordUri(tempDir)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{8, 9, 10, 11}, e.Buf)

	// 测试base64音频
	b64 := "SUQzBAAAAAABAFRYWFgAAAASAAADbWFqb3JfYnJhbmQAbXA0MgAAAAxtaW5vcl92ZXJzaW9uAAB4"
	e = record(b64)
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte{8, 9, 10, 11})
	}))
	defer server.Close()

	e = record(server.URL, map[string]interface{}{
		DDBOT_REQ_FETCH: "local",
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{8, 9, 10, 11}, e.Buf)

	e = record(server.URL, "alt text", map[string]interface{}{
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.Equal(t, server.URL, e.Url)
	assert.Nil(t, e.Buf)

	e = recordx(server.URL, "alt text")
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{8, 9, 10, 11}, e.Buf)
}

func TestFilexFuncs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="sample.bin"`)
		_, _ = w.Write([]byte{12, 13, 14, 15})
	}))
	defer server.Close()

	e := file(server.URL, map[string]interface{}{
		DDBOT_REQ_FETCH: "local",
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{12, 13, 14, 15}, e.Buf)

	e = file(server.URL, "alt text", map[string]interface{}{
		DDBOT_REQ_PROXY: "prefer_none",
	})
	assert.NotNil(t, e)
	assert.Equal(t, server.URL, e.Url)
	assert.Nil(t, e.Buf)

	e = filex(server.URL, "alt text")
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{12, 13, 14, 15}, e.Buf)
}

func TestCooldownFuncs(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	// 测试cooldown函数
	result := cooldown("2s", "test_cooldown")
	assert.True(t, result)

	result = cooldown("2s", "test_cooldown")
	assert.False(t, result)

	// 等待过期
	time.Sleep(time.Second * 3)

	result = cooldown("2s", "test_cooldown")
	assert.True(t, result)

	// 测试setCooldown函数
	result = setCooldown("2s", "test_set_cooldown")
	assert.True(t, result)

	result = setCooldown("2s", "test_set_cooldown")
	assert.True(t, result) // setCooldown总是返回true，即使覆盖

	time.Sleep(time.Second * 3)

	result = setCooldown("2s", "test_set_cooldown")
	assert.True(t, result)
}

func TestExecTemplateWithFuncs(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	// 测试一些函数在模板中的使用
	templateContent := `{{- if (cooldown "2s" "template_test") -}}
first execution
{{- else -}}
duplicate execution
{{- end -}}`

	s, err := runTemplateWithExt(templateContent, nil)
	assert.Nil(t, err)
	assert.EqualValues(t, "first execution", s)

	s, err = runTemplateWithExt(templateContent, nil)
	assert.Nil(t, err)
	assert.EqualValues(t, "duplicate execution", s)

	// 测试roll函数
	templateContent = `{{- $val := roll 1 10 -}}
{{- if and (ge $val 1) (le $val 10) -}}
valid roll
{{- else -}}
invalid roll
{{- end -}}`

	s, err = runTemplateWithExt(templateContent, nil)
	assert.Nil(t, err)
	assert.EqualValues(t, "valid roll", s)

	// 测试pic函数
	tempDir, err := os.MkdirTemp("", "ddbot_template_test")
	assert.Nil(t, err)
	defer os.RemoveAll(tempDir)

	imgFile := filepath.Join(tempDir, "test.jpg")
	f, err := os.Create(imgFile)
	assert.Nil(t, err)
	f.Write([]byte{0, 1, 2, 3})
	f.Close()

	templateContent = `{{- $e := pic .path -}}
{{- if $e -}}
image loaded
{{- else -}}
image not loaded
{{- end -}}`

	s, err = runTemplateWithExt(templateContent, map[string]interface{}{"path": imgFile})
	assert.Nil(t, err)
	assert.EqualValues(t, "image loaded", s)
}

func runTemplateWithExt(template string, data map[string]interface{}) (string, error) {
	var m = mmsg.NewMSG()
	var tmpl = Must(New("").Funcs(FuncMap(funcsExt)).Parse(template))
	var err = tmpl.Execute(m, data)
	return msgstringer.AdapterMsgToString(m.Elements()), err
}

// 辅助函数：比较年月日和时分
func sameDateTime(a, b time.Time) bool {
	return a.Year() == b.Year() &&
		a.Month() == b.Month() &&
		a.Day() == b.Day() &&
		a.Hour() == b.Hour() &&
		a.Minute() == b.Minute()
}

func TestGetTime_WithBase(t *testing.T) {
	// 固定基准时间：2025-11-15 12:00
	base := time.Date(2025, 11, 15, 12, 0, 0, 0, time.Local)

	tests := []struct {
		input interface{}
		f     string
		base  interface{}
		want  time.Time
	}{
		// 绝对时间
		{
			input: "2025-11-16 16:00:00",
			f:     "dateTime",
			base:  nil,
			want:  time.Date(2025, 11, 16, 16, 0, 0, 0, time.Local),
		},
		// 相对时间：今天
		{
			input: "今天 16:00",
			f:     "dateTime",
			base:  base,
			want:  time.Date(2025, 11, 15, 16, 0, 0, 0, time.Local),
		},
		// 相对时间：明天
		{
			input: "明天 20:00",
			f:     "dateTime",
			base:  base,
			want:  time.Date(2025, 11, 16, 20, 0, 0, 0, time.Local),
		},
		// 相对时间：后天
		{
			input: "后天 18:30",
			f:     "dateTime",
			base:  base,
			want:  time.Date(2025, 11, 17, 18, 30, 0, 0, time.Local),
		},
		// 简化日期
		{
			input: "11-22 20:00",
			f:     "dateTime",
			base:  base,
			want:  time.Date(2025, 11, 22, 20, 0, 0, 0, time.Local),
		},
		// Unix 时间戳输入
		{
			input: int64(base.Unix()),
			f:     "dateTime",
			base:  nil,
			want:  base,
		},
	}

	for _, tt := range tests {
		var gotStr string
		if tt.base != nil {
			gotStr = getTime(tt.input, tt.f, tt.base)
		} else {
			gotStr = getTime(tt.input, tt.f)
		}
		got, err := time.ParseInLocation(time.DateTime, gotStr, time.Local)
		if err != nil {
			t.Errorf("parse result failed for input %v: %v", tt.input, err)
			continue
		}
		if !sameDateTime(got, tt.want) {
			t.Errorf("getTime(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
