package bilibili

import (
	"fmt"
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
			detailFile:        "../../debug/dynamic_detail_2.json",
			targetDyId:        "1189906589005381649",
			newFile:           "../../debug/dynamic_new_2.json",
			expectMainEmojis:   0, // 主动态只有"转发动态"，emoji在origin
			expectOriginEmojis: 1, // NoWorld emoji 在原动态
			mainContent:       "转发动态",
			originContent:     "NoWorld_POWER",
		},
		{
			name:              "转发动态-emoji在主动态(动态详情3)",
			detailFile:        "../../debug/dynamic_detail_3.json",
			targetDyId:        "1189906090826924073",
			newFile:           "../../debug/dynamic_new_3.json",
			expectMainEmojis:   2, // 主动态 desc.rich_text_nodes 有2个 emoji
			expectOriginEmojis: 0, // 原动态是视频，无 emoji
			mainContent:       "妮莉安Lily",
			originContent:     "", // 原动态是视频，content 在 Video.Desc，不在 Content
		},
		{
			name:              "原始测试-emoji在正文(含转发-动态详情1)",
			detailFile:        "../../debug/dynamic_detail.json",
			targetDyId:        "1189893961821454336",
			newFile:           "../../debug/dynamic_new.json",
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

// TestSnapCastJSON 生成 SnapCast 测试 JSON
func TestSnapCastJSON(t *testing.T) {
	// 从 detail JSON 中提取 dynamic_id
	getDynamicId := func(data map[string]interface{}) string {
		if basic, ok := data["data"].(map[string]interface{}); ok {
			if item, ok := basic["item"].(map[string]interface{}); ok {
				if basic2, ok := item["basic"].(map[string]interface{}); ok {
					if id, ok := basic2["comment_id_str"].(string); ok {
						return id
					}
				}
			}
		}
		return "0000000000000000000"
	}

	getOrigDyId := func(data map[string]interface{}) string {
		if basic, ok := data["data"].(map[string]interface{}); ok {
			if item, ok := basic["item"].(map[string]interface{}); ok {
				if basic2, ok := item["basic"].(map[string]interface{}); ok {
					if rid, ok := basic2["rid_str"].(string); ok {
						return rid
					}
				}
			}
		}
		return "0000000000000000000"
	}

	testCases := []struct {
		name       string
		detailFile string
	}{
		{"dynamic_detail_4", "../../debug/dynamic_detail_4.json"},
		{"dynamic_detail_3", "../../debug/dynamic_detail_3.json"},
		{"dynamic_detail_2", "../../debug/dynamic_detail_2.json"},
		{"dynamic_detail_1", "../../debug/dynamic_detail.json"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			detailData, err := os.ReadFile(tc.detailFile)
			if err != nil {
				t.Fatalf("ReadFile failed: %v", err)
			}
			var detailResp map[string]interface{}
			err = jsoniter.Unmarshal(detailData, &detailResp)
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			detail := getDescContent(detailResp, false)
			originDetail := getDescContent(detailResp, true)
			dynamicId := getDynamicId(detailResp)
			origDyId := getOrigDyId(detailResp)

			renderData := map[string]interface{}{
				"dynamic": map[string]interface{}{
					"type":         1,
					"id":           dynamicId,
					"with_origin":  true,
					"origin_dy_id": origDyId,
					"date":         "2026-04-11 12:00:00",
					"content":       detail.Content,
					"title":        detail.Title,
					"topic_name":   detail.TopicName,
					"dynamic_url":   "https://www.bilibili.com/opus/" + dynamicId,
					"detail":        detail,
					"origin_detail": originDetail,
					"user": map[string]interface{}{
						"uid":  detail.Author.Uid,
						"name": detail.Author.Name,
						"face": detail.Author.Face,
					},
					"origin_user": map[string]interface{}{
						"uid":  originDetail.Author.Uid,
						"name": originDetail.Author.Name,
						"face": originDetail.Author.Face,
					},
				},
				"ContentWithEmoji":       detail.Content,
				"OriginContentWithEmoji": originDetail.Content,
			}

			payload := map[string]interface{}{
				"site": "bilibili",
				"type": "news",
				"data": renderData,
			}

			b, _ := jsoniter.MarshalIndent(payload, "", "  ")
			filename := tc.detailFile
			filename = replaceExt(filename, "_snapcast.json")
			os.WriteFile(filename, b, 0644)
			t.Logf("Written: %s", filename)
			fmt.Printf("\n=== %s ===\n%s\n", tc.name, b)
		})
	}
}

func replaceExt(filename, suffix string) string {
	if len(filename) > 5 && filename[len(filename)-5:] == ".json" {
		return filename[:len(filename)-5] + suffix
	}
	return filename + suffix
}

// TestTemplateRender_Full 完整流程测试
func TestTemplateRender_Full(t *testing.T) {
	// 加载并测试 dynamic_detail_3（转发-emoji在主动态）
	detailData, err := os.ReadFile("../../debug/dynamic_detail_3.json")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var detailResp map[string]interface{}
	err = jsoniter.Unmarshal(detailData, &detailResp)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	detail := getDescContent(detailResp, false)
	originDetail := getDescContent(detailResp, true)

	// 诊断：打印 Content 完整内容，检查是否有 JSON 泄露
	t.Logf("\n=== Content诊断 ===")
	t.Logf("Detail.Content = %q", detail.Content)
	t.Logf("Detail.Content length = %d", len(detail.Content))

	// 查找是否有 { 或 } 异常出现的位置
	for i, c := range detail.Content {
		if c == '{' || c == '}' {
			t.Logf("  Char at pos %d: '%c' (rune %d)", i, c, c)
			if i+1 < len(detail.Content) {
				t.Logf("    Context: ...%q...", detail.Content[testMax(0, i-10):testMin(len(detail.Content), i+30)])
			}
		}
	}

	// 模拟 prepare() + 模板渲染数据构造
	renderData := map[string]interface{}{
		"dynamic": map[string]interface{}{
			"type":          1, // DynamicDescType_WithOrigin
			"id":            "1189906090826924073",
			"with_origin":   true,
			"origin_dy_id":   "1189905816374825984",
			"date":          "2026年04月11日 13:23",
			"content":        detail.Content,
			"title":         detail.Title,
			"topic_name":    detail.TopicName,
			"dynamic_url":    "https://www.bilibili.com/opus/1189906090826924073",
			"detail":         detail,
			"origin_detail":  originDetail,
			"user": map[string]interface{}{
				"uid":  107493555,
				"name": "D_Alen",
				"face": "https://i0.hdslb.com/bfs/face/88f3c376caecf997965da7487de52a466fd415f6.jpg",
			},
			"origin_user": map[string]interface{}{
				"uid":  367157377,
				"name": "煎饺Turbo",
				"face": "https://i1.hdslb.com/bfs/face/0a9d23c5e5bd6ba94d537be532a2f07433362903.jpg",
			},
		},
		"ContentWithEmoji":       detail.Content,
		"OriginContentWithEmoji": originDetail.Content,
	}

	b, _ := jsoniter.MarshalIndent(renderData, "", "  ")
	fmt.Printf("\n=== Render JSON for SnapCast ===\n%s\n", b)

	// 关键检查
	t.Logf("\n=== Key Checks ===")
	t.Logf("ContentWithEmoji (main): %q", renderData["ContentWithEmoji"])
	t.Logf("Detail.Emojis count: %d", len(detail.Emojis))
	for i, e := range detail.Emojis {
		t.Logf("  [%d] Text=%q IconUrl=%q", i, e.Text, e.IconUrl)
	}
	t.Logf("OriginContentWithEmoji: %q", renderData["OriginContentWithEmoji"])
	t.Logf("OriginDetail.Emojis count: %d", len(originDetail.Emojis))
	for i, e := range originDetail.Emojis {
		t.Logf("  [%d] Text=%q", i, e.Text)
	}

	// 问题诊断：ContentWithEmoji 中是否包含 Emoji 文本？
	for _, e := range detail.Emojis {
		if contains(detail.Content, e.Text) {
			t.Logf("✓ Emoji text %q IS in ContentWithEmoji", e.Text)
		} else {
			t.Errorf("✗ Emoji text %q NOT in ContentWithEmoji! Emoji replacement will not work.", e.Text)
		}
	}
}

// TestDataFlow_ForwardVideo 测试被转发视频的数据流
// 验证模板使用 origin_detail 渲染被转发内容时，能否正确获取视频信息
func TestDataFlow_ForwardVideo(t *testing.T) {
	// 加载 dynamic_detail_3.json
	detailData, err := os.ReadFile("../../debug/dynamic_detail_3.json")
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

func testMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func testMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
