package xhs

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gzipJSONPayload(t *testing.T, payload string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write([]byte(payload)); err != nil {
		t.Fatalf("write gzip payload: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return compressed.Bytes()
}

func newTestClient(server *httptest.Server) *Client {
	client := NewClient(map[string]string{"a1": "test-a1", "web_session": "session"})
	client.baseURL = server.URL
	client.liveRoomBaseURL = server.URL
	client.webBaseURL = server.URL
	client.SetTransport(server.Client().Transport.(*http.Transport))
	return client
}

func TestSearchUserOneboxDecodesGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != SearchOneboxAPI {
			t.Fatalf("path = %s, want %s", r.URL.Path, SearchOneboxAPI)
		}
		if r.Header.Get("x-s") == "" || r.Header.Get("x-s-common") == "" || r.Header.Get("x-t") == "" {
			t.Fatalf("missing signature headers")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if payload["keyword"] != "target-user" {
			t.Fatalf("keyword = %q, want %q", payload["keyword"], "target-user")
		}
		if payload["biz_type"] != "web_search_user" {
			t.Fatalf("biz_type = %q, want %q", payload["biz_type"], "web_search_user")
		}
		if payload["search_id"] == "" || payload["request_id"] == "" {
			t.Fatalf("search_id/request_id should be non-empty")
		}

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gzipJSONPayload(t, `{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"nick","image":"avatar","link":"profile","live_info":{"room_id":"r1","user_id":"u1","status":2,"link":"live-link","start_time":1}}}]}}`))
	}))
	defer server.Close()

	resp, err := newTestClient(server).SearchUserOnebox("target-user")
	if err != nil {
		t.Fatalf("SearchUserOnebox() error = %v", err)
	}
	if !resp.Success {
		t.Fatalf("SearchUserOnebox() success = false")
	}
	if resp.Data == nil || len(resp.Data.OneboxList) != 1 {
		t.Fatalf("SearchUserOnebox() missing onebox list")
	}
	if resp.Data.OneboxList[0].UserOneBox.LiveInfo.RoomID != "r1" {
		t.Fatalf("room id = %q, want %q", resp.Data.OneboxList[0].UserOneBox.LiveInfo.RoomID, "r1")
	}
}

func TestFindExactUserPrefersExactUIDMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"other","title":"65f41f45000000000500f2c2"}},{"type":"user","user_one_box":{"id":"65f41f45000000000500f2c2","title":"nick"}}]}}`))
	}))
	defer server.Close()

	user, err := newTestClient(server).FindExactUser("65f41f45000000000500f2c2")
	if err != nil {
		t.Fatalf("FindExactUser() error = %v", err)
	}
	if user.ID != "65f41f45000000000500f2c2" {
		t.Fatalf("id = %q, want exact uid match", user.ID)
	}
}

func TestFindExactUserMatchesExactNickname(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"target-nick"}}]}}`))
	}))
	defer server.Close()

	user, err := newTestClient(server).FindExactUser("target-nick")
	if err != nil {
		t.Fatalf("FindExactUser() error = %v", err)
	}
	if user.Title != "target-nick" {
		t.Fatalf("title = %q, want %q", user.Title, "target-nick")
	}
}

func TestFindExactUserMatchesExactRedID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","red_id":"632666029","title":"target-nick"}}]}}`))
	}))
	defer server.Close()

	user, err := newTestClient(server).FindExactUser("632666029")
	if err != nil {
		t.Fatalf("FindExactUser() error = %v", err)
	}
	if user.RedID != "632666029" {
		t.Fatalf("red_id = %q, want %q", user.RedID, "632666029")
	}
	if user.ID != "u1" {
		t.Fatalf("id = %q, want %q", user.ID, "u1")
	}
}

func TestFindExactUserReturnsAmbiguousForDuplicateNickname(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","title":"dup"}},{"type":"user","user_one_box":{"id":"u2","title":"dup"}}]}}`))
	}))
	defer server.Close()

	_, err := newTestClient(server).FindExactUser("dup")
	if err == nil || !strings.Contains(err.Error(), "multiple exact nickname matches") {
		t.Fatalf("FindExactUser() error = %v, want ambiguous nickname error", err)
	}
}

func TestFindExactUserReturnsAmbiguousForDuplicateRedID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[{"type":"user","user_one_box":{"id":"u1","red_id":"632666029","title":"dup-1"}},{"type":"user","user_one_box":{"id":"u2","red_id":"632666029","title":"dup-2"}}]}}`))
	}))
	defer server.Close()

	_, err := newTestClient(server).FindExactUser("632666029")
	if err == nil || !strings.Contains(err.Error(), "multiple exact red_id matches") {
		t.Fatalf("FindExactUser() error = %v, want ambiguous red_id error", err)
	}
}

func TestFindExactUserReturnsNotFoundForEmptyResults(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty data object", body: `{"success":true,"msg":"ok","code":0,"data":{}}`},
		{name: "empty onebox list", body: `{"success":true,"msg":"ok","code":0,"data":{"onebox_list":[]}}`},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()

			_, err := newTestClient(server).FindExactUser("missing")
			if err == nil || !strings.Contains(err.Error(), "user not found") {
				t.Fatalf("FindExactUser() error = %v, want not found", err)
			}
		})
	}
}

func TestGetCurrentRoomInfoDecodesGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != CurrentRoomInfoAPI {
			t.Fatalf("path = %s, want %s", r.URL.Path, CurrentRoomInfoAPI)
		}
		if got := r.URL.RawQuery; got != "room_id=r1&request_user_id=u1&source=web_live&client_type=1" {
			t.Fatalf("query = %q, want fixed ordered query", got)
		}
		if r.Header.Get("x-s") == "" || r.Header.Get("x-s-common") == "" || r.Header.Get("x-t") == "" {
			t.Fatalf("missing signature headers")
		}

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gzipJSONPayload(t, `{"success":true,"msg":"ok","code":0,"data":{"host_info":{"user_id":"u1","avatar":"face","nick_name":"host"},"room_info":{"room_id":"r1","room_title":"room-title","room_cover":"cover","deeplink":"deeplink","status":2}}}`))
	}))
	defer server.Close()

	resp, err := newTestClient(server).GetCurrentRoomInfo("r1", "u1")
	if err != nil {
		t.Fatalf("GetCurrentRoomInfo() error = %v", err)
	}
	if !resp.Success {
		t.Fatalf("GetCurrentRoomInfo() success = false")
	}
	if resp.Data == nil || resp.Data.RoomInfo == nil || resp.Data.HostInfo == nil {
		t.Fatalf("GetCurrentRoomInfo() missing data")
	}
	if resp.Data.RoomInfo.RoomTitle != "room-title" {
		t.Fatalf("room title = %q, want %q", resp.Data.RoomInfo.RoomTitle, "room-title")
	}
}

func TestGetFeedNoteDecodesGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != FeedAPI {
			t.Fatalf("path = %s, want %s", r.URL.Path, FeedAPI)
		}
		if r.Header.Get("x-s") == "" || r.Header.Get("x-s-common") == "" || r.Header.Get("x-t") == "" {
			t.Fatalf("missing signature headers")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload struct {
			SourceNoteID string            `json:"source_note_id"`
			ImageFormats []string          `json:"image_formats"`
			Extra        map[string]string `json:"extra"`
			XsecSource   string            `json:"xsec_source"`
			XsecToken    string            `json:"xsec_token"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if payload.SourceNoteID != "note-1" {
			t.Fatalf("source_note_id = %q, want note-1", payload.SourceNoteID)
		}
		if payload.XsecToken != "token-1" || payload.XsecSource != "pc_user" {
			t.Fatalf("unexpected xsec fields: %+v", payload)
		}
		if len(payload.ImageFormats) != 3 || payload.Extra["need_body_topic"] != "1" {
			t.Fatalf("unexpected payload: %+v", payload)
		}

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(gzipJSONPayload(t, `{"success":true,"msg":"ok","code":0,"data":{"items":[{"id":"note-1","model_type":"note","note_card":{"note_id":"note-1","type":"video","title":"detail-title","desc":"detail-desc","time":123,"user":{"user_id":"u1","nickname":"detail-name","avatar":"http://example.com/avatar.jpg","xsec_token":"detail-token"},"image_list":[{"url_default":"http://example.com/detail-cover.jpg"}]}}]}}`))
	}))
	defer server.Close()

	resp, err := newTestClient(server).GetFeedNote("note-1", "token-1", "pc_user")
	if err != nil {
		t.Fatalf("GetFeedNote() error = %v", err)
	}
	if !resp.Success || resp.Data == nil || len(resp.Data.Items) != 1 || resp.Data.Items[0].NoteCard == nil {
		t.Fatalf("GetFeedNote() missing data: %+v", resp)
	}
	if resp.Data.Items[0].NoteCard.Title != "detail-title" {
		t.Fatalf("title = %q, want detail-title", resp.Data.Items[0].NoteCard.Title)
	}
}

func TestGetUserProfileDecodesGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/user/profile/u1" {
			t.Fatalf("path = %s, want /user/profile/u1", r.URL.Path)
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(gzipJSONPayload(t, `<html><body><section class="note-item"><a class="cover mask" href="/user/profile/u1/note-1?xsec_token=t1&amp;xsec_source=pc_user"><img src="http://example.com/cover.jpg"><span class="play-icon"></span></a></section><script>window.__INITIAL_STATE__={"global":{"pwaAddDesktopPrompt":undefined,"flags":[undefined]},"user":{"userPageData":{"basicInfo":{"nickname":"target-nick","images":"http://example.com/avatar.jpg"}},"notes":[[{"id":"note-1","noteCard":{"noteId":"note-1","xsecToken":"token-1","type":"normal","displayTitle":"note-title","user":{"userId":"u1","nickname":"target-nick","avatar":"http://example.com/avatar.jpg"},"cover":{"urlDefault":"http://example.com/cover.jpg"}}}]]}}</script></body></html>`))
	}))
	defer server.Close()

	profile, err := newTestClient(server).GetUserProfile("u1")
	if err != nil {
		t.Fatalf("GetUserProfile() error = %v", err)
	}
	if profile.UserPageData == nil || profile.UserPageData.BasicInfo == nil {
		t.Fatalf("GetUserProfile() missing basic info")
	}
	if profile.UserPageData.BasicInfo.Nickname != "target-nick" {
		t.Fatalf("nickname = %q, want %q", profile.UserPageData.BasicInfo.Nickname, "target-nick")
	}
	notes := profile.FlattenNotes()
	if len(notes) != 1 {
		t.Fatalf("len(notes) = %d, want 1", len(notes))
	}
	if notes[0].NoteCard == nil || notes[0].NoteCard.NoteID != "note-1" {
		t.Fatalf("note id = %+v, want note-1", notes[0].NoteCard)
	}
	if notes[0].NoteCard.Type != NoteTypeVideo {
		t.Fatalf("note type = %q, want %q", notes[0].NoteCard.Type, NoteTypeVideo)
	}
}

func TestGetUserProfileKeepsNormalTypeWithoutPlayIcon(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><section class="note-item"><a class="cover mask" href="/user/profile/u1/note-2?xsec_token=t2&amp;xsec_source=pc_user"><img src="http://example.com/cover-2.jpg"></a></section><script>window.__INITIAL_STATE__={"user":{"userPageData":{"basicInfo":{"nickname":"target-nick"}},"notes":[[{"id":"note-2","noteCard":{"noteId":"note-2","displayTitle":"note-title-2","user":{"userId":"u1"},"cover":{"urlDefault":"http://example.com/cover-2.jpg"}}}]]}}</script></body></html>`))
	}))
	defer server.Close()

	profile, err := newTestClient(server).GetUserProfile("u1")
	if err != nil {
		t.Fatalf("GetUserProfile() error = %v", err)
	}
	notes := profile.FlattenNotes()
	if len(notes) != 1 || notes[0].NoteCard == nil {
		t.Fatalf("GetUserProfile() notes = %+v, want one note card", notes)
	}
	if notes[0].NoteCard.Type != NoteTypeNormal {
		t.Fatalf("note type = %q, want %q", notes[0].NoteCard.Type, NoteTypeNormal)
	}
}

func TestGenerateRequestIDFormatsWithXT(t *testing.T) {
	requestID, err := generateRequestID(1779201051990)
	if err != nil {
		t.Fatalf("generateRequestID() error = %v", err)
	}
	parts := strings.Split(requestID, "-")
	if len(parts) != 2 {
		t.Fatalf("request id = %q, want prefix-suffix format", requestID)
	}
	if parts[1] != "1779201051990" {
		t.Fatalf("request id suffix = %q, want %q", parts[1], "1779201051990")
	}
	if len(parts[0]) < 10 {
		t.Fatalf("request id prefix = %q, want at least 10 digits", parts[0])
	}
	if _, err := fmt.Sscanf(parts[0], "%d", new(int64)); err != nil {
		t.Fatalf("request id prefix should be numeric: %v", err)
	}
}
