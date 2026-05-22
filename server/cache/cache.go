package cache

import (
        "context"
        "fmt"
        "log"
        "math/rand"
        "sync"
        "sync/atomic"
        "time"

        "token_factory/config"
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

        // API Key 自动状态管理相关
        activeRequests   map[uint]map[uint64]context.CancelFunc // providerAPIKeyID -> requestID -> cancel 函数
        activeRequestsMu sync.Mutex                             // 保护 activeRequests
        activeRequestID  uint64                                 // 请求ID自增计数器（在 activeRequestsMu 保护下使用）
        failureCounts    map[uint]int                           // providerAPIKeyID -> 连续失败计数
        failureCountsMu  sync.Mutex                             // 保护 failureCounts
        autoStatusCfg    config.AutoStatusConfig                // 自动状态管理配置
        stopRecovery     chan struct{}                          // 通知冷却恢复协程停止
}

// NewCache 创建缓存并加载数据
func NewCache(db *gorm.DB, encryptionKey string, autoStatusCfg config.AutoStatusConfig) *Cache {
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
                activeRequests:  make(map[uint]map[uint64]context.CancelFunc),
                failureCounts:   make(map[uint]int),
                autoStatusCfg:   autoStatusCfg,
                stopRecovery:    make(chan struct{}),
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
                        // 解密失败时检查是否为缺少加密前缀的明文数据（旧版本兼容）
                        // 如果不是明文格式，则跳过该 Key 并记录错误，避免使用密文作为 API Key 导致上游认证失败
                        if !database.HasEncryptedPrefix(pak.APIKey) {
                                // 旧版本明文存储的 API Key，直接使用（兼容升级场景）
                                log.Printf("[缓存] 警告: 供应商(ID=%d)的API Key(ID=%d)为明文存储，建议重新加密: %v", pak.ProviderID, pak.ID, err)
                                decryptedKey = pak.APIKey
                        } else {
                                // 有加密前缀但解密失败，说明加密密钥不匹配或数据损坏
                                // 跳过该 Key，不使用密文替代（密文作为 API Key 会导致上游认证失败且难以排查）
                                log.Printf("[缓存] 错误: 解密供应商(ID=%d)的API Key(ID=%d)失败: %v，已跳过该 Key（请检查 encryption_key 是否正确）", pak.ProviderID, pak.ID, err)
                                continue
                        }
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

// GetAutoStatusConfig 获取自动状态管理配置（供proxy模块使用）
func (c *Cache) GetAutoStatusConfig() config.AutoStatusConfig {
        return c.autoStatusCfg
}

// ==================== API Key 自动状态管理 ====================

// RegisterActiveRequest 注册正在使用指定 API Key 的活跃请求
// 当该 API Key 被禁用时，可通过 CancelActiveRequests 立即中断这些请求
// 返回唯一的请求ID，用于后续 UnregisterActiveRequest 时移除
func (c *Cache) RegisterActiveRequest(providerAPIKeyID uint, cancel context.CancelFunc) uint64 {
        c.activeRequestsMu.Lock()
        defer c.activeRequestsMu.Unlock()
        c.activeRequestID++
        requestID := c.activeRequestID
        if c.activeRequests[providerAPIKeyID] == nil {
                c.activeRequests[providerAPIKeyID] = make(map[uint64]context.CancelFunc)
        }
        c.activeRequests[providerAPIKeyID][requestID] = cancel
        return requestID
}

// UnregisterActiveRequest 移除已完成的请求
// requestID 由 RegisterActiveRequest 返回
func (c *Cache) UnregisterActiveRequest(providerAPIKeyID uint, requestID uint64) {
        c.activeRequestsMu.Lock()
        defer c.activeRequestsMu.Unlock()
        delete(c.activeRequests[providerAPIKeyID], requestID)
        // 清理空map
        if len(c.activeRequests[providerAPIKeyID]) == 0 {
                delete(c.activeRequests, providerAPIKeyID)
        }
}

// CancelActiveRequests 取消使用指定 API Key 的所有活跃请求
// 返回被取消的请求数量
func (c *Cache) CancelActiveRequests(providerAPIKeyID uint) int {
        c.activeRequestsMu.Lock()
        defer c.activeRequestsMu.Unlock()
        cancels := c.activeRequests[providerAPIKeyID]
        count := len(cancels)
        for _, cancel := range cancels {
                cancel()
        }
        delete(c.activeRequests, providerAPIKeyID)
        return count
}

// SetProviderAPIKeyStatus 更新供应商 API Key 的状态（同时更新缓存和数据库）
// 此方法是线程安全的，用于代理请求中自动检测到异常状态时实时更新
// 如果新状态为 disabled，会同时中断所有使用该 Key 的活跃请求
func (c *Cache) SetProviderAPIKeyStatus(providerAPIKeyID uint, newStatus string) error {
        c.mu.Lock()

        // 1. 更新数据库
        if err := c.db.Model(&database.ProviderAPIKey{}).
                Where("id = ?", providerAPIKeyID).
                Update("status", newStatus).Error; err != nil {
                c.mu.Unlock()
                return fmt.Errorf("更新API Key状态到数据库失败: %w", err)
        }

        // 2. 更新内存缓存中的 providerAPIKeys 映射
        for providerID, keys := range c.providerAPIKeys {
                for i := range keys {
                        if keys[i].KeyID == providerAPIKeyID {
                                c.providerAPIKeys[providerID][i].Status = newStatus
                                break
                        }
                }
        }

        // 3. 同步更新 modelProviders 映射中的 Status
        for modelID, providers := range c.modelProviders {
                for i := range providers {
                        if providers[i].ProviderAPIKeyID == providerAPIKeyID {
                                c.modelProviders[modelID][i].Status = newStatus
                                break
                        }
                }
        }

        log.Printf("[缓存] API Key(ID=%d) 状态已自动更新为: %s", providerAPIKeyID, newStatus)

        // 4. 先释放 mu 锁，再执行可能的连接中断（CancelActiveRequests 有自己的锁）
        c.mu.Unlock()

        // 5. 如果新状态为 disabled，中断所有使用该 Key 的活跃请求
        if newStatus == "disabled" {
                count := c.CancelActiveRequests(providerAPIKeyID)
                if count > 0 {
                        log.Printf("[缓存] 已中断 API Key(ID=%d) 上的 %d 个活跃请求（Key 已禁用）", providerAPIKeyID, count)
                }
        }

        return nil
}

// RecordAPIKeyFailure 记录 API Key 请求失败
// 返回当前连续失败次数；如果达到阈值则自动标记为冷却状态并返回 -1
// 如果自动状态管理未启用，直接返回 0
func (c *Cache) RecordAPIKeyFailure(providerAPIKeyID uint) int {
        if !c.autoStatusCfg.Enabled {
                return 0
        }

        c.failureCountsMu.Lock()
        c.failureCounts[providerAPIKeyID]++
        count := c.failureCounts[providerAPIKeyID]

        if count >= c.autoStatusCfg.ConsecutiveFailures {
                // 达到连续失败阈值，重置计数器并标记为冷却
                log.Printf("[缓存] API Key(ID=%d) 连续失败 %d 次（阈值=%d），自动标记为冷却状态",
                        providerAPIKeyID, count, c.autoStatusCfg.ConsecutiveFailures)
                delete(c.failureCounts, providerAPIKeyID)
                c.failureCountsMu.Unlock()
                // 在锁外执行状态更新（SetProviderAPIKeyStatus 有自己的锁）
                if err := c.SetProviderAPIKeyStatus(providerAPIKeyID, "cooldown"); err != nil {
                        log.Printf("[缓存] 自动标记冷却状态失败: %v", err)
                }
                return -1
        }

        c.failureCountsMu.Unlock()
        return count
}

// ResetAPIKeyFailure 重置 API Key 的连续失败计数
// 当请求成功时调用，避免历史偶发失败累积导致误判
func (c *Cache) ResetAPIKeyFailure(providerAPIKeyID uint) {
        c.failureCountsMu.Lock()
        defer c.failureCountsMu.Unlock()
        delete(c.failureCounts, providerAPIKeyID)
}

// StartCooldownRecovery 启动冷却状态自动恢复协程
// 每隔 checkInterval 检查一次，对冷却时间超过 minCooldownDuration 的 Key 自动恢复为 active
func (c *Cache) StartCooldownRecovery() {
        if !c.autoStatusCfg.Enabled {
                log.Printf("[缓存] 自动状态管理未启用，跳过冷却恢复协程启动")
                return
        }

        checkInterval := time.Duration(c.autoStatusCfg.CooldownCheckInterval) * time.Second
        minCooldown := time.Duration(c.autoStatusCfg.CooldownRecoverySec) * time.Second

        go func() {
                log.Printf("[缓存] 冷却恢复协程已启动 (检查间隔=%v, 最短冷却=%v)", checkInterval, minCooldown)
                ticker := time.NewTicker(checkInterval)
                defer ticker.Stop()

                for {
                        select {
                        case <-ticker.C:
                                c.recoverCooldownKeys(minCooldown)
                        case <-c.stopRecovery:
                                log.Printf("[缓存] 冷却恢复协程已停止")
                                return
                        }
                }
        }()
}

// StopCooldownRecovery 停止冷却恢复协程
func (c *Cache) StopCooldownRecovery() {
        close(c.stopRecovery)
}

// recoverCooldownKeys 扫描所有冷却中的 API Key，将冷却时间超过阈值的恢复为 active
func (c *Cache) recoverCooldownKeys(minCooldown time.Duration) {
        c.mu.RLock()

        // 收集所有冷却中的 Key ID
        var cooldownKeyIDs []uint
        for _, keys := range c.providerAPIKeys {
                for _, ak := range keys {
                        if ak.Status == "cooldown" {
                                cooldownKeyIDs = append(cooldownKeyIDs, ak.KeyID)
                        }
                }
        }
        c.mu.RUnlock()

        if len(cooldownKeyIDs) == 0 {
                return
        }

        now := time.Now()
        for _, keyID := range cooldownKeyIDs {
                // 从数据库读取 updated_at 判断冷却持续时间
                var dbKey database.ProviderAPIKey
                if err := c.db.Select("id, updated_at, status").First(&dbKey, keyID).Error; err != nil {
                        continue
                }
                // 再次确认状态（可能在读数据库期间已被手动修改）
                if dbKey.Status != "cooldown" {
                        continue
                }
                if now.Sub(dbKey.UpdatedAt) >= minCooldown {
                        if err := c.SetProviderAPIKeyStatus(keyID, "active"); err != nil {
                                log.Printf("[缓存] 冷却恢复失败(API Key ID=%d): %v", keyID, err)
                        } else {
                                log.Printf("[缓存] API Key(ID=%d) 冷却超时(%v)，自动恢复为 active", keyID, minCooldown)
                        }
                }
        }
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
