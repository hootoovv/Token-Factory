package cache

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"

	"token_factory/database"

	"gorm.io/gorm"
)

// ModelProviderInfo 模型对应的供应商信息
type ModelProviderInfo struct {
	ProviderID        uint
	ProviderName      string // 供应商名称
	ProviderModelName string // 供应商侧模型名
	BaseURL           string
	APIKey            string
	Timeout           int
	Retry             int
	Status            string
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
	providers      map[uint]*database.Provider
	providerByName map[string]*database.Provider
	models         map[uint]*database.Model
	modelByName    map[string]*database.Model
	modelProviders map[uint][]ModelProviderInfo // model_id -> providers
	apiKeys        map[string]*APIKeyInfo       // key string -> info
	mu             sync.RWMutex
	db             *gorm.DB

	// 供应商选择策略相关
	rrCounters  map[uint]*uint64 // model_id -> 轮询计数器（原子操作）
	rrMu        sync.Mutex       // 保护 rrCounters 的创建
	affinityMap map[string]uint  // "userID_modelID" -> providerID，会话亲和性
	affinityMu  sync.RWMutex     // 保护 affinityMap
}

// NewCache 创建缓存并加载数据
func NewCache(db *gorm.DB) *Cache {
	c := &Cache{
		db:             db,
		providers:      make(map[uint]*database.Provider),
		providerByName: make(map[string]*database.Provider),
		models:         make(map[uint]*database.Model),
		modelByName:    make(map[string]*database.Model),
		modelProviders: make(map[uint][]ModelProviderInfo),
		apiKeys:        make(map[string]*APIKeyInfo),
		rrCounters:     make(map[uint]*uint64),
		affinityMap:    make(map[string]uint),
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

	newModelProviders := make(map[uint][]ModelProviderInfo)
	for _, mp := range mappings {
		provider, ok := newProviders[mp.ProviderID]
		if !ok {
			continue
		}
		info := ModelProviderInfo{
			ProviderID:        mp.ProviderID,
			ProviderName:      provider.Name,
			ProviderModelName: mp.ProviderModelName,
			BaseURL:           provider.BaseURL,
			APIKey:            provider.APIKey,
			Timeout:           provider.Timeout,
			Retry:             provider.Retry,
			Status:            provider.Status,
		}
		newModelProviders[mp.ModelID] = append(newModelProviders[mp.ModelID], info)
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
	c.models = newModels
	c.modelByName = newModelByName
	c.modelProviders = newModelProviders
	c.apiKeys = newAPIKeys

	log.Printf("[缓存] 已加载: %d 供应商, %d 模型, %d 映射, %d API密钥",
		len(newProviders), len(newModels), len(mappings), len(newAPIKeys))

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
