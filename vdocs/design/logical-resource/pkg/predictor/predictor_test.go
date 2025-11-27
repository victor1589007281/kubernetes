/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package predictor

import (
	"math"
	"testing"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
)

func TestNewMemoryPredictor(t *testing.T) {
	config := types.DefaultPredictorConfig()
	predictor := NewMemoryPredictor(config)

	if predictor == nil {
		t.Fatal("Expected non-nil predictor")
	}

	if predictor.GetDataPointCount() != 0 {
		t.Errorf("Expected 0 data points, got %d", predictor.GetDataPointCount())
	}
}

func TestAddDataPoint(t *testing.T) {
	config := types.DefaultPredictorConfig()
	predictor := NewMemoryPredictor(config)

	// Add a valid data point
	point := types.MemoryDataPoint{
		Timestamp:        time.Now(),
		ActualUsageBytes: 1024 * 1024 * 1024, // 1GB
		BufferPoolBytes:  4 * 1024 * 1024 * 1024, // 4GB
		PodName:          "mysql-0",
		Namespace:        "default",
	}

	err := predictor.AddDataPoint(point)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if predictor.GetDataPointCount() != 1 {
		t.Errorf("Expected 1 data point, got %d", predictor.GetDataPointCount())
	}

	// Verify usage ratio was calculated
	latestPoint, ok := predictor.GetLatestDataPoint()
	if !ok {
		t.Fatal("Expected to get latest data point")
	}

	expectedRatio := float64(1024*1024*1024) / float64(4*1024*1024*1024)
	if math.Abs(latestPoint.UsageRatio-expectedRatio) > 0.001 {
		t.Errorf("Expected usage ratio %f, got %f", expectedRatio, latestPoint.UsageRatio)
	}
}

func TestAddDataPointValidation(t *testing.T) {
	config := types.DefaultPredictorConfig()
	predictor := NewMemoryPredictor(config)

	// Test invalid buffer pool
	point := types.MemoryDataPoint{
		Timestamp:        time.Now(),
		ActualUsageBytes: 1024,
		BufferPoolBytes:  0, // Invalid
	}

	err := predictor.AddDataPoint(point)
	if err == nil {
		t.Error("Expected error for zero buffer pool")
	}

	// Test negative usage
	point = types.MemoryDataPoint{
		Timestamp:        time.Now(),
		ActualUsageBytes: -1, // Invalid
		BufferPoolBytes:  1024,
	}

	err = predictor.AddDataPoint(point)
	if err == nil {
		t.Error("Expected error for negative usage")
	}
}

func TestPredict(t *testing.T) {
	config := types.DefaultPredictorConfig()
	config.SeasonalPeriod = 24
	predictor := NewMemoryPredictor(config)

	// Add enough data points for prediction (at least 2 seasonal periods)
	baseTime := time.Now().Add(-72 * time.Hour)
	bufferPool := int64(64 * 1024 * 1024 * 1024) // 64GB

	for i := 0; i < 72; i++ { // 72 hours of data
		// Simulate a daily pattern
		hour := i % 24
		var usageRatio float64
		if hour >= 9 && hour <= 17 {
			usageRatio = 0.4 + float64(i%10)*0.01 // Higher during business hours
		} else {
			usageRatio = 0.2 + float64(i%10)*0.01 // Lower at night
		}

		point := types.MemoryDataPoint{
			Timestamp:        baseTime.Add(time.Duration(i) * time.Hour),
			ActualUsageBytes: int64(usageRatio * float64(bufferPool)),
			BufferPoolBytes:  bufferPool,
			PodName:          "mysql-0",
			Namespace:        "default",
		}

		if err := predictor.AddDataPoint(point); err != nil {
			t.Fatalf("Failed to add data point: %v", err)
		}
	}

	// Test prediction
	predictions, err := predictor.Predict(24)
	if err != nil {
		t.Fatalf("Prediction failed: %v", err)
	}

	if len(predictions) != 24 {
		t.Errorf("Expected 24 predictions, got %d", len(predictions))
	}

	// Verify predictions are reasonable
	for i, pred := range predictions {
		if pred.PredictedUsageRatio < 0 || pred.PredictedUsageRatio > 1 {
			t.Errorf("Prediction %d has invalid ratio: %f", i, pred.PredictedUsageRatio)
		}

		if pred.ConfidenceLower > pred.PredictedUsageBytes {
			t.Errorf("Prediction %d: lower bound > predicted", i)
		}

		if pred.ConfidenceUpper < pred.PredictedUsageBytes {
			t.Errorf("Prediction %d: upper bound < predicted", i)
		}
	}
}

func TestPredictInsufficientData(t *testing.T) {
	config := types.DefaultPredictorConfig()
	config.SeasonalPeriod = 24
	predictor := NewMemoryPredictor(config)

	// Add only a few data points (not enough)
	for i := 0; i < 10; i++ {
		point := types.MemoryDataPoint{
			Timestamp:        time.Now().Add(time.Duration(i) * time.Hour),
			ActualUsageBytes: 1024 * 1024 * 1024,
			BufferPoolBytes:  4 * 1024 * 1024 * 1024,
		}
		predictor.AddDataPoint(point)
	}

	_, err := predictor.Predict(24)
	if err == nil {
		t.Error("Expected error for insufficient data")
	}
}

func TestHandleBufferPoolChange(t *testing.T) {
	config := types.DefaultPredictorConfig()
	predictor := NewMemoryPredictor(config)

	// Add initial data
	point := types.MemoryDataPoint{
		Timestamp:        time.Now(),
		ActualUsageBytes: 1024 * 1024 * 1024,
		BufferPoolBytes:  4 * 1024 * 1024 * 1024,
		PodName:          "mysql-0",
		Namespace:        "default",
	}
	predictor.AddDataPoint(point)

	// Handle buffer pool change
	event := types.BufferPoolChangeEvent{
		Timestamp:    time.Now(),
		PodName:      "mysql-0",
		Namespace:    "default",
		OldSizeBytes: 4 * 1024 * 1024 * 1024,
		NewSizeBytes: 8 * 1024 * 1024 * 1024,
	}

	err := predictor.HandleBufferPoolChange(event)
	if err != nil {
		t.Fatalf("Failed to handle buffer pool change: %v", err)
	}

	// Verify new buffer pool is tracked
	newPoint := types.MemoryDataPoint{
		Timestamp:        time.Now().Add(time.Hour),
		ActualUsageBytes: 2 * 1024 * 1024 * 1024,
		BufferPoolBytes:  8 * 1024 * 1024 * 1024,
		PodName:          "mysql-0",
		Namespace:        "default",
	}
	err = predictor.AddDataPoint(newPoint)
	if err != nil {
		t.Fatalf("Failed to add point after buffer pool change: %v", err)
	}
}

func TestGetRecommendedOversellRatio(t *testing.T) {
	config := types.DefaultPredictorConfig()
	config.SeasonalPeriod = 24
	predictor := NewMemoryPredictor(config)

	// Add data with low usage ratio
	baseTime := time.Now().Add(-72 * time.Hour)
	bufferPool := int64(64 * 1024 * 1024 * 1024)

	for i := 0; i < 72; i++ {
		usageRatio := 0.3 + float64(i%10)*0.01 // ~30-40% usage
		point := types.MemoryDataPoint{
			Timestamp:        baseTime.Add(time.Duration(i) * time.Hour),
			ActualUsageBytes: int64(usageRatio * float64(bufferPool)),
			BufferPoolBytes:  bufferPool,
		}
		predictor.AddDataPoint(point)
	}

	ratio, err := predictor.GetRecommendedOversellRatio(24, 0.85)
	if err != nil {
		t.Fatalf("Failed to get recommended ratio: %v", err)
	}

	// With ~30-40% usage, oversell ratio should be > 1.0
	if ratio < 1.0 {
		t.Errorf("Expected ratio >= 1.0, got %f", ratio)
	}

	// Should be capped at 3.0
	if ratio > 3.0 {
		t.Errorf("Expected ratio <= 3.0, got %f", ratio)
	}
}

func TestPredictMaxUsage(t *testing.T) {
	config := types.DefaultPredictorConfig()
	config.SeasonalPeriod = 24
	predictor := NewMemoryPredictor(config)

	// Add data
	baseTime := time.Now().Add(-72 * time.Hour)
	bufferPool := int64(64 * 1024 * 1024 * 1024)

	for i := 0; i < 72; i++ {
		usageRatio := 0.4
		point := types.MemoryDataPoint{
			Timestamp:        baseTime.Add(time.Duration(i) * time.Hour),
			ActualUsageBytes: int64(usageRatio * float64(bufferPool)),
			BufferPoolBytes:  bufferPool,
		}
		predictor.AddDataPoint(point)
	}

	maxUsage, err := predictor.PredictMaxUsage(24)
	if err != nil {
		t.Fatalf("Failed to predict max usage: %v", err)
	}

	if maxUsage <= 0 {
		t.Error("Expected positive max usage")
	}

	if maxUsage > bufferPool {
		t.Errorf("Max usage %d exceeds buffer pool %d", maxUsage, bufferPool)
	}
}

// Test Holt-Winters algorithm
func TestHoltWinters(t *testing.T) {
	hw := NewHoltWinters(0.3, 0.1, 0.2, 12) // 12-hour seasonal period

	// Create sample data with trend and seasonality
	data := make([]float64, 48) // 48 hours
	for i := 0; i < 48; i++ {
		// Base level with trend
		base := 100.0 + float64(i)*0.5
		// Seasonal component (12-hour period)
		seasonal := 20.0 * math.Sin(2*math.Pi*float64(i)/12.0)
		data[i] = base + seasonal
	}

	predictions, err := hw.Predict(data, 12)
	if err != nil {
		t.Fatalf("Holt-Winters prediction failed: %v", err)
	}

	if len(predictions) != 12 {
		t.Errorf("Expected 12 predictions, got %d", len(predictions))
	}

	// Predictions should be positive
	for i, p := range predictions {
		if p < 0 {
			t.Errorf("Prediction %d is negative: %f", i, p)
		}
	}
}

func TestHoltWintersInsufficientData(t *testing.T) {
	hw := NewHoltWinters(0.3, 0.1, 0.2, 24)

	// Not enough data for 2 seasonal periods
	data := make([]float64, 20)
	for i := range data {
		data[i] = float64(i)
	}

	_, err := hw.Predict(data, 10)
	if err == nil {
		t.Error("Expected error for insufficient data")
	}
}

func TestHoltWintersOptimize(t *testing.T) {
	hw := NewHoltWinters(0.3, 0.1, 0.2, 12)

	// Create data
	data := make([]float64, 60)
	for i := range data {
		data[i] = 100.0 + float64(i)*0.5 + 10.0*math.Sin(2*math.Pi*float64(i)/12.0)
	}

	alpha, beta, gamma, mse := hw.OptimizeParameters(data, 0.2)

	if mse == math.MaxFloat64 {
		t.Error("Optimization failed")
	}

	if alpha < 0 || alpha > 1 {
		t.Errorf("Invalid alpha: %f", alpha)
	}
	if beta < 0 || beta > 1 {
		t.Errorf("Invalid beta: %f", beta)
	}
	if gamma < 0 || gamma > 1 {
		t.Errorf("Invalid gamma: %f", gamma)
	}
}

// Test Trend Analyzer
func TestTrendAnalyzer(t *testing.T) {
	ta := NewTrendAnalyzer(24, 72)

	// Create data with upward trend
	data := make([]float64, 100)
	for i := range data {
		data[i] = 50.0 + float64(i)*0.5
	}

	// Test moving average
	ma := ta.MovingAverage(data)
	if len(ma) != len(data) {
		t.Errorf("Moving average length mismatch: %d vs %d", len(ma), len(data))
	}

	// Test trend calculation
	trend := ta.CalculateTrend(data)
	if trend <= 0 {
		t.Errorf("Expected positive trend, got %f", trend)
	}
}

func TestTrendAnalyzerPredict(t *testing.T) {
	ta := NewTrendAnalyzer(12, 24)

	// Create data
	data := make([]float64, 50)
	for i := range data {
		data[i] = 0.3 + float64(i)*0.002
	}

	predictions := ta.Predict(data, 10)

	if len(predictions) != 10 {
		t.Errorf("Expected 10 predictions, got %d", len(predictions))
	}

	// Predictions should follow the trend
	for i, p := range predictions {
		if p < 0 || p > 1 {
			t.Errorf("Prediction %d out of range: %f", i, p)
		}
	}
}

func TestTrendAnalyzerAnomaly(t *testing.T) {
	ta := NewTrendAnalyzer(10, 30)

	// Create data with anomaly
	data := make([]float64, 50)
	for i := range data {
		data[i] = 100.0
	}
	data[25] = 200.0 // Anomaly

	anomalies := ta.DetectAnomaly(data, 2.0)

	if len(anomalies) != len(data) {
		t.Errorf("Anomaly length mismatch")
	}

	// The anomaly point should be detected
	if !anomalies[25] {
		t.Error("Expected anomaly at index 25 to be detected")
	}
}

func TestTrendAnalyzerLinearRegression(t *testing.T) {
	ta := NewTrendAnalyzer(10, 30)

	// Create linear data: y = 2x + 10
	data := make([]float64, 50)
	for i := range data {
		data[i] = 2*float64(i) + 10
	}

	slope, intercept := ta.LinearRegression(data)

	if math.Abs(slope-2) > 0.001 {
		t.Errorf("Expected slope ~2, got %f", slope)
	}

	if math.Abs(intercept-10) > 0.1 {
		t.Errorf("Expected intercept ~10, got %f", intercept)
	}
}

func TestTrendAnalyzerVolatility(t *testing.T) {
	ta := NewTrendAnalyzer(10, 30)

	// Low volatility data
	lowVol := make([]float64, 50)
	for i := range lowVol {
		lowVol[i] = 100.0
	}

	// High volatility data
	highVol := make([]float64, 50)
	for i := range highVol {
		if i%2 == 0 {
			highVol[i] = 50.0
		} else {
			highVol[i] = 150.0
		}
	}

	lowVolatility := ta.CalculateVolatility(lowVol)
	highVolatility := ta.CalculateVolatility(highVol)

	if lowVolatility >= highVolatility {
		t.Errorf("Expected low volatility < high volatility, got %f >= %f",
			lowVolatility, highVolatility)
	}
}

// Benchmark tests
func BenchmarkAddDataPoint(b *testing.B) {
	config := types.DefaultPredictorConfig()
	predictor := NewMemoryPredictor(config)

	point := types.MemoryDataPoint{
		Timestamp:        time.Now(),
		ActualUsageBytes: 1024 * 1024 * 1024,
		BufferPoolBytes:  4 * 1024 * 1024 * 1024,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		point.Timestamp = time.Now().Add(time.Duration(i) * time.Hour)
		predictor.AddDataPoint(point)
	}
}

func BenchmarkHoltWintersPredict(b *testing.B) {
	hw := NewHoltWinters(0.3, 0.1, 0.2, 24)

	data := make([]float64, 100)
	for i := range data {
		data[i] = 100.0 + float64(i)*0.5 + 10.0*math.Sin(2*math.Pi*float64(i)/24.0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hw.Predict(data, 24)
	}
}

