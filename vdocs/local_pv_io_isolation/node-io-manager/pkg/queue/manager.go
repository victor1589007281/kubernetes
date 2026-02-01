// Package queue - 决策队列管理器
package queue

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/scoring"
	log "github.com/sirupsen/logrus"
)

// QueueStatus 队列状态
type QueueStatus string

const (
	StatusPending   QueueStatus = "pending"
	StatusExecuting QueueStatus = "executing"
	StatusObserving QueueStatus = "observing"
	StatusCompleted QueueStatus = "completed"
	StatusEscalated QueueStatus = "escalated"
	StatusCancelled QueueStatus = "cancelled"
)

// DecisionQueueItem 决策队列项
type DecisionQueueItem struct {
	ID       string
	PodScore *scoring.PodOperationScore
	Action   *PlannedAction

	// 队列状态
	Status   QueueStatus
	Priority int // 动态优先级

	// 时间追踪
	EnqueuedAt     time.Time
	ExecutedAt     *time.Time
	ObservationEnd *time.Time

	// 动态调整记录
	ScoreHistory  []ScoreSnapshot
	StatusHistory []StatusChange

	// 观察期数据
	ObservationID string
}

// PlannedAction 计划执行的操作
type PlannedAction struct {
	Type       scoring.ActionType
	TargetPod  string
	Namespace  string
	Parameters map[string]interface{}
}

// ScoreSnapshot 评分快照
type ScoreSnapshot struct {
	Timestamp time.Time
	Score     float64
	Reason    string
}

// StatusChange 状态变更记录
type StatusChange struct {
	Timestamp time.Time
	From      QueueStatus
	To        QueueStatus
	Reason    string
}

// Manager 决策队列管理器
type Manager struct {
	config config.QueueConfig

	// 队列
	items   map[string]*DecisionQueueItem
	itemsMu sync.RWMutex

	// 执行回调
	executeFunc func(item *DecisionQueueItem) error

	// 控制
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewManager 创建队列管理器
func NewManager(cfg config.QueueConfig) *Manager {
	return &Manager{
		config: cfg,
		items:  make(map[string]*DecisionQueueItem),
		stopCh: make(chan struct{}),
	}
}

// SetExecuteFunc 设置执行函数
func (m *Manager) SetExecuteFunc(f func(item *DecisionQueueItem) error) {
	m.executeFunc = f
}

// Run 运行队列处理循环
func (m *Manager) Run(ctx context.Context) {
	m.wg.Add(1)
	defer m.wg.Done()

	ticker := time.NewTicker(time.Duration(m.config.ProcessIntervalSec) * time.Second)
	defer ticker.Stop()

	decayTicker := time.NewTicker(time.Duration(m.config.ScoreDecayMinutes) * time.Minute)
	defer decayTicker.Stop()

	log.Info("Decision queue started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.processQueue()
		case <-decayTicker.C:
			m.applyScoreDecay()
		}
	}
}

// Stop 停止队列
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// processQueue 处理队列
func (m *Manager) processQueue() {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	// 获取待执行项（按优先级排序）
	var pendingItems []*DecisionQueueItem
	for _, item := range m.items {
		if item.Status == StatusPending {
			pendingItems = append(pendingItems, item)
		}
	}

	// 按优先级排序
	for i := 0; i < len(pendingItems); i++ {
		for j := i + 1; j < len(pendingItems); j++ {
			if pendingItems[j].Priority > pendingItems[i].Priority {
				pendingItems[i], pendingItems[j] = pendingItems[j], pendingItems[i]
			}
		}
	}

	// 执行优先级最高的项
	if len(pendingItems) > 0 {
		item := pendingItems[0]
		m.executeItem(item)
	}
}

// executeItem 执行队列项
func (m *Manager) executeItem(item *DecisionQueueItem) {
	if m.executeFunc == nil {
		log.Warn("Execute function not set")
		return
	}

	// 更新状态
	m.changeStatus(item, StatusExecuting, "Starting execution")

	now := time.Now()
	item.ExecutedAt = &now

	// 执行操作
	go func() {
		err := m.executeFunc(item)
		if err != nil {
			log.Errorf("Failed to execute action for %s: %v", item.ID, err)
			m.itemsMu.Lock()
			m.changeStatus(item, StatusEscalated, err.Error())
			m.itemsMu.Unlock()
		} else {
			m.itemsMu.Lock()
			m.changeStatus(item, StatusObserving, "Entering observation period")
			m.itemsMu.Unlock()
		}
	}()
}

// changeStatus 变更状态
func (m *Manager) changeStatus(item *DecisionQueueItem, newStatus QueueStatus, reason string) {
	oldStatus := item.Status
	item.Status = newStatus

	item.StatusHistory = append(item.StatusHistory, StatusChange{
		Timestamp: time.Now(),
		From:      oldStatus,
		To:        newStatus,
		Reason:    reason,
	})

	log.WithFields(log.Fields{
		"item_id":    item.ID,
		"from":       oldStatus,
		"to":         newStatus,
		"reason":     reason,
	}).Debug("Queue item status changed")
}

// applyScoreDecay 应用分数衰减
func (m *Manager) applyScoreDecay() {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	decayFactor := 0.9 // 每次衰减 10%

	for _, item := range m.items {
		if item.Status == StatusPending {
			oldScore := item.PodScore.FinalScore
			item.PodScore.FinalScore *= decayFactor
			item.Priority = int(item.PodScore.FinalScore)

			item.ScoreHistory = append(item.ScoreHistory, ScoreSnapshot{
				Timestamp: time.Now(),
				Score:     item.PodScore.FinalScore,
				Reason:    "time_decay",
			})

			log.Debugf("Score decay: %s %.2f -> %.2f", item.ID, oldScore, item.PodScore.FinalScore)
		}
	}
}

// Enqueue 添加到队列
func (m *Manager) Enqueue(score *scoring.PodOperationScore, action *PlannedAction) (string, error) {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	// 检查队列大小
	pendingCount := 0
	for _, item := range m.items {
		if item.Status == StatusPending {
			pendingCount++
		}
	}

	if pendingCount >= m.config.MaxPendingItems {
		return "", errors.New("queue is full")
	}

	// 检查是否已存在相同 Pod 的操作
	for _, item := range m.items {
		if item.PodScore.PodUID == score.PodUID && item.Status == StatusPending {
			// 更新现有项
			if score.FinalScore > item.PodScore.FinalScore {
				item.PodScore = score
				item.Action = action
				item.Priority = int(score.FinalScore)
				item.ScoreHistory = append(item.ScoreHistory, ScoreSnapshot{
					Timestamp: time.Now(),
					Score:     score.FinalScore,
					Reason:    "score_update",
				})
			}
			return item.ID, nil
		}
	}

	// 创建新项
	id := generateID()
	item := &DecisionQueueItem{
		ID:         id,
		PodScore:   score,
		Action:     action,
		Status:     StatusPending,
		Priority:   int(score.FinalScore),
		EnqueuedAt: time.Now(),
		ScoreHistory: []ScoreSnapshot{{
			Timestamp: time.Now(),
			Score:     score.FinalScore,
			Reason:    "initial",
		}},
		StatusHistory: []StatusChange{{
			Timestamp: time.Now(),
			From:      "",
			To:        StatusPending,
			Reason:    "enqueued",
		}},
	}

	m.items[id] = item

	log.WithFields(log.Fields{
		"item_id":  id,
		"pod":      score.PodName,
		"score":    score.FinalScore,
		"action":   action.Type,
	}).Info("Item enqueued")

	return id, nil
}

// Cancel 取消队列项
func (m *Manager) Cancel(id string) error {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	item, exists := m.items[id]
	if !exists {
		return errors.New("item not found")
	}

	if item.Status != StatusPending {
		return errors.New("can only cancel pending items")
	}

	m.changeStatus(item, StatusCancelled, "manually cancelled")
	return nil
}

// ExecuteNow 立即执行
func (m *Manager) ExecuteNow(id string) error {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	item, exists := m.items[id]
	if !exists {
		return errors.New("item not found")
	}

	if item.Status != StatusPending {
		return errors.New("can only execute pending items")
	}

	m.executeItem(item)
	return nil
}

// UpdateScores 更新评分
func (m *Manager) UpdateScores(scores []*scoring.PodOperationScore) {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	scoreMap := make(map[string]*scoring.PodOperationScore)
	for _, s := range scores {
		scoreMap[s.PodUID] = s
	}

	for _, item := range m.items {
		if item.Status == StatusPending {
			if newScore, ok := scoreMap[item.PodScore.PodUID]; ok {
				// 更新评分
				item.PodScore = newScore
				item.Priority = int(newScore.FinalScore)

				item.ScoreHistory = append(item.ScoreHistory, ScoreSnapshot{
					Timestamp: time.Now(),
					Score:     newScore.FinalScore,
					Reason:    "periodic_update",
				})
			}
		}
	}
}

// MarkObservationComplete 标记观察完成
func (m *Manager) MarkObservationComplete(id string, success bool) {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	item, exists := m.items[id]
	if !exists {
		return
	}

	if item.Status != StatusObserving {
		return
	}

	if success {
		m.changeStatus(item, StatusCompleted, "observation succeeded")
	} else {
		m.changeStatus(item, StatusEscalated, "observation failed")
	}
}

// GetItems 获取所有队列项
func (m *Manager) GetItems() []*DecisionQueueItem {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	result := make([]*DecisionQueueItem, 0, len(m.items))
	for _, item := range m.items {
		result = append(result, item)
	}
	return result
}

// GetPendingCount 获取待执行数量
func (m *Manager) GetPendingCount() int {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	count := 0
	for _, item := range m.items {
		if item.Status == StatusPending {
			count++
		}
	}
	return count
}

// GetObservingCount 获取观察中数量
func (m *Manager) GetObservingCount() int {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	count := 0
	for _, item := range m.items {
		if item.Status == StatusObserving {
			count++
		}
	}
	return count
}

// Cleanup 清理已完成的项
func (m *Manager) Cleanup(retention time.Duration) {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	cutoff := time.Now().Add(-retention)

	for id, item := range m.items {
		if (item.Status == StatusCompleted || item.Status == StatusCancelled) &&
			item.EnqueuedAt.Before(cutoff) {
			delete(m.items, id)
		}
	}
}

// generateID 生成唯一 ID
func generateID() string {
	return time.Now().Format("20060102150405.000")
}
