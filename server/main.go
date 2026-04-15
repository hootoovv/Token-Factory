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

	"encoding/base64"
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

	// 6. 读取加密密钥，用于供应商API Key加密存储
	// 优先级：环境变量 ENCRYPTION_KEY > 配置文件 encryption_key > 不加密（留空）
	encryptionKey := os.Getenv("ENCRYPTION_KEY")
	if encryptionKey != "" {
		log.Printf("[安全] 已从环境变量 ENCRYPTION_KEY 读取加密密钥，供应商API Key将加密存储")
	} else if cfg.EncryptionKey != "" {
		encryptionKey = cfg.EncryptionKey
		log.Printf("[安全] 已从配置文件读取加密密钥，供应商API Key将加密存储")
	} else {
		log.Printf("[安全] 警告: 未设置 ENCRYPTION_KEY 环境变量或配置文件encryption_key，供应商API Key将以明文存储")
		log.Printf("[安全] 建议执行以下命令生成密钥:")
		log.Printf("[安全]	 openssl rand -base64 32")
		log.Printf("[安全]	 然后设置环境变量 ENCRYPTION_KEY 或写入配置文件 encryption_key")
	}

	// 7. 初始化内存缓存（传入加密密钥用于自动解密）
	cacheObj := cache.NewCache(db, encryptionKey)
	log.Printf("[缓存] 数据加载完成")

	// 8. 初始化流量记录器
	recorder := traffic.NewRecorder(db, 2000)
	recorder.Start()
	defer recorder.Stop()
	log.Printf("[流量] 记录器已启动")

	// 9. 2.5 修复：JWT密钥优先从环境变量 JWT_SECRET 读取，否则使用配置文件中的值
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

	// 10. 启动代理服务器 (:11444)
	proxyServer := proxy.NewServer(cacheObj, recorder, &cfg.Proxy)
	go func() {
		if err := proxyServer.Start(cfg.ProxyListen); err != nil {
			log.Fatalf("代理服务器启动失败: %v", err)
		}
	}()

	// 11. 生成API Key传输加密密钥（每次启动随机生成，重启后前端需重新登录获取新密钥）
	transmissionKeyBytes := make([]byte, 32)
	if _, err := rand.Read(transmissionKeyBytes); err != nil {
		log.Fatalf("生成传输加密密钥失败: %v", err)
	}
	transmissionKey := base64.StdEncoding.EncodeToString(transmissionKeyBytes)
	log.Printf("[安全] 已生成API Key传输加密密钥")

	// 12. 启动管理服务器 (:8080)（传入加密密钥和传输密钥）
	// CORS来源配置：环境变量 CORS_ORIGINS > 配置文件 cors_origins > 硬编码默认值
	corsOrigins := os.Getenv("CORS_ORIGINS")
	if corsOrigins != "" {
		log.Printf("[安全] 已从环境变量 CORS_ORIGINS 读取CORS来源配置")
	} else if cfg.CorsOrigins != "" {
		corsOrigins = cfg.CorsOrigins
		log.Printf("[安全] 已从配置文件读取CORS来源配置")
	}
	adminServer := admin.NewServer(db, cacheObj, recorder, jwtSecret, encryptionKey, transmissionKey, corsOrigins, frontendFS)
	if err := adminServer.Start(cfg.AdminListen); err != nil {
		log.Fatalf("管理服务器启动失败: %v", err)
	}
}
