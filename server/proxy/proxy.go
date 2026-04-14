package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"token_factory/cache"
	"token_factory/traffic"
)

// Server API代理转发服务器
type Server struct {
	cache    *cache.Cache
	recorder *traffic.Recorder
	server   *http.Server
}

// NewServer 创建代理服务器
func NewServer(c *cache.Cache, r *traffic.Recorder) *Server {
	return &Server{
		cache:    c,
		recorder: r,
	}
}

// Start 启动代理服务器
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleProxy)

	s.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	log.Printf("[代理] 服务器启动在 %s", addr)
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
	apiKey := r.Header.Get("Authorization")
	if apiKey == "" {
		apiKey = r.URL.Query().Get("key")
	} else {
		// 去掉 "Bearer " 前缀
		apiKey = strings.TrimPrefix(apiKey, "Bearer ")
		apiKey = strings.TrimSpace(apiKey)
	}

	keyInfo := s.cache.GetAPIKeyInfo(apiKey)
	if keyInfo == nil {
		http.Error(w, `{"error":{"message":"Invalid API key","type":"auth_error"}}`, http.StatusUnauthorized)
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

	// 4. 尝试转发请求（支持重试到不同供应商）
	var lastErr error
	var usedProvider cache.ModelProviderInfo
	var resp *http.Response
	var inputBytes int64

	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"Failed to read request body","type":"request_error"}}`, http.StatusBadRequest)
		return
	}
	inputBytes = int64(len(bodyBytes))
	r.Body.Close()

	for attempt, provider := range activeProviders {
		if attempt >= 3 { // 最多尝试3个供应商
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

		// 发送请求（带超时）
		timeout := time.Duration(provider.Timeout) * time.Second
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		client := &http.Client{Timeout: timeout}

		resp, err = client.Do(proxyReq)
		if err != nil {
			lastErr = err
			log.Printf("[代理] 请求供应商 %s 失败: %v", provider.ProviderModelName, err)
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
			// 简单提取 "model":"xxx" 字段
			bodyBytes, err := io.ReadAll(r.Body)
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

// extractModelFromJSON 从JSON体中提取model字段
func extractModelFromJSON(data []byte) string {
	str := string(data)
	// 简单提取 "model":"xxx" 或 "model": "xxx"
	idx := strings.Index(str, `"model"`)
	if idx == -1 {
		return ""
	}

	// 找到冒号
	rest := str[idx+7:]
	colonIdx := strings.Index(rest, ":")
	if colonIdx == -1 || colonIdx > 5 {
		return ""
	}
	rest = rest[colonIdx+1:]

	// 跳过空格
	rest = strings.TrimSpace(rest)

	// 提取引号内的字符串
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	endQuote := strings.Index(rest[1:], `"`)
	if endQuote == -1 {
		return ""
	}

	return rest[1 : endQuote+1]
}
