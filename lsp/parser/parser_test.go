package parser

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/stretchr/testify/assert"
)

func TestNewParser(t *testing.T) {
	p := NewParser()
	assert.NotNil(t, p)
	p.Command = "cmd"
	p.Args = []string{"1", "2", "3"}

	assert.Equal(t, "cmd", p.GetCmd())
	assert.EqualValues(t, []string{"1", "2", "3"}, p.GetArgs())
	assert.EqualValues(t, []string{"cmd", "1", "2", "3"}, p.GetCmdArgs())
}

func TestParser_Parse(t *testing.T) {
	defer utils.GetBot().TESTReset()
	p := NewParser()
	assert.NotNil(t, p)

	elems := []adapter.IMessageElement{&adapter.AtSegment{Target: 0}, &adapter.TextSegment{Content: " "}, &adapter.TextSegment{Content: "/a -b 1 -c 2"}}
	p.Parse(elems)

	assert.EqualValues(t, "/a", p.GetCmd())
	assert.EqualValues(t, []string{"-b", "1", "-c", "2"}, p.GetArgs())
	assert.EqualValues(t, []string{"/a", "-b", "1", "-c", "2"}, p.GetCmdArgs())
	assert.True(t, p.AtCheck())

	utils.GetBot().TESTSetUin(test.UID1)
	elems2 := []adapter.IMessageElement{&adapter.AtSegment{Target: test.UID2}, &adapter.TextSegment{Content: " "}, &adapter.TextSegment{Content: "/a -b 1 -c 2"}}
	p.Parse(elems2)

	assert.False(t, p.AtCheck())
}

func TestParser_Parse2(t *testing.T) {
	defer utils.GetBot().TESTReset()
	p := NewParser()
	assert.NotNil(t, p)

	rawElems := []adapter.IMessageElement{
		&adapter.TextSegment{Content: " "},
		&adapter.TextSegment{Content: "/a -b 1 -c 2"},
		&adapter.ImageSegment{},
		&adapter.TextSegment{Content: "-d 3"},
		&adapter.AtSegment{Target: test.UID1},
		&adapter.AtSegment{Target: test.UID2},
		&adapter.TextSegment{Content: "-e 4"},
	}
	p.Parse(rawElems)

	assert.EqualValues(t, "/a", p.GetCmd())
	assert.EqualValues(t, []string{"-b", "1", "-c", "2", "-d", "3", "-e", "4"}, p.GetArgs())
	assert.EqualValues(t, []string{"/a", "-b", "1", "-c", "2", "-d", "3", "-e", "4"}, p.GetCmdArgs())
	assert.EqualValues(t, []int64{test.UID1, test.UID2}, p.GetAtArgs())
}
