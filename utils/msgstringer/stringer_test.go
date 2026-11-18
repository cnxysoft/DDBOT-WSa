package msgstringer

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

func TestAdapterMsgToString(t *testing.T) {
	m := []adapter.IMessageElement{
		&adapter.FaceSegment{Index: 1},
		&adapter.TextSegment{Content: "q"},
		&adapter.ImageSegment{},
		&adapter.AtSegment{Target: 0},
		&adapter.FileSegment{},
		&adapter.VideoSegment{},
		&adapter.ForwardSegment{},
		&adapter.JsonSegment{},
		&adapter.VoiceSegment{},
		&adapter.ReplySegment{ReplySeq: 199},
		nil,
	}
	AdapterMsgToString(m)
}

func TestAdapterMsgToStringSkipsNilElements(t *testing.T) {
	elements := []adapter.IMessageElement{
		(*adapter.TextSegment)(nil),
		&adapter.TextSegment{Content: "正文"},
		(*adapter.ReplySegment)(nil),
		(*adapter.ImageSegment)(nil),
		&adapter.ReplySegment{ReplySeq: 42},
		nil,
	}
	if got, want := AdapterMsgToString(elements), "正文[回复:42]"; got != want {
		t.Fatalf("AdapterMsgToString() = %q, want %q", got, want)
	}
}
