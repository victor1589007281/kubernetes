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

// Package predictor provides memory usage prediction capabilities.
//
// This package implements multiple time series prediction algorithms:
//   - Holt-Winters (Triple Exponential Smoothing)
//   - ARIMA (AutoRegressive Integrated Moving Average)
//   - SARIMA (Seasonal ARIMA)
//   - Prophet-like (Trend + Seasonality decomposition)
//   - LSTM/GRU (Simplified neural network approach)
//
// The EnsemblePredictor automatically evaluates all algorithms and selects
// the best one based on cross-validation MAPE (Mean Absolute Percentage Error).
package predictor

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
)

// Predictor defines the interface for memory usage prediction.
type Predictor interface {
	// AddDataPoint adds a new data point to the predictor.
	AddDataPoint(point types.MemoryDataPoint) error

	// Predict predicts memory usage for the next forecastHours hours.
	Predict(forecastHours int) ([]types.PredictionResult, error)

	// GetRecommendedOversellRatio calculates the recommended oversell ratio.
	GetRecommendedOversellRatio(forecastHours int, safetyFactor float64) (float64, error)

	// HandleBufferPoolChange handles a buffer pool configuration change.
	HandleBufferPoolChange(event types.BufferPoolChangeEvent) error

	// GetAccuracy returns the prediction accuracy based on historical predictions.
	GetAccuracy() float64
}

// ExtendedPredictor extends Predictor with model evaluation capabilities.
type ExtendedPredictor interface {
	Predictor

	// EvaluateModels evaluates all available algorithms and returns their performance.
	EvaluateModels() ([]ModelResult, error)

	// GetSelectedAlgorithm returns the currently selected best algorithm.
	GetSelectedAlgorithm() AlgorithmType

	// GetLastEvaluation returns the results of the last model evaluation.
	GetLastEvaluation() []ModelResult
}

// MemoryPredictor implements the Predictor interface using a hybrid model.
type MemoryPredictor struct {
	mu sync.RWMutex

	// config holds the predictor configuration.
	config types.PredictorConfig

	// dataPoints stores historical data points.
	dataPoints []types.MemoryDataPoint

	// bufferPoolChanges stores buffer pool change events.
	bufferPoolChanges []types.BufferPoolChangeEvent

	// holtWinters is the Holt-Winters predictor component.
	holtWinters *HoltWinters

	// trendAnalyzer is the trend analysis component.
	trendAnalyzer *TrendAnalyzer

	// predictions stores historical predictions for accuracy calculation.
	predictions []predictionRecord

	// currentBufferPool is the current buffer pool size.
	currentBufferPool int64
}

// predictionRecord stores a prediction and its actual value for accuracy calculation.
type predictionRecord struct {
	predictedAt time.Time
	forTime     time.Time
	predicted   int64
	actual      int64
}

// NewMemoryPredictor creates a new MemoryPredictor with the given configuration.
func NewMemoryPredictor(config types.PredictorConfig) *MemoryPredictor {
	return &MemoryPredictor{
		config:            config,
		dataPoints:        make([]types.MemoryDataPoint, 0),
		bufferPoolChanges: make([]types.BufferPoolChangeEvent, 0),
		holtWinters:       NewHoltWinters(config.Alpha, config.Beta, config.Gamma, config.SeasonalPeriod),
		trendAnalyzer:     NewTrendAnalyzer(24, 72), // 24-hour window, 72-hour trend period
		predictions:       make([]predictionRecord, 0),
	}
}

// AddDataPoint adds a new data point to the predictor.
func (p *MemoryPredictor) AddDataPoint(point types.MemoryDataPoint) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Validate data point
	if point.BufferPoolBytes <= 0 {
		return errors.New("buffer pool size must be positive")
	}
	if point.ActualUsageBytes < 0 {
		return errors.New("actual usage cannot be negative")
	}

	// Calculate usage ratio if not set
	if point.UsageRatio == 0 {
		point.UsageRatio = float64(point.ActualUsageBytes) / float64(point.BufferPoolBytes)
	}

	// Update current buffer pool
	p.currentBufferPool = point.BufferPoolBytes

	// Add data point
	p.dataPoints = append(p.dataPoints, point)

	// Sort by timestamp
	sort.Slice(p.dataPoints, func(i, j int) bool {
		return p.dataPoints[i].Timestamp.Before(p.dataPoints[j].Timestamp)
	})

	// Prune old data points (keep only HistoryDays worth of data)
	cutoff := time.Now().AddDate(0, 0, -p.config.HistoryDays)
	newPoints := make([]types.MemoryDataPoint, 0)
	for _, dp := range p.dataPoints {
		if dp.Timestamp.After(cutoff) {
			newPoints = append(newPoints, dp)
		}
	}
	p.dataPoints = newPoints

	// Update prediction accuracy
	p.updateAccuracy(point)

	return nil
}

// updateAccuracy updates prediction accuracy based on new actual data.
func (p *MemoryPredictor) updateAccuracy(actual types.MemoryDataPoint) {
	// Find predictions that were made for this timestamp
	for i := range p.predictions {
		if math.Abs(p.predictions[i].forTime.Sub(actual.Timestamp).Hours()) < 1 {
			p.predictions[i].actual = actual.ActualUsageBytes
		}
	}

	// Prune old predictions
	cutoff := time.Now().AddDate(0, 0, -7) // Keep 7 days of prediction records
	newPredictions := make([]predictionRecord, 0)
	for _, pr := range p.predictions {
		if pr.predictedAt.After(cutoff) {
			newPredictions = append(newPredictions, pr)
		}
	}
	p.predictions = newPredictions
}

// Predict predicts memory usage for the next forecastHours hours.
func (p *MemoryPredictor) Predict(forecastHours int) ([]types.PredictionResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.dataPoints) < p.config.SeasonalPeriod*2 {
		return nil, errors.New("insufficient data for prediction, need at least 2 seasonal periods")
	}

	// Prepare usage ratios for prediction
	ratios := make([]float64, len(p.dataPoints))
	for i, dp := range p.dataPoints {
		ratios[i] = dp.UsageRatio
	}

	// Get predictions from both models
	hwPredictions, err := p.holtWinters.Predict(ratios, forecastHours)
	if err != nil {
		return nil, err
	}

	trendPredictions := p.trendAnalyzer.Predict(ratios, forecastHours)

	// Determine if we're in a buffer pool change grace period
	inGracePeriod := p.isInBufferPoolChangeGracePeriod()

	// Combine predictions with weighted ensemble
	results := make([]types.PredictionResult, forecastHours)
	now := time.Now()

	for i := 0; i < forecastHours; i++ {
		// Weight: 60% Holt-Winters, 40% Trend for normal operation
		// During grace period: 40% Holt-Winters, 60% Trend (more conservative)
		var w1, w2 float64
		if inGracePeriod {
			w1, w2 = 0.4, 0.6
		} else {
			w1, w2 = 0.6, 0.4
		}

		combinedRatio := w1*hwPredictions[i] + w2*trendPredictions[i]

		// Apply buffer pool change adjustment if needed
		if inGracePeriod {
			// Add conservative margin during grace period
			combinedRatio = combinedRatio * 1.15 // 15% safety margin
		}

		// Clamp ratio to valid range [0, 1.0]
		combinedRatio = math.Max(0, math.Min(1.0, combinedRatio))

		// Convert ratio back to bytes using current buffer pool
		predictedBytes := int64(combinedRatio * float64(p.currentBufferPool))

		// Calculate confidence interval (using standard deviation approximation)
		stdDev := p.calculateStdDev(ratios)
		confidenceMultiplier := 1.96 // 95% confidence

		results[i] = types.PredictionResult{
			Timestamp:           now.Add(time.Duration(i+1) * time.Hour),
			PredictedUsageBytes: predictedBytes,
			PredictedUsageRatio: combinedRatio,
			ConfidenceLower:     int64(math.Max(0, float64(predictedBytes)-confidenceMultiplier*stdDev*float64(p.currentBufferPool))),
			ConfidenceUpper:     int64(math.Min(float64(p.currentBufferPool), float64(predictedBytes)+confidenceMultiplier*stdDev*float64(p.currentBufferPool))),
			ConfidenceLevel:     0.95,
		}
	}

	// Record predictions for accuracy tracking
	p.mu.RUnlock()
	p.mu.Lock()
	for _, result := range results {
		p.predictions = append(p.predictions, predictionRecord{
			predictedAt: now,
			forTime:     result.Timestamp,
			predicted:   result.PredictedUsageBytes,
		})
	}
	p.mu.Unlock()
	p.mu.RLock()

	return results, nil
}

// isInBufferPoolChangeGracePeriod checks if we're within the grace period after a buffer pool change.
func (p *MemoryPredictor) isInBufferPoolChangeGracePeriod() bool {
	if len(p.bufferPoolChanges) == 0 {
		return false
	}

	lastChange := p.bufferPoolChanges[len(p.bufferPoolChanges)-1]
	return time.Since(lastChange.Timestamp) < p.config.BufferPoolChangeGracePeriod
}

// calculateStdDev calculates the standard deviation of the ratios.
func (p *MemoryPredictor) calculateStdDev(ratios []float64) float64 {
	if len(ratios) == 0 {
		return 0
	}

	// Calculate mean
	sum := 0.0
	for _, r := range ratios {
		sum += r
	}
	mean := sum / float64(len(ratios))

	// Calculate variance
	variance := 0.0
	for _, r := range ratios {
		variance += (r - mean) * (r - mean)
	}
	variance /= float64(len(ratios))

	return math.Sqrt(variance)
}

// GetRecommendedOversellRatio calculates the recommended oversell ratio.
func (p *MemoryPredictor) GetRecommendedOversellRatio(forecastHours int, safetyFactor float64) (float64, error) {
	predictions, err := p.Predict(forecastHours)
	if err != nil {
		return 1.0, err
	}

	// Find the maximum predicted usage ratio
	maxRatio := 0.0
	for _, pred := range predictions {
		// Use upper confidence bound for conservative estimate
		upperRatio := float64(pred.ConfidenceUpper) / float64(p.currentBufferPool)
		if upperRatio > maxRatio {
			maxRatio = upperRatio
		}
	}

	if maxRatio <= 0 {
		return 1.0, nil
	}

	// Calculate oversell ratio: 1 / maxRatio gives us how much we can oversell
	// Apply safety factor to be conservative
	oversellRatio := (1.0 / maxRatio) * safetyFactor

	// Clamp to reasonable range [1.0, 3.0]
	return math.Max(1.0, math.Min(3.0, oversellRatio)), nil
}

// HandleBufferPoolChange handles a buffer pool configuration change.
func (p *MemoryPredictor) HandleBufferPoolChange(event types.BufferPoolChangeEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if event.NewSizeBytes <= 0 {
		return errors.New("new buffer pool size must be positive")
	}

	// Record the change event
	p.bufferPoolChanges = append(p.bufferPoolChanges, event)

	// Update current buffer pool
	p.currentBufferPool = event.NewSizeBytes

	// Adjust historical data weights
	// Data before the change is less reliable, so we might want to give it less weight
	// For now, we just record the event and handle it in predictions

	return nil
}

// GetAccuracy returns the prediction accuracy based on historical predictions.
func (p *MemoryPredictor) GetAccuracy() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.predictions) == 0 {
		return 0
	}

	// Calculate Mean Absolute Percentage Error (MAPE)
	totalError := 0.0
	validCount := 0

	for _, pr := range p.predictions {
		if pr.actual > 0 {
			error := math.Abs(float64(pr.predicted-pr.actual)) / float64(pr.actual)
			totalError += error
			validCount++
		}
	}

	if validCount == 0 {
		return 0
	}

	mape := totalError / float64(validCount)

	// Convert MAPE to accuracy (1 - MAPE, clamped to [0, 1])
	return math.Max(0, math.Min(1, 1-mape))
}

// GetDataPointCount returns the number of data points stored.
func (p *MemoryPredictor) GetDataPointCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.dataPoints)
}

// GetLatestDataPoint returns the most recent data point.
func (p *MemoryPredictor) GetLatestDataPoint() (types.MemoryDataPoint, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.dataPoints) == 0 {
		return types.MemoryDataPoint{}, false
	}

	return p.dataPoints[len(p.dataPoints)-1], true
}

// PredictMaxUsage predicts the maximum memory usage over the forecast period.
func (p *MemoryPredictor) PredictMaxUsage(forecastHours int) (int64, error) {
	predictions, err := p.Predict(forecastHours)
	if err != nil {
		return 0, err
	}

	maxUsage := int64(0)
	for _, pred := range predictions {
		if pred.ConfidenceUpper > maxUsage {
			maxUsage = pred.ConfidenceUpper
		}
	}

	return maxUsage, nil
}

