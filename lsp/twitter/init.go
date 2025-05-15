package twitter

import (
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

var (
	BaseURL     = []string{"https://lightbrd.com/", "https://nitter.net/"}
	UserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36 Edg/135.0.0.0"
	cfClearance = "MPhboJSYgu4mOYyOaczcIk9prFnvxhW7EVO30Z_cb0E-1745040790-1.2.1.1-ZJFr3vS0.hFcaPZFJFTI_OZIOeol36FEJBevzJYKR4qXANMYJpW3OxMbLfOAa.JUrzsj_1mF3xC51bLiUHiLrHRD1tMUzjr79Si1hXk5XyfUuSBeRGC7bOyT_FJqnHNsAPgbpHHmUwyoL5_LQI65m34JKQ1LHqlWZr019LVr9wcXKdVw0ojGIEd7neYLwV..A7Zkyu8HoXHo_myER.bliJyCMfxrF5sYl363ao8O1r5ZApgvWdtXEzho3kxM2DhCYs1CbO3D4I_1Z.h0YZ569i0O5i76o__eREcGspl5RTUoR.6wIs2VanDXHDVcVLBIEQavGN2SwEVkci5kCNWFeabFGiXU93VDok1SfrRUoaiGPDIVsQh5sHV7J.IvF1_n"
)

func init() {
	concern.RegisterConcern(newConcern(concern.GetNotifyChan()))
}

func setCookies() {
	ua := config.GlobalConfig.GetString("twitter.userAgent")
	ck := config.GlobalConfig.GetString("twitter.cfClearance")
	url := config.GlobalConfig.GetStringSlice("twitter.BaseUrl")
	if ua != "" {
		UserAgent = ua
	}
	if ck != "" {
		cfClearance = ck
	}
	if len(url) > 0 {
		BaseURL = url
	}
}
