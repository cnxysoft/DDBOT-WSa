package template

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/interfaces"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/shopspring/decimal"
)

var funcsExt = make(FuncMap)

// RegisterExtFunc 在init阶段插入额外的template函数
func RegisterExtFunc(name string, fn interface{}) {
	checkValueFuncs(name, fn)
	funcsExt[name] = fn
}

func memberList(groupCode int64) []map[string]interface{} {
	var result []map[string]interface{}
	gi := localutils.GetBot().FindGroup(groupCode)
	if gi == nil {
		return result
	}
	for _, m := range gi.Members {
		result = append(result, memberInfo(groupCode, m.Uin))
	}
	return result
}

func memberInfo(groupCode int64, uin int64) map[string]interface{} {
	var result = make(map[string]interface{})
	gi := localutils.GetBot().FindGroup(groupCode)
	if gi == nil {
		return result
	}
	fi := gi.FindMember(uin)
	if fi == nil {
		return result
	}
	result["uin"] = uin
	result["name"] = fi.DisplayName()
	switch fi.Permission {
	case client.Owner:
		// 群主
		result["permission"] = 10
	case client.Administrator:
		// 管理员
		result["permission"] = 5
	default:
		// 其他
		result["permission"] = 1
	}
	switch fi.Gender {
	case 0:
		// 男
		result["gender"] = 2
	case 1:
		// 女
		result["gender"] = 1
	default:
		// 未知
		result["gender"] = 0
	}
	return result
}

func cut() *mmsg.CutElement {
	return new(mmsg.CutElement)
}

func prefix(commandName ...string) string {
	if len(commandName) == 0 {
		return cfg.GetCommandPrefix()
	} else {
		return cfg.GetCommandPrefix(commandName[0]) + commandName[0]
	}
}

func reply(msg interface{}) *message.ReplyElement {
	if msg == nil {
		return nil
	}
	switch e := msg.(type) {
	case *message.GroupMessage:
		return message.NewReply(e)
	case *message.PrivateMessage:
		return message.NewPrivateReply(e)
	default:
		panic(fmt.Sprintf("unknown reply message %v", msg))
	}
}

func at(uin int64) *mmsg.AtElement {
	return mmsg.NewAt(uin)
}

// poke 戳一戳
func poke(uin int64) *mmsg.PokeElement {
	return mmsg.NewPoke(uin)
}

func botUin() int64 {
	return localutils.GetBot().GetUin()
}

func isAdmin(uin int64, groupCode ...int64) bool {
	key := localdb.Key("Permission", uin, "Admin")
	ret := localdb.Exist(key)
	if !ret && len(groupCode) > 0 {
		key = localdb.Key("GroupPermission", groupCode[0], uin, "GroupAdmin")
		ret = localdb.Exist(key)
	}
	return ret
}

func delAcct(uin int64, groupCode int64) bool {
	key := localdb.Key("Score", groupCode, uin)
	_, err := localdb.Delete(key)
	if err != nil {
		logger.Errorf("del Account error %v", err)
		return false
	}
	return true
}

func setScore(uin int64, groupCode int64, num int64) int64 {
	// date := time.Now().Format("20060102")

	if num < 0 {
		logger.Error("template: set score num must be positive")
		return -1
	}

	var score int64
	err := localdb.RWCover(func() error {
		var err error
		scoreKey := localdb.Key("Score", groupCode, uin)
		// dateMarker := localdb.Key("ScoreDate", groupCode, uin, date)

		score, err = localdb.GetInt64(scoreKey, localdb.IgnoreNotFoundOpt())
		if err != nil {
			return err
		}
		// if localdb.Exist(dateMarker) {
		// 	logger = logger.WithField("current_score", score)
		// 	return nil
		// }

		err = localdb.SetInt64(scoreKey, num)
		if err != nil {
			return err
		}

		score, err = localdb.GetInt64(scoreKey, localdb.IgnoreNotFoundOpt())
		if err != nil {
			return err
		}

		// err = localdb.Set(dateMarker, "", localdb.SetExpireOpt(time.Hour*24*3))
		// if err != nil {
		// 	return err
		// }
		logger = logger.WithField("new_score", score)
		return nil
	})
	if err != nil {
		logger.Errorf("add score error %v", err)
		return -1
	}
	return score
}

func addScore(uin int64, groupCode int64, num int64) int64 {
	// date := time.Now().Format("20060102")

	if num <= 0 {
		logger.Error("template: add score num must be positive")
		return -1
	}

	var score int64
	err := localdb.RWCover(func() error {
		var err error
		scoreKey := localdb.Key("Score", groupCode, uin)
		// dateMarker := localdb.Key("ScoreDate", groupCode, uin, date)

		score, err = localdb.GetInt64(scoreKey, localdb.IgnoreNotFoundOpt())
		if err != nil {
			return err
		}
		// if localdb.Exist(dateMarker) {
		// 	logger = logger.WithField("current_score", score)
		// 	return nil
		// }

		score, err = localdb.IncInt64(scoreKey, num)
		if err != nil {
			return err
		}

		// err = localdb.Set(dateMarker, "", localdb.SetExpireOpt(time.Hour*24*3))
		// if err != nil {
		// 	return err
		// }
		logger = logger.WithField("new_score", score)
		return nil
	})
	if err != nil {
		logger.Errorf("add score error %v", err)
		return -1
	}
	return score
}

func subScore(uin int64, groupCode int64, num int64) int64 {
	// date := time.Now().Format("20060102")

	if num <= 0 {
		logger.Error("template: sub score num must be positive")
		return -1
	}

	var score int64
	err := localdb.RWCover(func() error {
		var err error
		scoreKey := localdb.Key("Score", groupCode, uin)
		// dateMarker := localdb.Key("ScoreDate", groupCode, uin, date)

		score, err = localdb.GetInt64(scoreKey, localdb.IgnoreNotFoundOpt())
		if err != nil {
			return err
		}
		// if localdb.Exist(dateMarker) {
		// 	logger = logger.WithField("current_score", score)
		// 	return nil
		// }

		score, err = localdb.IncInt64(scoreKey, -num)
		if err != nil {
			return err
		}

		// err = localdb.Set(dateMarker, "", localdb.SetExpireOpt(time.Hour*24*3))
		// if err != nil {
		// 	return err
		// }
		logger = logger.WithField("new_score", score)
		return nil
	})
	if err != nil {
		logger.Errorf("sub score error %v", err)
		return -1
	}
	return score
}

func getScore(uin int64, groupCode int64) int64 {
	// date := time.Now().Format("20060102")

	var score int64
	err := localdb.RWCover(func() error {
		var err error
		scoreKey := localdb.Key("Score", groupCode, uin)
		// dateMarker := localdb.Key("ScoreDate", groupCode, uin, date)

		score, err = localdb.GetInt64(scoreKey, localdb.IgnoreNotFoundOpt())
		if err != nil {
			return err
		}
		// if localdb.Exist(dateMarker) {
		// 	logger = logger.WithField("current_score", score)
		// 	return nil
		// }

		// score, err = localdb.IncInt64(scoreKey, num)
		// if err != nil {
		// 	return err
		// }

		// err = localdb.Set(dateMarker, "", localdb.SetExpireOpt(time.Hour*24*3))
		// if err != nil {
		// 	return err
		// }
		logger = logger.WithField("now_score", score)
		return nil
	})
	if err != nil {
		logger.Errorf("get score error %v", err)
		return -1
	}
	return score
}

func picUri(uri string) (e *mmsg.ImageBytesElement) {
	logger := logger.WithField("uri", uri)
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		e = mmsg.NewImage(nil, uri)
		//e = mmsg.NewImageByUrlWithoutCache(uri)
	} else {
		fi, err := os.Stat(uri)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Errorf("template: pic uri doesn't exist")
			} else {
				logger.Errorf("template: pic uri Stat error %v", err)
			}
			goto END
		}
		if fi.IsDir() {
			f, err := os.Open(uri)
			if err != nil {
				logger.Errorf("template: pic uri Open error %v", err)
				goto END
			}
			dirs, err := f.ReadDir(-1)
			if err != nil {
				logger.Errorf("template: pic uri ReadDir error %v", err)
				goto END
			}
			var result []os.DirEntry
			for _, file := range dirs {
				if file.IsDir() || !(strings.HasSuffix(file.Name(), ".jpg") ||
					strings.HasSuffix(file.Name(), ".png") ||
					strings.HasSuffix(file.Name(), ".gif")) {
					continue
				}
				result = append(result, file)
			}
			if len(result) > 0 {
				e = mmsg.NewImageByLocal(filepath.Join(uri, result[rand.Intn(len(result))].Name()))
			} else {
				logger.Errorf("template: pic uri can not find any images")
			}
		}
	END:
		if e == nil {
			e = mmsg.NewImageByLocal(uri)
		}
	}
	return e
}

func pic(input interface{}, alternative ...string) *mmsg.ImageBytesElement {
	var alt string
	if len(alternative) > 0 && len(alternative[0]) > 0 {
		alt = alternative[0]
	}
	switch e := input.(type) {
	case string:
		if b, err := base64.StdEncoding.DecodeString(e); err == nil {
			return mmsg.NewImage(b).Alternative(alt)
		}
		return picUri(e).Alternative(alt)
	case []byte:
		return mmsg.NewImage(e).Alternative(alt)
	default:
		panic(fmt.Sprintf("invalid pic %v", input))
	}
}

func icon(uin int64, size ...uint) *mmsg.ImageBytesElement {
	var width uint = 120
	var height uint = 120
	if len(size) > 0 && size[0] > 0 {
		width = size[0]
		height = size[0]
		if len(size) > 1 && size[1] > 0 {
			height = size[1]
		}
	}
	return mmsg.NewImageByUrl(fmt.Sprintf("https://q1.qlogo.cn/g?b=qq&nk=%v&s=640", uin)).Resize(width, height)
}

func roll(from, to int64) int64 {
	return rand.Int63n(to-from+1) + from
}

func choose(args ...reflect.Value) string {
	if len(args) == 0 {
		panic("empty choose")
	}
	var items []string
	var weights []int64
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var weight int64 = 1
		if arg.Kind() != reflect.String {
			panic("choose item must be string")
		}
		items = append(items, arg.String())
		if i+1 < len(args) {
			next := args[i+1]
			if next.Kind() != reflect.String {
				// 当作权重处理
				switch next.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					weight = next.Int()
				default:
					panic("item weight must be integer")
				}
				i++
			}
		}
		if weight <= 0 {
			panic("item weight must greater than 0")
		}
		weights = append(weights, weight)
	}

	if len(items) != len(weights) {
		logger.Errorf("Internal: items weights mismatched: %v %v", items, weights)
		panic("Internal: items weights mismatched")
	}

	var sum int64 = 0
	for _, w := range weights {
		sum += w
	}
	result := rand.Int63n(sum) + 1
	for i := 0; i < len(weights); i++ {
		result -= weights[i]
		if result <= 0 {
			return items[i]
		}
	}
	logger.Errorf("Internal: wrong rand: %v %v - %v", items, weights, result)
	panic("Internal: wrong rand")
}

func execDecimalOp(a interface{}, b []interface{}, f func(d1, d2 decimal.Decimal) decimal.Decimal) float64 {
	prt := decimal.NewFromFloat(toFloat64(a))
	for _, x := range b {
		dx := decimal.NewFromFloat(toFloat64(x))
		prt = f(prt, dx)
	}
	rslt, _ := prt.Float64()
	return rslt
}

func cooldown(ttlUnit string, keys ...interface{}) bool {
	ttl, err := time.ParseDuration(ttlUnit)
	if err != nil {
		panic(fmt.Sprintf("ParseDuration: can not parse <%v>: %v", ttlUnit, err))
	}
	key := localdb.NamedKey("TemplateCooldown", keys)

	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	err = localdb.Set(key, "",
		localdb.SetExpireOpt(ttl),
		localdb.SetNoOverWriteOpt(),
	)
	if err == localdb.ErrRollback {
		return false
	} else if err != nil {
		logger.Errorf("localdb.Set: cooldown set <%v> error %v", key, err)
		panic(fmt.Sprintf("INTERNAL: db error"))
	}
	return true
}

func setCooldown(ttlUnit string, keys ...interface{}) bool {
	ttl, err := time.ParseDuration(ttlUnit)
	if err != nil {
		panic(fmt.Sprintf("ParseDuration: can not parse <%v>: %v", ttlUnit, err))
	}
	key := localdb.NamedKey("TemplateCooldown", keys)
	var Overwrite bool

	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	err = localdb.Set(key, "",
		localdb.SetExpireOpt(ttl),
		localdb.SetGetIsOverwriteOpt(&Overwrite),
	)
	if err == localdb.ErrRollback {
		return false
	} else if err != nil {
		logger.Errorf("localdb.Set: cooldown set <%v> error %v", key, err)
		panic(fmt.Sprintf("INTERNAL: db error"))
	}
	if Overwrite {
		logger.Debugf("template: cooldown set <%v> overwrite", key)
	}
	return true
}

type ddError struct {
	ddErrType string
	e         message.IMessageElement
	err       error
}

func (d *ddError) Error() string {
	if d.err != nil {
		return d.Error()
	}
	return ""
}

var errFin = &ddError{ddErrType: "fin", err: fmt.Errorf("fin")}

func abort(e ...interface{}) interface{} {
	if len(e) > 0 {
		i := e[0]
		aerr := &ddError{ddErrType: "abort", err: fmt.Errorf("abort")}
		switch s := i.(type) {
		case string:
			aerr.e = message.NewText(s)
		case message.IMessageElement:
			aerr.e = s
		default:
			panic("template: abort with invalid e")
		}
		panic(aerr)
	}
	panic(&ddError{ddErrType: "abort"})
}

func fin() interface{} {
	panic(errFin)
}

func getUnixTime(i int64, f string) string {
	t := time.Unix(i, 0)
	return getTime(t, f)
}

func getTimeStamp(t string) int64 {
	loc, _ := time.LoadLocation("Local")
	fTime, _ := time.ParseInLocation(time.DateTime, t, loc)
	ret := fTime.Unix()
	return ret
}

func getTime(s interface{}, f string, bases ...interface{}) string {
	var t time.Time

	// 如果传了基准时间，就用它；否则默认用当前时间
	var base time.Time
	if len(bases) > 0 {
		switch v := bases[0].(type) {
		case time.Time:
			base = v
		case int64:
			base = time.Unix(v, 0).In(time.Local)
		case int32:
			base = time.Unix(int64(v), 0).In(time.Local)
		case int:
			base = time.Unix(int64(v), 0).In(time.Local)
		case string:
			// 如果传的是字符串，也尝试解析
			tmp, err := time.ParseInLocation(time.DateTime, v, time.Local)
			if err == nil {
				base = tmp
			} else {
				base = time.Now()
			}
		default:
			base = time.Now()
		}
	} else {
		base = time.Now()
	}

	parseWithLayouts := func(str string, loc *time.Location, layouts ...string) (time.Time, error) {
		for _, layout := range layouts {
			if tt, err := time.ParseInLocation(layout, str, loc); err == nil {
				return tt, nil
			}
		}
		return time.Time{}, fmt.Errorf("no layout matched")
	}

	switch v := s.(type) {
	case time.Time:
		t = v
	case string:
		if v == "now" {
			t = base
		} else {
			str := strings.TrimSpace(v)
			str = strings.ReplaceAll(str, "预计", "")
			str = strings.ReplaceAll(str, "发布", "")
			str = strings.ReplaceAll(str, "直播", "")

			loc := time.Local
			now := base // 用基准时间替代 time.Now()

			// 1) 绝对时间
			absLayouts := []string{
				time.DateTime,
				"2006-01-02 15:04",
				"2006/01/02 15:04:05",
				"2006/01/02 15:04",
				"2006.01.02 15:04:05",
				"2006.01.02 15:04",
				time.RFC3339,
			}
			if tt, err := parseWithLayouts(str, loc, absLayouts...); err == nil {
				t = tt
				break
			}

			// 2) 相对时间：今天/明天/后天
			if strings.HasPrefix(str, "今天") || strings.HasPrefix(str, "明天") || strings.HasPrefix(str, "后天") {
				parts := strings.Fields(str)
				if len(parts) >= 2 {
					offset := 0
					switch {
					case strings.HasPrefix(str, "明天"):
						offset = 1
					case strings.HasPrefix(str, "后天"):
						offset = 2
					}
					baseDate := now.AddDate(0, 0, offset)
					dateStr := fmt.Sprintf("%04d-%02d-%02d %s", baseDate.Year(), baseDate.Month(), baseDate.Day(), parts[1])
					if tt, err := time.ParseInLocation("2006-01-02 15:04", dateStr, loc); err == nil {
						t = tt
						break
					}
				}
			}

			// 3) 简化日期：MM-DD HH:MM
			if matched := regexp.MustCompile(`^(?:\D|^)?(\d{2}-\d{2})\s+(\d{2}:\d{2})(?:\D|$)?`).FindStringSubmatch(str); len(matched) == 3 {
				dateStr := fmt.Sprintf("%04d-%s %s", now.Year(), matched[1], matched[2])
				if tt, err := time.ParseInLocation("2006-01-02 15:04", dateStr, loc); err == nil {
					t = tt
					break
				}
			}

			// 4) 兜底
			if tt, err := parseWithLayouts(str, loc,
				time.DateOnly,
				time.TimeOnly,
				"15:04",
			); err == nil {
				if tt.Year() == 0 {
					tt = time.Date(now.Year(), now.Month(), now.Day(), tt.Hour(), tt.Minute(), tt.Second(), 0, loc)
				}
				t = tt
			} else {
				logger.Error("template: parse time error")
				return ""
			}
		}

	case int:
		t = time.Unix(int64(v), 0).In(time.Local)
	case int32:
		t = time.Unix(int64(v), 0).In(time.Local)
	case int64:
		t = time.Unix(v, 0).In(time.Local)
	default:
		logger.Error("template: getTime with invalid s")
		return ""
	}

	switch f {
	case "dateonly":
		return t.Format(time.DateOnly)
	case "timeonly":
		return t.Format(time.TimeOnly)
	case "stamp":
		return t.Format(time.Stamp)
	case "unix":
		return strconv.FormatInt(t.Unix(), 10)
	case "elapsed":
		dur := time.Since(t)
		h := int64(dur.Hours())
		m := int64(dur.Minutes()) % 60
		s := int64(dur.Seconds()) % 60
		return fmt.Sprintf("%d小时%d分%d秒", h, m, s)
	default:
		return t.Format(time.DateTime)
	}
}

func uriEncode(s string) string {
	return url.QueryEscape(s)
}

func uriDecode(s string) (string, error) {
	return url.QueryUnescape(s)
}

// 修改调用方式
func getIListJson(groupCode int64, site string, msgContext ...interface{}) []byte {
	provider := interfaces.NewListProvider()
	return provider.QueryList(groupCode, site, msgContext)
}

func outputIList(msgContext interface{}, groupCode int64, site string) string {
	provider := interfaces.NewListProvider()
	provider.RunIList(msgContext, groupCode, site)
	return ""
}

func jsonToDictOrArray(jsonByte []byte, isArray bool) (interface{}, error) {
	if isArray {
		var result []map[string]interface{}
		err := json.Unmarshal(jsonByte, &result)
		if err != nil {
			return nil, err
		}
		return result, nil
	} else {
		var result map[string]interface{}
		err := json.Unmarshal(jsonByte, &result)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
}

func videoUri(uri string) (e *mmsg.VideoElement) {
	logger := logger.WithField("uri", uri)
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		e = mmsg.NewVideo(uri)
		//e = mmsg.NewVideoByUrl(uri)
	} else {
		fi, err := os.Stat(uri)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Errorf("template: video uri doesn't exist")
			} else {
				logger.Errorf("template: video uri Stat error %v", err)
			}
			goto END
		}
		if fi.IsDir() {
			f, err := os.Open(uri)
			if err != nil {
				logger.Errorf("template: video uri Open error %v", err)
				goto END
			}
			dirs, err := f.ReadDir(-1)
			if err != nil {
				logger.Errorf("template: video uri ReadDir error %v", err)
				goto END
			}
			var result []os.DirEntry
			for _, file := range dirs {
				if file.IsDir() || !(strings.HasSuffix(file.Name(), ".mp4")) {
					continue
				}
				result = append(result, file)
			}
			if len(result) > 0 {
				e = mmsg.NewVideo("", openFile(filepath.Join(uri, result[rand.Intn(len(result))].Name())))
			} else {
				logger.Errorf("template: video uri can not find any videos")
			}
		}
	END:
		if e == nil {
			e = mmsg.NewVideo(uri)
		}
	}
	return e
}

func video(input interface{}, name ...string) *mmsg.VideoElement {
	var alt string
	if len(name) > 0 && len(name[0]) > 0 {
		alt = name[0]
	}
	switch e := input.(type) {
	case string:
		if b, err := base64.StdEncoding.DecodeString(e); err == nil {
			return mmsg.NewVideo("", b).Alternative(alt)
		}
		return videoUri(e).Alternative(alt)
	case []byte:
		return mmsg.NewVideo("", e).Alternative(alt)
	default:
		panic(fmt.Sprintf("invalid video %v", input))
	}
}

func recordUri(uri string) (e *mmsg.RecordElement) {
	logger := logger.WithField("uri", uri)
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		e = mmsg.NewRecord(uri)
		//e = mmsg.NewRecordByUrl(uri)
	} else {
		fi, err := os.Stat(uri)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Errorf("template: record uri doesn't exist")
			} else {
				logger.Errorf("template: record uri Stat error %v", err)
			}
			goto END
		}
		if fi.IsDir() {
			f, err := os.Open(uri)
			if err != nil {
				logger.Errorf("template: record uri Open error %v", err)
				goto END
			}
			dirs, err := f.ReadDir(-1)
			if err != nil {
				logger.Errorf("template: record uri ReadDir error %v", err)
				goto END
			}
			var result []os.DirEntry
			for _, file := range dirs {
				if file.IsDir() || !(strings.HasSuffix(file.Name(), ".mp3") ||
					!(strings.HasSuffix(file.Name(), ".wav")) ||
					!(strings.HasSuffix(file.Name(), ".ogg"))) {
					continue
				}
				result = append(result, file)
			}
			if len(result) > 0 {
				e = mmsg.NewRecord("", openFile(filepath.Join(uri, result[rand.Intn(len(result))].Name())))
			} else {
				logger.Errorf("template: record uri can not find any records")
			}
		}
	END:
		if e == nil {
			e = mmsg.NewRecord(uri)
		}
	}
	return e
}

func record(input interface{}, name ...string) *mmsg.RecordElement {
	var alt string
	if len(name) > 0 && len(name[0]) > 0 {
		alt = name[0]
	}
	switch e := input.(type) {
	case string:
		if b, err := base64.StdEncoding.DecodeString(e); err == nil {
			return mmsg.NewRecord("", b).Alternative(alt)
		}
		return recordUri(e).Alternative(alt)
	case []byte:
		return mmsg.NewRecord("", e).Alternative(alt)
	default:
		panic(fmt.Sprintf("invalid record %v", input))
	}
}

func fileUri(uri string) (e *mmsg.FileElement) {
	logger := logger.WithField("uri", uri)
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		e = mmsg.NewFile(uri)
		//e = mmsg.NewFileByUrl(uri)
	} else {
		fi, err := os.Stat(uri)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Errorf("template: file uri doesn't exist")
			} else {
				logger.Errorf("template: file uri Stat error %v", err)
			}
			goto END
		}
		if fi.IsDir() {
			f, err := os.Open(uri)
			if err != nil {
				logger.Errorf("template: file uri Open error %v", err)
				goto END
			}
			dirs, err := f.ReadDir(-1)
			if err != nil {
				logger.Errorf("template: file uri ReadDir error %v", err)
				goto END
			}
			var result []os.DirEntry
			for _, file := range dirs {
				result = append(result, file)
			}
			if len(result) > 0 {
				f := result[rand.Intn(len(result))].Name()
				e = mmsg.NewFile("", openFile(filepath.Join(uri, f))).Name(f)
			} else {
				logger.Errorf("template: file uri can not find any files")
			}
		}
	END:
		if e == nil {
			e = mmsg.NewFile(uri)
		}
	}
	return e
}

func file(input interface{}, name ...string) *mmsg.FileElement {
	var alt string
	if len(name) > 0 && len(name[0]) > 0 {
		alt = name[0]
	}
	switch e := input.(type) {
	case string:
		if b, err := base64.StdEncoding.DecodeString(e); err == nil {
			return mmsg.NewFile("", b).Alternative(alt)
		}
		return fileUri(e).Alternative(alt)
	case []byte:
		return mmsg.NewFile("", e).Alternative(alt)
	default:
		panic(fmt.Sprintf("invalid file %v", input))
	}
}

func remoteDownloadFile(urlOrBase64 string, opts ...interface{}) string {
	var Url, Base64, name string
	var headers []string

	if urlOrBase64 == "" {
		logger.Error("至少需要提供 url 或 base64 参数")
		return ""
	}

	options := make(map[string]interface{})
	for _, arg := range opts {
		if m, ok := arg.(map[string]interface{}); ok {
			for k, v := range m {
				options[k] = v
			}
		}
	}
	if n, ok := options["name"].(string); ok {
		name = n
	}
	if h, ok := options["headers"].([]string); ok {
		headers = h
	}

	if strings.HasPrefix(urlOrBase64, "http://") || strings.HasPrefix(urlOrBase64, "https://") {
		Url = urlOrBase64
	} else if strings.HasPrefix(urlOrBase64, "base64://") {
		Base64 = urlOrBase64
	}

	bot := localutils.GetBot()
	if bot == nil {
		logger.Error("bot 实例未找到")
		return ""
	}

	ret, err := (*bot.Bot).QQClient.DownloadFile(Url, Base64, name, headers)
	if err != nil {
		logger.Errorf("文件下载失败: %v", err)
	}
	return ret
}

func getFileUrl(groupCode int64, fileId string) string {
	bot := localutils.GetBot()
	if bot == nil {
		logger.Error("bot 实例未找到")
		return ""
	}
	return (*bot.Bot).QQClient.GetFileUrl(groupCode, fileId)
}

func getMsg(msgId int32) interface{} {
	bot := localutils.GetBot()
	if bot == nil {
		logger.Error("bot 实例未找到")
		return nil
	}
	ret, err := (*bot.Bot).QQClient.GetMsg(msgId)
	if err != nil {
		logger.Errorf("获取消息失败: %v", err)
		return nil
	}
	return ret
}

func sendApi(api string, params map[string]interface{}, expTime ...float64) interface{} {
	bot := localutils.GetBot()
	if bot == nil {
		logger.Error("bot 实例未找到")
		return nil
	}
	ret, err := (*bot.Bot).QQClient.SendApi(api, params, expTime...)
	if err != nil {
		logger.Errorf("調用API失败: %v", err)
		return nil
	}
	return ret
}

func loop(from, to int64) <-chan int64 {
	ch := make(chan int64)
	go func() {
		for i := from; i <= to; i++ {
			ch <- i
		}
		close(ch)
	}()
	return ch
}

func getEleType(v interface{}) string {
	switch v.(type) {
	case *message.GroupImageElement, *message.FriendImageElement:
		return "image"
	case *message.GroupFileElement, *message.FriendFileElement:
		return "file"
	default:
		return "unknown"
	}
}

func reCall(msg interface{}) bool {
	if msg == nil {
		logger.Warn("未提供需要撤回的消息")
		return false
	}
	bot := localutils.GetBot()
	if bot == nil {
		logger.Error("bot 实例未找到")
		return false
	}
	var msgId int32
	switch e := msg.(type) {
	case *message.GroupMessage:

		msgId = e.Id
	case *message.PrivateMessage:

		msgId = e.Id
	default:
		panic(fmt.Sprintf("需要撤回的消息类型无法解析: %v", msg))
	}
	err := (*bot.Bot).QQClient.RecallMsg(msgId)
	if err != nil {
		logger.Errorf("撤回消息失败: %v", err)
		return false
	}
	return true
}

func exec(input interface{}, inargs ...interface{}) string {
	var cmd string
	var args []string
	var wait = true           // 默认等待执行完毕
	var silent = false        // 默认不静默执行
	var elevate = false       // 默认不提升权限
	var shellMode = false     // 默认不使用 shell 执行
	var explicitShell = false // 是否显式指定了 shell 参数

	errTxt := "exec: %v"
	errInvalidInput := "输入参数错误"

	// 参数解析函数，处理特殊参数
	parseSpecialArgs := func(arg string) bool {
		switch arg {
		case "nowait":
			wait = false
			return true
		case "silent":
			silent = true
			return true
		case "elevate":
			elevate = true
			return true
		case "shell":
			shellMode = true
			explicitShell = true
			return true
		default:
			return false
		}
	}

	if len(inargs) > 0 {
		if c, ok := inargs[0].(string); ok {
			cmd = c
		} else {
			logger.Errorf(errTxt, inargs[0])
			return errInvalidInput
		}
		for i := 1; i < len(inargs); i++ {
			if arg, ok := inargs[i].(string); ok {
				if !parseSpecialArgs(arg) {
					args = append(args, arg)
				}
			} else {
				logger.Errorf(errTxt, inargs[i])
				return errInvalidInput
			}
		}
	} else {
		switch in := input.(type) {
		case []string:
			if len(in) < 1 {
				logger.Errorf(errTxt, errInvalidInput)
				return errInvalidInput
			}
			cmd = in[0]
			for _, arg := range in[1:] {
				if !parseSpecialArgs(arg) {
					args = append(args, arg)
				}
			}
		case string:
			parts := strings.Fields(in)
			if len(parts) < 1 {
				logger.Errorf(errTxt, errInvalidInput)
				return errInvalidInput
			}
			cmd = parts[0]
			for _, arg := range parts[1:] {
				if !parseSpecialArgs(arg) {
					args = append(args, arg)
				}
			}
		default:
			logger.Errorf(errTxt, errInvalidInput)
			return errInvalidInput
		}
	}

	// 自动检测是否在类 Unix 环境下运行，如果是则默认使用 shell 模式
	// 但仅在未显式指定 shell 参数时才启用自动模式
	if !explicitShell && (runtime.GOOS == "linux" || runtime.GOOS == "darwin" ||
		runtime.GOOS == "freebsd" || runtime.GOOS == "openbsd") {
		shellMode = true
	}

	// 检查参数冲突
	if elevate && silent {
		return "参数冲突：elevate 和 silent 不能同时使用"
	}

	if elevate && shellMode {
		return "参数冲突：elevate 和 shell 不能同时使用"
	}

	// 定义执行函数的类型
	type execFunc func(cmd string, args []string, wait bool) ([]byte, error)

	// 根据参数选择执行函数
	var executor execFunc
	var executeWait = wait

	switch {
	case shellMode:
		executor = localutils.ExecWithShell
	case elevate:
		executor = func(cmd string, args []string, wait bool) ([]byte, error) {
			return localutils.ExecWithElevation(cmd, args, wait)
		}
		executeWait = wait
	case silent:
		// 对于静默模式，wait参数不适用，始终等待执行完成
		executor = func(cmd string, args []string, wait bool) ([]byte, error) {
			bytes, err := localutils.ExecSilently(cmd, args)
			return bytes, err
		}
		executeWait = true
	default:
		executor = localutils.ExecWithOption
		executeWait = wait
	}

	// 执行命令
	bytes, err := executor(cmd, args, executeWait)
	if err != nil {
		return err.Error()
	}
	return strings.ToValidUTF8(string(bytes), "")
}
