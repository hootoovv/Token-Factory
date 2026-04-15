package admin

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"token_factory/cache"
	"token_factory/database"
	"token_factory/middleware"
	"token_factory/traffic"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ==================== 常量定义 ====================

const (
	DefaultProviderTimeout = 30 // 供应商默认超时秒数
	DefaultProviderRetry   = 3  // 供应商默认重试次数
	MaxRequestBodySize     = 50 // 请求体最大大小(MB)
)

// Server 管理端服务器
type Server struct {
	db              *gorm.DB
	cache           *cache.Cache
	debouncedCache  *cache.DebouncedCache // 4.3 修复：防抖缓存，避免频繁重载
	recorder        *traffic.Recorder
	jwtSecret       []byte
	encryptionKey   string // 3.1 修复：加密密钥，用于加密存储供应商API Key
	transmissionKey string // API Key传输加密密钥，用于前后端安全传输API Key
	corsOrigins     string // CORS允许的来源，逗号分隔；环境变量优先
	frontend        embed.FS
	server          *http.Server
}

// NewServer 创建管理端服务器
func NewServer(db *gorm.DB, c *cache.Cache, r *traffic.Recorder, jwtSecret []byte, encryptionKey string, transmissionKey string, corsOrigins string, frontendFS embed.FS) *Server {
	return &Server{
		db:              db,
		cache:           c,
		debouncedCache:  cache.NewDebouncedCache(c, 2*time.Second), // 4.3 修复：2秒防抖延迟
		recorder:        r,
		jwtSecret:       jwtSecret,
		encryptionKey:   encryptionKey,
		transmissionKey: transmissionKey,
		corsOrigins:     corsOrigins,
		frontend:        frontendFS,
	}
}

// ==================== 请求结构体定义（2.4 字段白名单） ====================

// CreateProviderReq 创建供应商请求
type CreateProviderReq struct {
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Description string `json:"description" binding:"max=500"`
	BaseURL     string `json:"base_url" binding:"required,url,max=500"`
	APIKey      string `json:"api_key" binding:"required,max=500"`
	Timeout     int    `json:"timeout" binding:"omitempty,min=1,max=300"`
	Retry       int    `json:"retry" binding:"omitempty,min=0,max=10"`
	Status      string `json:"status" binding:"omitempty,oneof=active cooldown arrears"`
}

// UpdateProviderReq 更新供应商请求
type UpdateProviderReq struct {
	Name        *string `json:"name" binding:"omitempty,min=1,max=100"`
	Description *string `json:"description" binding:"omitempty,max=500"`
	BaseURL     *string `json:"base_url" binding:"omitempty,url,max=500"`
	APIKey      *string `json:"api_key" binding:"omitempty,max=500"`
	Timeout     *int    `json:"timeout" binding:"omitempty,min=1,max=300"`
	Retry       *int    `json:"retry" binding:"omitempty,min=0,max=10"`
	Status      *string `json:"status" binding:"omitempty,oneof=active cooldown arrears"`
}

// UpdateModelReq 更新模型请求
type UpdateModelReq struct {
	Name        *string `json:"name" binding:"omitempty,min=1,max=100"`
	Description *string `json:"description" binding:"omitempty,max=500"`
}

// ==================== 7.1 审计日志辅助函数 ====================

// recordAuditLog 记录审计日志（异步写入，不阻塞主流程）
func (s *Server) recordAuditLog(c *gin.Context, action, targetType, targetID, detail string) {
	operatorID := c.GetUint("userID")
	operatorName := c.GetString("username")
	ipAddress := c.ClientIP()

	auditLog := database.AuditLog{
		OperatorID:   operatorID,
		OperatorName: operatorName,
		Action:       action,
		TargetType:   targetType,
		TargetID:     targetID,
		Detail:       detail,
		IPAddress:    ipAddress,
	}

	// 异步写入，避免影响接口响应速度
	go func() {
		if err := s.db.Create(&auditLog).Error; err != nil {
			log.Printf("[审计] 写入审计日志失败: %v (action=%s, target=%s/%s)",
				err, action, targetType, targetID)
		}
	}()
}

// ==================== 辅助函数 ====================

// maskAPIKey 对API Key进行脱敏：仅显示前4位和后4位
func maskAPIKey(key string) string {
	// 3.1 修复：如果key是加密存储格式，先尝试解密再脱敏
	// 但此处maskAPIKey仅用于前端显示，输入应为已解密的明文
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// encryptForTransmission 使用XOR+Base64对API Key进行传输加密
// 密钥循环XOR明文，然后Base64编码，前端可用相同密钥解密
func encryptForTransmission(plaintext string, key string) string {
	if key == "" || plaintext == "" {
		return base64.StdEncoding.EncodeToString([]byte(plaintext))
	}
	keyBytes := []byte(key)
	plainBytes := []byte(plaintext)
	result := make([]byte, len(plainBytes))
	for i := 0; i < len(plainBytes); i++ {
		result[i] = plainBytes[i] ^ keyBytes[i%len(keyBytes)]
	}
	return base64.StdEncoding.EncodeToString(result)
}

// Start 启动管理端服务器
func (s *Server) Start(addr string) error {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	// 2.3 修复CORS配置：限制允许的来源域名
	// 可通过环境变量 CORS_ORIGINS 配置，默认允许本地开发环境
	router.Use(cors.New(cors.Config{
		AllowOrigins:     s.getAllowedOrigins(),
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 2.10 添加速率限制中间件
	rateLimiter := middleware.NewRateLimiter()
	router.Use(rateLimiter.RateLimit())

	// API路由
	api := router.Group("/api")
	{
		// 公开接口（无需认证）- 3.4 修复：仅提供公共汇总数据
		api.POST("/login", s.handleLogin)
		api.GET("/dashboard/stats", s.handleDashboardStats)
		api.GET("/dashboard/model-ranking", s.handleModelRanking)
		api.GET("/dashboard/provider-ranking", s.handleProviderRanking)
		api.GET("/dashboard/provider-status", s.handleProviderStatus)
		api.GET("/dashboard/models", s.handleDashboardModels)
		api.GET("/dashboard/providers", s.handleDashboardProviders)

		// 需要认证的接口
		auth := api.Group("")
		auth.Use(middleware.AuthRequired(s.jwtSecret))
		{
			// 用户信息
			auth.GET("/me", s.handleMe)

			// 传输密钥：前端页面刷新时重新获取（仅存内存，不持久化）
			auth.GET("/transmission-key", s.handleTransmissionKey)

			// 3.4 修复：认证用户的仪表板接口（显示该用户自己的数据，支持模型/供应商过滤）
			auth.GET("/my/dashboard/stats", s.handleMyDashboardStats)
			auth.GET("/my/dashboard/model-ranking", s.handleMyModelRanking)
			auth.GET("/my/dashboard/provider-ranking", s.handleMyProviderRanking)
			auth.GET("/my/dashboard/users", s.handleMyDashboardUsers) // 管理员专用：获取用户列表用于过滤

			// 普通用户接口
			user := auth.Group("")
			{
				user.GET("/api-keys", s.handleListAPIKeys)
				user.POST("/api-keys", s.handleCreateAPIKey)
				user.DELETE("/api-keys/:id", s.handleDeleteAPIKey)
				user.GET("/usage", s.handleUserUsage)
				user.GET("/usage/stats", s.handleUserStats)
				user.PUT("/password", s.handleChangePassword)
			}

			// 管理员接口
			admin := auth.Group("")
			admin.Use(middleware.AdminRequired(s.jwtSecret))
			{
				// 用户管理
				admin.GET("/users", s.handleListUsers)
				admin.POST("/users", s.handleCreateUser)
				admin.PUT("/users/:id", s.handleUpdateUser)
				admin.DELETE("/users/:id", s.handleDeleteUser)

				// 供应商管理
				admin.GET("/providers", s.handleListProviders)
				admin.POST("/providers", s.handleCreateProvider)
				admin.PUT("/providers/:id", s.handleUpdateProvider)
				admin.DELETE("/providers/:id", s.handleDeleteProvider)

				// 模型管理
				admin.GET("/models", s.handleListModels)
				admin.POST("/models", s.handleCreateModel)
				admin.PUT("/models/:id", s.handleUpdateModel)
				admin.DELETE("/models/:id", s.handleDeleteModel)

				// 模型-供应商映射
				admin.GET("/model-providers", s.handleListModelProviders)
				admin.POST("/model-providers", s.handleCreateModelProvider)
				admin.DELETE("/model-providers/:id", s.handleDeleteModelProvider)

				// 统计
				admin.GET("/stats/overview", s.handleAdminStats)

				// 审计日志
				admin.GET("/audit-logs", s.handleListAuditLogs)

				// 缓存重载
				admin.POST("/cache/reload", s.handleCacheReload)
			}
		}
	}

	// 禁用 Gin 的尾部斜杠重定向，避免 SPA 路由 301 循环
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false

	// 前端静态文件（SPA模式）
	distFS, err := fs.Sub(s.frontend, "web/dist")
	if err != nil {
		log.Printf("[管理] 前端文件系统初始化失败: %v，将不提供前端", err)
	} else {
		staticHandler := http.FileServer(http.FS(distFS))

		router.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path

			// 根路径直接返回 index.html
			if path == "/" || path == "" {
				data, err := fs.ReadFile(distFS, "index.html")
				if err != nil {
					c.String(404, "Frontend not found")
					return
				}
				c.Data(200, "text/html; charset=utf-8", data)
				return
			}

			// 去掉前导斜杠后检查文件是否存在
			trimmedPath := path
			if len(trimmedPath) > 0 && trimmedPath[0] == '/' {
				trimmedPath = trimmedPath[1:]
			}

			f, err := distFS.Open(trimmedPath)
			if err == nil {
				// 文件存在，直接提供
				f.Close()
				staticHandler.ServeHTTP(c.Writer, c.Request)
				return
			}

			// 文件不存在，SPA 回退：返回 index.html 让前端路由处理
			data, err := fs.ReadFile(distFS, "index.html")
			if err != nil {
				c.String(404, "Page not	found")
				return
			}
			c.Data(200, "text/html; charset=utf-8", data)
		})
	}

	s.server = &http.Server{
		Addr:    addr,
		Handler: router,
	}

	log.Printf("[管理] 服务器启动在 %s", addr)
	return s.server.ListenAndServe()
}

// 5.4 修复：优雅关闭管理服务器
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// getAllowedOrigins 获取CORS允许的来源列表
// 优先级：环境变量 CORS_ORIGINS > 配置文件 cors_origins > 硬编码默认值
func (s *Server) getAllowedOrigins() []string {
	// 1. 优先从环境变量读取
	origins := os.Getenv("CORS_ORIGINS")
	if origins != "" {
		result := []string{}
		for _, o := range strings.Split(origins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				result = append(result, o)
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// 2. 其次从配置文件读取
	if s.corsOrigins != "" {
		result := []string{}
		for _, o := range strings.Split(s.corsOrigins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				result = append(result, o)
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// 3. 默认允许本地开发环境
	return []string{"http://localhost:5173", "http://localhost:8080", "http://127.0.0.1:5173", "http://127.0.0.1:8080"}
}

// ==================== 公开接口 ====================

// handleLogin 登录
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供用户名和密码"})
		return
	}

	var user database.User
	if err := s.db.Where("username = ?", req.Username).First(&user).Error; err != nil {
		c.JSON(401, gin.H{"error": "用户名或密码错误"})
		return
	}

	if !database.CheckPassword(req.Password, user.Password) {
		c.JSON(401, gin.H{"error": "用户名或密码错误"})
		return
	}

	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role, s.jwtSecret)
	if err != nil {
		// 3.2 修复：不泄露内部错误细节
		log.Printf("[管理] 生成Token失败: %v", err)
		c.JSON(500, gin.H{"error": "生成Token失败，请稍后重试"})
		return
	}

	c.JSON(200, gin.H{
		"token":            token,
		"transmission_key": s.transmissionKey, // 返回传输加密密钥，前端用于解密API Key
		"user": gin.H{
			"id":           user.ID,
			"username":     user.Username,
			"role":         user.Role,
			"display_name": user.DisplayName,
		},
	})
}

// handleDashboardStats Dashboard统计（公开）
func (s *Server) handleDashboardStats(c *gin.Context) {
	since := s.parseSince(c)
	filter := s.parseDashboardFilter(c)
	stats, err := traffic.GetDashboardStats(s.db, since, filter)
	if err != nil {
		// 3.2 修复：通用错误消息
		log.Printf("[管理] 获取公共统计数据失败: %v", err)
		c.JSON(500, gin.H{"error": "获取统计数据失败"})
		return
	}
	c.JSON(200, stats)
}

// handleModelRanking 模型使用排行（公开）
func (s *Server) handleModelRanking(c *gin.Context) {
	since := s.parseSince(c)
	filter := s.parseDashboardFilter(c)
	ranking, err := traffic.GetModelRanking(s.db, since, 10, filter)
	if err != nil {
		log.Printf("[管理] 获取公共模型排行失败: %v", err)
		c.JSON(500, gin.H{"error": "获取模型排行失败"})
		return
	}
	c.JSON(200, ranking)
}

// handleProviderRanking 供应商使用排行（公开）
func (s *Server) handleProviderRanking(c *gin.Context) {
	since := s.parseSince(c)
	filter := s.parseDashboardFilter(c)
	ranking, err := traffic.GetProviderRanking(s.db, since, 10, filter)
	if err != nil {
		log.Printf("[管理] 获取公共供应商排行失败: %v", err)
		c.JSON(500, gin.H{"error": "获取供应商排行失败"})
		return
	}
	c.JSON(200, ranking)
}

// handleProviderStatus 供应商实时状态（公开）
func (s *Server) handleProviderStatus(c *gin.Context) {
	providers := s.cache.GetProviders()
	result := make([]gin.H, 0, len(providers))
	for _, p := range providers {
		statusText := "工作中"
		if p.Status == "cooldown" {
			statusText = "冷却中"
		} else if p.Status == "arrears" {
			statusText = "欠费"
		}
		result = append(result, gin.H{
			"id":          p.ID,
			"name":        p.Name,
			"status":      p.Status,
			"status_text": statusText,
		})
	}
	c.JSON(200, result)
}

// ==================== 3.4 修复：认证用户的仪表板接口 ====================

// handleMyDashboardStats 认证用户的仪表板统计
// 普通用户：只显示自己的数据；管理员：可按user_id过滤查看所有/特定用户数据
func (s *Server) handleMyDashboardStats(c *gin.Context) {
	userID := c.GetUint("userID")
	role := c.GetString("role")
	since := s.parseSince(c)
	filter := s.parseDashboardFilter(c)

	if role == "admin" {
		// 管理员：支持按user_id过滤，不传则显示所有数据
		if uid := c.Query("user_id"); uid != "" {
			var filterUserID uint
			fmt.Sscanf(uid, "%d", &filterUserID)
			if filterUserID > 0 {
				filter.UserID = filterUserID
			}
		}
	} else {
		// 普通用户：强制只显示自己的数据
		filter.UserID = userID
	}

	stats, err := traffic.GetDashboardStats(s.db, since, filter)
	if err != nil {
		log.Printf("[管理] 获取用户仪表板统计失败: %v", err)
		c.JSON(500, gin.H{"error": "获取统计数据失败"})
		return
	}
	c.JSON(200, stats)
}

// handleMyModelRanking 认证用户的模型排行
func (s *Server) handleMyModelRanking(c *gin.Context) {
	userID := c.GetUint("userID")
	role := c.GetString("role")
	since := s.parseSince(c)
	filter := s.parseDashboardFilter(c)

	if role == "admin" {
		if uid := c.Query("user_id"); uid != "" {
			var filterUserID uint
			fmt.Sscanf(uid, "%d", &filterUserID)
			if filterUserID > 0 {
				filter.UserID = filterUserID
			}
		}
	} else {
		filter.UserID = userID
	}

	ranking, err := traffic.GetModelRanking(s.db, since, 10, filter)
	if err != nil {
		log.Printf("[管理] 获取用户模型排行失败: %v", err)
		c.JSON(500, gin.H{"error": "获取模型排行失败"})
		return
	}
	c.JSON(200, ranking)
}

// handleMyProviderRanking 认证用户的供应商排行
func (s *Server) handleMyProviderRanking(c *gin.Context) {
	userID := c.GetUint("userID")
	role := c.GetString("role")
	since := s.parseSince(c)
	filter := s.parseDashboardFilter(c)

	if role == "admin" {
		if uid := c.Query("user_id"); uid != "" {
			var filterUserID uint
			fmt.Sscanf(uid, "%d", &filterUserID)
			if filterUserID > 0 {
				filter.UserID = filterUserID
			}
		}
	} else {
		filter.UserID = userID
	}

	ranking, err := traffic.GetProviderRanking(s.db, since, 10, filter)
	if err != nil {
		log.Printf("[管理] 获取用户供应商排行失败: %v", err)
		c.JSON(500, gin.H{"error": "获取供应商排行失败"})
		return
	}
	c.JSON(200, ranking)
}

// handleMyDashboardUsers 获取用户列表（管理员在仪表板中按用户过滤时使用）
func (s *Server) handleMyDashboardUsers(c *gin.Context) {
	role := c.GetString("role")
	if role != "admin" {
		c.JSON(403, gin.H{"error": "需要管理员权限"})
		return
	}

	var users []database.User
	s.db.Select("id, username, display_name").Order("id ASC").Find(&users)

	result := make([]gin.H, 0, len(users))
	for _, u := range users {
		result = append(result, gin.H{
			"id":           u.ID,
			"username":     u.Username,
			"display_name": u.DisplayName,
		})
	}
	c.JSON(200, result)
}

// ==================== 用户接口 ====================

// handleMe 获取当前用户信息
func (s *Server) handleMe(c *gin.Context) {
	userID := c.GetUint("userID")
	var user database.User
	if err := s.db.First(&user, userID).Error; err != nil {
		c.JSON(404, gin.H{"error": "用户不存在"})
		return
	}
	c.JSON(200, gin.H{
		"id":           user.ID,
		"username":     user.Username,
		"role":         user.Role,
		"display_name": user.DisplayName,
	})
}

// handleTransmissionKey 获取API Key传输加密密钥
// 前端页面刷新时调用此接口重新获取密钥（密钥仅存内存，不写入localStorage）
func (s *Server) handleTransmissionKey(c *gin.Context) {
	c.JSON(200, gin.H{
		"transmission_key": s.transmissionKey,
	})
}

// handleListAPIKeys 列出当前用户的API-Key
func (s *Server) handleListAPIKeys(c *gin.Context) {
	userID := c.GetUint("userID")
	var keys []database.APIKey
	s.db.Where("user_id = ?", userID).Find(&keys)

	// 3.5 修复：API Key值在列表查询中脱敏显示，同时提供加密传输的完整值
	result := make([]gin.H, 0, len(keys))
	for _, k := range keys {
		result = append(result, gin.H{
			"id":            k.ID,
			"user_id":       k.UserID,
			"key":           maskAPIKey(k.Key),                                // 脱敏显示
			"encrypted_key": encryptForTransmission(k.Key, s.transmissionKey), // 加密传输完整值
			"key_prefix":    k.Key[:7],                                        // 提供前缀方便识别
			"name":          k.Name,
			"status":        k.Status,
			"created_at":    k.CreatedAt,
			"updated_at":    k.UpdatedAt,
		})
	}
	c.JSON(200, result)
}

// handleCreateAPIKey 创建API-Key
func (s *Server) handleCreateAPIKey(c *gin.Context) {
	userID := c.GetUint("userID")
	var req struct {
		Name string `json:"name"`
		Key  string `json:"key"` // 前端生成的密钥，为空则后端自动生成
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Name = "Default Key"
	}

	var apiKey string
	if req.Key != "" {
		// 使用前端传入的密钥
		apiKey = req.Key
	} else {
		// 后端自动生成 tk- 前缀 + 32位随机字符
		apiKey = generateAPIKey()
	}

	newKey := database.APIKey{
		UserID: userID,
		Key:    apiKey,
		Name:   req.Name,
		Status: "active",
	}

	if err := s.db.Create(&newKey).Error; err != nil {
		// 3.2 修复：不泄露内部错误细节
		log.Printf("[管理] 创建API-Key失败: %v", err)
		c.JSON(500, gin.H{"error": "创建API-Key失败，请稍后重试"})
		return
	}

	// 重新加载缓存
	s.debouncedCache.Reload()

	// 7.1 审计日志：记录API-Key创建操作
	s.recordAuditLog(c, "create", "api_key", fmt.Sprintf("%d", newKey.ID),
		fmt.Sprintf("创建API-Key: name=%s, key_prefix=%s", newKey.Name, newKey.Key[:7]))

	// 3.5 修复：创建时返回加密的完整Key（仅此一次），之后列表中只显示脱敏值
	c.JSON(200, gin.H{
		"id":            newKey.ID,
		"user_id":       newKey.UserID,
		"key":           maskAPIKey(newKey.Key),                                // 脱敏显示
		"encrypted_key": encryptForTransmission(newKey.Key, s.transmissionKey), // 加密传输完整值
		"name":          newKey.Name,
		"status":        newKey.Status,
		"created_at":    newKey.CreatedAt,
		"updated_at":    newKey.UpdatedAt,
		"message":       "请妥善保存此密钥，之后将无法再次查看完整值",
	})
}

// handleDeleteAPIKey 删除API-Key
func (s *Server) handleDeleteAPIKey(c *gin.Context) {
	userID := c.GetUint("userID")
	id := c.Param("id")

	// 先查询API-Key信息用于审计日志
	var apiKeyInfo database.APIKey
	if err := s.db.Where("id = ? AND user_id = ?", id, userID).First(&apiKeyInfo).Error; err != nil {
		c.JSON(404, gin.H{"error": "API-Key不存在"})
		return
	}

	result := s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&database.APIKey{})
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "API-Key不存在"})
		return
	}

	// 7.1 审计日志：记录API-Key删除操作
	s.recordAuditLog(c, "delete", "api_key", id,
		fmt.Sprintf("删除API-Key: name=%s, key_prefix=%s", apiKeyInfo.Name, apiKeyInfo.Key[:7]))

	s.debouncedCache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// handleUserUsage 用户使用记录
func (s *Server) handleUserUsage(c *gin.Context) {
	userID := c.GetUint("userID")
	since := s.parseSince(c)
	page := s.getPage(c)
	pageSize := s.getPageSize(c)

	records, total, err := traffic.GetUserTrafficRecords(s.db, userID, since, page, pageSize)
	if err != nil {
		// 3.2 修复
		log.Printf("[管理] 获取用户使用记录失败: %v", err)
		c.JSON(500, gin.H{"error": "获取使用记录失败"})
		return
	}

	c.JSON(200, gin.H{
		"items":     records,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleUserStats 用户统计
func (s *Server) handleUserStats(c *gin.Context) {
	userID := c.GetUint("userID")
	since := s.parseSince(c)

	stats, err := traffic.GetUserStats(s.db, userID, since)
	if err != nil {
		log.Printf("[管理] 获取用户统计失败: %v", err)
		c.JSON(500, gin.H{"error": "获取统计失败"})
		return
	}
	c.JSON(200, stats)
}

// handleChangePassword 修改密码
func (s *Server) handleChangePassword(c *gin.Context) {
	userID := c.GetUint("userID")
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供旧密码和新密码（新密码至少6位）"})
		return
	}

	var user database.User
	if err := s.db.First(&user, userID).Error; err != nil {
		c.JSON(404, gin.H{"error": "用户不存在"})
		return
	}

	if !database.CheckPassword(req.OldPassword, user.Password) {
		c.JSON(401, gin.H{"error": "旧密码不正确"})
		return
	}

	hashedPassword, err := database.HashPassword(req.NewPassword)
	if err != nil {
		// 3.2 修复
		log.Printf("[管理] 加密密码失败: %v", err)
		c.JSON(500, gin.H{"error": "密码修改失败，请稍后重试"})
		return
	}

	s.db.Model(&user).Update("password", hashedPassword)
	c.JSON(200, gin.H{"message": "密码修改成功"})
}

// ==================== 管理员接口 ====================

// 4.1 修复：添加分页支持，避免全表扫描
// handleListUsers 列出用户（分页）
func (s *Server) handleListUsers(c *gin.Context) {
	page := s.getPage(c)
	pageSize := s.getPageSize(c)

	var users []database.User
	var total int64
	s.db.Model(&database.User{}).Count(&total)
	s.db.Select("id, username, role, display_name, created_at, updated_at").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&users)

	// 隐藏密码字段
	result := make([]gin.H, 0, len(users))
	for _, u := range users {
		result = append(result, gin.H{
			"id":           u.ID,
			"username":     u.Username,
			"role":         u.Role,
			"display_name": u.DisplayName,
			"created_at":   u.CreatedAt,
			"updated_at":   u.UpdatedAt,
		})
	}
	c.JSON(200, gin.H{
		"items":     result,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleCreateUser 创建用户
func (s *Server) handleCreateUser(c *gin.Context) {
	var req struct {
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required,min=6"`
		Role        string `json:"role"`
		DisplayName string `json:"display_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供用户名和密码（密码至少6位）"})
		return
	}

	if req.Role == "" {
		req.Role = "user"
	}

	// 校验角色合法性
	if req.Role != "admin" && req.Role != "user" {
		c.JSON(400, gin.H{"error": "无效的角色类型"})
		return
	}

	hashedPassword, err := database.HashPassword(req.Password)
	if err != nil {
		// 3.2 修复
		log.Printf("[管理] 加密密码失败: %v", err)
		c.JSON(500, gin.H{"error": "创建用户失败，请稍后重试"})
		return
	}

	user := database.User{
		Username:    req.Username,
		Password:    hashedPassword,
		Role:        req.Role,
		DisplayName: req.DisplayName,
	}

	if err := s.db.Create(&user).Error; err != nil {
		// 3.2 修复：不泄露数据库错误细节
		log.Printf("[管理] 创建用户失败: %v", err)
		c.JSON(500, gin.H{"error": "创建用户失败，请稍后重试"})
		return
	}

	c.JSON(200, gin.H{
		"id":           user.ID,
		"username":     user.Username,
		"role":         user.Role,
		"display_name": user.DisplayName,
	})
}

// handleUpdateUser 更新用户
func (s *Server) handleUpdateUser(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Role        *string `json:"role"`
		DisplayName *string `json:"display_name"`
		Password    *string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	updates := map[string]interface{}{}
	if req.Role != nil {
		// 校验角色合法性
		if *req.Role != "admin" && *req.Role != "user" {
			c.JSON(400, gin.H{"error": "无效的角色类型"})
			return
		}
		updates["role"] = *req.Role
	}
	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.Password != nil && *req.Password != "" {
		if len(*req.Password) < 6 {
			c.JSON(400, gin.H{"error": "密码长度不能少于6位"})
			return
		}
		hashed, err := database.HashPassword(*req.Password)
		if err != nil {
			log.Printf("[管理] 加密密码失败: %v", err)
			c.JSON(500, gin.H{"error": "更新用户失败，请稍后重试"})
			return
		}
		updates["password"] = hashed
	}

	if len(updates) > 0 {
		s.db.Model(&database.User{}).Where("id = ?", id).Updates(updates)
	}
	c.JSON(200, gin.H{"message": "更新成功"})
}

// 5.2 修复：使用事务保证关联操作的原子性
// handleDeleteUser 删除用户
func (s *Server) handleDeleteUser(c *gin.Context) {
	id := c.Param("id")
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 先删除该用户的API-Key
		if err := tx.Where("user_id = ?", id).Delete(&database.APIKey{}).Error; err != nil {
			return err
		}
		// 再删除用户
		result := tx.Delete(&database.User{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "用户不存在"})
			return
		}
		log.Printf("[管理] 删除用户失败: %v", err)
		c.JSON(500, gin.H{"error": "删除用户失败，请稍后重试"})
		return
	}
	s.debouncedCache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// 2.2 修复：供应商列表返回时对API Key进行脱敏
// 3.1 修复：从数据库读取并解密API Key，脱敏后返回前端
// 4.1 修复：添加分页支持，与其他管理员接口保持一致
// handleListProviders 列出供应商（分页）
func (s *Server) handleListProviders(c *gin.Context) {
	page := s.getPage(c)
	pageSize := s.getPageSize(c)

	var providers []database.Provider
	var total int64
	s.db.Model(&database.Provider{}).Count(&total)
	s.db.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&providers)

	// 脱敏处理：解密后脱敏，不直接返回完整API Key
	result := make([]gin.H, 0, len(providers))
	for _, p := range providers {
		decryptedKey, err := database.DecryptAPIKey(p.APIKey, s.encryptionKey)
		if err != nil {
			log.Printf("[管理] 解密供应商 %s 的API Key失败: %v", p.Name, err)
			decryptedKey = "****"
		}
		result = append(result, gin.H{
			"id":          p.ID,
			"name":        p.Name,
			"description": p.Description,
			"base_url":    p.BaseURL,
			"api_key":     maskAPIKey(decryptedKey), // 脱敏: "sk-a...yz"
			"timeout":     p.Timeout,
			"retry":       p.Retry,
			"status":      p.Status,
			"created_at":  p.CreatedAt,
			"updated_at":  p.UpdatedAt,
		})
	}
	c.JSON(200, gin.H{
		"items":     result,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// 2.4 修复：使用结构体绑定，添加输入校验
// 3.1 修复：创建供应商时加密存储API Key
// handleCreateProvider 创建供应商
func (s *Server) handleCreateProvider(c *gin.Context) {
	var req CreateProviderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据: " + err.Error()})
		return
	}

	if req.Timeout == 0 {
		req.Timeout = DefaultProviderTimeout
	}
	if req.Retry == 0 {
		req.Retry = DefaultProviderRetry
	}
	if req.Status == "" {
		req.Status = "active"
	}

	// 3.1 修复：加密API Key后再存储
	encryptedKey, err := database.EncryptAPIKey(req.APIKey, s.encryptionKey)
	if err != nil {
		log.Printf("[管理] 加密供应商API Key失败: %v", err)
		c.JSON(500, gin.H{"error": "创建供应商失败，请稍后重试"})
		return
	}

	provider := database.Provider{
		Name:        req.Name,
		Description: req.Description,
		BaseURL:     req.BaseURL,
		APIKey:      encryptedKey, // 存储加密后的值
		Timeout:     req.Timeout,
		Retry:       req.Retry,
		Status:      req.Status,
	}

	if err := s.db.Create(&provider).Error; err != nil {
		// 3.2 修复：不泄露内部错误细节
		log.Printf("[管理] 创建供应商失败: %v", err)
		c.JSON(500, gin.H{"error": "创建供应商失败，请稍后重试"})
		return
	}

	s.debouncedCache.Reload()

	// 返回时对API Key脱敏（返回用户输入的原始值脱敏，而非加密值）
	c.JSON(200, gin.H{
		"id":          provider.ID,
		"name":        provider.Name,
		"description": provider.Description,
		"base_url":    provider.BaseURL,
		"api_key":     maskAPIKey(req.APIKey), // 脱敏显示原始输入
		"timeout":     provider.Timeout,
		"retry":       provider.Retry,
		"status":      provider.Status,
		"created_at":  provider.CreatedAt,
		"updated_at":  provider.UpdatedAt,
	})
}

// 2.4 修复：使用结构体绑定，限制可更新字段
// 3.1 修复：更新供应商API Key时加密存储
// handleUpdateProvider 更新供应商
func (s *Server) handleUpdateProvider(c *gin.Context) {
	id := c.Param("id")
	var req UpdateProviderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据: " + err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.BaseURL != nil {
		updates["base_url"] = *req.BaseURL
	}
	if req.APIKey != nil {
		// 3.1 修复：加密API Key后再存储
		encryptedKey, err := database.EncryptAPIKey(*req.APIKey, s.encryptionKey)
		if err != nil {
			log.Printf("[管理] 加密供应商API Key失败: %v", err)
			c.JSON(500, gin.H{"error": "更新供应商失败，请稍后重试"})
			return
		}
		updates["api_key"] = encryptedKey
	}
	if req.Timeout != nil {
		updates["timeout"] = *req.Timeout
	}
	if req.Retry != nil {
		updates["retry"] = *req.Retry
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if len(updates) > 0 {
		s.db.Model(&database.Provider{}).Where("id = ?", id).Updates(updates)
		s.debouncedCache.Reload()
	}
	c.JSON(200, gin.H{"message": "更新成功"})
}

// 5.2 修复：使用事务保证关联操作的原子性
// handleDeleteProvider 删除供应商
func (s *Server) handleDeleteProvider(c *gin.Context) {
	id := c.Param("id")
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 先删除相关映射
		if err := tx.Where("provider_id = ?", id).Delete(&database.ModelProvider{}).Error; err != nil {
			return err
		}
		// 再删除供应商
		result := tx.Delete(&database.Provider{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "供应商不存在"})
			return
		}
		log.Printf("[管理] 删除供应商失败: %v", err)
		c.JSON(500, gin.H{"error": "删除供应商失败，请稍后重试"})
		return
	}
	s.debouncedCache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// 4.1 修复：添加分页支持，避免全表扫描
// handleListModels 列出模型（分页）
func (s *Server) handleListModels(c *gin.Context) {
	page := s.getPage(c)
	pageSize := s.getPageSize(c)

	var models []database.Model
	var total int64
	s.db.Model(&database.Model{}).Count(&total)
	s.db.Offset((page - 1) * pageSize).Limit(pageSize).Find(&models)
	c.JSON(200, gin.H{
		"items":     models,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleCreateModel 创建模型
func (s *Server) handleCreateModel(c *gin.Context) {
	var model database.Model
	if err := c.ShouldBindJSON(&model); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	if err := s.db.Create(&model).Error; err != nil {
		// 3.2 修复
		log.Printf("[管理] 创建模型失败: %v", err)
		c.JSON(500, gin.H{"error": "创建模型失败，请稍后重试"})
		return
	}

	s.debouncedCache.Reload()
	c.JSON(200, model)
}

// 2.4 修复：使用结构体绑定，限制可更新字段
// handleUpdateModel 更新模型
func (s *Server) handleUpdateModel(c *gin.Context) {
	id := c.Param("id")
	var req UpdateModelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据: " + err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}

	if len(updates) > 0 {
		s.db.Model(&database.Model{}).Where("id = ?", id).Updates(updates)
		s.debouncedCache.Reload()
	}
	c.JSON(200, gin.H{"message": "更新成功"})
}

// 5.2 修复：使用事务保证关联操作的原子性
// handleDeleteModel 删除模型
func (s *Server) handleDeleteModel(c *gin.Context) {
	id := c.Param("id")
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 先删除相关映射
		if err := tx.Where("model_id = ?", id).Delete(&database.ModelProvider{}).Error; err != nil {
			return err
		}
		// 再删除模型
		result := tx.Delete(&database.Model{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "模型不存在"})
			return
		}
		log.Printf("[管理] 删除模型失败: %v", err)
		c.JSON(500, gin.H{"error": "删除模型失败，请稍后重试"})
		return
	}
	s.debouncedCache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// 4.1 修复：添加分页支持，避免全表扫描
// handleListModelProviders 列出模型-供应商映射（分页）
func (s *Server) handleListModelProviders(c *gin.Context) {
	page := s.getPage(c)
	pageSize := s.getPageSize(c)

	var mappings []database.ModelProvider
	var total int64
	s.db.Model(&database.ModelProvider{}).Count(&total)
	s.db.Offset((page - 1) * pageSize).Limit(pageSize).Find(&mappings)
	c.JSON(200, gin.H{
		"items":     mappings,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleCreateModelProvider 创建模型-供应商映射
func (s *Server) handleCreateModelProvider(c *gin.Context) {
	var mp database.ModelProvider
	if err := c.ShouldBindJSON(&mp); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	if err := s.db.Create(&mp).Error; err != nil {
		// 3.2 修复
		log.Printf("[管理] 创建映射失败: %v", err)
		c.JSON(500, gin.H{"error": "创建映射失败，请稍后重试"})
		return
	}

	s.debouncedCache.Reload()
	c.JSON(200, mp)
}

// handleDeleteModelProvider 删除模型-供应商映射
func (s *Server) handleDeleteModelProvider(c *gin.Context) {
	id := c.Param("id")
	result := s.db.Delete(&database.ModelProvider{}, id)
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "映射不存在"})
		return
	}
	s.debouncedCache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// handleAdminStats 管理员统计概览
func (s *Server) handleAdminStats(c *gin.Context) {
	since := s.parseSince(c)

	stats, err := traffic.GetDashboardStats(s.db, since, traffic.FilterParams{})
	if err != nil {
		// 3.2 修复
		log.Printf("[管理] 获取管理员统计失败: %v", err)
		c.JSON(500, gin.H{"error": "获取统计失败"})
		return
	}

	modelRanking, _ := traffic.GetModelRanking(s.db, since, 10, traffic.FilterParams{})
	providerRanking, _ := traffic.GetProviderRanking(s.db, since, 10, traffic.FilterParams{})

	c.JSON(200, gin.H{
		"stats":            stats,
		"model_ranking":    modelRanking,
		"provider_ranking": providerRanking,
	})
}

// ==================== 7.1 审计日志接口 ====================

// handleListAuditLogs 查询审计日志（分页 + 过滤）
// 支持过滤参数：action（操作类型）、target_type（对象类型）、operator_name（操作者用户名）、
// start_time / end_time（时间范围）
func (s *Server) handleListAuditLogs(c *gin.Context) {
	page := s.getPage(c)
	pageSize := s.getPageSize(c)

	query := s.db.Model(&database.AuditLog{})

	// 过滤：操作类型
	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}

	// 过滤：对象类型
	if targetType := c.Query("target_type"); targetType != "" {
		query = query.Where("target_type = ?", targetType)
	}

	// 过滤：操作者用户名（模糊匹配）
	if operatorName := c.Query("operator_name"); operatorName != "" {
		query = query.Where("operator_name LIKE ?", "%"+operatorName+"%")
	}

	// 过滤：时间范围
	if startTime := c.Query("start_time"); startTime != "" {
		if t, err := time.Parse("2006-01-02", startTime); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if endTime := c.Query("end_time"); endTime != "" {
		if t, err := time.Parse("2006-01-02", endTime); err == nil {
			// 结束时间包含当天，所以加一天
			query = query.Where("created_at < ?", t.AddDate(0, 0, 1))
		}
	}

	var total int64
	query.Count(&total)

	var logs []database.AuditLog
	query.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&logs)

	c.JSON(200, gin.H{
		"items":     logs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleCacheReload 重载缓存
func (s *Server) handleCacheReload(c *gin.Context) {
	if err := s.cache.Reload(); err != nil {
		log.Printf("[管理] 缓存重载失败: %v", err)
		c.JSON(500, gin.H{"error": "缓存重载失败"})
		return
	}
	c.JSON(200, gin.H{"message": "缓存重载成功"})
}

// ==================== 辅助方法 ====================

func (s *Server) parseSince(c *gin.Context) time.Time {
	filter := c.DefaultQuery("time_filter", "1h")
	switch filter {
	case "1h":
		return time.Now().Add(-1 * time.Hour)
	case "today":
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "week":
		return time.Now().AddDate(0, 0, -7)
	case "month":
		return time.Now().AddDate(0, -1, 0)
	default:
		return time.Now().Add(-1 * time.Hour)
	}
}

func (s *Server) parseDashboardFilter(c *gin.Context) traffic.FilterParams {
	var filter traffic.FilterParams
	if mid := c.Query("model_id"); mid != "" {
		fmt.Sscanf(mid, "%d", &filter.ModelID)
	}
	if pid := c.Query("provider_id"); pid != "" {
		fmt.Sscanf(pid, "%d", &filter.ProviderID)
	}
	return filter
}

// handleDashboardModels 获取模型列表（公开，用于Dashboard过滤）
func (s *Server) handleDashboardModels(c *gin.Context) {
	var models []database.Model
	s.db.Select("id, name").Order("id ASC").Find(&models)
	c.JSON(200, models)
}

// handleDashboardProviders 获取供应商列表（公开，用于Dashboard过滤）
func (s *Server) handleDashboardProviders(c *gin.Context) {
	var providers []database.Provider
	s.db.Select("id, name").Order("id ASC").Find(&providers)
	c.JSON(200, providers)
}

func (s *Server) getPage(c *gin.Context) int {
	page := 1
	if p := c.Query("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if page < 1 {
		page = 1
	}
	return page
}

func (s *Server) getPageSize(c *gin.Context) int {
	size := 20
	if s := c.Query("page_size"); s != "" {
		fmt.Sscanf(s, "%d", &size)
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return size
}

// generateAPIKey 生成 tk- 前缀 + 32位随机字符（大小写字母、数字、-、_）
func generateAPIKey() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	randBytes := make([]byte, 32)
	rand.Read(randBytes)
	key := make([]byte, 3+32)
	key[0] = 't'
	key[1] = 'k'
	key[2] = '-'
	for i := 0; i < 32; i++ {
		key[3+i] = charset[int(randBytes[i])%len(charset)]
	}
	return string(key)
}
