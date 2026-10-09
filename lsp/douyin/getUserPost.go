package douyin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

const PathGetUserPosts = "/aweme/v1/web/aweme/post/"

// GetPosts 拉取用户作品列表：单次请求由 getPostsOnce 完成，本函数负责临时风控重试
// 与认证失败告警。
//
// 成品 @getUserPost.go:15（2112B）逐条证据：
//   - 重试循环上界 `cmp $0x3,%rcx`（try 1..3），每轮 @+0xc0 call getPostsOnce；
//   - 成功（error 接口 rsi==0）：@+0x38b markDouyinCookieHealthyFor("news"
//     @0x109955f,len4)、@+0x3a2 rememberDouyinPostExtras(raw)、返回 (resp, nil)；
//   - isDouyinRetryableRiskFailure(err) @+0xe0 命中且 attempt!=3 → Warn 文案
//     "抖音作品列表请求被临时拒绝，将保留完整Cookie并刷新动态签名重试"
//     （0x11261d0,len90；fields userId/attempt/next_attempt/retry_after）
//     然后 time.Sleep(attempt 秒)（@+0x88：imul $0x3b9aca00，attempt*1e9ns）；
//   - 重试耗尽或非重试：logger.WithField("userId", id)（0x109f796,len6）后
//     isDouyinRetryableRiskFailure(err) @+0x4ce，命中 → Error
//     "抖音作品接口连续被拒绝；本次仅判定为动态接口风控，不会误报Cookie失效"
//     （0x11261f0,len99；fields signature_mode=dynamic_current、cookie_invalid=false）；
//   - 否则 isDouyinAuthFailure(err) @+0x640，命中 → @+0x6a1 Logf、@+0x6cd
//     markDouyinCookieUnhealthyFor("news", err.Error())，随后 @+0x6d2 currentDouyinCookieJar
//     / @+0x6e1 hasDouyinLoginCookie 作 login_cookie_ready 字段并 Error
//     "抖音作品接口返回认证错误，请更新完整 Cookie；程序会保留去重基线并在下轮重试"
//     （0x1126200,len109）；
//   - 其余情况 logger.Errorf("获取用户作品列表失败：%v", err)（0x10cbb4f,len35）。
func GetPosts(id string) (*UserPostsResponse, error) {
	for attempt := 1; attempt <= 3; attempt++ {
		resp, raw, err := getPostsOnce(id)
		if err == nil {
			markDouyinCookieHealthyFor(douyinFailureScopeNews)
			rememberDouyinPostExtras(raw)
			return resp, nil
		}
		if isDouyinRetryableRiskFailure(err) && attempt < 3 {
			retryAfter := time.Duration(attempt) * time.Second
			logger.WithFields(logrus.Fields{
				"userId":       id,
				"attempt":      attempt,
				"next_attempt": attempt + 1,
				"retry_after":  retryAfter,
			}).Warn("抖音作品列表请求被临时拒绝，将保留完整Cookie并刷新动态签名重试")
			time.Sleep(retryAfter)
			continue
		}
		log := logger.WithField("userId", id)
		if isDouyinRetryableRiskFailure(err) {
			log.WithFields(logrus.Fields{
				"signature_mode": "dynamic_current",
				"cookie_invalid": false,
			}).Error("抖音作品接口连续被拒绝；本次仅判定为动态接口风控，不会误报Cookie失效")
		} else if isDouyinAuthFailure(err) {
			markDouyinCookieUnhealthyFor(douyinFailureScopeNews, err.Error())
			log.WithField("login_cookie_ready", hasDouyinLoginCookie(currentDouyinCookieJar())).
				Error("抖音作品接口返回认证错误，请更新完整 Cookie；程序会保留去重基线并在下轮重试")
		} else {
			log.Errorf("获取用户作品列表失败：%v", err)
		}
		return nil, err
	}
	return nil, nil
}

// getPostsOnce 以带 a_bogus 签名的作品列表接口请求一次，并返回原始 JSON 供图集解析。
// 证据：成品存在 douyin.getPostsOnce @ getUserPost.go:54（Lite 增量，2176B），
// 且带 getPostsOnce.GetWithHeader.func2 / getPostsOnce.HttpCodeOption.func1 闭包符号；
// 文档 DOUYIN_FLOW「请求带 A-Bogus 的用户作品列表接口」。
// 本实现复用重建版既有的签名请求路径（buildSignedDouyinUserPostsURL + 严格串行调度）。
func getPostsOnce(secUID string) (*UserPostsResponse, json.RawMessage, error) {
	rawURL := buildSignedDouyinUserPostsURL(secUID, douyinUserPostsCount, "0")
	trace := beginDouyinAPIRequest(douyinRequestTypeSignedUserPosts, http.MethodGet, rawURL)
	release, err := acquireDouyinRequestSlotWithWait(context.Background(), trace)
	if err != nil {
		return nil, nil, fmt.Errorf("获取用户作品列表失败：%w", err)
	}
	defer release()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	opts := douyinWebAPIRequestOptions(trace)
	opts = append(opts, requests.HeaderOption("Referer", BaseHost+"/user/"+secUID))
	statusCode := 0
	opts = append(opts, requests.HttpCodeOption(&statusCode))
	reqErr := requests.GetWithHeader(rawURL, nil, &resp, &respHeaders, opts...)
	trace.finish(reqErr)
	if reqErr != nil {
		// 成品 getPostsOnce callees 不含 markDouyinCookieUnhealthyFor；news 的认证失败
		// 告警由上层 GetPosts @getUserPost.go:15 +0x6cd 负责（scope "news"）。
		return nil, nil, fmt.Errorf("获取用户作品列表失败：%w", reqErr)
	}
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		return nil, nil, fmt.Errorf("解压缩抖音作品列表失败: %w", err)
	}
	posts := new(UserPostsResponse)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}
	if err := protoJsonOpts.Unmarshal(body, posts); err != nil {
		return nil, nil, fmt.Errorf("解析抖音作品列表失败: %w", err)
	}
	if posts.StatusCode != 0 {
		logger.WithField("RespCode", posts.StatusCode).Errorf("DynamicSvrDynamicNew failed")
		return nil, nil, fmt.Errorf("抖音作品列表返回 status_code=%d", posts.StatusCode)
	}
	return posts, json.RawMessage(body), nil
}

func isUnderReview(s *UserPostsResponse_Status) bool {
	if s == nil {
		return true
	}
	if s.GetInReviewing() {
		return true
	}
	review := s.GetReviewResult()
	if review == nil || review.GetReviewStatus() != 0 {
		return true
	}
	return false
}
