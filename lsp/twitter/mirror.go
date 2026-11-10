package twitter

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

const mirrorChallengeAttempts = 3

// fetchMirror keeps a challenge and its retry on the same instance. Prefer the
// last successful instance, then try every other configured instance once.
func (t *twitterConcern) fetchMirror(id string) (*UserProfile, []*Tweet, error) {
	if len(BaseURL) == 0 {
		return nil, nil, errors.New("未配置 Twitter 镜像")
	}
	start := int(t.mirrorNext.Load() % uint64(len(BaseURL)))
	var failures []error
	for offset := 0; offset < len(BaseURL); offset++ {
		index := (start + offset) % len(BaseURL)
		base, err := url.Parse(strings.TrimSpace(BaseURL[index]))
		if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
			failures = append(failures, fmt.Errorf("无效的 Twitter 镜像地址 %q", BaseURL[index]))
			continue
		}
		base.RawQuery, base.Fragment = "", ""
		base.Path = strings.TrimRight(base.Path, "/") + "/"
		target := base.JoinPath(id)
		profile, tweets, err := fetchMirrorPage(target)
		if err == nil && (profile == nil || !strings.EqualFold(profile.ScreenName, id)) {
			err = errors.New("镜像未返回目标用户资料，可能是验证页或错误页")
		}
		if err != nil {
			logger.WithField("Mirror", base.Host).WithField("userId", id).
				Warnf("镜像请求失败，尝试其他实例：%v", err)
			failures = append(failures, fmt.Errorf("%s: %w", base.Host, err))
			continue
		}
		if profile.Name == "" {
			profile.Name = profile.ScreenName
		}
		t.mirrorNext.Store(uint64(index))
		return profile, tweets, nil
	}
	return nil, nil, fmt.Errorf("所有 Twitter 镜像均不可用: %w", errors.Join(failures...))
}

func fetchMirrorPage(target *url.URL) (*UserProfile, []*Tweet, error) {
	for attempt := 0; attempt < mirrorChallengeAttempts; attempt++ {
		var response bytes.Buffer
		var headers requests.RespHeader
		var status int
		// Fail over to another instance instead of retrying a failing endpoint.
		opts := append(SetRequestOptions(), requests.RetryOption(0), requests.HttpCodeOption(&status))
		requestErr := requests.GetWithHeader(target.String(), nil, &response, &headers, opts...)
		if requestErr != nil && (status == 0 || response.Len() == 0) {
			return nil, nil, requestErr
		}
		body, err := localutils.HtmlDecoder(headers.ContentEncoding, response)
		if err != nil {
			return nil, nil, err
		}
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		title := strings.TrimSpace(doc.Find("title").Text())
		anubis := doc.Find("script#anubis_challenge").Length() > 0
		poast := strings.HasPrefix(title, "Verifying your browser")
		if requestErr != nil && !anubis && !poast {
			return nil, nil, fmt.Errorf("%w (%s)", requestErr, title)
		}
		if title == "Checking your browser" {
			link := doc.Find(`a[href*="__aragorn="]`).First().AttrOr("href", "")
			ref, err := url.Parse(link)
			if err != nil || link == "" {
				return nil, nil, errors.New("镜像验证页缺少继续链接")
			}
			next := target.ResolveReference(ref)
			if next.Scheme != target.Scheme || next.Host != target.Host || next.Path != target.Path {
				return nil, nil, errors.New("镜像验证链接必须指向当前实例的同一用户页")
			}
			target = next
			continue
		}
		profile, tweets, challenge, err := ParseResp(body, target.String())
		if err != nil {
			return nil, nil, err
		}
		if challenge == nil {
			return profile, tweets, nil
		}
		if attempt == mirrorChallengeAttempts-1 {
			break
		}
		switch challenge.Type {
		case "anubis":
			if err := refreshAnubisCookie(challenge.Anubis); err != nil {
				return nil, nil, err
			}
		case "poast":
			time.Sleep(3 * time.Second)
		default:
			return nil, nil, fmt.Errorf("不支持的镜像验证类型 %q", challenge.Type)
		}
	}
	return nil, nil, errors.New("镜像验证超过重试上限")
}
