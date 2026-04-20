package cache

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"token_factory/database"

	"gorm.io/gorm"
)

// ProviderAPIKeyInfo 供应商 API Key 信息（缓存中的结构）
type ProviderAPIKeyInfo struct {
	KeyID      uint
	ProviderID uint
	APIKey     string // 解密后的明文密钥，仅用于代理请求
	Name       string // 备注名称
	Status     string // active/cooldown/arrears
}

// 3.3 修复：实现fmt.Stringer接口，确保日志输出时API Key始终脱敏
func (info ProviderAPIKeyInfo) String() string {
	return fmt.Sprintf("ProviderAPIKeyInfo{KeyID:%d, ProviderID:%d, Name:%s, APIKey:%s, Status:%s}",
		info.KeyID, info.ProviderID, info.Name, maskSensitiveKey(info.APIKey), info.Status)
}

// ModelProviderInfo 模型对应的供应商信息
type ModelProviderInfo struct {
	ProviderID        uint
	ProviderAPIKeyID  uint   // 具体使用的 API Key ID
	ProviderName      string // 供应商名称
	ProviderModelName string // 供应商侧模型名
	BaseURL           string
	APIKey            string // 3.3 修复：此字段包含解密后的明文密钥，仅用于代理请求
	Timeout           int    // 总超时秒数
	ConnectTimeout    int    // 连接建立超时秒数（TCP+TLS握手）
	FirstTokenTimeout int    // 首Token返回超时秒数
	StreamIdleTimeout int    // 流传输Idle超时秒数
	Retry             int
	Status            string // API Key 级别的状态
}

// 3.3 修复：实现fmt.Stringer接口，确保日志输出时API Key始终脱敏
func (info ModelProviderInfo) String() string {
	return fmt.Sprintf("ModelProviderInfo{ProviderID:%d, ProviderAPIKeyID:%d, ProviderName:%s, ProviderModelName:%s, BaseURL:%s, APIKey:%s, Timeout:%d, ConnectTimeout:%d, FirstTokenTimeout:%d, StreamIdleTimeout:%d, Retry:%d, Status:%s}",
		info.ProviderID, info.ProviderAPIKeyID, info.ProviderName, info.ProviderModelName, info.BaseURL,
		maskSensitiveKey(info.APIKey), info.Timeout, info.ConnectTimeout, info.FirstTokenTimeout, info.StreamIdleTimeout, info.Retry, info.Status)
}

// 3.3 修复：对敏感密钥进行脱敏，仅显示前4位和后4位
func maskSensitiveKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// APIKeyInfo API密钥信息
type APIKeyInfo struct {
	Key      string
	UserID   uint
	UserName string
	Status   string
	KeyID    uint
}

// Cache 内存缓存，从数据库加载数据后供快速查询
type Cache struct {
	providers       map[uint]*database.Provider
	providerByName  map[string]*database.Provider
	providerAPIKeys map[uint][]ProviderAPIKeyInfo // providerID -> API Keys
	models          map[uint]*database.Model
	modelByName     map[string]*database.Model
	modelProviders  map[uint][]ModelProviderInfo // model_id -> providers
	apiKeys         map[string]*APIKeyInfo       // key string -> info
	mu              sync.RWMutex
	db              *gorm.DB
	encryptionKey   string // 3.1 修复：加密密钥，用于解密数据库中的API Key

	// 供应商选择策略相关
	rrCounters  map[uint]*uint64 // model_id -> 轮询计数器（原子操作）
	rrMu        sync.Mutex       // 保护 rrCounters 的创建
	affinityMap map[string]uint  // "userID_modelID" -> providerID，会话亲和性
	affinityMu  sync.RWMutex     // 保护 affinityMap
}

// NewCache 创建缓存并加载数据
func NewCache(db *gorm.DB, encryptionKey string) *Cache {
	c := &Cache{
		db:              db,
		encryptionKey:   encryptionKey,
		providers:       make(map[uint]*database.Provider),
		providerByName:  make(map[string]*database.Provider),
		providerAPIKeys: make(map[uint][]ProviderAPIKeyInfo),
		models:          make(map[uint]*database.Model),
		modelByName:     make(map[string]*database.Model),
		modelProviders:  make(map[uint][]ModelProviderInfo),
		apiKeys:         make(map[string]*APIKeyInfo),
		rrCounters:      make(map[uint]*uint64),
		affinityMap:     make(map[string]uint),
	}
	if err := c.Reload(); err != nil {
		log.Printf("[缓存] 初始加载失败: %v", err)
	}
	return c
}

// Reload 从数据库重新加载所有缓存数据
func (c *Cache) Reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 加载供应商
	var providers []database.Provider
	if err := c.db.Find(&providers).Error; err != nil {
		return fmt.Errorf("加载供应商失败: %w", err)
	}

	newProviders := make(map[uint]*database.Provider, len(providers))
	newProviderByName := make(map[string]*database.Provider, len(providers))
	for i := range providers {
		p := providers[i]
		newProviders[p.ID] = &p
		newProviderByName[p.Name] = &p
	}

	// 加载供应商 API Keys（解密）
	var providerAPIKeys []database.ProviderAPIKey
	if err := c.db.Find(&providerAPIKeys).Error; err != nil {
		return fmt.Errorf("加载供应商API Keys失败: %w", err)
	}

	newProviderAPIKeys := make(map[uint][]ProviderAPIKeyInfo)
	for _, pak := range providerAPIKeys {
		// 解密 API Key
		decryptedKey, err := database.DecryptAPIKey(pak.APIKey, c.encryptionKey)
		if err != nil {
			log.Printf("[缓存] 警告: 解密供应商(ID=%d)的API Key(ID=%d)失败: %v，将使用原始值", pak.ProviderID, pak.ID, err)
			decryptedKey = pak.APIKey
		}
		info := ProviderAPIKeyInfo{
			KeyID:      pak.ID,
			ProviderID: pak.ProviderID,
			APIKey:     decryptedKey,
			Name:       pak.Name,
			Status:     pak.Status,
		}
		newProviderAPIKeys[pak.ProviderID] = append(newProviderAPIKeys[pak.ProviderID], info)
	}

	// 加载模型
	var models []database.Model
	if err := c.db.Find(&models).Error; err != nil {
		return fmt.Errorf("加载模型失败: %w", err)
	}

	newModels := make(map[uint]*database.Model, len(models))
	newModelByName := make(map[string]*database.Model, len(models))
	for i := range models {
		m := models[i]
		newModels[m.ID] = &m
		newModelByName[m.Name] = &m
	}

	// 加载模型-供应商映射
	var mappings []database.ModelProvider
	if err := c.db.Find(&mappings).Error; err != nil {
		return fmt.Errorf("加载模型-供应商映射失败: %w", err)
	}

	// 构建 modelProviders 映射：同一个 Provider 下的每个 API Key 都生成一条 ModelProviderInfo
	newModelProviders := make(map[uint][]ModelProviderInfo)
	for _, mp := range mappings {
		provider, ok := newProviders[mp.ProviderID]
		if !ok {
			continue
		}

		// 获取该供应商的所有 API Keys
		apiKeys, hasKeys := newProviderAPIKeys[mp.ProviderID]
		if !hasKeys || len(apiKeys) == 0 {
			// 该供应商没有 API Key，跳过
			continue
		}

		// 为每个 API Key 创建一条 ModelProviderInfo
		for _, ak := range apiKeys {
			info := ModelProviderInfo{
				ProviderID:        mp.ProviderID,
				ProviderAPIKeyID:  ak.KeyID,
				ProviderName:      provider.Name,
				ProviderModelName: mp.ProviderModelName,
				BaseURL:           provider.BaseURL,
				APIKey:            ak.APIKey, // 已解密
				Timeout:           provider.Timeout,
				ConnectTimeout:    provider.ConnectTimeout,
				FirstTokenTimeout: provider.FirstTokenTimeout,
				StreamIdleTimeout: provider.StreamIdleTimeout,
				Retry:             provider.Retry,
				Status:            ak.Status, // API Key 级别的状态
			}
			newModelProviders[mp.ModelID] = append(newModelProviders[mp.ModelID], info)
		}
	}

	// 加载API密钥（带用户信息）
	type apiKeyRow struct {
		ID       uint
		UserID   uint
		Key      string
		Status   string
		Username string
	}
	var rows []apiKeyRow
	if err := c.db.Table("api_keys").
		Select("api_keys.id, api_keys.user_id, api_keys.key, api_keys.status, users.username").
		Joins("LEFT JOIN users ON users.id = api_keys.user_id").
		Where("api_keys.status = ?", "active").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("加载API密钥失败: %w", err)
	}

	newAPIKeys := make(map[string]*APIKeyInfo, len(rows))
	for _, row := range rows {
		newAPIKeys[row.Key] = &APIKeyInfo{
			Key:      row.Key,
			UserID:   row.UserID,
			UserName: row.Username,
			Status:   row.Status,
			KeyID:    row.ID,
		}
	}

	// 原子替换
	c.providers = newProviders
	c.providerByName = newProviderByName
	c.providerAPIKeys = newProviderAPIKeys
	c.models = newModels
	c.modelByName = newModelByName
	c.modelProviders = newModelProviders
	c.apiKeys = newAPIKeys

	log.Printf("[缓存] 已加载: %d 供应商, %d 供应商API Keys, %d 模型, %d 映射, %d API密钥",
		len(newProviders), len(providerAPIKeys), len(newModels), len(mappings), len(newAPIKeys))

	return nil
}

// GetProviders 获取所有供应商
func (c *Cache) GetProviders() []database.Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]database.Provider, 0, len(c.providers))
	for _, p := range c.providers {
		result = append(result, *p)
	}
	return result
}

// GetProvider 获取供应商ByID
func (c *Cache) GetProvider(id uint) *database.Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if p, ok := c.providers[id]; ok {
		pCopy := *p
		return &pCopy
	}
	return nil
}

// GetProviderByName 按名称获取供应商
func (c *Cache) GetProviderByName(name string) *database.Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if p, ok := c.providerByName[name]; ok {
		pCopy := *p
		return &pCopy
	}
	return nil
}

// GetProviderAPIKeys 获取供应商的所有 API Keys
func (c *Cache) GetProviderAPIKeys(providerID uint) []ProviderAPIKeyInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys, ok := c.providerAPIKeys[providerID]
	if !ok {
		return nil
	}
	result := make([]ProviderAPIKeyInfo, len(keys))
	copy(result, keys)
	return result
}

// GetModels 获取所有模型
func (c *Cache) GetModels() []database.Model {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]database.Model, 0, len(c.models))
	for _, m := range c.models {
		result = append(result, *m)
	}
	return result
}

// GetModelByID 按ID获取模型
func (c *Cache) GetModelByID(id uint) *database.Model {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if m, ok := c.models[id]; ok {
		mCopy := *m
		return &mCopy
	}
	return nil
}

// GetModelByName 按名称获取模型
func (c *Cache) GetModelByName(name string) *database.Model {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if m, ok := c.modelByName[name]; ok {
		mCopy := *m
		return &mCopy
	}
	return nil
}

// GetModelProviders 获取模型对应的供应商列表
func (c *Cache) GetModelProviders(modelID uint) []ModelProviderInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	infos, ok := c.modelProviders[modelID]
	if !ok {
		return nil
	}
	result := make([]ModelProviderInfo, len(infos))
	copy(result, infos)
	return result
}

// GetAPIKeyInfo 获取API密钥信息
func (c *Cache) GetAPIKeyInfo(key string) *APIKeyInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if info, ok := c.apiKeys[key]; ok {
		infoCopy := *info
		return &infoCopy
	}
	return nil
}

// SelectProviders 根据策略对活跃供应商列表排序后返回
// strategy: sequential / round-robin / random
// affinityKey: "userID_modelID"，用于会话亲和性
func (c *Cache) SelectProviders(modelID uint, activeProviders []ModelProviderInfo, strategy string, affinityKey string) []ModelProviderInfo {
	if len(activeProviders) == 0 {
		return activeProviders
	}

	// 1. 会话亲和性：如果启用且命中，将亲和供应商排到首位
	if affinityKey != "" {
		c.affinityMu.RLock()
		affinityProviderID, hit := c.affinityMap[affinityKey]
		c.affinityMu.RUnlock()

		if hit {
			// 将亲和供应商移到首位，其余保持原序
			var preferred []ModelProviderInfo
			var others []ModelProviderInfo
			for _, p := range activeProviders {
				if p.ProviderID == affinityProviderID {
					preferred = append(preferred, p)
				} else {
					others = append(others, p)
				}
			}
			if len(preferred) > 0 {
				activeProviders = append(preferred, others...)
				return activeProviders
			}
			// 亲和的供应商已不在活跃列表中，清除亲和记录
			c.affinityMu.Lock()
			delete(c.affinityMap, affinityKey)
			c.affinityMu.Unlock()
		}
	}

	// 2. 根据策略排序
	switch strategy {
	case "sequential":
		// 按原序（数据库ID升序），不做什么
		return activeProviders

	case "round-robin":
		// 轮询：从当前计数器位置开始轮转列表
		c.rrMu.Lock()
		counter, ok := c.rrCounters[modelID]
		if !ok {
			counter = new(uint64)
			c.rrCounters[modelID] = counter
		}
		c.rrMu.Unlock()

		idx := int(atomic.AddUint64(counter, 1)) % len(activeProviders)
		// 轮转：[idx, idx+1, ..., n-1, 0, 1, ..., idx-1]
		result := make([]ModelProviderInfo, len(activeProviders))
		for i := 0; i < len(activeProviders); i++ {
			result[i] = activeProviders[(idx+i)%len(activeProviders)]
		}
		return result

	case "random":
		// 随机：Fisher-Yates 洗牌
		result := make([]ModelProviderInfo, len(activeProviders))
		copy(result, activeProviders)
		rand.Shuffle(len(result), func(i, j int) {
			result[i], result[j] = result[j], result[i]
		})
		return result

	default:
		// 未知策略回退到轮询
		return activeProviders
	}
}

// SetAffinity 设置会话亲和性：记录用户+模型对应的供应商
func (c *Cache) SetAffinity(affinityKey string, providerID uint) {
	c.affinityMu.Lock()
	defer c.affinityMu.Unlock()
	c.affinityMap[affinityKey] = providerID
}

// GetEncryptionKey 获取加密密钥（供admin模块使用）
func (c *Cache) GetEncryptionKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.encryptionKey
}

// ==================== 4.3 修复：防抖缓存 ====================

// DebouncedCache 防抖缓存包装器
// 短时间内多次调用 Reload 时，仅执行最后一次，避免频繁全量重载缓存
// 典型场景：批量创建/删除映射时，短时间内多次触发 Reload
type DebouncedCache struct {
	cache *Cache
	timer *time.Timer
	mu    sync.Mutex
	delay time.Duration
}

// NewDebouncedCache 创建防抖缓存包装器
// delay: 防抖延迟时间，在此时间窗口内的多次Reload调用仅执行最后一次
func NewDebouncedCache(cache *Cache, delay time.Duration) *DebouncedCache {
	return &DebouncedCache{
		cache: cache,
		delay: delay,
	}
}

// Reload 防抖重载：取消之前的延迟定时器，重新设置新的定时器
// 只有当距离上次调用超过 delay 时间后，才会真正执行缓存重载
func (d *DebouncedCache) Reload() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, func() {
		if err := d.cache.Reload(); err != nil {
			log.Printf("[缓存] 防抖重载失败: %v", err)
		}
	})
}
