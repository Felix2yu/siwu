// Package events 是进程内事件总线：调度与流水线发布，SSE 端点订阅。
// 发布永不阻塞（缓冲区满就丢弃），监控系统的实时性不值得为主流程引入背压。
package events

import (
	"sync"
	"time"
)

// Event 是一条可推送到前端的事件。
type Event struct {
	Type       string `json:"type"` // run.start | run.finish | notify.ok | notify.fail
	At         int64  `json:"at"`
	SourceID   int64  `json:"source_id,omitempty"`
	SourceName string `json:"source_name,omitempty"`
	Slug       string `json:"slug,omitempty"`
	RunID      int64  `json:"run_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Found      int    `json:"found,omitempty"`
	New        int    `json:"new,omitempty"`
	Updated    int    `json:"updated,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	Message    string `json:"message,omitempty"`
}

// Bus 是极简的 fan-out 总线。
type Bus struct {
	mu     sync.Mutex
	next   int
	subs   map[int]chan Event
	closed bool
}

// New 构造总线。
func New() *Bus { return &Bus{subs: map[int]chan Event{}} }

// Publish 广播事件；某个订阅者处理不过来时丢弃它自己的这条。
func (b *Bus) Publish(e Event) {
	if e.At == 0 {
		e.At = time.Now().Unix()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe 返回事件通道与退订函数。通道带 64 条缓冲。
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
}

// Subscribers 返回当前订阅者数量（测试用）。
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
