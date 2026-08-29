package twitter

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestNormalizeGraphQLUserID(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("User:12345"))
	if got := normalizeGraphQLUserID(encoded); got != "12345" {
		t.Fatalf("normalizeGraphQLUserID(%q) = %q, want %q", encoded, got, "12345")
	}
	if got := normalizeGraphQLUserID("67890"); got != "67890" {
		t.Fatalf("normalizeGraphQLUserID(raw) = %q, want %q", got, "67890")
	}
}

func TestNormalizeAPIFetchMode(t *testing.T) {
	tests := map[string]string{
		"":              APIFetchModeHomeTimeline,
		"home":          APIFetchModeHomeTimeline,
		"home-timeline": APIFetchModeHomeTimeline,
		"per_user":      APIFetchModePerUser,
		"user-tweets":   APIFetchModePerUser,
		"invalid":       APIFetchModeHomeTimeline,
	}
	for input, want := range tests {
		if got := normalizeAPIFetchMode(input); got != want {
			t.Errorf("normalizeAPIFetchMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAPIFetchModeNeedsFollow(t *testing.T) {
	oldMode := TwitterAPIFetchMode
	defer func() { TwitterAPIFetchMode = oldMode }()

	TwitterAPIFetchMode = APIFetchModeHomeTimeline
	if !apiFetchModeNeedsFollow() {
		t.Fatal("home timeline mode should require following")
	}
	TwitterAPIFetchMode = APIFetchModePerUser
	if apiFetchModeNeedsFollow() {
		t.Fatal("per-user mode should not require following")
	}
}

func TestQueryIDsReady(t *testing.T) {
	oldMode := TwitterAPIFetchMode
	defer func() { TwitterAPIFetchMode = oldMode }()

	cache := &QueryIdCache{Operations: map[string]string{"HomeLatestTimeline": "home"}}
	TwitterAPIFetchMode = APIFetchModeHomeTimeline
	if !queryIDsReady(cache) {
		t.Fatal("home timeline cache should be ready")
	}
	TwitterAPIFetchMode = APIFetchModePerUser
	if queryIDsReady(cache) {
		t.Fatal("per-user cache without UserTweets should not be ready")
	}
	cache.Operations[userTweetsOperation] = "user"
	if !queryIDsReady(cache) {
		t.Fatal("per-user cache with UserTweets should be ready")
	}
}

func TestExtractQueryIDs(t *testing.T) {
	content := `moduleA={queryId:"home-query-id-123456789012345",operationName:"HomeLatestTimeline"};moduleB={queryId:"user-query-id-123456789012345",operationName:"UserTweets"}`
	got := extractQueryIDs(content)
	if got["HomeLatestTimeline"] != "home-query-id-123456789012345" {
		t.Fatalf("HomeLatestTimeline query id = %q", got["HomeLatestTimeline"])
	}
	if got[userTweetsOperation] != "user-query-id-123456789012345" {
		t.Fatalf("UserTweets query id = %q", got[userTweetsOperation])
	}
}

func TestUserTweetsResponseParsing(t *testing.T) {
	var decoded UserTweetsResponse
	if err := json.Unmarshal([]byte(`{"data":{"user":{"result":{"__typename":"User","timeline":{"timeline":{"instructions":[]}}}}}}`), &decoded); err != nil {
		t.Fatalf("unmarshal UserTweets response: %v", err)
	}
	if decoded.Data == nil || decoded.Data.User == nil || decoded.Data.User.Result == nil || decoded.Data.User.Result.Timeline == nil {
		t.Fatal("UserTweets response JSON shape is not decoded")
	}
	if decoded.Data.User.Result.timelineData() == nil {
		t.Fatal("UserTweets timeline response is not available")
	}

	response := UserTweetsResponse{
		Data: &UserTweetsData{
			User: &UserTweetsUser{
				Result: &UserTweetsResult{
					Typename: "User",
					Timeline: &UserTweetsTimelineData{
						Timeline: &TimelineURT{
							Instructions: []TimelineInstruction{{
								Entries: []TimelineEntry{{
									EntryID: "tweet-1",
									Content: EntryContent{ItemContent: &TimelineTweet{
										TweetResults: &TweetResults{Result: &TweetResult{
											RestID: "1",
											Core: &TweetCore{UserResults: &UserResults{Result: &UserResult{
												Core: &UserCoreInfo{ScreenName: "demo", Name: "Demo"},
											}}},
											Legacy: &TweetLegacy{IDStr: "1", FullText: "hello"},
										}},
									}},
								}},
							}},
						},
					},
				},
			},
		},
	}
	api := &TwitterAPI{}
	result, err := api.parseTimelineResponse(response.Data.User.Result.Timeline.Timeline)
	if err != nil {
		t.Fatalf("parse UserTweets timeline: %v", err)
	}
	if len(result.Tweets) != 1 || result.Tweets[0].ID != "1" || result.Tweets[0].OrgUser.ScreenName != "demo" {
		t.Fatalf("unexpected parsed tweets: %+v", result.Tweets)
	}
}

func TestUserTweetsResponseParsingTimelineV2(t *testing.T) {
	var decoded UserTweetsResponse
	if err := json.Unmarshal([]byte(`{"data":{"user":{"result":{"__typename":"User","timeline_v2":{"timeline":{"instructions":[]}}}}}}`), &decoded); err != nil {
		t.Fatalf("unmarshal UserTweets timeline_v2 response: %v", err)
	}
	if decoded.Data == nil || decoded.Data.User == nil || decoded.Data.User.Result == nil {
		t.Fatal("UserTweets timeline_v2 response JSON shape is not decoded")
	}
	if decoded.Data.User.Result.timelineData() == nil {
		t.Fatal("UserTweets timeline_v2 response is not available")
	}
}
