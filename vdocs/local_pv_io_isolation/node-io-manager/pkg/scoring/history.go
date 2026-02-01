// Package scoring - 历史行为分析器
package scoring

import (
	"math"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/config"
)

// PodHistory Pod 历史数据
type PodHistory struct {
	PodUID    string
	PodName   string
	Namespace string

	// 违规记录
	ViolationCount   int
	LastViolation    time.Time
	ViolationTypes   map[string]int // 违规类型计数

	// 操作记录
	ActionCount      int
	ActionHistory    []ActionRecord
	SuccessfulActions int
	FailedActions    int

	// 行为统计
	AvgRecoveryTime  time.Duration // 平均恢复时间
	StabilityScore   float64       // 稳定性评分 (0-1)

	// 最后更新时间
	UpdatedAt time.Time
}

// ActionRecord 操作记录
type ActionRecord struct {
	Timestamp    time.Time
	ActionType   string
	Success      bool
	RecoveryTime time.Duration
	Notes        string
}

// HistoryAnalyzer 历史分析器
type HistoryAnalyzer struct {
	config config.ScoringConfig

	// Pod 历史数据
	histories   map[string]*PodHistory
	historiesMu sync.RWMutex

	// 复犯模型参数
	baseRecidivismRate float64
	decayHalfLife      time.Duration
}

// NewHistoryAnalyzer 创建历史分析器
func NewHistoryAnalyzer(cfg config.ScoringConfig) *HistoryAnalyzer {
	return &HistoryAnalyzer{
		config:             cfg,
		histories:          make(map[string]*PodHistory),
		baseRecidivismRate: cfg.RecidivismBaseRate,
		decayHalfLife:      time.Duration(cfg.RecidivismDecayHours) * time.Hour,
	}
}

// CalculateRecidivismScore 计算复犯概率评分
func (ha *HistoryAnalyzer) CalculateRecidivismScore(podUID string) float64 {
	ha.historiesMu.RLock()
	history := ha.histories[podUID]
	ha.historiesMu.RUnlock()

	if history == nil || history.ViolationCount == 0 {
		return ha.baseRecidivismRate * 100 // 转换为 0-100 评分
	}

	// 基于历史违规次数和最近违规时间
	timeSinceLastViolation := time.Since(history.LastViolation)
	decayFactor := math.Exp(-float64(timeSinceLastViolation) / float64(ha.decayHalfLife))

	// 违规次数越多，基础概率越高
	countFactor := 1.0 - math.Exp(-0.3*float64(history.ViolationCount))

	probability := ha.baseRecidivismRate + (1-ha.baseRecidivismRate)*countFactor*decayFactor

	// 考虑操作成功率
	if history.ActionCount > 0 {
		successRate := float64(history.SuccessfulActions) / float64(history.ActionCount)
		// 成功率低说明 Pod 难以控制，复犯概率高
		probability = probability * (2 - successRate)
	}

	return math.Min(probability*100, 100)
}

// RecordViolation 记录违规
func (ha *HistoryAnalyzer) RecordViolation(podUID string, violationType string) {
	ha.historiesMu.Lock()
	defer ha.historiesMu.Unlock()

	history := ha.getOrCreateHistory(podUID)
	history.ViolationCount++
	history.LastViolation = time.Now()
	history.UpdatedAt = time.Now()

	if history.ViolationTypes == nil {
		history.ViolationTypes = make(map[string]int)
	}
	history.ViolationTypes[violationType]++
}

// RecordActionResult 记录操作结果
func (ha *HistoryAnalyzer) RecordActionResult(podUID string, actionType string, success bool) {
	ha.historiesMu.Lock()
	defer ha.historiesMu.Unlock()

	history := ha.getOrCreateHistory(podUID)
	history.ActionCount++

	record := ActionRecord{
		Timestamp:  time.Now(),
		ActionType: actionType,
		Success:    success,
	}

	if success {
		history.SuccessfulActions++
		// 计算恢复时间
		if !history.LastViolation.IsZero() {
			record.RecoveryTime = time.Since(history.LastViolation)
			ha.updateAvgRecoveryTime(history, record.RecoveryTime)
		}
	} else {
		history.FailedActions++
	}

	history.ActionHistory = append(history.ActionHistory, record)

	// 保持历史记录数量限制
	if len(history.ActionHistory) > 100 {
		history.ActionHistory = history.ActionHistory[len(history.ActionHistory)-100:]
	}

	history.UpdatedAt = time.Now()
}

// updateAvgRecoveryTime 更新平均恢复时间
func (ha *HistoryAnalyzer) updateAvgRecoveryTime(history *PodHistory, recoveryTime time.Duration) {
	if history.SuccessfulActions == 1 {
		history.AvgRecoveryTime = recoveryTime
	} else {
		// 指数移动平均
		alpha := 0.3
		history.AvgRecoveryTime = time.Duration(
			float64(history.AvgRecoveryTime)*(1-alpha) + float64(recoveryTime)*alpha,
		)
	}
}

// GetHistory 获取 Pod 历史
func (ha *HistoryAnalyzer) GetHistory(namespace, name string) *PodHistory {
	ha.historiesMu.RLock()
	defer ha.historiesMu.RUnlock()

	for _, h := range ha.histories {
		if h.Namespace == namespace && h.PodName == name {
			return h
		}
	}
	return nil
}

// GetHistoryByUID 根据 UID 获取历史
func (ha *HistoryAnalyzer) GetHistoryByUID(podUID string) *PodHistory {
	ha.historiesMu.RLock()
	defer ha.historiesMu.RUnlock()
	return ha.histories[podUID]
}

// getOrCreateHistory 获取或创建历史记录
func (ha *HistoryAnalyzer) getOrCreateHistory(podUID string) *PodHistory {
	history, exists := ha.histories[podUID]
	if !exists {
		history = &PodHistory{
			PodUID:         podUID,
			ViolationTypes: make(map[string]int),
			ActionHistory:  make([]ActionRecord, 0),
			StabilityScore: 1.0, // 初始稳定
			UpdatedAt:      time.Now(),
		}
		ha.histories[podUID] = history
	}
	return history
}

// CalculateStabilityScore 计算稳定性评分
func (ha *HistoryAnalyzer) CalculateStabilityScore(podUID string) float64 {
	ha.historiesMu.RLock()
	history := ha.histories[podUID]
	ha.historiesMu.RUnlock()

	if history == nil {
		return 1.0 // 无历史默认稳定
	}

	score := 1.0

	// 违规次数影响
	if history.ViolationCount > 0 {
		score -= math.Min(float64(history.ViolationCount)*0.1, 0.5)
	}

	// 操作失败率影响
	if history.ActionCount > 0 {
		failRate := float64(history.FailedActions) / float64(history.ActionCount)
		score -= failRate * 0.3
	}

	// 最近违规时间影响
	if !history.LastViolation.IsZero() {
		hoursSince := time.Since(history.LastViolation).Hours()
		if hoursSince < 1 {
			score -= 0.2
		} else if hoursSince < 24 {
			score -= 0.1
		}
	}

	return math.Max(0, score)
}

// CleanupOldData 清理旧数据
func (ha *HistoryAnalyzer) CleanupOldData(retention time.Duration) {
	ha.historiesMu.Lock()
	defer ha.historiesMu.Unlock()

	cutoff := time.Now().Add(-retention)

	for podUID, history := range ha.histories {
		if history.UpdatedAt.Before(cutoff) {
			delete(ha.histories, podUID)
		}
	}
}

// GetAllHistories 获取所有历史
func (ha *HistoryAnalyzer) GetAllHistories() map[string]*PodHistory {
	ha.historiesMu.RLock()
	defer ha.historiesMu.RUnlock()

	result := make(map[string]*PodHistory, len(ha.histories))
	for k, v := range ha.histories {
		result[k] = v
	}
	return result
}
