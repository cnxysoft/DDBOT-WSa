package cfg

import "testing"

// 块标量与多行纯量里的内容只是文本，不能拿它们判重复键或全角冒号。
//
// 回归背景：盲查发现块标量之后的内容行只要长得像「键: 值」就会被登记成键，
// 同一段里出现两行就报 duplicate_key —— 而函数的承诺是「宁可漏报也不误报」。
func TestCheckConfigFormatSkipsScalarBlocks(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"块标量 | 里的重复键", "a: |\n  key: a\n  key: b\n"},
		{"块标量 > 里的重复键", "a: >\n  time: 2024-02-02\n  time: 2025-01-01\n"},
		{"块标量里的全角冒号", "a: |\n  时间：2024\n  时间：2025\n"},
		{"多行纯量的续行", "a: 这是一段\n  not: a key\n  not: b key\n"},
		{"值里的全角冒号", "a:\n  name: 注意：不要\n"},
		{"注释里的全角冒号", "# 说明：xxx\na: 1\n"},
	}
	for _, c := range cases {
		if got := CheckConfigFormat(c.content); len(got) != 0 {
			t.Errorf("[%s] 不该报，got %+v", c.name, got)
		}
	}
}

// 全角冒号后面跟裸标量也是同一类静默失效，不能只认「右边像值」的那几种。
func TestCheckConfigFormatFullwidthColonBareValue(t *testing.T) {
	for _, content := range []string{
		"bilibili:\n  SESSDATA：abc123\n",
		"a:\n  b：1\n",
		"customCommandPrefix:\n  签到： \"\"\n",
	} {
		got := CheckConfigFormat(content)
		if len(got) != 1 || got[0].Kind != "fullwidth_colon" {
			t.Fatalf("[%q] 应报 1 处全角冒号，got %+v", content, got)
		}
	}
}
