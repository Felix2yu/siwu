package extract

import (
	"fmt"
	"strconv"
	"strings"
)

// JSONPath 只支持一个实用子集：`$.a.b[0].c`（点分段 + 数组下标），
// 覆盖各类后台接口的实际结构。复杂过滤交给字段模板。

type jsonSeg struct {
	key string
	idx int // -1 表示不是下标段
}

func normalizeJSONPath(path string) ([]jsonSeg, error) {
	p := strings.TrimSpace(path)
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("JSON 路径必须以 $ 开头: %q", path)
	}
	p = strings.TrimPrefix(p, "$")
	p = strings.TrimPrefix(p, ".")
	var segs []jsonSeg
	if p == "" {
		return segs, nil
	}
	for _, raw := range strings.Split(p, ".") {
		if raw == "" {
			continue
		}
		key := raw
		idx := -1
		if i := strings.Index(raw, "["); i >= 0 {
			if !strings.HasSuffix(raw, "]") {
				return nil, fmt.Errorf("JSON 路径段格式错误: %q", raw)
			}
			key = raw[:i]
			n, err := strconv.Atoi(raw[i+1 : len(raw)-1])
			if err != nil {
				return nil, fmt.Errorf("JSON 路径下标非法: %q", raw)
			}
			idx = n
		}
		if key == "" && idx < 0 {
			return nil, fmt.Errorf("JSON 路径段为空: %q", raw)
		}
		segs = append(segs, jsonSeg{key: key, idx: idx})
	}
	return segs, nil
}

func jsonGet(doc any, path string) (any, error) {
	segs, err := normalizeJSONPath(path)
	if err != nil {
		return nil, err
	}
	cur := doc
	for _, seg := range segs {
		if seg.key != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, nil
			}
			v, ok := m[seg.key]
			if !ok {
				return nil, nil
			}
			cur = v
		}
		if seg.idx >= 0 {
			list, ok := cur.([]any)
			if !ok || seg.idx >= len(list) {
				return nil, nil
			}
			cur = list[seg.idx]
		}
	}
	return cur, nil
}
