package weibo

import (
	"testing"
	"time"

	miraiConfig "github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/stretchr/testify/assert"
)

func TestWeiboTemplateGuestCard(t *testing.T) {
	createdAt := time.Date(2025, 10, 1, 8, 30, 0, 0, time.Local).Format(time.RubyDate)
	card := &Card{
		CreatedAt: createdAt,
		Id:        778899,
		Mblogid:   "guestmblog",
		RawText:   "guest<br />content",
		Mblogtype: CardType_Normal,
		PicInfos: map[string]*Card_PicInfo{
			"p1": {
				Type: "pic",
				Large: &Card_PicVariant{
					Url: "https://example.com/pic.jpg",
				},
			},
		},
	}

	guestResp := &apiContainerGetIndexGuestCardsResponse{
		Ok: 1,
		Data: &apiContainerGetIndexGuestCardsResponseData{
			Cards: []apiContainerGetIndexGuestCard{{CardGroup: []apiContainerGetIndexGuestCard{{Mblog: card}}}},
		},
	}

	flattened := guestResp.ToCardsResponse()
	assert.Len(t, flattened.GetData().GetList(), 1)

	cache := NewCacheCard(flattened.GetData().GetList()[0], "guest-user", 12345)
	msg := cache.GetMSG()
	text := msgstringer.MsgToString(msg.Elements())

	assert.Contains(t, text, "weibo-guest-user发布了新微博")
	assert.Contains(t, text, "guest")
	assert.Contains(t, text, "content")
	assert.Contains(t, text, "https://weibo.com/12345/guestmblog")
	assert.Contains(t, text, "[图片]")
}

func TestWeiboTemplateFallback(t *testing.T) {
	resetConfig := useTestConfig(t)
	defer resetConfig()

	miraiConfig.GlobalConfig.Set("extDb.enable", true)

	createdAt := time.Date(2025, 11, 2, 9, 45, 0, 0, time.Local).Format(time.RubyDate)
	card := &Card{
		CreatedAt: createdAt,
		Id:        223344,
		Mblogid:   "fallbackmblog",
		RawText:   "fallback<br />content",
		Mblogtype: CardType_Normal,
		User: &ApiContainerGetIndexProfileResponse_Data_UserInfo{
			Id:         54321,
			ScreenName: "fallback-user",
		},
	}

	cache := NewCacheCard(card, "fallback-user", 54321)
	msg := cache.GetMSG()
	text := msgstringer.MsgToString(msg.Elements())

	assert.Contains(t, text, "weibo-fallback-user发布了新微博")
	assert.Contains(t, text, "fallback")
	assert.Contains(t, text, "content")
	assert.Contains(t, text, "https://weibo.com/54321/fallbackmblog")
}

func TestWeiboTemplateFallbackMissingMblogId(t *testing.T) {
	resetConfig := useTestConfig(t)
	defer resetConfig()

	miraiConfig.GlobalConfig.Set("extDb.enable", true)

	createdAt := time.Date(2025, 11, 3, 10, 15, 0, 0, time.Local).Format(time.RubyDate)
	card := &Card{
		CreatedAt: createdAt,
		Id:        998877,
		RawText:   "fallback<br />missing-id",
		Mblogtype: CardType_Normal,
		User: &ApiContainerGetIndexProfileResponse_Data_UserInfo{
			Id:         665544,
			ScreenName: "fallback-user",
		},
	}

	cache := NewCacheCard(card, "fallback-user", 665544)
	msg := cache.GetMSG()
	text := msgstringer.MsgToString(msg.Elements())

	assert.Contains(t, text, "weibo-fallback-user发布了新微博")
	assert.Contains(t, text, "fallback")
	assert.Contains(t, text, "missing-id")
	assert.Contains(t, text, "https://weibo.com/665544/998877")
}

func TestWeiboTemplateRetweetPageInfoCover(t *testing.T) {
	createdAt := time.Date(2025, 11, 4, 11, 30, 0, 0, time.Local).Format(time.RubyDate)
	card := &Card{
		CreatedAt: createdAt,
		Id:        556677,
		Mblogid:   "retweet-cover",
		RawText:   "retweet<br />wrapper",
		Mblogtype: CardType_Normal,
		User: &ApiContainerGetIndexProfileResponse_Data_UserInfo{
			Id:         112233,
			ScreenName: "retweet-user",
		},
		RetweetedStatus: &Card{
			RawText: "origin<br />video",
			User: &ApiContainerGetIndexProfileResponse_Data_UserInfo{
				Id:         223344,
				ScreenName: "origin-user",
			},
			PageInfo: &Card_PageInfo{
				PagePic: "https://example.com/retweet-cover.jpg",
			},
		},
	}

	cache := NewCacheCard(card, "retweet-user", 112233)
	msg := cache.GetMSG()
	text := msgstringer.MsgToString(msg.Elements())

	assert.Contains(t, text, "weibo-retweet-user转发了origin-user的微博")
	assert.Contains(t, text, "origin")
	assert.Contains(t, text, "video")
	assert.Contains(t, text, "https://weibo.com/112233/retweet-cover")
	assert.Contains(t, text, "[图片]")
}
