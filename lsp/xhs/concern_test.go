package xhs

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	miraiConfig "github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/buntdb"
)

func TestNormalizeLiveStatusKeepsPreviousKnownStateForStatusFive(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	got := c.normalizeLiveStatus(
		&SearchOneboxUser{LiveInfo: &SearchOneboxLiveInfo{Status: 5}},
		&LiveInfo{Status: LiveStatus_Living},
	)
	require.Equal(t, LiveStatus_Living, got)

	got = c.normalizeLiveStatus(
		&SearchOneboxUser{LiveInfo: &SearchOneboxLiveInfo{Status: 5}},
		&LiveInfo{Status: LiveStatus_NoLiving},
	)
	require.Equal(t, LiveStatus_NoLiving, got)
}

func TestNormalizeLiveStatusFallsBackWhenNoPreviousState(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	require.Equal(t, LiveStatus_Unknown, c.normalizeLiveStatus(nil, nil))
	require.Equal(t, LiveStatus_Unknown, c.normalizeLiveStatus(
		&SearchOneboxUser{LiveInfo: &SearchOneboxLiveInfo{Status: 5}},
		nil,
	))
	require.Equal(t, LiveStatus_Living, c.normalizeLiveStatus(
		&SearchOneboxUser{LiveInfo: &SearchOneboxLiveInfo{Status: 2}},
		nil,
	))
	require.Equal(t, LiveStatus_NoLiving, c.normalizeLiveStatus(
		&SearchOneboxUser{LiveInfo: &SearchOneboxLiveInfo{Status: 3}},
		nil,
	))
	require.Equal(t, LiveStatus_NoLiving, c.normalizeLiveStatus(
		&SearchOneboxUser{LiveInfo: &SearchOneboxLiveInfo{Status: 0}},
		nil,
	))
}

func TestNormalizeLiveStatusUsesOneboxZeroToDowngradeCachedLivingState(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	got := c.normalizeLiveStatus(
		&SearchOneboxUser{LiveInfo: &SearchOneboxLiveInfo{Status: 0}},
		&LiveInfo{Status: LiveStatus_Living},
	)
	require.Equal(t, LiveStatus_NoLiving, got)
}

func TestFreshLiveUsesCurrentRoomInfoForLivingDetails(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"onebox-name","image":"onebox-face","link":"onebox-link","live_info":{"room_id":"r1","user_id":"u1","status":2,"link":"onebox-live-link","start_time":1}}}]}}`))
		case CurrentRoomInfoAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"host_info":{"user_id":"u1","avatar":"room-face","nick_name":"room-host"},"room_info":{"room_id":"r1","room_title":"room-title","room_cover":"room-cover","deeplink":"room-link","status":2}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	liveInfo, err := c.freshLive("u1")
	require.NoError(t, err)
	require.Equal(t, LiveStatus_Living, liveInfo.Status)
	require.Equal(t, "room-title", liveInfo.LiveTitle)
	require.Equal(t, "room-cover", liveInfo.Cover)
	require.Equal(t, "https://www.xiaohongshu.com/livestream/r1", liveInfo.Url)
	require.Equal(t, "room-host", liveInfo.Name)
	require.Equal(t, "room-face", liveInfo.Face)
}

func TestFreshNewsCreatesBaselineThenEmitsNewNotes(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "old-title", "http://example.com/cover-1.jpg"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/profile/u1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(profileHTML))
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	savedNewsInfo, err := c.GetNewsInfo("u1")
	require.NoError(t, err)
	require.Equal(t, "note-1", savedNewsInfo.LatestNoteID)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-2", "token-2", "new-title", "http://example.com/cover-2.jpg"),
		testProfileNote("note-1", "token-1", "old-title", "http://example.com/cover-1.jpg"))

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "note-2", newsInfo.Notes[0].NoteID)
	require.Equal(t, "new-title", newsInfo.Notes[0].Title)
	require.Equal(t, "https://www.xiaohongshu.com/explore/note-2", newsInfo.Notes[0].Url)
}

func TestFreshNewsUsesUserIDForProfileFetchAndURLs(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNoteWithUserID("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg", "profile-u1"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/profile/profile-u1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(profileHTML))
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "lookup-u1",
		UserId:     "profile-u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/profile-u1",
	}))

	newsInfo, err := c.freshNews("lookup-u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNoteWithUserID("note-2", "token-2", "title-2", "http://example.com/cover-2.jpg", "profile-u1"),
		testProfileNoteWithUserID("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg", "profile-u1"))

	newsInfo, err = c.freshNews("lookup-u1")
	require.NoError(t, err)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "https://www.xiaohongshu.com/explore/note-2", newsInfo.Notes[0].Url)
}

func TestAddNewsInitializesBaselineBeforeFirstScheduledRefresh(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	publishedAt := time.Now().Unix() + 10
	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"target-nick","image":"onebox-face","link":"xhsdiscover://user/u1","live_info":{"room_id":"","user_id":"u1","status":3}}}]}}`))
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-1","model_type":"note","note_card":{"note_id":"note-1","type":"normal","title":"feed-title","desc":"feed-desc","time":%d,"user":{"user_id":"u1","nickname":"target-nick","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover.jpg"}]}}]}}`, publishedAt)))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	identity, err := c.Add(nil, 1001, "u1", NewsType)
	require.NoError(t, err)
	require.NotNil(t, identity)

	savedNewsInfo, err := c.GetNewsInfo("u1")
	require.NoError(t, err)
	require.Equal(t, "", savedNewsInfo.LatestNoteID)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "profile-title", "http://example.com/cover-1.jpg"))

	events, err := c.freshEventsForTypes(NewsType, "u1")
	require.NoError(t, err)
	require.Len(t, events, 1)

	newsInfo, ok := events[0].(*NewsInfo)
	require.True(t, ok)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "note-1", newsInfo.Notes[0].NoteID)
	require.Equal(t, "feed-title", newsInfo.Notes[0].Title)
	require.Equal(t, "https://www.xiaohongshu.com/explore/note-1", newsInfo.Notes[0].Url)
}

func TestAddResolvesRedIDToCanonicalUID(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"65f41f45000000000500f2c2","red_id":"632666029","title":"target-nick","image":"onebox-face","live_info":{"room_id":"","user_id":"65f41f45000000000500f2c2","status":3}}}]}}`))
		case "/user/profile/65f41f45000000000500f2c2":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg")))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	identity, err := c.Add(nil, 1001, "632666029", NewsType)
	require.NoError(t, err)
	require.NotNil(t, identity)
	require.Equal(t, "65f41f45000000000500f2c2", identity.GetUid())

	userInfo, err := c.GetUserInfo("65f41f45000000000500f2c2")
	require.NoError(t, err)
	require.Equal(t, "632666029", userInfo.RedID)

	ctype, err := c.StateManager.GetGroupConcern(1001, "65f41f45000000000500f2c2")
	require.NoError(t, err)
	require.True(t, ctype.ContainAny(NewsType))
}

func TestRemoveResolvesCachedRedIDWithoutLookup(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	lookupCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookupCalled = true
		t.Fatalf("unexpected remote lookup during remove: %s", r.URL.Path)
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.baseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "65f41f45000000000500f2c2",
		RedID:      "632666029",
		UserId:     "65f41f45000000000500f2c2",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/65f41f45000000000500f2c2",
	}))
	_, err := c.StateManager.AddGroupConcern(1001, "65f41f45000000000500f2c2", NewsType)
	require.NoError(t, err)

	identity, err := c.Remove(nil, 1001, "632666029", NewsType)
	require.NoError(t, err)
	require.NotNil(t, identity)
	require.Equal(t, "65f41f45000000000500f2c2", identity.GetUid())
	require.False(t, lookupCalled)
}

func TestResolveCanonicalUserInfoUsesCachedExactNickname(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		RedID:      "632666029",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))
	_, err := c.StateManager.AddGroupConcern(1001, "u1", NewsType)
	require.NoError(t, err)

	info, err := c.resolveCanonicalUserInfo("target-nick")
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "u1", info.Uid)
}

func TestResolveSubscribedIDUsesCachedRedIDInGroup(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		RedID:      "632666029",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))
	_, err := c.StateManager.AddGroupConcern(1001, "u1", LiveType)
	require.NoError(t, err)

	id, err := c.ResolveSubscribedID(1001, "632666029", LiveType)
	require.NoError(t, err)
	require.Equal(t, "u1", id)
}

func TestResolveSubscribedIDUsesCurrentGroupScope(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		RedID:      "632666029",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u2",
		RedID:      "632666030",
		UserId:     "u2",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u2",
	}))
	_, err := c.StateManager.AddGroupConcern(1001, "u1", LiveType)
	require.NoError(t, err)
	_, err = c.StateManager.AddGroupConcern(2002, "u2", LiveType)
	require.NoError(t, err)

	id, err := c.ResolveSubscribedID(1001, "target-nick", LiveType)
	require.NoError(t, err)
	require.Equal(t, "u1", id)
}

func TestResolveSubscribedIDReturnsAmbiguousForDuplicateNicknameInGroup(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u2",
		UserId:     "u2",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u2",
	}))
	_, err := c.StateManager.AddGroupConcern(1001, "u1", LiveType)
	require.NoError(t, err)
	_, err = c.StateManager.AddGroupConcern(1001, "u2", LiveType)
	require.NoError(t, err)

	_, err = c.ResolveSubscribedID(1001, "target-nick", LiveType)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple cached xhs users match nickname")
}

func TestResolveSubscribedIDFiltersByConcernType(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "live-u1",
		UserId:     "live-u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/live-u1",
	}))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "news-u1",
		UserId:     "news-u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/news-u1",
	}))
	_, err := c.StateManager.AddGroupConcern(1001, "live-u1", LiveType)
	require.NoError(t, err)
	_, err = c.StateManager.AddGroupConcern(1001, "news-u1", NewsType)
	require.NoError(t, err)

	liveID, err := c.ResolveSubscribedID(1001, "target-nick", LiveType)
	require.NoError(t, err)
	require.Equal(t, "live-u1", liveID)

	newsID, err := c.ResolveSubscribedID(1001, "target-nick", NewsType)
	require.NoError(t, err)
	require.Equal(t, "news-u1", newsID)
}

func TestFreshEventsForTypesHandlesCombinedLiveAndNewsSubscription(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	publishedAt := time.Now().Unix() + 10
	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "old-title", "http://example.com/cover-1.jpg"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"target-nick","image":"onebox-face","live_info":{"room_id":"r1","user_id":"u1","status":2}}}]}}`))
		case CurrentRoomInfoAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"host_info":{"user_id":"u1","avatar":"room-face","nick_name":"room-host"},"room_info":{"room_id":"r1","room_title":"room-title","room_cover":"room-cover","status":2}}}`))
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-2","model_type":"note","note_card":{"note_id":"note-2","type":"video","title":"feed-title","desc":"feed-desc","time":%d,"user":{"user_id":"u1","nickname":"feed-name","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover.jpg"}]}}]}}`, publishedAt)))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	events, err := c.freshEventsForTypes(LiveType.Add(NewsType), "u1")
	require.NoError(t, err)
	require.Len(t, events, 1)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-2", "token-2", "new-title", "http://example.com/cover-2.jpg"),
		testProfileNote("note-1", "token-1", "old-title", "http://example.com/cover-1.jpg"))

	events, err = c.freshEventsForTypes(LiveType.Add(NewsType), "u1")
	require.NoError(t, err)
	require.Len(t, events, 2)

	var gotNews *NewsInfo
	for _, event := range events {
		if news, ok := event.(*NewsInfo); ok {
			gotNews = news
			break
		}
	}
	require.NotNil(t, gotNews)
	require.Len(t, gotNews.Notes, 1)
	require.Equal(t, "note-2", gotNews.Notes[0].NoteID)
	require.Equal(t, "feed-title", gotNews.Notes[0].Title)
	require.Equal(t, "feed-desc", gotNews.Notes[0].Desc)
	require.Equal(t, []string{"https://example.com/feed-cover.jpg"}, gotNews.Notes[0].Pictures)
}

func TestFreshNewsEnrichesWithFeedDetailAndFallsBackWhenFeedFails(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	publishedAt := time.Now().Unix() + 10
	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "old-title", "http://example.com/cover-1.jpg"))
	feedStatus := http.StatusOK
	feedBody := fmt.Sprintf(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-2","model_type":"note","note_card":{"note_id":"note-2","type":"video","title":"feed-title","desc":"feed-desc","time":%d,"user":{"user_id":"u1","nickname":"feed-name","avatar":"http://example.com/feed-avatar.jpg","xsec_token":"feed-token"},"image_list":[{"url_default":"http://example.com/feed-cover.jpg"},{"url_default":"http://example.com/feed-cover-2.jpg"}]}}]}}`, publishedAt)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(feedStatus)
			_, _ = w.Write([]byte(feedBody))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-2", "token-2", "new-title", "http://example.com/cover-2.jpg"),
		testProfileNote("note-1", "token-1", "old-title", "http://example.com/cover-1.jpg"))

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "feed-title", newsInfo.Notes[0].Title)
	require.Equal(t, "feed-desc", newsInfo.Notes[0].Desc)
	require.Equal(t, NoteTypeVideo, newsInfo.Notes[0].NoteType)
	require.Equal(t, []string{"https://example.com/feed-cover.jpg", "https://example.com/feed-cover-2.jpg"}, newsInfo.Notes[0].Pictures)
	require.Equal(t, publishedAt, newsInfo.Notes[0].PublishedAt)

	feedStatus = http.StatusInternalServerError
	feedBody = `{"success":false,"msg":"boom","code":500}`
	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-3", "token-3", "fallback-title", "http://example.com/cover-3.jpg"),
		testProfileNote("note-2", "token-2", "new-title", "http://example.com/cover-2.jpg"))

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "note-3", newsInfo.Notes[0].NoteID)
	require.Equal(t, "fallback-title", newsInfo.Notes[0].Title)
	require.Empty(t, newsInfo.Notes[0].Desc)
	require.Equal(t, []string{"https://example.com/cover-3.jpg"}, newsInfo.Notes[0].Pictures)
}

func TestFreshNewsUsesCanonicalInnerNoteIDForBaselineAndDedupe(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNoteWithOuterID("outer-1", "inner-1", "token-1", "title-1", "http://example.com/cover-1.jpg"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/profile/u1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(profileHTML))
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	savedNewsInfo, err := c.GetNewsInfo("u1")
	require.NoError(t, err)
	require.Equal(t, "inner-1", savedNewsInfo.LatestNoteID)
}

func TestFreshNewsOnlyOnlineNotifySkipsPreStartAndPushesNewerNote(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	miraiConfig.GlobalConfig.Set("xhs.onlyOnlineNotify", true)
	defer miraiConfig.GlobalConfig.Set("xhs.onlyOnlineNotify", false)

	postStartPublishedAt := time.Now().Unix() + 10
	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg")
	feedBody := `{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-1","model_type":"note","note_card":{"note_id":"note-1","type":"normal","title":"feed-title-1","desc":"feed-desc-1","time":900,"user":{"user_id":"u1","nickname":"target-nick","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover-1.jpg"}]}}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(feedBody))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.cacheStartTs = 1000
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "profile-title-1", "http://example.com/cover-1.jpg"))

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	savedNewsInfo, err := c.GetNewsInfo("u1")
	require.NoError(t, err)
	require.Equal(t, []string{"note-1"}, savedNewsInfo.RecentNoteIDs)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-2", "token-2", "profile-title-2", "http://example.com/cover-2.jpg"),
		testProfileNote("note-1", "token-1", "profile-title-1", "http://example.com/cover-1.jpg"))
	feedBody = fmt.Sprintf(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-2","model_type":"note","note_card":{"note_id":"note-2","type":"normal","title":"feed-title-2","desc":"feed-desc-2","time":%d,"user":{"user_id":"u1","nickname":"target-nick","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover-2.jpg"}]}}]}}`, postStartPublishedAt)

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "note-2", newsInfo.Notes[0].NoteID)
}

func TestFreshNewsRecentNoteWindowPreventsRepeatAfterLatestDeletion(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-3", "token-3", "title-3", "http://example.com/cover-3.jpg"),
		testProfileNote("note-2", "token-2", "title-2", "http://example.com/cover-2.jpg"),
		testProfileNote("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/profile/u1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(profileHTML))
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	for _, noteID := range []string{"note-1", "note-2", "note-3"} {
		_, err = c.Delete(c.MarkNoteIdKey(noteID))
		require.NoError(t, err)
	}

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg"))

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	savedNewsInfo, err := c.GetNewsInfo("u1")
	require.NoError(t, err)
	require.Equal(t, []string{"note-1"}, savedNewsInfo.RecentNoteIDs)
}

func TestFreshNewsRetriesWhenFeedDetailIsTemporarilyUnavailable(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	publishedAt := time.Now().Unix() + 10
	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg")
	feedStatus := http.StatusInternalServerError
	feedBody := `{"success":false,"msg":"boom","code":500}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(feedStatus)
			_, _ = w.Write([]byte(feedBody))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "profile-title-1", "http://example.com/cover-1.jpg"))

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	savedNewsInfo, err := c.GetNewsInfo("u1")
	require.NoError(t, err)
	require.Empty(t, savedNewsInfo.RecentNoteIDs)

	feedStatus = http.StatusOK
	feedBody = fmt.Sprintf(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-1","model_type":"note","note_card":{"note_id":"note-1","type":"normal","title":"feed-title-1","desc":"feed-desc-1","time":%d,"user":{"user_id":"u1","nickname":"target-nick","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover-1.jpg"}]}}]}}`, publishedAt)

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "note-1", newsInfo.Notes[0].NoteID)
	require.Equal(t, publishedAt, newsInfo.Notes[0].PublishedAt)
}

func TestFreshNewsDetectsNewNoteWhenSeenNoteIsPinnedFirst(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	publishedAt := time.Now().Unix() + 10
	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-2", "token-2", "title-2", "http://example.com/cover-2.jpg"),
		testProfileNote("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-3","model_type":"note","note_card":{"note_id":"note-3","type":"normal","title":"feed-title-3","desc":"feed-desc-3","time":%d,"user":{"user_id":"u1","nickname":"target-nick","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover-3.jpg"}]}}]}}`, publishedAt)))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg"),
		testProfileNote("note-3", "token-3", "title-3", "http://example.com/cover-3.jpg"),
		testProfileNote("note-2", "token-2", "title-2", "http://example.com/cover-2.jpg"))

	newsInfo, err = c.freshNews("u1")
	require.NoError(t, err)
	require.Len(t, newsInfo.Notes, 1)
	require.Equal(t, "note-3", newsInfo.Notes[0].NoteID)
	require.Equal(t, publishedAt, newsInfo.Notes[0].PublishedAt)
}

func TestFreshNewsPinnedOldNoteDoesNotRepeat(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("old-note", "token-old", "old-title", "http://example.com/cover-old.jpg"),
		testProfileNote("seen-note", "token-seen", "seen-title", "http://example.com/cover-seen.jpg"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"old-note","model_type":"note","note_card":{"note_id":"old-note","type":"normal","title":"feed-old-title","desc":"feed-old-desc","time":900,"user":{"user_id":"u1","nickname":"target-nick","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover-old.jpg"}]}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	userInfo := &UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}
	require.NoError(t, c.AddUserInfo(userInfo))
	require.NoError(t, c.AddNewsInfo(&NewsInfo{
		UserInfo:          *userInfo,
		LatestNoteID:      "seen-note",
		RecentNoteIDs:     []string{"seen-note"},
		LatestPublishedAt: 1000,
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)
}

func TestFreshEventsForTypesStillEmitsNewsWhenLiveRefreshFails(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	publishedAt := time.Now().Unix() + 10
	profileHTML := buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"target-nick","image":"onebox-face","live_info":{"room_id":"r1","user_id":"u1","status":2}}}]}}`))
		case CurrentRoomInfoAPI:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"success":false,"msg":"boom","code":500}`))
		case "/user/profile/u1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(profileHTML))
		case FeedAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-2","model_type":"note","note_card":{"note_id":"note-2","type":"normal","title":"feed-title-2","desc":"feed-desc-2","time":%d,"user":{"user_id":"u1","nickname":"target-nick","avatar":"http://example.com/feed-avatar.jpg"},"image_list":[{"url_default":"http://example.com/feed-cover-2.jpg"}]}}]}}`, publishedAt)))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.webBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	newsInfo, err := c.freshNews("u1")
	require.NoError(t, err)
	require.Empty(t, newsInfo.Notes)

	profileHTML = buildTestProfileHTML("target-nick", "http://example.com/avatar.jpg",
		testProfileNote("note-2", "token-2", "title-2", "http://example.com/cover-2.jpg"),
		testProfileNote("note-1", "token-1", "title-1", "http://example.com/cover-1.jpg"))

	events, err := c.freshEventsForTypes(LiveType.Add(NewsType), "u1")
	require.NoError(t, err)
	require.Len(t, events, 1)

	gotNews, ok := events[0].(*NewsInfo)
	require.True(t, ok)
	require.Len(t, gotNews.Notes, 1)
	require.Equal(t, "note-2", gotNews.Notes[0].NoteID)
}

func TestAddLiveWithNilContextDoesNotPanicWhenAlreadyLiving(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"target-nick","image":"onebox-face","live_info":{"room_id":"r1","user_id":"u1","status":2}}}]}}`))
		case CurrentRoomInfoAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"host_info":{"user_id":"u1","avatar":"room-face","nick_name":"room-host"},"room_info":{"room_id":"r1","room_title":"room-title","room_cover":"room-cover","status":2}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	identity, err := c.Add(nil, 1001, "u1", LiveType)
	require.NoError(t, err)
	require.NotNil(t, identity)
}

func TestRemoveCanonicalIDWithoutSubscribedTypeDoesNotTriggerLookup(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	lookupCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookupCalled = true
		t.Fatalf("unexpected remote lookup during remove: %s", r.URL.Path)
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.baseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		UserId:     "u1",
		Name:       "target-nick",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))
	_, err := c.StateManager.AddGroupConcern(1001, "u1", NewsType)
	require.NoError(t, err)

	identity, err := c.Remove(nil, 1001, "u1", LiveType)
	require.ErrorIs(t, err, buntdb.ErrNotFound)
	require.NotNil(t, identity)
	require.False(t, lookupCalled)
}

func TestFreshLiveUsesResolvedUserIDForOfflineProfileURL(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"lookup-u1","title":"onebox-name","image":"onebox-face","live_info":{"room_id":"","user_id":"profile-u1","status":3}}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	liveInfo, err := c.freshLive("lookup-u1")
	require.NoError(t, err)
	require.Equal(t, "profile-u1", liveInfo.UserId)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/profile-u1", liveInfo.Url)

	userInfo, err := c.GetUserInfo("lookup-u1")
	require.NoError(t, err)
	require.Equal(t, "profile-u1", userInfo.UserId)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/profile-u1", userInfo.ProfileURL)
}

func TestFreshLiveFallsBackToProfileURLWhenRoomInfoDowngradesToOffline(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"onebox-name","image":"onebox-face","link":"onebox-link","live_info":{"room_id":"r1","user_id":"u1","status":2,"link":"onebox-live-link","start_time":1}}}]}}`))
		case CurrentRoomInfoAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"host_info":{"user_id":"u1","avatar":"room-face","nick_name":"room-host"},"room_info":{"room_id":"r1","room_title":"room-title","room_cover":"room-cover","deeplink":"room-link","status":3}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	liveInfo, err := c.freshLive("u1")
	require.NoError(t, err)
	require.Equal(t, LiveStatus_NoLiving, liveInfo.Status)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/u1", liveInfo.Url)
}

func TestFreshLiveUsesWebUserProfileURLWhenNotLiving(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"onebox-name","image":"onebox-face","link":"xhsdiscover://user/u1","live_info":{"room_id":"","user_id":"u1","status":3,"link":"xhsdiscover://live_audience?room_id=r1","start_time":1}}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	liveInfo, err := c.freshLive("u1")
	require.NoError(t, err)
	require.Equal(t, LiveStatus_NoLiving, liveInfo.Status)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/u1", liveInfo.Url)

	userInfo, err := c.GetUserInfo("u1")
	require.NoError(t, err)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/u1", userInfo.ProfileURL)
}

func TestFreshLiveOneboxZeroMarksOfflineAndClearsStaleRoomState(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"onebox-name","image":"onebox-face","link":"xhsdiscover://user/u1","live_info":{"room_id":"","user_id":"u1","status":0}}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		Name:       "cached-name",
		RoomId:     "stale-room",
		UserId:     "u1",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))
	require.NoError(t, c.AddLiveInfo(&LiveInfo{
		UserInfo: UserInfo{
			Uid:        "u1",
			Name:       "cached-name",
			RoomId:     "stale-room",
			UserId:     "u1",
			ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
		},
		Status:    LiveStatus_Living,
		LiveTitle: "live-title",
		Url:       "https://www.xiaohongshu.com/livestream/stale-room",
		Cover:     "cover",
	}))

	liveInfo, err := c.freshLive("u1")
	require.NoError(t, err)
	require.Equal(t, LiveStatus_NoLiving, liveInfo.Status)
	require.Equal(t, "", liveInfo.RoomId)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/u1", liveInfo.Url)

	userInfo, err := c.GetUserInfo("u1")
	require.NoError(t, err)
	require.Equal(t, "", userInfo.RoomId)
}

func TestFreshLiveRewritesCachedProfileURLToWebUserProfileURL(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"onebox-name","image":"onebox-face","link":"xhsdiscover://user/u1","live_info":{"room_id":"","user_id":"u1","status":3,"link":"xhsdiscover://live_audience?room_id=r1","start_time":1}}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		Name:       "cached-name",
		ProfileURL: "xhsdiscover://user/u1",
	}))

	liveInfo, err := c.freshLive("u1")
	require.NoError(t, err)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/u1", liveInfo.Url)

	userInfo, err := c.GetUserInfo("u1")
	require.NoError(t, err)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/u1", userInfo.ProfileURL)
}

func TestFreshLiveKeepsProfileURLWhenOfflineResponseStillHasRoomID(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"onebox-name","image":"onebox-face","link":"xhsdiscover://user/u1","live_info":{"room_id":"r1","user_id":"u1","status":3,"link":"xhsdiscover://live_audience?room_id=r1","start_time":1}}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))

	liveInfo, err := c.freshLive("u1")
	require.NoError(t, err)
	require.Equal(t, LiveStatus_NoLiving, liveInfo.Status)
	require.Equal(t, "r1", liveInfo.RoomId)
	require.Equal(t, "https://www.xiaohongshu.com/user/profile/u1", liveInfo.Url)
}

func TestFreshLiveClearsCachedRoomIDWhenOfflineResponseHasEmptyRoomID(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SearchOneboxAPI:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"onebox-name","image":"onebox-face","link":"xhsdiscover://user/u1","live_info":{"room_id":"","user_id":"u1","status":3,"link":"xhsdiscover://live_audience?room_id=r1","start_time":1}}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	c := NewConcern(nil)
	c.client.SetCookies(map[string]string{"a1": "test-a1", "web_session": "session"})
	c.client.baseURL = server.URL
	c.client.liveRoomBaseURL = server.URL
	c.client.SetTransport(server.Client().Transport.(*http.Transport))
	require.NoError(t, c.AddUserInfo(&UserInfo{
		Uid:        "u1",
		Name:       "cached-name",
		RoomId:     "stale-room",
		UserId:     "u1",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}))

	liveInfo, err := c.freshLive("u1")
	require.NoError(t, err)
	require.Equal(t, "", liveInfo.RoomId)

	userInfo, err := c.GetUserInfo("u1")
	require.NoError(t, err)
	require.Equal(t, "", userInfo.RoomId)
}

func TestRemoveKeepsSharedCacheUntilLastGroupUnsubscribes(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := NewConcern(nil)
	c.StateManager.FreshIndex(1001, 1002)
	userInfo := &UserInfo{
		Uid:        "u1",
		Name:       "target",
		RoomId:     "r1",
		UserId:     "u1",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/u1",
	}
	liveInfo := &LiveInfo{
		UserInfo:  *userInfo,
		Status:    LiveStatus_Living,
		LiveTitle: "live-title",
		Url:       "https://www.xiaohongshu.com/livestream/r1",
		Cover:     "cover",
	}
	require.NoError(t, c.AddUserInfo(userInfo))
	require.NoError(t, c.AddLiveInfo(liveInfo))
	_, err := c.StateManager.AddGroupConcern(1001, "u1", LiveType)
	require.NoError(t, err)
	_, err = c.StateManager.AddGroupConcern(1002, "u1", LiveType)
	require.NoError(t, err)

	_, err = c.Remove(nil, 1001, "u1", LiveType)
	require.NoError(t, err)

	gotUserInfo, err := c.GetUserInfo("u1")
	require.NoError(t, err)
	require.Equal(t, userInfo.ProfileURL, gotUserInfo.ProfileURL)
	gotLiveInfo, err := c.GetLiveInfo("u1")
	require.NoError(t, err)
	require.Equal(t, liveInfo.RoomId, gotLiveInfo.RoomId)

	_, err = c.Remove(nil, 1002, "u1", LiveType)
	require.NoError(t, err)

	_, err = c.GetUserInfo("u1")
	require.Error(t, err)
	_, err = c.GetLiveInfo("u1")
	require.Error(t, err)
}

func buildTestProfileHTML(name string, avatar string, notes ...string) string {
	return fmt.Sprintf(`<html><script>window.__INITIAL_STATE__ = {"global":{"pwaAddDesktopPrompt": undefined},"user":{"userPageData":{"basicInfo":{"nickname":%q,"images":%q}},"notes":[[%s]]}}</script></html>`,
		name, avatar, strings.Join(notes, ","))
}

func testProfileNote(noteID string, token string, title string, cover string) string {
	return fmt.Sprintf(`{"id":%q,"noteCard":{"noteId":%q,"xsecToken":%q,"type":"normal","displayTitle":%q,"user":{"userId":"u1","nickname":"target-nick","avatar":"http://example.com/avatar.jpg"},"cover":{"urlDefault":%q}},"xsecToken":%q}`,
		noteID, noteID, token, title, cover, token)
}

func testProfileNoteWithOuterID(outerID string, innerID string, token string, title string, cover string) string {
	return fmt.Sprintf(`{"id":%q,"noteCard":{"noteId":%q,"xsecToken":%q,"type":"normal","displayTitle":%q,"user":{"userId":"u1","nickname":"target-nick","avatar":"http://example.com/avatar.jpg"},"cover":{"urlDefault":%q}},"xsecToken":%q}`,
		outerID, innerID, token, title, cover, token)
}

func testProfileNoteWithUserID(noteID string, token string, title string, cover string, userID string) string {
	return fmt.Sprintf(`{"id":%q,"noteCard":{"noteId":%q,"xsecToken":%q,"type":"normal","displayTitle":%q,"user":{"userId":%q,"nickname":"target-nick","avatar":"http://example.com/avatar.jpg"},"cover":{"urlDefault":%q}},"xsecToken":%q}`,
		noteID, noteID, token, title, userID, cover, token)
}
