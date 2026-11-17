package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/sirupsen/logrus"
)

type independentInvalidTemplateHook struct{ processed chan struct{} }

func (h *independentInvalidTemplateHook) Levels() []logrus.Level {
	return []logrus.Level{logrus.WarnLevel}
}
func (h *independentInvalidTemplateHook) Fire(entry *logrus.Entry) error {
	if strings.Contains(entry.Message, "自定义模板更新失败") {
		select {
		case h.processed <- struct{}{}:
		default:
		}
	}
	return nil
}

func independentWatchedTemplate(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	Close()
	t.Chdir(t.TempDir())
	previousLogger := logger
	localLogger := logrus.New()
	hook := &independentInvalidTemplateHook{processed: make(chan struct{}, 8)}
	localLogger.AddHook(hook)
	logger = localLogger.WithField("module", "template")
	t.Cleanup(func() { Close(); logger = previousLogger })
	if err := os.Mkdir("template", 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("template", "independent.tmpl")
	if err := os.WriteFile(path, []byte(`{{define "independent-loader"}}working{{end}}`), 0600); err != nil {
		t.Fatal(err)
	}
	off := reloadSnapshot(map[string]interface{}{"template.enable": false})
	on := reloadSnapshot(map[string]interface{}{"template.enable": true})
	commit, _, err := PrepareRuntimeReload(off, on)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	return path, hook.processed
}

func independentTemplateText() string {
	tmpl := LoadTemplate("independent-loader")
	if tmpl == nil {
		return "<missing>"
	}
	msg := mmsg.NewMSG()
	if err := tmpl.Execute(msg, nil); err != nil {
		return "<execute-error>"
	}
	var text strings.Builder
	for _, element := range msg.Elements() {
		if segment, ok := element.(*adapter.TextSegment); ok {
			text.WriteString(segment.Content)
		}
	}
	return text.String()
}

func independentWaitTemplateText(t *testing.T, want string) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if independentTemplateText() == want {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("template did not become %q; current=%q", want, independentTemplateText())
		}
	}
}

func TestIndependentTemplateTruncateThenInvalidRetainsAndRecovers(t *testing.T) {
	path, invalidProcessed := independentWatchedTemplate(t)
	if independentTemplateText() != "working" {
		t.Fatal("initial external generation was not active")
	}
	// Real editors and os.WriteFile truncate before writing. Expand that short
	// empty interval so a per-event reloader demonstrably sees the partial save.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`{{define`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-invalidProcessed:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher never processed the invalid final source")
	}
	if got := independentTemplateText(); got != "working" {
		t.Fatalf("a transient empty file replaced the last valid generation: %q", got)
	}
	if err := os.WriteFile(path, []byte(`{{define "independent-loader"}}edited{{end}}`), 0600); err != nil {
		t.Fatal(err)
	}
	independentWaitTemplateText(t, "edited")
	tmp := filepath.Join("template", "replacement.tmp")
	if err := os.WriteFile(tmp, []byte(`{{define "independent-loader"}}atomic{{end}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	independentWaitTemplateText(t, "atomic")
}

func TestIndependentTemplatePendingEventsCannotPublishAfterDisableOrClose(t *testing.T) {
	for _, action := range []string{"disable", "close"} {
		t.Run(action, func(t *testing.T) {
			path, _ := independentWatchedTemplate(t)
			loaderMu.Lock()
			old := activeLoader
			loaderMu.Unlock()
			if err := os.WriteFile(path, []byte(`{{define "independent-loader"}}pending{{end}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if action == "disable" {
				on := reloadSnapshot(map[string]interface{}{"template.enable": true})
				off := reloadSnapshot(map[string]interface{}{"template.enable": false})
				commit, _, err := PrepareRuntimeReload(on, off)
				if err != nil {
					t.Fatal(err)
				}
				commit()
			} else {
				Close()
			}
			select {
			case <-old.done:
			default:
				t.Fatal("retired watcher was not drained before shutdown returned")
			}
			kept := LoadTemplate("independent-loader")
			if action == "disable" && kept != nil {
				t.Fatal("disabled external template remained active")
			}
			// Wait beyond the write-coalescing interval to catch orphan timers.
			time.Sleep(250 * time.Millisecond)
			if LoadTemplate("independent-loader") != kept {
				t.Fatal("a queued event published after its watcher retired")
			}
		})
	}
}

func TestIndependentTemplateStableEmptyAndDeletionRemainSupported(t *testing.T) {
	path, _ := independentWatchedTemplate(t)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	independentWaitTemplateText(t, "<missing>")
	if LoadTemplate("command.group.help.tmpl") == nil {
		t.Fatal("a deliberate empty override removed embedded defaults")
	}
	if err := os.WriteFile(path, []byte(`{{define "independent-loader"}}restored{{end}}`), 0600); err != nil {
		t.Fatal(err)
	}
	independentWaitTemplateText(t, "restored")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	independentWaitTemplateText(t, "<missing>")
	if err := os.WriteFile(path, []byte(`{{define "independent-loader"}}recreated{{end}}`), 0600); err != nil {
		t.Fatal(err)
	}
	independentWaitTemplateText(t, "recreated")
}
