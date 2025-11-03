package twitter

import (
	"bytes"
	"compress/gzip"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTwitterConcern_GetUserInfo(t *testing.T) {
	// 测试用的 HTML 模板
	successHTML := `
	<html>
	<head>
		<title>Test User (@testuser) / X</title>
		<meta property="og:title" content="Test User (@testuser)">
		<meta property="og:description" content="This is a test user">
		<link rel="preload" as="image" href="https://test.com/banner.jpg">
		<link rel="preload" as="image" href="https://test.com/avatar.jpg">
	</head>
	<body>
		<div class="profile-joindate">
			<div class="icon-container">Joined March 2023</div>
		</div>
		<ul class="profile-statlist">
			<li class="posts"><span class="profile-stat-num">1,234</span></li>
			<li class="following"><span class="profile-stat-num">567</span></li>
			<li class="followers"><span class="profile-stat-num">8,901</span></li>
			<li class="likes"><span class="profile-stat-num">2,345</span></li>
		</ul>
	</body>
	</html>`

	tests := []struct {
		name         string
		screenName   string
		mockResponse string
		expected     *UserInfo
		expectError  bool
		errorMsg     string
	}{
		{
			name:         "successful user info fetch",
			screenName:   "testuser",
			mockResponse: successHTML,
			expected: &UserInfo{
				Id:   "testuser",
				Name: "Test User",
			},
			expectError: false,
		},
		{
			name:         "cloudflare challenge",
			screenName:   "cf_user",
			mockResponse: `<html><title>Just a moment...</title></html>`,
			expectError:  true,
			errorMsg:     "cf_clearance has expired!",
		},
		{
			name:         "suspended user",
			screenName:   "suspended_user",
			mockResponse: `<html><head><title>Error | nitter</title></head><div class="error-panel">This account has been suspended</div></html>`,
			expectError:  true,
			errorMsg:     "This account has been suspended",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			test.InitBuntdb(t)
			defer test.CloseBuntdb(t)

			// 创建测试服务器
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", "gzip") // 模拟压缩响应
				w.WriteHeader(http.StatusOK)

				// 创建 gzip 压缩的测试响应
				var buf bytes.Buffer
				gz := gzip.NewWriter(&buf)
				gz.Write([]byte(tt.mockResponse))
				gz.Close()
				w.Write(buf.Bytes())
			}))
			defer ts.Close()

			// 替换 buildProfileURL
			originalBuildProfileURL := buildProfileURL
			buildProfileURL = func(screenName string) *url.URL {
				Url, _ := url.Parse(ts.URL)
				return Url
			}
			defer func() { buildProfileURL = originalBuildProfileURL }()

			tc := &twitterConcern{
				StateManager: &StateManager{
					StateManager: concern.NewStateManagerWithStringID(Site, nil),
					ExtraKey:     new(ExtraKey),
				},
			}

			Cookie, _ = test.NewJar()
			defer test.DestroyJar(Cookie)

			result, err := tc.FindUserInfo(tt.screenName, true)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected.Id, result.Id)
				assert.Equal(t, tt.expected.Name, result.Name)

				// 验证数据库存储
				dbInfo, err := tc.GetUserInfo(tt.screenName)
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, dbInfo)
			}
		})
	}
}

func TestTwitterConcern_GetTweets(t *testing.T) {
	// 创建测试用的HTML响应内容
	testHTML := `
    <html>
    <head>
		<title>test User (@testuser) / Twitter</title>
		<meta property="og:title" content="efbell (@YY749649883736)">
	</head>
    <body>
<div class="timeline-item ">
              <a class="tweet-link" href="/YY749649883736/status/1920439635265601673#m"></a>
              <div class="tweet-body">
                <div><div class="tweet-header">
                    <a class="tweet-avatar" href="/YY749649883736"><img class="avatar round" src="/pic/profile_images%2F1898789065090297856%2FRUoCd_rU_bigger.jpg" alt="" loading="lazy"></a>
                    <div class="tweet-name-row">
                      <div class="fullname-and-username">
                        <a class="fullname" href="/YY749649883736" title="efbell">efbell<div class="icon-container"><span class="icon-ok verified-icon blue" title="Verified blue account"></span></div></a>
                        <a class="username" href="/YY749649883736" title="@YY749649883736">@YY749649883736</a>
                      </div>
                      <span class="tweet-date"><a href="/YY749649883736/status/1920439635265601673#m" title="May 8, 2025 · 11:24 AM UTC">May 8</a></span>
                    </div>
                  </div></div>
                <div class="tweet-content media-body" dir="auto">ムツキ
<a href="/search?q=%23ブルアカ">#ブルアカ</a> <a href="/search?q=%23BlueArchive">#BlueArchive</a></div>
                <div class="attachments"><div class="gallery-row" style=""><div class="attachment image"><a class="still-image" href="/pic/orig/media%2FGqbFhUuW0AAVRk4.jpg" target="_blank"><img src="/pic/media%2FGqbFhUuW0AAVRk4.jpg%3Fname%3Dsmall%26format%3Dwebp" alt="" loading="lazy"></a></div></div></div>
                <div class="tweet-stats">
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-comment" title=""></span> 5</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-retweet" title=""></span> 409</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-quote" title=""></span> 3</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-heart" title=""></span> 4,117</div></span>
                </div>
              </div>
            </div>
<div class="timeline-item ">
              <a class="tweet-link" href="/T1kosewad78/status/1929615275475009783#m"></a>
              <div class="tweet-body">
                <div><div class="tweet-header">
                    <a class="tweet-avatar" href="/T1kosewad78"><img class="avatar round" src="/pic/profile_images%2F1716368485847277568%2F4ytZ1lng_bigger.jpg" alt="" loading="lazy"></a>
                    <div class="tweet-name-row">
                      <div class="fullname-and-username">
                        <a class="fullname" href="/T1kosewad78" title="ちこせわと|">ちこせわと|<div class="icon-container"><span class="icon-ok verified-icon blue" title="Verified blue account"></span></div></a>
                        <a class="username" href="/T1kosewad78" title="@T1kosewad78">@T1kosewad78</a>
                      </div>
                      <span class="tweet-date"><a href="/T1kosewad78/status/1929615275475009783#m" title="Jun 2, 2025 · 7:05 PM UTC">Jun 2</a></span>
                    </div>
                  </div></div>
                <div class="tweet-content media-body" dir="auto">So this is what it feels like after years of struggling to build own illustration style, only for an AI user to copy it instantly...</div>
                <div class="attachments media-gif"><div class="gallery-gif" style="max-height: unset; "><div class="attachment"><video class="gif" poster="/pic/tweet_video_thumb%2FGsde3Fkb0AE31iu.jpg%3Fname%3Dsmall%26format%3Dwebp" autoplay="" controls="" muted="" loop="" __idm_id__="475137"><source src="/pic/video.twimg.com%2Ftweet_video%2FGsde3Fkb0AE31iu.mp4" type="video/mp4"></video></div></div></div>
                <div class="tweet-stats">
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-comment" title=""></span> 39</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-retweet" title=""></span> 56</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-quote" title=""></span> 2</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-heart" title=""></span> 1,217</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-play" title=""></span> GIF</div></span>
                </div>
              </div>
            </div>
<div class="timeline-item ">
            <a class="tweet-link" href="/TactBets/status/1930281922921410905#m"></a>
            <div class="tweet-body">
              <div>
                <div class="retweet-header"><span><div class="icon-container"><span class="icon-retweet" title=""></span> Deadshot retweeted</div></span></div>
                <div class="tweet-header">
                  <a class="tweet-avatar" href="/TactBets"><img class="avatar round" src="https://pbs.twimg.com/profile_images/1801798108134916098/Him-aKN__bigger.jpg" alt=""></a>
                  <div class="tweet-name-row">
                    <div class="fullname-and-username">
                      <a class="fullname" href="/TactBets" title="Tact">Tact<div class="icon-container"><span class="icon-ok verified-icon blue" title="Verified blue account"></span></div></a>
                      <a class="username" href="/TactBets" title="@TactBets">@TactBets</a>
                    </div>
                    <span class="tweet-date"><a href="/TactBets/status/1930281922921410905#m" title="Jun 4, 2025 · 3:14 PM UTC">1h</a></span>
                  </div>
                </div>
              </div>
              <div class="tweet-content media-body" dir="auto">CAN WE KEEP OUR WINNING STREAK GOING ON LIGHTNING STORM GAME SHOW?!

💸$50 STAKE DEPOSIT VIDEO GIVEAWAY💸($10 x 5)
Link: <a href="https://youtu.be/1ndym-VP9dA">youtu.be/1ndym-VP9dA</a>

- RETWEET THIS POST🔁 &amp; LIKE♥️
- FOLLOW MY TWITTER➡️ <a href="/TactBets" title="Tact">@TactBets</a>
- COMMENT ON THE VIDEO ✍️
- SUBSCRIBE TO MY YOUTUBE</div>
              <div class="attachments card"><div class="gallery-video"><div class="attachment video-container"><video poster="https://pbs.twimg.com/amplify_video_thumb/1930279430674403328/img/UsFPTnJuEtF29T45.jpg?name=small&amp;format=webp" controls=""><source src="https://video.twimg.com/amplify_video/1930279430674403328/vid/avc1/1920x1080/36Iobmc-rj_wUZG2.mp4" type="video/mp4"></video></div></div></div>
              <div class="tweet-stats">
                <span class="tweet-stat"><div class="icon-container"><span class="icon-comment" title=""></span> 14</div></span>
                <span class="tweet-stat"><div class="icon-container"><span class="icon-retweet" title=""></span> 26</div></span>
                <span class="tweet-stat"><div class="icon-container"><span class="icon-quote" title=""></span></div></span>
                <span class="tweet-stat"><div class="icon-container"><span class="icon-heart" title=""></span> 25</div></span>
                <span class="tweet-stat"><div class="icon-container"><span class="icon-play" title=""></span> 0</div></span>
              </div>
            </div>
          </div>
<div class="timeline-item ">
              <a class="tweet-link" href="/pandarion_v3/status/1931877486037782753#m"></a>
              <div class="tweet-body">
                <div>
                  <div class="retweet-header"><span><div class="icon-container"><span class="icon-retweet" title=""></span> Alen retweeted</div></span></div>
                  <div class="tweet-header">
                    <a class="tweet-avatar" href="/pandarion_v3"><img class="avatar round" src="/pic/profile_images%2F1834780570410532864%2FFsaVJy6C_bigger.jpg" alt="" loading="lazy"></a>
                    <div class="tweet-name-row">
                      <div class="fullname-and-username">
                        <a class="fullname" href="/pandarion_v3" title="古老のパンダ">古老のパンダ</a>
                        <a class="username" href="/pandarion_v3" title="@pandarion_v3">@pandarion_v3</a>
                      </div>
                      <span class="tweet-date"><a href="/pandarion_v3/status/1931877486037782753#m" title="Jun 9, 2025 · 12:54 AM UTC">Jun 9</a></span>
                    </div>
                  </div>
                </div>
                <div class="tweet-content media-body" dir="auto">ポストする直前で寝落ちしてしまった…🤤

スクチラから始まって
これで〆🫡

<a href="https://nitter.net/i/grok/share/lbXvEhkNemrYoyb2hR6QVgH1A">nitter.net/i/grok/share/lbXvEhkNe…</a>

<a href="/search?q=%23AIイラスト">#AIイラスト</a></div>
                <div class="attachments"><div class="gallery-row" style=""><div class="attachment image"><a class="still-image" href="/pic/orig/media%2FGs9oVdNakAAgElN.jpg" target="_blank"><img src="/pic/media%2FGs9oVdNakAAgElN.jpg%3Fname%3Dsmall%26format%3Dwebp" alt="" loading="lazy"></a></div></div></div>
                <div class="quote quote-big">
                  <a class="quote-link" href="/pandarion_v3/status/1931525405007397208#m"></a>
                  <div class="tweet-name-row">
                    <div class="fullname-and-username">
                      <img class="avatar round mini" src="/pic/profile_images%2F1834780570410532864%2FFsaVJy6C_mini.jpg" alt="" loading="lazy">
                      <a class="fullname" href="/pandarion_v3" title="古老のパンダ">古老のパンダ</a>
                      <a class="username" href="/pandarion_v3" title="@pandarion_v3">@pandarion_v3</a>
                    </div>
                    <span class="tweet-date"><a href="/pandarion_v3/status/1931525405007397208#m" title="Jun 8, 2025 · 1:35 AM UTC">Jun 8</a></span>
                  </div>
                  <div class="quote-text" dir="auto">トラブルもありましたが
お約束まで持っていけました🤤

<a href="https://nitter.net/i/grok/share/KgVwuWDyV3Prtxz0h9aM25ec8">nitter.net/i/grok/share/KgVwuWDyV…</a>

真面目なgrokくんに好感☺️

<a href="/search?q=%23AIイラスト">#AIイラスト</a></div>
                  <div class="quote-media-container"><div class="attachments"><div class="gallery-row" style=""><div class="attachment image"><a class="still-image" href="/pic/orig/media%2FGs4oHpAbkAAbH1Z.jpg" target="_blank"><img src="/pic/media%2FGs4oHpAbkAAbH1Z.jpg%3Fname%3Dsmall%26format%3Dwebp" alt="" loading="lazy"></a></div></div></div></div>
                </div>
                <div class="tweet-stats">
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-comment" title=""></span> 9</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-retweet" title=""></span> 89</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-quote" title=""></span> 2</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-heart" title=""></span> 1,401</div></span>
                </div>
              </div>
            </div>
<div class="timeline-item ">
              <a class="tweet-link" href="/peace_maki02/status/1920766476270678132#m"></a>
              <div class="tweet-body">
                <div>
                  <div class="pinned"><span><div class="icon-container"><span class="icon-pin" title=""></span> Pinned Tweet</div></span></div>
                  <div class="tweet-header">
                    <a class="tweet-avatar" href="/peace_maki02"><img class="avatar round" src="/pic/profile_images%2F1362429533790498817%2FLURcNXBA_bigger.jpg" alt="" loading="lazy"></a>
                    <div class="tweet-name-row">
                      <div class="fullname-and-username">
                        <a class="fullname" href="/peace_maki02" title="安原宏和@９巻発売、アニメ化決定！🐨">安原宏和@９巻発売、アニメ化決定！🐨<div class="icon-container"><span class="icon-ok verified-icon blue" title="Verified blue account"></span></div></a>
                        <a class="username" href="/peace_maki02" title="@peace_maki02">@peace_maki02</a>
                      </div>
                      <span class="tweet-date"><a href="/peace_maki02/status/1920766476270678132#m" title="May 9, 2025 · 9:03 AM UTC">May 9</a></span>
                    </div>
                  </div>
                </div>
                <div class="tweet-content media-body" dir="auto">／
⋰
「ゲーセン少女と異文化交流」
🎮メインPVを公開🎮
⋱
＼

勘違いから始まる、
ゲーセンでの異文化交流👾🎀
TVアニメは7月6日(日)放送開始です！

リリー <a href="/search?q=%23天城サリー">#天城サリー</a>
蓮司 <a href="/search?q=%23千葉翔也">#千葉翔也</a>
葵衣 <a href="/search?q=%23小山内怜央">#小山内怜央</a>
花梨 <a href="/search?q=%23結川あさき">#結川あさき</a>
蛍 <a href="/search?q=%23石原夏織">#石原夏織</a>
桃子 <a href="/search?q=%23茅野愛衣">#茅野愛衣</a>

<a href="https://piped.video/watch?v=QOVabX4iYYY">piped.video/watch?v=QOVabX4i…</a>

<a href="/search?q=%23ゲーセン少女">#ゲーセン少女</a></div>
                <div class="attachments card"><div class="gallery-video"><div class="attachment video-container">
                      <video poster="/pic/amplify_video_thumb%2F1920766235832172544%2Fimg%2F-wvBuAXTdzu4CEnN.jpg%3Fname%3Dsmall%26format%3Dwebp" data-url="/video/CDBABB50751BD/https%3A%2F%2Fvideo.twimg.com%2Famplify_video%2F1920766235832172544%2Fpl%2FFNAXoec5cT40vEaG.m3u8" data-autoload="false"></video>
                      <div class="video-overlay" onclick="playVideo(this)">
                      <div class="overlay-circle"><span class="overlay-triangle"></span></div>
                      </div>
                    </div></div></div>
                <div class="tweet-stats">
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-comment" title=""></span> 27</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-retweet" title=""></span> 1,782</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-quote" title=""></span> 216</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-heart" title=""></span> 4,329</div></span>
                  <span class="tweet-stat"><div class="icon-container"><span class="icon-play" title=""></span> 0</div></span>
                </div>
              </div>
            </div>
    </body>
    </html>
    `

	// 创建测试服务器
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	// 替换 buildProfileURL 函数（需要修改 production 代码以支持此操作）
	originalBuildProfileURL := buildProfileURL
	buildProfileURL = func(screenName string) *url.URL {
		Url, _ := url.Parse(ts.URL)
		return Url
	}
	defer func() { buildProfileURL = originalBuildProfileURL }()

	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	// 初始化 twitterConcern
	tc := &twitterConcern{
		StateManager: &StateManager{
			StateManager: concern.NewStateManagerWithStringID(Site, nil),
			ExtraKey:     new(ExtraKey),
		},
	}

	// 创建 CookieJar
	Cookie, _ = test.NewJar()
	defer test.DestroyJar(Cookie)

	// 执行测试
	tweets, err := tc.GetTweets("testuser")

	assert.NoError(t, err)
	assert.NotNil(t, tweets)
	assert.Len(t, tweets, 5, "应解析出5条推文")

	// 验证推文内容
	tweet := tweets[0]
	assert.Equal(t, "1920439635265601673", tweet.ID)
	assert.Contains(t, tweet.Content, "ムツキ\n#ブルアカ #BlueArchive")
	assert.Equal(t, int64(4117), tweet.Likes)
	assert.Equal(t, int64(409), tweet.Retweets)
}
