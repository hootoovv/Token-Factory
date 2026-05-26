package callrecords

import (
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
	OutputParams   string    `json:"output_params"`    // 输出参数（JSON字符串）
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
