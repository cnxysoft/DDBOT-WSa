package bilibili

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestKeySetParseGroupConcernStateKey 状态键解析必须同时支持 int64（uid）与
// 字符串（合集 <mid>/<series_id>）两种 id，否则整表枚举会失败，
// 连带直播/动态刷新一起报错。
func TestKeySetParseGroupConcernStateKey(t *testing.T) {
	ks := NewKeySet()

	groupCode, id, err := ks.ParseGroupConcernStateKey("ConcernState:1053858006:123895")
	assert.NoError(t, err)
	assert.EqualValues(t, 1053858006, groupCode)
	assert.EqualValues(t, 123895, id)

	groupCode, id, err = ks.ParseGroupConcernStateKey("ConcernState:685164226:123895/3945451")
	assert.NoError(t, err)
	assert.EqualValues(t, 685164226, groupCode)
	assert.Equal(t, "123895/3945451", id)

	_, _, err = ks.ParseGroupConcernStateKey("bad-key")
	assert.Error(t, err)
}

func TestParseSeriesSubId(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantMid    int64
		wantSeries int64
		wantErr    bool
	}{
		{name: "标准格式", in: "123895/3945451", wantMid: 123895, wantSeries: 3945451},
		{name: "带空格", in: " 123895 / 3945451 ", wantMid: 123895, wantSeries: 3945451},
		{
			name:       "合集链接",
			in:         "https://space.bilibili.com/123895/lists/3945451?type=series",
			wantMid:    123895,
			wantSeries: 3945451,
		},
		{name: "缺少分隔符", in: "3945451", wantErr: true},
		{name: "分段过多", in: "123895/3945451/1", wantErr: true},
		{name: "非数字", in: "abc/3945451", wantErr: true},
		{name: "空字符串", in: "", wantErr: true},
		{name: "零值", in: "0/0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mid, seriesId, err := ParseSeriesSubId(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantMid, mid)
			assert.Equal(t, tt.wantSeries, seriesId)
		})
	}
}

func TestIsSeriesSubId(t *testing.T) {
	assert.True(t, IsSeriesSubId("123895/3945451"))
	assert.True(t, IsSeriesSubId("https://space.bilibili.com/123895/lists/3945451?type=series"))
	assert.False(t, IsSeriesSubId("123895"), "纯 uid 不应被识别为合集订阅")
	assert.False(t, IsSeriesSubId("UID:123895"), "uid 前缀格式不应被识别为合集订阅")
	assert.False(t, IsSeriesSubId(""), "空字符串不应被识别为合集订阅")
}

// TestConcernParseIdDispatch 合集订阅 id 走字符串，uid 仍走 int64，两者不能互相误判
func TestConcernParseIdDispatch(t *testing.T) {
	c := new(Concern)

	sid, err := c.ParseId("123895/3945451")
	assert.NoError(t, err)
	assert.Equal(t, "123895/3945451", sid)

	sid2, err := c.ParseId("https://space.bilibili.com/123895/lists/3945451?type=series")
	assert.NoError(t, err)
	assert.Equal(t, "123895/3945451", sid2)

	uid, err := c.ParseId("123895")
	assert.NoError(t, err)
	assert.EqualValues(t, 123895, uid)
}

func TestNewSeriesArchives(t *testing.T) {
	mk := func(aids ...int64) []*SeriesArchive {
		var result []*SeriesArchive
		for _, aid := range aids {
			result = append(result, &SeriesArchive{Aid: aid, Bvid: "BV" + string(rune('a'+aid%26))})
		}
		return result
	}

	t.Run("首次订阅(基线为最新)不补推", func(t *testing.T) {
		got := NewSeriesArchives(mk(30, 20, 10), 30)
		assert.Empty(t, got)
	})

	t.Run("返回新增并按发布时间升序", func(t *testing.T) {
		got := NewSeriesArchives(mk(50, 40, 30, 20, 10), 30)
		assert.Len(t, got, 2)
		assert.EqualValues(t, 40, got[0].Aid)
		assert.EqualValues(t, 50, got[1].Aid)
	})

	t.Run("基线为0时全部视为新增", func(t *testing.T) {
		got := NewSeriesArchives(mk(30, 10, 20), 0)
		assert.Len(t, got, 3)
		assert.EqualValues(t, 10, got[0].Aid)
		assert.EqualValues(t, 30, got[2].Aid)
	})

	t.Run("忽略无效条目", func(t *testing.T) {
		got := NewSeriesArchives([]*SeriesArchive{nil, {Aid: 0}, {Aid: 41}}, 40)
		assert.Len(t, got, 1)
		assert.EqualValues(t, 41, got[0].Aid)
	})

	t.Run("空列表", func(t *testing.T) {
		assert.Empty(t, NewSeriesArchives(nil, 0))
	})
}

func TestParseSeriesArchives(t *testing.T) {
	t.Run("正常返回", func(t *testing.T) {
		body := `{"code":0,"message":"0","data":{"archives":[{"aid":117405967715083,"bvid":"BV1L3HX6wEP1","title":"【直播回放】测试","pubdate":1791474647,"pic":"http://i2.hdslb.com/x.jpg","upMid":123895}],"page":{"num":1,"size":30,"total":475}}}`
		archives, err := parseSeriesArchives([]byte(body))
		assert.NoError(t, err)
		assert.Len(t, archives, 1)
		assert.EqualValues(t, 117405967715083, archives[0].Aid)
		assert.Equal(t, "BV1L3HX6wEP1", archives[0].Bvid)
		assert.EqualValues(t, 123895, archives[0].UpMid)
	})

	t.Run("空合集", func(t *testing.T) {
		archives, err := parseSeriesArchives([]byte(`{"code":0,"data":{"archives":[]}}`))
		assert.NoError(t, err)
		assert.Empty(t, archives)
	})

	t.Run("接口报错", func(t *testing.T) {
		_, err := parseSeriesArchives([]byte(`{"code":-400,"message":"请求错误"}`))
		assert.Error(t, err)
	})

	t.Run("空响应", func(t *testing.T) {
		_, err := parseSeriesArchives([]byte("   "))
		assert.Error(t, err)
	})

	t.Run("非JSON", func(t *testing.T) {
		_, err := parseSeriesArchives([]byte("<html>风控</html>"))
		assert.Error(t, err)
	})
}

func TestParseSeriesMeta(t *testing.T) {
	t.Run("正常返回", func(t *testing.T) {
		body := `{"code":0,"data":{"meta":{"series_id":3945451,"mid":123895,"name":"直播回放","total":475,"last_update_ts":1791474648}}}`
		meta, err := parseSeriesMeta([]byte(body))
		assert.NoError(t, err)
		assert.Equal(t, "直播回放", meta.Name)
		assert.EqualValues(t, 123895, meta.Mid)
		assert.EqualValues(t, 475, meta.Total)
	})

	t.Run("合集不存在", func(t *testing.T) {
		_, err := parseSeriesMeta([]byte(`{"code":0,"data":{}}`))
		assert.Error(t, err)
	})

	t.Run("接口报错", func(t *testing.T) {
		_, err := parseSeriesMeta([]byte(`{"code":-404,"message":"啥都木有"}`))
		assert.Error(t, err)
	})
}
