// Package scoring - 评分引擎测试
package scoring

import (
	"testing"
	"time"

	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/profile"
)

func TestNewEngine(t *testing.T) {
	cfg := config.ScoringConfig{
		IntervalSeconds: 10,
		Weights: config.WeightsConfig{
			BusinessImportance: 0.3,
			HistoryBehavior:    0.2,
			ActionEffect:       0.3,
			CurrentImpact:      0.2,
		},
		RecidivismBaseRate:   0.1,
		RecidivismDecayHours: 24,
	}

	engine := NewEngine(cfg)

	if engine == nil {
		t.Fatal("Engine should not be nil")
	}

	if engine.config.IntervalSeconds != 10 {
		t.Errorf("Expected interval 10, got %d", engine.config.IntervalSeconds)
	}
}

func TestCalculateScores(t *testing.T) {
	cfg := config.ScoringConfig{
		IntervalSeconds: 10,
		Weights: config.WeightsConfig{
			BusinessImportance: 0.3,
			HistoryBehavior:    0.2,
			ActionEffect:       0.3,
			CurrentImpact:      0.2,
		},
		RecidivismBaseRate:   0.1,
		RecidivismDecayHours: 24,
	}

	engine := NewEngine(cfg)

	// 模拟 Pod 指标
	podMetrics := map[string]*collector.PodMetrics{
		"pod-1": {
			PodUID:      "pod-1",
			PodName:     "test-pod-1",
			Namespace:   "default",
			TotalIOPS:   5000,
			TotalBPS:    100 * 1024 * 1024,
			IOPSPercent: 50,
			BPSPercent:  40,
			CollectedAt: time.Now(),
		},
		"pod-2": {
			PodUID:      "pod-2",
			PodName:     "test-pod-2",
			Namespace:   "production",
			TotalIOPS:   1000,
			TotalBPS:    20 * 1024 * 1024,
			IOPSPercent: 10,
			BPSPercent:  8,
			CollectedAt: time.Now(),
		},
	}

	// 模拟画像
	profiles := map[string]*profile.IOProfile{
		"pod-1": {
			PodUID:      "pod-1",
			PodName:     "test-pod-1",
			Namespace:   "default",
			AvgIOPS:     5000,
			BurstScore:  1.5,
			Volatility:  0.3,
			SampleCount: 50,
		},
		"pod-2": {
			PodUID:      "pod-2",
			PodName:     "test-pod-2",
			Namespace:   "production",
			AvgIOPS:     1000,
			BurstScore:  0.5,
			Volatility:  0.1,
			SampleCount: 50,
		},
	}

	scores := engine.CalculateScores(podMetrics, profiles)

	if len(scores) != 2 {
		t.Errorf("Expected 2 scores, got %d", len(scores))
	}

	// 验证 pod-1 的评分应该更高 (IO 占比更大)
	var pod1Score, pod2Score *PodOperationScore
	for _, s := range scores {
		if s.PodUID == "pod-1" {
			pod1Score = s
		} else if s.PodUID == "pod-2" {
			pod2Score = s
		}
	}

	if pod1Score == nil || pod2Score == nil {
		t.Fatal("Scores should not be nil")
	}

	// pod-1 在 default 命名空间，业务重要性低
	// pod-1 IO 占比高，影响评分高
	// 因此 pod-1 的最终评分应该更高
	if pod1Score.ImpactScore <= pod2Score.ImpactScore {
		t.Errorf("Pod-1 impact score (%f) should be higher than Pod-2 (%f)",
			pod1Score.ImpactScore, pod2Score.ImpactScore)
	}
}

func TestRecommendAction(t *testing.T) {
	cfg := config.ScoringConfig{
		Weights: config.WeightsConfig{
			BusinessImportance: 0.3,
			HistoryBehavior:    0.2,
			ActionEffect:       0.3,
			CurrentImpact:      0.2,
		},
	}

	engine := NewEngine(cfg)

	tests := []struct {
		name           string
		score          *PodOperationScore
		expectedAction ActionType
	}{
		{
			name: "critical_business",
			score: &PodOperationScore{
				BusinessScore: 95,
				FinalScore:    80,
			},
			expectedAction: ActionNone,
		},
		{
			name: "high_score_low_business",
			score: &PodOperationScore{
				BusinessScore: 30,
				FinalScore:    85,
			},
			expectedAction: ActionThrottle50,
		},
		{
			name: "high_score_medium_business",
			score: &PodOperationScore{
				BusinessScore: 60,
				FinalScore:    80,
			},
			expectedAction: ActionThrottle25,
		},
		{
			name: "medium_score",
			score: &PodOperationScore{
				BusinessScore: 30,
				FinalScore:    50,
			},
			expectedAction: ActionThrottle25,
		},
		{
			name: "low_score",
			score: &PodOperationScore{
				BusinessScore: 30,
				FinalScore:    30,
			},
			expectedAction: ActionAlert,
		},
		{
			name: "very_low_score",
			score: &PodOperationScore{
				BusinessScore: 30,
				FinalScore:    10,
			},
			expectedAction: ActionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := engine.recommendAction(tt.score)
			if action != tt.expectedAction {
				t.Errorf("Expected action %s, got %s", tt.expectedAction, action)
			}
		})
	}
}

func TestWeightAdjuster(t *testing.T) {
	weights := map[string]float64{
		"business": 0.3,
		"history":  0.2,
		"effect":   0.3,
		"impact":   0.2,
	}

	adjuster := NewWeightAdjuster(weights)

	// 验证初始权重
	currentWeights := adjuster.GetWeights()
	if len(currentWeights) != 4 {
		t.Errorf("Expected 4 weights, got %d", len(currentWeights))
	}

	// 测试权重更新
	newWeights := map[string]float64{
		"business": 0.4,
		"history":  0.2,
		"effect":   0.2,
		"impact":   0.2,
	}

	err := adjuster.Update(newWeights)
	if err != nil {
		t.Errorf("Update should not fail: %v", err)
	}

	// 测试反馈更新
	adjuster.UpdateFromFeedback("throttle_25", true)
	adjuster.UpdateFromFeedback("throttle_25", true)
	adjuster.UpdateFromFeedback("throttle_25", false)

	accuracy := adjuster.GetAccuracy()
	if len(accuracy) == 0 {
		t.Error("Accuracy should be tracked")
	}
}

func TestHistoryAnalyzer(t *testing.T) {
	cfg := config.ScoringConfig{
		RecidivismBaseRate:   0.1,
		RecidivismDecayHours: 24,
	}

	analyzer := NewHistoryAnalyzer(cfg)

	podUID := "test-pod-uid"

	// 初始复犯评分应该很低
	score := analyzer.CalculateRecidivismScore(podUID)
	if score > 15 {
		t.Errorf("Initial recidivism score should be low, got %f", score)
	}

	// 记录违规
	analyzer.RecordViolation(podUID, "high_io")
	analyzer.RecordViolation(podUID, "high_io")
	analyzer.RecordViolation(podUID, "high_io")

	// 违规后复犯评分应该增加
	scoreAfterViolations := analyzer.CalculateRecidivismScore(podUID)
	if scoreAfterViolations <= score {
		t.Errorf("Recidivism score should increase after violations, before: %f, after: %f",
			score, scoreAfterViolations)
	}

	// 记录操作结果
	analyzer.RecordActionResult(podUID, "throttle_25", true)
	analyzer.RecordActionResult(podUID, "throttle_25", false)

	history := analyzer.GetHistoryByUID(podUID)
	if history == nil {
		t.Fatal("History should exist")
	}

	if history.ViolationCount != 3 {
		t.Errorf("Expected 3 violations, got %d", history.ViolationCount)
	}

	if history.ActionCount != 2 {
		t.Errorf("Expected 2 actions, got %d", history.ActionCount)
	}
}

func TestActionSimulator(t *testing.T) {
	simulator := NewActionSimulator()

	metrics := &collector.PodMetrics{
		PodUID:      "pod-1",
		TotalIOPS:   5000,
		TotalBPS:    100 * 1024 * 1024,
		IOPSPercent: 50,
		BPSPercent:  40,
	}

	prof := &profile.IOProfile{
		BurstScore: 1.5,
		Volatility: 0.5,
	}

	// 测试效果估计
	effect := simulator.EstimateEffect(metrics, prof)
	if effect <= 0 {
		t.Errorf("Effect should be positive, got %f", effect)
	}

	// 测试模拟操作
	action := CandidateAction{
		Type:      ActionThrottle25,
		TargetPod: "pod-1",
	}

	result := simulator.SimulateAction(action, metrics)

	if result.ExpectedIOPSRelief <= 0 {
		t.Errorf("Expected IOPS relief should be positive, got %f", result.ExpectedIOPSRelief)
	}

	if result.VictimRecoveryProb <= 0 {
		t.Errorf("Victim recovery prob should be positive, got %f", result.VictimRecoveryProb)
	}
}

func TestBusinessPriorityConfig(t *testing.T) {
	config := NewBusinessPriorityConfig()

	// 测试命名空间优先级
	priority := config.GetPriority("kube-system", "coredns")
	if priority != 100 {
		t.Errorf("kube-system priority should be 100, got %d", priority)
	}

	priority = config.GetPriority("production", "api-server")
	if priority != 90 {
		t.Errorf("production priority should be 90, got %d", priority)
	}

	priority = config.GetPriority("dev", "test-app")
	if priority != 20 {
		t.Errorf("dev priority should be 20, got %d", priority)
	}

	// 测试保护级别
	level := config.GetProtectionLevel("kube-system", "coredns")
	if level != ProtectionCritical {
		t.Errorf("kube-system should be critical, got %s", level)
	}

	// 测试禁止限流
	neverThrottle := config.IsNeverThrottle("kube-system", "coredns")
	if !neverThrottle {
		t.Error("kube-system should never be throttled")
	}
}
