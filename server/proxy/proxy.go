package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"token_factory/cache"
	"token_factory/config"
	"token_factory/traffic"
)

// 常量定义
const (
	MaxRequestBodySize  = 50 * 1024 * 1024 // 2.9 修复：请求体最大50MB
	MaxProviderAttempts = 3                // 最大供应商尝试次数
	DefaultProxyTimeout = 30 * time.Second // 默认代理超时
)

// 4.2 修复：使用全局HTTP连接池复用连接，避免每次请求都创建新的http.Client
// 全局连接池配置：MaxIdleConns=100, MaxIdleConnsPerHost=20, IdleConnTimeout=90s
var proxyTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 20,
	IdleConnTimeout:     90 * time.Second,
	DisableKeepAlives:   false,
}

// proxyClient 全局HTTP客户端，复用连接池
var proxyClient = &http.Client{
	Timeout:   60 * time.Second,
	Transport: proxyTransport,
}

// Server API代理转发服务器
type Server struct {
	cache    *cache.Cache
	recorder *traffic.Recorder
	server   *http.Server
	strategy string // sequential / round-robin / random
	affinity bool   // 会话亲和性
}

// NewServer 创建代理服务器
func NewServer(c *cache.Cache, r *traffic.Recorder, proxyCfg *config.ProxyConfig) *Server {
	strategy := "round-robin"
	affinity := true
	if proxyCfg != nil {
		if proxyCfg.ProviderStrategy != "" {
			strategy = proxyCfg.ProviderStrategy
		}
		affinity = proxyCfg.SessionAffinity
	}
	return &Server{
		cache:    c,
		recorder: r,
		strategy: strategy,
		affinity: affinity,
	}
}

// Start 启动代理服务器
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	// OpenAI 兼容的模型列表接口
	mux.HandleFunc("/v1/models", s.handleModelsList)
	mux.HandleFunc("/v1/models/", s.handleModelsList)

	// Ollama 兼容的标签列表接口
	mux.HandleFunc("/api/tags", s.handleOllamaTags)

	// 其他所有请求走代理转发
	mux.HandleFunc("/", s.handleProxy)

	s.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	log.Printf("[代理] 服务器启动在 %s (策略: %s, 亲和性: %v)", addr, s.strategy, s.affinity)
	return s.server.ListenAndServe()
}

// Shutdown 优雅关闭
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// handleProxy 处理代理请求
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// 1. 验证API-Key
	apiKey := extractAPIKey(r)
	keyInfo := s.cache.GetAPIKeyInfo(apiKey)
	if keyInfo == nil {
		http.Error(w, `{"error":{"message":"Invalid API	key","type":"auth_error"}}`, http.StatusUnauthorized)
		return
	}

	// 2. 解析目标模型
	modelName := s.extractModelName(r)
	if modelName == "" {
		http.Error(w, `{"error":{"message":"Model not specified","type":"request_error"}}`, http.StatusBadRequest)
		return
	}

	model := s.cache.GetModelByName(modelName)
	if model == nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"Model '%s' not found","type":"request_error"}}`, modelName), http.StatusNotFound)
		return
	}

	// 3. 查找可用供应商
	providers := s.cache.GetModelProviders(model.ID)
	if len(providers) == 0 {
		http.Error(w, `{"error":{"message":"No available provider for this model","type":"server_error"}}`, http.StatusServiceUnavailable)
		return
	}

	// 筛选活跃供应商
	var activeProviders []cache.ModelProviderInfo
	for _, p := range providers {
		if p.Status == "active" {
			activeProviders = append(activeProviders, p)
		}
	}
	if len(activeProviders) == 0 {
		http.Error(w, `{"error":{"message":"All providers for this model are unavailable","type":"server_error"}}`, http.StatusServiceUnavailable)
		return
	}

	// 构建亲和性键：userID_modelID
	var affinityKey string
	if s.affinity {
		affinityKey = fmt.Sprintf("%d_%d", keyInfo.UserID, model.ID)
	}

	// 根据策略和亲和性选择供应商顺序
	orderedProviders := s.cache.SelectProviders(model.ID, activeProviders, s.strategy, affinityKey)

	// 4. 尝试转发请求（支持重试到不同供应商）
	var lastErr error
	var usedProvider cache.ModelProviderInfo
	var resp *http.Response
	var inputBytes int64

	// 2.9 修复：添加请求体大小限制，防止OOM攻击
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBodySize))
	if err != nil {
		http.Error(w, `{"error":{"message":"Failed to read request body","type":"request_error"}}`, http.StatusBadRequest)
		return
	}
	inputBytes = int64(len(bodyBytes))
	r.Body.Close()

	for attempt, provider := range orderedProviders {
		if attempt >= MaxProviderAttempts {
			break
		}

		// 构建目标URL
		targetURL := provider.BaseURL + r.URL.Path
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		// 创建新请求
		proxyReq, err := http.NewRequest(r.Method, targetURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			lastErr = err
			continue
		}

		// 复制请求头
		for key, values := range r.Header {
			for _, value := range values {
				// 不转发原始Authorization头中的API-Key
				if strings.EqualFold(key, "Authorization") {
					continue
				}
				proxyReq.Header.Add(key, value)
			}
		}

		// 设置供应商API-Key
		proxyReq.Header.Set("Authorization", "Bearer "+provider.APIKey)
		proxyReq.Header.Set("Content-Type", "application/json")

		// 4.2 修复：使用全局连接池发送请求，复用TCP连接和TLS会话
		// 通过context设置单次请求超时，避免修改全局客户端的Timeout属性
		timeout := time.Duration(provider.Timeout) * time.Second
		if timeout == 0 {
			timeout = DefaultProxyTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		proxyReq = proxyReq.WithContext(ctx)

		resp, err = proxyClient.Do(proxyReq)
		cancel() // 立即释放context资源，不使用defer（在循环中defer会延迟到函数返回）
		if err != nil {
			lastErr = err
			log.Printf("[代理] 请求供应商 %s (模型: %s) 失败: %v", provider.ProviderName, provider.ProviderModelName, err)
			continue
		}

		usedProvider = provider
		break
	}

	if resp == nil {
		errMsg := "All providers failed"
		if lastErr != nil {
			errMsg = lastErr.Error()
		}
		endTime := time.Now()
		s.recorder.Record(traffic.TrafficItem{
			APIKeyID:    keyInfo.KeyID,
			UserID:      keyInfo.UserID,
			ModelID:     model.ID,
			ProviderID:  usedProvider.ProviderID,
			InputBytes:  inputBytes,
			OutputBytes: 0,
			StartTime:   startTime,
			EndTime:     endTime,
			Duration:    endTime.Sub(startTime).Milliseconds(),
			Status:      "error",
		})
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"server_error"}}`, errMsg), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 5. 复制响应头
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 6. 复制响应体并计算输出字节数
	w.WriteHeader(resp.StatusCode)
	outputBytes, err := io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("[代理] 写入响应失败: %v", err)
	}

	// 7. 记录流量
	endTime := time.Now()
	status := "success"
	if resp.StatusCode >= 400 {
		status = "error"
	}

	// 8. 成功时记录会话亲和性
	if s.affinity && status == "success" && affinityKey != "" {
		s.cache.SetAffinity(affinityKey, usedProvider.ProviderID)
	}

	s.recorder.Record(traffic.TrafficItem{
		APIKeyID:    keyInfo.KeyID,
		UserID:      keyInfo.UserID,
		ModelID:     model.ID,
		ProviderID:  usedProvider.ProviderID,
		InputBytes:  inputBytes,
		OutputBytes: outputBytes,
		StartTime:   startTime,
		EndTime:     endTime,
		Duration:    endTime.Sub(startTime).Milliseconds(),
		Status:      status,
	})
}

// ==================== 模型列表接口 ====================

// handleModelsList OpenAI兼容的 /v1/models 接口
// 返回当前系统中所有已配置的模型列表
func (s *Server) handleModelsList(w http.ResponseWriter, r *http.Request) {
	// 验证API-Key
	apiKey := extractAPIKey(r)
	keyInfo := s.cache.GetAPIKeyInfo(apiKey)
	if keyInfo == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid API key","type":"auth_error"}}`))
		return
	}

	models := s.cache.GetModels()

	// OpenAI 格式的模型列表响应
	type ModelObject struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	type ListModelsResponse struct {
		Object string        `json:"object"`
		Data   []ModelObject `json:"data"`
	}

	var data []ModelObject
	for _, m := range models {
		data = append(data, ModelObject{
			ID:      m.Name,
			Object:  "model",
			Created: m.CreatedAt.Unix(),
			OwnedBy: "token-factory",
		})
	}

	resp := ListModelsResponse{
		Object: "list",
		Data:   data,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleOllamaTags Ollama兼容的 /api/tags 接口
// 返回当前系统中所有已配置的模型列表（Ollama格式）
func (s *Server) handleOllamaTags(w http.ResponseWriter, r *http.Request) {
	// 验证API-Key
	apiKey := extractAPIKey(r)
	keyInfo := s.cache.GetAPIKeyInfo(apiKey)
	if keyInfo == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"Invalid API key"}`))
		return
	}

	models := s.cache.GetModels()

	// Ollama 格式的标签列表响应
	type OllamaModel struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		ModifiedAt string `json:"modified_at"`
		Size       int64  `json:"size"`
		Digest     string `json:"digest"`
		Details    struct {
			ParentModel       string   `json:"parent_model"`
			Format            string   `json:"format"`
			Family            string   `json:"family"`
			Families          []string `json:"families"`
			ParameterSize     string   `json:"parameter_size"`
			QuantizationLevel string   `json:"quantization_level"`
		} `json:"details"`
	}

	type TagsResponse struct {
		Models []OllamaModel `json:"models"`
	}

	var ollamaModels []OllamaModel
	for _, m := range models {
		om := OllamaModel{
			Name:       m.Name,
			Model:      m.Name,
			ModifiedAt: m.UpdatedAt.Format(time.RFC3339),
			Size:       0,
			Digest:     fmt.Sprintf("%x", m.ID),
		}
		om.Details.Format = "gguf"
		om.Details.Family = "token-factory"
		ollamaModels = append(ollamaModels, om)
	}

	resp := TagsResponse{
		Models: ollamaModels,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// 2.7 修复：移除URL参数传递API Key的方式，仅支持Authorization头
// extractAPIKey 从请求中提取API-Key
func extractAPIKey(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")
	return strings.TrimSpace(apiKey)
}

// extractModelName 从请求中提取模型名称
func (s *Server) extractModelName(r *http.Request) string {
	path := r.URL.Path

	// OpenAI兼容格式: /v1/chat/completions, /v1/completions, /v1/embeddings
	if strings.Contains(path, "/chat/completions") ||
		strings.Contains(path, "/completions") ||
		strings.Contains(path, "/embeddings") ||
		strings.Contains(path, "/images/generations") {

		// 从请求体中提取model字段
		if r.Method == "POST" && r.Body != nil {
			// 2.9 修复：添加请求体大小限制
			bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBodySize))
			if err == nil {
				r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
				modelName := extractModelFromJSON(bodyBytes)
				if modelName != "" {
					return modelName
				}
			}
		}
	}

	// 尝试从URL参数获取
	if m := r.URL.Query().Get("model"); m != "" {
		return m
	}

	return ""
}

// 2.8 修复：使用encoding/json标准库替代手动JSON解析
// extractModelFromJSON 从JSON体中提取model字段
func extractModelFromJSON(data []byte) string {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	return body.Model
}
