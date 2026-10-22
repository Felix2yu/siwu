package model

// Settings 是整个实例共享的偏好设置。主题属于设备偏好，不放在这里。
type Settings struct {
	Defaults SourceDefaults `json:"defaults"`
}

// SourceDefaults 是新建监控源时套用的默认值，只影响之后创建的源。
type SourceDefaults struct {
	IntervalSec    int    `json:"interval_sec"`
	MaxItems       int    `json:"max_items"`
	RetentionDays  int    `json:"retention_days"`
	UpdateMode     string `json:"update_mode"`
	ArticleEnabled bool   `json:"article_enabled"`
}

// DefaultSettings 返回与前端 blankSource 一致的内置默认值。
func DefaultSettings() Settings {
	return Settings{Defaults: SourceDefaults{
		IntervalSec: 3600,
		MaxItems:    50,
		UpdateMode:  "update",
	}}
}

// Normalize 收敛越界值，避免坏值落库后让新建源行为异常。
func (s *Settings) Normalize() {
	d := &s.Defaults
	if d.IntervalSec <= 0 {
		d.IntervalSec = 3600
	}
	if d.MaxItems <= 0 {
		d.MaxItems = 50
	}
	if d.MaxItems > 500 {
		d.MaxItems = 500
	}
	if d.RetentionDays < 0 {
		d.RetentionDays = 0
	}
	switch d.UpdateMode {
	case "ignore", "update", "archive":
	default:
		d.UpdateMode = "update"
	}
}
