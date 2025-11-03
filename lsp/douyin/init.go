package douyin

import (
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"net/http/cookiejar"
	"strings"
)

var (
	UserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36 Edg/135.0.0.0"
	AcSignature = ""
	AcNonce     = ""
	Stop        = false
)

func init() {
	concern.RegisterConcern(NewConcern(concern.GetNotifyChan()))
}

func setCookies() {
	ua := config.GlobalConfig.GetString("douyin.userAgent")
	as := config.GlobalConfig.GetString("douyin.acSignature")
	an := config.GlobalConfig.GetString("douyin.acNonce")
	Cookie, _ = cookiejar.New(nil)
	if ua != "" {
		UserAgent = ua
	}
	if as != "" {
		AcSignature = as
	} else {
		Stop = true
	}
	if an != "" {
		AcNonce = an
	} else {
		Stop = true
	}
}

func DPath(path string) string {
	if strings.HasPrefix(path, "/") {
		return BasePath[path] + path
	} else {
		return BasePath[path] + "/" + path
	}
}
