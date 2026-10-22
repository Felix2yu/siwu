package probe

import _ "embed"

//go:embed picker.js
var pickerJS []byte

// PickerJS 返回注入预览页的框选脚本。
func PickerJS() []byte { return pickerJS }
