// Package cronexpr 解析标准 5 字段 cron 并推算下次触发时刻。
//
// 支持：`*`、`*/n`、`a`、`a-b`、`a-b/n`、逗号列表；周字段 0/7 都表示周日。
// 额外支持 `@every 1h30m`、`@hourly`、`@daily`、`@weekly` 与 `@monthly` 简写。
// 语义遵循 Vixie cron：**日与周都被限定时取并集**（OR），其余字段取交集。
package cronexpr

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule 是解析后的 cron 表达式。
type Schedule struct {
	expr     string
	minutes  uint64 // bit 0..59
	hours    uint32 // bit 0..23
	days     uint32 // bit 1..31
	months   uint16 // bit 1..12
	weeks    uint8  // bit 0..6（0=周日）
	dayStar  bool
	weekStar bool
	every    time.Duration // @every 形式
}

// String 返回原始表达式。
func (s Schedule) String() string { return s.expr }

// IsEvery 表示这是 @every 形式（按固定时长，而不是日历对齐）。
func (s Schedule) IsEvery() bool { return s.every > 0 }

// Every 返回 @every 的周期，非 @every 形式返回 0。
func (s Schedule) Every() time.Duration { return s.every }

// Parse 解析表达式。
func Parse(expr string) (Schedule, error) {
	raw := strings.TrimSpace(expr)
	out := Schedule{expr: raw}
	if raw == "" {
		return out, fmt.Errorf("cron 表达式为空")
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "@") {
		sch, err := parseAlias(out, lower)
		if err != nil {
			return out, err
		}
		// 保留用户写的原始表达式，便于回显
		sch.expr = raw
		return sch, nil
	}
	fields := strings.Fields(raw)
	if len(fields) != 5 {
		return out, fmt.Errorf("cron 需要 5 个字段（分 时 日 月 周），实际 %d 个", len(fields))
	}
	var err error
	if out.minutes, err = parseField(fields[0], 0, 59); err != nil {
		return out, fmt.Errorf("分钟字段: %w", err)
	}
	h, err := parseField(fields[1], 0, 23)
	if err != nil {
		return out, fmt.Errorf("小时字段: %w", err)
	}
	out.hours = uint32(h)
	d, err := parseField(fields[2], 1, 31)
	if err != nil {
		return out, fmt.Errorf("日字段: %w", err)
	}
	out.days = uint32(d)
	mo, err := parseField(fields[3], 1, 12)
	if err != nil {
		return out, fmt.Errorf("月字段: %w", err)
	}
	out.months = uint16(mo)
	// 周字段允许 0-7，7 与 0 都表示周日
	w, err := parseField(fields[4], 0, 7)
	if err != nil {
		return out, fmt.Errorf("周字段: %w", err)
	}
	if w&(1<<7) != 0 {
		w |= 1
	}
	out.weeks = uint8(w & 0x7f)
	out.dayStar = strings.HasPrefix(fields[2], "*")
	out.weekStar = strings.HasPrefix(fields[4], "*")
	return out, nil
}

func parseAlias(s Schedule, expr string) (Schedule, error) {
	switch {
	case strings.HasPrefix(expr, "@every "):
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expr, "@every ")))
		if err != nil {
			return s, fmt.Errorf("@every 时长非法: %w", err)
		}
		if d < time.Minute {
			return s, fmt.Errorf("@every 最短 1m")
		}
		s.every = d
		return s, nil
	case expr == "@hourly":
		return Parse("0 * * * *")
	case expr == "@daily" || expr == "@midnight":
		return Parse("0 0 * * *")
	case expr == "@weekly":
		return Parse("0 0 * * 0")
	case expr == "@monthly":
		return Parse("0 0 1 * *")
	case expr == "@yearly" || expr == "@annually":
		return Parse("0 0 1 1 *")
	default:
		return s, fmt.Errorf("不认识的 cron 简写 %q", expr)
	}
}

// parseField 把单个字段解析成位图；返回 uint64 以便分钟字段用满 60 位。
func parseField(field string, lo, hi int) (uint64, error) {
	if field == "" {
		return 0, fmt.Errorf("字段为空")
	}
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("逗号列表里有空项")
		}
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			sv := strings.TrimSpace(part[i+1:])
			n, err := strconv.Atoi(sv)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("步长 %q 非法", sv)
			}
			step = n
			part = part[:i]
		}
		start, end := lo, hi
		switch {
		case part == "*" || part == "":
			// 全区间
		case strings.Contains(part, "-"):
			ab := strings.SplitN(part, "-", 2)
			a, err1 := strconv.Atoi(strings.TrimSpace(ab[0]))
			b, err2 := strconv.Atoi(strings.TrimSpace(ab[1]))
			if err1 != nil || err2 != nil {
				return 0, fmt.Errorf("区间 %q 非法", part)
			}
			if a > b {
				return 0, fmt.Errorf("区间 %q 起点大于终点", part)
			}
			start, end = a, b
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return 0, fmt.Errorf("%q 不是数字", part)
			}
			start, end = n, n
			if step > 1 {
				// `5/10` 等价 `5-最大值/10`
				end = hi
			}
		}
		if start < lo || end > hi {
			return 0, fmt.Errorf("取值超出 %d-%d", lo, hi)
		}
		for v := start; v <= end; v += step {
			bits |= 1 << uint(v)
		}
	}
	if bits == 0 {
		return 0, fmt.Errorf("没有任何有效取值")
	}
	return bits, nil
}

// Next 返回严格晚于 from 的下一次触发时刻（按 from 的时区）。
func (s Schedule) Next(from time.Time) time.Time {
	if s.every > 0 {
		return from.Add(s.every)
	}
	t := from.Truncate(time.Minute).Add(time.Minute)
	// 最多向后看 5 年（含闰年），足够覆盖 2 月 29 日类表达式
	limit := from.AddDate(5, 0, 0)
	loc := from.Location()
	for t.Before(limit) {
		if s.months&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
			continue
		}
		if !s.dayMatch(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			continue
		}
		if s.hours&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Add(time.Hour)
			continue
		}
		if s.minutes&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// dayMatch 处理「日与周同时限定取并集」的标准 cron 语义。
func (s Schedule) dayMatch(t time.Time) bool {
	dayOK := s.days&(1<<uint(t.Day())) != 0
	weekOK := s.weeks&(1<<uint(t.Weekday())) != 0
	switch {
	case s.dayStar && s.weekStar:
		return true
	case s.dayStar:
		return weekOK
	case s.weekStar:
		return dayOK
	default:
		return dayOK || weekOK
	}
}
