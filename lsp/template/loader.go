package template

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/fsnotify/fsnotify"
)

const templateDir = "template"

var rootT = New("root")
var mu sync.RWMutex

//go:embed default/*.tmpl
var tfs embed.FS

var once sync.Once
var loaderMu sync.Mutex
var activeLoader *templateLoader

type templateLoader struct {
	watcher *fsnotify.Watcher
	done    chan struct{}
}

func defaultRoot() (*Template, error) { return New("root").ParseFS(tfs, "default/*.tmpl") }
func initRootT() {
	once.Do(func() {
		initial := Must(defaultRoot())
		mu.Lock()
		rootT = initial
		mu.Unlock()
	})
}

func externalRoot() (*Template, error) {
	candidate, err := defaultRoot()
	if err != nil {
		return nil, err
	}
	_, err = candidate.ParseGlob(filepath.Join(templateDir, "*.tmpl"))
	if err == ErrGlobNotMatch {
		err = nil
	}
	return candidate, err
}

func prepareLoader(enabled bool) (func(), func(), error) {
	initRootT()
	candidate, err := defaultRoot()
	if err != nil {
		return nil, nil, err
	}
	var next *templateLoader
	if enabled {
		if err = os.MkdirAll(templateDir, 0766); err != nil {
			return nil, nil, fmt.Errorf("无法访问自定义模板目录")
		}
		candidate, err = externalRoot()
		if err != nil {
			return nil, nil, fmt.Errorf("自定义模板语法无效，保留旧模板")
		}
		w, e := fsnotify.NewWatcher()
		if e != nil {
			return nil, nil, e
		}
		if e = w.Add(templateDir); e != nil {
			_ = w.Close()
			return nil, nil, e
		}
		next = &templateLoader{watcher: w, done: make(chan struct{})}
	}
	commit := func() {
		loaderMu.Lock()
		old := activeLoader
		activeLoader = next
		mu.Lock()
		rootT = candidate
		mu.Unlock()
		if next != nil {
			go next.run()
		}
		loaderMu.Unlock()
		if old != nil {
			_ = old.watcher.Close()
			<-old.done
		}
	}
	abort := func() {
		if next != nil {
			_ = next.watcher.Close()
		}
	}
	return commit, abort, nil
}

func (l *templateLoader) run() {
	defer close(l.done)
	// In-place saves produce separate truncate/write events. Coalesce the burst
	// before parsing to avoid publishing an intermediate empty file during an
	// ordinary save whose final contents may be invalid.
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()
	schedule := func() {
		if !debounce.Stop() {
			select {
			case <-debounce.C:
			default:
			}
		}
		debounce.Reset(100 * time.Millisecond)
	}
	for {
		select {
		case event, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			if !strings.HasSuffix(event.Name, ".tmpl") {
				continue
			}
			schedule()
		case <-debounce.C:
			candidate, err := externalRoot()
			// Parsing can span another edit. If a new event arrived, discard this
			// candidate and wait for that edit to settle as well.
			changed := false
		drain:
			for {
				select {
				case event, ok := <-l.watcher.Events:
					if !ok {
						return
					}
					changed = changed || strings.HasSuffix(event.Name, ".tmpl")
				default:
					break drain
				}
			}
			if changed {
				schedule()
				continue
			}
			if err != nil {
				logger.WithError(err).Warn("自定义模板更新失败，保留上次有效模板")
				continue
			}
			loaderMu.Lock()
			if activeLoader == l {
				mu.Lock()
				rootT = candidate
				mu.Unlock()
			}
			loaderMu.Unlock()
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			logger.WithError(err).Warn("模板目录监听错误")
		}
	}
}

func InitTemplateLoader() {
	commit, _, err := prepareLoader(true)
	if err != nil {
		logger.WithError(err).Error("自定义模板初始化失败")
		return
	}
	commit()
}

// Close stops the currently active external template watcher.
func Close() {
	loaderMu.Lock()
	old := activeLoader
	activeLoader = nil
	loaderMu.Unlock()
	if old != nil {
		_ = old.watcher.Close()
		<-old.done
	}
}

// PrepareRuntimeReload validates custom templates and opens a replacement external DB.
func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	var commits, aborts []func()
	cleanup := func() {
		for i := len(aborts) - 1; i >= 0; i-- {
			if aborts[i] != nil {
				aborts[i]()
			}
		}
	}
	if previous == nil || previous.GetBool("template.enable") != next.GetBool("template.enable") {
		commit, abort, err := prepareLoader(next.GetBool("template.enable"))
		if err != nil {
			return nil, nil, err
		}
		commits = append(commits, commit)
		aborts = append(aborts, abort)
	}
	if previous == nil || !reflect.DeepEqual(previous.Get("extDb"), next.Get("extDb")) {
		commit, abort, err := prepareTemplateDB(next.GetBool("extDb.enable"), next.GetString("extDb.path"))
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		commits = append(commits, commit)
		aborts = append(aborts, abort)
	}
	return func() {
		for _, commit := range commits {
			if commit != nil {
				commit()
			}
		}
	}, cleanup, nil
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
	data, preparedImage := prepareLegacyImageMerge(name, data)
	if err := t.Execute(m, data); err != nil {
		logger.WithField("data", data).Errorf("template: %v execute error: %v", name, err)
		return nil, err
	}
	return mergeRenderedImages(name, data, markPreparedImage(m, preparedImage)), nil
}

var templateDB *buntdb.DB
var templateDBMu sync.RWMutex
var templateDBPath string

func canonicalDBPath(path string) string {
	if path == ":memory:" {
		return path
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return filepath.Clean(resolved)
}

func prepareTemplateDB(enabled bool, path string) (func(), func(), error) {
	if enabled && strings.TrimSpace(path) == "" {
		return nil, nil, fmt.Errorf("extDb.path 不能为空")
	}
	canonical := canonicalDBPath(path)
	templateDBMu.RLock()
	same := enabled && templateDB != nil && templateDB.DB != nil && templateDBPath == canonical
	templateDBMu.RUnlock()
	if same {
		return nil, nil, nil
	}
	var candidate *buntdb.DB
	var err error
	if enabled {
		candidate, err = buntdb.NewDB(path)
		if err != nil {
			return nil, nil, fmt.Errorf("无法打开 extDb.path，保留运行中的外部数据库")
		}
	}
	return func() {
			templateDBMu.Lock()
			old := templateDB
			templateDB = candidate
			templateDBPath = canonical
			if old != nil {
				_ = old.Close()
			}
			templateDBMu.Unlock()
		}, func() {
			if candidate != nil {
				_ = candidate.Close()
			}
		}, nil
}

func InitTemplateDB(dbPath string) error {
	commit, _, err := prepareTemplateDB(true, dbPath)
	if err == nil && commit != nil {
		commit()
	}
	return err
}

// GetTemplateDB is for startup compatibility. Runtime functions use transaction leases.
func GetTemplateDB() *buntdb.DB {
	templateDBMu.RLock()
	defer templateDBMu.RUnlock()
	return templateDB
}

func CloseTemplateDB() {
	templateDBMu.Lock()
	defer templateDBMu.Unlock()
	if templateDB != nil {
		_ = templateDB.Close()
		templateDB = nil
	}
	templateDBPath = ""
}

// GetTemplateSC is only safe while the caller holds templateDBMu for the whole operation.
func GetTemplateSC() *buntdb.ShortCut { return buntdb.WithDB(templateDB) }
