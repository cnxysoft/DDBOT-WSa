package bilibili

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/stretchr/testify/assert"
)

// TestSeriesDispatchFilterMatches 复现框架 DefaultDispatch 的分发过滤：
// 只有 event.GetUid() 与订阅 id 相等、且订阅类型包含事件类型时，群才会收到推送。
func TestSeriesDispatchFilterMatches(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	// 生产环境由 StateManager.Start() 建立 ConcernState 索引，
	// 测试里手动建，否则 Ascend 会因索引缺失返回 ErrNotFound
	assert.NoError(t, c.StateManager.CreatePatternIndex(c.GroupConcernStateKey, nil))
	const groupCode int64 = 685164226
	const sid = "123895/3945451"

	_, err := c.StateManager.AddGroupConcern(groupCode, sid, Series)
	assert.NoError(t, err)

	ctype, gerr := c.StateManager.GetConcern(sid)
	t.Logf("GetConcern(%s) => %v, err=%v", sid, ctype, gerr)

	event := &SeriesNewInfo{
		SeriesInfo: &SeriesInfo{Mid: 123895, SeriesId: 3945451, Name: "直播回放", LastAid: 1},
		UpName:     "文七传个火",
		Archive:    &SeriesArchive{Aid: 117405967715083, Bvid: "BV1L3HX6wEP1", Title: "【直播回放】测试"},
	}

	groups, _, _, err := c.StateManager.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool {
		return event.GetUid() == id && p.ContainAll(event.Type())
	})
	assert.NoError(t, err)
	assert.Equal(t, []int64{groupCode}, groups, "合集订阅应能匹配到群")

	notifies := c.notifyGenerator()(groupCode, event)
	assert.Len(t, notifies, 1, "合集事件应生成一条推送")
	assert.Equal(t, groupCode, notifies[0].GetGroupCode())

	// 回归：bilibili 的 FilterHook 必须放行合集推送，
	// 历史上只处理了 live/news 两种类型，系列会被静默丢弃
	concernConfig := c.StateManager.GetGroupConcernConfig(groupCode, sid)
	assert.True(t, concernConfig.FilterHook(notifies[0]).Pass, "未配置过滤时合集推送应放行")
}
