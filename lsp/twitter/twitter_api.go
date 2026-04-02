package twitter

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/andybalholm/brotli"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

const (
	TwitterGraphQLAPI = "https://x.com/i/api/graphql"
	TwitterHomeURL    = "https://x.com/home"

	DefaultHomeTimelineQueryId = "0vp2Au9doTKsbn2vIk48Dg"
	DefaultBearerToken         = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
)

// TwitterAPI handles official X.com API requests
type TwitterAPI struct {
	ct0         string
	authToken   string
	bearerToken string
	queryId     string
	screenName  string // Cookie账号的screenName，用于processHomeTimeline区分转发来源
}

// NewTwitterAPI creates a new TwitterAPI instance with user-provided cookies
// If ct0 or auth_token are not provided, returns nil (Twitter concern will be disabled)
func NewTwitterAPI(ct0, authToken, bearerToken, queryId, screenName string) *TwitterAPI {
	if ct0 == "" || authToken == "" {
		return nil
	}
	api := &TwitterAPI{
		ct0:         ct0,
		authToken:   authToken,
		bearerToken: bearerToken,
		queryId:     queryId,
		screenName:  screenName,
	}
	// Use defaults if not provided
	if api.bearerToken == "" {
		api.bearerToken = DefaultBearerToken
	}
	if api.queryId == "" {
		api.queryId = DefaultHomeTimelineQueryId
	}
	return api
}

// IsEnabled returns true if the API is properly configured with cookies
func (t *TwitterAPI) IsEnabled() bool {
	return t != nil && t.ct0 != "" && t.authToken != ""
}

// GetScreenName returns the Cookie account's screen name
func (t *TwitterAPI) GetScreenName() string {
	if t == nil {
		return ""
	}
	return t.screenName
}

func (t *TwitterAPI) UpdateQueryId(queryId string) {
	if queryId != "" {
		t.queryId = queryId
	}
}

// FetchCurrentUserScreenName fetches the current logged-in user's screenName from x.com
// It parses window.__INITIAL_STATE__ from the HTML to extract the user info
func (t *TwitterAPI) FetchCurrentUserScreenName() (string, error) {
	screenName, _, err := t.FetchInitialState()
	return screenName, err
}

// FetchInitialState fetches x.com/home once and extracts both screenName and main.js URL
func (t *TwitterAPI) FetchInitialState() (screenName, mainJsUrl string, err error) {
	if t == nil || !t.IsEnabled() {
		return "", "", errors.New("twitter API not configured")
	}

	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 30),
		requests.AddUAOption(UserAgent),
		requests.HeaderOption("x-csrf-token", t.ct0),
		requests.HeaderOption("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"),
		requests.HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br"),
		requests.CookieOption("ct0", t.ct0),
		requests.CookieOption("auth_token", t.authToken),
		requests.RetryOption(3),
	}

	var resp bytes.Buffer
	err = requests.Get(TwitterHomeURL, nil, &resp, opts...)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch x.com home: %w", err)
	}

	decompressed, err := decompressResponse(resp.Bytes())
	if err != nil {
		return "", "", fmt.Errorf("decompress failed: %w", err)
	}

	html := string(decompressed)

	// Extract main.js URL
	mainJsPattern := `https://abs\.twimg\.com/responsive-web/client-web/(main\.[a-z0-9]+\.js)`
	re := regexp.MustCompile(mainJsPattern)
	matches := re.FindStringSubmatch(html)
	if len(matches) >= 2 {
		mainJsUrl = "https://abs.twimg.com/responsive-web/client-web/" + matches[1]
	}

	// Find window.__INITIAL_STATE__
	const prefix = "window.__INITIAL_STATE__="
	startIdx := strings.Index(html, prefix)
	if startIdx == -1 {
		return "", mainJsUrl, errors.New("window.__INITIAL_STATE__ not found in response")
	}
	startIdx += len(prefix)

	endIdx := startIdx
	braceCount := 0
	inString := false
	escaped := false
	for i := startIdx; i < len(html); i++ {
		c := html[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			braceCount++
		} else if c == '}' {
			braceCount--
			if braceCount == 0 {
				endIdx = i + 1
				break
			}
		}
	}

	if endIdx <= startIdx {
		return "", mainJsUrl, errors.New("failed to parse window.__INITIAL_STATE__")
	}

	jsonStr := html[startIdx:endIdx]

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "", mainJsUrl, fmt.Errorf("failed to parse JSON: %w", err)
	}

	var userEntity map[string]interface{}
	if entities, ok := data["entities"].(map[string]interface{}); ok {
		if users, ok := entities["users"].(map[string]interface{}); ok {
			if userEntities, ok := users["entities"].(map[string]interface{}); ok {
				for _, v := range userEntities {
					if u, ok := v.(map[string]interface{}); ok {
						userEntity = u
						break
					}
				}
			}
		}
	}

	if userEntity == nil {
		return "", mainJsUrl, errors.New("user entity not found in __INITIAL_STATE__")
	}

	if v, exists := userEntity["screen_name"]; exists {
		if sn, ok := v.(string); ok && sn != "" {
			screenName = sn
		}
	}

	return screenName, mainJsUrl, nil
}

func (t *TwitterAPI) FetchQueryIdAndBearerFromMainJs(mainJsUrl string) (queryId, bearer string, err error) {
	if t == nil || !t.IsEnabled() {
		return "", "", errors.New("twitter API not configured")
	}

	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 60),
		requests.AddUAOption(UserAgent),
		requests.HeaderOption("Accept", "*/*"),
		requests.HeaderOption("Accept-Language", "en-US,en;q=0.9"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br"),
		requests.HeaderOption("Referer", TwitterHomeURL),
		requests.RetryOption(3),
	}

	var resp bytes.Buffer
	err = requests.Get(mainJsUrl, nil, &resp, opts...)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch main.js: %w", err)
	}

	decompressed, err := decompressResponse(resp.Bytes())
	if err != nil {
		return "", "", fmt.Errorf("decompress main.js failed: %w", err)
	}

	jsContent := string(decompressed)

	queryIdPattern := `queryId:"([a-zA-Z0-9_-]{20,})",operationName:"HomeLatestTimeline"`
	queryIdRe := regexp.MustCompile(queryIdPattern)
	queryIdMatches := queryIdRe.FindStringSubmatch(jsContent)
	if len(queryIdMatches) < 2 {
		return "", "", errors.New("queryId not found in main.js")
	}
	queryId = queryIdMatches[1]

	bearerPattern := `[Bb]earer\s+(AAAA%?[a-zA-Z0-9%]+)`
	bearerRe := regexp.MustCompile(bearerPattern)
	bearerMatches := bearerRe.FindStringSubmatch(jsContent)
	if len(bearerMatches) < 2 {
		return "", "", errors.New("bearerToken not found in main.js")
	}
	bearer = bearerMatches[1]

	return queryId, bearer, nil
}

func RefreshAPIFromMainJS() error {
	if TwitterMode != ModeAPI || twitterAPI == nil {
		return nil
	}

	_, mainJsUrl, err := twitterAPI.FetchInitialState()
	if err != nil {
		return fmt.Errorf("failed to fetch main.js URL: %w", err)
	}
	if mainJsUrl == "" {
		return errors.New("main.js URL not found")
	}

	return refreshAPIWithMainJsUrl(mainJsUrl)
}

func RefreshAPIFromMainJSWithUrl(mainJsUrl string) error {
	if TwitterMode != ModeAPI || twitterAPI == nil {
		return nil
	}
	if mainJsUrl == "" {
		return errors.New("main.js URL is empty")
	}
	return refreshAPIWithMainJsUrl(mainJsUrl)
}

func refreshAPIWithMainJsUrl(mainJsUrl string) error {
	queryId, bearer, err := twitterAPI.FetchQueryIdAndBearerFromMainJs(mainJsUrl)
	if err != nil {
		return fmt.Errorf("failed to extract queryId/bearer from main.js: %w", err)
	}

	if queryId != "" {
		twitterAPI.queryId = queryId
		logger.Infof("Updated queryId from main.js: %s", queryId)
	}
	if bearer != "" {
		twitterAPI.bearerToken = bearer
		logger.Infof("Updated bearer token from main.js")
	}

	return nil
}

func RefreshTwitterAPIFromConfig() {
	if TwitterMode != ModeAPI {
		return
	}
	ct0 := config.GlobalConfig.GetString("twitter.ct0")
	authToken := config.GlobalConfig.GetString("twitter.auth_token")
	bearerToken := config.GlobalConfig.GetString("twitter.bearerToken")
	queryId := config.GlobalConfig.GetString("twitter.queryId")
	screenName := config.GlobalConfig.GetString("twitter.screenName")

	if twitterAPI != nil {
		twitterAPI.ct0 = ct0
		twitterAPI.authToken = authToken
		if bearerToken != "" {
			twitterAPI.bearerToken = bearerToken
		}
		if queryId != "" {
			twitterAPI.queryId = queryId
		}
		if screenName != "" {
			twitterAPI.screenName = screenName
		}
	} else {
		twitterAPI = NewTwitterAPI(ct0, authToken, bearerToken, queryId, screenName)
	}
}

// AutoFetchScreenName automatically fetches and sets the screenName from x.com
// This should be called after TwitterAPI is initialized with valid cookies
func AutoFetchScreenName() error {
	if twitterAPI == nil || !twitterAPI.IsEnabled() {
		return errors.New("twitter API not configured")
	}

	screenName, err := twitterAPI.FetchCurrentUserScreenName()
	if err != nil {
		return fmt.Errorf("failed to fetch screenName: %w", err)
	}

	twitterAPI.screenName = screenName
	logger.Infof("Auto-fetched Cookie account screenName: %s", screenName)
	return nil
}

// HomeTimelineRequest represents the GraphQL request for HomeLatestTimeline
type HomeTimelineRequest struct {
	Variables HomeTimelineVariables `json:"variables"`
	Features  HomeTimelineFeatures  `json:"features"`
	QueryID   string                `json:"queryId"`
}

type HomeTimelineVariables struct {
	Count                  int      `json:"count"`
	EnableRanking          bool     `json:"enableRanking"`
	IncludePromotedContent bool     `json:"includePromotedContent"`
	RequestContext         string   `json:"requestContext"`
	SeenTweetIDs           []string `json:"seenTweetIds"`
	Cursor                 string   `json:"cursor,omitempty"`
}

type HomeTimelineFeatures struct {
	RwebVideoScreenEnabled                                         bool `json:"rweb_video_screen_enabled"`
	ProfileLabelImprovementsPcfLabelInPostEnabled                  bool `json:"profile_label_improvements_pcf_label_in_post_enabled"`
	ResponsiveWebProfileRedirectEnabled                            bool `json:"responsive_web_profile_redirect_enabled"`
	RwebTipjarConsumptionEnabled                                   bool `json:"rweb_tipjar_consumption_enabled"`
	VerifiedPhoneLabelEnabled                                      bool `json:"verified_phone_label_enabled"`
	CreatorSubscriptionsTweetPreviewAPIEnabled                     bool `json:"creator_subscriptions_tweet_preview_api_enabled"`
	ResponsiveWebGraphqlTimelineNavigationEnabled                  bool `json:"responsive_web_graphql_timeline_navigation_enabled"`
	ResponsiveWebGraphqlSkipUserProfileImageExtensionsEnabled      bool `json:"responsive_web_graphql_skip_user_profile_image_extensions_enabled"`
	PremiumContentAPIReadEnabled                                   bool `json:"premium_content_api_read_enabled"`
	CommunitiesWebEnableTweetCommunityResultsFetch                 bool `json:"communities_web_enable_tweet_community_results_fetch"`
	C9sTweetAnatomyModeratorBadgeEnabled                           bool `json:"c9s_tweet_anatomy_moderator_badge_enabled"`
	ResponsiveWebGrokAnalyzeButtonFetchTrendsEnabled               bool `json:"responsive_web_grok_analyze_button_fetch_trends_enabled"`
	ResponsiveWebGrokAnalyzePostFollowupsEnabled                   bool `json:"responsive_web_grok_analyze_post_followups_enabled"`
	ResponsiveWebJetfuelFrame                                      bool `json:"responsive_web_jetfuel_frame"`
	ResponsiveWebGrokShareAttachmentEnabled                        bool `json:"responsive_web_grok_share_attachment_enabled"`
	ResponsiveWebGrokAnnotationsEnabled                            bool `json:"responsive_web_grok_annotations_enabled"`
	ArticlesPreviewEnabled                                         bool `json:"articles_preview_enabled"`
	ResponsiveWebEditTweetAPIEnabled                               bool `json:"responsive_web_edit_tweet_api_enabled"`
	GraphqlIsTranslatableRwebTweetIsTranslatableEnabled            bool `json:"graphql_is_translatable_rweb_tweet_is_translatable_enabled"`
	ViewCountsEverywhereAPIEnabled                                 bool `json:"view_counts_everywhere_api_enabled"`
	LongformNotetweetsConsumptionEnabled                           bool `json:"longform_notetweets_consumption_enabled"`
	ResponsiveWebTwitterArticleTweetConsumptionEnabled             bool `json:"responsive_web_twitter_article_tweet_consumption_enabled"`
	TweetAwardsWebTippingEnabled                                   bool `json:"tweet_awards_web_tipping_enabled"`
	ContentDisclosureIndicatorEnabled                              bool `json:"content_discover_indicator_enabled"`
	ContentDisclosureAIGeneratedIndicatorEnabled                   bool `json:"content_disclosure_ai_generated_indicator_enabled"`
	ResponsiveWebGrokShowGrokTranslatedPost                        bool `json:"responsive_web_grok_show_grok_translated_post"`
	ResponsiveWebGrokAnalysisButtonFromBackend                     bool `json:"responsive_web_grok_analysis_button_from_backend"`
	PostCtasFetchEnabled                                           bool `json:"post_ctas_fetch_enabled"`
	FreedomOfSpeechNotReachFetchEnabled                            bool `json:"freedom_of_speech_not_reach_fetch_enabled"`
	StandardizedNudgesMisinfo                                      bool `json:"standardized_nudges_misinfo"`
	TweetWithVisibilityResultsPreferGqlLimitedActionsPolicyEnabled bool `json:"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled"`
	LongformNotetweetsRichTextReadEnabled                          bool `json:"longform_notetweets_rich_text_read_enabled"`
	LongformNotetweetsInlineMediaEnabled                           bool `json:"longform_notetweets_inline_media_enabled"`
	ResponsiveWebGrokImageAnnotationEnabled                        bool `json:"responsive_web_grok_image_annotation_enabled"`
	ResponsiveWebGrokImagineAnnotationEnabled                      bool `json:"responsive_web_grok_imagine_annotation_enabled"`
	ResponsiveWebGrokCommunityNoteAutoTranslationIsEnabled         bool `json:"responsive_web_grok_community_note_auto_translation_is_enabled"`
	ResponsiveWebEnhanceCardsEnabled                               bool `json:"responsive_web_enhance_cards_enabled"`
}

// HomeTimelineResponse represents the GraphQL response
type HomeTimelineResponse struct {
	Data   *HomeTimelineData `json:"data,omitempty"`
	Errors []GraphQLError    `json:"errors,omitempty"`
}

type GraphQLError struct {
	Message string `json:"message"`
}

type HomeTimelineData struct {
	Home *HomeTimelineInfo `json:"home,omitempty"`
}

type HomeTimelineInfo struct {
	HomeTimelineURT *TimelineURT `json:"home_timeline_urt,omitempty"`
}

type TimelineURT struct {
	Instructions []TimelineInstruction `json:"instructions"`
}

type TimelineInstruction struct {
	Entries []TimelineEntry `json:"entries"`
}

type TimelineEntry struct {
	EntryID   string       `json:"entryId"`
	SortIndex string       `json:"sortIndex"`
	Content   EntryContent `json:"content"`
}

type EntryContent struct {
	ItemContent *TimelineTweet `json:"itemContent,omitempty"`
	CursorType  string         `json:"cursorType,omitempty"`
	Value       string         `json:"value,omitempty"`
	EntryType   string         `json:"entryType,omitempty"`
}

type TimelineTweet struct {
	TweetDisplayType string        `json:"tweetDisplayType,omitempty"`
	TweetResults     *TweetResults `json:"tweet_results,omitempty"`
}

// TweetResults wraps the actual tweet result
type TweetResults struct {
	Result *TweetResult `json:"result,omitempty"`
}

// TweetResult contains the actual tweet data
type TweetResult struct {
	RestID             string              `json:"rest_id,omitempty"`
	Core               *TweetCore          `json:"core,omitempty"`
	Legacy             *TweetLegacy        `json:"legacy,omitempty"`
	EditControl        *EditControl        `json:"edit_control,omitempty"`
	Views              *TweetViews         `json:"views,omitempty"`
	IsTranslatable     bool                `json:"is_translatable,omitempty"`
	QuotedStatusResult *TweetResultWrapper `json:"quoted_status_result,omitempty"`
}

type TweetCore struct {
	UserResults *UserResults `json:"user_results,omitempty"`
}

type UserResults struct {
	Result *UserResult `json:"result,omitempty"`
}

type UserResult struct {
	RestID string        `json:"rest_id,omitempty"`
	Core   *UserCoreInfo `json:"core,omitempty"`
	Legacy *UserLegacy   `json:"legacy,omitempty"`
}

type UserCoreInfo struct {
	Name       string `json:"name,omitempty"`
	ScreenName string `json:"screen_name,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type UserLegacy struct {
	Description    string `json:"description,omitempty"`
	FollowersCount int64  `json:"followers_count,omitempty"`
	FriendsCount   int64  `json:"friends_count,omitempty"`
	MediaCount     int64  `json:"media_count,omitempty"`
}

type TweetLegacy struct {
	FullText              string              `json:"full_text,omitempty"`
	CreatedAt             string              `json:"created_at,omitempty"`
	IDStr                 string              `json:"id_str,omitempty"`
	RetweetCount          int64               `json:"retweet_count,omitempty"`
	FavoriteCount         int64               `json:"favorite_count,omitempty"`
	ReplyCount            int64               `json:"reply_count,omitempty"`
	Lang                  string              `json:"lang,omitempty"`
	Retweeted             bool                `json:"retweeted,omitempty"`
	Favorited             bool                `json:"favorited,omitempty"`
	PossiblySensitive     bool                `json:"possibly_sensitive,omitempty"`
	Entities              *TweetEntities      `json:"entities,omitempty"`
	ExtendedEntities      *TweetEntities      `json:"extended_entities,omitempty"`
	RetweetedStatusResult *TweetResultWrapper `json:"retweeted_status_result,omitempty"`
	QuotedStatusResult    *TweetResultWrapper `json:"quoted_status_result,omitempty"`
}

type TweetResultWrapper struct {
	Result *TweetResult `json:"result,omitempty"`
}

type TweetEntities struct {
	Media []EntityMedia `json:"media,omitempty"`
}

type EntityMedia struct {
	IDStr                string             `json:"id_str,omitempty"`
	MediaURLHTTPS        string             `json:"media_url_https,omitempty"`
	Type                 string             `json:"type,omitempty"`
	ExtMediaAvailability *MediaAvailability `json:"ext_media_availability,omitempty"`
	VideoInfo            *VideoInfo         `json:"video_info,omitempty"`
	Sizes                *MediaSizes        `json:"sizes,omitempty"`
	Indices              []int              `json:"indices,omitempty"`
	URL                  string             `json:"url,omitempty"`
	DisplayURL           string             `json:"display_url,omitempty"`
	ExpandedURL          string             `json:"expanded_url,omitempty"`
}

type MediaAvailability struct {
	Status string `json:"status,omitempty"`
}

type VideoInfo struct {
	AspectRatio    []int          `json:"aspect_ratio,omitempty"`
	DurationMillis int64          `json:"duration_millis,omitempty"`
	Variants       []VideoVariant `json:"variants,omitempty"`
}

type VideoVariant struct {
	ContentType string `json:"content_type,omitempty"`
	URL         string `json:"url,omitempty"`
	Bitrate     int64  `json:"bitrate,omitempty"`
}

type MediaSizes struct {
	Large  *MediaSize `json:"large,omitempty"`
	Medium *MediaSize `json:"medium,omitempty"`
	Small  *MediaSize `json:"small,omitempty"`
	Thumb  *MediaSize `json:"thumb,omitempty"`
}

type MediaSize struct {
	Width  int    `json:"w,omitempty"`
	Height int    `json:"h,omitempty"`
	Resize string `json:"resize,omitempty"`
}

type EditControl struct {
	EditTweetIDs       []string `json:"edit_tweet_ids,omitempty"`
	EditableUntilMsecs string   `json:"editable_until_msecs,omitempty"`
	EditsRemaining     string   `json:"edits_remaining,omitempty"`
	IsEditEligible     bool     `json:"is_edit_eligible,omitempty"`
}

type TweetViews struct {
	Count string `json:"count,omitempty"`
	State string `json:"state,omitempty"`
}

// HomeTimelineResult contains parsed tweets and pagination cursor
type HomeTimelineResult struct {
	Tweets []*Tweet
	Cursor string
}

// HomeTimeline fetches the home timeline with optional cursor for pagination
// Uses POST for first request (no cursor), GET for subsequent pagination requests
func (t *TwitterAPI) HomeTimeline(ctx context.Context, cursor string) (*HomeTimelineResult, error) {
	if !t.IsEnabled() {
		return nil, errors.New("twitter API not configured with cookies")
	}

	variables := HomeTimelineVariables{
		Count:                  20,
		EnableRanking:          false,
		IncludePromotedContent: true,
		RequestContext:         "launch",
		SeenTweetIDs:           []string{},
		Cursor:                 cursor,
	}

	features := HomeTimelineFeatures{
		RwebVideoScreenEnabled:                                         false,
		ProfileLabelImprovementsPcfLabelInPostEnabled:                  true,
		ResponsiveWebProfileRedirectEnabled:                            false,
		RwebTipjarConsumptionEnabled:                                   false,
		VerifiedPhoneLabelEnabled:                                      false,
		CreatorSubscriptionsTweetPreviewAPIEnabled:                     true,
		ResponsiveWebGraphqlTimelineNavigationEnabled:                  true,
		ResponsiveWebGraphqlSkipUserProfileImageExtensionsEnabled:      false,
		PremiumContentAPIReadEnabled:                                   false,
		CommunitiesWebEnableTweetCommunityResultsFetch:                 true,
		C9sTweetAnatomyModeratorBadgeEnabled:                           true,
		ResponsiveWebGrokAnalyzeButtonFetchTrendsEnabled:               false,
		ResponsiveWebGrokAnalyzePostFollowupsEnabled:                   true,
		ResponsiveWebJetfuelFrame:                                      true,
		ResponsiveWebGrokShareAttachmentEnabled:                        true,
		ResponsiveWebGrokAnnotationsEnabled:                            true,
		ArticlesPreviewEnabled:                                         true,
		ResponsiveWebEditTweetAPIEnabled:                               true,
		GraphqlIsTranslatableRwebTweetIsTranslatableEnabled:            true,
		ViewCountsEverywhereAPIEnabled:                                 true,
		LongformNotetweetsConsumptionEnabled:                           true,
		ResponsiveWebTwitterArticleTweetConsumptionEnabled:             true,
		TweetAwardsWebTippingEnabled:                                   false,
		ContentDisclosureIndicatorEnabled:                              true,
		ContentDisclosureAIGeneratedIndicatorEnabled:                   true,
		ResponsiveWebGrokShowGrokTranslatedPost:                        false,
		ResponsiveWebGrokAnalysisButtonFromBackend:                     true,
		PostCtasFetchEnabled:                                           false,
		FreedomOfSpeechNotReachFetchEnabled:                            true,
		StandardizedNudgesMisinfo:                                      true,
		TweetWithVisibilityResultsPreferGqlLimitedActionsPolicyEnabled: true,
		LongformNotetweetsRichTextReadEnabled:                          true,
		LongformNotetweetsInlineMediaEnabled:                           false,
		ResponsiveWebGrokImageAnnotationEnabled:                        true,
		ResponsiveWebGrokImagineAnnotationEnabled:                      true,
		ResponsiveWebGrokCommunityNoteAutoTranslationIsEnabled:         false,
		ResponsiveWebEnhanceCardsEnabled:                               false,
	}

	apiURL := fmt.Sprintf("%s/%s/HomeLatestTimeline", TwitterGraphQLAPI, t.queryId)

	var resp HomeTimelineResponse

	if cursor == "" {
		req := HomeTimelineRequest{
			Variables: variables,
			Features:  features,
			QueryID:   t.queryId,
		}
		reqBody, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		err = t.doPost(ctx, apiURL, reqBody, &resp)
		if err != nil {
			return nil, err
		}
	} else {
		req := HomeTimelineRequest{
			Variables: variables,
			Features:  features,
			QueryID:   t.queryId,
		}
		var err error
		err = t.doGet(ctx, apiURL, req, &resp)
		if err != nil {
			return nil, err
		}
	}

	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", resp.Errors)
	}

	if resp.Data == nil || resp.Data.Home == nil || resp.Data.Home.HomeTimelineURT == nil {
		return nil, errors.New("invalid API response: missing data")
	}

	return t.parseTimelineResponse(resp.Data.Home.HomeTimelineURT)
}

func (t *TwitterAPI) doPost(_ context.Context, apiURL string, body []byte, out any) error {
	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 30),
		requests.AddUAOption(UserAgent),
		requests.HeaderOption("authorization", "Bearer "+t.bearerToken),
		requests.HeaderOption("x-csrf-token", t.ct0),
		requests.HeaderOption("content-type", "application/json"),
		requests.HeaderOption("Accept", "*/*"),
		requests.HeaderOption("Accept-Language", "en-US,en;q=0.9"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br"),
		requests.HeaderOption("sec-ch-ua", `"Chromium";v="135", "Not-A.Brand";v="8"`),
		requests.HeaderOption("sec-ch-ua-mobile", "?0"),
		requests.HeaderOption("sec-ch-ua-platform", `"Windows"`),
		requests.HeaderOption("sec-fetch-dest", "empty"),
		requests.HeaderOption("sec-fetch-mode", "cors"),
		requests.HeaderOption("sec-fetch-site", "same-origin"),
		requests.HeaderOption("Referer", "https://x.com/home"),
		requests.HeaderOption("X-Twitter-Active-User", "yes"),
		requests.CookieOption("ct0", t.ct0),
		requests.CookieOption("auth_token", t.authToken),
		requests.RetryOption(3),
	}

	var rawResp []byte
	err := requests.PostBody(apiURL, body, &rawResp, opts...)
	if err != nil {
		return fmt.Errorf("POST request failed: %w", err)
	}

	decompressed, err := decompressResponse(rawResp)
	if err != nil {
		return fmt.Errorf("decompress failed: %w", err)
	}

	err = json.Unmarshal(decompressed, out)
	if err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	return nil
}

func (t *TwitterAPI) doGet(ctx context.Context, apiURL string, req HomeTimelineRequest, out any) error {
	variablesJson, err := json.Marshal(req.Variables)
	if err != nil {
		return fmt.Errorf("failed to marshal variables: %w", err)
	}

	featuresJson, err := json.Marshal(req.Features)
	if err != nil {
		return fmt.Errorf("failed to marshal features: %w", err)
	}

	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 30),
		requests.AddUAOption(UserAgent),
		requests.HeaderOption("authorization", "Bearer "+t.bearerToken),
		requests.HeaderOption("x-csrf-token", t.ct0),
		requests.HeaderOption("Accept", "*/*"),
		requests.HeaderOption("Accept-Language", "en-US,en;q=0.9"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br"),
		requests.HeaderOption("sec-ch-ua", `"Chromium";v="135", "Not-A.Brand";v="8"`),
		requests.HeaderOption("sec-ch-ua-mobile", "?0"),
		requests.HeaderOption("sec-ch-ua-platform", `"Windows"`),
		requests.HeaderOption("sec-fetch-dest", "empty"),
		requests.HeaderOption("sec-fetch-mode", "cors"),
		requests.HeaderOption("sec-fetch-site", "same-origin"),
		requests.HeaderOption("Referer", "https://x.com/home"),
		requests.HeaderOption("X-Twitter-Active-User", "yes"),
		requests.CookieOption("ct0", t.ct0),
		requests.CookieOption("auth_token", t.authToken),
		requests.RetryOption(3),
	}

	getURL := apiURL + "?variables=" + url.QueryEscape(string(variablesJson)) + "&features=" + url.QueryEscape(string(featuresJson))

	var rawResp []byte
	err = requests.Get(getURL, nil, &rawResp, opts...)
	if err != nil {
		return fmt.Errorf("GET request failed: %w", err)
	}

	decompressed, err := decompressResponse(rawResp)
	if err != nil {
		return fmt.Errorf("decompress failed: %w", err)
	}

	err = json.Unmarshal(decompressed, out)
	if err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	return nil
}

func decompressResponse(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return data, nil
	}

	switch {
	case data[0] == 0x1f && data[1] == 0x8b:
		return decompressGzip(data)
	case data[0] == 0x78 && (data[1] == 0x9c || data[1] == 0xda || data[1] == 0x01):
		return decompressDeflate(data)
	case data[0] == 0xce && data[1] == 0xb2 && data[2] == 0xcf && data[3] == 0xfa:
		return decompressBrotli(data)
	default:
		return data, nil
	}
}

func decompressGzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func decompressDeflate(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func decompressBrotli(data []byte) ([]byte, error) {
	r := brotli.NewReader(bytes.NewReader(data))
	return io.ReadAll(r)
}

// parseTimelineResponse extracts tweets and next cursor from the API response
func (t *TwitterAPI) parseTimelineResponse(urt *TimelineURT) (result *HomeTimelineResult, err error) {
	result = &HomeTimelineResult{
		Tweets: make([]*Tweet, 0),
	}

	var topCursor string

	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("parseTimelineResponse panic recovered: %v", r)
			// 返回已解析的部分，不返回错误让调用方继续
			result.Cursor = topCursor
			err = nil
		}
	}()

	for _, instruction := range urt.Instructions {
		for _, entry := range instruction.Entries {
			// Check if this is a cursor entry
			if isCursorEntry(entry.EntryID) && entry.Content.CursorType == "Top" && entry.Content.Value != "" {
				topCursor = entry.Content.Value
				continue
			}

			// Skip entries without item content
			if entry.EntryID == "" || entry.Content.ItemContent == nil {
				continue
			}

			// Only process tweet entries
			if !isTweetEntry(entry.EntryID) {
				continue
			}

			tweet := t.parseTweetEntry(&entry)
			if tweet != nil {
				result.Tweets = append(result.Tweets, tweet)
			}
		}
	}

	result.Cursor = topCursor
	return result, nil
}

// isCursorEntry checks if the entry is a cursor entry
func isCursorEntry(entryID string) bool {
	return len(entryID) > 7 && entryID[:7] == "cursor-"
}

// isTweetEntry checks if the entry is a tweet (not a cursor or other type)
func isTweetEntry(entryID string) bool {
	return len(entryID) > 6 && entryID[:6] == "tweet-"
}

// parseTweetEntry converts a TimelineEntry to a Tweet model
func (t *TwitterAPI) parseTweetEntry(entry *TimelineEntry) *Tweet {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("parseTweetEntry panic recovered: %v", r)
		}
	}()

	item := entry.Content.ItemContent
	if item == nil || item.TweetResults == nil || item.TweetResults.Result == nil {
		return nil
	}

	result := item.TweetResults.Result

	if result.Legacy != nil && result.Legacy.RetweetedStatusResult != nil {
		return t.parseRetweetEntry(result)
	}

	if result.QuotedStatusResult != nil {
		return t.parseQuoteEntry(result)
	}

	return t.parseOriginalTweet(result)
}

func (t *TwitterAPI) parseRetweetEntry(result *TweetResult) *Tweet {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("parseRetweetEntry panic recovered: %v", r)
		}
	}()

	orig := result.Legacy.RetweetedStatusResult
	if orig == nil || orig.Result == nil {
		return nil
	}

	original := orig.Result

	origUser := t.extractUserFromResult(original)
	retweetUser := t.extractUserFromResult(result)

	tweet := &Tweet{
		ID:          result.Legacy.IDStr,
		Content:     result.Legacy.FullText,
		Likes:       result.Legacy.FavoriteCount,
		Retweets:    result.Legacy.RetweetCount,
		Replies:     result.Legacy.ReplyCount,
		IsRetweet:   true,
		OrgUser:     origUser,
		RetweetUser: retweetUser,
		Media:       make([]*Media, 0),
	}

	if original.Legacy != nil {
		tweet.CreatedAt = parseTwitterDate(result.Legacy.CreatedAt)
		if original.Legacy.ExtendedEntities != nil && len(original.Legacy.ExtendedEntities.Media) > 0 {
			for _, m := range original.Legacy.ExtendedEntities.Media {
				media := &Media{
					Type: m.Type,
					Url:  m.MediaURLHTTPS,
				}
				if (m.Type == "video" || m.Type == "animated_gif") && m.VideoInfo != nil {
					for _, v := range m.VideoInfo.Variants {
						if v.ContentType == "video/mp4" && v.URL != "" {
							media.Url = v.URL
							break
						}
					}
				}
				tweet.Media = append(tweet.Media, media)
			}
		}

		// 处理嵌套的引用推文 (转发+引用的情况)
		if original.QuotedStatusResult != nil && original.QuotedStatusResult.Result != nil {
			quotedOriginal := original.QuotedStatusResult.Result
			quotedUser := t.extractUserFromResult(quotedOriginal)
			tweet.QuoteTweet = &Tweet{
				OrgUser: quotedUser,
				Media:   make([]*Media, 0),
			}
			if quotedOriginal.Legacy != nil {
				tweet.QuoteTweet.ID = quotedOriginal.Legacy.IDStr
				tweet.QuoteTweet.Content = quotedOriginal.Legacy.FullText
				tweet.QuoteTweet.CreatedAt = parseTwitterDate(quotedOriginal.Legacy.CreatedAt)
				if quotedOriginal.Legacy.ExtendedEntities != nil {
					for _, m := range quotedOriginal.Legacy.ExtendedEntities.Media {
						media := &Media{
							Type: m.Type,
							Url:  m.MediaURLHTTPS,
						}
						if (m.Type == "video" || m.Type == "animated_gif") && m.VideoInfo != nil {
							for _, v := range m.VideoInfo.Variants {
								if v.ContentType == "video/mp4" && v.URL != "" {
									media.Url = v.URL
									break
								}
							}
						}
						tweet.QuoteTweet.Media = append(tweet.QuoteTweet.Media, media)
					}
				}
			}
		}
	}

	if origUser != nil && tweet.ID != "" {
		tweet.Url = fmt.Sprintf("https://x.com/%s/status/%s", origUser.ScreenName, tweet.ID)
	}

	return tweet
}

func (t *TwitterAPI) extractUserFromResult(result *TweetResult) *UserProfile {
	if result == nil {
		return nil
	}
	if result.Core != nil && result.Core.UserResults != nil && result.Core.UserResults.Result != nil {
		u := result.Core.UserResults.Result
		if u.Core != nil {
			return &UserProfile{
				ScreenName: u.Core.ScreenName,
				Name:       u.Core.Name,
			}
		}
	}
	return nil
}

func (t *TwitterAPI) parseQuoteEntry(result *TweetResult) *Tweet {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("parseQuoteEntry panic recovered: %v", r)
		}
	}()

	// quoted_status_result 可能在 result 层级，也可能在 result.legacy 里
	var quotedResult *TweetResult
	if result.QuotedStatusResult != nil && result.QuotedStatusResult.Result != nil {
		quotedResult = result.QuotedStatusResult.Result
	} else if result.Legacy != nil && result.Legacy.QuotedStatusResult != nil && result.Legacy.QuotedStatusResult.Result != nil {
		quotedResult = result.Legacy.QuotedStatusResult.Result
	}

	if quotedResult == nil {
		return nil
	}

	// quotedUser 是被引用者（被引用的推文作者）
	quotedUser := t.extractUserFromResult(quotedResult)
	// quoterUser 是引用者（当前用户，例如 alen）
	quoterUser := t.extractUserFromResult(result)

	var quoteCreatedAt time.Time
	var quoteContent string
	var quoteID string
	if quotedResult.Legacy != nil {
		quoteID = quotedResult.Legacy.IDStr
		quoteContent = quotedResult.Legacy.FullText
		quoteCreatedAt = parseTwitterDate(quotedResult.Legacy.CreatedAt)
	}

	tweet := &Tweet{
		ID:        "",
		Content:   "",
		Likes:     0,
		Retweets:  0,
		Replies:   0,
		IsRetweet: false,
		OrgUser:   quoterUser,  // 引用者（订阅者）
		CreatedAt: time.Time{}, // 引用本身没有独立时间，用主推文时间
		Media:     make([]*Media, 0),
		QuoteTweet: &Tweet{
			ID:        quoteID,
			Content:   quoteContent,
			CreatedAt: quoteCreatedAt, // 被引用推文的时间
			OrgUser:   quotedUser,     // 被引用作者
			Media:     make([]*Media, 0),
		},
	}

	if result.Legacy != nil {
		tweet.ID = result.Legacy.IDStr
		tweet.Content = result.Legacy.FullText
		tweet.Likes = result.Legacy.FavoriteCount
		tweet.Retweets = result.Legacy.RetweetCount
		tweet.Replies = result.Legacy.ReplyCount
		tweet.CreatedAt = parseTwitterDate(result.Legacy.CreatedAt) // 引用这条推文的时间
	}

	if quotedResult.Legacy != nil {
		if len(quotedResult.Legacy.ExtendedEntities.Media) > 0 {
			for _, m := range quotedResult.Legacy.ExtendedEntities.Media {
				media := &Media{
					Type: m.Type,
					Url:  m.MediaURLHTTPS,
				}
				if (m.Type == "video" || m.Type == "animated_gif") && m.VideoInfo != nil {
					for _, v := range m.VideoInfo.Variants {
						if v.ContentType == "video/mp4" && v.URL != "" {
							media.Url = v.URL
							break
						}
					}
				}
				tweet.QuoteTweet.Media = append(tweet.QuoteTweet.Media, media)
			}
		}
	}

	if quoterUser != nil {
		tweet.Url = fmt.Sprintf("https://x.com/%s/status/%s", quoterUser.ScreenName, tweet.ID)
	}

	return tweet
}

func (t *TwitterAPI) parseOriginalTweet(result *TweetResult) *Tweet {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("parseOriginalTweet panic recovered: %v", r)
		}
	}()

	if result.RestID == "" && result.Legacy != nil {
		result.RestID = result.Legacy.IDStr
	}

	tweet := &Tweet{
		ID:        result.RestID,
		IsRetweet: false,
		Media:     make([]*Media, 0),
	}

	if result.Core != nil && result.Core.UserResults != nil && result.Core.UserResults.Result != nil {
		user := result.Core.UserResults.Result
		screenName := ""
		name := ""
		if user.Core != nil {
			screenName = user.Core.ScreenName
			name = user.Core.Name
		}
		tweet.OrgUser = &UserProfile{
			ScreenName: screenName,
			Name:       name,
		}
	}

	if result.Legacy != nil {
		legacy := result.Legacy
		tweet.Content = legacy.FullText
		tweet.Likes = legacy.FavoriteCount
		tweet.Retweets = legacy.RetweetCount
		tweet.Replies = legacy.ReplyCount

		if legacy.CreatedAt != "" {
			tweet.CreatedAt = parseTwitterDate(legacy.CreatedAt)
		}

		if legacy.ExtendedEntities != nil {
			for _, m := range legacy.ExtendedEntities.Media {
				media := &Media{
					Type: m.Type,
					Url:  m.MediaURLHTTPS,
				}
				if (m.Type == "video" || m.Type == "animated_gif") && m.VideoInfo != nil {
					for _, v := range m.VideoInfo.Variants {
						if v.ContentType == "video/mp4" && v.URL != "" {
							media.Url = v.URL
							break
						}
					}
				}
				tweet.Media = append(tweet.Media, media)
			}
		}

		if len(tweet.Media) == 0 && legacy.Entities != nil {
			for _, m := range legacy.Entities.Media {
				tweet.Media = append(tweet.Media, &Media{
					Type: m.Type,
					Url:  m.MediaURLHTTPS,
				})
			}
		}
	}

	if tweet.OrgUser != nil && tweet.ID != "" {
		tweet.Url = fmt.Sprintf("https://x.com/%s/status/%s", tweet.OrgUser.ScreenName, tweet.ID)
	}

	return tweet
}

func parseTwitterDate(dateStr string) time.Time {
	formats := []string{
		"Mon Jan 2 15:04:05 +0000 2006",
		"Mon Jan 2 15:04:05 -0700 2006",
		time.RFC822,
	}
	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}
	return time.Time{}
}
func (t *TwitterAPI) GetHomeTimelineFirstPage(ctx context.Context) (*HomeTimelineResult, error) {
	return t.HomeTimeline(ctx, "")
}

// Follow sends a follow request for the given userId
func (t *TwitterAPI) Follow(ctx context.Context, userId string) error {
	return t.followUnfollow(ctx, userId, "create")
}

// Unfollow sends an unfollow request for the given userId
func (t *TwitterAPI) Unfollow(ctx context.Context, userId string) error {
	return t.followUnfollow(ctx, userId, "destroy")
}

func (t *TwitterAPI) followUnfollow(ctx context.Context, userId, action string) error {
	if !t.IsEnabled() {
		return errors.New("twitter API not configured with cookies")
	}

	apiURL := fmt.Sprintf("https://x.com/i/api/1.1/friendships/%s.json", action)

	formData := fmt.Sprintf(
		"include_profile_interstitial_type=1&include_blocking=1&include_blocked_by=1&include_followed_by=1&include_want_retweets=1&include_mute_edge=1&include_can_dm=1&include_can_media_tag=1&include_ext_is_blue_verified=1&include_ext_verified_type=1&include_ext_profile_image_shape=1&skip_status=1&user_id=%s",
		userId,
	)

	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 30),
		requests.AddUAOption(UserAgent),
		requests.HeaderOption("authorization", "Bearer "+t.bearerToken),
		requests.HeaderOption("x-csrf-token", t.ct0),
		requests.HeaderOption("content-type", "application/x-www-form-urlencoded"),
		requests.HeaderOption("Accept", "*/*"),
		requests.HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br"),
		requests.HeaderOption("sec-ch-ua", `"Chromium";v="135", "Not-A.Brand";v="8"`),
		requests.HeaderOption("sec-ch-ua-mobile", "?0"),
		requests.HeaderOption("sec-ch-ua-platform", `"Windows"`),
		requests.HeaderOption("sec-fetch-dest", "empty"),
		requests.HeaderOption("sec-fetch-mode", "cors"),
		requests.HeaderOption("sec-fetch-site", "same-origin"),
		requests.HeaderOption("X-Twitter-Active-User", "yes"),
		requests.HeaderOption("X-Twitter-Auth-Type", "OAuth2Session"),
		requests.CookieOption("ct0", t.ct0),
		requests.CookieOption("auth_token", t.authToken),
		requests.RetryOption(3),
	}

	var rawResp []byte
	body := []byte(formData)
	err := requests.PostBody(apiURL, body, &rawResp, opts...)
	if err != nil {
		return fmt.Errorf("follow/unfollow request failed: %w", err)
	}

	decompressed, err := decompressResponse(rawResp)
	if err != nil {
		return fmt.Errorf("decompress failed: %w", err)
	}

	// Check for error response
	var errResp struct {
		Errors []struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(decompressed, &errResp); err == nil {
		if len(errResp.Errors) > 0 {
			return fmt.Errorf("follow/unfollow error: %s (code: %d)", errResp.Errors[0].Message, errResp.Errors[0].Code)
		}
	}

	// Check if response contains user data (success)
	var userResp struct {
		ID          string `json:"id"`
		ScreenName  string `json:"screen_name"`
		Following   bool   `json:"following"`
	}
	if err := json.Unmarshal(decompressed, &userResp); err != nil {
		return fmt.Errorf("failed to parse follow/unfollow response: %w", err)
	}

	if userResp.ID == "" {
		return errors.New("follow/unfollow failed: no user id in response")
	}

	return nil
}

// UserProfileInfo contains user profile info from ProfileSpotlightsQuery
type UserProfileInfo struct {
	RestID      string // Numeric user ID
	ScreenName  string // Username (without @)
	Name        string // Display name
	IsFollowing bool   // Current user is following this user
}

// GetUserByScreenName fetches user profile info by screen name using GraphQL API
// Returns UserProfileInfo with following status and user details
func (t *TwitterAPI) GetUserByScreenName(ctx context.Context, screenName string) (*UserProfileInfo, error) {
	if !t.IsEnabled() {
		return nil, errors.New("twitter API not configured with cookies")
	}

	queryId := "mzoqrVGwk-YTSGME1dRfXQ"
	apiURL := fmt.Sprintf("https://x.com/i/api/graphql/%s/ProfileSpotlightsQuery", queryId)

	variables := map[string]string{"screen_name": screenName}
	reqBody := map[string]interface{}{
		"variables": variables,
	}
	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 30),
		requests.AddUAOption(UserAgent),
		requests.HeaderOption("authorization", "Bearer "+t.bearerToken),
		requests.HeaderOption("x-csrf-token", t.ct0),
		requests.HeaderOption("content-type", "application/json"),
		requests.HeaderOption("Accept", "*/*"),
		requests.HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br"),
		requests.HeaderOption("sec-ch-ua", `"Chromium";v="135", "Not-A.Brand";v="8"`),
		requests.HeaderOption("sec-ch-ua-mobile", "?0"),
		requests.HeaderOption("sec-ch-ua-platform", `"Windows"`),
		requests.HeaderOption("sec-fetch-dest", "empty"),
		requests.HeaderOption("sec-fetch-mode", "cors"),
		requests.HeaderOption("sec-fetch-site", "same-origin"),
		requests.HeaderOption("X-Twitter-Active-User", "yes"),
		requests.HeaderOption("X-Twitter-Auth-Type", "OAuth2Session"),
		requests.HeaderOption("Referer", fmt.Sprintf("https://x.com/%s", screenName)),
		requests.CookieOption("ct0", t.ct0),
		requests.CookieOption("auth_token", t.authToken),
		requests.RetryOption(3),
	}

	var rawResp []byte
	err = requests.PostBody(apiURL, reqBytes, &rawResp, opts...)
	if err != nil {
		return nil, fmt.Errorf("ProfileSpotlightsQuery request failed: %w", err)
	}

	decompressed, err := decompressResponse(rawResp)
	if err != nil {
		return nil, fmt.Errorf("decompress failed: %w", err)
	}

	var resp struct {
		Data struct {
			UserResultByScreenName struct {
				Result struct {
					Typename                   string `json:"__typename"`
					Core                       struct {
						Name       string `json:"name"`
						ScreenName string `json:"screen_name"`
					} `json:"core"`
					ID                        string `json:"id"`
					RelationshipPerspectives struct {
						Following bool `json:"following"`
					} `json:"relationship_perspectives"`
				} `json:"result"`
			} `json:"user_result_by_screen_name"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(decompressed, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse ProfileSpotlightsQuery response: %w", err)
	}

	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("ProfileSpotlightsQuery error: %s", resp.Errors[0].Message)
	}

	result := &resp.Data.UserResultByScreenName.Result
	return &UserProfileInfo{
		RestID:      result.ID,
		ScreenName:  result.Core.ScreenName,
		Name:        result.Core.Name,
		IsFollowing: resp.Data.UserResultByScreenName.Result.RelationshipPerspectives.Following,
	}, nil
}
