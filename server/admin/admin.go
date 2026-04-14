package admin

import (
	"crypto/rand"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"token_factory/cache"
	"token_factory/database"
	"token_factory/middleware"
	"token_factory/traffic"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Server 管理端服务器
type Server struct {
	db        *gorm.DB
	cache     *cache.Cache
	recorder  *traffic.Recorder
	jwtSecret []byte
	frontend  embed.FS
	server    *http.Server
}

// NewServer 创建管理端服务器
func NewServer(db *gorm.DB, c *cache.Cache, r *traffic.Recorder, jwtSecret []byte, frontendFS embed.FS) *Server {
	return &Server{
		db:        db,
		cache:     c,
		recorder:  r,
		jwtSecret: jwtSecret,
		frontend:  frontendFS,
	}
}

// Start 启动管理端服务器
func (s *Server) Start(addr string) error {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// API路由
	api := router.Group("/api")
	{
		// 公开接口（无需认证）
		api.POST("/login", s.handleLogin)
		api.GET("/dashboard/stats", s.handleDashboardStats)
		api.GET("/dashboard/model-ranking", s.handleModelRanking)
		api.GET("/dashboard/provider-ranking", s.handleProviderRanking)
		api.GET("/dashboard/provider-status", s.handleProviderStatus)

		// 需要认证的接口
		auth := api.Group("")
		auth.Use(middleware.AuthRequired(s.jwtSecret))
		{
			// 用户信息
			auth.GET("/me", s.handleMe)

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

				// 缓存重载
				admin.POST("/cache/reload", s.handleCacheReload)
			}
		}
	}

	// 前端静态文件
	distFS, err := fs.Sub(s.frontend, "web/dist")
	if err != nil {
		log.Printf("[管理] 前端文件系统初始化失败: %v，将不提供前端", err)
	} else {
		router.NoRoute(func(c *gin.Context) {
			// 尝试提供静态文件
			path := c.Request.URL.Path
			if path == "/" {
				path = "/index.html"
			}
			c.FileFromFS(path, http.FS(distFS))
		})
	}

	s.server = &http.Server{
		Addr:    addr,
		Handler: router,
	}

	log.Printf("[管理] 服务器启动在 %s", addr)
	return s.server.ListenAndServe()
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
		c.JSON(500, gin.H{"error": "生成Token失败"})
		return
	}

	c.JSON(200, gin.H{
		"token": token,
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
	stats, err := traffic.GetDashboardStats(s.db, since)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, stats)
}

// handleModelRanking 模型使用排行（公开）
func (s *Server) handleModelRanking(c *gin.Context) {
	since := s.parseSince(c)
	ranking, err := traffic.GetModelRanking(s.db, since, 10)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, ranking)
}

// handleProviderRanking 供应商使用排行（公开）
func (s *Server) handleProviderRanking(c *gin.Context) {
	since := s.parseSince(c)
	ranking, err := traffic.GetProviderRanking(s.db, since, 10)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, ranking)
}

// handleProviderStatus 供应商实时状态（公开）
func (s *Server) handleProviderStatus(c *gin.Context) {
	providers := s.cache.GetProviders()
	var result []gin.H
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

// handleListAPIKeys 列出当前用户的API-Key
func (s *Server) handleListAPIKeys(c *gin.Context) {
	userID := c.GetUint("userID")
	var keys []database.APIKey
	s.db.Where("user_id = ?", userID).Find(&keys)
	c.JSON(200, keys)
}

// handleCreateAPIKey 创建API-Key
func (s *Server) handleCreateAPIKey(c *gin.Context) {
	userID := c.GetUint("userID")
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Name = "Default Key"
	}

	// 生成 sk-xxxx 格式的密钥
	keyBytes := make([]byte, 32)
	rand.Read(keyBytes)
	apiKey := fmt.Sprintf("sk-%x", keyBytes)

	newKey := database.APIKey{
		UserID: userID,
		Key:    apiKey,
		Name:   req.Name,
		Status: "active",
	}

	if err := s.db.Create(&newKey).Error; err != nil {
		c.JSON(500, gin.H{"error": "创建API-Key失败"})
		return
	}

	// 重新加载缓存
	go s.cache.Reload()

	c.JSON(200, newKey)
}

// handleDeleteAPIKey 删除API-Key
func (s *Server) handleDeleteAPIKey(c *gin.Context) {
	userID := c.GetUint("userID")
	id := c.Param("id")

	result := s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&database.APIKey{})
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "API-Key不存在"})
		return
	}

	go s.cache.Reload()
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
		c.JSON(500, gin.H{"error": err.Error()})
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
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, stats)
}

// handleChangePassword 修改密码
func (s *Server) handleChangePassword(c *gin.Context) {
	userID := c.GetUint("userID")
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供旧密码和新密码"})
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
		c.JSON(500, gin.H{"error": "加密密码失败"})
		return
	}

	s.db.Model(&user).Update("password", hashedPassword)
	c.JSON(200, gin.H{"message": "密码修改成功"})
}

// ==================== 管理员接口 ====================

// handleListUsers 列出用户
func (s *Server) handleListUsers(c *gin.Context) {
	var users []database.User
	s.db.Find(&users)

	// 隐藏密码字段
	var result []gin.H
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
	c.JSON(200, result)
}

// handleCreateUser 创建用户
func (s *Server) handleCreateUser(c *gin.Context) {
	var req struct {
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required"`
		Role        string `json:"role"`
		DisplayName string `json:"display_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供用户名和密码"})
		return
	}

	if req.Role == "" {
		req.Role = "user"
	}

	hashedPassword, err := database.HashPassword(req.Password)
	if err != nil {
		c.JSON(500, gin.H{"error": "加密密码失败"})
		return
	}

	user := database.User{
		Username:    req.Username,
		Password:    hashedPassword,
		Role:        req.Role,
		DisplayName: req.DisplayName,
	}

	if err := s.db.Create(&user).Error; err != nil {
		c.JSON(500, gin.H{"error": "创建用户失败: " + err.Error()})
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
		updates["role"] = *req.Role
	}
	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.Password != nil && *req.Password != "" {
		hashed, err := database.HashPassword(*req.Password)
		if err != nil {
			c.JSON(500, gin.H{"error": "加密密码失败"})
			return
		}
		updates["password"] = hashed
	}

	if len(updates) > 0 {
		s.db.Model(&database.User{}).Where("id = ?", id).Updates(updates)
	}
	c.JSON(200, gin.H{"message": "更新成功"})
}

// handleDeleteUser 删除用户
func (s *Server) handleDeleteUser(c *gin.Context) {
	id := c.Param("id")
	result := s.db.Delete(&database.User{}, id)
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "用户不存在"})
		return
	}
	// 同时删除该用户的API-Key
	s.db.Where("user_id = ?", id).Delete(&database.APIKey{})
	go s.cache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// handleListProviders 列出供应商
func (s *Server) handleListProviders(c *gin.Context) {
	var providers []database.Provider
	s.db.Find(&providers)
	c.JSON(200, providers)
}

// handleCreateProvider 创建供应商
func (s *Server) handleCreateProvider(c *gin.Context) {
	var provider database.Provider
	if err := c.ShouldBindJSON(&provider); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}
	if provider.Timeout == 0 {
		provider.Timeout = 30
	}
	if provider.Retry == 0 {
		provider.Retry = 3
	}
	if provider.Status == "" {
		provider.Status = "active"
	}

	if err := s.db.Create(&provider).Error; err != nil {
		c.JSON(500, gin.H{"error": "创建供应商失败: " + err.Error()})
		return
	}

	go s.cache.Reload()
	c.JSON(200, provider)
}

// handleUpdateProvider 更新供应商
func (s *Server) handleUpdateProvider(c *gin.Context) {
	id := c.Param("id")
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	// 不允许修改ID
	delete(req, "id")
	delete(req, "created_at")

	s.db.Model(&database.Provider{}).Where("id = ?", id).Updates(req)
	go s.cache.Reload()
	c.JSON(200, gin.H{"message": "更新成功"})
}

// handleDeleteProvider 删除供应商
func (s *Server) handleDeleteProvider(c *gin.Context) {
	id := c.Param("id")
	result := s.db.Delete(&database.Provider{}, id)
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "供应商不存在"})
		return
	}
	// 同时删除相关映射
	s.db.Where("provider_id = ?", id).Delete(&database.ModelProvider{})
	go s.cache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// handleListModels 列出模型
func (s *Server) handleListModels(c *gin.Context) {
	var models []database.Model
	s.db.Find(&models)
	c.JSON(200, models)
}

// handleCreateModel 创建模型
func (s *Server) handleCreateModel(c *gin.Context) {
	var model database.Model
	if err := c.ShouldBindJSON(&model); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	if err := s.db.Create(&model).Error; err != nil {
		c.JSON(500, gin.H{"error": "创建模型失败: " + err.Error()})
		return
	}

	go s.cache.Reload()
	c.JSON(200, model)
}

// handleUpdateModel 更新模型
func (s *Server) handleUpdateModel(c *gin.Context) {
	id := c.Param("id")
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	delete(req, "id")
	delete(req, "created_at")

	s.db.Model(&database.Model{}).Where("id = ?", id).Updates(req)
	go s.cache.Reload()
	c.JSON(200, gin.H{"message": "更新成功"})
}

// handleDeleteModel 删除模型
func (s *Server) handleDeleteModel(c *gin.Context) {
	id := c.Param("id")
	result := s.db.Delete(&database.Model{}, id)
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "模型不存在"})
		return
	}
	s.db.Where("model_id = ?", id).Delete(&database.ModelProvider{})
	go s.cache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// handleListModelProviders 列出模型-供应商映射
func (s *Server) handleListModelProviders(c *gin.Context) {
	var mappings []database.ModelProvider
	s.db.Find(&mappings)
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
		c.JSON(500, gin.H{"error": "创建映射失败: " + err.Error()})
		return
	}

	go s.cache.Reload()
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
	go s.cache.Reload()
	c.JSON(200, gin.H{"message": "删除成功"})
}

// handleAdminStats 管理员统计概览
func (s *Server) handleAdminStats(c *gin.Context) {
	since := s.parseSince(c)

	stats, err := traffic.GetDashboardStats(s.db, since)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	modelRanking, _ := traffic.GetModelRanking(s.db, since, 10)
	providerRanking, _ := traffic.GetProviderRanking(s.db, since, 10)

	c.JSON(200, gin.H{
		"stats":            stats,
		"model_ranking":    modelRanking,
		"provider_ranking": providerRanking,
	})
}

// handleCacheReload 重载缓存
func (s *Server) handleCacheReload(c *gin.Context) {
	if err := s.cache.Reload(); err != nil {
		c.JSON(500, gin.H{"error": "缓存重载失败: " + err.Error()})
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
