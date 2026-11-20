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
	"github.com/sirupsen/logrus"
)

const (
	mirrorChallengeAttempts = 3
	mirrorCooldownMin       = 30 * time.Second
	mirrorCooldownMax       = 15 * time.Minute
)

type mirrorFailure struct {
	count      int
	retryAfter time.Time
}

// A missing or protected account must not put a healthy instance into cooldown.
type mirrorAccountError struct{ message string }

func (e *mirrorAccountError) Error() string { return e.message }

type mirrorFetchError struct {
	stage  string
	status int
	err    error
}

func (e *mirrorFetchError) Error() string { return e.err.Error() }
func (e *mirrorFetchError) Unwrap() error { return e.err }

func (t *twitterConcern) mirrorRetryAfter(base string) time.Time {
	t.mirrorMu.Lock()
	defer t.mirrorMu.Unlock()
	return t.mirrorFailures[base].retryAfter
}

func (t *twitterConcern) recordMirrorResult(base string, now time.Time, err error) {
	t.mirrorMu.Lock()
	defer t.mirrorMu.Unlock()
	if err == nil {
		delete(t.mirrorFailures, base)
		return
	}
	var accountErr *mirrorAccountError
	if errors.As(err, &accountErr) {
		return
	}
	if t.mirrorFailures == nil {
		t.mirrorFailures = make(map[string]mirrorFailure)
	}
	failure := t.mirrorFailures[base]
	if failure.count < 6 {
		failure.count++
	}
	delay := min(mirrorCooldownMin<<uint(failure.count-1), mirrorCooldownMax)
	failure.retryAfter = now.Add(delay)
	t.mirrorFailures[base] = failure
}

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
		key := base.String()
		entry := logger.WithFields(logrus.Fields{"Mirror": base.Host, "userId": id, "attempt": offset + 1})
		if retryAfter := t.mirrorRetryAfter(key); time.Now().Before(retryAfter) {
			entry.WithField("cooldown_seconds", time.Until(retryAfter).Seconds()).Debug("跳过冷却中的 Twitter 镜像")
			failures = append(failures, fmt.Errorf("%s: 镜像冷却中，%s 后重试", base.Host,
				time.Until(retryAfter).Round(time.Second)))
			continue
		}
		target := base.JoinPath(id)
		started := time.Now()
		profile, tweets, err := fetchMirrorPage(target)
		entry = entry.WithField("duration_ms", time.Since(started).Milliseconds())
		if err == nil && (profile == nil || !strings.EqualFold(profile.ScreenName, id)) {
			err = &mirrorFetchError{stage: "profile", err: errors.New("镜像未返回目标用户资料，可能是验证页或错误页")}
		}
		if err != nil {
			t.recordMirrorResult(key, time.Now(), err)
			var fetchErr *mirrorFetchError
			if errors.As(err, &fetchErr) {
				entry = entry.WithFields(logrus.Fields{"stage": fetchErr.stage, "http_status": fetchErr.status})
			}
			if retryAfter := t.mirrorRetryAfter(key); !retryAfter.IsZero() {
				entry = entry.WithField("cooldown_seconds", max(0, time.Until(retryAfter).Seconds()))
			}
			entry.WithError(err).Warn("Twitter 镜像请求失败，尝试其他实例")
			failures = append(failures, fmt.Errorf("%s: %w", base.Host, err))
			continue
		}
		t.recordMirrorResult(key, time.Now(), nil)
		if profile.Name == "" {
			profile.Name = profile.ScreenName
		}
		t.mirrorNext.Store(uint64(index))
		entry = entry.WithField("tweets", len(tweets))
		if offset > 0 {
			entry.Info("Twitter 镜像切换成功")
		} else {
			entry.Debug("Twitter 镜像抓取成功")
		}
		return profile, tweets, nil
	}
	return nil, nil, fmt.Errorf("所有 Twitter 镜像均不可用: %w", errors.Join(failures...))
}

func fetchMirrorPage(target *url.URL) (resultProfile *UserProfile, resultTweets []*Tweet, resultErr error) {
	stage, status := "request", 0
	defer func() {
		if resultErr != nil {
			resultErr = &mirrorFetchError{stage: stage, status: status, err: resultErr}
		}
	}()
	// Resolve the proxy once so challenge assets and the pass request use the same exit IP.
	options := append(SetRequestOptions(), requests.RetryOption(0))
	for attempt := 0; attempt < mirrorChallengeAttempts; attempt++ {
		var response bytes.Buffer
		var headers requests.RespHeader
		stage, status = "request", 0
		// Fail over to another instance instead of retrying a failing endpoint.
		opts := append(options[:len(options):len(options)], requests.HttpCodeOption(&status))
		requestStarted := time.Now()
		requestErr := requests.GetWithHeader(target.String(), nil, &response, &headers, opts...)
		logger.WithFields(logrus.Fields{"Mirror": target.Host, "stage": "request", "attempt": attempt + 1,
			"http_status": status, "duration_ms": time.Since(requestStarted).Milliseconds()}).Debug("Twitter 镜像 HTTP 请求完成")
		if requestErr != nil && (status == 0 || response.Len() == 0) {
			return nil, nil, requestErr
		}
		stage = "decode"
		body, err := localutils.HtmlDecoder(headers.ContentEncoding, response)
		if err != nil {
			return nil, nil, err
		}
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		title := strings.TrimSpace(doc.Find("title").Text())
		if message := strings.TrimSpace(doc.Find(".error-panel, .timeline-message").Text()); message != "" {
			lower := strings.ToLower(message)
			if strings.Contains(lower, "suspended") || strings.Contains(lower, "protected") ||
				strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist") {
				stage = "account"
				return nil, nil, &mirrorAccountError{message: message}
			}
		}
		anubis := doc.Find("script#anubis_challenge").Length() > 0
		poast := strings.HasPrefix(title, "Verifying your browser")
		if requestErr != nil && !anubis && !poast {
			stage = "request"
			return nil, nil, fmt.Errorf("%w (%s)", requestErr, title)
		}
		if title == "Checking your browser" {
			stage = "challenge"
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
		stage = "parse"
		parseStarted := time.Now()
		profile, tweets, challenge, err := ParseResp(body, target.String())
		logger.WithFields(logrus.Fields{"Mirror": target.Host, "stage": "parse",
			"duration_ms": time.Since(parseStarted).Milliseconds()}).Debug("Twitter 镜像页面解析完成")
		if err != nil {
			return nil, nil, err
		}
		if challenge == nil {
			return profile, tweets, nil
		}
		stage = "challenge"
		if attempt == mirrorChallengeAttempts-1 {
			break
		}
		switch challenge.Type {
		case "anubis":
			if err := refreshAnubisCookieWithOptions(challenge.Anubis, options); err != nil {
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
