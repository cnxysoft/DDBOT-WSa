package cfg

import "testing"

// 干净配置一处都不该报 —— 这个检查在启动时跑，误报会变成噪音。
func TestCheckConfigFormatClean(t *testing.T) {
	clean := "bot:\n" +
		"  commandPrefix: \"/\"\n" +
		"customCommandPrefix:\n" +
		"  签到: \"\"\n" +
		"bilibili:\n" +
		"  SESSDATA: \"x\"\n" +
		"  bili_jct: \"y\"\n" +
		"list:\n" +
		"  - name: a\n" +
		"    value: 1\n" +
		"  - name: b\n" +
		"    value: 2\n"
	if got := CheckConfigFormat(clean); len(got) != 0 {
		t.Fatalf("干净配置不该报问题，got %+v", got)
	}
}

// 全角冒号：群友真机上踩过的那条，必须报出来。
func TestCheckConfigFormatFullwidthColon(t *testing.T) {
	got := CheckConfigFormat("customCommandPrefix:\n  签到： \"\"\n")
	if len(got) != 1 || got[0].Kind != "fullwidth_colon" || got[0].Line != 2 {
		t.Fatalf("应报第 2 行全角冒号，got %+v", got)
	}
}

// 全角冒号出现在注释、值、块标量里都不能误报。
func TestCheckConfigFormatFullwidthColonNoFalsePositive(t *testing.T) {
	cases := []string{
		"# 说明：这是注释\n",
		"bot:\n  name: 注意：不要这样\n",          // 半角冒号在前，是全角在值里
		"bot:\n  desc: |\n    注意：这是一段说明\n", // 块标量
		"bot:\n  注意：这是一段说明\n",              // 右边不像值 -> 宁可漏报
	}
	for _, s := range cases {
		if got := CheckConfigFormat(s); len(got) != 0 {
			t.Fatalf("不该误报: %q -> %+v", s, got)
		}
	}
}

// 同层等价键：大小写不同也算同一个（viper 大小写不敏感）。
func TestCheckConfigFormatDuplicateKeyCaseInsensitive(t *testing.T) {
	got := CheckConfigFormat("bilibili:\n  sessdata: \"a\"\n  SESSDATA: \"b\"\n")
	if len(got) != 1 || got[0].Kind != "duplicate_key" || got[0].Line != 3 {
		t.Fatalf("应报第 3 行重复键，got %+v", got)
	}
}

// 完全相同的键重复写也算。
func TestCheckConfigFormatDuplicateKeyExact(t *testing.T) {
	got := CheckConfigFormat("weibo:\n  sub: \"a\"\n  sub: \"b\"\n")
	if len(got) != 1 || got[0].Line != 3 {
		t.Fatalf("应报第 3 行重复键，got %+v", got)
	}
}

// 不同段下的同名键不是重复。
func TestCheckConfigFormatSameKeyDifferentSections(t *testing.T) {
	if got := CheckConfigFormat("a:\n  interval: 1s\nb:\n  interval: 2s\n"); len(got) != 0 {
		t.Fatalf("不同段下的同名键不该报，got %+v", got)
	}
}

// 序列里每一项都有的键不能互相误报。
func TestCheckConfigFormatSequenceNoFalsePositive(t *testing.T) {
	if got := CheckConfigFormat("list:\n  - name: a\n    value: 1\n  - name: b\n    value: 2\n"); len(got) != 0 {
		t.Fatalf("序列项里的键不该误报，got %+v", got)
	}
}

func TestCheckConfigFormatTabIndent(t *testing.T) {
	got := CheckConfigFormat("bot:\n\tcommandPrefix: \"/\"\n")
	if len(got) != 1 || got[0].Kind != "tab_indent" {
		t.Fatalf("应报 Tab 缩进，got %+v", got)
	}
}

// 行号要准，且要带上原始行方便用户定位。
func TestCheckConfigFormatReportsLineAndText(t *testing.T) {
	got := CheckConfigFormat("a: 1\nb: 2\nb: 3\n")
	if len(got) != 1 {
		t.Fatalf("应报 1 处，got %+v", got)
	}
	if got[0].Line != 3 || got[0].Text != "b: 3" {
		t.Fatalf("行号/原文不对：%+v", got[0])
	}
}
