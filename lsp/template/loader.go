package template

import (
	"embed"
	"fmt"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/fsnotify/fsnotify"
	"os"
	"path/filepath"
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
	// 通知模板走缓存版函数表（notification_cache.go）。
	//
	// 证据（DDBOT-Lite x64 成品二进制，LoadAndExec @0xb55b60）：
	// 该函数在 LoadTemplate 之后依次调用
	//   notificationTemplateScope @0xb55bfa  （判断并生成缓存 scope 串）
	//   (*Template).Clone        @0xb55c20  （仅在命中时执行）
	//   notificationTemplateFuncs@0xb55d11  （入参就是上面的 scope）
	//   (*Template).Funcs        @0xb55d21
	// 四个符号在整个 .text 段各只有这一个调用点，全部落在 LoadAndExec 内；
	// 命令/触发器模板走的是不注入分支。Clone 是必要的：Funcs 会改写模板的
	// parseFuncs/execFuncs，直接改共享的 rootT 会在并发执行时产生数据竞争。
	if scope, ok := notificationTemplateScope(name, data); ok {
		nt, err := t.Clone()
		if err != nil {
			logger.WithField("data", data).Errorf("template: %v clone error: %v", name, err)
			return nil, err
		}
		t = nt.Funcs(notificationTemplateFuncs(scope))
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
