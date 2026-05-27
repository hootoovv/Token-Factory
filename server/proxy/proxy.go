package proxy

import (
        "bytes"
        "context"
        "encoding/json"
        "errors"
        "fmt"
        "io"
        "log"
        "net"
        "net/http"
        "regexp"
        "strings"
        "sync"
        "time"

        "token_factory/cache"
        "token_factory/callrecords"
        "token_factory/config"
        "token_factory/traffic"

        "golang.org/x/net/http2"
)

// versionPathSuffix 检测URL是否以版本路径结尾（如 /v1, /v2, /v4 等）
// 供应商的BaseURL通常包含完整的API基础路径含版本号，例如：
//   - https://integrate.api.nvidia.com/v1
//   - https://open.bigmodel.cn/api/paas/v4
//   - https://api.openai.com/v1
var versionPathSuffix = regexp.MustCompile(`/v\d+$`)

// replaceModelInBody 将请求体中的model字段替换为供应商侧的模型名
// 客户端使用统一模型名（如kimi-k2.5），但不同供应商需要不同的模型名（如moonshotai/kimi-k2.5）
func replaceModelInBody(body []byte, newModel string) []byte {
        var reqBody map[string]interface{}
        if err := json.Unmarshal(body, &reqBody); err != nil {
                // JSON解析失败，返回原始body
                return body
        }
        reqBody["model"] = newModel
        newBody, err := json.Marshal(reqBody)
        if err != nil {
                // 序列化失败，返回原始body
                return body
        }
        return newBody
}

// isStreamRequest 检测请求体中是否包含 "stream": true
// 用于判断是否需要为上游请求设置 Accept: text/event-stream 头
// 某些供应商（如NVIDIA API）要求显式设置此头才返回SSE流
func isStreamRequest(body []byte) bool {
        var reqBody map[string]interface{}
        if err := json.Unmarshal(body, &reqBody); err != nil {
                return false
        }
        streamVal, ok := reqBody["stream"]
        if !ok {
                return false
        }
        // JSON 的 true 解析为 bool，但也要兼容某些客户端发送字符串 "true"
        switch v := streamVal.(type) {
        case bool:
                return v
        case string:
                return strings.EqualFold(v, "true")
        default:
                return false
        }
}

// 常量定义
const (
        MaxRequestBodySize  = 50 * 1024 * 1024 // 2.9 修复：请求体最大50MB
        MaxProviderAttempts = 3                // 最大供应商尝试次数

        // 四阶段超时默认值（秒）
        DefaultTotalTimeout      = 300 // 总超时：从请求发送到响应完成的绝对最大时间
        DefaultConnectTimeout    = 10  // 连接建立超时：TCP+TLS握手完成的最大时间
        DefaultFirstTokenTimeout = 30  // 首Token返回超时：从请求发送完毕到收到第一个响应字节的时间
        DefaultStreamIdleTimeout = 15  // 流传输Idle超时：流式响应中两次数据传输之间的最大空闲时间
)

// 4.2 修复：使用全局HTTP连接池复用连接，避免每次请求都创建新的http.Client
// 全局连接池配置：MaxIdleConns=100, MaxIdleConnsPerHost=20, IdleConnTimeout=90s
// 注意：不在此处设置 DialContext 超时和 ResponseHeaderTimeout，
// 改为每个请求通过 context 和 per-request Transport 动态控制
var proxyTransport = &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 20,
        IdleConnTimeout:     90 * time.Second,
        DisableKeepAlives:   false,
        ForceAttemptHTTP2:   true, // 显式启用HTTP/2协商，确保HTTPS上游连接正确使用HTTP/2
}

func init() {
        // 显式配置HTTP/2支持：注册TLSNextProto["h2"]处理器
        // 当自定义DialContext时，Go的自动HTTP/2配置可能失效，
        // 导致客户端以HTTP/1.1协商但服务器返回HTTP/2帧，引发"malformed HTTP response"错误
        if err := http2.ConfigureTransport(proxyTransport); err != nil {
                log.Printf("[代理] 配置HTTP/2传输层失败: %v，将仅支持HTTP/1.1", err)
        }
}

// proxyClient 全局HTTP客户端，复用连接池
// 注意：不设置 Timeout 字段，所有超时通过 per-request context 精确控制
// 避免 Client.Timeout 与 context.WithTimeout 冲突
var proxyClient = &http.Client{
        Transport: proxyTransport,
}

// Server API代理转发服务器
type Server struct {
        cache           *cache.Cache
        recorder        *traffic.Recorder
        callRecords     *callrecords.Store        // API调用记录存储（内存环形缓冲）
        server          *http.Server
        strategy        string                    // sequential / round-robin / random
        affinity        bool                      // 会话亲和性
        defaultTimeouts config.ProxyTimeoutConfig // 供应商默认超时配置
}

// NewServer 创建代理服务器
func NewServer(c *cache.Cache, r *traffic.Recorder, cr *callrecords.Store, proxyCfg *config.ProxyConfig) *Server {
        strategy := "round-robin"
        affinity := true
        defaultTimeouts := config.ProxyTimeoutConfig{
                Total:      DefaultTotalTimeout,
                Connect:    DefaultConnectTimeout,
                FirstToken: DefaultFirstTokenTimeout,
                StreamIdle: DefaultStreamIdleTimeout,
        }
        if proxyCfg != nil {
                if proxyCfg.ProviderStrategy != "" {
                        strategy = proxyCfg.ProviderStrategy
                }
                affinity = proxyCfg.SessionAffinity
                if proxyCfg.DefaultTimeouts.Total > 0 {
                        defaultTimeouts = proxyCfg.DefaultTimeouts
                }
        }
        return &Server{
                cache:           c,
                recorder:        r,
                callRecords:     cr,
                strategy:        strategy,
                affinity:        affinity,
                defaultTimeouts: defaultTimeouts,
        }
}

// Start 启动代理服务器
func (s *Server) Start(addr string) error {
        mux := http.NewServeMux()

        // OpenAI 兼容的模型列表接口
        mux.HandleFunc("/models", s.handleModelsList)
        mux.HandleFunc("/models/", s.handleModelsList)

        // Ollama 兼容的标签列表接口
        mux.HandleFunc("/api/tags", s.handleOllamaTags)

        // 其他所有请求走代理转发
        mux.HandleFunc("/", s.handleProxy)

        s.server = &http.Server{
                Addr:    addr,
                Handler: mux,
        }

        log.Printf("[代理] 服务器启动在 %s (策略: %s, 亲和性: %v, 默认超时: 总=%ds/连接=%ds/首Token=%ds/流Idle=%ds)",
                addr, s.strategy, s.affinity,
                s.defaultTimeouts.Total, s.defaultTimeouts.Connect,
                s.defaultTimeouts.FirstToken, s.defaultTimeouts.StreamIdle)
        return s.server.ListenAndServe()
}

// Shutdown 优雅关闭
func (s *Server) Shutdown(ctx context.Context) error {
        if s.server != nil {
                return s.server.Shutdown(ctx)
        }
        return nil
}

// ==================== 超时辅助函数 ====================

// providerTimeouts 供应商的四阶段超时配置（转换为 time.Duration）
type providerTimeouts struct {
        Total      time.Duration
        Connect    time.Duration
        FirstToken time.Duration
        StreamIdle time.Duration
}

// getProviderTimeouts 获取供应商的超时配置，未配置的字段使用全局默认值回退
func (s *Server) getProviderTimeouts(p cache.ModelProviderInfo) providerTimeouts {
        t := p.Timeout
        if t == 0 {
                t = s.defaultTimeouts.Total
        }
        if t == 0 {
                t = DefaultTotalTimeout
        }

        c := p.ConnectTimeout
        if c == 0 {
                c = s.defaultTimeouts.Connect
        }
        if c == 0 {
                c = DefaultConnectTimeout
        }

        ft := p.FirstTokenTimeout
        if ft == 0 {
                ft = s.defaultTimeouts.FirstToken
        }
        if ft == 0 {
                ft = DefaultFirstTokenTimeout
        }

        si := p.StreamIdleTimeout
        if si == 0 {
                si = s.defaultTimeouts.StreamIdle
        }
        if si == 0 {
                si = DefaultStreamIdleTimeout
        }

        return providerTimeouts{
                Total:      time.Duration(t) * time.Second,
                Connect:    time.Duration(c) * time.Second,
                FirstToken: time.Duration(ft) * time.Second,
                StreamIdle: time.Duration(si) * time.Second,
        }
}

// createPerRequestClient 创建带连接超时的 per-request HTTP 客户端
// 基于 proxyTransport 克隆，仅覆盖 DialContext 以注入连接建立超时
func createPerRequestClient(connectTimeout time.Duration) *http.Client {
        transport := proxyTransport.Clone()
        transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
                dialer := &net.Dialer{
                        Timeout:   connectTimeout,
                        KeepAlive: 30 * time.Second,
                }
                conn, err := dialer.DialContext(ctx, network, addr)
                if err != nil {
                        return nil, err
                }
                return conn, nil
        }

        // 克隆Transport后重新配置HTTP/2：覆盖DialContext可能导致TLSNextProto失效
        // 必须重新调用http2.ConfigureTransport确保"h2"协议处理器正确注册
        if err := http2.ConfigureTransport(transport); err != nil {
                log.Printf("[代理] 配置per-request HTTP/2传输层失败: %v", err)
        }

        return &http.Client{
                Transport: transport,
                // 不设置 Timeout，所有超时由 context 控制
        }
}

// readFirstByteWithTimeout 带首Token超时的首字节读取
// 返回读取到的第一个字节；如果超时则返回 errFirstTokenTimeout
var errFirstTokenTimeout = errors.New("首Token返回超时")

func readFirstByteWithTimeout(body io.Reader, firstTokenTimeout time.Duration, totalCtx context.Context) (byte, error) {
        type readResult struct {
                b   byte
                err error
        }

        resultCh := make(chan readResult, 1)

        go func() {
                buf := make([]byte, 1)
                _, err := body.Read(buf)
                if err != nil {
                        resultCh <- readResult{0, err}
                        return
                }
                resultCh <- readResult{buf[0], nil}
        }()

        // 首Token超时取 firstTokenTimeout 和 totalCtx 剩余时间中较短的一个
        firstTokenCtx, firstTokenCancel := context.WithTimeout(totalCtx, firstTokenTimeout)
        defer firstTokenCancel()

        select {
        case result := <-resultCh:
                if result.err != nil {
                        return 0, result.err
                }
                return result.b, nil
        case <-firstTokenCtx.Done():
                // 首Token超时或总超时到期
                if totalCtx.Err() != nil {
                        // 总超时到期
                        return 0, totalCtx.Err()
                }
                return 0, errFirstTokenTimeout
        }
}

// streamingCopy 带流传输Idle超时的流式拷贝
// 在流式响应（如SSE）中，如果两次数据传输之间的空闲时间超过 idleTimeout，则返回错误
// 同时监控总超时 context，确保绝对时间不超过限制
// providerName 用于debug日志标识
// outputBuf 如果非nil，会同时将传输的数据写入该buffer用于调用记录采集
func streamingCopy(dst http.ResponseWriter, src io.ReadCloser, firstByte byte, idleTimeout time.Duration, totalCtx context.Context, providerName string, outputBuf *bytes.Buffer) (int64, error) {
        var totalWritten int64
        buf := make([]byte, 32*1024)
        chunkCount := 0
        // copyStart := time.Now()

        // 先写入已读取的首字节
        firstByteData := []byte{firstByte}
        n, err := dst.Write(firstByteData)
        totalWritten += int64(n)
        if err != nil {
                // log.Printf("[DEBUG] [%s] 写入首字节失败: %v (耗时: %v)", providerName, err, time.Since(copyStart))
                return totalWritten, fmt.Errorf("写入首字节失败: %w", err)
        }
        // 同时写入采集buffer
        if outputBuf != nil {
                outputBuf.Write(firstByteData)
        }
        // 立即 flush，确保客户端尽快收到首字节
        if f, ok := dst.(http.Flusher); ok {
                f.Flush()
        }
        // log.Printf("[DEBUG] [%s] 首字节已写入并flush (0x%02x, 总已写: %d字节)", providerName, firstByte, totalWritten)

        // 用于确保 Read goroutine 退出
        var readWg sync.WaitGroup
        defer readWg.Wait()

        for {
                chunkCount++
                // readStart := time.Now()

                type readResult struct {
                        n   int
                        err error
                }

                resultCh := make(chan readResult, 1)
                readWg.Add(1)

                go func() {
                        defer readWg.Done()
                        nr, er := src.Read(buf)
                        resultCh <- readResult{nr, er}
                }()

                select {
                case result := <-resultCh:
                        // readDuration := time.Since(readStart)
                        if result.err != nil {
                                if result.err == io.EOF {
                                        // 正常结束
                                        // log.Printf("[DEBUG] [%s] 流传输正常结束(EOF), 共%d个chunk, 总写入:%d字节, 耗时:%v",
                                        //                               providerName, chunkCount, totalWritten, time.Since(copyStart))
                                        return totalWritten, nil
                                }
                                // log.Printf("[DEBUG] [%s] 流读取错误(第%d个chunk, 已写:%d字节, 读耗时:%v): %v",
                                //                               providerName, chunkCount, totalWritten, readDuration, result.err)
                                return totalWritten, fmt.Errorf("读取响应体失败: %w", result.err)
                        }

                        if result.n > 0 {
                                // writeStart := time.Now()
                                nw, ew := dst.Write(buf[:result.n])
                                totalWritten += int64(nw)
                                if ew != nil {
                                        // log.Printf("[DEBUG] [%s] 写入客户端失败(第%d个chunk): %v", providerName, chunkCount, ew)
                                        return totalWritten, fmt.Errorf("写入响应失败: %w", ew)
                                }
                                // 同时写入采集buffer
                                if outputBuf != nil {
                                        outputBuf.Write(buf[:result.n])
                                }
                                // 每次写入后 flush，确保流式数据及时推送到客户端
                                if f, ok := dst.(http.Flusher); ok {
                                        f.Flush()
                                }
                                // log.Printf("[DEBUG] [%s] chunk#%d: 读取%d字节(耗时%v)→写入%d字节→flush(总:%d字节, 流耗时:%v)",
                                //                               providerName, chunkCount, result.n, readDuration, nw, totalWritten, time.Since(copyStart))
                        } else {
                                // log.Printf("[DEBUG] [%s] chunk#%d: Read返回0字节且无error(耗时%v, 总:%d字节)",
                                //                               providerName, chunkCount, readDuration, totalWritten)
                        }

                case <-time.After(idleTimeout):
                        // 流传输Idle超时：超过 idleTimeout 未收到任何数据
                        // log.Printf("[DEBUG] [%s] 流传输空闲超时触发(第%d次等待, idleTimeout=%v, 等待耗时:%v, 总已写:%d字节, 流耗时:%v)",
                        //                               providerName, chunkCount, idleTimeout, time.Since(readStart), totalWritten, time.Since(copyStart))
                        return totalWritten, fmt.Errorf("流传输空闲超时 (%v)，上游可能已停止生成", idleTimeout)

                case <-totalCtx.Done():
                        // 总超时到期
                        // log.Printf("[DEBUG] [%s] 总超时context取消(第%d次等待, 等待耗时:%v, 总已写:%d字节, 流耗时:%v, ctxErr:%v)",
                        //                               providerName, chunkCount, time.Since(readStart), totalWritten, time.Since(copyStart), totalCtx.Err())
                        return totalWritten, fmt.Errorf("总超时到期: %w", totalCtx.Err())
                }
        }
}

// bufferedCopy 非流式响应的完整体拷贝
// 一次性读取上游完整响应体，然后写入客户端
// 适用于非流式请求（stream 未设置或 stream:false），避免 streamingCopy 的以下问题：
//   1. 流Idle超时（默认15秒）可能在非流式响应的TCP段间触发，导致响应被截断
//   2. 逐块flush与上游Content-Length头冲突，导致客户端解析失败
//   3. 对非流式响应做逐字节flush没有意义（客户端需要完整JSON才能解析）
// 返回值：写入字节数、响应体数据（用于调用记录采集）、错误
func bufferedCopy(dst http.ResponseWriter, src io.ReadCloser, totalCtx context.Context, providerName string) (int64, []byte, error) {
        // 带超时的完整读取
        type readResult struct {
                data []byte
                err  error
        }

        resultCh := make(chan readResult, 1)
        go func() {
                // LimitReader 防止异常大响应导致OOM（上限50MB，与请求体限制一致）
                data, err := io.ReadAll(io.LimitReader(src, MaxRequestBodySize))
                resultCh <- readResult{data, err}
        }()

        select {
        case result := <-resultCh:
                if result.err != nil {
                        return 0, nil, fmt.Errorf("读取响应体失败: %w", result.err)
                }
                n, err := dst.Write(result.data)
                return int64(n), result.data, err
        case <-totalCtx.Done():
                return 0, nil, fmt.Errorf("总超时到期: %w", totalCtx.Err())
        }
}

// ==================== 错误状态分类 ====================

// classifyErrorStatus 根据上游供应商的HTTP状态码和响应体内容，判断应该将 API Key 设置为什么状态
// 返回值：应该设置的新状态（空字符串表示无需更改状态）
// 欠费状态（arrears）不参与自动恢复，只能管理员手动恢复
// 冷却状态（cooldown）可由后台协程自动恢复
func classifyErrorStatus(statusCode int, errorBody string) string {
        // ---- 欠费状态检测 ----
        // HTTP 402 Payment Required → 明确的欠费信号
        if statusCode == 402 {
                return "arrears"
        }

        // HTTP 429 Too Many Requests → 可能是配额耗尽或欠费
        // 需要检查响应体中是否包含欠费/续费相关关键词
        if statusCode == 429 {
                bodyLower := strings.ToLower(errorBody)
                // 英文欠费关键词
                arrearsKeywordsEN := []string{
                        "arrears", "payment", "billing", "overdue",
                        "subscription", "expired", "insufficient quota",
                        "quota exceeded", "plan expired", "renew",
                        "recharge", "top up", "balance",
                }
                // 中文欠费关键词
                arrearsKeywordsCN := []string{
                        "欠费", "续费", "充值", "余额不足", "账户过期",
                        "套餐过期", "配额耗尽", "账单", "到期",
                }
                for _, kw := range arrearsKeywordsEN {
                        if strings.Contains(bodyLower, kw) {
                                return "arrears"
                        }
                }
                for _, kw := range arrearsKeywordsCN {
                        if strings.Contains(bodyLower, kw) {
                                return "arrears"
                        }
                }
                // 429 但不含欠费关键词 → 冷却（可能是速率限制）
                return "cooldown"
        }

        // ---- 冷冻/冷却状态检测 ----
        // 502 Bad Gateway / 504 Gateway Timeout / 503 Service Unavailable
        // 这些通常表示供应商服务不可用，应设为冷却状态
        if statusCode == 502 || statusCode == 503 || statusCode == 504 {
                return "cooldown"
        }

        // ---- 401/403 → 可能是 API Key 失效 ----
        if statusCode == 401 || statusCode == 403 {
                bodyLower := strings.ToLower(errorBody)
                // 检查是否包含Key失效的关键词
                invalidKeywords := []string{
                        "invalid api key", "invalid key", "key not found",
                        "key revoked", "key disabled", "key expired",
                        "api key 无效", "密钥已失效", "密钥已撤销", "密钥过期",
                }
                for _, kw := range invalidKeywords {
                        if strings.Contains(bodyLower, kw) {
                                return "cooldown"
                        }
                }
        }

        // 其他4xx/5xx错误 → 不自动修改状态，保持当前状态
        return ""
}

// ==================== 代理请求处理 ====================

// handleProxy 处理代理请求
// 实现四阶段超时控制：连接建立超时 → 首Token返回超时 → 流传输Idle超时 → 总超时
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
        var inputBytes int64
        var outputBytes int64
        var resp *http.Response
        var streamCopyErr error
        var capturedOutput string // 采集的完整输出数据（用于调用记录）
        proxyStatus := "error"

        // 2.9 修复：添加请求体大小限制，防止OOM攻击
        bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBodySize))
        if err != nil {
                http.Error(w, `{"error":{"message":"Failed to read request body","type":"request_error"}}`, http.StatusBadRequest)
                return
        }
        inputBytes = int64(len(bodyBytes))
        r.Body.Close()

        // ---- 阶段A：连接 + 首Token（可重试阶段） ----
        // 在此阶段如果失败，可以重试到其他供应商
        for attempt, provider := range orderedProviders {
                if attempt >= MaxProviderAttempts {
                        break
                }

                usedProvider = provider
                timeouts := s.getProviderTimeouts(provider)

                // 构建目标URL
                // BaseURL约定：包含完整的API基础路径含版本号，例如：
                //       - https://integrate.api.nvidia.com/v1
                //       - https://open.bigmodel.cn/api/paas/v4
                //       - https://api.openai.com/v1
                //       - http://localhost:11434/v1 (Ollama)
                //
                // 客户端发到代理的请求路径统一为 /v1/chat/completions 等格式
                // 如果BaseURL已包含版本路径（/v1, /v2, /v4等），需要剥离请求路径中的/v1前缀
                // 避免产生 /v4/v1/chat/completions 这样的重复路径
                //
                // 示例：
                //       BaseURL=https://open.bigmodel.cn/api/paas/v4 + /v1/chat/completions → /api/paas/v4/chat/completions
                //       BaseURL=https://integrate.api.nvidia.com/v1 + /v1/chat/completions → /v1/chat/completions
                //       BaseURL=https://my-server.com + /v1/chat/completions → /v1/chat/completions (无版本路径，保留/v1)
                baseURL := strings.TrimRight(provider.BaseURL, "/")
                requestPath := r.URL.Path
                if versionPathSuffix.MatchString(baseURL) && strings.HasPrefix(requestPath, "/v1/") {
                        requestPath = strings.TrimPrefix(requestPath, "/v1")
                }
                targetURL := baseURL + requestPath
                if r.URL.RawQuery != "" {
                        targetURL += "?" + r.URL.RawQuery
                }

                // 将请求体中的模型名替换为供应商侧的模型名
                // 客户端发送统一模型名（如 kimi-k2.5），但不同供应商需要不同的模型名
                // 例如 NVIDIA API 需要 moonshotai/kimi-k2.5，而其他供应商可能只需要 kimi-k2.5
                upstreamBody := replaceModelInBody(bodyBytes, provider.ProviderModelName)
                // log.Printf("[DEBUG] [%s] 模型名替换: 客户端模型=%s → 供应商模型=%s, 请求体大小: %d→%d字节",
                //                               provider.ProviderName, modelName, provider.ProviderModelName, len(bodyBytes), len(upstreamBody))

                // 创建新请求
                proxyReq, err := http.NewRequest(r.Method, targetURL, strings.NewReader(string(upstreamBody)))
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

                // 为流式请求设置Accept头
                // 某些供应商（如NVIDIA API）要求显式设置 Accept: text/event-stream 才返回SSE流
                // 仅当请求体中包含 "stream": true 时才设置，避免影响非流式请求
                if isStreamRequest(bodyBytes) {
                        proxyReq.Header.Set("Accept", "text/event-stream")
                }

                // 创建总超时 context（绝对截止时间）
                totalCtx, totalCancel := context.WithTimeout(r.Context(), timeouts.Total)
                proxyReq = proxyReq.WithContext(totalCtx)

                // 注册活跃请求，以便该 Key 被禁用时能立即中断
                activeRequestID := s.cache.RegisterActiveRequest(provider.ProviderAPIKeyID, totalCancel)

                // 创建带连接超时的 per-request 客户端
                perReqClient := createPerRequestClient(timeouts.Connect)

                // ---- 阶段1：发送请求 + 连接建立超时 ----
                // 连接超时由 DialContext.Timeout 控制，如果连接无法建立会快速返回错误
                // log.Printf("[DEBUG] [%s] 开始发送请求到 %s (超时: 连接=%v, 首Token=%v, 流Idle=%v, 总=%v)",
                //                               provider.ProviderName, targetURL, timeouts.Connect, timeouts.FirstToken, timeouts.StreamIdle, timeouts.Total)
                // log.Printf("[DEBUG] [%s] 请求详情: Method=%s, 模型=%s→%s, 请求头: Authorization=Bearer %s, Accept=%s, Content-Type=%s",
                //                               provider.ProviderName, r.Method, modelName, provider.ProviderModelName,
                //                               maskAPIKey(provider.APIKey), proxyReq.Header.Get("Accept"), proxyReq.Header.Get("Content-Type"))
                reqStart := time.Now()
                resp, err = perReqClient.Do(proxyReq)
                reqDuration := time.Since(reqStart)
                if err != nil {
                        totalCancel()
                        s.cache.UnregisterActiveRequest(provider.ProviderAPIKeyID, activeRequestID)
                        lastErr = fmt.Errorf("连接供应商失败: %w", err)
                        // 增强错误日志：检测HTTP/2握手失败等常见协议协商问题
                        errStr := err.Error()
                        if strings.Contains(errStr, "http2_handshake_failed") || strings.Contains(errStr, "malformed HTTP response") {
                                log.Printf("[代理] 请求供应商 %s (模型: %s) HTTP/2协议协商失败（供应商可能要求HTTP/2）: %v (耗时:%v)", provider.ProviderName, provider.ProviderModelName, err, reqDuration)
                        } else {
                                log.Printf("[代理] 请求供应商 %s (模型: %s) 连接失败: %v (耗时:%v)", provider.ProviderName, provider.ProviderModelName, err, reqDuration)
                        }
                        continue
                }
                // log.Printf("[DEBUG] [%s] 请求已发送, 收到响应头(HTTP %d), 耗时:%v", provider.ProviderName, resp.StatusCode, reqDuration)

                // ---- 阶段1.5：上游HTTP错误检查 ----
                // 如果上游返回4xx/5xx状态码，视为供应商错误，不进入流式传输阶段
                // 这样可以重试下一个供应商，而不是将错误响应转发给客户端
                if resp.StatusCode >= 400 {
                        // 保存状态码和Content-Type，在关闭resp后仍可用于日志
                        errStatusCode := resp.StatusCode
                        errContentType := resp.Header.Get("Content-Type")

                        // 读取错误响应体（限制4KB用于日志和返回）
                        errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
                        resp.Body.Close()
                        resp = nil // 重置resp，确保"所有供应商失败"的判断逻辑正确
                        totalCancel()
                        s.cache.UnregisterActiveRequest(provider.ProviderAPIKeyID, activeRequestID)

                        lastErr = fmt.Errorf("供应商返回HTTP %d", errStatusCode)
                        log.Printf("[代理] 供应商 %s (模型: %s) 返回错误状态 HTTP %d (Content-Type: %s, 响应体: %s, 耗时:%v), 尝试下一个供应商",
                                provider.ProviderName, provider.ProviderModelName, errStatusCode,
                                errContentType, truncateString(string(errorBody), 200), reqDuration)

                        // ===== 自动状态检测 =====
                        // 根据错误码和响应体内容自动判断应设置的状态
                        newStatus := classifyErrorStatus(errStatusCode, string(errorBody))
                        if newStatus != "" && newStatus != provider.Status {
                                // 欠费状态立即设置（不等待连续失败计数）
                                if newStatus == "arrears" {
                                        log.Printf("[代理] 检测到 API Key(ID=%d) 欠费信号: %s → arrears (原因: HTTP %d)",
                                                provider.ProviderAPIKeyID, provider.Status, errStatusCode)
                                        s.cache.ResetAPIKeyFailure(provider.ProviderAPIKeyID)
                                        if err := s.cache.SetProviderAPIKeyStatus(provider.ProviderAPIKeyID, "arrears"); err != nil {
                                                log.Printf("[代理] 自动设置欠费状态失败: %v", err)
                                        }
                                } else {
                                        // 冷却状态需要连续失败N次才标记
                                        failCount := s.cache.RecordAPIKeyFailure(provider.ProviderAPIKeyID)
                                        if failCount == -1 {
                                                log.Printf("[代理] API Key(ID=%d) 连续失败达到阈值，已自动标记为冷却 (原因: HTTP %d)",
                                                        provider.ProviderAPIKeyID, errStatusCode)
                                        } else {
                                                log.Printf("[代理] API Key(ID=%d) 第 %d 次失败 (原因: HTTP %d, 阈值: %d)",
                                                        provider.ProviderAPIKeyID, failCount, errStatusCode, s.cache.GetAutoStatusConfig().ConsecutiveFailures)
                                        }
                                }
                        } else if newStatus == "" {
                                // 未知错误类型，也计入连续失败（防止所有错误被忽略）
                                failCount := s.cache.RecordAPIKeyFailure(provider.ProviderAPIKeyID)
                                if failCount > 0 && failCount != -1 {
                                        log.Printf("[代理] API Key(ID=%d) 第 %d 次失败 (HTTP %d, 未匹配自动状态规则)",
                                                provider.ProviderAPIKeyID, failCount, errStatusCode)
                                }
                        }
                        // ===== 自动状态检测结束 =====

                        continue
                }

                // ---- 阶段2：首Token返回超时 ----
                // 检测上游是否在合理时间内返回了第一个字节
                // log.Printf("[DEBUG] [%s] 开始等待首Token (超时: %v)...", provider.ProviderName, timeouts.FirstToken)
                ftStart := time.Now()
                firstByte, err := readFirstByteWithTimeout(resp.Body, timeouts.FirstToken, totalCtx)
                ftDuration := time.Since(ftStart)
                if err != nil {
                        totalCancel()
                        resp.Body.Close()
                        s.cache.UnregisterActiveRequest(provider.ProviderAPIKeyID, activeRequestID)
                        if errors.Is(err, errFirstTokenTimeout) {
                                lastErr = fmt.Errorf("首Token超时 (%v)", timeouts.FirstToken)
                                log.Printf("[代理] 供应商 %s (模型: %s) 首 Token 超时 (%v)，尝试下一个供应商 (等待耗时:%v)",
                                        provider.ProviderName, provider.ProviderModelName, timeouts.FirstToken, ftDuration)
                        } else if totalCtx.Err() != nil {
                                lastErr = fmt.Errorf("总超时 (%v)", timeouts.Total)
                                log.Printf("[代理] 供应商 %s (模型: %s) 总超时 (%v) (等待耗时:%v)", provider.ProviderName, provider.ProviderModelName, timeouts.Total, ftDuration)
                        } else {
                                lastErr = fmt.Errorf("读取首字节失败: %w", err)
                                log.Printf("[代理] 供应商 %s (模型: %s) 读取首字节失败: %v (等待耗时:%v)", provider.ProviderName, provider.ProviderModelName, err, ftDuration)
                        }
                        continue
                }
                // log.Printf("[DEBUG] [%s] 首Token已接收(0x%02x), 等待耗时:%v", provider.ProviderName, firstByte, ftDuration)

                // 连接成功 + 首Token已收到，不可再重试，退出循环
                // 从此开始进入响应传输阶段
                connectDuration := time.Since(startTime)

                // ===== 请求成功，重置连续失败计数 =====
                s.cache.ResetAPIKeyFailure(provider.ProviderAPIKeyID)

                // ---- 阶段B：响应传输（不可重试阶段） ----
                isStream := isStreamRequest(bodyBytes)

                if isStream {
                        // ---- 流式请求：使用 streamingCopy（逐块flush + 流Idle超时） ----
                        log.Printf("[代理] 供应商 %s (模型: %s) 连接成功，首 Token 已接收 (首字节:0x%02x, HTTP状态:%d, 连接耗时:%v, 模式:流式, 超时:总=%v/流Idle=%v)",
                                provider.ProviderName, provider.ProviderModelName, firstByte, resp.StatusCode, connectDuration, timeouts.Total, timeouts.StreamIdle)

                        // 复制响应头
                        for key, values := range resp.Header {
                                for _, value := range values {
                                        w.Header().Add(key, value)
                                }
                        }
                        w.WriteHeader(resp.StatusCode)

                        // 创建输出采集buffer，用于记录流式响应的完整输出
                        var outputBuf bytes.Buffer

                        // streamingCopy 在流式传输中监控 idle 超时和总超时
                        outputBytes, streamCopyErr = streamingCopy(w, resp.Body, firstByte, timeouts.StreamIdle, totalCtx, provider.ProviderName, &outputBuf)
                        totalCancel()
                        s.cache.UnregisterActiveRequest(provider.ProviderAPIKeyID, activeRequestID)

                        // 采集流式输出数据：先聚合为完整的非流式响应，再截断到安全大小
                        // 注意：必须先聚合再截断，因为SSE原始数据包含大量帧头开销（每个chunk都有
                        // "data: {"choices":[{"delta":{"content":"..."}}]}",...），
                        // 如果先截断再聚合，64KB的SSE原始数据可能只对应几千字节的实际内容，
                        // 导致聚合结果严重不完整。聚合后的JSON远小于原始SSE，截断上限设为256KB。
                        aggregatedOutput := callrecords.AggregateStreamOutput(outputBuf.String())
                        capturedOutput = truncateString(aggregatedOutput, 262144)

                        streamDuration := time.Since(startTime)
                        if streamCopyErr != nil {
                                log.Printf("[代理] 流传输异常: %v (供应商: %s, 已写入: %d 字节, 总耗时: %v)",
                                        streamCopyErr, provider.ProviderName, outputBytes, streamDuration)
                        } else {
                                log.Printf("[代理] 流传输完成 (供应商: %s, 已写入: %d 字节, 总耗时: %v)",
                                        provider.ProviderName, outputBytes, streamDuration)
                        }

                        if streamCopyErr != nil {
                                proxyStatus = "error"
                        } else {
                                proxyStatus = "success"
                        }
                } else {
                        // ---- 非流式请求：使用 bufferedCopy（一次性完整读取+写入） ----
                        // 非流式请求不走 streamingCopy，避免：
                        //   1. 流Idle超时截断完整响应（Ollama等后端生成完毕后才一次性返回JSON）
                        //   2. 逐块flush与上游Content-Length冲突导致客户端解析失败
                        //   3. 不必要的首字节拆分（readFirstByteWithTimeout消费1字节后再拼接）
                        log.Printf("[代理] 供应商 %s (模型: %s) 连接成功 (HTTP状态:%d, 连接耗时:%v, 模式:非流式, 超时:总=%v)",
                                provider.ProviderName, provider.ProviderModelName, resp.StatusCode, connectDuration, timeouts.Total)

                        // 非流式响应：读取完整响应体，然后一次性写入
                        // 不复制上游的 Transfer-Encoding/Content-Length 头，让Go自动根据实际写入量设置
                        for key, values := range resp.Header {
                                for _, value := range values {
                                        lowerKey := strings.ToLower(key)
                                        // 跳过 hop-by-hop 头和传输编码头，避免与Go的ResponseWriter冲突
                                        if lowerKey == "transfer-encoding" || lowerKey == "content-length" {
                                                continue
                                        }
                                        w.Header().Add(key, value)
                                }
                        }
                        // 将已读取的首字节放回，组成完整响应体
                        // readFirstByteWithTimeout 已消费1字节，需要将它与剩余体拼接
                        w.WriteHeader(resp.StatusCode)

                        // 先写入已读取的首字节
                        n, writeErr := w.Write([]byte{firstByte})
                        outputBytes = int64(n)
                        if writeErr != nil {
                                streamCopyErr = fmt.Errorf("写入首字节失败: %w", writeErr)
                                totalCancel()
                                s.cache.UnregisterActiveRequest(provider.ProviderAPIKeyID, activeRequestID)
                                proxyStatus = "error"
                                break
                        }

                        // 读取并写入剩余响应体（带总超时，无流Idle超时）
                        remainingBytes, remainingData, remainingErr := bufferedCopy(w, resp.Body, totalCtx, provider.ProviderName)
                        outputBytes += remainingBytes
                        totalCancel()
                        s.cache.UnregisterActiveRequest(provider.ProviderAPIKeyID, activeRequestID)

                        // 采集非流式输出数据：首字节 + 剩余体
                        if remainingData != nil {
                                var fullOutput bytes.Buffer
                                fullOutput.WriteByte(firstByte)
                                fullOutput.Write(remainingData)
                                capturedOutput = truncateString(fullOutput.String(), 262144)
                        }

                        streamDuration := time.Since(startTime)
                        if remainingErr != nil {
                                streamCopyErr = remainingErr
                                log.Printf("[代理] 非流式响应传输异常: %v (供应商: %s, 已写入: %d 字节, 总耗时: %v)",
                                        remainingErr, provider.ProviderName, outputBytes, streamDuration)
                                proxyStatus = "error"
                        } else {
                                log.Printf("[代理] 非流式响应传输完成 (供应商: %s, 已写入: %d 字节, 总耗时: %v)",
                                        provider.ProviderName, outputBytes, streamDuration)
                                proxyStatus = "success"
                        }
                }

                break
        }

        // 所有供应商连接/首Token阶段都失败
        if resp == nil {
                errMsg := "All providers failed"
                if lastErr != nil {
                        errMsg = lastErr.Error()
                }
                endTime := time.Now()
                s.recorder.Record(traffic.TrafficItem{
                        APIKeyID:         keyInfo.KeyID,
                        UserID:           keyInfo.UserID,
                        ModelID:          model.ID,
                        ProviderID:       usedProvider.ProviderID,
                        ProviderAPIKeyID: usedProvider.ProviderAPIKeyID,
                        InputBytes:       inputBytes,
                        OutputBytes:      0,
                        StartTime:        startTime,
                        EndTime:          endTime,
                        Duration:         endTime.Sub(startTime).Milliseconds(),
                        Status:           "error",
                })

                // 记录API调用（失败）
                if s.callRecords != nil {
                        s.callRecords.Add(callrecords.CallRecord{
                                Time:           startTime,
                                Caller:         keyInfo.UserName,
                                ModelName:      modelName,
                                InputDataSize:  inputBytes,
                                OutputDataSize: 0,
                                TotalDuration:  endTime.Sub(startTime).Milliseconds(),
                                Status:         "error",
                                InputParams:    callrecords.DecodeJSON(truncateString(string(bodyBytes), 262144)),
                                OutputParams:   callrecords.DecodeJSON(truncateString(errMsg, 4096)),
                                ProviderName:   usedProvider.ProviderName,
                                ProviderModel:  usedProvider.ProviderModelName,
                                IsStream:       isStreamRequest(bodyBytes),
                        })
                }

                http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"server_error"}}`, errMsg), http.StatusBadGateway)
                return
        }
        defer resp.Body.Close()

        // 成功时记录会话亲和性
        if s.affinity && proxyStatus == "success" && affinityKey != "" {
                s.cache.SetAffinity(affinityKey, usedProvider.ProviderID)
        }

        // 记录流量
        endTime := time.Now()
        s.recorder.Record(traffic.TrafficItem{
                APIKeyID:         keyInfo.KeyID,
                UserID:           keyInfo.UserID,
                ModelID:          model.ID,
                ProviderID:       usedProvider.ProviderID,
                ProviderAPIKeyID: usedProvider.ProviderAPIKeyID,
                InputBytes:       inputBytes,
                OutputBytes:      outputBytes,
                StartTime:        startTime,
                EndTime:          endTime,
                Duration:         endTime.Sub(startTime).Milliseconds(),
                Status:           proxyStatus,
        })

        // 记录API调用（成功或部分成功）
        if s.callRecords != nil {
                s.callRecords.Add(callrecords.CallRecord{
                        Time:           startTime,
                        Caller:         keyInfo.UserName,
                        ModelName:      modelName,
                        InputDataSize:  inputBytes,
                        OutputDataSize: outputBytes,
                        TotalDuration:  endTime.Sub(startTime).Milliseconds(),
                        Status:         proxyStatus,
                        InputParams:    callrecords.DecodeJSON(truncateString(string(bodyBytes), 262144)),
                        OutputParams:   callrecords.DecodeJSON(capturedOutput), // 采集的完整输出数据（流式/非流式均支持，已UTF-8解码）
                        ProviderName:   usedProvider.ProviderName,
                        ProviderModel:  usedProvider.ProviderModelName,
                        IsStream:       isStreamRequest(bodyBytes),
                })
        }
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

// maskAPIKey 对API-Key脱敏，仅显示前4位和后4位，中间用****替代
func maskAPIKey(key string) string {
        if len(key) <= 8 {
                return "****"
        }
        return key[:4] + "****" + key[len(key)-4:]
}

// truncateString 截断字符串到指定长度，超出部分用"..."替代
func truncateString(s string, maxLen int) string {
        if len(s) <= maxLen {
                return s
        }
        if maxLen <= 3 {
                return s[:maxLen]
        }
        return s[:maxLen-3] + "..."
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
