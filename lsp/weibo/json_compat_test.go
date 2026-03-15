package weibo

import (
	stdjson "encoding/json"
	"testing"

	miraiConfig "github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/stretchr/testify/assert"
)

func TestWeiboJsonIdCompat(t *testing.T) {
	testCases := []struct {
		name              string
		payload           string
		expectedCardID    int64
		expectedUserID    int64
		expectedRetweetID int64
	}{
		{
			name: "string ids",
			payload: `{
				"id":"123",
				"user":{"id":"456","screen_name":"alice"},
				"retweeted_status":{"id":"789","user":{"id":"987","screen_name":"bob"}}
			}`,
			expectedCardID:    123,
			expectedUserID:    456,
			expectedRetweetID: 789,
		},
		{
			name: "number ids",
			payload: `{
				"id":123,
				"user":{"id":456,"screen_name":"alice"},
				"retweeted_status":{"id":789,"user":{"id":987,"screen_name":"bob"}}
			}`,
			expectedCardID:    123,
			expectedUserID:    456,
			expectedRetweetID: 789,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var card Card
			err := stdjson.Unmarshal([]byte(tc.payload), &card)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedCardID, card.GetId())
			assert.Equal(t, tc.expectedUserID, card.GetUser().GetId())
			assert.Equal(t, tc.expectedRetweetID, card.GetRetweetedStatus().GetId())
		})
	}
}

func TestWeiboJsonIdCompatMessageRender(t *testing.T) {
	resetConfig := useTestConfig(t)
	defer resetConfig()

	miraiConfig.GlobalConfig.Set("extDb.enable", true)

	payload := `{
		"id":"123456",
		"created_at":"Mon Jan 02 15:04:05 -0700 2006",
		"raw_text":"hello<br />world",
		"user":{"id":"54321","screen_name":"compat-user"},
		"mblogtype":0
	}`

	var card Card
	err := stdjson.Unmarshal([]byte(payload), &card)
	assert.NoError(t, err)

	msg := NewCacheCard(&card, "compat-user", 54321).GetMSG()
	text := msgstringer.MsgToString(msg.Elements())
	assert.NotEmpty(t, text)
}

func TestWeiboJsonObjectTypeCompat(t *testing.T) {
	testCases := []struct {
		name                      string
		payload                   string
		expectedObjectType        string
		expectedRetweetObjectType string
	}{
		{
			name: "number object_type",
			payload: `{
				"id": 123,
				"user": {"id": 456, "screen_name": "alice"},
				"page_info": {"object_type": 1},
				"retweeted_status": {
					"id": 789,
					"user": {"id": 987, "screen_name": "bob"},
					"page_info": {"object_type": 2}
				}
			}`,
			expectedObjectType:        "1",
			expectedRetweetObjectType: "2",
		},
		{
			name: "string object_type",
			payload: `{
				"id": "123",
				"user": {"id": "456", "screen_name": "alice"},
				"page_info": {"object_type": "video"},
				"retweeted_status": {
					"id": "789",
					"user": {"id": "987", "screen_name": "bob"},
					"page_info": {"object_type": "article"}
				}
			}`,
			expectedObjectType:        "video",
			expectedRetweetObjectType: "article",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var card Card
			err := stdjson.Unmarshal([]byte(tc.payload), &card)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedObjectType, card.GetPageInfo().GetObjectType())
			assert.Equal(t, tc.expectedRetweetObjectType, card.GetRetweetedStatus().GetPageInfo().GetObjectType())
		})
	}
}

func TestWeiboJsonPagePicCompat(t *testing.T) {
	testCases := []struct {
		name                   string
		payload                string
		expectedPagePic        string
		expectedRetweetPagePic string
	}{
		{
			name: "object page_pic",
			payload: `{
				"id": 123,
				"user": {"id": 456, "screen_name": "alice"},
				"page_info": {"page_pic": {"url": "https://example.com/a.png"}},
				"retweeted_status": {
					"id": 789,
					"user": {"id": 987, "screen_name": "bob"},
					"page_info": {"page_pic": {"url": "https://example.com/b.png"}}
				}
			}`,
			expectedPagePic:        "https://example.com/a.png",
			expectedRetweetPagePic: "https://example.com/b.png",
		},
		{
			name: "string page_pic",
			payload: `{
				"id": 111,
				"user": {"id": 222, "screen_name": "alice"},
				"page_info": {"page_pic": "https://example.com/c.png"},
				"retweeted_status": {
					"id": 333,
					"user": {"id": 444, "screen_name": "bob"},
					"page_info": {"page_pic": {"url": "https://example.com/d.png"}}
				}
			}`,
			expectedPagePic:        "https://example.com/c.png",
			expectedRetweetPagePic: "https://example.com/d.png",
		},
		{
			name: "object page_pic source fallback",
			payload: `{
				"id": 222,
				"user": {"id": 333, "screen_name": "alice"},
				"page_info": {"page_pic": {"source": "https://example.com/source.png"}}
			}`,
			expectedPagePic:        "https://example.com/source.png",
			expectedRetweetPagePic: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var card Card
			err := stdjson.Unmarshal([]byte(tc.payload), &card)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedPagePic, card.GetPageInfo().GetPagePic())
			assert.Equal(t, tc.expectedRetweetPagePic, card.GetRetweetedStatus().GetPageInfo().GetPagePic())
		})
	}
}

func TestWeiboJsonTopicPagePicSkipped(t *testing.T) {
	payload := `{
		"id": 123,
		"user": {"id": 456, "screen_name": "alice"},
		"page_info": {
			"type": "Topic",
			"page_pic": {"url": "https://example.com/topic.png"}
		},
		"retweeted_status": {
			"id": 789,
			"user": {"id": 987, "screen_name": "bob"},
			"page_info": {
				"type": "topic",
				"page_pic": {"url": "https://example.com/retweet-topic.png"}
			}
		}
	}`

	var card Card
	err := stdjson.Unmarshal([]byte(payload), &card)
	assert.NoError(t, err)
	assert.Empty(t, card.GetPageInfo().GetPagePic())
	assert.Empty(t, card.GetRetweetedStatus().GetPageInfo().GetPagePic())
}

func TestWeiboJsonMediaCompat(t *testing.T) {
	t.Run("extract page_pic object url and pic keys", func(t *testing.T) {
		payload := `{
			"id":"123",
			"user":{"id":"456","screen_name":"alice"},
			"page_info":{"page_pic":{"url":"https://example.com/main-cover.png"}},
			"pics":[
				{
					"pic_id":"pic-main",
					"url":"https://example.com/main-fallback.png",
					"large":{"url":"https://example.com/main-large.png"},
					"type":"livephoto"
				}
			],
			"retweeted_status":{
				"id":"789",
				"user":{"id":"987","screen_name":"bob"},
				"page_info":{"page_pic":{"pic":{"url":"https://example.com/retweet-cover.png"}}}
			}
		}`

		var card Card
		err := stdjson.Unmarshal([]byte(payload), &card)
		assert.NoError(t, err)

		assert.Equal(t, "https://example.com/main-cover.png", card.GetPageInfo().GetPagePic())
		assert.Equal(t, "https://example.com/retweet-cover.png", card.GetRetweetedStatus().GetPageInfo().GetPagePic())
	})

	t.Run("pick mw2000 and largest when large empty", func(t *testing.T) {
		var picInfos map[string]*Card_PicInfo
		err := stdjson.Unmarshal([]byte(`{
			"mw": {
				"type": "livephoto",
				"large": {"url": ""},
				"mw2000": {"url": "https://example.com/pic-mw2000.jpg"}
			},
			"largest": {
				"type": "pic",
				"large": {"url": ""},
				"largest": {"url": "https://example.com/pic-largest.jpg"}
			},
			"gif": {
				"type": "gif",
				"original": {"url": "https://example.com/pic-original.gif"},
				"large": {"url": "https://example.com/pic-large.jpg"}
			}
		}`), &picInfos)
		assert.NoError(t, err)

		urls := findPicUrlsForCard(picInfos)
		assert.ElementsMatch(t, []string{
			"https://example.com/pic-mw2000.jpg",
			"https://example.com/pic-largest.jpg",
			"https://example.com/pic-original.gif",
		}, urls)
	})
}

func TestWeiboTopicCoverSkipped(t *testing.T) {
	resetConfig := useTestConfig(t)
	defer resetConfig()

	miraiConfig.GlobalConfig.Set("extDb.enable", true)

	payload := `{
		"id":"1001",
		"created_at":"Mon Jan 02 15:04:05 -0700 2006",
		"raw_text":"outer post",
		"mblogtype":0,
		"user":{"id":"3001","screen_name":"outer-user"},
		"page_info":{
			"type":"topic",
			"object_type":"article",
			"page_pic":{"url":"https://example.com/main-topic-cover.png"}
		},
		"retweeted_status":{
			"id":"2001",
			"raw_text":"inner post",
			"user":{"id":"4001","screen_name":"inner-user"},
			"page_info":{
				"type":"topic",
				"object_type":"article",
				"page_pic":{"url":"https://example.com/retweet-topic-cover.png"}
			}
		}
	}`

	var card Card
	err := stdjson.Unmarshal([]byte(payload), &card)
	assert.NoError(t, err)

	cache := NewCacheCard(&card, "outer-user", 3001)
	cache.prepare()
	assert.Empty(t, cache.dynamic.Page.CoverUrl)
	assert.NotContains(t, cache.dynamic.Retweet.Images, "https://example.com/retweet-topic-cover.png")

	card.PageInfo = nil
	msg := NewCacheCard(&card, "outer-user", 3001).GetMSG()
	text := msgstringer.MsgToString(msg.Elements())
	assert.NotContains(t, text, "[图片]")
}
