package database

import (
        "crypto/aes"
        "crypto/cipher"
        "crypto/rand"
        "encoding/base64"
        "fmt"
        "log"
        "os"
        "path/filepath"
        "time"

        "token_factory/config"

        "github.com/glebarez/sqlite"
        "golang.org/x/crypto/bcrypt"
        "gorm.io/driver/mysql"
        "gorm.io/driver/postgres"
        "gorm.io/gorm"
        "gorm.io/gorm/logger"
)

// 3.1 修复：加密前缀标识，用于区分加密存储和明文存储的API Key
const EncryptedPrefix = "enc:"

// Provider 供应商模型（API Key 已迁移到 ProviderAPIKey 表，一个供应商可对应多个 API Key）
type Provider struct {
        ID                uint   `gorm:"primaryKey" json:"id"`
        Name              string `gorm:"uniqueIndex;size:100;not null" json:"name"`
        Description       string `gorm:"size:500" json:"description"`
        BaseURL           string `gorm:"column:base_url;size:500;not null" json:"base_url"`
        Timeout           int    `gorm:"default:300" json:"timeout"`                                       // 总超时秒数（从请求发送到响应完成的绝对最大时间）
        ConnectTimeout    int    `gorm:"default:10;column:connect_timeout" json:"connect_timeout"`         // 连接建立超时秒数（TCP+TLS握手）
        FirstTokenTimeout int    `gorm:"default:30;column:first_token_timeout" json:"first_token_timeout"` // 首Token返回超时秒数
        StreamIdleTimeout int    `gorm:"default:15;column:stream_idle_timeout" json:"stream_idle_timeout"` // 流传输Idle超时秒数
        Retry             int    `json:"retry"`                                                            // 重试次数（默认值在应用层处理，避免GORM零值问题）
        // 以下旧字段保留用于数据迁移兼容，新代码不再使用
        APIKey    string    `gorm:"column:api_key;size:500" json:"api_key,omitempty"` // 已迁移到 provider_api_keys 表
        Status    string    `gorm:"size:20;default:active" json:"status,omitempty"`   // 已迁移到 provider_api_keys 表
        CreatedAt time.Time `json:"created_at"`
        UpdatedAt time.Time `json:"updated_at"`
}

// ProviderAPIKey 供应商API Key映射表（一个供应商可对应多个 API Key）
type ProviderAPIKey struct {
        ID         uint      `gorm:"primaryKey" json:"id"`
        ProviderID uint      `gorm:"index;not null" json:"provider_id"`               // 外键关联 Provider
        APIKey     string    `gorm:"column:api_key;size:500;not null" json:"api_key"` // 加密存储的 API Key
        Name       string    `gorm:"size:100" json:"name"`                            // API Key 备注名称（如"主用Key"、"备用Key"）
        Status     string    `gorm:"size:20;default:active" json:"status"`            // active/cooldown/arrears/disabled — 状态下沉到 Key 级别
        CreatedAt  time.Time `json:"created_at"`
        UpdatedAt  time.Time `json:"updated_at"`
}

// Model 模型定义
type Model struct {
        ID          uint      `gorm:"primaryKey" json:"id"`
        Name        string    `gorm:"uniqueIndex;size:100;not null" json:"name"`
        Description string    `gorm:"size:500" json:"description"`
        CreatedAt   time.Time `json:"created_at"`
        UpdatedAt   time.Time `json:"updated_at"`
}

// ModelProvider 模型-供应商映射
type ModelProvider struct {
        ID                uint      `gorm:"primaryKey" json:"id"`
        ModelID           uint      `gorm:"index;not null" json:"model_id"`
        ProviderID        uint      `gorm:"index;not null" json:"provider_id"`
        ProviderModelName string    `gorm:"size:100;not null" json:"provider_model_name"` // 供应商侧的模型名
        CreatedAt         time.Time `json:"created_at"`
        UpdatedAt         time.Time `json:"updated_at"`
}

// User 用户
type User struct {
        ID          uint      `gorm:"primaryKey" json:"id"`
        Username    string    `gorm:"uniqueIndex;size:100;not null" json:"username"`
        Password    string    `gorm:"size:200;not null" json:"-"`       // JSON输出时隐藏
        Role        string    `gorm:"size:20;default:user" json:"role"` // admin/user
        DisplayName string    `gorm:"size:100" json:"display_name"`
        CreatedAt   time.Time `json:"created_at"`
        UpdatedAt   time.Time `json:"updated_at"`
}

// APIKey API密钥
type APIKey struct {
        ID        uint      `gorm:"primaryKey" json:"id"`
        UserID    uint      `gorm:"index;not null" json:"user_id"`
        Key       string    `gorm:"uniqueIndex;size:100;not null" json:"key"`
        Name      string    `gorm:"size:100" json:"name"`
        Status    string    `gorm:"size:20;default:active" json:"status"` // active/disabled
        CreatedAt time.Time `json:"created_at"`
        UpdatedAt time.Time `json:"updated_at"`
}

// AuditLog 7.1 审计日志：记录所有管理操作，便于安全事件追溯
type AuditLog struct {
        ID           uint      `gorm:"primaryKey" json:"id"`
        OperatorID   uint      `gorm:"index;not null" json:"operator_id"`         // 操作者用户ID
        OperatorName string    `gorm:"size:100;not null" json:"operator_name"`    // 操作者用户名
        Action       string    `gorm:"size:50;index;not null" json:"action"`      // 操作类型：create/delete/update/login等
        TargetType   string    `gorm:"size:50;index;not null" json:"target_type"` // 操作对象类型：api_key/user/provider/model等
        TargetID     string    `gorm:"size:100;index" json:"target_id"`           // 操作对象ID
        Detail       string    `gorm:"size:500" json:"detail"`                    // 操作详情
        IPAddress    string    `gorm:"size:50" json:"ip_address"`                 // 操作者IP地址
        CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

// TrafficRecord 流量记录（基础表结构，实际存储在按月分表中）
type TrafficRecord struct {
        ID               uint      `gorm:"primaryKey" json:"id"`
        APIKeyID         uint      `gorm:"index" json:"api_key_id"`
        UserID           uint      `gorm:"index" json:"user_id"`
        ModelID          uint      `gorm:"index" json:"model_id"`
        ProviderID       uint      `gorm:"index" json:"provider_id"`
        ProviderAPIKeyID uint      `gorm:"index;column:provider_api_key_id" json:"provider_api_key_id"` // 具体使用的供应商 API Key ID
        InputBytes       int64     `json:"input_bytes"`
        OutputBytes      int64     `json:"output_bytes"`
        StartTime        time.Time `gorm:"index" json:"start_time"`
        EndTime          time.Time `gorm:"index" json:"end_time"`
        Duration         int64     `json:"duration"`              // 毫秒
        Status           string    `gorm:"size:20" json:"status"` // success/error
        CreatedAt        time.Time `gorm:"index" json:"created_at"`
}

// ==================== 3.1 修复：API Key 加密/解密函数 ====================

// 注意：加密密钥为强制配置项，获取逻辑（环境变量 > 配置文件）在 main.go 中统一实现，
// 密钥通过参数传递给各模块，无需在 database 包中单独获取。

// EncryptAPIKey 使用AES-256-GCM加密API Key，返回带前缀的加密字符串
// encryptionKey为必填参数，未配置时返回错误（全新部署强制加密存储）
func EncryptAPIKey(plaintext, encryptionKey string) (string, error) {
        if encryptionKey == "" {
                return "", fmt.Errorf("加密密钥未配置，供应商API Key加密为强制要求，请设置ENCRYPTION_KEY环境变量或配置文件encryption_key")
        }

        key, err := base64.StdEncoding.DecodeString(encryptionKey)
        if err != nil {
                return "", fmt.Errorf("加密密钥base64解码失败: %w", err)
        }

        if len(key) != 32 {
                return "", fmt.Errorf("加密密钥长度必须为32字节(AES-256)，当前为%d字节", len(key))
        }

        block, err := aes.NewCipher(key)
        if err != nil {
                return "", fmt.Errorf("创建AES密码器失败: %w", err)
        }

        gcm, err := cipher.NewGCM(block)
        if err != nil {
                return "", fmt.Errorf("创建GCM模式失败: %w", err)
        }

        nonce := make([]byte, gcm.NonceSize())
        if _, err := rand.Read(nonce); err != nil {
                return "", fmt.Errorf("生成随机nonce失败: %w", err)
        }

        ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
        return EncryptedPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptAPIKey 解密API Key，仅支持带加密前缀的密文格式
// 全新部署下所有API Key均为加密存储，缺少加密前缀视为数据异常
func DecryptAPIKey(ciphertext, encryptionKey string) (string, error) {
        // 检查加密前缀，确认数据格式正确
        if !hasEncryptedPrefix(ciphertext) {
                return "", fmt.Errorf("API Key未加密（缺少加密前缀 %q），数据格式异常", EncryptedPrefix)
        }

        if encryptionKey == "" {
                return "", fmt.Errorf("API Key已加密但未提供解密密钥，请设置ENCRYPTION_KEY环境变量或配置文件encryption_key")
        }

        key, err := base64.StdEncoding.DecodeString(encryptionKey)
        if err != nil {
                return "", fmt.Errorf("加密密钥base64解码失败: %w", err)
        }

        if len(key) != 32 {
                return "", fmt.Errorf("加密密钥长度必须为32字节(AES-256)，当前为%d字节", len(key))
        }

        // 去掉前缀
        encodedData := ciphertext[len(EncryptedPrefix):]
        data, err := base64.StdEncoding.DecodeString(encodedData)
        if err != nil {
                return "", fmt.Errorf("加密数据base64解码失败: %w", err)
        }

        block, err := aes.NewCipher(key)
        if err != nil {
                return "", fmt.Errorf("创建AES密码器失败: %w", err)
        }

        gcm, err := cipher.NewGCM(block)
        if err != nil {
                return "", fmt.Errorf("创建GCM模式失败: %w", err)
        }

        nonceSize := gcm.NonceSize()
        if len(data) < nonceSize {
                return "", fmt.Errorf("加密数据长度不足")
        }

        nonce, encryptedData := data[:nonceSize], data[nonceSize:]
        plaintext, err := gcm.Open(nil, nonce, encryptedData, nil)
        if err != nil {
                return "", fmt.Errorf("解密失败: %w", err)
        }

        return string(plaintext), nil
}

// HasEncryptedPrefix 检查字符串是否以加密前缀开头（导出供其他包使用）
func HasEncryptedPrefix(s string) bool {
        return len(s) > len(EncryptedPrefix) && s[:len(EncryptedPrefix)] == EncryptedPrefix
}

// hasEncryptedPrefix 检查字符串是否以加密前缀开头（内部使用，保持向后兼容）
func hasEncryptedPrefix(s string) bool {
        return HasEncryptedPrefix(s)
}

// GenerateEncryptionKey 生成一个新的32字节AES-256加密密钥（base64编码）
func GenerateEncryptionKey() (string, error) {
        key := make([]byte, 32)
        if _, err := rand.Read(key); err != nil {
                return "", err
        }
        return base64.StdEncoding.EncodeToString(key), nil
}

// ==================== 数据库初始化 ====================

// InitDB 初始化数据库连接
func InitDB(cfg *config.DatabaseConfig) (*gorm.DB, error) {
        gormConfig := &gorm.Config{
                Logger: logger.Default.LogMode(logger.Warn),
        }

        var db *gorm.DB
        var err error

        switch cfg.Type {
        case "sqlite":
                // 确保数据目录存在
                dir := filepath.Dir(cfg.DSN)
                if dir != "" && dir != "." {
                        if err := os.MkdirAll(dir, 0755); err != nil {
                                return nil, fmt.Errorf("创建数据库目录失败: %w", err)
                        }
                }

                // 检查数据目录是否可写（提前发现权限问题，避免 SQLite 返回误导性 "out of memory" 错误）
                checkDir := dir
                if checkDir == "" || checkDir == "." {
                        checkDir = "."
                }
                if err := os.WriteFile(filepath.Join(checkDir, ".db_write_check"), []byte("check"), 0644); err != nil {
                        return nil, fmt.Errorf("数据库目录 %q 不可写 (权限不足)，请检查目录权限或 Docker 挂载卷权限: %w", checkDir, err)
                }
                os.Remove(filepath.Join(checkDir, ".db_write_check")) // 清理临时文件

                db, err = gorm.Open(sqlite.Open(cfg.DSN+"?_journal_mode=WAL"), gormConfig)
        case "mysql":
                db, err = gorm.Open(mysql.Open(cfg.DSN), gormConfig)
        case "postgres":
                db, err = gorm.Open(postgres.Open(cfg.DSN), gormConfig)
        default:
                return nil, fmt.Errorf("不支持的数据库类型: %s", cfg.Type)
        }

        if err != nil {
                return nil, fmt.Errorf("连接数据库失败: %w", err)
        }

        return db, nil
}

// AutoMigrate 自动迁移表结构
func AutoMigrate(db *gorm.DB) error {
        return db.AutoMigrate(
                &Provider{},
                &ProviderAPIKey{}, // 供应商 API Key 映射表
                &Model{},
                &ModelProvider{},
                &User{},
                &APIKey{},
                &AuditLog{}, // 7.1 审计日志表
        )
}

// EnsureDefaultAdmin 确保默认管理员存在
func EnsureDefaultAdmin(db *gorm.DB, cfg *config.AdminConfig) error {
        var count int64
        db.Model(&User{}).Where("role = ?", "admin").Count(&count)
        if count > 0 {
                return nil
        }

        hashedPassword, err := HashPassword(cfg.Password)
        if err != nil {
                return fmt.Errorf("加密密码失败: %w", err)
        }

        admin := User{
                Username:    cfg.Username,
                Password:    hashedPassword,
                Role:        "admin",
                DisplayName: "系统管理员",
        }

        return db.Create(&admin).Error
}

// HashPassword 密码哈希
func HashPassword(password string) (string, error) {
        bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
        return string(bytes), err
}

// CheckPassword 校验密码
func CheckPassword(password, hash string) bool {
        err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
        return err == nil
}

// MigrateProviderAPIKeys 将旧 Provider 表中的 api_key 和 status 迁移到新的 ProviderAPIKey 表
// 幂等操作：已迁移的记录不会重复迁移
func MigrateProviderAPIKeys(db *gorm.DB, encryptionKey string) error {
        // 检查 provider_api_keys 表是否存在
        if !db.Migrator().HasTable(&ProviderAPIKey{}) {
                log.Printf("[迁移] provider_api_keys 表尚未创建，跳过数据迁移")
                return nil
        }

        // 读取所有现有 Provider 记录
        var providers []Provider
        if err := db.Find(&providers).Error; err != nil {
                return fmt.Errorf("读取供应商记录失败: %w", err)
        }

        migrated := 0
        for _, p := range providers {
                // 跳过没有 API Key 的记录
                if p.APIKey == "" {
                        continue
                }

                // 检查该供应商是否已有 API Key 记录（幂等）
                var count int64
                db.Model(&ProviderAPIKey{}).Where("provider_id = ?", p.ID).Count(&count)
                if count > 0 {
                        continue // 已迁移则跳过
                }

                // 创建 ProviderAPIKey 记录
                apiKeyToStore := p.APIKey
                // 如果旧字段中的 API Key 是明文（没有加密前缀），则在迁移时加密
                if !HasEncryptedPrefix(p.APIKey) && encryptionKey != "" {
                        encrypted, encErr := EncryptAPIKey(p.APIKey, encryptionKey)
                        if encErr != nil {
                                log.Printf("[迁移] 加密供应商 %s (ID=%d) 的 API Key 失败: %v，将以明文迁移", p.Name, p.ID, encErr)
                        } else {
                                apiKeyToStore = encrypted
                        }
                }
                providerAPIKey := ProviderAPIKey{
                        ProviderID: p.ID,
                        APIKey:     apiKeyToStore,
                        Name:       "默认Key",
                        Status:     p.Status,
                }
                if err := db.Create(&providerAPIKey).Error; err != nil {
                        log.Printf("[迁移] 迁移供应商 %s (ID=%d) 的 API Key 失败: %v", p.Name, p.ID, err)
                        continue
                }
                migrated++
        }

        if migrated > 0 {
                log.Printf("[迁移] 成功迁移 %d 个供应商的 API Key 到 provider_api_keys 表", migrated)
        } else {
                log.Printf("[迁移] 无需迁移，所有供应商的 API Key 已在 provider_api_keys 表中")
        }

        return nil
}

// GetTrafficTableName 获取流量记录按月分表的表名
func GetTrafficTableName(t time.Time) string {
        return fmt.Sprintf("traffic_records_%s", t.Format("200601"))
}

// EnsureTrafficTable 确保按月分表存在
func EnsureTrafficTable(db *gorm.DB, t time.Time) error {
        tableName := GetTrafficTableName(t)

        // 检查表是否已存在
        if db.Migrator().HasTable(tableName) {
                return nil
        }

        // 创建表
        createSQL := fmt.Sprintf(`
                                CREATE TABLE IF NOT EXISTS %s (
                                                                id INTEGER PRIMARY KEY AUTOINCREMENT,
                                                                api_key_id INTEGER NOT NULL DEFAULT 0,
                                                                user_id INTEGER NOT NULL DEFAULT 0,
                                                                model_id INTEGER NOT NULL DEFAULT 0,
                                                                provider_id INTEGER NOT NULL DEFAULT 0,
                                                                provider_api_key_id INTEGER NOT NULL DEFAULT 0,
                                                                input_bytes INTEGER NOT NULL DEFAULT 0,
                                                                output_bytes INTEGER NOT NULL DEFAULT 0,
                                                                start_time DATETIME NOT NULL,
                                                                end_time DATETIME NOT NULL,
                                                                duration INTEGER NOT NULL DEFAULT 0,
                                                                status VARCHAR(20) NOT NULL DEFAULT 'success',
                                                                created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
                                )`, tableName)

        if err := db.Exec(createSQL).Error; err != nil {
                return err
        }

        // 逐个创建索引（SQLite不支持一次Exec多条语句）
        indexes := []string{
                fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_api_key_id ON %s(api_key_id)", tableName, tableName),
                fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_user_id ON %s(user_id)", tableName, tableName),
                fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_model_id ON %s(model_id)", tableName, tableName),
                fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_provider_id ON %s(provider_id)", tableName, tableName),
                fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_provider_api_key_id ON %s(provider_api_key_id)", tableName, tableName),
                fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_created_at ON %s(created_at)", tableName, tableName),
        }

        for _, idxSQL := range indexes {
                if err := db.Exec(idxSQL).Error; err != nil {
                        log.Printf("[数据库] 创建索引失败(可忽略): %v", err)
                }
        }

        return nil
}

// EnsureTrafficTableMySQL MySQL版本的建表语句
func EnsureTrafficTableMySQL(db *gorm.DB, t time.Time) error {
        tableName := GetTrafficTableName(t)

        sql := fmt.Sprintf(`
                                CREATE TABLE IF NOT EXISTS %s (
                                                                id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
                                                                api_key_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
                                                                user_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
                                                                model_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
                                                                provider_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
                                                                provider_api_key_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
                                                                input_bytes BIGINT NOT NULL DEFAULT 0,
                                                                output_bytes BIGINT NOT NULL DEFAULT 0,
                                                                start_time DATETIME(3) NOT NULL,
                                                                end_time DATETIME(3) NOT NULL,
                                                                duration BIGINT NOT NULL DEFAULT 0,
                                                                status VARCHAR(20) NOT NULL DEFAULT 'success',
                                                                created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
                                                                INDEX idx_api_key_id (api_key_id),
                                                                INDEX idx_user_id (user_id),
                                                                INDEX idx_model_id (model_id),
                                                                INDEX idx_provider_id (provider_id),
                                                                INDEX idx_provider_api_key_id (provider_api_key_id),
                                                                INDEX idx_created_at (created_at)
                                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
                                `, tableName)

        return db.Exec(sql).Error
}

// EnsureTrafficTablePostgres PostgreSQL版本的建表语句
func EnsureTrafficTablePostgres(db *gorm.DB, t time.Time) error {
        tableName := GetTrafficTableName(t)

        sql := fmt.Sprintf(`
                                CREATE TABLE IF NOT EXISTS %s (
                                                                id SERIAL PRIMARY KEY,
                                                                api_key_id INTEGER NOT NULL DEFAULT 0,
                                                                user_id INTEGER NOT NULL DEFAULT 0,
                                                                model_id INTEGER NOT NULL DEFAULT 0,
                                                                provider_id INTEGER NOT NULL DEFAULT 0,
                                                                provider_api_key_id INTEGER NOT NULL DEFAULT 0,
                                                                input_bytes BIGINT NOT NULL DEFAULT 0,
                                                                output_bytes BIGINT NOT NULL DEFAULT 0,
                                                                start_time TIMESTAMPTZ NOT NULL,
                                                                end_time TIMESTAMPTZ NOT NULL,
                                                                duration BIGINT NOT NULL DEFAULT 0,
                                                                status VARCHAR(20) NOT NULL DEFAULT 'success',
                                                                created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
                                );
                                CREATE INDEX IF NOT EXISTS idx_%s_api_key_id ON %s(api_key_id);
                                CREATE INDEX IF NOT EXISTS idx_%s_user_id ON %s(user_id);
                                CREATE INDEX IF NOT EXISTS idx_%s_model_id ON %s(model_id);
                                CREATE INDEX IF NOT EXISTS idx_%s_provider_id ON %s(provider_id);
                                CREATE INDEX IF NOT EXISTS idx_%s_provider_api_key_id ON %s(provider_api_key_id);
                                CREATE INDEX IF NOT EXISTS idx_%s_created_at ON %s(created_at);
                                `, tableName, tableName, tableName, tableName, tableName, tableName, tableName, tableName, tableName, tableName, tableName)

        return db.Exec(sql).Error
}

// GetTrafficTables 获取所有流量记录分表
func GetTrafficTables(db *gorm.DB) ([]string, error) {
        var tables []string
        result := db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'traffic_records_%'").Scan(&tables)
        if result.Error != nil {
                return nil, result.Error
        }
        return tables, nil
}

// GetTrafficTablesMySQL MySQL版本
func GetTrafficTablesMySQL(db *gorm.DB) ([]string, error) {
        var tables []string
        result := db.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name LIKE 'traffic_records_%'").Scan(&tables)
        if result.Error != nil {
                return nil, result.Error
        }
        return tables, nil
}

// GetTrafficTablesPostgres PostgreSQL版本
func GetTrafficTablesPostgres(db *gorm.DB) ([]string, error) {
        var tables []string
        result := db.Raw("SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename LIKE 'traffic_records_%'").Scan(&tables)
        if result.Error != nil {
                return nil, result.Error
        }
        return tables, nil
}
