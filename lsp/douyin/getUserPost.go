package douyin

import (
	"bytes"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"google.golang.org/protobuf/encoding/protojson"
)

const PathGetUserPosts = "/aweme/v1/web/aweme/post/"

func GetPosts(id string) (*UserPostsResponse, error) {
	Url := DPath(PathGetUserPosts)
	param := map[string]string{
		"aid":         "6383",
		"sec_user_id": id,
		"count":       "10",
	}
	opts := SetRequestOptions()
	opts = append(opts,
		requests.HeaderOption("referer", DPath(PathGetUserInfo)+id),
	)
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	if err := requests.GetWithHeader(Url, param, &resp, &respHeaders, opts...); err != nil {
		logger.WithField("userId", id).Errorf("获取用户作品列表失败：%v", err)
		return nil, err
	}

	// 解压缩HTML
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		logger.WithField("userId", id).Errorf("解压缩HTML失败：%v", err)
		return nil, err
	}

	posts := new(UserPostsResponse)
	protoJsonOpts := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	err = protoJsonOpts.Unmarshal(body, posts)
	if err != nil || posts.StatusCode != 0 {
		logger.WithField("userId", id).Errorf("解析用户作品列表失败：%v", err)
		return nil, err
	}
	return posts, nil
}
