// Package predictor - IO 预测引擎
package predictor

import (
	"math"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/profile"
)

// PredictionResult 预测结果
type PredictionResult struct {
	Metric        string    // disk_util, iops, latency
	CurrentValue  float64
	PredictedValue float64
	Threshold     float64
	TimeToThreshold time.Duration // 预计多久到达阈值
	Confidence    float64   // 置信度 (0-1)
	Trend         string    // increasing, decreasing, stable
	Alert         bool      // 是否触发告警
	AlertSeverity string    // warning, critical
	PredictedAt   time.Time
}

// Predictor 预测引擎
type Predictor struct {
	// 历史数据
	diskUtilHistory    []float64
	iopsHistory        []float64
	latencyHistory     []float64
	historyMu          sync.RWMutex

	// 配置
	windowSize         int
	predictionHorizon  time.Duration

	// 阈值
	thresholds         map[string]float64
}

// NewPredictor 创建预测器
func NewPredictor() *Predictor {
	return &Predictor{
		windowSize:        60, // 60 个样本
		predictionHorizon: 5 * time.Minute,
		thresholds: map[string]float64{
			"disk_util": 90.0,
			"iops":      100000,
			"latency":   50.0,
		},
		diskUtilHistory: make([]float64, 0, 60),
		iopsHistory:     make([]float64, 0, 60),
		latencyHistory:  make([]float64, 0, 60),
	}
}

// Update 更新历史数据
func (p *Predictor) Update(diskUtil, iops, latency float64) {
	p.historyMu.Lock()
	defer p.historyMu.Unlock()

	p.diskUtilHistory = appendWithLimit(p.diskUtilHistory, diskUtil, p.windowSize)
	p.iopsHistory = appendWithLimit(p.iopsHistory, iops, p.windowSize)
	p.latencyHistory = appendWithLimit(p.latencyHistory, latency, p.windowSize)
}

// Predict 执行预测
func (p *Predictor) Predict() []*PredictionResult {
	p.historyMu.RLock()
	defer p.historyMu.RUnlock()

	results := make([]*PredictionResult, 0, 3)

	// 预测磁盘利用率
	if len(p.diskUtilHistory) >= 10 {
		result := p.predictMetric("disk_util", p.diskUtilHistory, p.thresholds["disk_util"])
		results = append(results, result)
	}

	// 预测 IOPS
	if len(p.iopsHistory) >= 10 {
		result := p.predictMetric("iops", p.iopsHistory, p.thresholds["iops"])
		results = append(results, result)
	}

	// 预测延迟
	if len(p.latencyHistory) >= 10 {
		result := p.predictMetric("latency", p.latencyHistory, p.thresholds["latency"])
		results = append(results, result)
	}

	return results
}

// predictMetric 预测单个指标
func (p *Predictor) predictMetric(metric string, history []float64, threshold float64) *PredictionResult {
	result := &PredictionResult{
		Metric:      metric,
		Threshold:   threshold,
		PredictedAt: time.Now(),
	}

	n := len(history)
	if n == 0 {
		return result
	}

	result.CurrentValue = history[n-1]

	// 线性回归预测
	slope, intercept := linearRegression(history)

	// 预测未来值 (假设采样间隔 5 秒)
	stepsAhead := int(p.predictionHorizon.Seconds() / 5)
	result.PredictedValue = intercept + slope*float64(n+stepsAhead)

	// 计算趋势
	if slope > 0.1 {
		result.Trend = "increasing"
	} else if slope < -0.1 {
		result.Trend = "decreasing"
	} else {
		result.Trend = "stable"
	}

	// 计算到达阈值的时间
	if result.Trend == "increasing" && result.CurrentValue < threshold {
		stepsToThreshold := (threshold - result.CurrentValue) / slope
		if stepsToThreshold > 0 && stepsToThreshold < 1000000 {
			result.TimeToThreshold = time.Duration(stepsToThreshold*5) * time.Second
		}
	}

	// 计算置信度 (基于 R² 决定系数)
	result.Confidence = calculateRSquared(history, slope, intercept)

	// 判断是否触发告警
	if result.PredictedValue > threshold {
		result.Alert = true
		if result.TimeToThreshold > 0 && result.TimeToThreshold < 5*time.Minute {
			result.AlertSeverity = "critical"
		} else {
			result.AlertSeverity = "warning"
		}
	} else if result.CurrentValue > threshold*0.9 {
		result.Alert = true
		result.AlertSeverity = "warning"
	}

	return result
}

// linearRegression 简单线性回归
func linearRegression(y []float64) (slope, intercept float64) {
	n := float64(len(y))
	if n < 2 {
		return 0, y[0]
	}

	var sumX, sumY, sumXY, sumX2 float64
	for i, v := range y {
		x := float64(i)
		sumX += x
		sumY += v
		sumXY += x * v
		sumX2 += x * x
	}

	denominator := n*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0, sumY / n
	}

	slope = (n*sumXY - sumX*sumY) / denominator
	intercept = (sumY - slope*sumX) / n

	return slope, intercept
}

// calculateRSquared 计算 R² 决定系数
func calculateRSquared(y []float64, slope, intercept float64) float64 {
	n := float64(len(y))
	if n < 2 {
		return 0
	}

	var sumY float64
	for _, v := range y {
		sumY += v
	}
	meanY := sumY / n

	var ssTotal, ssResidual float64
	for i, v := range y {
		predicted := intercept + slope*float64(i)
		ssTotal += math.Pow(v-meanY, 2)
		ssResidual += math.Pow(v-predicted, 2)
	}

	if ssTotal == 0 {
		return 1.0
	}

	rSquared := 1 - (ssResidual / ssTotal)
	return math.Max(0, math.Min(1, rSquared))
}

// appendWithLimit 添加元素并保持限制
func appendWithLimit(slice []float64, value float64, limit int) []float64 {
	slice = append(slice, value)
	if len(slice) > limit {
		slice = slice[len(slice)-limit:]
	}
	return slice
}

// SetThreshold 设置阈值
func (p *Predictor) SetThreshold(metric string, threshold float64) {
	p.thresholds[metric] = threshold
}

// UpdateFromProfiles 从画像数据更新
func (p *Predictor) UpdateFromProfiles(profiles map[string]*profile.IOProfile, diskUtil float64) {
	var totalIOPS, maxLatency float64

	for _, prof := range profiles {
		totalIOPS += prof.AvgIOPS
		// 使用 BurstScore 作为延迟的近似
		if prof.BurstScore > maxLatency {
			maxLatency = prof.BurstScore
		}
	}

	p.Update(diskUtil, totalIOPS, maxLatency*10) // 转换为 ms 量级
}
