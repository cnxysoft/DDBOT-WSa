package twitter

import (
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func mirrorText(selection *goquery.Selection) string {
	copy := selection.Clone()
	copy.Find("br").ReplaceWithHtml("\n")
	return strings.TrimSpace(copy.Text())
}

// Parse both upstream Nitter and NitterTS attachments, including quoted media.
func parseMirrorMedia(attachments *goquery.Selection) []*Media {
	var media []*Media
	attachments.Find("img, video").Each(func(_ int, element *goquery.Selection) {
		if goquery.NodeName(element) == "img" {
			// Some instances include a poster image next to a playable video.
			container := element.Closest(".gallery-video, .video-container, .gif")
			if container.Find("video[data-url], video[src], video source[src]").Length() > 0 {
				return
			}
			if src := element.AttrOr("src", ""); src != "" {
				media = append(media, &Media{Type: "image", Url: src})
			}
			return
		}
		src := element.AttrOr("data-url", "")
		hls := src != ""
		if src == "" {
			src = element.AttrOr("src", "")
		}
		if src == "" {
			src = element.Find("source[src]").First().AttrOr("src", "")
		}
		if src == "" {
			return
		}
		kind := "video"
		_, loop := element.Attr("loop")
		switch {
		case element.Is(".gif") || element.Closest(".gif, .media-gif").Length() > 0 || loop:
			kind = "gif"
		case hls || strings.Contains(strings.ToLower(src), ".m3u8"):
			kind = "video(m3u8)"
		}
		media = append(media, &Media{Type: kind, Url: src})
	})
	return media
}

func mirrorAuthor(header *goquery.Selection) *UserProfile {
	screenName := strings.TrimPrefix(strings.TrimSpace(header.Find(".username").First().Text()), "@")
	name := strings.TrimSpace(header.Find(".fullname").First().Text())
	if name == "" {
		name = screenName
	}
	return &UserProfile{ScreenName: screenName, Name: name, Website: XUrl + "/" + screenName}
}

func mirrorTweetURL(link string) string {
	ref, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return XUrl + ref.Path
}
