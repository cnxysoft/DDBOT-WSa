package template

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	localdb "github.com/Sora233/DDBOT/lsp/buntdb"
	"github.com/Sora233/DDBOT/lsp/cfg"
	"github.com/Sora233/DDBOT/lsp/mmsg"
	localutils "github.com/Sora233/DDBOT/utils"
	"github.com/shopspring/decimal"
)

var funcsExt = make(FuncMap)

// RegisterExtFunc 在init阶段插入额外的template函数
func RegisterExtFunc(name string, fn interface{}) {
	checkValueFuncs(name, fn)
	funcsExt[name] = fn
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
		e = mmsg.NewImageByUrlWithoutCache(uri)
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
		panic(fmt.Sprintf("invalid input %v", input))
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

func openFile(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		logger.Errorf("template: openFile <%v> error %v", path, err)
		return nil
	}
	return data
}

func updateFile(path string, data string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Errorf("template: openFile <%v> error %v", path, err)
		return err
	}
	defer file.Close()
	_, err = file.WriteString(data)
	if err != nil {
		logger.Errorf("template: updateFile <%v> error %v", path, err)
		return err
	}
	return nil
}

func writeFile(path string, data string) error {
	err := os.WriteFile(path, []byte(data), 0644)
	if err != nil {
		logger.Errorf("template: writeFile <%v> error %v", path, err)
		return err
	}
	return nil
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

func getTime(s interface{}, f string) string {
	var t time.Time
	if _, ok := s.(time.Time); ok {
		t = s.(time.Time)
	} else if _, ok := s.(string); ok {
		if s.(string) == "now" {
			t = time.Now()
		} else {
			tmp, err := time.Parse(time.DateTime, s.(string))
			if err != nil {
				return "parse time error"
			}
			t = tmp
		}
	}
	if f == "dateonly" {
		return t.Format(time.DateOnly)
	} else if f == "timeonly" {
		return t.Format(time.TimeOnly)
	} else if f == "stamp" {
		return t.Format(time.Stamp)
	} else {
		return t.Format(time.DateTime)
	}
}

func readLine(p string, l int64) string {
	file, err := os.OpenFile(p, os.O_RDONLY, 0666)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var ret string
	for i := int64(0); ; i++ {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if i == l-1 {
			ret = line
			break
		}
	}
	return ret
}

func findReadLine(p string, s string) string {
	file, err := os.OpenFile(p, os.O_RDONLY, 0666)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if strings.Contains(line, s) {
			return line
		}
	}
	return ""
}

func findWriteLine(p string, s string, n string) error {
	file, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer file.Close()
	lines := make([]string, 0)
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if strings.Contains(line, s) {
			lines = append(lines, n)
		} else {
			lines = append(lines, line)
		}
	}
	writer := bufio.NewWriter(file)
	_, err = file.Seek(0, 0)
	if err != nil {
		logger.Errorf("template: seek <%v> error %v", p, err)
		return err
	}
	for _, line := range lines {
		_, err = writer.WriteString(line)
		if err != nil {
			logger.Errorf("template: writeFile <%v> error %v", p, err)
			return err
		}
	}
	writer.Flush()
	return nil
}

func writeLine(p string, l int64, s string) error {
	file, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	if l == 0 {
		_, err = writer.WriteString(s)
		if err != nil {
			logger.Errorf("template: writeFile <%v> error %v", p, err)
			return err
		}
	} else {
		lines := make([]string, 0)
		reader := bufio.NewReader(file)
		for i := int64(0); ; i++ {
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				break
			}
			if i == l-1 {
				lines = append(lines, s)
			} else {
				lines = append(lines, line)
			}
		}
		_, err = file.Seek(0, 0)
		if err != nil {
			logger.Errorf("template: seek <%v> error %v", p, err)
			return err
		}
		for _, line := range lines {
			_, err = writer.WriteString(line)
			if err != nil {
				logger.Errorf("template: writeFile <%v> error %v", p, err)
				return err
			}
		}
	}
	writer.Flush()
	return nil
}

func uriEncode(s string) string {
	return url.QueryEscape(s)
}

func uriDecode(s string) (string, error) {
	return url.QueryUnescape(s)
}
