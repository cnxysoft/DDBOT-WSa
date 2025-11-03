package douyin

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/PuerkitoBio/goquery"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/mitchellh/mapstructure"
	"strings"
)

const PathGetUserInfo = "/user/"

type UserInfoResp struct {
	User struct {
		StatusCode int         `json:"statusCode"`
		StatusMsg  interface{} `json:"statusMsg"`
		LogPb      struct {
			ImprId string `json:"impr_id"`
		} `json:"logPb"`
		User struct {
			Uid                        string      `json:"uid"`
			SecUid                     string      `json:"secUid"`
			ShortId                    string      `json:"shortId"`
			RealName                   string      `json:"realName"`
			RemarkName                 string      `json:"remarkName"`
			Nickname                   string      `json:"nickname"`
			Desc                       string      `json:"desc"`
			DescExtra                  any         `json:"descExtra"`
			Gender                     interface{} `json:"gender"`
			AvatarUrl                  string      `json:"avatarUrl"`
			Avatar300Url               string      `json:"avatar300Url"`
			FollowStatus               int         `json:"followStatus"`
			FollowerStatus             int         `json:"followerStatus"`
			AwemeCount                 int         `json:"awemeCount"`
			FollowingCount             int         `json:"followingCount"`
			FollowerCount              int         `json:"followerCount"`
			FollowerCountStr           string      `json:"followerCountStr"`
			MplatformFollowersCount    int         `json:"mplatformFollowersCount"`
			MplatformFollowersCountStr string      `json:"mplatformFollowersCountStr"`
			FavoritingCount            int         `json:"favoritingCount"`
			WatchLaterCount            int         `json:"watchLaterCount"`
			TotalFavorited             int         `json:"totalFavorited"`
			TotalFavoritedStr          string      `json:"totalFavoritedStr"`
			HideTotalFavorited         string      `json:"hideTotalFavorited"`
			UserCollectCount           struct {
				LogPb            string `json:"logPb"`
				CollectCountList string `json:"collectCountList"`
				StatusCode       string `json:"statusCode"`
				Extra            string `json:"extra"`
			} `json:"userCollectCount"`
			UniqueId          string `json:"uniqueId"`
			CustomVerify      string `json:"customVerify"`
			GeneralPermission struct {
				FansPageToast              int  `json:"fans_page_toast"`
				FollowingFollowerListToast int  `json:"following_follower_list_toast"`
				IsHitActiveFansGrayed      bool `json:"is_hit_active_fans_grayed"`
			} `json:"generalPermission"`
			PunishRemindInfo       string      `json:"punishRemindInfo"`
			Age                    interface{} `json:"age"`
			Birthday               string      `json:"birthday"`
			Country                interface{} `json:"country"`
			Province               interface{} `json:"province"`
			City                   interface{} `json:"city"`
			District               interface{} `json:"district"`
			School                 interface{} `json:"school"`
			SchoolVisible          string      `json:"schoolVisible"`
			EnterpriseVerifyReason string      `json:"enterpriseVerifyReason"`
			Secret                 int         `json:"secret"`
			UserCanceled           bool        `json:"userCanceled"`
			RoomData               struct {
				Status    int `json:"status"`
				UserCount int `json:"user_count"`
				StreamUrl struct {
					DefaultResolution string `json:"default_resolution"`
					Extra             struct {
						Height int `json:"height"`
						Width  int `json:"width"`
					} `json:"extra"`
					FlvPullUrl struct {
						FULLHD1 string `json:"FULL_HD1"`
						HD1     string `json:"HD1"`
						SD1     string `json:"SD1"`
						SD2     string `json:"SD2"`
					} `json:"flv_pull_url"`
					CandidateResolution []string `json:"candidate_resolution"`
					FlvPullUrlParams    struct {
						HD1 string `json:"HD1"`
						SD1 string `json:"SD1"`
						SD2 string `json:"SD2"`
					} `json:"flv_pull_url_params"`
					LiveCoreSdkData struct {
						PullData struct {
							StreamData string `json:"stream_data"`
							Options    struct {
								DefaultQuality struct {
									Name   string `json:"name"`
									SdkKey string `json:"sdk_key"`
								} `json:"default_quality"`
								Qualities []struct {
									Name       string `json:"name"`
									SdkKey     string `json:"sdk_key"`
									VCodec     string `json:"v_codec"`
									Resolution string `json:"resolution"`
									Level      int    `json:"level"`
									VBitRate   int    `json:"v_bit_rate"`
									Fps        int    `json:"fps,omitempty"`
								} `json:"qualities"`
							} `json:"options"`
						} `json:"pull_data"`
					} `json:"live_core_sdk_data"`
					StreamOrientation int `json:"stream_orientation"`
					Play              struct {
						Horizontal string `json:"horizontal"`
						Vertical   string `json:"vertical"`
					} `json:"play"`
				} `json:"stream_url"`
				Owner struct {
					WebRid string `json:"web_rid"`
				} `json:"owner"`
				LiveTypeNormal bool `json:"live_type_normal"`
				PaidLiveData   struct {
					PayAbType int `json:"pay_ab_type"`
				} `json:"paid_live_data"`
				EcomData struct {
					RoomCartV2 struct {
						ShowCart int `json:"show_cart"`
					} `json:"room_cart_v2"`
				} `json:"ecom_data"`
				PackMeta struct {
					Scene   string `json:"scene"`
					Env     string `json:"env"`
					Dc      string `json:"dc"`
					Cluster string `json:"cluster"`
				} `json:"pack_meta"`
			} `json:"roomData"`
			ShareQrcodeUrl string `json:"shareQrcodeUrl"`
			ShareInfo      struct {
				BoolPersist   int    `json:"boolPersist"`
				ShareDesc     string `json:"shareDesc"`
				ShareImageUrl struct {
					Uri     string   `json:"uri"`
					UrlList []string `json:"url_list"`
				} `json:"shareImageUrl"`
				ShareQrcodeUrl struct {
					Uri     string        `json:"uri"`
					UrlList []interface{} `json:"url_list"`
				} `json:"shareQrcodeUrl"`
				ShareUrl       string `json:"shareUrl"`
				ShareWeiboDesc string `json:"shareWeiboDesc"`
			} `json:"shareInfo"`
			CoverAndHeadImageInfo struct {
				ProfileCoverList []struct {
					CoverUrl struct {
						Uri     string   `json:"uri"`
						UrlList []string `json:"urlList"`
					} `json:"coverUrl"`
					DarkCoverColor  string `json:"darkCoverColor"`
					LightCoverColor string `json:"lightCoverColor"`
				} `json:"profileCoverList"`
			} `json:"coverAndHeadImageInfo"`
			RoomId                       int64       `json:"roomId"`
			IsBlocked                    bool        `json:"isBlocked"`
			IsBlock                      bool        `json:"isBlock"`
			IsBan                        bool        `json:"isBan"`
			FavoritePermission           int         `json:"favoritePermission"`
			ShowFavoriteList             bool        `json:"showFavoriteList"`
			ViewHistoryPermission        bool        `json:"viewHistoryPermission"`
			IpLocation                   string      `json:"ipLocation"`
			IsNotShowBaseTag             string      `json:"isNotShowBaseTag"`
			IsGovMediaVip                bool        `json:"isGovMediaVip"`
			IsStar                       bool        `json:"isStar"`
			HideLocation                 string      `json:"hideLocation"`
			NeedSpecialShowFollowerCount bool        `json:"needSpecialShowFollowerCount"`
			IsNotShow                    bool        `json:"isNotShow"`
			AvatarAuditing               string      `json:"avatarAuditing"`
			ContinuationState            int         `json:"continuationState"`
			ImRoleIds                    interface{} `json:"im_role_ids"`
			RoomIdStr                    string      `json:"roomIdStr"`
			CloseConsecutiveChat         string      `json:"close_consecutive_chat"`
			AccountCertInfo              struct {
				LabelStyle   any    `json:"labelStyle"`
				LabelText    string `json:"labelText"`
				IsBizAccount any    `json:"isBizAccount"`
			} `json:"accountCertInfo"`
			ProfileRecordParams string      `json:"profileRecordParams"`
			ProfileRankLabel    interface{} `json:"profileRankLabel"`
			ProfileTabInfo      struct {
				ProfileLandingTab int           `json:"profile_landing_tab"`
				ProfileTabList    interface{}   `json:"profile_tab_list"`
				ProfileTabListV2  []interface{} `json:"profile_tab_list_v2"`
			} `json:"profileTabInfo"`
			IsOverFollower bool `json:"isOverFollower"`
		} `json:"user"`
	} `json:"user"`
	StatusCode    int         `json:"statusCode"`
	Mix           interface{} `json:"mix"`
	Series        interface{} `json:"series"`
	Post          interface{} `json:"post"`
	Uid           string      `json:"uid"`
	IsHideImpInfo bool        `json:"isHideImpInfo"`
	IsClient      bool        `json:"isClient"`
	OsInfo        struct {
		Os      string `json:"os"`
		Version string `json:"version"`
		IsMas   bool   `json:"isMas"`
	} `json:"osInfo"`
	IsSpider     bool   `json:"isSpider"`
	RedirectFrom string `json:"redirectFrom"`
}

func GetUserInfo(uid string) (*UserInfo, error) {
	Url := DPath(PathGetUserInfo) + uid
	opts := SetRequestOptions()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	if err := requests.GetWithHeader(Url, nil, &resp, &respHeaders, opts...); err != nil {
		logger.Errorf("查找用户失败：%v", err)
		return nil, err
	}

	// 解压缩HTML
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		logger.WithField("User", uid).Errorf("解压缩HTML失败：%v", err)
		return nil, err
	}

	// 解析用户信息
	profile, err := ParseUserInfoResp(body)
	if err != nil {
		return nil, err
	} else if profile == nil {
		return nil, errors.New("用户不存在或返回结果为空")
	}
	return profile, nil
}

func ParseUserInfoResp(body []byte) (*UserInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// 查找 <body> 标签并判断 onload 属性
	onload, exists := doc.Find("body").Attr("onload")
	if exists && onload == "readygo()" {
		return nil, errors.New("检测到人机验证，请稍后再试或尝试手动完成验证")
	}

	var userInfo *UserInfo
	doc.Find("body > script[crossorigin=anonymous]").Each(func(i int, s *goquery.Selection) {
		if userInfo != nil {
			return
		}

		str := s.Text()
		if !strings.Contains(str, `\"realName\"`) {
			return
		}

		// 提取 self.__pace_f.push 的参数部分
		start := strings.Index(str, "self.__pace_f.push(")
		if start == -1 {
			return
		}
		start += len("self.__pace_f.push(")

		// 找到匹配的括号对
		bracketCount := 0
		end := -1
		for i := start; i < len(str); i++ {
			if str[i] == '[' {
				bracketCount++
			} else if str[i] == ']' {
				bracketCount--
				if bracketCount == 0 {
					end = i + 1
					break
				}
			}
		}

		if end == -1 {
			return
		}

		jsonData := str[start:end]

		// 解析外层数组
		var outerArr []interface{}
		if err := json.Unmarshal([]byte(jsonData), &outerArr); err != nil {
			return
		}

		if len(outerArr) < 2 {
			return
		}

		// 处理内层字符串数据
		innerStr, ok := outerArr[1].(string)
		if !ok || len(innerStr) < 3 || (len(innerStr) > 1 && !strings.HasPrefix(innerStr[1:], ":[\"")) {
			return
		}

		// 解析内层JSON数组
		var innerArr []interface{}
		if err := json.Unmarshal([]byte(innerStr[2:]), &innerArr); err != nil {
			return
		}

		if len(innerArr) < 4 {
			return
		}

		// 关键修正：使用 mapstructure 进行深度解析
		var usrInfo UserInfoResp
		config := &mapstructure.DecoderConfig{
			TagName: "json", // 明确使用 json 标签
			Result:  &usrInfo,
			// 处理可能的大小写不一致问题
			MatchName: func(mapKey, fieldName string) bool {
				return strings.EqualFold(mapKey, fieldName)
			},
		}

		decoder, err := mapstructure.NewDecoder(config)
		if err != nil {
			return
		}

		if err := decoder.Decode(innerArr[3]); err != nil {
			return
		}

		// 验证必要字段
		if usrInfo.User.User.Uid == "" {
			return
		}

		userInfo = &UserInfo{
			Uid:       usrInfo.User.User.Uid,
			SecUid:    usrInfo.User.User.SecUid,
			NikeName:  usrInfo.User.User.Nickname,
			RealName:  usrInfo.User.User.RealName,
			Desc:      usrInfo.User.User.Desc,
			WebRoomId: usrInfo.User.User.RoomData.Owner.WebRid,
		}
		return
	})

	if userInfo == nil {
		return nil, errors.New("未找到有效的用户信息")
	}
	return userInfo, nil
}
