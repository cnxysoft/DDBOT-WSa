package template

import (
	"embed"
	"fmt"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/fsnotify/fsnotify"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const templateDir = "template"

var rootT = New("root")
var mu sync.RWMutex

//go:embed default/*.tmpl
var tfs embed.FS

var once sync.Once
var watcher *fsnotify.Watcher

func initRootT() {
	once.Do(func() {
		mu.Lock()
		rootT = Must(rootT.ParseFS(tfs, "default/*.tmpl"))
		mu.Unlock()
	})
}

func InitTemplateLoader() {
	var err error
	initRootT()
	watcher, err = fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	if _, err := os.Stat(templateDir); err != nil {
		if os.IsNotExist(err) {
			logger.Infof("监测到<%v>目录不存在，将自动创建，请将所有自定义模板放在<%v>文件夹内", templateDir, templateDir)
			os.Mkdir(templateDir, 0766)
		} else {
			logger.Errorf("监测<%v>目录失败：%v", templateDir, err)
			return
		}
	}
	if err := watcher.Add(templateDir); err != nil {
		logger.Errorf("监测<%v>文件夹失败，自定义模板可能无法生效: %v", templateDir, err)
		return
	}
	parseExternalTemplate := func() {
		if _, err := rootT.ParseGlob(filepath.Join(templateDir, "*.tmpl")); err != nil {
			if err == ErrGlobNotMatch {
				logger.Infof(`没有发现模板文件，注意模板必须以".tmpl"结尾`)
			} else {
				logger.Errorf("解析模板错误：%v", err)
			}
		}
	}
	parseExternalTemplate()
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if !strings.HasSuffix(event.Name, "tmpl") {
					continue
				}
				logger.Infof("监测到%v文件变动: %v", templateDir, event.String())
				mu.Lock()
				rootT = Must(rootT.ParseFS(tfs, "default/*.tmpl"))
				parseExternalTemplate()
				mu.Unlock()
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Errorf("监测template发生错误: %v", err)
			}
		}
	}()
}

func Close() {
	if watcher != nil {
		watcher.Close()
	}
}

// DefaultTemplateNames 返回内嵌默认模板名列表（不含 .tmpl 后缀，已排序）。
// 供 admin 等管理层列举可覆盖的模板。
func DefaultTemplateNames() []string {
	initRootT()
	entries, err := tfs.ReadDir("default")
	if err != nil {
		logger.Errorf("read default templates error: %v", err)
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".tmpl") {
			names = append(names, strings.TrimSuffix(n, ".tmpl"))
		}
	}
	sort.Strings(names)
	return names
}

// DefaultTemplateContent 返回默认模板的内容。
func DefaultTemplateContent(name string) (string, error) {
	initRootT()
	b, err := tfs.ReadFile(fmt.Sprintf("default/%s.tmpl", name))
	return string(b), err
}

// ValidateTemplate 用独立实例校验模板语法（不触碰运行中的 rootT）。
// 语法错误时返回 error，调用方应拒绝写入。
func ValidateTemplate(content string) error {
	_, err := New("validate").Parse(content)
	return err
}

// RenderPreview 用给定的示例数据渲染模板内容，返回渲染结果的可读文本。
// 与线上渲染共用同一套内建函数表（getTime/pic 等）。
// 文本元素原样输出，图片/@等富媒体元素折叠为占位标记。
// 语法或执行错误均返回 error（执行期 panic 已被模板引擎转为 error）。
func RenderPreview(content string, data map[string]interface{}) (string, error) {
	t, err := New("preview").Parse(content)
	if err != nil {
		return "", err
	}
	msg := mmsg.NewMSG()
	if err := t.Execute(msg, data); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, e := range msg.Elements() {
		if ts, ok := e.(*adapter.TextSegment); ok {
			sb.WriteString(ts.Content)
		} else if _, ok := e.(*mmsg.ImageBytesElement); ok {
			sb.WriteString("[img]")
		} else {
			sb.WriteString("[元素]")
		}
	}
	return sb.String(), nil
}

func LoadTemplate(name string) *Template {
	if cfg.GetExtDbEnable() && templateDB == nil {
		logger.Errorf("ExtDb: 未初始化")
		return nil
	}
	initRootT()
	mu.RLock()
	defer mu.RUnlock()
	return rootT.Lookup(name)
}

func LoadAndExec(name string, data interface{}) (*mmsg.MSG, error) {
	initRootT()
	m := mmsg.NewMSG()
	t := LoadTemplate(name)
	if t == nil {
		return nil, fmt.Errorf("<!missing template %v>", name)
	}
	if err := t.Execute(m, data); err != nil {
		logger.WithField("data", data).Errorf("template: %v execute error: %v", name, err)
		return nil, err
	}
	return m, nil
}

var templateDB *buntdb.DB

func InitTemplateDB(dbPath string) error {
	var err error
	templateDB, err = buntdb.NewDB(dbPath)
	return err
}

func GetTemplateDB() *buntdb.DB {
	return templateDB
}

func GetTemplateSC() *buntdb.ShortCut {
	return buntdb.WithDB(templateDB)
}
