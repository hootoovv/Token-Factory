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
// 支持的格式：
//   - OpenAI Chat Completion 流式响应（delta.content / delta.tool_calls / delta.function_call）
//   - 包含 usage 字段的最后一个chunk
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

	// 用于聚合的结构
	var aggregated struct {
		ID      string                 `json:"id"`
		Object  string                 `json:"object"`
		Created int64                  `json:"created,omitempty"`
		Model   string                 `json:"model,omitempty"`
		Choices []AggregatedChoice     `json:"choices"`
		Usage   map[string]interface{} `json:"usage,omitempty"`
	}

	// choices 按 index 聚合
	choiceMap := make(map[int]*AggregatedChoice)

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
			// 解析失败，跳过此行
			continue
		}
		chunkCount++

		// 提取顶层字段（取第一个有效chunk的值）
		if aggregated.ID == "" {
			if id, ok := chunk["id"].(string); ok {
				aggregated.ID = id
			}
			if obj, ok := chunk["object"].(string); ok {
				aggregated.Object = obj
			}
			if created, ok := chunk["created"].(float64); ok {
				aggregated.Created = int64(created)
			}
			if model, ok := chunk["model"].(string); ok {
				aggregated.Model = model
			}
		}

		// 提取 usage（通常在最后一个chunk中出现）
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			aggregated.Usage = usage
		}

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

			// 获取或创建聚合choice
			if _, exists := choiceMap[idx]; !exists {
				choiceMap[idx] = &AggregatedChoice{
					Index: idx,
					Message: &AggregatedMessage{
						Role:    "assistant",
						Content: "",
					},
					FinishReason: nil,
				}
			}
			aggChoice := choiceMap[idx]

			// 提取 delta
			delta, ok := choiceData["delta"].(map[string]interface{})
			if ok {
				// 提取 role（通常在第一个chunk中）
				if role, ok := delta["role"].(string); ok && role != "" {
					aggChoice.Message.Role = role
				}
				// 提取 content
				if content, ok := delta["content"].(string); ok {
					aggChoice.Message.Content += content
				}
				// 提取 tool_calls（函数调用）
				if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
					aggChoice.aggregateToolCalls(toolCalls)
				}
				// 提取 function_call（旧版函数调用）
				if fnCall, ok := delta["function_call"].(map[string]interface{}); ok {
					aggChoice.aggregateFunctionCall(fnCall)
				}
			}

			// 提取 finish_reason
			if fr, ok := choiceData["finish_reason"]; ok && fr != nil {
				if frStr, ok := fr.(string); ok && frStr != "" {
					aggChoice.FinishReason = &frStr
				}
			}
		}
	}

	// 如果没有成功解析任何chunk，返回原始数据
	if chunkCount == 0 || len(choiceMap) == 0 {
		return rawOutput
	}

	// 构建 choices 列表（按 index 排序）
	for i := 0; i < len(choiceMap); i++ {
		if choice, ok := choiceMap[i]; ok {
			aggregated.Choices = append(aggregated.Choices, *choice)
		}
	}

	// 将 object 从 "chat.completion.chunk" 改为 "chat.completion"
	if aggregated.Object == "chat.completion.chunk" {
		aggregated.Object = "chat.completion"
	}

	// 序列化为美化的JSON
	result, err := json.MarshalIndent(aggregated, "", "  ")
	if err != nil {
		log.Printf("[调用记录] 聚合流式输出序列化失败: %v", err)
		return rawOutput
	}

	return string(result)
}

// AggregatedChoice 聚合后的选择项
type AggregatedChoice struct {
	Index        int                  `json:"index"`
	Message      *AggregatedMessage   `json:"message"`
	FinishReason *string              `json:"finish_reason"`
	ToolCalls    []AggregatedToolCall `json:"tool_calls,omitempty"`
}

// AggregatedMessage 聚合后的消息
type AggregatedMessage struct {
	Role      string               `json:"role"`
	Content   string               `json:"content"`
	ToolCalls []AggregatedToolCall `json:"tool_calls,omitempty"`
	FuncCall  *AggregatedFuncCall  `json:"function_call,omitempty"`
}

// AggregatedToolCall 聚合后的工具调用项
type AggregatedToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function *AggregatedFuncCall `json:"function"`
}

// AggregatedFuncCall 聚合后的函数调用
type AggregatedFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// aggregateToolCalls 将增量 tool_calls delta 拼接到 choice 的 tool_calls 列表
func (c *AggregatedChoice) aggregateToolCalls(deltas []interface{}) {
	for _, d := range deltas {
		delta, ok := d.(map[string]interface{})
		if !ok {
			continue
		}

		tcIndex := 0
		if idx, ok := delta["index"].(float64); ok {
			tcIndex = int(idx)
		}

		// 扩展切片以容纳此index
		for len(c.Message.ToolCalls) <= tcIndex {
			c.Message.ToolCalls = append(c.Message.ToolCalls, AggregatedToolCall{
				Index:    len(c.Message.ToolCalls),
				ID:       "",
				Type:     "function",
				Function: &AggregatedFuncCall{},
			})
		}

		tc := &c.Message.ToolCalls[tcIndex]

		// ID 仅设置一次
		if id, ok := delta["id"].(string); ok && id != "" {
			tc.ID = id
		}
		// type 仅设置一次
		if typ, ok := delta["type"].(string); ok && typ != "" {
			tc.Type = typ
		}
		// function delta
		if fnDelta, ok := delta["function"].(map[string]interface{}); ok {
			if name, ok := fnDelta["name"].(string); ok && name != "" {
				tc.Function.Name = name
			}
			if args, ok := fnDelta["arguments"].(string); ok {
				tc.Function.Arguments += args
			}
		}
	}

	// 同步到 choice 层级的 ToolCalls（用于 JSON 输出）
	c.ToolCalls = c.Message.ToolCalls
}

// aggregateFunctionCall 将旧版 function_call delta 拼接到 choice
func (c *AggregatedChoice) aggregateFunctionCall(delta map[string]interface{}) {
	if c.Message.FuncCall == nil {
		c.Message.FuncCall = &AggregatedFuncCall{}
	}
	if name, ok := delta["name"].(string); ok && name != "" {
		c.Message.FuncCall.Name = name
	}
	if args, ok := delta["arguments"].(string); ok {
		c.Message.FuncCall.Arguments += args
	}
}
