package traffic

import (
	"fmt"
	"log"
	"time"

	"token_factory/database"

	"gorm.io/gorm"
)

// TrafficItem 流量记录项
type TrafficItem struct {
	APIKeyID    uint
	UserID      uint
	ModelID     uint
	ProviderID  uint
	InputBytes  int64
	OutputBytes int64
	StartTime   time.Time
	EndTime     time.Time
	Duration    int64 // 毫秒
	Status      string
}

// DashboardStats Dashboard统计数据
type DashboardStats struct {
	TotalCalls       int64   `json:"total_calls"`
	TotalInputBytes  int64   `json:"total_input_bytes"`
	TotalOutputBytes int64   `json:"total_output_bytes"`
	AvgDuration      float64 `json:"avg_duration"` // 平均耗时毫秒
}

// RankingItem 排行项
type RankingItem struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Count       int64  `json:"count"`
	InputBytes  int64  `json:"input_bytes"`
	OutputBytes int64  `json:"output_bytes"`
}

// Recorder 流量记录器，批量写入数据库
type Recorder struct {
	records       chan TrafficItem
	db            *gorm.DB
	done          chan struct{}
	batchSize     int
	flushInterval time.Duration
}

// NewRecorder 创建流量记录器
func NewRecorder(db *gorm.DB, bufferSize int) *Recorder {
	return &Recorder{
		records:       make(chan TrafficItem, bufferSize),
		db:            db,
		done:          make(chan struct{}),
		batchSize:     100,
		flushInterval: 5 * time.Second,
	}
}

// Start 启动后台刷新协程
func (r *Recorder) Start() {
	go r.run()
	log.Printf("[流量] 记录器已启动 (缓冲: %d, 批量: %d, 刷新间隔: %s)",
		cap(r.records), r.batchSize, r.flushInterval)
}

// Stop 停止记录器
func (r *Recorder) Stop() {
	close(r.done)
	// 排空剩余记录
	r.drainAndFlush()
	log.Printf("[流量] 记录器已停止")
}

// Record 记录一条流量（非阻塞，缓冲满则丢弃）
func (r *Recorder) Record(item TrafficItem) {
	select {
	case r.records <- item:
	default:
		log.Printf("[流量] 警告: 缓冲已满，丢弃流量记录")
	}
}

func (r *Recorder) run() {
	ticker := time.NewTicker(r.flushInterval)
	defer ticker.Stop()

	var batch []TrafficItem

	for {
		select {
		case <-r.done:
			return
		case item := <-r.records:
			batch = append(batch, item)
			if len(batch) >= r.batchSize {
				r.flushBatch(batch)
				batch = nil
			}
		case <-ticker.C:
			if len(batch) > 0 {
				r.flushBatch(batch)
				batch = nil
			}
		}
	}
}

func (r *Recorder) drainAndFlush() {
	var batch []TrafficItem
	for {
		select {
		case item := <-r.records:
			batch = append(batch, item)
		default:
			if len(batch) > 0 {
				r.flushBatch(batch)
			}
			return
		}
	}
}

// flushBatch 将一批记录写入数据库
func (r *Recorder) flushBatch(items []TrafficItem) {
	if len(items) == 0 {
		return
	}

	// 按月份分组
	grouped := make(map[string][]TrafficItem)
	for _, item := range items {
		tableName := database.GetTrafficTableName(item.EndTime)
		grouped[tableName] = append(grouped[tableName], item)
	}

	for tableName, group := range grouped {
		// 确保表存在
		if err := database.EnsureTrafficTable(r.db, group[0].EndTime); err != nil {
			log.Printf("[流量] 创建分表 %s 失败: %v", tableName, err)
			continue
		}

		// 批量插入
		for _, item := range group {
			sql := fmt.Sprintf(`INSERT INTO %s (api_key_id, user_id, model_id, provider_id, input_bytes, output_bytes, start_time, end_time, duration, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, tableName)
			if err := r.db.Exec(sql,
				item.APIKeyID, item.UserID, item.ModelID, item.ProviderID,
				item.InputBytes, item.OutputBytes,
				item.StartTime, item.EndTime, item.Duration, item.Status,
				item.EndTime,
			).Error; err != nil {
				log.Printf("[流量] 写入记录失败: %v", err)
			}
		}
	}
}

// FilterParams 过滤参数
type FilterParams struct {
	ModelID    uint
	ProviderID uint
}

// buildWhereClause 构建公共 WHERE 子句
func buildWhereClause(sinceStr string, filter FilterParams) string {
	where := fmt.Sprintf("created_at >= '%s'", sinceStr)
	if filter.ModelID > 0 {
		where += fmt.Sprintf(" AND model_id = %d", filter.ModelID)
	}
	if filter.ProviderID > 0 {
		where += fmt.Sprintf(" AND provider_id = %d", filter.ProviderID)
	}
	return where
}

// GetDashboardStats 获取Dashboard统计数据
func GetDashboardStats(db *gorm.DB, since time.Time, filter FilterParams) (DashboardStats, error) {
	tables, err := database.GetTrafficTables(db)
	if err != nil {
		return DashboardStats{}, err
	}

	if len(tables) == 0 {
		return DashboardStats{}, nil
	}

	// 构建UNION ALL查询
	var stats DashboardStats
	sinceStr := since.Format("2006-01-02 15:04:05")
	where := buildWhereClause(sinceStr, filter)

	// 先获取总调用数
	countSQL := ""
	for i, table := range tables {
		if i > 0 {
			countSQL += " UNION ALL "
		}
		countSQL += fmt.Sprintf("SELECT COUNT(*) as cnt, COALESCE(SUM(input_bytes),0) as ib, COALESCE(SUM(output_bytes),0) as ob, COALESCE(AVG(duration),0) as ad FROM %s WHERE %s", table, where)
	}

	// 聚合所有表的结果
	aggSQL := fmt.Sprintf("SELECT SUM(cnt) as total_calls, SUM(ib) as total_input, SUM(ob) as total_output, CASE WHEN SUM(cnt)>0 THEN SUM(cnt*ad)/SUM(cnt) ELSE 0 END as avg_dur FROM (%s) t", countSQL)

	var result struct {
		TotalCalls  int64
		TotalInput  int64
		TotalOutput int64
		AvgDur      float64
	}

	if err := db.Raw(aggSQL).Scan(&result).Error; err != nil {
		return DashboardStats{}, err
	}

	stats = DashboardStats{
		TotalCalls:       result.TotalCalls,
		TotalInputBytes:  result.TotalInput,
		TotalOutputBytes: result.TotalOutput,
		AvgDuration:      result.AvgDur,
	}

	return stats, nil
}

// GetModelRanking 获取模型使用排行
func GetModelRanking(db *gorm.DB, since time.Time, limit int, filter FilterParams) ([]RankingItem, error) {
	tables, err := database.GetTrafficTables(db)
	if err != nil || len(tables) == 0 {
		return nil, err
	}

	sinceStr := since.Format("2006-01-02 15:04:05")
	where := buildWhereClause(sinceStr, filter)

	unionSQL := ""
	for i, table := range tables {
		if i > 0 {
			unionSQL += " UNION ALL "
		}
		unionSQL += fmt.Sprintf("SELECT model_id, COUNT(*) as cnt, COALESCE(SUM(input_bytes),0) as ib, COALESCE(SUM(output_bytes),0) as ob FROM %s WHERE %s GROUP BY model_id", table, where)
	}

	aggSQL := fmt.Sprintf(`
                SELECT t.model_id as id, m.name, SUM(t.cnt) as count, SUM(t.ib) as input_bytes, SUM(t.ob) as output_bytes
                FROM (%s) t
                LEFT JOIN models m ON m.id = t.model_id
                GROUP BY t.model_id, m.name
                ORDER BY count DESC
                LIMIT %d
        `, unionSQL, limit)

	var items []RankingItem
	if err := db.Raw(aggSQL).Scan(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// GetProviderRanking 获取供应商使用排行
func GetProviderRanking(db *gorm.DB, since time.Time, limit int, filter FilterParams) ([]RankingItem, error) {
	tables, err := database.GetTrafficTables(db)
	if err != nil || len(tables) == 0 {
		return nil, err
	}

	sinceStr := since.Format("2006-01-02 15:04:05")
	where := buildWhereClause(sinceStr, filter)

	unionSQL := ""
	for i, table := range tables {
		if i > 0 {
			unionSQL += " UNION ALL "
		}
		unionSQL += fmt.Sprintf("SELECT provider_id, COUNT(*) as cnt, COALESCE(SUM(input_bytes),0) as ib, COALESCE(SUM(output_bytes),0) as ob FROM %s WHERE %s GROUP BY provider_id", table, where)
	}

	aggSQL := fmt.Sprintf(`
                SELECT t.provider_id as id, p.name, SUM(t.cnt) as count, SUM(t.ib) as input_bytes, SUM(t.ob) as output_bytes
                FROM (%s) t
                LEFT JOIN providers p ON p.id = t.provider_id
                GROUP BY t.provider_id, p.name
                ORDER BY count DESC
                LIMIT %d
        `, unionSQL, limit)

	var items []RankingItem
	if err := db.Raw(aggSQL).Scan(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// GetUserStats 获取用户的使用统计
func GetUserStats(db *gorm.DB, userID uint, since time.Time) (DashboardStats, error) {
	tables, err := database.GetTrafficTables(db)
	if err != nil || len(tables) == 0 {
		return DashboardStats{}, err
	}

	sinceStr := since.Format("2006-01-02 15:04:05")

	countSQL := ""
	for i, table := range tables {
		if i > 0 {
			countSQL += " UNION ALL "
		}
		countSQL += fmt.Sprintf("SELECT COUNT(*) as cnt, COALESCE(SUM(input_bytes),0) as ib, COALESCE(SUM(output_bytes),0) as ob, COALESCE(AVG(duration),0) as ad FROM %s WHERE user_id = %d AND created_at >= '%s'", table, userID, sinceStr)
	}

	aggSQL := fmt.Sprintf("SELECT SUM(cnt) as total_calls, SUM(ib) as total_input, SUM(ob) as total_output, CASE WHEN SUM(cnt)>0 THEN SUM(cnt*ad)/SUM(cnt) ELSE 0 END as avg_dur FROM (%s) t", countSQL)

	var result struct {
		TotalCalls  int64
		TotalInput  int64
		TotalOutput int64
		AvgDur      float64
	}

	if err := db.Raw(aggSQL).Scan(&result).Error; err != nil {
		return DashboardStats{}, err
	}

	return DashboardStats{
		TotalCalls:       result.TotalCalls,
		TotalInputBytes:  result.TotalInput,
		TotalOutputBytes: result.TotalOutput,
		AvgDuration:      result.AvgDur,
	}, nil
}

// GetUserTrafficRecords 获取用户的使用记录
func GetUserTrafficRecords(db *gorm.DB, userID uint, since time.Time, page, pageSize int) ([]map[string]interface{}, int64, error) {
	tables, err := database.GetTrafficTables(db)
	if err != nil || len(tables) == 0 {
		return nil, 0, err
	}

	sinceStr := since.Format("2006-01-02 15:04:05")

	// 先统计总数
	countSQL := ""
	for i, table := range tables {
		if i > 0 {
			countSQL += " UNION ALL "
		}
		countSQL += fmt.Sprintf("SELECT * FROM %s WHERE user_id = %d AND created_at >= '%s'", table, userID, sinceStr)
	}

	var total int64
	db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM (%s) t", countSQL)).Scan(&total)

	// 查询记录
	offset := (page - 1) * pageSize
	querySQL := fmt.Sprintf(`
                SELECT t.*, m.name as model_name, p.name as provider_name
                FROM (%s) t
                LEFT JOIN models m ON m.id = t.model_id
                LEFT JOIN providers p ON p.id = t.provider_id
                ORDER BY t.created_at DESC
                LIMIT %d OFFSET %d
        `, countSQL, pageSize, offset)

	var records []map[string]interface{}
	if err := db.Raw(querySQL).Scan(&records).Error; err != nil {
		return nil, 0, err
	}

	return records, total, nil
}
