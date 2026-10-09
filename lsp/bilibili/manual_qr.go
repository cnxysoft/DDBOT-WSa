package bilibili

import (
	"errors"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
)

// 管理员确认后生成扫码二维码：
// authprompt 私聊管理员后回复“生成 B站”触发 runBilibiliQRLoginOnce；
// 默认模式写入配置，CookieCloud 模式下拒绝生成。
// qrcode/poll 状态码：86038 已失效、86090 已扫描待确认、86101 未扫描。

// runBilibiliQRLoginOnce 执行一次完整的扫码登录流程：
// 申请二维码 → 每秒轮询 → 登录导航 → 校验并写回配置。
// 由 authprompt 在管理员私聊确认后调用，同一个凭据不会并发生成。
func runBilibiliQRLoginOnce() error {
	if isBilibiliCookieCloudMode() {
		logger.Warn("当前为 CookieCloud 模式，Bot不会生成二维码；请在浏览器重新登录并等待CookieCloud同步。")
		return errors.New("CookieCloud 模式禁止生成B站二维码")
	}
	if !cfg.GetBilibiliQRLoginEnabled() {
		logger.Warn("当前配置不允许生成二维码，请更新Cookie或调整登录模式。")
		return errors.New("当前配置不允许生成二维码，请更新Cookie或调整登录模式。")
	}
	session := newQRLoginSession()
	qrResp, err := session.getQRCode()
	if err != nil {
		return err
	}
	logger.Info("B站登录二维码已按管理员确认生成，请使用哔哩哔哩 App 扫码")
	if err := writeQRCode(qrResp.Data.Url); err != nil {
		return err
	}
	for {
		resp, err := session.pollQRLogin(qrResp.Data.QrcodeKey)
		if err != nil {
			logger.Warn("B站扫码轮询请求失败，本轮继续")
			time.Sleep(qrLoginPollInterval)
			continue
		}
		switch resp.Data.Code {
		case 0:
			return finishBilibiliQRLogin(session, resp)
		case 86038:
			return errors.New("B站登录二维码已过期")
		case 86090, 86101:
			// 已扫描待确认 / 未扫描，继续轮询
		default:
			logger.Warnf("B站扫码轮询状态异常: %d %s", resp.Data.Code, resp.Data.Message)
		}
		time.Sleep(qrLoginPollInterval)
	}
}

// finishBilibiliQRLogin 完成扫码登录：导航回调、校验、写回配置并激活凭据。
func finishBilibiliQRLogin(session *qrLoginSession, resp *QRLoginResponse) error {
	cookies, err := session.completeQRLogin(resp.Data.Url)
	if err != nil {
		return err
	}
	var sessdata, biliJct, dedeUserID, buvid3 string
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		switch cookie.Name {
		case "SESSDATA":
			sessdata = cookie.Value
		case "bili_jct":
			biliJct = cookie.Value
		case "DedeUserID":
			dedeUserID = cookie.Value
		case "buvid3":
			buvid3 = cookie.Value
		}
	}
	if sessdata == "" || biliJct == "" || dedeUserID == "" {
		return errors.New("B站扫码响应中未取得Cookie")
	}
	// 实时校验：CookieCloud 与扫码都要求账号接口确认登录态
	verify := newVerifyInfoWithCookies(sessdata, biliJct, dedeUserID, buvid3)
	previous := atomicVerifyInfo.Load()
	atomicVerifyInfo.Store(verify)
	navResp, navErr := XWebInterfaceNav(true)
	if navErr != nil || navResp == nil || !navResp.GetData().GetIsLogin() {
		atomicVerifyInfo.Store(previous)
		if navErr == nil {
			logger.Warn("B站扫码Cookie实时校验未登录")
		}
		return errors.New("B站扫码Cookie实时校验未登录")
	}
	if buvid3 == "" {
		logger.Info("B站扫码登录成功，但暂时未取得buvid3；直播WebSocket会继续自动补取")
	}
	// 成品 runBilibiliQRLoginOnce @0xd3938b：Info "B站扫码登录成功，新Cookie已即时生效"。
	logger.Info("B站扫码登录成功，新Cookie已即时生效")
	// refresh_token 持久化为 bilibili.ac_time_value；DedeUserID/buvid3 一并写回
	// （成品 WriteConfigKeyValues 键集合，见 cookie_refresh.go:writeBilibiliCredentialConfig）。
	if err := writeBilibiliCredentialConfig(sessdata, biliJct, dedeUserID, buvid3, resp.Data.RefreshToken); err != nil {
		logger.Errorf("保存扫码登录结果失败 - %v", err)
	}
	atomicVerifyInfo.Store(verify)
	FreshSelfInfo()
	markBilibiliCookieHealthy()
	return nil
}
