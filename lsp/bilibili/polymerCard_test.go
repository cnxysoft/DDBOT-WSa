package bilibili

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 这些 fixture 是 2026-09-13 从 feed/all 抓下来的真实返回，类型分别为
// 图文（实际走 MAJOR_TYPE_OPUS）、视频、转发、系统推荐直播间。
func loadPolymerItem(t *testing.T, file string) *polymerItem {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	item := new(polymerItem)
	require.NoError(t, json.Unmarshal(data, item))
	return item
}

func TestPolymerItemToCard_Opus(t *testing.T) {
	item := loadPolymerItem(t, "res/dynamic_feed_opus.json")
	card, err := polymerItemToCard(item)
	require.NoError(t, err)

	assert.Equal(t, DynamicDescType_WithImage, card.GetDesc().GetType())
	assert.Equal(t, "1247415702868983809", card.GetDesc().GetDynamicIdStr())
	assert.EqualValues(t, 1247415702868983809, card.GetDesc().GetDynamicId())
	assert.EqualValues(t, 174501086, card.GetDesc().GetUid())
	assert.EqualValues(t, 1789275001, card.GetDesc().GetTimestamp())
	assert.Equal(t, "哔哩哔哩会员购", card.GetDesc().GetUserProfile().GetInfo().GetUname())
	assert.NotEmpty(t, card.GetDesc().GetUserProfile().GetInfo().GetFace())

	// 下游靠 GetCardWithImage 反序列化，必须能被正确解析
	imageCard, err := card.GetCardWithImage()
	require.NoError(t, err)
	require.Len(t, imageCard.GetItem().GetPictures(), 2)
	assert.Contains(t, imageCard.GetItem().GetPictures()[0].GetImgSrc(), "hdslb.com")
	assert.Contains(t, imageCard.GetItem().GetDescription(), "凡人众筹")
	assert.NotZero(t, imageCard.GetItem().GetPictures()[0].GetImgWidth())
}

func TestPolymerItemToCard_Archive(t *testing.T) {
	item := loadPolymerItem(t, "res/dynamic_feed_archive.json")
	card, err := polymerItemToCard(item)
	require.NoError(t, err)

	assert.Equal(t, DynamicDescType_WithVideo, card.GetDesc().GetType())
	assert.Equal(t, "BV1BzYa6rEQp", card.GetDesc().GetBvid())
	assert.NotZero(t, card.GetDesc().GetDynamicId())

	videoCard, err := card.GetCardWithVideo()
	require.NoError(t, err)
	assert.Equal(t, "BV1BzYa6rEQp", videoCard.GetOrigin().GetBvid())
	assert.NotEmpty(t, videoCard.GetTitle())
	assert.Contains(t, videoCard.GetPic(), "hdslb.com")
	// duration_text 是 "01:48"
	assert.EqualValues(t, 108, videoCard.GetDuration())
}

func TestPolymerItemToCard_Forward(t *testing.T) {
	item := loadPolymerItem(t, "res/dynamic_feed_forward.json")
	card, err := polymerItemToCard(item)
	require.NoError(t, err)

	assert.Equal(t, DynamicDescType_WithOrigin, card.GetDesc().GetType())
	assert.Equal(t, "1247403857365958689", card.GetDesc().GetDynamicIdStr())
	// 原动态 id 用于合并转发去重
	assert.Equal(t, "1247402815852118034", card.GetDesc().GetOrigDyIdStr())
	assert.Equal(t, DynamicDescType_WithImage, card.GetDesc().GetOrigType())

	origCard, err := card.GetCardWithOrig()
	require.NoError(t, err)
	assert.Equal(t, DynamicDescType_WithImage, origCard.GetItem().GetOrigType())
	assert.NotEmpty(t, origCard.GetOriginUser().GetInfo().GetUname())

	// origin 字段本身是一段 JSON，prepare() 会再解析一次
	inner := new(CardWithImage)
	require.NoError(t, safeUnmarshalCard(origCard.GetOrigin(), inner))
	assert.NotEmpty(t, inner.GetItem().GetPictures())
}

func TestPolymerItemToCard_ForwardWithoutOrigin(t *testing.T) {
	// 原动态不可见时写入占位文案，prepare() 依赖这个固定值
	item := &polymerItem{
		IDStr: "1",
		Type:  "DYNAMIC_TYPE_FORWARD",
		Modules: &polymerModules{
			ModuleAuthor: &polymerModuleAuthor{Mid: 1, Name: "a", PubTs: "100"},
			ModuleDynamic: &polymerModuleDynamic{
				Desc: &polymerDesc{Text: "转发内容"},
			},
		},
	}
	card, err := polymerItemToCard(item)
	require.NoError(t, err)

	origCard, err := card.GetCardWithOrig()
	require.NoError(t, err)
	assert.Equal(t, missingOriginPlaceholder, origCard.GetOrigin())
	assert.Equal(t, DynamicDescType_WithMiss, origCard.GetItem().GetOrigType())
	assert.Equal(t, "转发内容", origCard.GetItem().GetContent())
}

func TestPolymerItemToCard_LiveRcmd(t *testing.T) {
	item := loadPolymerItem(t, "res/dynamic_feed_live_rcmd.json")
	card, err := polymerItemToCard(item)
	require.NoError(t, err)

	// 与旧接口保持一致：系统推荐直播间在 filterCard 里会被丢掉
	assert.Equal(t, DynamicDescType_WithLiveV2, card.GetDesc().GetType())

	liveCard, err := card.GetCardWithLiveV2()
	require.NoError(t, err)
	assert.EqualValues(t, 5055636, liveCard.GetLivePlayInfo().GetRoomId())
	assert.NotEmpty(t, liveCard.GetLivePlayInfo().GetTitle())
}

func TestPolymerItemToCard_Invalid(t *testing.T) {
	_, err := polymerItemToCard(&polymerItem{})
	assert.ErrorIs(t, err, ErrPolymerItemInvalid)
}

// feed/all 的 update_num 是字符串（如 "32"），而同一字段在别处又是数字，
// flexInt64 必须两种都吃得下，且脏数据不能导致整个响应反序列化失败。
func TestFlexInt64(t *testing.T) {
	var resp polymerFeedResponse
	body := `{"code":0,"data":{"update_num":"32","update_baseline":"1247424644978311168",` +
		`"has_more":true,"items":[]}}`
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.EqualValues(t, 32, resp.GetData().GetUpdateNum())
	assert.Equal(t, "1247424644978311168", resp.GetData().UpdateBaseline)

	var probe polymerFeedResponse
	require.NoError(t, json.Unmarshal([]byte(`{"code":0,"data":{"update_num":0}}`), &probe))
	assert.EqualValues(t, 0, probe.GetData().GetUpdateNum())

	var dirty polymerFeedResponse
	require.NoError(t, json.Unmarshal([]byte(`{"code":0,"data":{"update_num":"abc"}}`), &dirty))
	assert.EqualValues(t, 0, dirty.GetData().GetUpdateNum())
}

func TestParseDurationSeconds(t *testing.T) {
	assert.EqualValues(t, 108, parseDurationSeconds("01:48"))
	assert.EqualValues(t, 3723, parseDurationSeconds("1:02:03"))
	assert.EqualValues(t, 0, parseDurationSeconds(""))
	assert.EqualValues(t, 0, parseDurationSeconds("abc"))
}
