package template

import (
	"fmt"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	assert.EqualValues(t, videoFile, e.Url)
	//assert.EqualValues(t, []byte{4, 5, 6, 7}, e.Buf)

	// 测试videoUri函数
	e = videoUri(tempDir)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{4, 5, 6, 7}, e.Buf)

	// 测试base64视频
	b64 := "AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29tYXZjMQAAAAAAAQAAAABJ//9tZGF0AAACngYJ//9sVQAA"
	e = video(b64)
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)
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
	assert.EqualValues(t, recordFile, e.Url)
	//assert.EqualValues(t, []byte{8, 9, 10, 11}, e.Buf)

	// 测试recordUri函数
	e = recordUri(tempDir)
	assert.NotNil(t, e)
	assert.EqualValues(t, []byte{8, 9, 10, 11}, e.Buf)

	// 测试base64音频
	b64 := "SUQzBAAAAAABAFRYWFgAAAASAAADbWFqb3JfYnJhbmQAbXA0MgAAAAxtaW5vcl92ZXJzaW9uAAB4"
	e = record(b64)
	assert.NotNil(t, e)
	assert.NotEmpty(t, e.Buf)
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
	return msgstringer.MsgToString(m.Elements()), err
}
