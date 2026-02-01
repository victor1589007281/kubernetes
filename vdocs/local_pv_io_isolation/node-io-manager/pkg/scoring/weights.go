// Package scoring - 动态权重调整器
package scoring

import (
	"fmt"
	"math"
	"sync"
)

// WeightAdjuster 权重自适应调整器
type WeightAdjuster struct {
	// 基础权重
	baseWeights map[string]float64

	// 当前权重
	currentWeights map[string]float64

	// 历史准确率
	accuracy map[string]*accuracyTracker

	// 衰减因子
	decayFactor float64

	mu sync.RWMutex
}

// accuracyTracker 准确率追踪器
type accuracyTracker struct {
	TotalActions     int
	SuccessfulActions int
	Accuracy         float64
}

// NewWeightAdjuster 创建权重调整器
func NewWeightAdjuster(baseWeights map[string]float64) *WeightAdjuster {
	wa := &WeightAdjuster{
		baseWeights:    make(map[string]float64),
		currentWeights: make(map[string]float64),
		accuracy:       make(map[string]*accuracyTracker),
		decayFactor:    0.95,
	}

	// 复制并归一化基础权重
	total := 0.0
	for _, w := range baseWeights {
		total += w
	}

	for k, w := range baseWeights {
		normalized := w / total
		wa.baseWeights[k] = normalized
		wa.currentWeights[k] = normalized
		wa.accuracy[k] = &accuracyTracker{Accuracy: 0.5} // 初始准确率 50%
	}

	return wa
}

// GetWeights 获取当前权重
func (wa *WeightAdjuster) GetWeights() map[string]float64 {
	wa.mu.RLock()
	defer wa.mu.RUnlock()

	result := make(map[string]float64, len(wa.currentWeights))
	for k, v := range wa.currentWeights {
		result[k] = v
	}
	return result
}

// ComputeDynamicWeight 计算动态权重
func (wa *WeightAdjuster) ComputeDynamicWeight(factor string) float64 {
	wa.mu.RLock()
	defer wa.mu.RUnlock()

	base := wa.baseWeights[factor]
	acc := wa.accuracy[factor]
	if acc == nil {
		return base
	}

	// 根据历史准确率调整权重
	// 准确率高的因子权重增加，低的降低
	adjusted := base * (0.5 + 0.5*acc.Accuracy)

	return adjusted
}

// Update 手动更新权重
func (wa *WeightAdjuster) Update(weights map[string]float64) error {
	wa.mu.Lock()
	defer wa.mu.Unlock()

	// 验证权重
	total := 0.0
	for _, w := range weights {
		if w < 0 || w > 1 {
			return fmt.Errorf("weight must be between 0 and 1")
		}
		total += w
	}

	// 归一化
	for k, w := range weights {
		if _, exists := wa.currentWeights[k]; exists {
			wa.currentWeights[k] = w / total
		}
	}

	return nil
}

// UpdateFromFeedback 根据反馈更新权重
func (wa *WeightAdjuster) UpdateFromFeedback(action string, success bool) {
	wa.mu.Lock()
	defer wa.mu.Unlock()

	// 更新所有因子的准确率
	for _, acc := range wa.accuracy {
		acc.TotalActions++
		if success {
			acc.SuccessfulActions++
		}

		// 计算新准确率 (指数移动平均)
		if acc.TotalActions > 0 {
			newAccuracy := float64(acc.SuccessfulActions) / float64(acc.TotalActions)
			acc.Accuracy = acc.Accuracy*wa.decayFactor + newAccuracy*(1-wa.decayFactor)
		}
	}

	// 重新计算权重
	wa.recalculateWeights()
}

// recalculateWeights 重新计算权重
func (wa *WeightAdjuster) recalculateWeights() {
	// 计算调整后的权重
	adjusted := make(map[string]float64)
	total := 0.0

	for k, base := range wa.baseWeights {
		acc := wa.accuracy[k]
		if acc == nil {
			adjusted[k] = base
		} else {
			// 根据准确率调整
			adjusted[k] = base * (0.5 + 0.5*acc.Accuracy)
		}
		total += adjusted[k]
	}

	// 归一化
	for k, v := range adjusted {
		wa.currentWeights[k] = v / total
	}
}

// GetAccuracy 获取因子准确率
func (wa *WeightAdjuster) GetAccuracy() map[string]float64 {
	wa.mu.RLock()
	defer wa.mu.RUnlock()

	result := make(map[string]float64, len(wa.accuracy))
	for k, acc := range wa.accuracy {
		result[k] = acc.Accuracy
	}
	return result
}

// Reset 重置到基础权重
func (wa *WeightAdjuster) Reset() {
	wa.mu.Lock()
	defer wa.mu.Unlock()

	for k, v := range wa.baseWeights {
		wa.currentWeights[k] = v
		wa.accuracy[k] = &accuracyTracker{Accuracy: 0.5}
	}
}

// Normalize 归一化权重
func (wa *WeightAdjuster) Normalize(adjusted float64) float64 {
	wa.mu.RLock()
	defer wa.mu.RUnlock()

	total := 0.0
	for _, w := range wa.currentWeights {
		total += w
	}

	if total == 0 {
		return adjusted
	}

	return adjusted / total
}

// WeightedScore 计算加权评分
func WeightedScore(scores, weights map[string]float64) float64 {
	total := 0.0
	weightSum := 0.0

	for k, score := range scores {
		if weight, ok := weights[k]; ok {
			total += score * weight
			weightSum += weight
		}
	}

	if weightSum == 0 {
		return 0
	}

	return total / weightSum
}

// ConfidenceAdjustedScore 置信度调整后的评分
func ConfidenceAdjustedScore(score, confidence float64) float64 {
	// 低置信度时向均值回归
	meanScore := 50.0
	return score*confidence + meanScore*(1-confidence)
}

// DynamicThreshold 动态阈值
type DynamicThreshold struct {
	BaseValue   float64
	CurrentValue float64
	Sensitivity  float64 // 0-1, 越高越敏感
	mu          sync.RWMutex
}

// NewDynamicThreshold 创建动态阈值
func NewDynamicThreshold(base float64, sensitivity float64) *DynamicThreshold {
	return &DynamicThreshold{
		BaseValue:    base,
		CurrentValue: base,
		Sensitivity:  math.Max(0, math.Min(1, sensitivity)),
	}
}

// Update 根据误报/漏报调整阈值
func (dt *DynamicThreshold) Update(falsePositive, falseNegative bool) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	adjustment := dt.BaseValue * 0.05 * dt.Sensitivity

	if falsePositive {
		// 误报太多，提高阈值
		dt.CurrentValue += adjustment
	}

	if falseNegative {
		// 漏报太多，降低阈值
		dt.CurrentValue -= adjustment
	}

	// 限制范围
	minThreshold := dt.BaseValue * 0.5
	maxThreshold := dt.BaseValue * 2.0
	dt.CurrentValue = math.Max(minThreshold, math.Min(maxThreshold, dt.CurrentValue))
}

// Get 获取当前阈值
func (dt *DynamicThreshold) Get() float64 {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	return dt.CurrentValue
}
