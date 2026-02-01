// Package scoring - 动态评分引擎
package scoring

import (
	"math"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/profile"
)

// ActionType 操作类型
type ActionType string

const (
	ActionNone         ActionType = "none"
	ActionThrottle10   ActionType = "throttle_10"
	ActionThrottle25   ActionType = "throttle_25"
	ActionThrottle50   ActionType = "throttle_50"
	ActionEvict        ActionType = "evict"
	ActionAlert        ActionType = "alert"
)

// PodOperationScore Pod 操作评分
type PodOperationScore struct {
	PodUID    string
	PodName   string
	Namespace string

	// 各因子得分 (0-100)
	BusinessScore float64 // 业务重要性 (越高越不应操作)
	HistoryScore  float64 // 历史行为 (越高越可能再犯)
	EffectScore   float64 // 操作效果 (越高效果越好)
	ImpactScore   float64 // 当前影响 (越高影响越大)

	// 动态权重
	Weights map[string]float64

	// 最终评分 (越高越应该操作)
	FinalScore  float64
	Confidence  float64

	// 推荐操作
	RecommendedAction ActionType
	ExpectedBenefit   float64

	// 元数据
	CalculatedAt time.Time
	ValidUntil   time.Time
}

// Engine 评分引擎
type Engine struct {
	config config.ScoringConfig

	// 当前评分
	scores   map[string]*PodOperationScore
	scoresMu sync.RWMutex

	// 动态权重
	weights   *WeightAdjuster
	weightsMu sync.RWMutex

	// 历史分析器
	historyAnalyzer *HistoryAnalyzer

	// 效果模拟器
	simulator *ActionSimulator

	// 业务优先级配置
	businessPriority *BusinessPriorityConfig
}

// NewEngine 创建评分引擎
func NewEngine(cfg config.ScoringConfig) *Engine {
	return &Engine{
		config: cfg,
		scores: make(map[string]*PodOperationScore),
		weights: NewWeightAdjuster(map[string]float64{
			"business":  cfg.Weights.BusinessImportance,
			"history":   cfg.Weights.HistoryBehavior,
			"effect":    cfg.Weights.ActionEffect,
			"impact":    cfg.Weights.CurrentImpact,
		}),
		historyAnalyzer:  NewHistoryAnalyzer(cfg),
		simulator:        NewActionSimulator(),
		businessPriority: NewBusinessPriorityConfig(),
	}
}

// CalculateScores 计算所有 Pod 的评分
func (e *Engine) CalculateScores(podMetrics map[string]*collector.PodMetrics, profiles map[string]*profile.IOProfile) []*PodOperationScore {
	e.scoresMu.Lock()
	defer e.scoresMu.Unlock()

	e.weightsMu.RLock()
	weights := e.weights.GetWeights()
	e.weightsMu.RUnlock()

	scores := make([]*PodOperationScore, 0, len(podMetrics))
	now := time.Now()

	for podUID, metrics := range podMetrics {
		prof := profiles[podUID]

		score := &PodOperationScore{
			PodUID:       podUID,
			PodName:      metrics.PodName,
			Namespace:    metrics.Namespace,
			Weights:      weights,
			CalculatedAt: now,
			ValidUntil:   now.Add(time.Duration(e.config.IntervalSeconds*2) * time.Second),
		}

		// 计算各因子得分

		// 1. 业务重要性评分 (越高越不应操作)
		score.BusinessScore = e.calculateBusinessScore(metrics.Namespace, metrics.PodName)

		// 2. 历史行为评分 (越高越可能再犯)
		score.HistoryScore = e.historyAnalyzer.CalculateRecidivismScore(podUID)

		// 3. 操作效果评分 (越高效果越好)
		score.EffectScore = e.simulator.EstimateEffect(metrics, prof)

		// 4. 当前影响评分 (越高影响越大)
		score.ImpactScore = e.calculateImpactScore(metrics, prof)

		// 计算最终评分
		// 公式: (100 - BusinessScore) * w1 + HistoryScore * w2 + EffectScore * w3 + ImpactScore * w4
		score.FinalScore = (100-score.BusinessScore)*weights["business"] +
			score.HistoryScore*weights["history"] +
			score.EffectScore*weights["effect"] +
			score.ImpactScore*weights["impact"]

		// 置信度
		score.Confidence = e.calculateConfidence(metrics, prof)

		// 推荐操作
		score.RecommendedAction = e.recommendAction(score)
		score.ExpectedBenefit = score.EffectScore * score.Confidence / 100

		scores = append(scores, score)
		e.scores[podUID] = score
	}

	return scores
}

// calculateBusinessScore 计算业务重要性评分
func (e *Engine) calculateBusinessScore(namespace, podName string) float64 {
	priority := e.businessPriority.GetPriority(namespace, podName)
	return float64(priority) // 0-100
}

// calculateImpactScore 计算当前影响评分
func (e *Engine) calculateImpactScore(metrics *collector.PodMetrics, prof *profile.IOProfile) float64 {
	score := 0.0

	// IO 占比 (0-50分)
	score += math.Min(metrics.IOPSPercent, 50)

	// 带宽占比 (0-30分)
	score += math.Min(metrics.BPSPercent*0.6, 30)

	// D 状态进程 (每个 10 分，最多 20 分)
	if metrics.HasDStateProc {
		score += math.Min(float64(metrics.DStateProcCount)*10, 20)
	}

	return math.Min(score, 100)
}

// calculateConfidence 计算置信度
func (e *Engine) calculateConfidence(metrics *collector.PodMetrics, prof *profile.IOProfile) float64 {
	confidence := 0.5 // 基础置信度

	// 数据新鲜度
	age := time.Since(metrics.CollectedAt)
	if age < 10*time.Second {
		confidence += 0.2
	} else if age < 30*time.Second {
		confidence += 0.1
	}

	// 画像数据量
	if prof != nil && prof.SampleCount > 30 {
		confidence += 0.2
	} else if prof != nil && prof.SampleCount > 10 {
		confidence += 0.1
	}

	return math.Min(confidence, 1.0)
}

// recommendAction 推荐操作
func (e *Engine) recommendAction(score *PodOperationScore) ActionType {
	// 关键业务不操作
	if score.BusinessScore >= 90 {
		return ActionNone
	}

	// 根据最终评分推荐操作
	switch {
	case score.FinalScore >= 80:
		if score.BusinessScore < 50 {
			return ActionThrottle50
		}
		return ActionThrottle25
	case score.FinalScore >= 60:
		return ActionThrottle25
	case score.FinalScore >= 40:
		return ActionThrottle10
	case score.FinalScore >= 20:
		return ActionAlert
	default:
		return ActionNone
	}
}

// GetScores 获取所有评分
func (e *Engine) GetScores() []*PodOperationScore {
	e.scoresMu.RLock()
	defer e.scoresMu.RUnlock()

	scores := make([]*PodOperationScore, 0, len(e.scores))
	for _, s := range e.scores {
		scores = append(scores, s)
	}
	return scores
}

// GetPodScore 获取指定 Pod 的评分
func (e *Engine) GetPodScore(namespace, name string) *PodOperationScore {
	e.scoresMu.RLock()
	defer e.scoresMu.RUnlock()

	for _, s := range e.scores {
		if s.Namespace == namespace && s.PodName == name {
			return s
		}
	}
	return nil
}

// GetFactors 获取评分因子配置
func (e *Engine) GetFactors() map[string]interface{} {
	return map[string]interface{}{
		"business": map[string]interface{}{
			"description": "业务重要性 (越高越不应操作)",
			"range":       "0-100",
			"source":      "外部配置/K8s标签",
		},
		"history": map[string]interface{}{
			"description": "历史行为 (越高越可能再犯)",
			"range":       "0-100",
			"source":      "历史数据库",
		},
		"effect": map[string]interface{}{
			"description": "操作效果 (越高效果越好)",
			"range":       "0-100",
			"source":      "模拟器",
		},
		"impact": map[string]interface{}{
			"description": "当前影响 (越高影响越大)",
			"range":       "0-100",
			"source":      "实时分析",
		},
	}
}

// GetWeights 获取当前权重
func (e *Engine) GetWeights() map[string]float64 {
	e.weightsMu.RLock()
	defer e.weightsMu.RUnlock()
	return e.weights.GetWeights()
}

// UpdateWeights 更新权重
func (e *Engine) UpdateWeights(weights map[string]float64) error {
	e.weightsMu.Lock()
	defer e.weightsMu.Unlock()
	return e.weights.Update(weights)
}

// GetPodHistory 获取 Pod 历史数据
func (e *Engine) GetPodHistory(namespace, name string) *PodHistory {
	return e.historyAnalyzer.GetHistory(namespace, name)
}

// RecordViolation 记录违规
func (e *Engine) RecordViolation(podUID string, violationType string) {
	e.historyAnalyzer.RecordViolation(podUID, violationType)
}

// RecordActionResult 记录操作结果
func (e *Engine) RecordActionResult(podUID string, action ActionType, success bool) {
	e.historyAnalyzer.RecordActionResult(podUID, string(action), success)

	// 更新权重
	e.weightsMu.Lock()
	e.weights.UpdateFromFeedback(string(action), success)
	e.weightsMu.Unlock()
}
