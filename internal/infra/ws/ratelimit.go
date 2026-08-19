// Package ws 提供 WebSocket 传输层：单连接读写 goroutine、JSON 信封编解码、
// per-IP 连接限速 / per-conn 消息限速 / 聊天限速（见技术设计 5.1 / 5.3）。
package ws

import (
	"sync"
	"time"
)

// TokenBucket 简单令牌桶（非线程安全，调用方持锁）
type TokenBucket struct {
	capacity float64
	tokens   float64
	refill   float64 // 每秒补充的令牌数
	last     time.Time
}

// NewTokenBucket 创建令牌桶；capacity 为桶容量，perSecond 为每秒补充速率
func NewTokenBucket(capacity int, perSecond float64) *TokenBucket {
	return &TokenBucket{
		capacity: float64(capacity),
		tokens:   float64(capacity),
		refill:   perSecond,
		last:     time.Now(),
	}
}

// Allow 消耗一个令牌；不足返回 false
func (tb *TokenBucket) Allow() bool {
	now := time.Now()
	tb.tokens += now.Sub(tb.last).Seconds() * tb.refill
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.last = now
	if tb.tokens < 1 {
		return false
	}
	tb.tokens--
	return true
}

// IPRateLimiter per-IP 连接限速 + 超限封禁（对齐原 security.RateLimiter）
type IPRateLimiter struct {
	mu           sync.Mutex
	buckets      map[string]*TokenBucket
	bannedUntil  map[string]time.Time
	maxPerSecond int
	maxPerMinute int
	banDuration  time.Duration
}

// NewIPRateLimiter 创建 per-IP 限速器
func NewIPRateLimiter(maxPerSecond, maxPerMinute int, banDuration time.Duration) *IPRateLimiter {
	rl := &IPRateLimiter{
		buckets:      make(map[string]*TokenBucket),
		bannedUntil:  make(map[string]time.Time),
		maxPerSecond: maxPerSecond,
		maxPerMinute: maxPerMinute,
		banDuration:  banDuration,
	}
	go rl.cleanupLoop()
	return rl
}

// Allow 检查某 IP 是否允许新建连接；超限则封禁
func (rl *IPRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if until, ok := rl.bannedUntil[ip]; ok {
		if time.Now().Before(until) {
			return false
		}
		delete(rl.bannedUntil, ip)
	}

	b, ok := rl.buckets[ip]
	if !ok {
		// 桶容量取分钟上限，补充速率取秒上限（二者取严）
		b = NewTokenBucket(rl.maxPerMinute, float64(rl.maxPerSecond))
		rl.buckets[ip] = b
	}

	if !b.Allow() {
		rl.bannedUntil[ip] = time.Now().Add(rl.banDuration)
		delete(rl.buckets, ip)
		return false
	}
	return true
}

// cleanupLoop 定期清理空闲令牌桶与过期封禁
func (rl *IPRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		// 清理过期封禁记录
		for ip, until := range rl.bannedUntil {
			if now.After(until) {
				delete(rl.bannedUntil, ip)
			}
		}
		// 清理空闲令牌桶：令牌已满（无消耗）的 IP 桶，防止长期运行内存泄漏
		for ip, b := range rl.buckets {
			if b.tokens >= b.capacity {
				delete(rl.buckets, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// MessageLimiter per-conn 消息限速（超限计数，由调用方决定是否断连）
type MessageLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*TokenBucket
	warnings  map[string]int
	maxPerSec int
}

// NewMessageLimiter 创建 per-conn 消息限速器
func NewMessageLimiter(maxPerSecond int) *MessageLimiter {
	return &MessageLimiter{
		buckets:   make(map[string]*TokenBucket),
		warnings:  make(map[string]int),
		maxPerSec: maxPerSecond,
	}
}

// AllowMessage 是否放行该连接的一条消息；超速时返回 false 并累计警告
func (ml *MessageLimiter) AllowMessage(connID string) bool {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	b, ok := ml.buckets[connID]
	if !ok {
		b = NewTokenBucket(ml.maxPerSec, float64(ml.maxPerSec))
		ml.buckets[connID] = b
	}
	if !b.Allow() {
		ml.warnings[connID]++
		return false
	}
	return true
}

// WarningCount 累计超速警告次数
func (ml *MessageLimiter) WarningCount(connID string) int {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	return ml.warnings[connID]
}

// Remove 连接关闭时清理
func (ml *MessageLimiter) Remove(connID string) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	delete(ml.buckets, connID)
	delete(ml.warnings, connID)
}
