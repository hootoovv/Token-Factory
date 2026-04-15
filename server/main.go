package main

import (
	"crypto/rand"
	"embed"
	"log"
	"os"

	"token_factory/admin"
	"token_factory/cache"
	"token_factory/config"
	"token_factory/database"
	"token_factory/proxy"
	"token_factory/traffic"
)

//go:embed all:web/dist
var frontendFS embed.FS

func main() {
	// 1. 加载配置
	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2.6 修复：管理员密码优先从环境变量 ADMIN_PASSWORD 读取
	if adminPassword := os.Getenv("ADMIN_PASSWORD"); adminPassword != "" {
		cfg.Admin.Password = adminPassword
		log.Printf("[安全] 已从环境变量 ADMIN_PASSWORD 读取管理员密码")
	}

	log.Printf("========================================")
	log.Printf(" Token Factory - 企业级LLM API代理中心")
	log.Printf("========================================")

	// 3. 初始化数据库
	db, err := database.InitDB(&cfg.Database)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	log.Printf("[数据库] %s 连接成功 (%s)", cfg.Database.Type, cfg.Database.DSN)

	// 4. 自动迁移
	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	log.Printf("[数据库] 表结构迁移完成")

	// 5. 确保默认管理员存在
	if err := database.EnsureDefaultAdmin(db, &cfg.Admin); err != nil {
		log.Fatalf("创建默认管理员失败: %v", err)
	}
	log.Printf("[管理员] 默认管理员已就绪 (用户名: %s)", cfg.Admin.Username)

	// 6. 初始化内存缓存
	cacheObj := cache.NewCache(db)
	log.Printf("[缓存] 数据加载完成")

	// 7. 初始化流量记录器
	recorder := traffic.NewRecorder(db, 2000)
	recorder.Start()
	defer recorder.Stop()
	log.Printf("[流量] 记录器已启动")

	// 8. 2.5 修复：JWT密钥优先从环境变量 JWT_SECRET 读取，否则使用配置文件中的值
	jwtSecretStr := os.Getenv("JWT_SECRET")
	if jwtSecretStr != "" {
		log.Printf("[安全] 已从环境变量 JWT_SECRET 读取JWT密钥")
	} else {
		jwtSecretStr = cfg.JWTSecret
	}

	jwtSecret := []byte(jwtSecretStr)
	if len(jwtSecret) == 0 || string(jwtSecret) == "" {
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			log.Fatalf("生成JWT密钥失败: %v", err)
		}
		jwtSecret = secretBytes
		log.Printf("[安全] 已自动生成JWT密钥（重启后旧Token将失效）")
	}

	// 9. 启动代理服务器 (:11444)
	proxyServer := proxy.NewServer(cacheObj, recorder, &cfg.Proxy)
	go func() {
		if err := proxyServer.Start(cfg.ProxyListen); err != nil {
			log.Fatalf("代理服务器启动失败: %v", err)
		}
	}()

	// 10. 启动管理服务器 (:8080)
	adminServer := admin.NewServer(db, cacheObj, recorder, jwtSecret, frontendFS)
	if err := adminServer.Start(cfg.AdminListen); err != nil {
		log.Fatalf("管理服务器启动失败: %v", err)
	}
}