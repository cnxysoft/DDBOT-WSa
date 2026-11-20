package cfg

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrConfigConflict is safe to return to clients: it contains no paths or values.
var ErrConfigConflict = errors.New("配置文件已发生变化，请重新读取后重试")

// ConfigFileVersion captures the source bytes and existence used by a read/modify/
// write operation. Internal writers must still share GetConfigWriteMutex.
type ConfigFileVersion struct {
	path   string
	data   []byte
	exists bool
	mode   os.FileMode
}

// ReadConfigFileVersion distinguishes a missing file from an existing empty one.
// Missing files are allowed so the initialization wizard can create a new file.
func ReadConfigFileVersion(path string) (*ConfigFileVersion, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return &ConfigFileVersion{path: path}, nil
	}
	if err != nil {
		return nil, errors.New("读取配置文件失败")
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, errors.New("读取配置文件失败")
	}
	info, err := f.Stat()
	if err != nil {
		return nil, errors.New("读取配置文件状态失败")
	}
	return &ConfigFileVersion{path: path, data: data, exists: true, mode: info.Mode()}, nil
}

func (v *ConfigFileVersion) Bytes() []byte     { return bytes.Clone(v.data) }
func (v *ConfigFileVersion) Exists() bool      { return v.exists }
func (v *ConfigFileVersion) Mode() os.FileMode { return v.mode }

// ConfigETag identifies the exact unmasked source bytes, including formatting.
func ConfigETag(data []byte) string { return fmt.Sprintf(`"%x"`, sha256.Sum256(data)) }

func (v *ConfigFileVersion) ETag() string {
	if !v.exists {
		return ""
	}
	return ConfigETag(v.data)
}

// MatchesETag accepts HTTP If-Match's strong tag list or wildcard. Callers may
// allow an omitted header separately; present empty and weak tags never match.
func (v *ConfigFileVersion) MatchesETag(ifMatch string) bool {
	if strings.TrimSpace(ifMatch) == "" {
		return false
	}
	for _, tag := range strings.Split(ifMatch, ",") {
		tag = strings.TrimSpace(tag)
		if v.exists && (tag == "*" || tag == v.ETag()) {
			return true
		}
	}
	return false
}

func (v *ConfigFileVersion) check() error {
	data, err := os.ReadFile(v.path)
	if os.IsNotExist(err) {
		if v.exists {
			return ErrConfigConflict
		}
		return nil
	}
	if err != nil {
		return errors.New("无法检查配置文件，已拒绝写入")
	}
	if !v.exists || !bytes.Equal(data, v.data) {
		return ErrConfigConflict
	}
	return nil
}

// Persist must run inside Config.Save's persistence callback, after prepare.
// It checks the source before staging and again immediately before every rename.
// This detects edits during slow preparation, but cannot eliminate the final
// check/rename race with an external editor that does not share our write lock.
// Existing .bak files are preserved; the first backup is created exclusively.
func (v *ConfigFileVersion) Persist(data []byte, mode os.FileMode, backup bool) error {
	if err := v.check(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(v.path), filepath.Base(v.path)+".tmp-*")
	if err != nil {
		return errors.New("创建配置临时文件失败")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return errors.New("设置配置文件权限失败")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errors.New("写入配置临时文件失败")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errors.New("同步配置临时文件失败")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("关闭配置临时文件失败")
	}
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := v.check(); err != nil {
			return err
		}
		if backup && v.exists {
			if err := v.backupExclusive(mode); err != nil {
				return err
			}
			backup = false
			if err := v.check(); err != nil {
				return err
			}
		}
		if err := os.Rename(tmpName, v.path); err == nil {
			return nil
		}
		if attempt+1 < maxAttempts {
			time.Sleep(time.Duration(200*(1<<attempt)) * time.Millisecond)
		}
	}
	return errors.New("原子替换配置文件失败，已保留原配置")
}

func (v *ConfigFileVersion) backupExclusive(mode os.FileMode) error {
	f, err := os.OpenFile(v.path+".bak", os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return errors.New("创建配置备份失败，已拒绝写入")
	}
	created, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return errors.New("读取配置备份状态失败，已拒绝写入")
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		// Remove only our incomplete file. If another process has replaced it,
		// leave that backup alone; this is not a lock for external file editors.
		if current, err := os.Stat(v.path + ".bak"); err == nil && os.SameFile(created, current) {
			_ = os.Remove(v.path + ".bak")
		}
	}()
	if _, err := f.Write(v.data); err != nil {
		_ = f.Close()
		return errors.New("写入配置备份失败，已拒绝写入")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return errors.New("同步配置备份失败，已拒绝写入")
	}
	if err := f.Close(); err != nil {
		return errors.New("关闭配置备份失败，已拒绝写入")
	}
	complete = true
	return nil
}
