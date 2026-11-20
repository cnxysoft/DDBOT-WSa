package xhs

import (
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
)

func TestLiveInfoGetMSGCachesRenderedTemplate(t *testing.T) {
	liveInfo := &LiveInfo{
		UserInfo: UserInfo{
			Uid:    "u1",
			Name:   "name",
			RoomId: "r1",
		},
		Status:    LiveStatus_Living,
		LiveTitle: "title",
		Cover:     "cover",
		Url:       "https://www.xiaohongshu.com/livestream/r1",
	}

	msg1 := liveInfo.GetMSG()
	msg2 := liveInfo.GetMSG()
	if msg1 == nil || msg2 == nil {
		t.Fatalf("GetMSG() should return non-nil messages")
	}
	if msg1 != msg2 {
		t.Fatalf("GetMSG() should reuse cached rendered message")
	}
}

func TestCloneWithStatusChangedResetsMessageCache(t *testing.T) {
	liveInfo := &LiveInfo{
		UserInfo: UserInfo{
			Uid:    "u1",
			Name:   "name",
			RoomId: "r1",
		},
		Status:    LiveStatus_Living,
		LiveTitle: "title",
		Cover:     "cover",
		Url:       "https://www.xiaohongshu.com/livestream/r1",
	}

	origMsg := liveInfo.GetMSG()
	clone := liveInfo.CloneWithStatusChanged()
	cloneMsg := clone.GetMSG()
	if origMsg == nil || cloneMsg == nil {
		t.Fatalf("GetMSG() should return non-nil messages")
	}
	if origMsg == cloneMsg {
		t.Fatalf("CloneWithStatusChanged() should reset message cache for the clone")
	}
}

func TestNoteInfoGetMSGRendersTitleAndURL(t *testing.T) {
	note := &NoteInfo{
		UserInfo: UserInfo{
			Uid:        "u1",
			UserId:     "u1",
			Name:       "target-nick",
			ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
		},
		NoteID:   "note-1",
		Title:    "note-title",
		Cover:    "https://example.com/cover.jpg",
		Url:      "https://www.xiaohongshu.com/explore/note-1",
		NoteType: "normal",
	}

	msg := note.GetMSG()
	if msg == nil {
		t.Fatalf("GetMSG() should return non-nil message")
	}
	text := msgstringer.AdapterMsgToString(msg.ToCombineMessage(mmsg.NewGroupTarget(0)).Elements)
	if text == "" {
		t.Fatalf("rendered text should not be empty")
	}
	if !containsAll(text, "note-title", "https://www.xiaohongshu.com/explore/note-1") {
		t.Fatalf("rendered text = %q, want title and url", text)
	}
}

func TestNoteInfoGetMSGRendersVideoLabel(t *testing.T) {
	note := &NoteInfo{
		UserInfo: UserInfo{
			Uid:    "u1",
			UserId: "u1",
			Name:   "target-nick",
		},
		NoteID:   "note-1",
		Title:    "video-note",
		Desc:     "video-desc",
		Pictures: []string{"https://example.com/p1.jpg"},
		Url:      "https://www.xiaohongshu.com/explore/note-1",
		NoteType: NoteTypeVideo,
	}

	msg := note.GetMSG()
	if msg == nil {
		t.Fatalf("GetMSG() should return non-nil message")
	}
	text := msgstringer.AdapterMsgToString(msg.ToCombineMessage(mmsg.NewGroupTarget(0)).Elements)
	if !containsAll(text, "发布了新视频", "video-note", "video-desc") {
		t.Fatalf("rendered text = %q, want video label, title and desc", text)
	}
}

func TestNoteInfoGetMSGRendersNormalLabel(t *testing.T) {
	note := &NoteInfo{
		UserInfo: UserInfo{
			Uid:    "u1",
			UserId: "u1",
			Name:   "target-nick",
		},
		NoteID:   "note-2",
		Title:    "normal-note",
		Url:      "https://www.xiaohongshu.com/explore/note-2",
		NoteType: NoteTypeNormal,
	}

	msg := note.GetMSG()
	if msg == nil {
		t.Fatalf("GetMSG() should return non-nil message")
	}
	text := msgstringer.AdapterMsgToString(msg.ToCombineMessage(mmsg.NewGroupTarget(0)).Elements)
	if !containsAll(text, "发布了新图文", "normal-note") {
		t.Fatalf("rendered text = %q, want normal label and title", text)
	}
}

func containsAll(text string, wants ...string) bool {
	for _, want := range wants {
		if !strings.Contains(text, want) {
			return false
		}
	}
	return true
}
