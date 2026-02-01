// Package observation - 观察期管理器
package observation

import (
	"context"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// ObservationState 观察期状态
type ObservationState string

const (
	StateActive    ObservationState = "active"
	StateSucceeded ObservationState = "succeeded"
	StateFailed    ObservationState = "failed"
	StateEscalated ObservationState = "escalated"
)

// Observation 观察期
type Observation struct {
	ID          string
	QueueItemID string
	PodUID      string
	PodName     string
	Namespace   string
	ActionType  string

	// 时间
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration

	// 状态
	State          ObservationState
	EscalationLevel int

	// 成功条件检查结果
	CriteriaResults []CriteriaResult

	// 指标历史
	MetricHistory []MetricSnapshot
}

// CriteriaResult 条件检查结果
type CriteriaResult struct {
	Metric      string
	Threshold   float64
	Operator    string
	CurrentValue float64
	Satisfied   bool
	SatisfiedAt *time.Time
	Duration    time.Duration // 持续满足时间
}

// MetricSnapshot 指标快照
type MetricSnapshot struct {
	Timestamp time.Time
	Metrics   map[string]float64
}

// Manager 观察期管理器
type Manager struct {
	config config.ObservationConfig

	// 活跃的观察期
	observations   map[string]*Observation
	observationsMu sync.RWMutex

	// 指标提供函数
	metricsProvider func(podUID string) map[string]float64

	// 完成回调
	onComplete func(obs *Observation, success bool)

	// 控制
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewManager 创建观察期管理器
func NewManager(cfg config.ObservationConfig) *Manager {
	return &Manager{
		config:       cfg,
		observations: make(map[string]*Observation),
		stopCh:       make(chan struct{}),
	}
}

// SetMetricsProvider 设置指标提供函数
func (m *Manager) SetMetricsProvider(f func(podUID string) map[string]float64) {
	m.metricsProvider = f
}

// SetOnComplete 设置完成回调
func (m *Manager) SetOnComplete(f func(obs *Observation, success bool)) {
	m.onComplete = f
}

// Run 运行观察期检查循环
func (m *Manager) Run(ctx context.Context) {
	m.wg.Add(1)
	defer m.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	log.Info("Observation manager started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkObservations()
		}
	}
}

// Stop 停止管理器
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// Start 开始观察期
func (m *Manager) Start(queueItemID, podUID, podName, namespace, actionType string) string {
	m.observationsMu.Lock()
	defer m.observationsMu.Unlock()

	id := generateObsID()
	now := time.Now()

	obs := &Observation{
		ID:              id,
		QueueItemID:     queueItemID,
		PodUID:          podUID,
		PodName:         podName,
		Namespace:       namespace,
		ActionType:      actionType,
		StartTime:       now,
		EndTime:         now.Add(m.config.DefaultDuration),
		Duration:        m.config.DefaultDuration,
		State:           StateActive,
		EscalationLevel: 0,
		CriteriaResults: make([]CriteriaResult, len(m.config.SuccessCriteria)),
		MetricHistory:   make([]MetricSnapshot, 0),
	}

	// 初始化条件检查结果
	for i, criteria := range m.config.SuccessCriteria {
		obs.CriteriaResults[i] = CriteriaResult{
			Metric:    criteria.Metric,
			Threshold: criteria.Threshold,
			Operator:  criteria.Operator,
			Duration:  criteria.Duration,
		}
	}

	m.observations[id] = obs

	log.WithFields(log.Fields{
		"obs_id":   id,
		"pod":      podName,
		"duration": m.config.DefaultDuration,
	}).Info("Observation started")

	return id
}

// checkObservations 检查所有观察期
func (m *Manager) checkObservations() {
	m.observationsMu.Lock()
	defer m.observationsMu.Unlock()

	now := time.Now()

	for _, obs := range m.observations {
		if obs.State != StateActive {
			continue
		}

		// 收集指标
		if m.metricsProvider != nil {
			metrics := m.metricsProvider(obs.PodUID)
			obs.MetricHistory = append(obs.MetricHistory, MetricSnapshot{
				Timestamp: now,
				Metrics:   metrics,
			})

			// 检查成功条件
			m.checkCriteria(obs, metrics)
		}

		// 检查是否超时
		if now.After(obs.EndTime) {
			m.handleTimeout(obs)
		}
	}
}

// checkCriteria 检查成功条件
func (m *Manager) checkCriteria(obs *Observation, metrics map[string]float64) {
	allSatisfied := true
	now := time.Now()

	for i := range obs.CriteriaResults {
		result := &obs.CriteriaResults[i]
		value, ok := metrics[result.Metric]
		if !ok {
			allSatisfied = false
			continue
		}

		result.CurrentValue = value

		// 检查条件
		satisfied := false
		switch result.Operator {
		case "<":
			satisfied = value < result.Threshold
		case "<=":
			satisfied = value <= result.Threshold
		case ">":
			satisfied = value > result.Threshold
		case ">=":
			satisfied = value >= result.Threshold
		case "==":
			satisfied = value == result.Threshold
		}

		if satisfied {
			if result.SatisfiedAt == nil {
				t := now
				result.SatisfiedAt = &t
			}
			// 检查持续时间
			if now.Sub(*result.SatisfiedAt) >= result.Duration {
				result.Satisfied = true
			} else {
				allSatisfied = false
			}
		} else {
			result.SatisfiedAt = nil
			result.Satisfied = false
			allSatisfied = false
		}
	}

	// 如果所有条件都满足，观察期成功
	if allSatisfied {
		obs.State = StateSucceeded
		log.WithFields(log.Fields{
			"obs_id": obs.ID,
			"pod":    obs.PodName,
		}).Info("Observation succeeded")

		if m.onComplete != nil {
			m.onComplete(obs, true)
		}
	}
}

// handleTimeout 处理超时
func (m *Manager) handleTimeout(obs *Observation) {
	// 检查是否需要升级
	if obs.EscalationLevel < len(m.config.Escalation) {
		escalation := m.config.Escalation[obs.EscalationLevel]

		obs.EscalationLevel++
		obs.State = StateEscalated
		obs.EndTime = time.Now().Add(escalation.NextObservation)

		log.WithFields(log.Fields{
			"obs_id":           obs.ID,
			"pod":              obs.PodName,
			"escalation_level": obs.EscalationLevel,
			"action":           escalation.Action,
		}).Warn("Observation escalated")

		// 重置为活跃状态继续观察
		obs.State = StateActive
	} else {
		// 达到最大升级级别，观察期失败
		obs.State = StateFailed
		log.WithFields(log.Fields{
			"obs_id": obs.ID,
			"pod":    obs.PodName,
		}).Warn("Observation failed - max escalation reached")

		if m.onComplete != nil {
			m.onComplete(obs, false)
		}
	}
}

// Get 获取观察期
func (m *Manager) Get(id string) *Observation {
	m.observationsMu.RLock()
	defer m.observationsMu.RUnlock()
	return m.observations[id]
}

// GetAll 获取所有观察期
func (m *Manager) GetAll() []*Observation {
	m.observationsMu.RLock()
	defer m.observationsMu.RUnlock()

	result := make([]*Observation, 0, len(m.observations))
	for _, obs := range m.observations {
		result = append(result, obs)
	}
	return result
}

// GetActive 获取活跃的观察期
func (m *Manager) GetActive() []*Observation {
	m.observationsMu.RLock()
	defer m.observationsMu.RUnlock()

	result := make([]*Observation, 0)
	for _, obs := range m.observations {
		if obs.State == StateActive {
			result = append(result, obs)
		}
	}
	return result
}

// Cancel 取消观察期
func (m *Manager) Cancel(id string) {
	m.observationsMu.Lock()
	defer m.observationsMu.Unlock()

	if obs, ok := m.observations[id]; ok {
		obs.State = StateFailed
		log.WithFields(log.Fields{
			"obs_id": obs.ID,
			"pod":    obs.PodName,
		}).Info("Observation cancelled")
	}
}

// Cleanup 清理已完成的观察期
func (m *Manager) Cleanup(retention time.Duration) {
	m.observationsMu.Lock()
	defer m.observationsMu.Unlock()

	cutoff := time.Now().Add(-retention)

	for id, obs := range m.observations {
		if obs.State != StateActive && obs.StartTime.Before(cutoff) {
			delete(m.observations, id)
		}
	}
}

// generateObsID 生成观察期 ID
func generateObsID() string {
	return "obs-" + time.Now().Format("20060102150405.000")
}
