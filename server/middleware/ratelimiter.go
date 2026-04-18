package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// 限流器常量配置
const (
	MaxVisitors       = 100000              // 访问者最大数量上限，防止OOM
	CleanupInterval   = 3 * time.Minute     // 清理检查间隔
	VisitorExpiry     = 3 * time.Minute     // 访问者过期时间
	DefaultRate       = 10                  // 默认每秒允许的请求数
	DefaultBurst      = 20                  // 默认突发最大请求数
)

// RateLimiter 基于IP的速率限制器
type RateLimiter struct {
	visitors map[string]*visitorInfo
	mu       sync.RWMutex
	rate     rate.Limit // 每秒允许的请求数
	burst    int        // 突发最大请求数
	done     chan struct{} // 用于通知清理协程退出
}

// visitorInfo 访问者信息
type visitorInfo struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter 创建速率限制器
// 默认：每秒10个请求，突发最多20个
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitorInfo),
		rate:     DefaultRate,
		burst:    DefaultBurst,
		done:     make(chan struct{}),
	}

	// 启动后台清理协程，移除过期的访问者
	go rl.cleanupVisitors()
	return rl
}

// Stop 停止速率限制器的后台清理协程，释放资源
func (rl *RateLimiter) Stop() {
	close(rl.done)
}

// getVisitor 获取或创建访问者的限流器
func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if v, exists := rl.visitors[ip]; exists {
		v.lastSeen = time.Now()
		return v.limiter
	}

	// 如果访问者数量已达上限，先触发一次紧急清理
	if len(rl.visitors) >= MaxVisitors {
		rl.emergencyCleanup()
	}

	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.visitors[ip] = &visitorInfo{
		limiter:  limiter,
		lastSeen: time.Now(),
	}
	return limiter
}

// emergencyCleanup 紧急清理：在访问者数量达到上限时调用
// 必须在持有写锁的情况下调用
func (rl *RateLimiter) emergencyCleanup() {
	now := time.Now()
	for ip, v := range rl.visitors {
		if now.Sub(v.lastSeen) > VisitorExpiry {
			delete(rl.visitors, ip)
		}
	}
	// 如果清理后仍然超过上限，删除最旧的访问者直到数量降下来
	if len(rl.visitors) >= MaxVisitors {
		// 找出最旧的访问者并删除
		var oldestIP string
		var oldestTime time.Time
		first := true
		for ip, v := range rl.visitors {
			if first || v.lastSeen.Before(oldestTime) {
				oldestIP = ip
				oldestTime = v.lastSeen
				first = false
			}
		}
		if oldestIP != "" {
			delete(rl.visitors, oldestIP)
		}
	}
}

// cleanupVisitors 定期清理不活跃的访问者
// 支持通过 done channel 优雅退出
func (rl *RateLimiter) cleanupVisitors() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			// 收到退出信号，停止清理协程
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, v := range rl.visitors {
				if now.Sub(v.lastSeen) > VisitorExpiry {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// RateLimit Gin中间件：基于IP的速率限制
func (rl *RateLimiter) RateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		limiter := rl.getVisitor(ip)

		if !limiter.Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
