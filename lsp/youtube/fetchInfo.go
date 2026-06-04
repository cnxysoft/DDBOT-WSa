package youtube

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

const (
	// 老的 channelID 订阅
	VideoPathOld  = "https://www.youtube.com/channel/%s/videos?view=57&flow=grid"
	ShortPathOld  = "https://www.youtube.com/channel/%s/shorts?view=57&flow=grid"
	StreamPathOld = "https://www.youtube.com/channel/%s/streams?view=57&flow=grid"
	// 新的 UID 订阅
	VideoPathNew  = "https://www.youtube.com/%s/videos?view=57&flow=grid"
	ShortPathNew  = "https://www.youtube.com/%s/shorts?view=57&flow=grid"
	StreamPathNew = "https://www.youtube.com/%s/streams?view=57&flow=grid"
)

const (
	lockupContentTypeVideo  = "LOCKUP_CONTENT_TYPE_VIDEO"
	lockupContentTypeShorts = "LOCKUP_CONTENT_TYPE_SHORTS"
)

var ErrYoutubeConsentPage = errors.New("youtube consent page returned")
var ErrYoutubeInitialDataNotFound = errors.New("youtube ytInitialData not found")
var scheduledPublishTimePattern = regexp.MustCompile(`\d{4}/\d{1,2}/\d{1,2}\s+\d{1,2}:\d{2}`)
var clockDurationPattern = regexp.MustCompile(`\b\d{1,2}:\d{2}(?::\d{2})?\b`)
var englishLiveIndicatorPattern = regexp.MustCompile(`(?i)\bLIVE(?:\s+NOW)?\b`)
var englishRelativeTimePattern = regexp.MustCompile(`(?i)(\d+)\s*(second|seconds|minute|minutes|hour|hours|day|days|week|weeks|month|months|year|years)\s*ago`)
var cjkRelativeTimePattern = regexp.MustCompile("(\\d+)\\s*(\u79d2\u949f\u524d|\u79d2\u524d|\u5206\u9418\u524d|\u5206\u949f\u524d|\u5206\u524d|\u5c0f\u6642\u524d|\u5c0f\u65f6\u524d|\u5929\u524d|\u65e5\u524d|\u9031\u524d|\u5468\u524d|\u9031\u9593\u524d|\u4e2a\u6708\u524d|\u500b\u6708\u524d|\u304b\u6708\u524d|\u30f6\u6708\u524d|\u6708\u524d|\u5e74\u524d)")

type Searcher struct {
	Sub []*gabs.Container
	l   *list.List
}

type fetchPageType string

const (
	fetchPageTypeVideo  fetchPageType = "video"
	fetchPageTypeStream fetchPageType = "stream"
	fetchPageTypeShorts fetchPageType = "shorts"
)

type fetchPage struct {
	root *gabs.Container
	kind fetchPageType
}

func (r *Searcher) search(key string, j *gabs.Container) {
	if r.l == nil {
		r.l = list.New()
	}
	r.l.PushBack(j)
	for r.l.Len() != 0 {
		head := r.l.Front()
		r.l.Remove(head)
		j := head.Value.(*gabs.Container)
		if len(j.ChildrenMap()) != 0 {
			for k, v := range j.ChildrenMap() {
				if k == key {
					r.Sub = append(r.Sub, v)
					continue
				}
				r.l.PushBack(v)
			}
		} else {
			for _, c := range j.Children() {
				if len(c.ChildrenMap()) != 0 {
					r.l.PushBack(c)
				}
			}
		}
	}
}

func searchAll(root *gabs.Container, key string) []*gabs.Container {
	searcher := new(Searcher)
	searcher.search(key, root)
	return searcher.Sub
}

func extractJSONObjectAfterPrefix(content []byte, prefix string) []byte {
	index := bytes.Index(content, []byte(prefix))
	if index < 0 {
		return nil
	}

	start := bytes.IndexByte(content[index:], '{')
	if start < 0 {
		return nil
	}
	start += index

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(content); i++ {
		ch := content[i]
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
				return content[start : i+1]
			}
		}
	}
	return nil
}

func extractData(content []byte) (*gabs.Container, error) {
	if bytes.Contains(content, []byte("consent.youtube.com")) {
		return nil, ErrYoutubeConsentPage
	}

	for _, prefix := range []string{
		`window["ytInitialData"] = `,
		`var ytInitialData = `,
	} {
		if jsonBody := extractJSONObjectAfterPrefix(content, prefix); len(jsonBody) > 0 {
			return gabs.ParseJSON(jsonBody)
		}
	}

	return nil, ErrYoutubeInitialDataNotFound
}

func YPatch(channelID string, video bool) string {
	var baseURL string
	if strings.HasPrefix(channelID, "@") {
		if video {
			baseURL = VideoPathNew
		} else {
			baseURL = StreamPathNew
		}
	} else {
		if video {
			baseURL = VideoPathOld
		} else {
			baseURL = StreamPathOld
		}
	}
	return fmt.Sprintf(baseURL, channelID)
}

func YShortPatch(channelID string) string {
	if strings.HasPrefix(channelID, "@") {
		return fmt.Sprintf(ShortPathNew, channelID)
	}
	return fmt.Sprintf(ShortPathOld, channelID)
}

func containerString(c *gabs.Container) string {
	if c == nil {
		return ""
	}
	switch v := c.Data().(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return strings.Trim(c.String(), `"`)
	}
}

func readString(c *gabs.Container, path ...string) string {
	if c == nil {
		return ""
	}
	return containerString(c.S(path...))
}

func parseInt64String(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	v, _ := strconv.ParseInt(text, 10, 64)
	return v
}

func containsAnyFold(text string, expects ...string) bool {
	upperText := strings.ToUpper(text)
	for _, expect := range expects {
		if expect == "" {
			continue
		}
		if strings.Contains(upperText, strings.ToUpper(expect)) {
			return true
		}
	}
	return false
}

func anyContainsFold(texts []string, expects ...string) bool {
	for _, text := range texts {
		if containsAnyFold(text, expects...) {
			return true
		}
	}
	return false
}

func containsLiveIndicator(text string) bool {
	if text == "" {
		return false
	}
	if containsAnyFold(text, "LIVE NOW", "\u6b63\u5728\u76f4\u64ad") {
		return true
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "LIVE" || trimmed == "\u76f4\u64ad" || trimmed == "\u30e9\u30a4\u30d6" {
		return true
	}
	return englishLiveIndicatorPattern.MatchString(trimmed)
}

func containsActiveLiveIndicator(text string) bool {
	if text == "" {
		return false
	}
	return containsAnyFold(text, "LIVE NOW", "\u6b63\u5728\u76f4\u64ad")
}

func anyContainsActiveLiveIndicator(texts []string) bool {
	for _, text := range texts {
		if containsActiveLiveIndicator(text) {
			return true
		}
	}
	return false
}

func anyContainsLiveIndicator(texts []string) bool {
	for _, text := range texts {
		if containsLiveIndicator(text) {
			return true
		}
	}
	return false
}

func collectMetadataTexts(content *gabs.Container) []string {
	if content == nil {
		return nil
	}
	var result []string
	for _, row := range content.S("metadataRows").Children() {
		for _, part := range row.S("metadataParts").Children() {
			if text := readString(part, "text", "content"); text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

func collectBadgeTexts(overlays *gabs.Container) []string {
	if overlays == nil {
		return nil
	}
	var result []string
	for _, overlay := range overlays.Children() {
		for _, badge := range overlay.S("thumbnailBottomOverlayViewModel", "badges").Children() {
			if text := readString(badge, "thumbnailBadgeViewModel", "text"); text != "" {
				result = append(result, text)
			}
			if label := readString(badge, "thumbnailBadgeViewModel", "rendererContext", "accessibilityContext", "label"); label != "" {
				result = append(result, label)
			}
		}
	}
	return result
}

func collectBadgeStyles(overlays *gabs.Container) []string {
	if overlays == nil {
		return nil
	}
	var result []string
	for _, overlay := range overlays.Children() {
		for _, badge := range overlay.S("thumbnailBottomOverlayViewModel", "badges").Children() {
			if style := readString(badge, "thumbnailBadgeViewModel", "badgeStyle"); style != "" {
				result = append(result, style)
			}
		}
	}
	return result
}

func collectDurationTexts(overlays *gabs.Container) []string {
	if overlays == nil {
		return nil
	}
	var result []string
	for _, overlay := range overlays.Children() {
		for _, badge := range overlay.S("thumbnailBottomOverlayViewModel", "badges").Children() {
			if readString(badge, "thumbnailBadgeViewModel", "badgeStyle") != "THUMBNAIL_OVERLAY_BADGE_STYLE_DEFAULT" {
				continue
			}
			if text := readString(badge, "thumbnailBadgeViewModel", "text"); text != "" {
				result = append(result, text)
			}
			if label := readString(badge, "thumbnailBadgeViewModel", "rendererContext", "accessibilityContext", "label"); label != "" {
				result = append(result, label)
			}
		}
	}
	return result
}

func selectedTabTitle(root *gabs.Container) string {
	tabs := root.S("contents", "twoColumnBrowseResultsRenderer", "tabs")
	if tabs == nil {
		return ""
	}
	for _, tab := range tabs.Children() {
		for _, item := range []*gabs.Container{tab.S("tabRenderer"), tab.S("expandableTabRenderer")} {
			if item == nil || item.Data() == nil {
				continue
			}
			if selected, ok := item.S("selected").Data().(bool); ok && selected {
				return readString(item, "title")
			}
		}
	}
	return ""
}

func pageMatchesRequestedType(root *gabs.Container, requested fetchPageType) bool {
	switch requested {
	case fetchPageTypeStream:
		title := selectedTabTitle(root)
		if title == "" {
			return false
		}
		return containsAnyFold(title, "LIVE", "STREAM", "直播", "實況", "配信", "ライブ")
	case fetchPageTypeShorts:
		title := selectedTabTitle(root)
		if title == "" {
			return false
		}
		return containsAnyFold(title, "SHORTS", "Shorts", "短视频")
	default:
		return true
	}
}

func collectAttachmentAPIURLs(lockup *gabs.Container) []string {
	var result []string
	for _, apiURL := range searchAll(lockup.S("attachmentSlot"), "apiUrl") {
		if url := containerString(apiURL); url != "" {
			result = append(result, url)
		}
	}
	return result
}

func parseScheduledTimestamp(texts []string) int64 {
	for _, text := range texts {
		matched := scheduledPublishTimePattern.FindString(text)
		if matched == "" {
			continue
		}
		ts, err := time.ParseInLocation("2006/1/2 15:04", matched, time.Local)
		if err == nil {
			return ts.Unix()
		}
	}
	return 0
}

func parseClockDurationSeconds(text string) int64 {
	matched := clockDurationPattern.FindString(text)
	if matched == "" {
		return 0
	}
	parts := strings.Split(matched, ":")
	var total int64
	for _, part := range parts {
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return 0
		}
		total = total*60 + value
	}
	return total
}

func findNumberBeforeUnit(text string, units ...string) int64 {
	bestIndex := -1
	var bestValue int64
	for _, unit := range units {
		idx := strings.Index(strings.ToLower(text), strings.ToLower(unit))
		if idx < 0 {
			continue
		}
		end := idx
		for end > 0 && text[end-1] == ' ' {
			end--
		}
		start := end
		for start > 0 && text[start-1] >= '0' && text[start-1] <= '9' {
			start--
		}
		if start == end {
			continue
		}
		value := parseInt64String(text[start:end])
		if value == 0 {
			continue
		}
		if bestIndex < 0 || idx < bestIndex {
			bestIndex = idx
			bestValue = value
		}
	}
	return bestValue
}

func parseTextDurationSeconds(text string) int64 {
	hours := findNumberBeforeUnit(text, "hours", "hour", "\u5c0f\u65f6", "\u5c0f\u6642", "\u6642\u9593")
	minutes := findNumberBeforeUnit(text, "minutes", "minute", "\u5206\u949f", "\u5206\u9418", "\u5206")
	seconds := findNumberBeforeUnit(text, "seconds", "second", "\u79d2\u949f", "\u79d2")
	if hours == 0 && minutes == 0 && seconds == 0 {
		return 0
	}
	return hours*3600 + minutes*60 + seconds
}

func parseDurationSeconds(texts []string) int64 {
	for _, text := range texts {
		if seconds := parseClockDurationSeconds(text); seconds > 0 {
			return seconds
		}
	}
	for _, text := range texts {
		if seconds := parseTextDurationSeconds(text); seconds > 0 {
			return seconds
		}
	}
	return 0
}

func parseRelativeTimestamp(text string, now time.Time) int64 {
	if matches := englishRelativeTimePattern.FindStringSubmatch(text); len(matches) == 3 {
		value := int(parseInt64String(matches[1]))
		switch strings.ToLower(matches[2]) {
		case "second", "seconds":
			return now.Add(-time.Duration(value) * time.Second).Unix()
		case "minute", "minutes":
			return now.Add(-time.Duration(value) * time.Minute).Unix()
		case "hour", "hours":
			return now.Add(-time.Duration(value) * time.Hour).Unix()
		case "day", "days":
			return now.AddDate(0, 0, -value).Unix()
		case "week", "weeks":
			return now.AddDate(0, 0, -7*value).Unix()
		case "month", "months":
			return now.AddDate(0, -value, 0).Unix()
		case "year", "years":
			return now.AddDate(-value, 0, 0).Unix()
		}
	}

	if matches := cjkRelativeTimePattern.FindStringSubmatch(text); len(matches) == 3 {
		value := int(parseInt64String(matches[1]))
		switch matches[2] {
		case "\u79d2\u949f\u524d", "\u79d2\u524d":
			return now.Add(-time.Duration(value) * time.Second).Unix()
		case "\u5206\u9418\u524d", "\u5206\u949f\u524d", "\u5206\u524d":
			return now.Add(-time.Duration(value) * time.Minute).Unix()
		case "\u5c0f\u6642\u524d", "\u5c0f\u65f6\u524d":
			return now.Add(-time.Duration(value) * time.Hour).Unix()
		case "\u5929\u524d", "\u65e5\u524d":
			return now.AddDate(0, 0, -value).Unix()
		case "\u9031\u524d", "\u5468\u524d", "\u9031\u9593\u524d":
			return now.AddDate(0, 0, -7*value).Unix()
		case "\u4e2a\u6708\u524d", "\u500b\u6708\u524d", "\u304b\u6708\u524d", "\u30f6\u6708\u524d", "\u6708\u524d":
			return now.AddDate(0, -value, 0).Unix()
		case "\u5e74\u524d":
			return now.AddDate(-value, 0, 0).Unix()
		}
	}

	return 0
}

func parseRelativeTimestampFromTexts(texts []string, now time.Time) int64 {
	for _, text := range texts {
		if ts := parseRelativeTimestamp(text, now); ts > 0 {
			return ts
		}
	}
	return 0
}

func pickLargestThumbnail(thumbnails *gabs.Container) string {
	if thumbnails == nil {
		return ""
	}
	bestHeight := int64(-1)
	bestURL := ""
	for _, thumbnail := range thumbnails.Children() {
		height := parseInt64String(readString(thumbnail, "height"))
		if height >= bestHeight {
			bestHeight = height
			bestURL = readString(thumbnail, "url")
		}
	}
	return bestURL
}

func extractChannelName(root *gabs.Container) string {
	for _, info := range searchAll(root, "channelMetadataRenderer") {
		if title := readString(info, "title"); title != "" {
			return title
		}
	}
	return readString(root, "microformat", "microformatDataRenderer", "title")
}

func extractVideoTypeAndStatusFromLegacy(videoJSON *gabs.Container) (VideoType, VideoStatus, int64, bool) {
	label := readString(videoJSON, "thumbnailOverlays", "0",
		"thumbnailOverlayTimeStatusRenderer", "text", "accessibility", "accessibilityData", "label")
	style := readString(videoJSON, "thumbnailOverlays", "0", "thumbnailOverlayTimeStatusRenderer", "style")

	videoType := VideoType_Video
	switch {
	case containsAnyFold(label, "PREMIERE", "\u9996\u64ad", "\u9996\u6620", "\u30d7\u30ec\u30df\u30a2"):
		videoType = VideoType_FirstLive
	case containsLiveIndicator(label):
		videoType = VideoType_Live
	}

	videoStatus := VideoStatus_Upload
	var ts int64
	switch style {
	case "UPCOMING":
		videoStatus = VideoStatus_Waiting
		ts = parseInt64String(readString(videoJSON, "upcomingEventData", "startTime"))
		if videoType == VideoType_Video {
			videoType = VideoType_FirstLive
		}
	case "LIVE":
		videoStatus = VideoStatus_Living
		if videoType == VideoType_Video {
			videoType = VideoType_Live
		}
	case "null":
		return 0, 0, 0, false
	}
	return videoType, videoStatus, ts, true
}

func parseLegacyVideoInfo(videoJSON *gabs.Container, channelID, channelName string) *VideoInfo {
	videoID := readString(videoJSON, "videoId")
	if videoID == "" {
		return nil
	}

	videoType, videoStatus, ts, ok := extractVideoTypeAndStatusFromLegacy(videoJSON)
	if !ok {
		return nil
	}

	title := readString(videoJSON, "title", "simpleText")
	if title == "" {
		var sb strings.Builder
		for _, run := range videoJSON.S("title", "runs").Children() {
			sb.WriteString(readString(run, "text"))
		}
		title = sb.String()
	}

	durationSeconds := parseDurationSeconds([]string{
		readString(videoJSON, "lengthText", "simpleText"),
		readString(videoJSON, "thumbnailOverlays", "0", "thumbnailOverlayTimeStatusRenderer", "text", "simpleText"),
		readString(videoJSON, "thumbnailOverlays", "0", "thumbnailOverlayTimeStatusRenderer", "text", "accessibility", "accessibilityData", "label"),
		readString(videoJSON, "title", "accessibility", "accessibilityData", "label"),
	})

	var publishTimestamp int64
	if videoStatus == VideoStatus_Upload {
		publishTimestamp = parseRelativeTimestampFromTexts([]string{
			readString(videoJSON, "publishedTimeText", "simpleText"),
			readString(videoJSON, "publishedTimeText", "runs", "0", "text"),
		}, time.Now())
	}

	return &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   channelID,
			ChannelName: channelName,
		},
		Cover:            pickLargestThumbnail(videoJSON.S("thumbnail", "thumbnails")),
		VideoId:          videoID,
		VideoTitle:       title,
		VideoType:        videoType,
		VideoStatus:      videoStatus,
		VideoTimestamp:   ts,
		PublishTimestamp: publishTimestamp,
		DurationSeconds:  durationSeconds,
	}
}

func videoInfoQualityScore(info *VideoInfo) int {
	if info == nil {
		return -1
	}

	score := 0
	if info.IsLive() {
		score += 100
	}
	if info.IsWaiting() || info.IsLiving() {
		score += 20
	}
	if info.VideoTimestamp != 0 {
		score += 10
	}
	if info.PublishTimestamp != 0 {
		score += 10
	}
	if info.DurationSeconds != 0 {
		score += 5
	}
	if info.VideoTitle != "" {
		score++
	}
	if info.Cover != "" {
		score++
	}
	if info.ChannelName != "" {
		score++
	}
	if info.HeaderSummary {
		// Channel header live summaries only carry avatar/title-level fallback data.
		// Prefer actual video cards when both refer to the same live stream.
		score -= 10
	}
	return score
}

func mergeVideoInfo(current, candidate *VideoInfo) *VideoInfo {
	if current == nil {
		return candidate
	}
	if candidate == nil {
		return current
	}

	primary := current
	secondary := candidate
	if videoInfoQualityScore(candidate) > videoInfoQualityScore(current) {
		primary = candidate
		secondary = current
	}

	// NOTE: VideoInfo embeds sync.Once and *mmsg.MSG for the lazily-built
	// msgCache. Both must never be copied 鈥?copying sync.Once breaks the
	// once.Do contract, and copying a populated msgCache pointer would
	// alias the cache across instances. Reconstruct explicitly instead.
	merged := &VideoInfo{
		UserInfo:         primary.UserInfo,
		Cover:            primary.Cover,
		VideoId:          primary.VideoId,
		VideoTitle:       primary.VideoTitle,
		VideoType:        primary.VideoType,
		VideoStatus:      primary.VideoStatus,
		VideoTimestamp:   primary.VideoTimestamp,
		PublishTimestamp: primary.PublishTimestamp,
		DurationSeconds:  primary.DurationSeconds,
		GroupCode:        primary.GroupCode,
		HeaderSummary:    primary.HeaderSummary,
	}
	if merged.ChannelId == "" {
		merged.ChannelId = secondary.ChannelId
	}
	if merged.ChannelName == "" {
		merged.ChannelName = secondary.ChannelName
	}
	if merged.Cover == "" {
		merged.Cover = secondary.Cover
	}
	if merged.VideoTitle == "" {
		merged.VideoTitle = secondary.VideoTitle
	}
	if merged.VideoTimestamp == 0 {
		merged.VideoTimestamp = secondary.VideoTimestamp
	}
	if merged.PublishTimestamp == 0 {
		merged.PublishTimestamp = secondary.PublishTimestamp
	}
	if merged.DurationSeconds == 0 {
		merged.DurationSeconds = secondary.DurationSeconds
	}
	if merged.VideoType == VideoType_Video && secondary.IsLive() {
		merged.VideoType = secondary.VideoType
		merged.VideoStatus = secondary.VideoStatus
	}
	if merged.VideoStatus == VideoStatus_Upload && secondary.VideoStatus != VideoStatus_Upload {
		merged.VideoStatus = secondary.VideoStatus
	}

	return merged
}

func extractVideoTypeAndStatusFromLockup(lockup *gabs.Container, pageType fetchPageType, durationSeconds int64) (VideoType, VideoStatus, int64) {
	metadataTexts := collectMetadataTexts(lockup.S("metadata", "lockupMetadataViewModel", "metadata", "contentMetadataViewModel"))
	badgeTexts := collectBadgeTexts(lockup.S("contentImage", "thumbnailViewModel", "overlays"))
	badgeStyles := collectBadgeStyles(lockup.S("contentImage", "thumbnailViewModel", "overlays"))
	attachmentAPIURLs := collectAttachmentAPIURLs(lockup)
	accessibilityLabel := readString(lockup, "rendererContext", "accessibilityContext", "label")
	scheduledTimestamp := parseScheduledTimestamp(metadataTexts)

	allTexts := append([]string{accessibilityLabel}, metadataTexts...)
	allTexts = append(allTexts, badgeTexts...)

	switch {
	case pageType == fetchPageTypeStream && durationSeconds > 0 &&
		!anyContainsActiveLiveIndicator(allTexts) &&
		(anyContainsFold(badgeStyles, "LIVE") || anyContainsLiveIndicator(allTexts)):
		// Stream replays can retain LIVE badges after ending; a fixed duration is a
		// stronger signal that this card is a replay instead of an active live.
		return VideoType_Video, VideoStatus_Upload, 0
	case anyContainsFold(badgeStyles, "LIVE") || anyContainsLiveIndicator(allTexts):
		return VideoType_Live, VideoStatus_Living, 0
	case anyContainsFold(attachmentAPIURLs,
		"/youtubei/v1/notification/add_upcoming_event_reminder",
		"/youtubei/v1/notification/remove_upcoming_event_reminder"),
		anyContainsFold(badgeStyles, "UPCOMING"),
		anyContainsFold(allTexts, "\u5373\u5c06\u5f00\u59cb", "\u9884\u5b9a\u53d1\u5e03\u65f6\u95f4", "\u9884\u7ea6", "\u9996\u64ad", "\u9996\u6620", "PREMIERE", "UPCOMING"):
		return VideoType_FirstLive, VideoStatus_Waiting, scheduledTimestamp
	case anyContainsFold(metadataTexts, "\u76f4\u64ad\u65f6\u95f4", "streamed", "Streamed"):
		return VideoType_Video, VideoStatus_Upload, 0
	case pageType == fetchPageTypeStream:
		return VideoType_Video, VideoStatus_Upload, 0
	default:
		return VideoType_Video, VideoStatus_Upload, 0
	}
}

func parseLockupVideoInfo(lockup *gabs.Container, pageType fetchPageType, channelID, channelName string) *VideoInfo {
	contentType := readString(lockup, "contentType")
	if contentType != "" && contentType != lockupContentTypeVideo && contentType != lockupContentTypeShorts {
		return nil
	}

	videoID := readString(lockup, "contentId")
	if videoID == "" {
		videoID = readString(lockup, "rendererContext", "commandContext", "onTap", "innertubeCommand", "watchEndpoint", "videoId")
	}
	if videoID == "" {
		return nil
	}

	title := readString(lockup, "metadata", "lockupMetadataViewModel", "title", "content")
	metadataTexts := collectMetadataTexts(lockup.S("metadata", "lockupMetadataViewModel", "metadata", "contentMetadataViewModel"))
	durationTexts := collectDurationTexts(lockup.S("contentImage", "thumbnailViewModel", "overlays"))
	durationSeconds := parseDurationSeconds(durationTexts)

	// Detect shorts based on content type
	isShorts := contentType == lockupContentTypeShorts

	videoType, videoStatus, ts := extractVideoTypeAndStatusFromLockup(lockup, pageType, durationSeconds)
	if isShorts {
		videoType = VideoType_Shorts
	}

	var publishTimestamp int64
	if videoStatus == VideoStatus_Upload {
		publishTimestamp = parseRelativeTimestampFromTexts(metadataTexts, time.Now())
	}

	return &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   channelID,
			ChannelName: channelName,
		},
		Cover:            pickLargestThumbnail(lockup.S("contentImage", "thumbnailViewModel", "image", "sources")),
		VideoId:          videoID,
		VideoTitle:       title,
		VideoType:        videoType,
		VideoStatus:      videoStatus,
		VideoTimestamp:   ts,
		PublishTimestamp: publishTimestamp,
		DurationSeconds:  durationSeconds,
	}
}

func parseShortsLockupVideoInfo(shortsLockup *gabs.Container, channelID, channelName string) *VideoInfo {
	// Get videoId from reelWatchEndpoint
	videoID := readString(shortsLockup, "onTap", "innertubeCommand", "reelWatchEndpoint", "videoId")
	if videoID == "" {
		return nil
	}

	// Get title from accessibilityText (format: "title #shorts #tags, views次观看")
	accessibilityText := readString(shortsLockup, "accessibilityText")
	title := extractShortsTitle(accessibilityText)

	// Get cover/thumbnail
	cover := readString(shortsLockup, "onTap", "innertubeCommand", "reelWatchEndpoint", "thumbnail", "thumbnails", "0", "url")

	// Get publish timestamp. Try several sources in order of reliability:
	//   1. explicit publishedTimeText fields on the lockup
	//   2. relative time inside accessibilityText
	//   3. fallback to "now" so downstream templates / filterCard always see a
	//      sensible timestamp instead of 0.
	// accessibilityText rarely contains a parseable relative time on its own
	// (it's usually "title #shorts #tags, N views" with no time phrase), so
	// step 1 / 3 are what keep the field non-zero in practice.
	now := time.Now()
	publishTimestamp := parseRelativeTimestampFromTexts([]string{
		readString(shortsLockup, "publishedTimeText", "simpleText"),
		readString(shortsLockup, "publishedTimeText", "runs", "0", "text"),
		readString(shortsLockup, "metadata", "lockupMetadataViewModel", "metadata", "contentMetadataViewModel", "metadataRows", "0", "metadataParts", "0", "text"),
		readString(shortsLockup, "metadata", "lockupMetadataViewModel", "metadata", "contentMetadataViewModel", "metadataRows", "0", "metadataParts", "1", "text"),
		accessibilityText,
	}, now)
	if publishTimestamp == 0 {
		publishTimestamp = now.Unix()
	}

	return &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   channelID,
			ChannelName: channelName,
		},
		Cover:            cover,
		VideoId:          videoID,
		VideoTitle:       title,
		VideoType:        VideoType_Shorts,
		VideoStatus:      VideoStatus_Upload,
		VideoTimestamp:   0,
		PublishTimestamp: publishTimestamp,
		DurationSeconds:  0,
	}
}

func extractShortsTitle(accessibilityText string) string {
	if accessibilityText == "" {
		return ""
	}
	// Format is like "忍得住这种酥麻感吗？♡ #shorts #asmr, 7,690次观看 - 播放 Shorts 短视频"
	// We want everything before the first #shorts or #tag
	if idx := strings.Index(accessibilityText, " #"); idx > 0 {
		return strings.TrimSpace(accessibilityText[:idx])
	}
	// Also check for comma before view count
	if idx := strings.Index(accessibilityText, ","); idx > 0 {
		return strings.TrimSpace(accessibilityText[:idx])
	}
	return accessibilityText
}

func parseHeaderLiveInfo(root *gabs.Container, channelID, channelName string) *VideoInfo {
	header := root.S("header", "pageHeaderRenderer", "content", "pageHeaderViewModel")
	if header == nil || header.Data() == nil {
		return nil
	}

	liveBadgeText := readString(header, "image", "decoratedAvatarViewModel", "liveData", "liveBadgeText")
	accessibilityLabel := readString(header, "image", "decoratedAvatarViewModel", "rendererContext", "accessibilityContext", "label")
	if !containsAnyFold(liveBadgeText, "LIVE", "\u76f4\u64ad", "\u30e9\u30a4\u30d6") &&
		!containsAnyFold(accessibilityLabel, "\u76f4\u64ad", "LIVE") {
		return nil
	}

	videoID := readString(header, "image", "decoratedAvatarViewModel", "rendererContext", "commandContext", "onTap", "innertubeCommand", "watchEndpoint", "videoId")
	if videoID == "" {
		return nil
	}

	title := readString(header, "title", "dynamicTextViewModel", "text", "content")
	if title == "" {
		title = readString(root, "header", "pageHeaderRenderer", "pageTitle")
	}
	if title == "" {
		title = channelName
	}

	if channelName == "" {
		channelName = title
	}

	return &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   channelID,
			ChannelName: channelName,
		},
		Cover:          pickLargestThumbnail(header.S("image", "decoratedAvatarViewModel", "avatar", "avatarViewModel", "image", "sources")),
		VideoId:        videoID,
		VideoTitle:     title,
		VideoType:      VideoType_Live,
		VideoStatus:    VideoStatus_Living,
		VideoTimestamp: 0,
		HeaderSummary:  true,
	}
}

func mergeVideoInfos(videoInfos []*VideoInfo) []*VideoInfo {
	indexByID := make(map[string]int)
	merged := make([]*VideoInfo, 0, len(videoInfos))
	for _, info := range videoInfos {
		if info == nil || info.VideoId == "" {
			continue
		}
		if idx, ok := indexByID[info.VideoId]; ok {
			merged[idx] = mergeVideoInfo(merged[idx], info)
			continue
		}
		indexByID[info.VideoId] = len(merged)
		merged = append(merged, info)
	}
	return merged
}

func extractVideoInfos(root *gabs.Container, pageType fetchPageType, channelID, channelName string) []*VideoInfo {
	var result []*VideoInfo

	for _, videoJSON := range searchAll(root, "gridVideoRenderer") {
		if info := parseLegacyVideoInfo(videoJSON, channelID, channelName); info != nil {
			result = append(result, info)
		}
	}
	for _, videoJSON := range searchAll(root, "videoRenderer") {
		if info := parseLegacyVideoInfo(videoJSON, channelID, channelName); info != nil {
			result = append(result, info)
		}
	}
	for _, lockup := range searchAll(root, "lockupViewModel") {
		if info := parseLockupVideoInfo(lockup, pageType, channelID, channelName); info != nil {
			result = append(result, info)
		}
	}
	for _, shortsLockup := range searchAll(root, "shortsLockupViewModel") {
		if info := parseShortsLockupVideoInfo(shortsLockup, channelID, channelName); info != nil {
			result = append(result, info)
		}
	}
	if info := parseHeaderLiveInfo(root, channelID, channelName); info != nil {
		result = append(result, info)
	}

	return result
}

// XFetchInfo very sb
func XFetchInfo(channelID string) ([]*VideoInfo, error) {
	log := logger.WithField("channel_id", channelID)
	st := time.Now()
	defer func() {
		ed := time.Now()
		log.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()

	var opts = []requests.Option{
		requests.HeaderOption("accept-language", "zh-CN"),
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.TimeoutOption(time.Second * 10),
		requests.RetryOption(3),
	}
	var pages []fetchPage

	{
		path := YPatch(channelID, true)
		body := new(bytes.Buffer)
		if err := requests.Get(path, nil, body, opts...); err != nil {
			return nil, err
		}
		if root, err := extractData(body.Bytes()); err == nil {
			pages = append(pages, fetchPage{root: root, kind: fetchPageTypeVideo})
		}
	}

	// Random delay between page fetches to avoid bot detection
	time.Sleep(time.Duration(100+rand.Intn(200)) * time.Millisecond)

	{
		path := YShortPatch(channelID)
		body := new(bytes.Buffer)
		if err := requests.Get(path, nil, body, opts...); err != nil {
			return nil, err
		}
		if root, err := extractData(body.Bytes()); err == nil {
			if pageMatchesRequestedType(root, fetchPageTypeShorts) {
				pages = append(pages, fetchPage{root: root, kind: fetchPageTypeShorts})
			} else {
				log.WithField("selected_tab", selectedTabTitle(root)).Debug("skip youtube shorts page because selected tab is not shorts")
			}
		}
	}

	// Random delay between page fetches to avoid bot detection
	time.Sleep(time.Duration(100+rand.Intn(200)) * time.Millisecond)

	{
		path := YPatch(channelID, false)
		body := new(bytes.Buffer)
		if err := requests.Get(path, nil, body, opts...); err != nil {
			return nil, err
		}
		if root, err := extractData(body.Bytes()); err == nil {
			if pageMatchesRequestedType(root, fetchPageTypeStream) {
				pages = append(pages, fetchPage{root: root, kind: fetchPageTypeStream})
			} else {
				log.WithField("selected_tab", selectedTabTitle(root)).Debug("skip youtube stream page because selected tab is not stream")
			}
		}
	}

	channelName := "<nil>"
	for _, page := range pages {
		if name := extractChannelName(page.root); name != "" {
			channelName = name
			break
		}
	}

	var videoInfos []*VideoInfo
	for _, page := range pages {
		videoInfos = append(videoInfos, extractVideoInfos(page.root, page.kind, channelID, channelName)...)
	}
	videoInfos = mergeVideoInfos(videoInfos)

	log.WithField("video_count", len(videoInfos)).Tracef("fetch info")
	return videoInfos, nil
}
