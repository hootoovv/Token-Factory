package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimiter 基于IP的速率限制器
type RateLimiter struct {
	visitors map[string]*visitorInfo
	mu       sync.RWMutex
	rate     rate.Limit // 每秒允许的请求数
	burst    int        // 突发最大请求数
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
		rate:     10,    // 每秒10个请求
		burst:    20,    // 突发最多20个
	}

	// 启动后台清理协程，移除3分钟内未活跃的访问者
	go rl.cleanupVisitors()
	return rl
}

// getVisitor 获取或创建访问者的限流器
func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if v, exists := rl.visitors[ip]; exists {
		v.lastSeen = time.Now()
		return v.limiter
	}

	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.visitors[ip] = &visitorInfo{
		limiter:  limiter,
		lastSeen: time.Now(),
	}
	return limiter
}

// cleanupVisitors 定期清理不活跃的访问者
func (rl *RateLimiter) cleanupVisitors() {
	for {
		time.Sleep(3 * time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
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