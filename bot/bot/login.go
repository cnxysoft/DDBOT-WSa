package bot

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"github.com/Mrs4s/MiraiGo/utils"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/guonaihong/gout"
	"github.com/mattn/go-colorable"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

var console = bufio.NewReader(os.Stdin)

func energy(uin uint64, id string, appVersion string, salt []byte) ([]byte, error) {
	signServer := config.GlobalConfig.GetString("sign-server")
	if !strings.HasSuffix(signServer, "/") {
		signServer += "/"
	}
	resp, err := http.Get(signServer + "custom_energy" + fmt.Sprintf("?data=%v&salt=%v", id, hex.EncodeToString(salt)))
	if err != nil {
		logger.Warnf("获取T544 sign时出现错误: %v server: %v", err, signServer)
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Warnf("获取T544 sign时出现错误: %v server: %v", err, signServer)
		return nil, err
	}
	data, err = hex.DecodeString(gjson.GetBytes(data, "data").String())
	if err != nil {
		logger.Warnf("获取T544 sign时出现错误: %v", err)
		return nil, err
	}
	if len(data) == 0 {
		logger.Warnf("获取T544 sign时出现错误: %v", "data is empty")
		return nil, errors.New("data is empty")
	}
	return data, nil
}

func sign(seq uint64, uin string, cmd string, qua string, buff []byte) (sign []byte, extra []byte, token []byte, err error) {
	signServer := config.GlobalConfig.GetString("sign-server")
	if !strings.HasSuffix(signServer, "/") {
		signServer += "/"
	}
	req, err := http.NewRequest(http.MethodPost, signServer+"sign", bytes.NewReader([]byte(fmt.Sprintf("uin=%v&qua=%s&cmd=%s&seq=%v&buffer=%v", uin, qua, cmd, seq, hex.EncodeToString(buff)))))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Body = io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf("uin=%v&qua=%s&cmd=%s&seq=%v&buffer=%v", uin, qua, cmd, seq, hex.EncodeToString(buff)))))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Warnf("获取sso sign时出现错误: %v server: %v", err, signServer)
		return nil, nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Warnf("获取sso sign时出现错误: %v server: %v", err, signServer)
		return nil, nil, nil, err
	}
	sign, _ = hex.DecodeString(gjson.GetBytes(data, "data.sign").String())
	extra, _ = hex.DecodeString(gjson.GetBytes(data, "data.extra").String())
	token, _ = hex.DecodeString(gjson.GetBytes(data, "data.token").String())
	return sign, extra, token, nil
}

func fetchCaptcha(id string) string {
	var b []byte
	err := gout.GET("https://captcha.go-cqhttp.org/captcha/ticket?id=" + id).BindBody(&b).Do()
	//g, err := download.Request{URL: "https://captcha.go-cqhttp.org/captcha/ticket?id=" + id}.JSON()
	if err != nil {
		logger.Debugf("获取 Ticket 时出现错误: %v", err)
		return ""
	}
	if gt := gjson.GetBytes(b, "ticket"); gt.Exists() {
		return gt.String()
	}
	return ""
}

func readLine() (str string) {
	str, _ = console.ReadString('\n')
	str = strings.TrimSpace(str)
	return
}

func readLineTimeout(t time.Duration, de string) (str string) {
	r := make(chan string)
	go func() {
		select {
		case r <- readLine():
		case <-time.After(t):
		}
	}()
	str = de
	select {
	case str = <-r:
	case <-time.After(t):
	}
	return
}

// ErrSMSRequestError SMS请求出错
var ErrSMSRequestError = errors.New("sms request error")

func commonLogin() error {
	// 使用适配器模式，不需要传统的登录
	return nil
}

func printQRCode(imgData []byte) {
	const (
		black = "\033[48;5;0m  \033[0m"
		white = "\033[48;5;7m  \033[0m"
	)
	img, err := png.Decode(bytes.NewReader(imgData))
	if err != nil {
		log.Panic(err)
	}
	data := img.(*image.Gray).Pix
	bound := img.Bounds().Max.X
	buf := make([]byte, 0, (bound*4+1)*(bound))
	i := 0
	for y := 0; y < bound; y++ {
		i = y * bound
		for x := 0; x < bound; x++ {
			if data[i] != 255 {
				buf = append(buf, white...)
			} else {
				buf = append(buf, black...)
			}
			i++
		}
		buf = append(buf, '\n')
	}
	_, _ = colorable.NewColorableStdout().Write(buf)
}

func qrcodeLogin() error {
	// 使用适配器模式，不需要二维码登录
	logger.Info("Adapter mode: QR code login skipped")
	return nil
}

func getTicket(u string) string {
	logger.Warnf("请选择提交滑块ticket方式:")
	logger.Warnf("1. 自动提交")
	logger.Warnf("2. 手动抓取提交")
	logger.Warn("请输入(1 - 2)：")
	text := readLine()
	id := utils.RandomString(8)
	auto := !strings.Contains(text, "2")
	if auto {
		u = strings.ReplaceAll(u, "https://ssl.captcha.qq.com/template/wireless_mqq_captcha.html?", fmt.Sprintf("https://captcha.go-cqhttp.org/captcha?id=%v&", id))
	}
	logger.Warnf("请前往该地址验证 -> %v ", u)
	if !auto {
		logger.Warn("请输入ticket： (Enter 提交)")
		return readLine()
	}

	for count := 120; count > 0; count-- {
		str := fetchCaptcha(id)
		if str != "" {
			return str
		}
		time.Sleep(time.Second)
	}
	logger.Warnf("验证超时")
	return ""
}

func loginResponseProcessor(res interface{}) error {
	// 使用适配器模式，不需要处理登录响应
	return nil
}
