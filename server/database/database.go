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

// Provider 供应商模型
type Provider struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:100;not null" json:"name"`
	Description string    `gorm:"size:500" json:"description"`
	BaseURL     string    `gorm:"column:base_url;size:500;not null" json:"base_url"`
	APIKey      string    `gorm:"column:api_key;size:500" json:"api_key"`
	Timeout     int       `gorm:"default:30" json:"timeout"`            // 超时秒数
	Retry       int       `gorm:"default:3" json:"retry"`               // 重试次数
	Status      string    `gorm:"size:20;default:active" json:"status"` // active/cooldown/arrears
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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

// TrafficRecord 流量记录（基础表结构，实际存储在按月分表中）
type TrafficRecord struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	APIKeyID    uint      `gorm:"index" json:"api_key_id"`
	UserID      uint      `gorm:"index" json:"user_id"`
	ModelID     uint      `gorm:"index" json:"model_id"`
	ProviderID  uint      `gorm:"index" json:"provider_id"`
	InputBytes  int64     `json:"input_bytes"`
	OutputBytes int64     `json:"output_bytes"`
	StartTime   time.Time `gorm:"index" json:"start_time"`
	EndTime     time.Time `gorm:"index" json:"end_time"`
	Duration    int64     `json:"duration"`              // 毫秒
	Status      string    `gorm:"size:20" json:"status"` // success/error
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
}

// ==================== 3.1 修复：API Key 加密/解密函数 ====================

// 注意：加密密钥的获取逻辑（环境变量 > 配置文件 > 不加密）已在 main.go 中统一实现，
// 密钥通过参数传递给各模块，无需在 database 包中单独获取。

// EncryptAPIKey 使用AES-256-GCM加密API Key，返回带前缀的加密字符串
// 如果encryptionKey为空，则不加密，直接返回原文（向后兼容）
func EncryptAPIKey(plaintext, encryptionKey string) (string, error) {
	if encryptionKey == "" {
		return plaintext, nil
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

// DecryptAPIKey 解密API Key，支持带加密前缀的密文和明文两种格式
// 如果encryptionKey为空或值没有加密前缀，则直接返回原文
func DecryptAPIKey(ciphertext, encryptionKey string) (string, error) {
	// 没有加密前缀，视为明文（向后兼容旧数据）
	if !hasEncryptedPrefix(ciphertext) {
		return ciphertext, nil
	}

	if encryptionKey == "" {
		// 数据已加密但没有密钥，无法解密
		log.Printf("[安全] 警告: API Key已加密但未提供ENCRYPTION_KEY，无法解密")
		return ciphertext, fmt.Errorf("API Key已加密但未提供解密密钥")
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

// hasEncryptedPrefix 检查字符串是否以加密前缀开头
func hasEncryptedPrefix(s string) bool {
	return len(s) > len(EncryptedPrefix) && s[:len(EncryptedPrefix)] == EncryptedPrefix
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
		&Model{},
		&ModelProvider{},
		&User{},
		&APIKey{},
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
				CREATE INDEX IF NOT EXISTS idx_%s_created_at ON %s(created_at);
				`, tableName, tableName, tableName, tableName, tableName, tableName, tableName, tableName, tableName)

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
