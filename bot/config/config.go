package config

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Config struct {
	// Viper is retained for construction compatibility. Use Config methods after initialization.
	Viper       *viper.Viper
	source      *viper.Viper
	mu          sync.RWMutex
	opMu        sync.Mutex
	watchMu     sync.Mutex
	path        string
	raw         []byte
	desired     *viper.Viper
	restartBase *viper.Viper
	overrides   map[string]interface{}
	validate    func(*Snapshot) error
	hot         func(string) bool
	onChange    func(fsnotify.Event)
	prepare     func(previous, next *Snapshot) (commit func(), abort func(), err error)
	status      ReloadStatus
	stop        chan struct{}
	done        chan struct{}
	closing     bool
}

type ReloadStatus struct {
	Revision        uint64    `json:"revision"`
	AppliedAt       time.Time `json:"appliedAt"`
	RestartRequired []string  `json:"restartRequired"`
	LastError       string    `json:"lastError,omitempty"`
	Watching        bool      `json:"watching"`
}

// PrepareError means a live-resource candidate was rejected before persistence.
// Resource owners must provide a message without secrets or remote response bodies.
type PrepareError struct{ Err error }

func (e *PrepareError) Error() string { return e.Err.Error() }
func (e *PrepareError) Unwrap() error { return e.Err }

// Snapshot retains one complete generation. Its getters never expose mutable configuration maps.
type Snapshot struct {
	v   *viper.Viper
	raw []byte
}

func (s *Snapshot) Get(key string) interface{}           { return clone(s.v.Get(key)) }
func (s *Snapshot) GetString(key string) string          { return s.v.GetString(key) }
func (s *Snapshot) GetBool(key string) bool              { return s.v.GetBool(key) }
func (s *Snapshot) GetInt(key string) int                { return s.v.GetInt(key) }
func (s *Snapshot) GetInt64(key string) int64            { return s.v.GetInt64(key) }
func (s *Snapshot) GetDuration(key string) time.Duration { return s.v.GetDuration(key) }
func (s *Snapshot) IsSet(key string) bool                { return s.v.IsSet(key) }
func (s *Snapshot) GetStringSlice(key string) []string {
	return append([]string(nil), s.v.GetStringSlice(key)...)
}
func (s *Snapshot) UnmarshalKey(key string, out interface{}) error { return s.v.UnmarshalKey(key, out) }
func (s *Snapshot) AllKeys() []string                              { return s.v.AllKeys() }

// RawYAML returns accepted source bytes, including any settings pending restart.
func (s *Snapshot) RawYAML() []byte { return bytes.Clone(s.raw) }

var GlobalConfig = &Config{Viper: viper.New()}

func clone(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		m := make(map[string]interface{}, len(v))
		for k, x := range v {
			m[k] = clone(x)
		}
		return m
	case []interface{}:
		a := make([]interface{}, len(v))
		for i, x := range v {
			a[i] = clone(x)
		}
		return a
	case []string:
		return append([]string(nil), v...)
	case map[string]string:
		m := make(map[string]string, len(v))
		for k, x := range v {
			m[k] = x
		}
		return m
	default:
		// Runtime setters may also receive typed maps/slices, such as []int64.
		if value != nil {
			rv := reflect.ValueOf(value)
			if rv.Kind() == reflect.Map {
				if rv.IsNil() {
					return value
				}
				out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
				iter := rv.MapRange()
				for iter.Next() {
					copied := clone(iter.Value().Interface())
					cv := reflect.Zero(rv.Type().Elem())
					if copied != nil {
						cv = reflect.ValueOf(copied)
					}
					out.SetMapIndex(iter.Key(), cv)
				}
				return out.Interface()
			}
			if rv.Kind() == reflect.Slice {
				if rv.IsNil() {
					return value
				}
				out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
				for i := 0; i < rv.Len(); i++ {
					copied := clone(rv.Index(i).Interface())
					if copied != nil {
						out.Index(i).Set(reflect.ValueOf(copied))
					}
				}
				return out.Interface()
			}
		}
		return value
	}
}

func fromSettings(settings map[string]interface{}) *viper.Viper {
	v := viper.New()
	_ = v.MergeConfigMap(clone(settings).(map[string]interface{}))
	return v
}

func (c *Config) Snapshot() *Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return &Snapshot{c.Viper, c.raw}
}
func (c *Config) Get(k string) interface{}           { return c.Snapshot().Get(k) }
func (c *Config) GetString(k string) string          { return c.Snapshot().GetString(k) }
func (c *Config) GetBool(k string) bool              { return c.Snapshot().GetBool(k) }
func (c *Config) GetInt(k string) int                { return c.Snapshot().GetInt(k) }
func (c *Config) GetInt64(k string) int64            { return c.Snapshot().GetInt64(k) }
func (c *Config) GetDuration(k string) time.Duration { return c.Snapshot().GetDuration(k) }
func (c *Config) IsSet(k string) bool                { return c.Snapshot().IsSet(k) }
func (c *Config) GetStringSlice(k string) []string   { return c.Snapshot().GetStringSlice(k) }
func (c *Config) UnmarshalKey(k string, out interface{}) error {
	return c.Snapshot().UnmarshalKey(k, out)
}
func (c *Config) SetConfigName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sourceLocked().SetConfigName(name)
}
func (c *Config) SetConfigType(kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sourceLocked().SetConfigType(kind)
}
func (c *Config) AddConfigPath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sourceLocked().AddConfigPath(path)
}
func (c *Config) SetConfigFile(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path = path
	c.sourceLocked().SetConfigFile(path)
}

// sourceLocked retains search paths and format metadata across snapshot replacements.
// The caller holds mu. The source is never used as a published runtime generation.
func (c *Config) sourceLocked() *viper.Viper {
	if c.source == nil {
		c.source = c.Viper
		c.Viper = fromSettings(c.Viper.AllSettings())
	}
	return c.source
}
func (c *Config) ConfigFileUsed() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.path != "" {
		return c.path
	}
	if c.source != nil {
		return c.source.ConfigFileUsed()
	}
	return c.Viper.ConfigFileUsed()
}

// Set publishes a runtime override without modifying an existing snapshot. A later explicit
// change to the same file key supersedes it; unrelated file changes retain it.
func (c *Config) Set(key string, value interface{}) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.closing {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key = strings.ToLower(key)
	source := c.sourceLocked()
	if c.overrides == nil {
		c.overrides = make(map[string]interface{})
	}
	c.overrides[key] = clone(value)
	if c.path == "" {
		c.path = source.ConfigFileUsed()
	}
	v := fromSettings(c.Viper.AllSettings())
	v.Set(key, clone(value))
	// Normalize overrides into the config map. Viper's Get(section) otherwise
	// returns only that section's last Set override, hiding sibling fields.
	c.Viper = fromSettings(v.AllSettings())
}

// MarkApplied is reserved for owners that have actually updated a live resource.
// Merely setting a runtime credential does not prove that a connection was rebuilt.
func (c *Config) MarkApplied(keys ...string) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.closing {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restartBase == nil {
		return
	}
	base := fromSettings(c.restartBase.AllSettings())
	for _, key := range keys {
		base.Set(key, clone(c.Viper.Get(key)))
	}
	c.restartBase = base
	var pending []string
	_ = reconcile(base.AllSettings(), c.desired.AllSettings(), "", c.hot, &pending)
	sort.Strings(pending)
	c.status.RestartRequired = pending
}

func (c *Config) ConfigureReload(validate func(*Snapshot) error, hot func(string) bool) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.validate, c.hot = validate, hot
}
func (c *Config) OnConfigChange(fn func(fsnotify.Event)) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.onChange = fn
}

// OnPrepareReload prepares live resources before accepting a candidate. A failed
// preparation or persistence aborts the prepared resources. Commit runs after the
// generation is published and must not fail or call Config mutation methods.
// Unchanged source bytes do not prepare resources again.
func (c *Config) OnPrepareReload(fn func(previous, next *Snapshot) (commit func(), abort func(), err error)) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.prepare = fn
}

func (c *Config) prepareReload(desired *viper.Viper, data []byte) (func(), func(), error) {
	if c.prepare == nil {
		return nil, nil, nil
	}
	c.mu.RLock()
	if c.desired == nil || bytes.Equal(c.raw, data) {
		c.mu.RUnlock()
		return nil, nil, nil
	}
	previous := &Snapshot{c.Viper, c.raw}
	var pending []string
	effective := fromSettings(reconcile(c.Viper.AllSettings(), desired.AllSettings(), "", c.hot, &pending))
	for key, value := range c.overrides {
		if reflect.DeepEqual(c.desired.Get(key), desired.Get(key)) && !reflect.DeepEqual(value, desired.Get(key)) {
			effective.Set(key, clone(value))
		}
	}
	c.mu.RUnlock()
	commit, abort, err := c.prepare(previous, &Snapshot{fromSettings(effective.AllSettings()), bytes.Clone(data)})
	if err != nil {
		err = &PrepareError{Err: err}
	}
	return commit, abort, err
}
func (c *Config) Status() ReloadStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.status
	s.RestartRequired = append([]string{}, s.RestartRequired...)
	return s
}

func (c *Config) parse(data []byte) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return nil, errors.New("配置不是合法 YAML，请检查文件格式")
	}
	if c.validate != nil {
		if err := c.validate(&Snapshot{v, data}); err != nil {
			return nil, err
		}
	}
	return v, nil
}
func (c *Config) Validate(data []byte) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	_, err := c.parse(data)
	return err
}

func (c *Config) ReadInConfig() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.desired != nil {
		return c.reloadLocked()
	}
	c.mu.Lock()
	var err error
	if c.path == "" {
		source := c.sourceLocked()
		err = source.ReadInConfig()
		if err == nil {
			c.path = source.ConfigFileUsed()
		}
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(c.ConfigFileUsed())
	if err != nil {
		return err
	}
	v, err := c.parse(data)
	if err != nil {
		return err
	}
	c.publish(v, data, true, nil)
	return nil
}

func (c *Config) Reload() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	return c.reloadLocked()
}

func (c *Config) reloadLocked() error {
	if c.closing {
		return errors.New("配置管理器正在关闭")
	}
	data, err := os.ReadFile(c.ConfigFileUsed())
	if err == nil {
		var v *viper.Viper
		v, err = c.parse(data)
		if err == nil {
			var commit, abort func()
			commit, abort, err = c.prepareReload(v, data)
			if err == nil {
				c.publish(v, data, false, commit)
			} else if abort != nil {
				abort()
			}
		}
	}
	if err != nil {
		c.mu.Lock()
		c.status.LastError = "配置重载失败，保留上次有效配置：" + safeError(err)
		c.mu.Unlock()
	}
	return err
}
func safeError(err error) string {
	if _, ok := err.(*os.PathError); ok {
		return "无法读取配置文件"
	}
	return err.Error()
}

// Save validates exactly the bytes that will be persisted, and serializes publication with
// watcher reloads. persist must only write the file; it must not invoke Config mutations.
func (c *Config) Save(data []byte, persist func() error) error {
	_, err := c.SaveAndStatus(data, persist)
	return err
}

// SaveAndStatus returns the status of this save before another reload can intervene.
func (c *Config) SaveAndStatus(data []byte, persist func() error) (ReloadStatus, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.closing {
		return ReloadStatus{}, errors.New("配置管理器正在关闭")
	}
	v, err := c.parse(data)
	if err != nil {
		return ReloadStatus{}, err
	}
	commit, abort, err := c.prepareReload(v, data)
	if err != nil {
		if abort != nil {
			abort()
		}
		return ReloadStatus{}, err
	}
	if err := persist(); err != nil {
		if abort != nil {
			abort()
		}
		return ReloadStatus{}, err
	}
	c.publish(v, data, false, commit)
	return c.Status(), nil
}

// BeginShutdown drains accepted transactions and rejects new publications before
// listeners, workers and their backing storage are retired.
func (c *Config) BeginShutdown() {
	c.StopWatching()
	c.opMu.Lock()
	c.closing = true
	c.opMu.Unlock()
}

// reconcile recursively preserves cold settings, including removed keys and map/scalar changes.
func reconcile(old, desired map[string]interface{}, prefix string, hot func(string) bool, pending *[]string) map[string]interface{} {
	out := make(map[string]interface{})
	keys := make(map[string]bool)
	for k := range old {
		keys[k] = true
	}
	for k := range desired {
		keys[k] = true
	}
	for k := range keys {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		a, aok := old[k]
		b, bok := desired[k]
		if reflect.DeepEqual(a, b) && aok == bok {
			if bok {
				out[k] = clone(b)
			}
			continue
		}
		am, ais := a.(map[string]interface{})
		bm, bis := b.(map[string]interface{})
		if (ais || !aok) && (bis || !bok) && (ais || bis) {
			m := reconcile(am, bm, path, hot, pending)
			if len(m) > 0 || bok {
				out[k] = m
			}
			continue
		}
		if hot == nil || hot(path) {
			if bok {
				out[k] = clone(b)
			}
		} else {
			*pending = append(*pending, path)
			if aok {
				out[k] = clone(a)
			}
		}
	}
	return out
}

func (c *Config) publish(desired *viper.Viper, data []byte, initial bool, commit func()) {
	c.mu.Lock()
	if !initial && bytes.Equal(c.raw, data) && c.desired != nil {
		c.status.LastError = ""
		c.mu.Unlock()
		return
	}
	var pending []string
	effective := fromSettings(desired.AllSettings())
	if !initial && c.desired != nil {
		settings := reconcile(c.Viper.AllSettings(), desired.AllSettings(), "", c.hot, &pending)
		effective = fromSettings(settings)
	}
	if initial || c.restartBase == nil {
		c.restartBase = fromSettings(desired.AllSettings())
	}
	for key, value := range c.overrides {
		if c.desired != nil && !reflect.DeepEqual(c.desired.Get(key), desired.Get(key)) || reflect.DeepEqual(value, desired.Get(key)) {
			delete(c.overrides, key)
		} else {
			effective.Set(key, clone(value))
		}
	}
	// Compare cold changes with the configuration actually applied by resource owners,
	// rather than runtime overrides used by login/account displays.
	pending = nil
	_ = reconcile(c.restartBase.AllSettings(), desired.AllSettings(), "", c.hot, &pending)
	sort.Strings(pending)
	c.Viper, c.desired, c.raw = fromSettings(effective.AllSettings()), desired, bytes.Clone(data)
	c.mu.Unlock()
	if commit != nil {
		commit()
	}
	if !initial && c.onChange != nil {
		c.onChange(fsnotify.Event{Name: c.ConfigFileUsed(), Op: fsnotify.Write})
	}
	c.mu.Lock()
	c.status.Revision++
	c.status.RestartRequired = pending
	c.status.LastError = ""
	c.status.AppliedAt = time.Now().UTC()
	c.mu.Unlock()
}

// Init 使用 ./application.yaml 初始化全局配置
func Init() {
	GlobalConfig.SetConfigName("application")
	GlobalConfig.SetConfigType("yaml")
	GlobalConfig.AddConfigPath(".")
	GlobalConfig.AddConfigPath("./config")

	err := GlobalConfig.ReadInConfig()
	if err != nil {
		logrus.WithField("config", "GlobalConfig").WithError(err).Fatal("unable to read global config")
	}
}
