package huya

import (
	"errors"
	"strings"
	"testing"
)

func TestParseProfileRoom(t *testing.T) {
	cases := []struct {
		name             string
		roomId           string
		body             string
		living           bool
		nick             string
		avatar           string
		roomName         string
		wantErr          error
		errShouldContain string
	}{
		{
			name:     "直播中",
			roomId:   "660000",
			body:     `{"status":200,"message":"","data":{"liveStatus":"ON","profileInfo":{"nick":"虎牙英雄联盟赛事","avatar180":"https://a.msstatic.com/x.jpg"},"liveData":{"roomName":"德玛西亚杯","introduction":"简介"}}}`,
			living:   true,
			nick:     "虎牙英雄联盟赛事",
			avatar:   "https://a.msstatic.com/x.jpg",
			roomName: "德玛西亚杯",
		},
		{
			name:     "回放中（roomName 为空时退回简介）",
			roomId:   "10001",
			body:     `{"status":200,"data":{"liveStatus":"REPLAY","profileInfo":{"nick":"贤菜xc","avatar180":"//a.msstatic.com/y.jpg"},"liveData":{"roomName":"","introduction":"操作之星"}}}`,
			living:   false,
			nick:     "贤菜xc",
			avatar:   "https://a.msstatic.com/y.jpg",
			roomName: "操作之星",
		},
		{
			name:   "未开播",
			roomId: "518512",
			body:   `{"status":200,"data":{"liveStatus":"OFF","profileInfo":{"nick":"颜色-GED在石250"},"liveData":{}}}`,
			living: false,
			nick:   "颜色-GED在石250",
		},
		{
			name:             "房间不存在（data 是数组）",
			roomId:           "99999",
			body:             `{"status":422,"message":"该主播不存在！","data":[]}`,
			errShouldContain: "status=422",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseProfileRoom(c.roomId, []byte(c.body))
			if c.errShouldContain != "" {
				if err == nil || !strings.Contains(err.Error(), c.errShouldContain) {
					t.Fatalf("want err containing %q, got %v", c.errShouldContain, err)
				}
				return
			}
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("want err %v, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if got.IsLiving != c.living {
				t.Errorf("IsLiving = %v, want %v", got.IsLiving, c.living)
			}
			if got.Name != c.nick {
				t.Errorf("Name = %q, want %q", got.Name, c.nick)
			}
			if got.Avatar != c.avatar {
				t.Errorf("Avatar = %q, want %q", got.Avatar, c.avatar)
			}
			if got.RoomName != c.roomName {
				t.Errorf("RoomName = %q, want %q", got.RoomName, c.roomName)
			}
			if got.RoomId != c.roomId {
				t.Errorf("RoomId = %q, want %q", got.RoomId, c.roomId)
			}
		})
	}
}

// 页面同时渲染 host-spectator 与 host-prevStartTime，不能再靠“唯一 span”判定
func TestParseRoomPage(t *testing.T) {
	cases := []struct {
		name     string
		page     string
		living   bool
		nick     string
		avatar   string
		roomName string
		wantErr  error
	}{
		{
			name: "直播中：两个 span 并存 + 头像在 .host-pic",
			page: `<html><body class="liveStatus-on on-match">` +
				`<h3 class="host-name" title="贤菜xc"></h3>` +
				`<span class="host-spectator"><span id="live-count">15.1万</span></span>` +
				`<span class="host-prevStartTime"><span>14:09</span></span>` +
				`<h2 class="host-title" title="标题"></h2>` +
				`<a class="host-pic"><div><img src="//a.msstatic.com/z.jpg"></div></a>` +
				`</body></html>`,
			living:   true,
			nick:     "贤菜xc",
			avatar:   "https://a.msstatic.com/z.jpg",
			roomName: "标题",
		},
		{
			name: "回放中",
			page: `<html><body class="liveStatus-replay on-match">` +
				`<h3 class="host-name" title="贤菜xc"></h3>` +
				`<span class="host-spectator"><span id="live-count">15.1万</span></span>` +
				`<span class="host-prevStartTime"><span>正在回放 昨天 14:09</span></span>` +
				`<h2 class="host-title" title="【重播】操作之星"></h2>` +
				`</body></html>`,
			living:   false,
			nick:     "贤菜xc",
			roomName: "【重播】操作之星",
		},
		{
			name:    "房间不存在",
			page:    `<html><body>找不到这个主播</body></html>`,
			wantErr: ErrRoomNotExist,
		},
		{
			name:    "涉嫌违规",
			page:    `<html><body>涉嫌违规，正在整改中</body></html>`,
			wantErr: ErrRoomBanned,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseRoomPage("10001", []byte(c.page))
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("want err %v, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if got.IsLiving != c.living {
				t.Errorf("IsLiving = %v, want %v", got.IsLiving, c.living)
			}
			if got.Name != c.nick {
				t.Errorf("Name = %q, want %q", got.Name, c.nick)
			}
			if got.Avatar != c.avatar {
				t.Errorf("Avatar = %q, want %q", got.Avatar, c.avatar)
			}
			if got.RoomName != c.roomName {
				t.Errorf("RoomName = %q, want %q", got.RoomName, c.roomName)
			}
		})
	}
}

// 老页面没有 liveStatus-* class 时，退回旧判定
func TestParseRoomPageLegacyClass(t *testing.T) {
	page := `<html><body><span class="host-spectator"></span>` +
		`<h3 class="host-name" title="n"></h3><h2 class="host-title" title="t"></h2></body></html>`
	got, err := parseRoomPage("1", []byte(page))
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if !got.IsLiving {
		t.Errorf("IsLiving = false, want true")
	}
}
func TestCoverFallback(t *testing.T) {
	withCover := `{"status":200,"data":{"liveStatus":"ON","profileInfo":{"nick":"n","avatar180":"https://a/x.jpg"},"liveData":{"roomName":"t","screenshot":"https://c/s.jpg"}}}`
	got, err := parseProfileRoom("1", []byte(withCover))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cover != "https://c/s.jpg" || got.GetCover() != "https://c/s.jpg" {
		t.Errorf("Cover = %q, GetCover() = %q, want https://c/s.jpg", got.Cover, got.GetCover())
	}

	noCover := `{"status":200,"data":{"liveStatus":"OFF","profileInfo":{"nick":"n","avatar180":"https://a/x.jpg"},"liveData":{}}}`
	got2, err := parseProfileRoom("1", []byte(noCover))
	if err != nil {
		t.Fatal(err)
	}
	if got2.Cover != "" {
		t.Errorf("Cover = %q, want empty", got2.Cover)
	}
	if got2.GetCover() != "https://a/x.jpg" {
		t.Errorf("GetCover() = %q, want fallback to avatar", got2.GetCover())
	}

	// 页面兜底：从内嵌 TT_ROOM_DATA 提取（含 JSON 转义的 \/）
	page := `<html><body class="liveStatus-on">` +
		`<h3 class="host-name" title="n"></h3><h2 class="host-title" title="t"></h2>` +
		`<script>var TT_ROOM_DATA = {"screenshot":"https:\/\/c\/s.jpg"};</script>` +
		`</body></html>`
	got3, err := parseRoomPage("1", []byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if got3.Cover != "https://c/s.jpg" {
		t.Errorf("page Cover = %q, want https://c/s.jpg", got3.Cover)
	}
}
