package utils

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/stretchr/testify/assert"
)

func TestSerializationAdapterGroupMsg(t *testing.T) {
	msg := &adapter.GroupMessage{
		ID:        1,
		GroupCode: test.G1,
		Sender: &adapter.SenderInfo{
			UserID: test.ID1,
			Uin:    test.ID1,
		},
		Time: 30,
		Elements: []adapter.IMessageElement{
			&adapter.TextSegment{Content: "qwe"},
			&adapter.TextSegment{Content: "asd"},
			&adapter.ImageSegment{File: "1231we"},
			&adapter.ImageSegment{File: "qwe"},
		},
	}

	msgString, err := SerializationAdapterGroupMsg(msg)
	assert.Nil(t, err)
	msg2, err := DeserializationAdapterGroupMsg(msgString)
	assert.Nil(t, err)
	assert.EqualValues(t, msg, msg2)
}

func TestAdapterMessageFilter(t *testing.T) {
	var e = []adapter.IMessageElement{
		&adapter.TextSegment{Content: "asd"},
		&adapter.ImageSegment{},
		&adapter.AtSegment{},
	}
	c := AdapterMessageFilter(e, func(element adapter.IMessageElement) bool {
		return element.Type() == adapter.ElementTypeText
	})
	assert.Len(t, c, 1)

	c = AdapterMessageFilter(e, func(element adapter.IMessageElement) bool {
		return element.Type() == adapter.ElementTypeText || element.Type() == adapter.ElementTypeImage
	})
	assert.Len(t, c, 2)

	c = AdapterMessageFilter(e, func(element adapter.IMessageElement) bool {
		return element.Type() == adapter.ElementTypeAt
	})
	assert.Len(t, c, 1)
}

func TestUploadGroupImage(t *testing.T) {
	test.InitMirai()
	defer test.CloseMirai()
	e, err := UploadGroupImage(test.G1, []byte("asdsad"), true)
	assert.NotNil(t, err)
	assert.Nil(t, e)
	e, err = UploadGroupImageByUrl(test.G1, test.FakeImage(10), true)
	assert.NotNil(t, err)
}

func TestUploadPrivateImage(t *testing.T) {
	t.Skip("Skipped in adapter mode - requires bot connection")
}
