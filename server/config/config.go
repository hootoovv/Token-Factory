package config

import (
        "fmt"
        "os"

        "gopkg.in/yaml.v3"
)

// Config 主配置结构
type Config struct {
        ProxyListen     string         `yaml:"proxy_listen"`
        AdminListen     string         `yaml:"admin_listen"`
        Database        DatabaseConfig `yaml:"database"`
        Admin           AdminConfig    `yaml:"admin"`
        JWTSecret       string         `yaml:"jwt_secret"`
        EncryptionKey   string         `yaml:"encryption_key"`    // 供应商API Key加密密钥（base64编码的32字节密钥）
        CorsOrigins     string         `yaml:"cors_origins"`      // CORS允许的来源，逗号分隔；环境变量 CORS_ORIGINS 优先
        CallRecordLimit int            `yaml:"call_record_limit"` // 内存中保留的最近API调用记录条数（默认10，最大20）
        Proxy           ProxyConfig    `yaml:"proxy"`
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

// ProxyTimeoutConfig 代理默认超时配置（未配置供应商超时时的回退值）
type ProxyTimeoutConfig struct {
        Total      int `yaml:"total"`       // 总超时秒数
        Connect    int `yaml:"connect"`     // 连接建立超时秒数
        FirstToken int `yaml:"first_token"` // 首Token返回超时秒数
        StreamIdle int `yaml:"stream_idle"` // 流传输Idle超时秒数
}

// ProxyConfig 代理策略配置
type ProxyConfig struct {
        ProviderStrategy string             `yaml:"provider_strategy"` // sequential / round-robin / random
        SessionAffinity  bool               `yaml:"session_affinity"`  //       会话亲和性
        DefaultTimeouts  ProxyTimeoutConfig `yaml:"default_timeouts"`  // 供应商默认超时配置
        AutoStatus       AutoStatusConfig   `yaml:"auto_status"`       // API Key 自动状态管理配置
}

// AutoStatusConfig API Key 自动状态管理配置
type AutoStatusConfig struct {
        Enabled                bool `yaml:"enabled"`                  // 是否启用自动状态检测（默认 true）
        ConsecutiveFailures    int  `yaml:"consecutive_failures"`      // 连续失败N次后标记为冷却（默认2次）
        CooldownRecoverySec    int  `yaml:"cooldown_recovery_sec"`    // 冷却状态自动恢复时间（秒，默认300=5分钟）
        CooldownCheckInterval  int  `yaml:"cooldown_check_interval"` // 冷却恢复检查间隔（秒，默认60）
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
        if cfg.Proxy.DefaultTimeouts.Total == 0 {
                cfg.Proxy.DefaultTimeouts.Total = 300
        }
        if cfg.Proxy.DefaultTimeouts.Connect == 0 {
                cfg.Proxy.DefaultTimeouts.Connect = 10
        }
        if cfg.Proxy.DefaultTimeouts.FirstToken == 0 {
                cfg.Proxy.DefaultTimeouts.FirstToken = 30
        }
        if cfg.Proxy.DefaultTimeouts.StreamIdle == 0 {
                cfg.Proxy.DefaultTimeouts.StreamIdle = 15
        }

        // 调用记录条数限制
        if cfg.CallRecordLimit <= 0 {
                cfg.CallRecordLimit = 10
        }
        if cfg.CallRecordLimit > 20 {
                cfg.CallRecordLimit = 20
        }

        // 自动状态管理默认值
        if cfg.Proxy.AutoStatus.ConsecutiveFailures == 0 {
                cfg.Proxy.AutoStatus.ConsecutiveFailures = 2
        }
        if cfg.Proxy.AutoStatus.CooldownRecoverySec == 0 {
                cfg.Proxy.AutoStatus.CooldownRecoverySec = 300
        }
        if cfg.Proxy.AutoStatus.CooldownCheckInterval == 0 {
                cfg.Proxy.AutoStatus.CooldownCheckInterval = 60
        }
        // AutoStatus.Enabled 默认 true（yaml 中未显式配置时，Go 零值为 false）
        // 由于 yaml 中 enabled 字段不写时默认为零值 false，但业务语义上应默认开启
        // 因此当配置文件中未出现 auto_status 段时，强制设为 true
        // 判断依据：如果所有 AutoStatus 子字段都是零值，说明用户未配置此段
        if !cfg.Proxy.AutoStatus.Enabled && cfg.Proxy.AutoStatus.ConsecutiveFailures == 0 &&
                cfg.Proxy.AutoStatus.CooldownRecoverySec == 0 && cfg.Proxy.AutoStatus.CooldownCheckInterval == 0 {
                cfg.Proxy.AutoStatus.Enabled = true
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
                JWTSecret:       "",
                EncryptionKey:   "",
                CorsOrigins:     "",  // 为空时使用硬编码默认值（本地开发环境）
                CallRecordLimit: 10,  // 内存中保留的最近API调用记录条数
                Proxy: ProxyConfig{
                        ProviderStrategy: "round-robin",
                        SessionAffinity:  true,
                        DefaultTimeouts: ProxyTimeoutConfig{
                                Total:      300,
                                Connect:    10,
                                FirstToken: 30,
                                StreamIdle: 15,
                        },
                        AutoStatus: AutoStatusConfig{
                                Enabled:               true,
                                ConsecutiveFailures:   2,
                                CooldownRecoverySec:   300,
                                CooldownCheckInterval: 60,
                        },
                },
        }
}
