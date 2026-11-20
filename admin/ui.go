package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/sirupsen/logrus"
)

// WebUI 外观设置：背景图 / 字体颜色 / 背景位置。
//
// 这些是「展示层」配置，和机器人行为无关，单独放在配置文件同目录的 ui.json，
// 不写进 application.yaml，也不进日志。登录页也需要显示背景图，所以
// GET /api/v1/ui 允许匿名读取（只返回外观字段）；保存接口在本机匿名可用，
// 远端则必须带 admin.token。

type UISettings struct {
	BackgroundURL      string `json:"backgroundUrl"`
	BackgroundBlur     int    `json:"backgroundBlur"`
	BackgroundOpacity  int    `json:"backgroundOpacity"`
	BackgroundMask     int    `json:"backgroundMask"`
	BackgroundPosition string `json:"backgroundPosition"`
	FontCustom         bool   `json:"fontCustom"`
	FontColor          string `json:"fontColor"`
}

func defaultUISettings() UISettings {
	return UISettings{
		BackgroundBlur:     0,
		BackgroundOpacity:  85,
		BackgroundMask:     35,
		BackgroundPosition: "center",
	}
}

// uiSettingsPath 与 application.yaml 同目录，避免配置在 ./config/ 时写到别处。
func uiSettingsPath() string {
	return filepath.Join(filepath.Dir(cfg.ConfigFilePath()), "ui.json")
}

var uiFontColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

func validateUISettings(s UISettings) error {
	if s.BackgroundBlur < 0 || s.BackgroundBlur > 24 {
		return fmt.Errorf("背景模糊必须在 0-24 之间")
	}
	if s.BackgroundOpacity < 20 || s.BackgroundOpacity > 100 {
		return fmt.Errorf("背景不透明度必须在 20-100 之间")
	}
	if s.BackgroundMask < 0 || s.BackgroundMask > 80 {
		return fmt.Errorf("背景遮罩必须在 0-80 之间")
	}
	switch s.BackgroundPosition {
	case "", "center", "top", "bottom", "left", "right":
	default:
		return fmt.Errorf("不支持的背景位置 %q", s.BackgroundPosition)
	}
	if s.BackgroundURL != "" {
		if strings.HasPrefix(s.BackgroundURL, "data:image/") {
			// 本地图片压缩后是 data URL；限制大小，避免 ui.json 被写爆
			if len(s.BackgroundURL) > 6*1024*1024 {
				return fmt.Errorf("本地背景图片过大，请换一张小一点的图片")
			}
		} else {
			u, err := url.Parse(s.BackgroundURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("背景图必须是 http(s) 图床直链")
			}
		}
	}
	if s.FontCustom && strings.TrimSpace(s.FontColor) == "" {
		return fmt.Errorf("启用了自定义字体颜色时必须填写颜色")
	}
	if s.FontColor != "" && !uiFontColorRe.MatchString(s.FontColor) {
		return fmt.Errorf("字体颜色必须是 #RRGGBB 格式")
	}
	return nil
}

func loadUISettings() (UISettings, error) {
	settings := defaultUISettings()
	data, err := os.ReadFile(uiSettingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return settings, err
	}
	// 先填默认值再 Unmarshal：缺失字段保留默认，显式 0 / false 正常生效
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	if settings.BackgroundPosition == "" {
		settings.BackgroundPosition = "center"
	}
	return settings, nil
}

// handleUIGet GET /api/v1/ui —— 登录页也要读，允许匿名，只返回外观字段。
func (s *Server) handleUIGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}
	settings, err := loadUISettings()
	if err != nil {
		// 文件坏了不能让登录页白屏：回默认值，同时把原因写日志
		logrus.Warnf("读取 ui.json 失败，使用默认外观: %v", err)
		settings = defaultUISettings()
	}
	_ = json.NewEncoder(w).Encode(settings)
}

// handleUISave POST /api/v1/ui/save —— 写入 ui.json。
// 本机匿名可用（登录页也能改），远端必须带 admin.token（见 guardLocalOrAuth）。
func (s *Server) handleUISave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var settings UISettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid ui settings"})
		return
	}
	if settings.BackgroundOpacity == 0 {
		settings.BackgroundOpacity = 85
	}
	if settings.BackgroundPosition == "" {
		settings.BackgroundPosition = "center"
	}
	if err := validateUISettings(settings); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to marshal ui settings"})
		return
	}
	data = append(data, '\n')
	if err := writeFileAtomic(uiSettingsPath(), data, 0600); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "写入 ui.json 失败: " + err.Error()})
		return
	}
	logrus.Infof("WebUI 外观设置已写入 %s", uiSettingsPath())
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}
