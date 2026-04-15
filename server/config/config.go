package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 主配置结构
type Config struct {
	ProxyListen   string         `yaml:"proxy_listen"`
	AdminListen   string         `yaml:"admin_listen"`
	Database      DatabaseConfig `yaml:"database"`
	Admin         AdminConfig    `yaml:"admin"`
	JWTSecret     string         `yaml:"jwt_secret"`
	EncryptionKey string         `yaml:"encryption_key"` // 供应商API Key加密密钥（base64编码的32字节密钥）
	CorsOrigins   string         `yaml:"cors_origins"`   // CORS允许的来源，逗号分隔；环境变量 CORS_ORIGINS 优先
	Proxy         ProxyConfig    `yaml:"proxy"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Type string `yaml:"type"` // sqlite / mysql / postgres
	DSN  string `yaml:"dsn"`  // 连接字符串
}

// AdminConfig 默认管理员配置
type AdminConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// ProxyConfig 代理策略配置
type ProxyConfig struct {
	ProviderStrategy string `yaml:"provider_strategy"` // sequential / round-robin / random
	SessionAffinity  bool   `yaml:"session_affinity"`  // 会话亲和性
}

// LoadConfig 加载配置文件，文件不存在则返回默认配置
func LoadConfig(path string) (*Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("[配置] 配置文件 %s 不存在，使用默认配置\n", path)
			return cfg, nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 填充默认值
	if cfg.ProxyListen == "" {
		cfg.ProxyListen = ":11444"
	}
	if cfg.AdminListen == "" {
		cfg.AdminListen = ":8080"
	}
	if cfg.Database.Type == "" {
		cfg.Database.Type = "sqlite"
	}
	if cfg.Database.DSN == "" {
		cfg.Database.DSN = "data/token_factory.db"
	}
	if cfg.Admin.Username == "" {
		cfg.Admin.Username = "admin"
	}
	if cfg.Admin.Password == "" {
		cfg.Admin.Password = "admin123"
	}
	if cfg.Proxy.ProviderStrategy == "" {
		cfg.Proxy.ProviderStrategy = "round-robin"
	}

	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		ProxyListen: ":11444",
		AdminListen: ":8080",
		Database: DatabaseConfig{
			Type: "sqlite",
			DSN:  "data/token_factory.db",
		},
		Admin: AdminConfig{
			Username: "admin",
			Password: "admin123",
		},
		JWTSecret:     "",
		EncryptionKey: "",
		CorsOrigins:   "", // 为空时使用硬编码默认值（本地开发环境）
		Proxy: ProxyConfig{
			ProviderStrategy: "round-robin",
			SessionAffinity:  true,
		},
	}
}
