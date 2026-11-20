package huya

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRoom = "s"

type concernFixtureTransport struct{}

func (concernFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.String() != huyaProfileRoomApi+testRoom {
		return nil, fmt.Errorf("unexpected request in concern test: %s %s", req.Method, req.URL)
	}
	const body = `{"status":200,"data":{"liveStatus":"OFF","profileInfo":{"nick":"test host"},"liveData":{"roomName":"test room"}}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestConcern(t *testing.T) {
	// Exercise subscription and notification behavior without depending on a
	// public room alias or allowing API fallback to reach the network.
	oldTransport, oldLevel := http.DefaultTransport, logrus.GetLevel()
	http.DefaultTransport = concernFixtureTransport{}
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		logrus.SetLevel(oldLevel)
	})
	logrus.SetLevel(logrus.TraceLevel)
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	testEventChan := make(chan concern.Event, 16)
	testNotifyChan := make(chan concern.Notify)

	c := NewConcern(testNotifyChan)

	assert.NotNil(t, c.GetStateManager())

	_, err := c.ParseId(testRoom)
	assert.Nil(t, err)

	c.StateManager.UseNotifyGeneratorFunc(c.notifyGenerator())
	c.StateManager.UseFreshFunc(func(ctx context.Context, eventChan chan<- concern.Event) {
		for {
			select {
			case e := <-testEventChan:
				if e != nil {
					eventChan <- e
				}
			case <-ctx.Done():
				return
			}
		}
	})

	assert.Nil(t, c.StateManager.Start())
	defer c.Stop()
	defer close(testEventChan)

	_, err = c.Add(nil, test.G1, testRoom, Live)
	require.NoError(t, err)

	liveInfo2, err := c.FindOrLoadRoom(testRoom)
	require.NoError(t, err)
	require.NotNil(t, liveInfo2)

	liveInfo2.liveStatusChanged = true
	liveInfo2.IsLiving = true

	testEventChan <- liveInfo2

	select {
	case notify := <-testNotifyChan:
		assert.Equal(t, test.G1, notify.GetGroupCode())
		assert.Equal(t, testRoom, notify.GetUid())
	case <-time.After(time.Second):
		assert.Fail(t, "no notify received")
	}

	_, err = c.Remove(nil, test.G1, testRoom, Live)
	assert.Nil(t, err)
}
