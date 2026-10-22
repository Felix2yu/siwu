package probe

import (
	"strings"
	"testing"
)

// 预览是去掉脚本后的静态 DOM，站点自己的 loading 层不会关闭；拾取器必须兜底隐藏。
func TestPickerHidesFrozenLoadingOverlays(t *testing.T) {
	js := string(PickerJS())
	for _, want := range []string{
		".at-activity-indicator",
		".taro__toast",
		"加载中",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("picker.js 缺少加载层清理片段 %q", want)
		}
	}
}
