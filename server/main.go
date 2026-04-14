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

	log.Printf("========================================")
	log.Printf(" Token Factory - 企业级LLM API代理中心")
	log.Printf("========================================")

	// 2. 初始化数据库
	db, err := database.InitDB(&cfg.Database)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	log.Printf("[数据库] %s 连接成功 (%s)", cfg.Database.Type, cfg.Database.DSN)

	// 3. 自动迁移
	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	log.Printf("[数据库] 表结构迁移完成")

	// 4. 确保默认管理员存在
	if err := database.EnsureDefaultAdmin(db, &cfg.Admin); err != nil {
		log.Fatalf("创建默认管理员失败: %v", err)
	}
	log.Printf("[管理员] 默认管理员已就绪 (用户名: %s)", cfg.Admin.Username)

	// 5. 初始化内存缓存
	cacheObj := cache.NewCache(db)
	log.Printf("[缓存] 数据加载完成")

	// 6. 初始化流量记录器
	recorder := traffic.NewRecorder(db, 2000)
	recorder.Start()
	defer recorder.Stop()
	log.Printf("[流量] 记录器已启动")

	// 7. 生成或使用JWT密钥
	jwtSecret := []byte(cfg.JWTSecret)
	if len(jwtSecret) == 0 || string(jwtSecret) == "" {
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			log.Fatalf("生成JWT密钥失败: %v", err)
		}
		jwtSecret = secretBytes
		log.Printf("[安全] 已自动生成JWT密钥（重启后旧Token将失效）")
	}

	// 8. 启动代理服务器 (:11444)
	proxyServer := proxy.NewServer(cacheObj, recorder, &cfg.Proxy)
	go func() {
		if err := proxyServer.Start(cfg.ProxyListen); err != nil {
			log.Fatalf("代理服务器启动失败: %v", err)
		}
	}()

	// 9. 启动管理服务器 (:8080)
	adminServer := admin.NewServer(db, cacheObj, recorder, jwtSecret, frontendFS)
	if err := adminServer.Start(cfg.AdminListen); err != nil {
		log.Fatalf("管理服务器启动失败: %v", err)
	}
}
