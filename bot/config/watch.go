package config

import (
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// WatchConfig watches the directory, so atomic replacement and remove/recreate never
// detach the listener from the file. Periodic reconciliation also covers missed events.
func (c *Config) WatchConfig() {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.stop != nil {
		return
	}
	path, err := filepath.Abs(c.ConfigFileUsed())
	if err != nil {
		logrus.WithError(err).Error("无法监听配置文件")
		return
	}
	w, err := fsnotify.NewWatcher()
	if err == nil {
		err = w.Add(filepath.Dir(path))
	}
	if err != nil {
		if w != nil {
			_ = w.Close()
		}
		logrus.WithError(err).Warn("配置文件监听不可用，使用定期检查")
		w = nil
	}
	c.stop, c.done = make(chan struct{}), make(chan struct{})
	c.mu.Lock()
	c.status.Watching = true
	c.mu.Unlock()
	go c.watch(path, w, c.stop, c.done)
}

func (c *Config) watch(path string, w *fsnotify.Watcher, stop, done chan struct{}) {
	defer close(done)
	if w != nil {
		defer w.Close()
	}
	var events <-chan fsnotify.Event
	var errs <-chan error
	if w != nil {
		events, errs = w.Events, w.Errors
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	debounce := time.NewTimer(time.Hour)
	if !debounce.Stop() {
		<-debounce.C
	}
	defer debounce.Stop()
	lastError := ""
	reload := func() {
		if err := c.Reload(); err != nil {
			message := c.Status().LastError
			if message != lastError {
				logrus.Warn(message)
			}
			lastError = message
		} else {
			lastError = ""
		}
	}
	for {
		select {
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			name, err := filepath.Abs(e.Name)
			if err == nil && name == path {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(100 * time.Millisecond)
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			} else {
				logrus.Warn("配置监听发生错误，将定期检查文件")
			}
		case <-debounce.C:
			reload()
		case <-ticker.C:
			reload()
		case <-stop:
			return
		}
	}
}

func (c *Config) StopWatching() {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	c.opMu.Lock()
	stop, done := c.stop, c.done
	if stop != nil {
		close(stop)
		c.stop, c.done = nil, nil
	}
	c.opMu.Unlock()
	if done != nil {
		<-done
	}
	c.mu.Lock()
	c.status.Watching = false
	c.mu.Unlock()
}
