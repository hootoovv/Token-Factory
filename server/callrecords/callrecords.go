package callrecords

import (
        "encoding/json"
        "log"
        "strings"
        "sync"
        "time"
)

// CallRecord 单次API调用记录
type CallRecord struct {
        ID             uint      `json:"id"`               // 记录序号（自增）
        Time           time.Time `json:"time"`             // 调用时间
        Caller         string    `json:"caller"`           // 调用者（用户名）
        ModelName      string    `json:"model_name"`       // 模型名称
        InputDataSize  int64     `json:"input_data_size"`  // 输入数据量（字节）
        OutputDataSize int64     `json:"output_data_size"` // 输出数据量（字节）
        TotalDuration  int64     `json:"total_duration"`   // 总用时（毫秒）
        Status         string    `json:"status"`           // 调用状态：success / error
        InputParams    string    `json:"input_params"`     // 输入参数（JSON字符串）
        OutputParams   string    `json:"output_params"`    // 输出参数（JSON字符串，流式请求已聚合为完整响应）
        ProviderName   string    `json:"provider_name"`    // 供应商名称
        ProviderModel  string    `json:"provider_model"`   // 供应商侧模型名
        IsStream       bool      `json:"is_stream"`        // 是否流式请求
}

// Store 内存中的调用记录存储（环形缓冲区）
type Store struct {
        mu      sync.RWMutex
        records []CallRecord
        limit   int
        nextID  uint
}

// NewStore 创建调用记录存储
func NewStore(limit int) *Store {
        if limit <= 0 {
                limit = 10
        }
        if limit > 20 {
                limit = 20
        }
        return &Store{
                records: make([]CallRecord, 0, limit),
                limit:   limit,
                nextID:  1,
        }
}

// Add 添加一条调用记录（线程安全）
func (s *Store) Add(record CallRecord) {
        s.mu.Lock()
        defer s.mu.Unlock()

        record.ID = s.nextID
        s.nextID++

        if len(s.records) >= s.limit {
                // 环形缓冲：移除最旧的记录
                s.records = s.records[1:]
        }
        s.records = append(s.records, record)
}

// GetAll 获取所有调用记录（按时间倒序，线程安全）
func (s *Store) GetAll() []CallRecord {
        s.mu.RLock()
        defer s.mu.RUnlock()

        // 返回倒序副本（最新的在前）
        result := make([]CallRecord, len(s.records))
        for i, j := 0, len(s.records)-1; j >= 0; i, j = i+1, j-1 {
                result[i] = s.records[j]
        }
        return result
}

// GetByID 根据ID获取单条记录（线程安全）
func (s *Store) GetByID(id uint) *CallRecord {
        s.mu.RLock()
        defer s.mu.RUnlock()

        for i := range s.records {
                if s.records[i].ID == id {
                        return &s.records[i]
                }
        }
        return nil
}

// Limit 返回配置的记录上限
func (s *Store) Limit() int {
        s.mu.RLock()
        defer s.mu.RUnlock()
        return s.limit
}

// ==================== 流式输出聚合 ====================

// AggregateStreamOutput 将原始SSE流式数据聚合为完整的非流式API响应JSON
// 解析SSE格式（data: {json}\n\n），提取所有chunk中的内容，合并为一条完整的响应
//
// 聚合策略（全量保留所有JSON字段）：
//   - 顶层字段：保留所有字段，首次出现的值优先（id/object/created/model 等），
//     usage 等出现在后续 chunk 中的字段取最后非空值
//   - choices[].delta：转为 choices[].message，所有字段全量保留
//     - 字符串字段（content/reasoning_content/refusal 等）：拼接
//     - role 字段：取首个非空值
//     - tool_calls：增量拼接（id/name 仅首次，arguments 拼接）
//     - function_call：增量拼接（name 仅首次，arguments 拼接）
//     - 其他未知字段：非字符串取最后值，字符串拼接
//   - choices[].finish_reason：取最后非空值
//   - choices 中除 index/delta/finish_reason 外的其他字段：保留最后值
//
// 如果解析失败（非SSE格式），返回原始数据
func AggregateStreamOutput(rawOutput string) string {
        if rawOutput == "" {
                return rawOutput
        }

        // 检测是否为SSE格式（包含 "data: " 行）
        if !strings.Contains(rawOutput, "data: ") {
                return rawOutput
        }

        lines := strings.Split(rawOutput, "\n")

        // 使用 map 存储聚合结果，全量保留所有字段
        aggregated := make(map[string]interface{})

        // choices 按 index 聚合
        type choiceState struct {
                choice  map[string]interface{} // choice 级别的所有字段（index/finish_reason/logprobs/等）
                message map[string]interface{} // 从 delta 合并而来的 message（全量保留所有字段）
        }
        choiceMap := make(map[int]*choiceState)

        chunkCount := 0
        for _, line := range lines {
                line = strings.TrimSpace(line)
                if line == "" || !strings.HasPrefix(line, "data: ") {
                        continue
                }

                dataStr := strings.TrimPrefix(line, "data: ")
                dataStr = strings.TrimSpace(dataStr)

                // 结束标记
                if dataStr == "[DONE]" {
                        continue
                }

                // 解析chunk JSON
                var chunk map[string]interface{}
                if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
                        continue
                }
                chunkCount++

                // 合并顶层字段（全量保留）
                mergeTopLevel(aggregated, chunk)

                // 提取 choices
                choices, ok := chunk["choices"].([]interface{})
                if !ok {
                        continue
                }

                for _, c := range choices {
                        choiceData, ok := c.(map[string]interface{})
                        if !ok {
                                continue
                        }

                        idx := 0
                        if index, ok := choiceData["index"].(float64); ok {
                                idx = int(index)
                        }

                        // 获取或创建聚合 choice
                        if _, exists := choiceMap[idx]; !exists {
                                choiceMap[idx] = &choiceState{
                                        choice:  make(map[string]interface{}),
                                        message: make(map[string]interface{}),
                                }
                                choiceMap[idx].choice["index"] = idx
                                choiceMap[idx].message["role"] = "assistant"
                        }
                        cs := choiceMap[idx]

                        // 合并 choice 级别字段（除 delta 外的所有字段全量保留）
                        mergeChoiceFields(cs.choice, choiceData)

                        // 合并 delta → message（全量保留所有字段）
                        if delta, ok := choiceData["delta"].(map[string]interface{}); ok {
                                mergeDeltaToMessage(cs.message, delta)
                        }
                }
        }

        // 如果没有成功解析任何chunk，返回原始数据
        if chunkCount == 0 || len(choiceMap) == 0 {
                return rawOutput
        }

        // 构建 choices 列表（按 index 排序）
        choicesList := make([]interface{}, 0, len(choiceMap))
        for i := 0; i < len(choiceMap); i++ {
                cs, ok := choiceMap[i]
                if !ok {
                        continue
                }
                // 将 message 写入 choice
                choice := cs.choice
                choice["message"] = cs.message
                choicesList = append(choicesList, choice)
        }
        aggregated["choices"] = choicesList

        // 将 object 从 "chat.completion.chunk" 改为 "chat.completion"
        if obj, ok := aggregated["object"].(string); ok && obj == "chat.completion.chunk" {
                aggregated["object"] = "chat.completion"
        }

        // 序列化为美化的JSON
        result, err := json.MarshalIndent(aggregated, "", "  ")
        if err != nil {
                log.Printf("[调用记录] 聚合流式输出序列化失败: %v", err)
                return rawOutput
        }

        return string(result)
}

// mergeTopLevel 合并顶层字段到聚合结果
// 策略：首次出现的值优先（id/object/created/model 等标识字段不覆盖），
// 其余字段（usage/system_fingerprint/service_tier 等）取最后非nil值
func mergeTopLevel(aggregated map[string]interface{}, chunk map[string]interface{}) {
        // 仅在首次出现时设置的字段（响应标识，不应被后续chunk覆盖）
        topLevelFirstSet := map[string]bool{
                "id":      true,
                "object":  true,
                "created": true,
                "model":   true,
        }

        for k, v := range chunk {
                // choices 单独处理
                if k == "choices" {
                        continue
                }
                if topLevelFirstSet[k] {
                        // 标识字段：仅首次设置
                        if _, exists := aggregated[k]; !exists {
                                aggregated[k] = v
                        }
                } else {
                        // 其他字段：后出现的覆盖（usage/system_fingerprint 等取最后值）
                        if v != nil {
                                aggregated[k] = v
                        }
                }
        }
}

// mergeChoiceFields 合并 choice 级别字段（除 delta 外）
// 保留所有字段，delta 单独处理不在此处合并
func mergeChoiceFields(choice map[string]interface{}, choiceData map[string]interface{}) {
        for k, v := range choiceData {
                // delta 和 message 由 mergeDeltaToMessage 单独处理
                if k == "delta" || k == "message" {
                        continue
                }
                // index 仅首次设置
                if k == "index" {
                        if _, exists := choice[k]; !exists {
                                choice[k] = v
                        }
                        continue
                }
                // finish_reason：取最后非nil非空值
                if k == "finish_reason" {
                        if v != nil {
                                if strVal, ok := v.(string); ok && strVal != "" {
                                        choice[k] = v
                                } else if !ok {
                                        // 非字符串类型（如 null），跳过
                                }
                        }
                        continue
                }
                // 其他字段（logprobs 等）：取最后非nil值
                if v != nil {
                        choice[k] = v
                }
        }
}

// mergeDeltaToMessage 将 delta 中的字段合并到 message
// 策略：
//   - role：取首个非空值
//   - tool_calls：增量拼接（id/name 仅首次，arguments 拼接）
//   - function_call：增量拼接（name 仅首次，arguments 拼接）
//   - 其他字符串字段（content/reasoning_content/refusal/等）：拼接
//   - 其他非字符串字段：取最后非nil值
func mergeDeltaToMessage(message map[string]interface{}, delta map[string]interface{}) {
        for k, v := range delta {
                switch k {
                case "role":
                        // role：取首个非空值
                        if v != nil {
                                if strVal, ok := v.(string); ok && strVal != "" {
                                        if existing, ok := message["role"].(string); !ok || existing == "" {
                                                message["role"] = strVal
                                        }
                                }
                        }

                case "tool_calls":
                        // tool_calls：增量拼接
                        if toolCallDeltas, ok := v.([]interface{}); ok {
                                mergeToolCalls(message, toolCallDeltas)
                        }

                case "function_call":
                        // function_call：增量拼接
                        if fnCallDelta, ok := v.(map[string]interface{}); ok {
                                mergeFunctionCall(message, fnCallDelta)
                        }

                default:
                        // 其他字段：字符串拼接，非字符串取最后值
                        if v == nil {
                                continue
                        }
                        if strVal, ok := v.(string); ok {
                                // 字符串字段：拼接（content/reasoning_content/refusal/等）
                                if existing, ok := message[k].(string); ok {
                                        message[k] = existing + strVal
                                } else {
                                        message[k] = strVal
                                }
                        } else {
                                // 非字符串字段：取最后值
                                message[k] = v
                        }
                }
        }
}

// mergeToolCalls 将增量 tool_calls delta 拼接到 message 的 tool_calls 列表
// tool_calls 结构：
//
//      delta: {"tool_calls": [{"index":0, "id":"call_xxx", "type":"function", "function":{"name":"fn","arguments":"..."}}]}
//
// 聚合规则：
//   - index 用于匹配同一组 tool call
//   - id/type/function.name 仅首次设置
//   - function.arguments 拼接
func mergeToolCalls(message map[string]interface{}, deltas []interface{}) {
        // 获取或创建 tool_calls 列表
        var toolCalls []map[string]interface{}
        if existing, ok := message["tool_calls"].([]interface{}); ok {
                for _, tc := range existing {
                        if tcMap, ok := tc.(map[string]interface{}); ok {
                                toolCalls = append(toolCalls, tcMap)
                        }
                }
        }

        for _, d := range deltas {
                delta, ok := d.(map[string]interface{})
                if !ok {
                        continue
                }

                tcIndex := 0
                if idx, ok := delta["index"].(float64); ok {
                        tcIndex = int(idx)
                }

                // 扩展切片以容纳此 index
                for len(toolCalls) <= tcIndex {
                        toolCalls = append(toolCalls, map[string]interface{}{
                                "index":    len(toolCalls),
                                "id":       "",
                                "type":     "function",
                                "function": map[string]interface{}{"name": "", "arguments": ""},
                        })
                }

                tc := toolCalls[tcIndex]

                // ID 仅设置一次
                if id, ok := delta["id"].(string); ok && id != "" {
                        tc["id"] = id
                }
                // type 仅设置一次
                if typ, ok := delta["type"].(string); ok && typ != "" {
                        tc["type"] = typ
                }
                // function delta
                if fnDelta, ok := delta["function"].(map[string]interface{}); ok {
                        fnMap, ok := tc["function"].(map[string]interface{})
                        if !ok {
                                fnMap = make(map[string]interface{})
                                tc["function"] = fnMap
                        }
                        if name, ok := fnDelta["name"].(string); ok && name != "" {
                                fnMap["name"] = name
                        }
                        if args, ok := fnDelta["arguments"].(string); ok {
                                if existingArgs, ok := fnMap["arguments"].(string); ok {
                                        fnMap["arguments"] = existingArgs + args
                                } else {
                                        fnMap["arguments"] = args
                                }
                        }
                        // function 中的其他未知字段也全量保留
                        for k, v := range fnDelta {
                                if k != "name" && k != "arguments" && v != nil {
                                        fnMap[k] = v
                                }
                        }
                }

                // tool_call 中的其他未知字段也全量保留（除 index/id/type/function）
                for k, v := range delta {
                        if k != "index" && k != "id" && k != "type" && k != "function" && v != nil {
                                tc[k] = v
                        }
                }
        }

        // 写回 message
        tcInterface := make([]interface{}, len(toolCalls))
        for i, tc := range toolCalls {
                tcInterface[i] = tc
        }
        message["tool_calls"] = tcInterface
}

// mergeFunctionCall 将旧版 function_call delta 拼接到 message
func mergeFunctionCall(message map[string]interface{}, delta map[string]interface{}) {
        fnMap, ok := message["function_call"].(map[string]interface{})
        if !ok {
                fnMap = make(map[string]interface{})
                message["function_call"] = fnMap
        }
        if name, ok := delta["name"].(string); ok && name != "" {
                fnMap["name"] = name
        }
        if args, ok := delta["arguments"].(string); ok {
                if existingArgs, ok := fnMap["arguments"].(string); ok {
                        fnMap["arguments"] = existingArgs + args
                } else {
                        fnMap["arguments"] = args
                }
        }
        // function_call 中的其他未知字段也全量保留
        for k, v := range delta {
                if k != "name" && k != "arguments" && v != nil {
                        fnMap[k] = v
                }
        }
}

// ==================== 输入参数 JSON 解码 ====================

// DecodeJSON 将 JSON 字符串进行解析后重新序列化，实现 UTF-8 解码
// 作用：将 JSON 中的 Unicode 转义序列（如 \u4f60\u597d）解码为可读的 UTF-8 字符（如 你好），
// 同时美化 JSON 格式（缩进2空格），与输出参数的格式保持一致
// 如果解析失败（非合法JSON），返回原始字符串
func DecodeJSON(raw string) string {
        if raw == "" {
                return raw
        }
        var data interface{}
        if err := json.Unmarshal([]byte(raw), &data); err != nil {
                return raw
        }
        decoded, err := json.MarshalIndent(data, "", "  ")
        if err != nil {
                log.Printf("[调用记录] JSON解码序列化失败: %v", err)
                return raw
        }
        return string(decoded)
}
