// Package profile - IO 画像引擎测试
package profile

import (
	"testing"
	"time"

	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/config"
)

func TestNewEngine(t *testing.T) {
	cfg := config.ProfileConfig{
		IntervalSeconds:         10,
		WindowSize:              60,
		RetentionHours:          24,
		SequentialIOThreshold:   0.7,
	}

	engine := NewEngine(cfg)

	if engine == nil {
		t.Fatal("Engine should not be nil")
	}

	if engine.config.WindowSize != 60 {
		t.Errorf("Expected window size 60, got %d", engine.config.WindowSize)
	}
}

func TestUpdateProfile(t *testing.T) {
	cfg := config.ProfileConfig{
		IntervalSeconds: 10,
		WindowSize:      10,
	}

	engine := NewEngine(cfg)

	// 模拟采集数据
	data := &collector.CollectedData{
		Timestamp: time.Now(),
		Disks: map[string]*collector.DiskStats{
			"nvme0n1": {
				ReadIOPS:        10000,
				WriteIOPS:       5000,
				ReadBytesPerSec: 500 * 1024 * 1024,
				WriteBytesPerSec: 200 * 1024 * 1024,
			},
		},
		Pods: map[string]*collector.PodIOStats{
			"pod-1": {
				PodUID:           "pod-1",
				PodName:          "test-pod",
				Namespace:        "default",
				ReadIOPS:         3000,
				WriteIOPS:        1000,
				ReadBytesPerSec:  150 * 1024 * 1024,
				WriteBytesPerSec: 50 * 1024 * 1024,
			},
		},
	}

	// 多次更新以积累样本
	for i := 0; i < 15; i++ {
		data.Timestamp = time.Now().Add(time.Duration(i) * time.Second)
		// 添加一些变化
		data.Pods["pod-1"].ReadIOPS = float64(3000 + i*100)
		engine.Update(data)
	}

	// 获取画像
	profiles := engine.GetProfiles()

	if len(profiles) != 1 {
		t.Errorf("Expected 1 profile, got %d", len(profiles))
	}

	profile := profiles["pod-1"]
	if profile == nil {
		t.Fatal("Profile should exist")
	}

	if profile.PodName != "test-pod" {
		t.Errorf("Expected pod name 'test-pod', got '%s'", profile.PodName)
	}

	if profile.SampleCount < 10 {
		t.Errorf("Expected at least 10 samples, got %d", profile.SampleCount)
	}

	// 验证统计量
	if profile.AvgIOPS <= 0 {
		t.Errorf("AvgIOPS should be positive, got %f", profile.AvgIOPS)
	}

	if profile.MaxIOPS <= profile.MinIOPS {
		t.Errorf("MaxIOPS (%f) should be greater than MinIOPS (%f)",
			profile.MaxIOPS, profile.MinIOPS)
	}
}

func TestComputeTrend(t *testing.T) {
	cfg := config.ProfileConfig{
		WindowSize: 10,
	}

	engine := NewEngine(cfg)

	// 测试上升趋势
	increasingSamples := []sample{
		{IOPS: 100},
		{IOPS: 200},
		{IOPS: 300},
		{IOPS: 400},
		{IOPS: 500},
	}

	trend := engine.computeTrend(increasingSamples)
	if trend <= 0 {
		t.Errorf("Expected positive trend for increasing data, got %f", trend)
	}

	// 测试下降趋势
	decreasingSamples := []sample{
		{IOPS: 500},
		{IOPS: 400},
		{IOPS: 300},
		{IOPS: 200},
		{IOPS: 100},
	}

	trend = engine.computeTrend(decreasingSamples)
	if trend >= 0 {
		t.Errorf("Expected negative trend for decreasing data, got %f", trend)
	}

	// 测试稳定
	stableSamples := []sample{
		{IOPS: 100},
		{IOPS: 101},
		{IOPS: 99},
		{IOPS: 100},
		{IOPS: 100},
	}

	trend = engine.computeTrend(stableSamples)
	if trend < -0.1 || trend > 0.1 {
		t.Errorf("Expected near-zero trend for stable data, got %f", trend)
	}
}

func TestCompareProfiles(t *testing.T) {
	p1 := &IOProfile{
		ReadRatio:       0.7,
		SequentialRatio: 0.8,
		Fingerprint: IOFingerprint{
			IOPSHistogram:   [4]float64{0.1, 0.3, 0.4, 0.2},
			IOSizeHistogram: [6]float64{0.1, 0.2, 0.3, 0.2, 0.1, 0.1},
		},
	}

	// 相似的画像
	p2 := &IOProfile{
		ReadRatio:       0.65,
		SequentialRatio: 0.75,
		Fingerprint: IOFingerprint{
			IOPSHistogram:   [4]float64{0.15, 0.25, 0.4, 0.2},
			IOSizeHistogram: [6]float64{0.1, 0.25, 0.25, 0.2, 0.1, 0.1},
		},
	}

	// 不相似的画像
	p3 := &IOProfile{
		ReadRatio:       0.2,
		SequentialRatio: 0.1,
		Fingerprint: IOFingerprint{
			IOPSHistogram:   [4]float64{0.7, 0.2, 0.05, 0.05},
			IOSizeHistogram: [6]float64{0.6, 0.3, 0.05, 0.02, 0.02, 0.01},
		},
	}

	// p1 和 p2 应该相似
	sim12 := CompareProfiles(p1, p2)
	// p1 和 p3 应该不相似
	sim13 := CompareProfiles(p1, p3)

	if sim12 <= sim13 {
		t.Errorf("Similar profiles should have higher similarity: p1-p2=%f, p1-p3=%f",
			sim12, sim13)
	}

	// 相同画像应该完全相似
	sim11 := CompareProfiles(p1, p1)
	if sim11 < 0.99 {
		t.Errorf("Same profile should have similarity ~1.0, got %f", sim11)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{
			name:     "identical",
			a:        []float64{1, 2, 3},
			b:        []float64{1, 2, 3},
			expected: 1.0,
		},
		{
			name:     "orthogonal",
			a:        []float64{1, 0, 0},
			b:        []float64{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "similar",
			a:        []float64{1, 2, 3},
			b:        []float64{1, 2, 4},
			expected: 0.99, // 应该接近1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := cosineSimilarity(tt.a, tt.b)

			if tt.name == "identical" && sim != 1.0 {
				t.Errorf("Expected 1.0, got %f", sim)
			}

			if tt.name == "orthogonal" && sim != 0.0 {
				t.Errorf("Expected 0.0, got %f", sim)
			}

			if tt.name == "similar" && sim < 0.95 {
				t.Errorf("Expected > 0.95, got %f", sim)
			}
		})
	}
}

func TestIOFingerprint(t *testing.T) {
	cfg := config.ProfileConfig{
		WindowSize: 10,
	}

	engine := NewEngine(cfg)

	samples := []sample{
		{Timestamp: time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC), IOPS: 50, IOSize: 4096},
		{Timestamp: time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC), IOPS: 500, IOSize: 8192},
		{Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC), IOPS: 5000, IOSize: 65536},
		{Timestamp: time.Date(2024, 1, 1, 13, 0, 0, 0, time.UTC), IOPS: 50000, IOSize: 131072},
	}

	fp := engine.computeFingerprint(samples)

	// 验证 IOPS 直方图
	totalIOPS := fp.IOPSHistogram[0] + fp.IOPSHistogram[1] + fp.IOPSHistogram[2] + fp.IOPSHistogram[3]
	if totalIOPS < 0.99 || totalIOPS > 1.01 {
		t.Errorf("IOPS histogram should sum to 1.0, got %f", totalIOPS)
	}

	// 验证 IO 大小直方图
	totalSize := 0.0
	for _, v := range fp.IOSizeHistogram {
		totalSize += v
	}
	if totalSize < 0.99 || totalSize > 1.01 {
		t.Errorf("IO size histogram should sum to 1.0, got %f", totalSize)
	}

	// 验证小时分布
	totalHour := 0.0
	for _, v := range fp.HourlyPattern {
		totalHour += v
	}
	if totalHour < 0.99 || totalHour > 1.01 {
		t.Errorf("Hourly pattern should sum to 1.0, got %f", totalHour)
	}
}
