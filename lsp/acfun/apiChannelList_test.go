package acfun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApiChannelList(t *testing.T) {
	useHTTPFixture(t, APath(PathApiChannelList)+"?count=100&pcursor=next", `{"channelListData":{"result":0,"count":1,"pcursor":"end","liveList":[{"authorId":1,"title":"test live","user":{"id":"1","name":"admin"}}]}}`)
	resp, err := ApiChannelList(100, "next")
	require.NoError(t, err)
	data := resp.GetChannelListData()
	require.NotNil(t, data)
	assert.EqualValues(t, 1, data.GetCount())
	assert.Equal(t, "end", data.GetPcursor())
	require.Len(t, data.GetLiveList(), 1)
	assert.Equal(t, "admin", data.GetLiveList()[0].GetUser().GetName())
	assert.Equal(t, "test live", data.GetLiveList()[0].GetTitle())
}
