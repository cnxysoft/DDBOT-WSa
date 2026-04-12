package bilibili

import (
	"os"
	"testing"
	"time"

	jsoniter "github.com/json-iterator/go"
)

// timestampFormat 模拟 localutils.TimestampFormat
func timestampFormat(ts int64) string {
	t := time.Unix(ts, 0).In(time.FixedZone("CST", 8*3600))
	return t.Format("2006年01月02日 15:04")
}

// TestGetDescContent_Multi 测试多组 dynamic_detail JSON
// 用途：验证 getDescContent 能正确解析各种类型的动态
func TestGetDescContent_Multi(t *testing.T) {
	testCases := []struct {
		name              string
		detailFile        string
		targetDyId        string // 动态ID，用于从 new JSON 中查找卡片
		newFile           string
		expectMainEmojis    int // 期望主动态 Emoji 数量
		expectOriginEmojis  int // 期望原动态 Emoji 数量
		mainContent       string // 期望主动态 Content 包含的字符串
		originContent     string // 期望原动态 Content 包含的字符串
	}{
		{
			name:              "转发动态-emoji在原动态(动态详情2)",
			detailFile:        "res/dynamic_detail_2.json",
			targetDyId:        "1189906589005381649",
			newFile:           "res/dynamic_new_2.json",
			expectMainEmojis:   0, // 主动态只有"转发动态"，emoji在origin
			expectOriginEmojis: 1, // NoWorld emoji 在原动态
			mainContent:       "转发动态",
			originContent:     "NoWorld_POWER",
		},
		{
			name:              "转发动态-emoji在主动态(动态详情3)",
			detailFile:        "res/dynamic_detail_3.json",
			targetDyId:        "1189906090826924073",
			newFile:           "res/dynamic_new_3.json",
			expectMainEmojis:   2, // 主动态 desc.rich_text_nodes 有2个 emoji
			expectOriginEmojis: 0, // 原动态是视频，无 emoji
			mainContent:       "妮莉安Lily",
			originContent:     "", // 原动态是视频，content 在 Video.Desc，不在 Content
		},
		{
			name:              "原始测试-emoji在正文(含转发-动态详情1)",
			detailFile:        "res/dynamic_detail.json",
			targetDyId:        "1189893961821454336",
			newFile:           "res/dynamic_new.json",
			expectMainEmojis:   1, // 主动态 desc.rich_text_nodes 有1个 emoji
			expectOriginEmojis: 2, // 原动态 emoji 出现2次所以2个
			mainContent:       "模糊小黄豆",
			originContent:     "幼年沐表情包25张_死机",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 加载 detail JSON
			detailData, err := os.ReadFile(tc.detailFile)
			if err != nil {
				t.Fatalf("ReadFile %s failed: %v", tc.detailFile, err)
			}
			var detailResp map[string]interface{}
			err = jsoniter.Unmarshal(detailData, &detailResp)
			if err != nil {
				t.Fatalf("Unmarshal %s failed: %v", tc.detailFile, err)
			}

			// 解析
			detail := getDescContent(detailResp, false)
			originDetail := getDescContent(detailResp, true)

			// 检查主动态 Emoji 数量
			if len(detail.Emojis) != tc.expectMainEmojis {
				t.Errorf("Main Detail.Emojis count = %d, want %d", len(detail.Emojis), tc.expectMainEmojis)
				for i, e := range detail.Emojis {
					t.Logf("  Main Emoji[%d]: Text=%q IconUrl=%q", i, e.Text, e.IconUrl)
				}
			}

			// 检查原动态 Emoji 数量
			if len(originDetail.Emojis) != tc.expectOriginEmojis {
				t.Errorf("OriginDetail.Emojis count = %d, want %d", len(originDetail.Emojis), tc.expectOriginEmojis)
				for i, e := range originDetail.Emojis {
					t.Logf("  Origin Emoji[%d]: Text=%q IconUrl=%q", i, e.Text, e.IconUrl)
				}
			}

			// 检查主动态 Content
			if !contains(detail.Content, tc.mainContent) {
				t.Errorf("Main Content = %q, want to contain %q", detail.Content, tc.mainContent)
			}

			// 检查原动态 Content
			if !contains(originDetail.Content, tc.originContent) {
				t.Errorf("OriginDetail Content = %q, want to contain %q", originDetail.Content, tc.originContent)
			}

			// 打印完整结构供调试
			t.Logf("=== %s ===", tc.name)
			t.Logf("Main Content: %q", detail.Content)
			t.Logf("Main Emojis: %d", len(detail.Emojis))
			for i, e := range detail.Emojis {
				t.Logf("  [%d] Text=%q IconUrl=%q", i, e.Text, e.IconUrl)
			}
			t.Logf("Origin Content: %q", originDetail.Content)
			t.Logf("Origin Emojis: %d", len(originDetail.Emojis))
			for i, e := range originDetail.Emojis {
				t.Logf("  [%d] Text=%q IconUrl=%q", i, e.Text, e.IconUrl)
			}
		})
	}
}

// TestDataFlow_ForwardVideo 测试被转发视频的数据流
// 验证模板使用 origin_detail 渲染被转发内容时，能否正确获取视频信息
func TestDataFlow_ForwardVideo(t *testing.T) {
	// 加载 dynamic_detail_3.json
	detailData, err := os.ReadFile("res/dynamic_detail_3.json")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var detailResp map[string]interface{}
	err = jsoniter.Unmarshal(detailData, &detailResp)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	originDetail := getDescContent(detailResp, true)

	t.Logf("=== 被转发视频数据分析 ===")
	t.Logf("")
	t.Logf("煎饺Turbo (原动态作者) 的数据:")
	t.Logf("  originDetail.Author.Name: %s", originDetail.Author.Name)
	t.Logf("  originDetail.Content: %q", originDetail.Content)
	t.Logf("  originDetail.Archive.Title: %q", originDetail.Archive.Title)
	t.Logf("  originDetail.Archive.Cover: %q", originDetail.Archive.Cover)
	t.Logf("  originDetail.Archive.Aid: %s", originDetail.Archive.Aid)
	t.Logf("")
	t.Logf("模板 media 块 type=8 期望的数据:")
	t.Logf("  {{.video.title}} - 但 originDetail 没有 video 字段!")
	t.Logf("  {{.video.cover_url}} - 但 originDetail 没有 video 字段!")
	t.Logf("  {{.video.desc}} - 但 originDetail 没有 video 字段!")
	t.Logf("")
	t.Logf("实际数据在:")
	t.Logf("  originDetail.Archive.Title: %q", originDetail.Archive.Title)
	t.Logf("  originDetail.Archive.Cover: %q", originDetail.Archive.Cover)
	t.Logf("")
	t.Logf("=== 问题 ===")
	t.Logf("模板的 media 块 type=8 使用 {{.video.xxx}} 访问视频数据")
	t.Logf("但传入 origin_detail 时，数据在 {{.archive.xxx}} 而不是 {{.video.xxx}}")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
