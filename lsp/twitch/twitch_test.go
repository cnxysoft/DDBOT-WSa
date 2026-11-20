package twitch

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/stretchr/testify/assert"
)

func initConcern(t *testing.T) *TwitchConcern {
	c := NewConcern(nil)
	assert.NotNil(t, c)
	c.StateManager.FreshIndex(test.G1, test.G2)
	return c
}

func TestNewConcern(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := initConcern(t)
	assert.NotNil(t, c.GetStateManager())
	c.Stop()
}

func TestParseId(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := initConcern(t)
	defer c.Stop()

	id, err := c.ParseId("testuser")
	assert.Nil(t, err)
	assert.EqualValues(t, "testuser", id)

	id, err = c.ParseId("")
	assert.NotNil(t, err)
}

func TestSite(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := initConcern(t)
	defer c.Stop()

	assert.EqualValues(t, "twitch", c.Site())
}

func TestTypes(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := initConcern(t)
	defer c.Stop()

	types := c.Types()
	assert.Len(t, types, 1)
	assert.Equal(t, Live, types[0])
}
