package model

import (
	"math/rand/v2"
	"time"
)

// 调度精度约定：本应用不追求秒级精度，检测间隔最少 1 分钟，漂移也只按整分钟走。
// 这样落点不会固定在整点（一眼就是机器定时），「下次运行」也仍然好读。
const (
	// MinIntervalSec 是允许的最小检测间隔。
	MinIntervalSec = 60
	// DefaultIntervalSec 是内置默认间隔（60 分钟，界面上按分钟填写）。
	DefaultIntervalSec = 3600
	// MaxIntervalDriftSec 是固定间隔允许的最大漂移窗口（10 分钟）。
	MaxIntervalDriftSec = 600
	// MaxCalendarDriftSec 是日历型调度（cron）允许的最大延后窗口（5 分钟）。
	MaxCalendarDriftSec = 300
)

// DriftWindow 返回漂移窗口（秒）：周期的 1/10，向下取整到整分钟，并受 maxSec 限制。
// 不足 1 分钟一律返回 0——分钟级的短间隔本身不会形成「整点」特征，不值得抖。
func DriftWindow(periodSec, maxSec int) int {
	if periodSec <= 0 {
		return 0
	}
	w := periodSec / 10
	if maxSec > 0 && w > maxSec {
		w = maxSec
	}
	w -= w % 60
	if w < 60 {
		return 0
	}
	return w
}

// DriftSec 返回 ±窗口 内的整分钟偏移（秒），用于固定间隔。
// 每次落点都不同，间隔在均值附近浮动，长时间看不会回到固定时刻。
func DriftSec(periodSec, maxSec int) int {
	w := DriftWindow(periodSec, maxSec)
	if w == 0 {
		return 0
	}
	n := w / 60
	return (int(rand.Int64N(int64(2*n+1))) - n) * 60
}

// DelaySec 返回 [0, 窗口] 内的整分钟延后（秒），用于日历型调度：
// 只向后偏，既不会早于用户写下的时刻，也不会跨过下一次日历时刻。
func DelaySec(periodSec, maxSec int) int {
	w := DriftWindow(periodSec, maxSec)
	if w == 0 {
		return 0
	}
	n := w / 60
	return int(rand.Int64N(int64(n+1))) * 60
}

// ClampInterval 把间隔收敛到不短于 1 分钟；0 或负数退回默认间隔。
func ClampInterval(sec int) int {
	if sec <= 0 {
		return DefaultIntervalSec
	}
	if sec < MinIntervalSec {
		return MinIntervalSec
	}
	return sec
}

// IntervalDrift 是 DriftSec 的 time.Duration 版本。
func IntervalDrift(intervalSec int) time.Duration {
	return time.Duration(DriftSec(intervalSec, MaxIntervalDriftSec)) * time.Second
}
