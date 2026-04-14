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
        apiKey := extractAPIKey(r)
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
                        ParentModel       string `json:"parent_model"`
                        Format            string `json:"format"`
                        Family            string `json:"family"`
                        Families          []string `json:"families"`
                        ParameterSize     string `json:"parameter_size"`
                        QuantizationLevel string `json:"quantization_level"`
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

// extractAPIKey 从请求中提取API-Key
func extractAPIKey(r *http.Request) string {
        apiKey := r.Header.Get("Authorization")
        if apiKey == "" {
                apiKey = r.URL.Query().Get("key")
        } else {
                apiKey = strings.TrimPrefix(apiKey, "Bearer ")
                apiKey = strings.TrimSpace(apiKey)
        }
        return apiKey
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
