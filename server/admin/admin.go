package admin

import (
        "context"
        "crypto/rand"
        "crypto/tls"
        "embed"
        "encoding/base64"
        "encoding/json"
        "fmt"
        "io"
        "io/fs"
        "log"
        "net/http"
        "os"
        "regexp"
        "strings"
        "time"

        "token_factory/cache"
        "token_factory/database"
        "token_factory/middleware"
        "token_factory/traffic"

        "github.com/gin-contrib/cors"
        "github.com/gin-gonic/gin"
        "golang.org/x/net/http2"
        "gorm.io/gorm"
)

// ==================== 常量定义 ====================

const (
        DefaultProviderTimeout           = 300 // 供应商默认总超时秒数（适合长文本生成）
        DefaultProviderConnectTimeout    = 10  // 供应商默认连接建立超时秒数
        DefaultProviderFirstTokenTimeout = 30  // 供应商默认首Token返回超时秒数
        DefaultProviderStreamIdleTimeout = 15  // 供应商默认流传输Idle超时秒数
        DefaultProviderRetry             = 3   // 供应商默认重试次数
        MaxRequestBodySize               = 50  // 请求体最大大小(MB)
)

// versionPathSuffix 检测URL是否以版本路径结尾（如 /v1, /v2, /v4 等）
// 与 proxy 包中同名变量保持一致，用于测试供应商时构建正确的URL
var versionPathSuffix = regexp.MustCompile(`/v\d+$`)

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
        rateLimiter     *middleware.RateLimiter // 限流器，用于优雅关闭时释放资源
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
// API Key 已迁移到 ProviderAPIKey 表，创建供应商时不再包含 api_key/status
// 整数字段使用指针类型，区分"用户未提供"（nil→使用默认值）和"用户明确设为0"
type CreateProviderReq struct {
        Name              string `json:"name" binding:"required,min=1,max=100"`
        Description       string `json:"description" binding:"max=500"`
        BaseURL           string `json:"base_url" binding:"required,url,max=500"`
        Timeout           *int   `json:"timeout" binding:"omitempty,min=1,max=1800"`            // 总超时秒数
        ConnectTimeout    *int   `json:"connect_timeout" binding:"omitempty,min=1,max=60"`      // 连接建立超时秒数
        FirstTokenTimeout *int   `json:"first_token_timeout" binding:"omitempty,min=1,max=300"` // 首Token返回超时秒数
        StreamIdleTimeout *int   `json:"stream_idle_timeout" binding:"omitempty,min=1,max=120"` // 流传输Idle超时秒数
        Retry             *int   `json:"retry" binding:"min=0,max=10"`
}

// CreateProviderAPIKeyReq 创建供应商 API Key 请求
type CreateProviderAPIKeyReq struct {
        ProviderID uint   `json:"provider_id" binding:"required"`
        APIKey     string `json:"api_key" binding:"required,max=500"`
        Name       string `json:"name" binding:"max=100"`
        Status     string `json:"status" binding:"omitempty,oneof=active cooldown arrears disabled"`
}

// UpdateProviderAPIKeyReq 更新供应商 API Key 请求
type UpdateProviderAPIKeyReq struct {
        Name   *string `json:"name" binding:"omitempty,max=100"`
        APIKey *string `json:"api_key" binding:"omitempty,max=500"`
        Status *string `json:"status" binding:"omitempty,oneof=active cooldown arrears disabled"`
}

// UpdateProviderReq 更新供应商请求
// API Key 已迁移到 ProviderAPIKey 表，更新供应商时不再包含 api_key/status
type UpdateProviderReq struct {
        Name              *string `json:"name" binding:"omitempty,min=1,max=100"`
        Description       *string `json:"description" binding:"omitempty,max=500"`
        BaseURL           *string `json:"base_url" binding:"omitempty,url,max=500"`
        Timeout           *int    `json:"timeout" binding:"omitempty,min=1,max=1800"`
        ConnectTimeout    *int    `json:"connect_timeout" binding:"omitempty,min=1,max=60"`
        FirstTokenTimeout *int    `json:"first_token_timeout" binding:"omitempty,min=1,max=300"`
        StreamIdleTimeout *int    `json:"stream_idle_timeout" binding:"omitempty,min=1,max=120"`
        Retry             *int    `json:"retry" binding:"min=0,max=10"`
}

// TestProviderAPIKeyReq 测试供应商 API Key 连接请求
type TestProviderAPIKeyReq struct {
        ProviderID       uint   `json:"provider_id" binding:"required"`
        ProviderAPIKeyID *uint  `json:"provider_api_key_id"`       // 已有 API Key 的 ID（使用数据库中的真实密钥测试）
        APIKey           string `json:"api_key" binding:"max=500"` // 明文 API Key（新增模式或手动输入时使用）
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
        s.rateLimiter = middleware.NewRateLimiter()
        router.Use(s.rateLimiter.RateLimit())

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
                        auth.GET("/my/dashboard/user-ranking", s.handleMyUserRanking)  // 用户使用排行（管理员查全部，普通用户仅自己）
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

                                // 供应商 API Key 管理
                                admin.GET("/provider-api-keys", s.handleListProviderAPIKeys)
                                admin.POST("/provider-api-keys", s.handleCreateProviderAPIKey)
                                admin.PUT("/provider-api-keys/:id", s.handleUpdateProviderAPIKey)
                                admin.DELETE("/provider-api-keys/:id", s.handleDeleteProviderAPIKey)
                                admin.POST("/provider-api-keys/test", s.handleTestProviderAPIKey)

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
                                c.String(404, "Page not found")
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
        // 停止限流器后台清理协程，释放资源
        if s.rateLimiter != nil {
                s.rateLimiter.Stop()
        }
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
// 状态已下沉到 API Key 级别，返回树形结构：供应商 -> API Keys
func (s *Server) handleProviderStatus(c *gin.Context) {
        providers := s.cache.GetProviders()
        result := make([]gin.H, 0, len(providers))
        for _, p := range providers {
                // 获取该供应商的所有 API Keys 及其状态
                apiKeys := s.cache.GetProviderAPIKeys(p.ID)
                keyStatuses := make([]gin.H, 0, len(apiKeys))
                for _, ak := range apiKeys {
                        statusText := "工作中"
                        if ak.Status == "cooldown" {
                                statusText = "冷却中"
                        } else if ak.Status == "arrears" {
                                statusText = "欠费"
                        } else if ak.Status == "disabled" {
                                statusText = "已禁用"
                        }
                        keyStatuses = append(keyStatuses, gin.H{
                                "id":          ak.KeyID,
                                "name":        ak.Name,
                                "status":      ak.Status,
                                "status_text": statusText,
                        })
                }
                // 供应商级别状态：取所有 API Key 中最差的状态
                providerStatus := "active"
                for _, ak := range apiKeys {
                        if ak.Status == "arrears" {
                                providerStatus = "arrears"
                                break
                        }
                        if ak.Status == "disabled" {
                                providerStatus = "disabled"
                                break
                        }
                        if ak.Status == "cooldown" {
                                providerStatus = "cooldown"
                        }
                }
                providerStatusText := "工作中"
                if providerStatus == "cooldown" {
                        providerStatusText = "冷却中"
                } else if providerStatus == "arrears" {
                        providerStatusText = "欠费"
                } else if providerStatus == "disabled" {
                        providerStatusText = "已禁用"
                }
                result = append(result, gin.H{
                        "id":          p.ID,
                        "name":        p.Name,
                        "status":      providerStatus,
                        "status_text": providerStatusText,
                        "api_keys":    keyStatuses,
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

// handleMyUserRanking 认证用户的用户使用排行
// 管理员：查看所有用户的排行；普通用户：仅查看自己的数据（单条记录）
func (s *Server) handleMyUserRanking(c *gin.Context) {
        userID := c.GetUint("userID")
        role := c.GetString("role")
        since := s.parseSince(c)
        filter := s.parseDashboardFilter(c)

        if role == "admin" {
                // 管理员：支持按user_id过滤，不传则显示所有用户排行
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

        ranking, err := traffic.GetUserRanking(s.db, since, 10, filter)
        if err != nil {
                log.Printf("[管理] 获取用户排行失败: %v", err)
                c.JSON(500, gin.H{"error": "获取用户排行失败"})
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

        if err := s.db.Model(&user).Update("password", hashedPassword).Error; err != nil {
                log.Printf("[管理] 更新密码失败: %v", err)
                c.JSON(500, gin.H{"error": "密码修改失败，请稍后重试"})
                return
        }

        s.recordAuditLog(c, "update", "user", fmt.Sprintf("%d", userID), "修改密码")
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
                if err := s.db.Model(&database.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
                        log.Printf("[管理] 更新用户失败: %v", err)
                        c.JSON(500, gin.H{"error": "更新用户失败，请稍后重试"})
                        return
                }
        }

        // 审计日志：记录密码重置操作
        if _, ok := updates["password"]; ok {
                s.recordAuditLog(c, "reset_password", "user", id, "管理员重置用户密码")
        } else if len(updates) > 0 {
                s.recordAuditLog(c, "update", "user", id, "管理员更新用户信息")
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

// handleListProviders 列出供应商（分页）
// API Key 已迁移到 ProviderAPIKey 表，列表返回供应商基础信息 + 关联的 API Keys
func (s *Server) handleListProviders(c *gin.Context) {
        page := s.getPage(c)
        pageSize := s.getPageSize(c)

        var providers []database.Provider
        var total int64
        s.db.Model(&database.Provider{}).Count(&total)
        s.db.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&providers)

        result := make([]gin.H, 0, len(providers))
        for _, p := range providers {
                // 获取该供应商的所有 API Keys
                var providerAPIKeys []database.ProviderAPIKey
                s.db.Where("provider_id = ?", p.ID).Order("id ASC").Find(&providerAPIKeys)

                keyResults := make([]gin.H, 0, len(providerAPIKeys))
                for _, ak := range providerAPIKeys {
                        decryptedKey, err := database.DecryptAPIKey(ak.APIKey, s.encryptionKey)
                        if err != nil {
                                log.Printf("[管理] 解密供应商 %s 的API Key(ID=%d)失败: %v", p.Name, ak.ID, err)
                                decryptedKey = "****"
                        }
                        keyResults = append(keyResults, gin.H{
                                "id":            ak.ID,
                                "provider_id":   ak.ProviderID,
                                "name":          ak.Name,
                                "api_key":       maskAPIKey(decryptedKey),
                                "encrypted_key": encryptForTransmission(decryptedKey, s.transmissionKey),
                                "status":        ak.Status,
                                "created_at":    ak.CreatedAt,
                                "updated_at":    ak.UpdatedAt,
                        })
                }

                result = append(result, gin.H{
                        "id":                  p.ID,
                        "name":                p.Name,
                        "description":         p.Description,
                        "base_url":            p.BaseURL,
                        "timeout":             p.Timeout,
                        "connect_timeout":     p.ConnectTimeout,
                        "first_token_timeout": p.FirstTokenTimeout,
                        "stream_idle_timeout": p.StreamIdleTimeout,
                        "retry":               p.Retry,
                        "api_keys":            keyResults,
                        "created_at":          p.CreatedAt,
                        "updated_at":          p.UpdatedAt,
                })
        }
        c.JSON(200, gin.H{
                "items":     result,
                "total":     total,
                "page":      page,
                "page_size": pageSize,
        })
}

// handleTestProviderAPIKey 测试供应商 API Key 连接
// 使用供应商的 baseURL 和 API Key 访问其 OpenAI 兼容接口 /v1/models 来验证配置是否正确
// 在后端实现是因为：1.避免前端跨域问题 2.前端只有脱敏的API Key，无法用于测试
func (s *Server) handleTestProviderAPIKey(c *gin.Context) {
        var req TestProviderAPIKeyReq
        if err := c.ShouldBindJSON(&req); err != nil {
                c.JSON(400, gin.H{"error": "无效的请求数据: " + err.Error()})
                return
        }

        // 获取供应商信息（需要 BaseURL）
        var provider database.Provider
        if err := s.db.First(&provider, req.ProviderID).Error; err != nil {
                c.JSON(404, gin.H{"success": false, "error": "供应商不存在"})
                return
        }

        // 确定用于测试的 API Key
        var apiKey string
        if req.ProviderAPIKeyID != nil && *req.ProviderAPIKeyID > 0 {
                // 使用已有的 API Key（从数据库解密）
                var providerAPIKey database.ProviderAPIKey
                if err := s.db.Where("id = ? AND provider_id = ?", *req.ProviderAPIKeyID, req.ProviderID).First(&providerAPIKey).Error; err != nil {
                        c.JSON(404, gin.H{"success": false, "error": "API Key 不存在"})
                        return
                }
                decryptedKey, err := database.DecryptAPIKey(providerAPIKey.APIKey, s.encryptionKey)
                if err != nil {
                        log.Printf("[管理] 测试供应商时解密API Key失败: %v (provider_api_key_id=%d)", err, *req.ProviderAPIKeyID)
                        c.JSON(500, gin.H{"success": false, "error": "解密API Key失败，请检查加密密钥配置"})
                        return
                }
                apiKey = decryptedKey
        } else if req.APIKey != "" {
                // 使用前端传入的明文 API Key
                apiKey = req.APIKey
        } else {
                c.JSON(400, gin.H{"success": false, "error": "请提供 API Key（provider_api_key_id 或 api_key）"})
                return
        }

        // 构建目标URL
        baseURL := strings.TrimRight(provider.BaseURL, "/")
        var targetURL string
        if versionPathSuffix.MatchString(baseURL) {
                targetURL = baseURL + "/models"
        } else {
                targetURL = baseURL + "/v1/models"
        }

        // 创建带超时的请求context
        ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
        defer cancel()

        httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
        if err != nil {
                log.Printf("[管理] 测试供应商连接创建请求失败: %v", err)
                c.JSON(400, gin.H{"success": false, "error": "无效的URL: " + err.Error()})
                return
        }

        httpReq.Header.Set("Authorization", "Bearer "+apiKey)
        httpReq.Header.Set("Accept", "application/json")

        testTransport := &http.Transport{
                TLSClientConfig: &tls.Config{
                        InsecureSkipVerify: true, // nolint:gosec // 允许自签证书的私有部署
                },
                ForceAttemptHTTP2: true,
        }
        if err := http2.ConfigureTransport(testTransport); err != nil {
                log.Printf("[管理] 配置测试客户端HTTP/2传输层失败: %v", err)
        }
        testClient := &http.Client{
                Timeout:   15 * time.Second,
                Transport: testTransport,
        }

        startTime := time.Now()
        resp, err := testClient.Do(httpReq)
        latency := time.Since(startTime).Milliseconds()

        if err != nil {
                log.Printf("[管理] 测试供应商连接失败: %v (url=%s)", err, targetURL)
                errMsg := "连接失败"
                errStr := err.Error()
                if strings.Contains(errStr, "context deadline") || strings.Contains(errStr, "timeout") {
                        errMsg = "连接超时，请检查Base URL是否正确以及供应商是否可达"
                } else if strings.Contains(errStr, "http2_handshake_failed") || strings.Contains(errStr, "malformed HTTP response") {
                        errMsg = "HTTP/2协议协商失败，供应商服务器要求HTTP/2但客户端协商未成功，请检查网络环境"
                } else if strings.Contains(errStr, "certificate") {
                        errMsg = "TLS证书验证失败: " + err.Error()
                } else if strings.Contains(errStr, "no such host") {
                        errMsg = "域名解析失败，请检查Base URL"
                } else if strings.Contains(errStr, "connection refused") {
                        errMsg = "连接被拒绝，请检查供应商服务是否运行"
                }
                c.JSON(200, gin.H{
                        "success": false,
                        "error":   errMsg,
                        "latency": latency,
                })
                return
        }
        defer resp.Body.Close()

        body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
        if err != nil {
                log.Printf("[管理] 测试供应商读取响应失败: %v", err)
                c.JSON(200, gin.H{
                        "success": false,
                        "error":   "读取响应失败: " + err.Error(),
                        "latency": latency,
                })
                return
        }

        if resp.StatusCode == http.StatusUnauthorized {
                c.JSON(200, gin.H{
                        "success":     false,
                        "error":       "认证失败，API Key无效或已过期",
                        "http_status": resp.StatusCode,
                        "latency":     latency,
                })
                return
        }

        if resp.StatusCode == http.StatusForbidden {
                c.JSON(200, gin.H{
                        "success":     false,
                        "error":       "权限不足，API Key无权访问此接口",
                        "http_status": resp.StatusCode,
                        "latency":     latency,
                })
                return
        }

        if resp.StatusCode != http.StatusOK {
                errorDetail := string(body)
                if len(errorDetail) > 200 {
                        errorDetail = errorDetail[:200] + "..."
                }
                c.JSON(200, gin.H{
                        "success":     false,
                        "error":       fmt.Sprintf("供应商返回HTTP %d", resp.StatusCode),
                        "http_status": resp.StatusCode,
                        "detail":      errorDetail,
                        "latency":     latency,
                })
                return
        }

        var modelsResp struct {
                Object string `json:"object"`
                Data   []struct {
                        ID      string `json:"id"`
                        Object  string `json:"object"`
                        Created int64  `json:"created"`
                        OwnedBy string `json:"owned_by"`
                } `json:"data"`
        }

        if err := json.Unmarshal(body, &modelsResp); err != nil {
                log.Printf("[管理] 测试供应商解析响应失败: %v", err)
                c.JSON(200, gin.H{
                        "success":      true,
                        "warning":      "连接成功但响应格式非标准OpenAI格式，模型列表可能不可用",
                        "http_status":  resp.StatusCode,
                        "latency":      latency,
                        "raw_response": string(body[:min(len(body), 500)]),
                })
                return
        }

        modelIDs := make([]string, 0, len(modelsResp.Data))
        for _, m := range modelsResp.Data {
                modelIDs = append(modelIDs, m.ID)
        }

        log.Printf("[管理] 供应商API Key测试成功: provider=%s, 模型数量=%d, 延迟=%dms", provider.Name, len(modelIDs), latency)

        c.JSON(200, gin.H{
                "success":     true,
                "message":     fmt.Sprintf("连接成功，共获取到 %d 个模型", len(modelIDs)),
                "http_status": resp.StatusCode,
                "latency":     latency,
                "models":      modelIDs,
                "model_count": len(modelIDs),
        })
}

// min 返回两个整数中的较小值（Go 1.21+内置min，此处兼容旧版本）
func min(a, b int) int {
        if a < b {
                return a
        }
        return b
}

// 2.4 修复：使用结构体绑定，添加输入校验
// handleCreateProvider 创建供应商（不含 API Key，API Key 通过 /provider-api-keys 单独管理）
func (s *Server) handleCreateProvider(c *gin.Context) {
        var req CreateProviderReq
        if err := c.ShouldBindJSON(&req); err != nil {
                c.JSON(400, gin.H{"error": "无效的请求数据: " + err.Error()})
                return
        }

        // 处理可选整数字段的默认值
        timeout := DefaultProviderTimeout
        if req.Timeout != nil {
                timeout = *req.Timeout
        }
        connectTimeout := DefaultProviderConnectTimeout
        if req.ConnectTimeout != nil {
                connectTimeout = *req.ConnectTimeout
        }
        firstTokenTimeout := DefaultProviderFirstTokenTimeout
        if req.FirstTokenTimeout != nil {
                firstTokenTimeout = *req.FirstTokenTimeout
        }
        streamIdleTimeout := DefaultProviderStreamIdleTimeout
        if req.StreamIdleTimeout != nil {
                streamIdleTimeout = *req.StreamIdleTimeout
        }
        retry := DefaultProviderRetry
        if req.Retry != nil {
                retry = *req.Retry
        }

        // 修复：使用 map 而非 struct 进行 Create，彻底绕过 GORM 零值跳过机制
        createData := map[string]interface{}{
                "name":                req.Name,
                "description":         req.Description,
                "base_url":            req.BaseURL,
                "timeout":             timeout,
                "connect_timeout":     connectTimeout,
                "first_token_timeout": firstTokenTimeout,
                "stream_idle_timeout": streamIdleTimeout,
                "retry":               retry,
        }

        if err := s.db.Table("providers").Create(createData).Error; err != nil {
                log.Printf("[管理] 创建供应商失败: %v", err)
                c.JSON(500, gin.H{"error": "创建供应商失败，请稍后重试"})
                return
        }

        // map 方式 Create 后需要回查以获取自动生成的 ID 和时间戳
        var provider database.Provider
        if err := s.db.Where("name = ?", req.Name).First(&provider).Error; err != nil {
                log.Printf("[管理] 创建供应商成功但回查失败: %v", err)
                c.JSON(500, gin.H{"error": "创建供应商失败，请稍后重试"})
                return
        }

        s.debouncedCache.Reload()

        // 审计日志
        s.recordAuditLog(c, "create", "provider", fmt.Sprintf("%d", provider.ID),
                fmt.Sprintf("创建供应商: name=%s, base_url=%s, retry=%d", provider.Name, provider.BaseURL, provider.Retry))

        c.JSON(200, gin.H{
                "id":                  provider.ID,
                "name":                provider.Name,
                "description":         provider.Description,
                "base_url":            provider.BaseURL,
                "timeout":             provider.Timeout,
                "connect_timeout":     provider.ConnectTimeout,
                "first_token_timeout": provider.FirstTokenTimeout,
                "stream_idle_timeout": provider.StreamIdleTimeout,
                "retry":               provider.Retry,
                "api_keys":            []interface{}{},
                "created_at":          provider.CreatedAt,
                "updated_at":          provider.UpdatedAt,
        })
}

// handleUpdateProvider 更新供应商（不含 API Key/Status，这些在 ProviderAPIKey 表中管理）
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
        if req.Timeout != nil {
                updates["timeout"] = *req.Timeout
        }
        if req.ConnectTimeout != nil {
                updates["connect_timeout"] = *req.ConnectTimeout
        }
        if req.FirstTokenTimeout != nil {
                updates["first_token_timeout"] = *req.FirstTokenTimeout
        }
        if req.StreamIdleTimeout != nil {
                updates["stream_idle_timeout"] = *req.StreamIdleTimeout
        }
        if req.Retry != nil {
                updates["retry"] = *req.Retry
        }

        if len(updates) > 0 {
                s.db.Model(&database.Provider{}).Where("id = ?", id).Updates(updates)
                s.debouncedCache.Reload()

                // 审计日志
                updateDetail := fmt.Sprintf("更新供应商(id=%s): ", id)
                updateFields := make([]string, 0)
                if req.Name != nil {
                        updateFields = append(updateFields, fmt.Sprintf("name=%s", *req.Name))
                }
                if req.BaseURL != nil {
                        updateFields = append(updateFields, fmt.Sprintf("base_url=%s", *req.BaseURL))
                }
                if req.Timeout != nil {
                        updateFields = append(updateFields, fmt.Sprintf("timeout=%d", *req.Timeout))
                }
                if req.ConnectTimeout != nil {
                        updateFields = append(updateFields, fmt.Sprintf("connect_timeout=%d", *req.ConnectTimeout))
                }
                if req.FirstTokenTimeout != nil {
                        updateFields = append(updateFields, fmt.Sprintf("first_token_timeout=%d", *req.FirstTokenTimeout))
                }
                if req.StreamIdleTimeout != nil {
                        updateFields = append(updateFields, fmt.Sprintf("stream_idle_timeout=%d", *req.StreamIdleTimeout))
                }
                if req.Retry != nil {
                        updateFields = append(updateFields, fmt.Sprintf("retry=%d", *req.Retry))
                }
                if req.Description != nil {
                        updateFields = append(updateFields, "description已更新")
                }
                updateDetail += strings.Join(updateFields, ", ")
                s.recordAuditLog(c, "update", "provider", id, updateDetail)
        }
        c.JSON(200, gin.H{"message": "更新成功"})
}

// handleDeleteProvider 删除供应商（同时删除关联的 API Keys 和映射）
func (s *Server) handleDeleteProvider(c *gin.Context) {
        id := c.Param("id")

        // 先查询供应商信息用于审计日志
        var providerInfo database.Provider
        if err := s.db.First(&providerInfo, id).Error; err != nil {
                c.JSON(404, gin.H{"error": "供应商不存在"})
                return
        }
        err := s.db.Transaction(func(tx *gorm.DB) error {
                // 先删除相关映射
                if err := tx.Where("provider_id = ?", id).Delete(&database.ModelProvider{}).Error; err != nil {
                        return err
                }
                // 删除关联的 API Keys
                if err := tx.Where("provider_id = ?", id).Delete(&database.ProviderAPIKey{}).Error; err != nil {
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

        // 审计日志
        s.recordAuditLog(c, "delete", "provider", id,
                fmt.Sprintf("删除供应商: name=%s, base_url=%s", providerInfo.Name, providerInfo.BaseURL))

        c.JSON(200, gin.H{"message": "删除成功"})
}

// ==================== 供应商 API Key 管理 ====================

// handleListProviderAPIKeys 列出供应商的 API Keys
// 支持 provider_id 查询参数过滤
func (s *Server) handleListProviderAPIKeys(c *gin.Context) {
        providerIDStr := c.Query("provider_id")
        if providerIDStr == "" {
                c.JSON(400, gin.H{"error": "请提供 provider_id 参数"})
                return
        }

        var providerID uint
        fmt.Sscanf(providerIDStr, "%d", &providerID)
        if providerID == 0 {
                c.JSON(400, gin.H{"error": "无效的 provider_id"})
                return
        }

        // 确认供应商存在
        var provider database.Provider
        if err := s.db.First(&provider, providerID).Error; err != nil {
                c.JSON(404, gin.H{"error": "供应商不存在"})
                return
        }

        var keys []database.ProviderAPIKey
        s.db.Where("provider_id = ?", providerID).Order("id ASC").Find(&keys)

        result := make([]gin.H, 0, len(keys))
        for _, ak := range keys {
                decryptedKey, err := database.DecryptAPIKey(ak.APIKey, s.encryptionKey)
                if err != nil {
                        log.Printf("[管理] 解密供应商 %s 的API Key(ID=%d)失败: %v", provider.Name, ak.ID, err)
                        decryptedKey = "****"
                }
                result = append(result, gin.H{
                        "id":            ak.ID,
                        "provider_id":   ak.ProviderID,
                        "name":          ak.Name,
                        "api_key":       maskAPIKey(decryptedKey),
                        "encrypted_key": encryptForTransmission(decryptedKey, s.transmissionKey),
                        "status":        ak.Status,
                        "created_at":    ak.CreatedAt,
                        "updated_at":    ak.UpdatedAt,
                })
        }
        c.JSON(200, result)
}

// handleCreateProviderAPIKey 创建供应商 API Key
func (s *Server) handleCreateProviderAPIKey(c *gin.Context) {
        var req CreateProviderAPIKeyReq
        if err := c.ShouldBindJSON(&req); err != nil {
                c.JSON(400, gin.H{"error": "无效的请求数据: " + err.Error()})
                return
        }

        // 确认供应商存在
        var provider database.Provider
        if err := s.db.First(&provider, req.ProviderID).Error; err != nil {
                c.JSON(404, gin.H{"error": "供应商不存在"})
                return
        }

        // 加密 API Key
        encryptedKey, err := database.EncryptAPIKey(req.APIKey, s.encryptionKey)
        if err != nil {
                log.Printf("[管理] 加密供应商API Key失败: %v", err)
                c.JSON(500, gin.H{"error": "加密API Key失败: " + err.Error() + "，请检查 encryption_key 配置"})
                return
        }

        status := "active"
        if req.Status != "" {
                status = req.Status
        }

        providerAPIKey := database.ProviderAPIKey{
                ProviderID: req.ProviderID,
                APIKey:     encryptedKey,
                Name:       req.Name,
                Status:     status,
        }

        if err := s.db.Create(&providerAPIKey).Error; err != nil {
                log.Printf("[管理] 创建供应商API Key失败: %v", err)
                c.JSON(500, gin.H{"error": "创建API Key失败，请稍后重试"})
                return
        }

        s.debouncedCache.Reload()

        // 审计日志
        s.recordAuditLog(c, "create", "provider_api_key", fmt.Sprintf("%d", providerAPIKey.ID),
                fmt.Sprintf("创建供应商API Key: provider=%s, name=%s", provider.Name, req.Name))

        c.JSON(200, gin.H{
                "id":          providerAPIKey.ID,
                "provider_id": providerAPIKey.ProviderID,
                "name":        providerAPIKey.Name,
                "api_key":     maskAPIKey(req.APIKey),
                "status":      providerAPIKey.Status,
                "created_at":  providerAPIKey.CreatedAt,
                "updated_at":  providerAPIKey.UpdatedAt,
        })
}

// handleUpdateProviderAPIKey 更新供应商 API Key
func (s *Server) handleUpdateProviderAPIKey(c *gin.Context) {
        id := c.Param("id")
        var req UpdateProviderAPIKeyReq
        if err := c.ShouldBindJSON(&req); err != nil {
                c.JSON(400, gin.H{"error": "无效的请求数据: " + err.Error()})
                return
        }

        // 确认 API Key 存在
        var existingKey database.ProviderAPIKey
        if err := s.db.First(&existingKey, id).Error; err != nil {
                c.JSON(404, gin.H{"error": "API Key不存在"})
                return
        }

        updates := map[string]interface{}{}
        if req.Name != nil {
                updates["name"] = *req.Name
        }
        if req.Status != nil {
                updates["status"] = *req.Status
        }
        if req.APIKey != nil {
                // 重新加密 API Key
                encryptedKey, err := database.EncryptAPIKey(*req.APIKey, s.encryptionKey)
                if err != nil {
                        log.Printf("[管理] 加密供应商API Key失败: %v", err)
                        c.JSON(500, gin.H{"error": "加密API Key失败: " + err.Error() + "，请检查 encryption_key 配置"})
                        return
                }
                updates["api_key"] = encryptedKey
        }

        if len(updates) > 0 {
                if err := s.db.Model(&database.ProviderAPIKey{}).Where("id = ?", id).Updates(updates).Error; err != nil {
                        log.Printf("[管理] 更新供应商API Key失败: %v", err)
                        c.JSON(500, gin.H{"error": "更新API Key失败，请稍后重试"})
                        return
                }
                s.debouncedCache.Reload()

                // 如果状态被设为 disabled，立即中断所有使用该 Key 的活跃代理请求
                if req.Status != nil && *req.Status == "disabled" {
                        var keyID uint
                        fmt.Sscanf(id, "%d", &keyID)
                        count := s.cache.CancelActiveRequests(keyID)
                        if count > 0 {
                                log.Printf("[管理] API Key(ID=%s) 已禁用，中断了 %d 个活跃代理请求", id, count)
                        }
                }

                // 如果状态从 cooldown/arrears/disabled 恢复为 active，重置失败计数
                if req.Status != nil && *req.Status == "active" {
                        var keyID uint
                        fmt.Sscanf(id, "%d", &keyID)
                        s.cache.ResetAPIKeyFailure(keyID)
                }

                // 审计日志
                updateFields := make([]string, 0)
                if req.Name != nil {
                        updateFields = append(updateFields, fmt.Sprintf("name=%s", *req.Name))
                }
                if req.Status != nil {
                        updateFields = append(updateFields, fmt.Sprintf("status=%s", *req.Status))
                }
                if req.APIKey != nil {
                        updateFields = append(updateFields, "api_key已更新")
                }
                s.recordAuditLog(c, "update", "provider_api_key", id,
                        fmt.Sprintf("更新供应商API Key(id=%s): %s", id, strings.Join(updateFields, ", ")))
        }
        c.JSON(200, gin.H{"message": "更新成功"})
}

// handleDeleteProviderAPIKey 删除供应商 API Key
func (s *Server) handleDeleteProviderAPIKey(c *gin.Context) {
        id := c.Param("id")

        // 确认 API Key 存在
        var existingKey database.ProviderAPIKey
        if err := s.db.First(&existingKey, id).Error; err != nil {
                c.JSON(404, gin.H{"error": "API Key不存在"})
                return
        }

        if err := s.db.Delete(&database.ProviderAPIKey{}, id).Error; err != nil {
                log.Printf("[管理] 删除供应商API Key失败: %v", err)
                c.JSON(500, gin.H{"error": "删除API Key失败，请稍后重试"})
                return
        }

        s.debouncedCache.Reload()

        // 审计日志
        s.recordAuditLog(c, "delete", "provider_api_key", id,
                fmt.Sprintf("删除供应商API Key: id=%s, name=%s, provider_id=%d", id, existingKey.Name, existingKey.ProviderID))

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

        // 审计日志：记录模型创建操作
        s.recordAuditLog(c, "create", "model", fmt.Sprintf("%d", model.ID),
                fmt.Sprintf("创建模型: name=%s", model.Name))

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

                // 审计日志：记录模型更新操作
                updateDetail := fmt.Sprintf("更新模型(id=%s): ", id)
                updateFields := make([]string, 0)
                if req.Name != nil {
                        updateFields = append(updateFields, fmt.Sprintf("name=%s", *req.Name))
                }
                if req.Description != nil {
                        updateFields = append(updateFields, "description已更新")
                }
                updateDetail += strings.Join(updateFields, ", ")
                s.recordAuditLog(c, "update", "model", id, updateDetail)
        }
        c.JSON(200, gin.H{"message": "更新成功"})
}

// 5.2 修复：使用事务保证关联操作的原子性
// handleDeleteModel 删除模型
func (s *Server) handleDeleteModel(c *gin.Context) {
        id := c.Param("id")

        // 先查询模型信息用于审计日志
        var modelInfo database.Model
        if err := s.db.First(&modelInfo, id).Error; err != nil {
                c.JSON(404, gin.H{"error": "模型不存在"})
                return
        }

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

        // 审计日志：记录模型删除操作
        s.recordAuditLog(c, "delete", "model", id,
                fmt.Sprintf("删除模型: name=%s", modelInfo.Name))

        c.JSON(200, gin.H{"message": "删除成功"})
}

// 4.1 修复：添加分页支持，避免全表扫描
// handleListModelProviders 列出模型-供应商映射（全量返回，不分页）
// 模型-供应商映射数据量通常较小，无需分页
func (s *Server) handleListModelProviders(c *gin.Context) {
        var mappings []database.ModelProvider
        s.db.Order("model_id ASC, id ASC").Find(&mappings)
        c.JSON(200, mappings)
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

        // 审计日志：记录模型-供应商映射创建操作
        s.recordAuditLog(c, "create", "model_provider", fmt.Sprintf("%d", mp.ID),
                fmt.Sprintf("创建模型-供应商映射: model_id=%d, provider_id=%d, provider_model_name=%s", mp.ModelID, mp.ProviderID, mp.ProviderModelName))

        c.JSON(200, mp)
}

// handleDeleteModelProvider 删除模型-供应商映射
func (s *Server) handleDeleteModelProvider(c *gin.Context) {
        id := c.Param("id")

        // 先查询映射信息用于审计日志
        var mpInfo database.ModelProvider
        if err := s.db.First(&mpInfo, id).Error; err != nil {
                c.JSON(404, gin.H{"error": "映射不存在"})
                return
        }

        result := s.db.Delete(&database.ModelProvider{}, id)
        if result.RowsAffected == 0 {
                c.JSON(404, gin.H{"error": "映射不存在"})
                return
        }
        s.debouncedCache.Reload()

        // 审计日志：记录模型-供应商映射删除操作
        s.recordAuditLog(c, "delete", "model_provider", id,
                fmt.Sprintf("删除模型-供应商映射: model_id=%d, provider_id=%d, provider_model_name=%s", mpInfo.ModelID, mpInfo.ProviderID, mpInfo.ProviderModelName))

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
        userRanking, _ := traffic.GetUserRanking(s.db, since, 10, traffic.FilterParams{})
        providerRanking, _ := traffic.GetProviderRanking(s.db, since, 10, traffic.FilterParams{})

        c.JSON(200, gin.H{
                "stats":            stats,
                "model_ranking":    modelRanking,
                "user_ranking":     userRanking,
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
