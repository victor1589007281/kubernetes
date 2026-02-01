// Package analyzer - 受害者分析器测试
package analyzer

import (
	"testing"

	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/profile"
)

func TestNewVictimAnalyzer(t *testing.T) {
	cfg := config.AnalyzerConfig{
		ZScoreThreshold:      2.0,
		CorrelationMinimum:   0.6,
		VictimScoreThreshold: 0.5,
	}

	analyzer := NewVictimAnalyzer(cfg)

	if analyzer == nil {
		t.Fatal("Analyzer should not be nil")
	}

	if analyzer.config.ZScoreThreshold != 2.0 {
		t.Errorf("Expected Z-Score threshold 2.0, got %f", analyzer.config.ZScoreThreshold)
	}
}

func TestAnalyze(t *testing.T) {
	cfg := config.AnalyzerConfig{
		ZScoreThreshold:      2.0,
		CorrelationMinimum:   0.6,
		VictimScoreThreshold: 0.3,
	}

	analyzer := NewVictimAnalyzer(cfg)

	// 先建立基线
	normalProfiles := map[string]*profile.IOProfile{
		"pod-1": {
			PodUID:     "pod-1",
			PodName:    "normal-pod",
			Namespace:  "default",
			AvgIOPS:    1000,
			StdDevIOPS: 100,
			Volatility: 0.2,
			BurstScore: 0.5,
			Trend:      0,
		},
	}

	// 多次更新建立基线
	for i := 0; i < 15; i++ {
		analyzer.updateBaseline(normalProfiles)
	}

	// 模拟受害者场景：IOPS 显著下降
	victimProfiles := map[string]*profile.IOProfile{
		"pod-1": {
			PodUID:     "pod-1",
			PodName:    "normal-pod",
			Namespace:  "default",
			AvgIOPS:    300, // 显著下降
			StdDevIOPS: 50,
			Volatility: 0.9, // 高波动
			BurstScore: 3.0, // 高突发
			Trend:      -0.7, // 下降趋势
		},
		"pod-2": {
			PodUID:     "pod-2",
			PodName:    "aggressor-pod",
			Namespace:  "default",
			AvgIOPS:    8000, // 高 IOPS
			IOPSScore:  70,   // 高占比
			BPSScore:   60,
			Volatility: 0.1,
			BurstScore: 0.3,
		},
	}

	victims := analyzer.Analyze(victimProfiles)

	// 应该识别出 pod-1 是受害者
	found := false
	for _, v := range victims {
		if v.PodUID == "pod-1" {
			found = true
			if v.Score <= 0 {
				t.Errorf("Victim score should be positive, got %f", v.Score)
			}
			if len(v.Reasons) == 0 {
				t.Error("Victim should have reasons")
			}
			break
		}
	}

	if !found {
		t.Error("Pod-1 should be identified as victim")
	}
}

func TestScoreToSeverity(t *testing.T) {
	cfg := config.AnalyzerConfig{}
	analyzer := NewVictimAnalyzer(cfg)

	tests := []struct {
		score    float64
		expected Severity
	}{
		{90, SeverityCritical},
		{80, SeverityCritical},
		{70, SeverityHigh},
		{60, SeverityHigh},
		{50, SeverityMedium},
		{40, SeverityMedium},
		{30, SeverityLow},
		{10, SeverityLow},
	}

	for _, tt := range tests {
		severity := analyzer.scoreToseverity(tt.score)
		if severity != tt.expected {
			t.Errorf("Score %f: expected %s, got %s", tt.score, tt.expected, severity)
		}
	}
}

func TestSeverityString(t *testing.T) {
	tests := []struct {
		severity Severity
		expected string
	}{
		{SeverityLow, "low"},
		{SeverityMedium, "medium"},
		{SeverityHigh, "high"},
		{SeverityCritical, "critical"},
	}

	for _, tt := range tests {
		if tt.severity.String() != tt.expected {
			t.Errorf("Expected %s, got %s", tt.expected, tt.severity.String())
		}
	}
}

func TestIdentifyAggressors(t *testing.T) {
	cfg := config.AnalyzerConfig{
		CorrelationMinimum: 0.6,
	}

	analyzer := NewVictimAnalyzer(cfg)

	victims := []*VictimResult{
		{
			PodUID:    "victim-1",
			PodName:   "victim-pod",
			Namespace: "default",
			Score:     70,
		},
	}

	profiles := map[string]*profile.IOProfile{
		"victim-1": {
			PodUID:    "victim-1",
			AvgIOPS:   500,
			IOPSScore: 5,
			BPSScore:  5,
		},
		"aggressor-1": {
			PodUID:    "aggressor-1",
			PodName:   "aggressor-pod-1",
			Namespace: "default",
			AvgIOPS:   8000,
			IOPSScore: 50, // 高占比
			BPSScore:  40,
		},
		"aggressor-2": {
			PodUID:    "aggressor-2",
			PodName:   "aggressor-pod-2",
			Namespace: "default",
			AvgIOPS:   5000,
			IOPSScore: 30,
			BPSScore:  25,
		},
		"normal": {
			PodUID:    "normal",
			PodName:   "normal-pod",
			Namespace: "default",
			AvgIOPS:   1000,
			IOPSScore: 10,
			BPSScore:  8,
		},
	}

	analyzer.identifyAggressors(victims, profiles)

	if len(victims[0].Aggressors) == 0 {
		t.Error("Should identify aggressors")
	}

	// 验证攻击者按相关性排序
	if len(victims[0].Aggressors) >= 2 {
		if victims[0].Aggressors[0].Correlation < victims[0].Aggressors[1].Correlation {
			t.Error("Aggressors should be sorted by correlation")
		}
	}

	// aggressor-1 应该是主要攻击者
	if victims[0].Aggressors[0].PodUID != "aggressor-1" {
		t.Errorf("Expected aggressor-1 as top aggressor, got %s",
			victims[0].Aggressors[0].PodUID)
	}
}

func TestCalculateCorrelation(t *testing.T) {
	p1 := &profile.IOProfile{
		ReadRatio:       0.7,
		SequentialRatio: 0.8,
		Fingerprint: profile.IOFingerprint{
			IOPSHistogram:   [4]float64{0.1, 0.3, 0.4, 0.2},
			IOSizeHistogram: [6]float64{0.1, 0.2, 0.3, 0.2, 0.1, 0.1},
		},
	}

	p2 := &profile.IOProfile{
		ReadRatio:       0.7,
		SequentialRatio: 0.8,
		Fingerprint: profile.IOFingerprint{
			IOPSHistogram:   [4]float64{0.1, 0.3, 0.4, 0.2},
			IOSizeHistogram: [6]float64{0.1, 0.2, 0.3, 0.2, 0.1, 0.1},
		},
	}

	correlation := CalculateCorrelation(p1, p2)

	// 相同的画像应该有高相关性
	if correlation < 0.9 {
		t.Errorf("Same profiles should have high correlation, got %f", correlation)
	}

	// 测试 nil 情况
	correlation = CalculateCorrelation(nil, p2)
	if correlation != 0 {
		t.Errorf("Nil profile should return 0 correlation, got %f", correlation)
	}
}
