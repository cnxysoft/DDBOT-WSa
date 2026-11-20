package twitter

import (
	"context"
	"sync"
	"time"
)

// RateLimiter 全局限流器，防止请求过于频繁导致封号
type RateLimiter struct {
	mu                sync.Mutex
	lastRequestTime   time.Time
	minInterval       time.Duration
	consecutiveErrors int
	backoffUntil      time.Time
}

var globalRateLimiter = &RateLimiter{
	minInterval: 2 * time.Second, // 默认最小间隔2秒
}

// Wait 等待直到可以发送请求，可被 ctx 取消。
// 返回 false 表示调用方上下文已取消（不占用请求配额）。
// 实现采用"锁内计算等待时长 + 锁外等待"模式：持锁期间绝不睡眠，
// 因此退避/限流等待中的 goroutine 不会阻塞其它调用方的 RecordSuccess/RecordError，
// 也不会让锁等待者无法响应 ctx 取消。
func (r *RateLimiter) Wait(ctx context.Context) bool {
	for {
		r.mu.Lock()
		now := time.Now()
		var wait time.Duration

		// 是否处于退避期
		if !r.backoffUntil.IsZero() && now.Before(r.backoffUntil) {
			wait = r.backoffUntil.Sub(now)
		}

		// 确保最小间隔
		if !r.lastRequestTime.IsZero() {
			elapsed := time.Since(r.lastRequestTime)
			if elapsed < r.minInterval {
				if d := r.minInterval - elapsed; d > wait {
					wait = d
				}
			}
		}

		if wait <= 0 {
			r.lastRequestTime = time.Now()
			r.mu.Unlock()
			return true
		}
		r.mu.Unlock()

		logger.Debugf("Rate limiter: waiting %v before next request", wait)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		// 醒来后重新竞争锁并重算等待时长：期间其它请求可能已占用配额，
		// 也可能退避已被 RecordSuccess 解除，以最新状态为准。
	}
}

// RecordSuccess 记录成功请求
func (r *RateLimiter) RecordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consecutiveErrors = 0
	r.backoffUntil = time.Time{}
}

// RecordError 记录失败请求，触发指数退避
func (r *RateLimiter) RecordError() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consecutiveErrors++

	// 指数退避：2^errors 秒，最大5分钟。
	// consecutiveErrors 无上限（429 时 +3），直接移位会在 errors>=63 时翻转为负数、>=64 时为 0，
	// 使退避在最需要它的限流风暴/长期故障下静默失效，故先封顶再计算。
	backoffSeconds := 300
	if r.consecutiveErrors < 9 {
		backoffSeconds = 1 << uint(r.consecutiveErrors)
	}

	r.backoffUntil = time.Now().Add(time.Duration(backoffSeconds) * time.Second)
	logger.Warnf("Rate limiter: recorded error #%d, backing off for %d seconds",
		r.consecutiveErrors, backoffSeconds)
}

// Handle429 处理429 Too Many Requests响应
func (r *RateLimiter) Handle429() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 429响应，强制退避60秒
	r.backoffUntil = time.Now().Add(60 * time.Second)
	r.consecutiveErrors += 3 // 加速退避
	logger.Warnf("Rate limiter: received 429, backing off for 60 seconds")
}

// apiWait 全局API请求等待，返回 false 表示调用方上下文已取消
func apiWait(ctx context.Context) bool {
	return globalRateLimiter.Wait(ctx)
}

// apiSuccess 记录API请求成功
func apiSuccess() {
	globalRateLimiter.RecordSuccess()
}

// apiError 记录API请求失败
func apiError() {
	globalRateLimiter.RecordError()
}

// api429 处理429响应
func api429() {
	globalRateLimiter.Handle429()
}
