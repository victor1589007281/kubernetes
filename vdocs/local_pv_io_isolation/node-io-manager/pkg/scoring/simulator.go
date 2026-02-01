// Package scoring - 操作效果模拟器
package scoring

import (
	"math"

	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/profile"
)

// ActionSimulator 操作效果模拟器
type ActionSimulator struct {
	// 历史操作效果数据
	historicalEffects map[string]*EffectHistory
}

// EffectHistory 历史效果数据
type EffectHistory struct {
	ActionType     string
	TotalExecutions int
	SuccessfulExecutions int
	AvgIOPSReduction float64
	AvgBPSReduction  float64
	AvgRecoveryTime  float64 // 秒
}

// CandidateAction 候选操作
type CandidateAction struct {
	Type        ActionType
	TargetPod   string
	Cost        float64 // 业务影响成本 (0-100)
	Benefit     float64 // IO 释放收益 (0-100)
	Confidence  float64 // 效果置信度 (0-1)
}

// NewActionSimulator 创建模拟器
func NewActionSimulator() *ActionSimulator {
	return &ActionSimulator{
		historicalEffects: make(map[string]*EffectHistory),
	}
}

// EstimateEffect 估计操作效果
func (s *ActionSimulator) EstimateEffect(metrics *collector.PodMetrics, prof *profile.IOProfile) float64 {
	if metrics == nil {
		return 0
	}

	score := 0.0

	// 基于 IO 占比的效果估计
	// IO 占比越高，操作效果越明显
	score += metrics.IOPSPercent * 0.4
	score += metrics.BPSPercent * 0.3

	// 基于行为特征的效果估计
	if prof != nil {
		// 高突发性 Pod 限制效果好
		if prof.BurstScore > 1.0 {
			score += math.Min(prof.BurstScore*10, 20)
		}

		// 高波动性 Pod 限制效果好
		if prof.Volatility > 0.5 {
			score += math.Min(prof.Volatility*20, 10)
		}
	}

	return math.Min(score, 100)
}

// SimulateAction 模拟操作影响
func (s *ActionSimulator) SimulateAction(action CandidateAction, metrics *collector.PodMetrics) SimulationResult {
	result := SimulationResult{
		Action:    action,
		TargetPod: action.TargetPod,
	}

	if metrics == nil {
		return result
	}

	// 根据操作类型估计 IO 释放量
	var reductionFactor float64
	switch action.Type {
	case ActionThrottle10:
		reductionFactor = 0.10
	case ActionThrottle25:
		reductionFactor = 0.25
	case ActionThrottle50:
		reductionFactor = 0.50
	case ActionEvict:
		reductionFactor = 1.0
	default:
		reductionFactor = 0
	}

	result.ExpectedIOPSRelief = metrics.TotalIOPS * reductionFactor
	result.ExpectedBPSRelief = metrics.TotalBPS * reductionFactor

	// 估计对受害者的恢复概率
	// IO 释放越多，恢复概率越高
	result.VictimRecoveryProb = math.Min(reductionFactor*2, 0.9)

	// 业务影响评估
	// 限制越大，业务影响越大
	result.BusinessImpact = reductionFactor * 100

	// 综合效率评分
	result.EfficiencyScore = (result.ExpectedIOPSRelief/1000 + result.VictimRecoveryProb*50) / (result.BusinessImpact/100 + 0.1)

	return result
}

// SimulationResult 模拟结果
type SimulationResult struct {
	Action             CandidateAction
	TargetPod          string
	ExpectedIOPSRelief float64 // 预期释放的 IOPS
	ExpectedBPSRelief  float64 // 预期释放的带宽
	VictimRecoveryProb float64 // 受害者恢复概率
	BusinessImpact     float64 // 业务影响 (0-100)
	EfficiencyScore    float64 // 效率评分
}

// FindOptimalActions 找到最优操作组合
func (s *ActionSimulator) FindOptimalActions(
	pods map[string]*collector.PodMetrics,
	profiles map[string]*profile.IOProfile,
	targetRelief float64, // 目标 IO 释放量
	protectedPods map[string]bool, // 受保护的 Pod
) []CandidateAction {
	candidates := make([]CandidateAction, 0)

	// 为每个 Pod 生成候选操作
	for podUID, metrics := range pods {
		// 跳过受保护的 Pod
		if protectedPods[podUID] {
			continue
		}

		prof := profiles[podUID]

		// 生成不同强度的操作候选
		for _, actionType := range []ActionType{ActionThrottle10, ActionThrottle25, ActionThrottle50} {
			action := CandidateAction{
				Type:      actionType,
				TargetPod: podUID,
			}

			// 计算成本和收益
			simulation := s.SimulateAction(action, metrics)
			action.Cost = simulation.BusinessImpact
			action.Benefit = simulation.ExpectedIOPSRelief + simulation.VictimRecoveryProb*100
			action.Confidence = s.estimateConfidence(podUID, actionType, prof)

			candidates = append(candidates, action)
		}
	}

	// 使用贪心算法选择最优组合
	return s.greedySelect(candidates, targetRelief)
}

// greedySelect 贪心选择最优操作组合
func (s *ActionSimulator) greedySelect(candidates []CandidateAction, targetRelief float64) []CandidateAction {
	// 按效率评分排序 (收益/成本)
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			effI := (candidates[i].Benefit * candidates[i].Confidence) / (candidates[i].Cost + 0.1)
			effJ := (candidates[j].Benefit * candidates[j].Confidence) / (candidates[j].Cost + 0.1)
			if effJ > effI {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// 贪心选择
	selected := make([]CandidateAction, 0)
	totalRelief := 0.0
	selectedPods := make(map[string]bool)

	for _, action := range candidates {
		// 每个 Pod 只选择一个操作
		if selectedPods[action.TargetPod] {
			continue
		}

		// 检查是否达到目标
		if totalRelief >= targetRelief {
			break
		}

		selected = append(selected, action)
		selectedPods[action.TargetPod] = true
		totalRelief += action.Benefit
	}

	return selected
}

// estimateConfidence 估计置信度
func (s *ActionSimulator) estimateConfidence(podUID string, actionType ActionType, prof *profile.IOProfile) float64 {
	confidence := 0.5 // 基础置信度

	// 历史数据
	key := podUID + "_" + string(actionType)
	if history, ok := s.historicalEffects[key]; ok && history.TotalExecutions > 0 {
		successRate := float64(history.SuccessfulExecutions) / float64(history.TotalExecutions)
		confidence += successRate * 0.3
	}

	// 画像数据
	if prof != nil && prof.SampleCount > 30 {
		confidence += 0.2
	}

	return math.Min(confidence, 1.0)
}

// RecordEffect 记录实际效果
func (s *ActionSimulator) RecordEffect(podUID string, actionType ActionType, success bool, iopsReduction, bpsReduction float64) {
	key := podUID + "_" + string(actionType)

	history, exists := s.historicalEffects[key]
	if !exists {
		history = &EffectHistory{ActionType: string(actionType)}
		s.historicalEffects[key] = history
	}

	history.TotalExecutions++
	if success {
		history.SuccessfulExecutions++

		// 更新平均效果 (指数移动平均)
		alpha := 0.3
		history.AvgIOPSReduction = history.AvgIOPSReduction*(1-alpha) + iopsReduction*alpha
		history.AvgBPSReduction = history.AvgBPSReduction*(1-alpha) + bpsReduction*alpha
	}
}

// CalculateEfficiencyScore 计算效率评分
func (a *CandidateAction) CalculateEfficiencyScore() float64 {
	return (a.Benefit * a.Confidence) / (a.Cost + 0.1) // 避免除零
}
