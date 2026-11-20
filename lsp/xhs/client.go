package xhs

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/xhs/crypto"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	"github.com/guonaihong/gout/dataflow"
	"github.com/sirupsen/logrus"
)

const (
	BaseURL            = "https://edith.xiaohongshu.com"
	LiveRoomBaseURL    = "https://live-room.xiaohongshu.com"
	WebBaseURL         = "https://www.xiaohongshu.com"
	SearchOneboxAPI    = "/api/sns/web/v1/search/onebox"
	FeedAPI            = "/api/sns/web/v1/feed"
	CurrentRoomInfoAPI = "/api/sns/red/live/web/v1/room/current_room_info"
)

const searchIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

var undefinedValuePattern = regexp.MustCompile(`([:\[,])\s*undefined(\s*[,}\]])`)
var noteItemPattern = regexp.MustCompile(`(?s)<section class="note-item".*?</section>`)
var noteItemHrefPattern = regexp.MustCompile(`/user/profile/[^/"'?]+/([^/"'?]+)`)

// Client is the XHS API client
//
// Search requests are signed against the exact JSON payload bytes, so the
// payload map is marshaled once and reused for both x-s and the request body.
type Client struct {
	config          *crypto.CryptoConfig
	crypto          *crypto.CryptoProcessor
	session         *crypto.SessionManager
	cookies         map[string]string
	logger          *logrus.Logger
	baseURL         string
	liveRoomBaseURL string
	webBaseURL      string
	requestOptions  []requests.Option
	httpTransport   *http.Transport
}

// NewClient creates a new XHS client
func NewClient(cookies map[string]string) *Client {
	config := crypto.DefaultCryptoConfig()
	return &Client{
		config:          config,
		crypto:          crypto.NewCryptoProcessor(config),
		session:         crypto.NewSessionManager(config),
		cookies:         cookies,
		logger:          logrus.New(),
		baseURL:         BaseURL,
		liveRoomBaseURL: LiveRoomBaseURL,
		webBaseURL:      WebBaseURL,
		requestOptions: []requests.Option{
			requests.TimeoutOption(30 * time.Second),
		},
	}
}

// SetCookies sets the cookies for the client
func (c *Client) SetCookies(cookies map[string]string) {
	c.cookies = cookies
}

// SetTransport sets the HTTP transport for the client (used in tests)
func (c *Client) SetTransport(tran *http.Transport) {
	c.httpTransport = tran
}

type SearchOneboxResponse struct {
	Code    int               `json:"code"`
	Success bool              `json:"success"`
	Msg     string            `json:"msg"`
	Data    *SearchOneboxData `json:"data"`
}

type SearchOneboxData struct {
	OneboxList []SearchOneboxItem `json:"onebox_list"`
}

type SearchOneboxItem struct {
	Type       string            `json:"type"`
	UserOneBox *SearchOneboxUser `json:"user_one_box"`
}

type SearchOneboxUser struct {
	Image    string                `json:"image"`
	Title    string                `json:"title"`
	ID       string                `json:"id"`
	RedID    string                `json:"red_id"`
	Link     string                `json:"link"`
	LiveInfo *SearchOneboxLiveInfo `json:"live_info"`
}

type SearchOneboxLiveInfo struct {
	RoomID    string `json:"room_id"`
	UserID    string `json:"user_id"`
	Status    int    `json:"status"`
	Link      string `json:"link"`
	StartTime int64  `json:"start_time"`
}

func (l *SearchOneboxLiveInfo) IsLiving() bool {
	return l != nil && l.Status == int(LiveStatus_Living)
}

func (l *SearchOneboxLiveInfo) IsNotLiving() bool {
	return l != nil && l.Status == int(LiveStatus_NoLiving)
}

type CurrentRoomInfoResponse struct {
	Code    int                  `json:"code"`
	Success bool                 `json:"success"`
	Msg     string               `json:"msg"`
	Data    *CurrentRoomInfoData `json:"data"`
}

type CurrentRoomInfoData struct {
	HostInfo *CurrentRoomHostInfo `json:"host_info"`
	RoomInfo *CurrentRoomInfo     `json:"room_info"`
}

type CurrentRoomHostInfo struct {
	UserID   string `json:"user_id"`
	Avatar   string `json:"avatar"`
	NickName string `json:"nick_name"`
}

type CurrentRoomInfo struct {
	DisplayMemberCount string `json:"display_member_count"`
	RoomID             string `json:"room_id"`
	RoomTitle          string `json:"room_title"`
	RoomCover          string `json:"room_cover"`
	Deeplink           string `json:"deeplink"`
	Status             int    `json:"status"`
	DisplayPraiseCount string `json:"display_praise_count"`
	DisplayViewerCount string `json:"display_viewer_count"`
	PullConfig         string `json:"pull_config"`
}

type UserProfileInitialState struct {
	User *UserProfilePageUser `json:"user"`
}

type UserProfilePageUser struct {
	UserPageData *UserProfilePageData `json:"userPageData"`
	Notes        [][]UserProfileNote  `json:"notes"`
}

type UserProfilePageData struct {
	BasicInfo *UserProfileBasicInfo `json:"basicInfo"`
}

type UserProfileBasicInfo struct {
	RedID      string `json:"redId"`
	Gender     int    `json:"gender"`
	IPLocation string `json:"ipLocation"`
	Desc       string `json:"desc"`
	ImageB     string `json:"imageb"`
	Nickname   string `json:"nickname"`
	Images     string `json:"images"`
}

type UserProfileNote struct {
	ID        string               `json:"id"`
	NoteCard  *UserProfileNoteCard `json:"noteCard"`
	XsecToken string               `json:"xsecToken"`
}

type UserProfileNoteCard struct {
	NoteID       string                `json:"noteId"`
	XsecToken    string                `json:"xsecToken"`
	Type         string                `json:"type"`
	DisplayTitle string                `json:"displayTitle"`
	User         *UserProfileNoteUser  `json:"user"`
	Cover        *UserProfileNoteCover `json:"cover"`
}

type UserProfileNoteUser struct {
	NickName string `json:"nickName"`
	Avatar   string `json:"avatar"`
	UserID   string `json:"userId"`
	Nickname string `json:"nickname"`
}

type UserProfileNoteCover struct {
	URL        string                 `json:"url"`
	URLPre     string                 `json:"urlPre"`
	URLDefault string                 `json:"urlDefault"`
	InfoList   []UserProfileImageInfo `json:"infoList"`
}

type UserProfileImageInfo struct {
	ImageScene string `json:"imageScene"`
	URL        string `json:"url"`
}

type FeedResponse struct {
	Code    int       `json:"code"`
	Success bool      `json:"success"`
	Msg     string    `json:"msg"`
	Data    *FeedData `json:"data"`
}

type FeedData struct {
	CursorScore string         `json:"cursor_score"`
	Items       []FeedDataItem `json:"items"`
	CurrentTime int64          `json:"current_time"`
}

type FeedDataItem struct {
	ID        string        `json:"id"`
	ModelType string        `json:"model_type"`
	NoteCard  *FeedNoteCard `json:"note_card"`
}

type FeedNoteCard struct {
	Desc           string            `json:"desc"`
	User           *FeedNoteUser     `json:"user"`
	InteractInfo   *FeedInteractInfo `json:"interact_info"`
	ImageList      []FeedImage       `json:"image_list"`
	TagList        []FeedTag         `json:"tag_list"`
	NoteID         string            `json:"note_id"`
	Type           string            `json:"type"`
	Title          string            `json:"title"`
	Time           int64             `json:"time"`
	LastUpdateTime int64             `json:"last_update_time"`
	Video          *FeedVideo        `json:"video"`
}

type FeedNoteUser struct {
	UserID    string `json:"user_id"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	XsecToken string `json:"xsec_token"`
}

type FeedInteractInfo struct {
	CollectedCount string `json:"collected_count"`
	CommentCount   string `json:"comment_count"`
	ShareCount     string `json:"share_count"`
	LikedCount     string `json:"liked_count"`
}

type FeedImage struct {
	InfoList   []FeedImageInfo `json:"info_list"`
	URLPre     string          `json:"url_pre"`
	URLDefault string          `json:"url_default"`
	URL        string          `json:"url"`
	Height     int             `json:"height"`
	Width      int             `json:"width"`
}

type FeedImageInfo struct {
	ImageScene string `json:"image_scene"`
	URL        string `json:"url"`
}

type FeedTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type FeedVideo struct {
	Image *FeedVideoImage `json:"image"`
	Capa  *FeedVideoCapa  `json:"capa"`
}

type FeedVideoImage struct {
	FirstFrameFileID string `json:"first_frame_fileid"`
	ThumbnailFileID  string `json:"thumbnail_fileid"`
}

type FeedVideoCapa struct {
	Duration int `json:"duration"`
}

func (c *Client) SearchUserOnebox(keyword string) (*SearchOneboxResponse, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("keyword is required")
	}

	a1Value := c.cookies["a1"]
	if a1Value == "" {
		return nil, fmt.Errorf("a1 cookie is required")
	}

	timestamp := float64(time.Now().Unix()) + float64(time.Now().Nanosecond())/1e9
	searchID, err := generateSearchID(21)
	if err != nil {
		return nil, fmt.Errorf("failed to generate search_id: %w", err)
	}
	xT := c.crypto.GetXT(timestamp)
	requestID, err := generateRequestID(xT)
	if err != nil {
		return nil, fmt.Errorf("failed to generate request_id: %w", err)
	}

	payload := map[string]interface{}{
		"keyword":    keyword,
		"search_id":  searchID,
		"biz_type":   "web_search_user",
		"request_id": requestID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	uri := SearchOneboxAPI
	signature, err := c.crypto.BuildSignature("POST", uri, a1Value, "xhs-pc-web", nil, payload, timestamp, c.session)
	if err != nil {
		return nil, fmt.Errorf("failed to build signature: %w", err)
	}

	headers := c.buildSignedHeaders(timestamp, xT, true)
	headers["x-s"] = signature
	fullURL := c.baseURL + uri
	decodedBody, err := c.doSignedRequest(http.MethodPost, fullURL, headers, body)
	if err != nil {
		return nil, err
	}

	var result SearchOneboxResponse
	if err := json.Unmarshal(decodedBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

func (c *Client) GetFeedNote(sourceNoteID string, xsecToken string, xsecSource string) (*FeedResponse, error) {
	sourceNoteID = strings.TrimSpace(sourceNoteID)
	xsecToken = strings.TrimSpace(xsecToken)
	xsecSource = strings.TrimSpace(xsecSource)
	if sourceNoteID == "" {
		return nil, fmt.Errorf("source_note_id is required")
	}
	if xsecToken == "" {
		return nil, fmt.Errorf("xsec_token is required")
	}
	if xsecSource == "" {
		xsecSource = "pc_user"
	}

	a1Value := c.cookies["a1"]
	if a1Value == "" {
		return nil, fmt.Errorf("a1 cookie is required")
	}

	timestamp := float64(time.Now().Unix()) + float64(time.Now().Nanosecond())/1e9
	xT := c.crypto.GetXT(timestamp)
	payload := map[string]interface{}{
		"source_note_id": sourceNoteID,
		"image_formats":  []string{"jpg", "webp", "avif"},
		"extra": map[string]string{
			"need_body_topic": "1",
		},
		"xsec_source": xsecSource,
		"xsec_token":  xsecToken,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	signature, err := c.crypto.BuildSignature(http.MethodPost, FeedAPI, a1Value, "xhs-pc-web", nil, payload, timestamp, c.session)
	if err != nil {
		return nil, fmt.Errorf("failed to build signature: %w", err)
	}

	headers := c.buildSignedHeaders(timestamp, xT, true)
	headers["x-s"] = signature
	fullURL := c.baseURL + FeedAPI
	decodedBody, err := c.doSignedRequest(http.MethodPost, fullURL, headers, body)
	if err != nil {
		return nil, err
	}

	var result FeedResponse
	if err := json.Unmarshal(decodedBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetCurrentRoomInfo(roomID string, requestUserID string) (*CurrentRoomInfoResponse, error) {
	roomID = strings.TrimSpace(roomID)
	requestUserID = strings.TrimSpace(requestUserID)
	if roomID == "" {
		return nil, fmt.Errorf("room_id is required")
	}
	if requestUserID == "" {
		return nil, fmt.Errorf("request_user_id is required")
	}

	a1Value := c.cookies["a1"]
	if a1Value == "" {
		return nil, fmt.Errorf("a1 cookie is required")
	}

	timestamp := float64(time.Now().Unix()) + float64(time.Now().Nanosecond())/1e9
	xT := c.crypto.GetXT(timestamp)
	params := map[string]string{
		"room_id":         roomID,
		"request_user_id": requestUserID,
		"source":          "web_live",
		"client_type":     "1",
	}

	signature, err := c.crypto.BuildSignature(http.MethodGet, CurrentRoomInfoAPI, a1Value, "xhs-pc-web", params, nil, timestamp, c.session)
	if err != nil {
		return nil, fmt.Errorf("failed to build signature: %w", err)
	}

	headers := c.buildSignedHeaders(timestamp, xT, false)
	headers["x-s"] = signature
	fullURL := c.liveRoomBaseURL + CurrentRoomInfoAPI + "?" + buildOrderedQueryString(params)
	decodedBody, err := c.doSignedRequest(http.MethodGet, fullURL, headers, nil)
	if err != nil {
		return nil, err
	}

	var result CurrentRoomInfoResponse
	if err := json.Unmarshal(decodedBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

func (c *Client) GetUserProfile(userID string) (*UserProfilePageUser, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	headers := c.buildWebHeaders("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	fullURL := c.webBaseURL + "/user/profile/" + url.PathEscape(userID)
	body, err := c.doRequest(http.MethodGet, fullURL, headers, nil)
	if err != nil {
		return nil, err
	}

	initialStateJSON, err := extractInitialStateJSON(body)
	if err != nil {
		return nil, err
	}

	var state UserProfileInitialState
	if err := json.Unmarshal(initialStateJSON, &state); err != nil {
		return nil, fmt.Errorf("failed to parse initial state: %w", err)
	}
	if state.User == nil {
		return nil, fmt.Errorf("user profile page returned empty user state")
	}
	applyProfileNoteTypesFromHTML(state.User, body)
	return state.User, nil
}

func (c *Client) buildSignedHeaders(timestamp float64, xT int64, includeJSONContentType bool) map[string]string {
	headers := map[string]string{
		"x-s-common":         c.crypto.SignXSCommon(c.cookies["a1"], c.cookies, timestamp),
		"x-t":                fmt.Sprintf("%d", xT),
		"x-b3-traceid":       c.crypto.GenerateB3TraceID(),
		"x-xray-traceid":     c.crypto.GenerateXrayTraceID(xT, 0),
		"sec-ch-ua-platform": "\"Windows\"",
		"sec-ch-ua":          "\"Chromium\";v=\"148\", \"Microsoft Edge\";v=\"148\", \"Not/A)Brand\";v=\"99\"",
		"sec-ch-ua-mobile":   "?0",
		"user-agent":         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36 Edg/148.0.0.0",
		"accept":             "application/json, text/plain, */*",
		"origin":             "https://www.xiaohongshu.com",
		"sec-fetch-site":     "same-site",
		"sec-fetch-mode":     "cors",
		"sec-fetch-dest":     "empty",
		"referer":            "https://www.xiaohongshu.com/",
		"accept-encoding":    "gzip, deflate, br, zstd",
		"accept-language":    "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6",
		"priority":           "u=1, i",
	}
	if includeJSONContentType {
		headers["content-type"] = "application/json;charset=UTF-8"
	}
	return headers
}

func (c *Client) buildWebHeaders(accept string) map[string]string {
	return map[string]string{
		"sec-ch-ua-platform":        "\"Windows\"",
		"sec-ch-ua":                 "\"Chromium\";v=\"148\", \"Microsoft Edge\";v=\"148\", \"Not/A)Brand\";v=\"99\"",
		"sec-ch-ua-mobile":          "?0",
		"user-agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36 Edg/148.0.0.0",
		"accept":                    accept,
		"upgrade-insecure-requests": "1",
		"sec-fetch-site":            "same-origin",
		"sec-fetch-mode":            "navigate",
		"sec-fetch-user":            "?1",
		"sec-fetch-dest":            "document",
		"referer":                   c.webBaseURL + "/",
		"accept-encoding":           "gzip, deflate, br, zstd",
		"accept-language":           "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6",
		"priority":                  "u=0, i",
	}
}

func (c *Client) doSignedRequest(method string, fullURL string, headers map[string]string, body []byte) ([]byte, error) {
	return c.doRequest(method, fullURL, headers, body)
}

func (c *Client) doRequest(method string, fullURL string, headers map[string]string, body []byte) ([]byte, error) {
	var respBody []byte
	var respHeader requests.RespHeader
	options := append([]requests.Option{}, c.requestOptions...)
	if c.httpTransport != nil {
		options = append(options, requests.WithTransport(c.httpTransport))
	}
	for key, value := range headers {
		options = append(options, requests.HeaderOption(key, value))
	}
	for key, value := range c.cookies {
		options = append(options, requests.CookieOption(key, value))
	}

	err := requests.Do(func(gcli *gout.Client) *dataflow.DataFlow {
		switch method {
		case http.MethodGet:
			return gcli.GET(fullURL).BindHeader(&respHeader)
		default:
			return gcli.POST(fullURL).BindHeader(&respHeader).SetBody(body)
		}
	}, &respBody, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	decodedBody, err := utils.ParseRespBody(*bytes.NewBuffer(respBody), respHeader)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return decodedBody, nil
}

func extractInitialStateJSON(body []byte) ([]byte, error) {
	const marker = "window.__INITIAL_STATE__"
	content := string(body)
	idx := strings.Index(content, marker)
	if idx < 0 {
		return nil, fmt.Errorf("initial state marker not found")
	}
	remainder := content[idx+len(marker):]
	assignIdx := strings.Index(remainder, "=")
	if assignIdx < 0 {
		return nil, fmt.Errorf("initial state assignment not found")
	}
	start := strings.Index(remainder[assignIdx+1:], "{")
	if start < 0 {
		return nil, fmt.Errorf("initial state object start not found")
	}
	start += idx + len(marker) + assignIdx + 1
	end, err := findJSONObjectEnd(content, start)
	if err != nil {
		return nil, err
	}
	return sanitizeInitialStateJSON(content[start:end]), nil
}

func sanitizeInitialStateJSON(raw string) []byte {
	return []byte(undefinedValuePattern.ReplaceAllString(raw, `$1 null$2`))
}

func applyProfileNoteTypesFromHTML(user *UserProfilePageUser, body []byte) {
	if user == nil || len(body) == 0 {
		return
	}
	noteTypes := extractProfileNoteTypes(body)
	if len(noteTypes) == 0 {
		return
	}
	for groupIdx := range user.Notes {
		for noteIdx := range user.Notes[groupIdx] {
			note := &user.Notes[groupIdx][noteIdx]
			noteID := canonicalNoteID(*note)
			if noteID == "" || note.NoteCard == nil {
				continue
			}
			if noteType, ok := noteTypes[noteID]; ok && noteType != "" {
				note.NoteCard.Type = noteType
			}
		}
	}
}

func extractProfileNoteTypes(body []byte) map[string]string {
	matches := noteItemPattern.FindAll(body, -1)
	if len(matches) == 0 {
		return nil
	}

	noteTypes := make(map[string]string, len(matches))
	for _, match := range matches {
		noteIDMatch := noteItemHrefPattern.FindSubmatch(match)
		if len(noteIDMatch) < 2 {
			continue
		}
		noteID := strings.TrimSpace(string(noteIDMatch[1]))
		if noteID == "" {
			continue
		}
		noteType := "normal"
		if bytes.Contains(match, []byte(`class="play-icon"`)) {
			noteType = "video"
		}
		noteTypes[noteID] = noteType
	}
	return noteTypes
}

func findJSONObjectEnd(content string, start int) (int, error) {
	depth := 0
	inString := false
	escaped := false
	for idx := start; idx < len(content); idx++ {
		ch := content[idx]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return idx + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("initial state object end not found")
}

func buildOrderedQueryString(params map[string]string) string {
	preferredOrder := []string{"room_id", "request_user_id", "source", "client_type"}
	var builder strings.Builder
	written := 0
	usedKeys := make(map[string]struct{}, len(params))

	writeKV := func(key string, value string) {
		if written > 0 {
			builder.WriteString("&")
		}
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(url.QueryEscape(value))
		written++
	}

	for _, key := range preferredOrder {
		value, ok := params[key]
		if !ok {
			continue
		}
		writeKV(key, value)
		usedKeys[key] = struct{}{}
	}
	for key, value := range params {
		if _, ok := usedKeys[key]; ok {
			continue
		}
		writeKV(key, value)
	}
	return builder.String()
}

func (c *Client) FindExactUser(keyword string) (*SearchOneboxUser, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("keyword is required")
	}

	resp, err := c.SearchUserOnebox(keyword)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("API returned error: %s", resp.Msg)
	}
	if resp.Data == nil || len(resp.Data.OneboxList) == 0 {
		return nil, fmt.Errorf("user not found")
	}

	var exactIDMatch *SearchOneboxUser
	var exactRedIDMatches []*SearchOneboxUser
	var exactTitleMatches []*SearchOneboxUser
	for _, item := range resp.Data.OneboxList {
		if item.Type != "user" || item.UserOneBox == nil {
			continue
		}
		user := item.UserOneBox
		if user.ID == keyword {
			if exactIDMatch != nil && exactIDMatch.ID != user.ID {
				return nil, fmt.Errorf("multiple exact user id matches for %q", keyword)
			}
			exactIDMatch = user
		}
		if user.RedID == keyword {
			exactRedIDMatches = append(exactRedIDMatches, user)
		}
		if user.Title == keyword {
			exactTitleMatches = append(exactTitleMatches, user)
		}
	}

	if exactIDMatch != nil {
		return exactIDMatch, nil
	}
	if len(exactRedIDMatches) == 1 {
		return exactRedIDMatches[0], nil
	}
	if len(exactRedIDMatches) > 1 {
		return nil, fmt.Errorf("multiple exact red_id matches for %q", keyword)
	}
	if len(exactTitleMatches) == 1 {
		return exactTitleMatches[0], nil
	}
	if len(exactTitleMatches) > 1 {
		return nil, fmt.Errorf("multiple exact nickname matches for %q", keyword)
	}
	return nil, fmt.Errorf("user not found")
}

func generateSearchID(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("invalid search_id length %d", length)
	}

	var builder strings.Builder
	builder.Grow(length)
	max := big.NewInt(int64(len(searchIDAlphabet)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		builder.WriteByte(searchIDAlphabet[n.Int64()])
	}
	return builder.String(), nil
}

func generateRequestID(xT int64) (string, error) {
	max := big.NewInt(9000000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	prefix := n.Int64() + 1000000000
	return fmt.Sprintf("%d-%d", prefix, xT), nil
}

func (u *UserProfilePageUser) FlattenNotes() []UserProfileNote {
	if u == nil {
		return nil
	}
	var notes []UserProfileNote
	for _, group := range u.Notes {
		notes = append(notes, group...)
	}
	return notes
}
