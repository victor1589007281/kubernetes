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
	"sort"
	"sync"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
)

// AlgorithmType represents the type of prediction algorithm.
type AlgorithmType string

const (
	AlgorithmHoltWinters AlgorithmType = "holt_winters"
	AlgorithmARIMA       AlgorithmType = "arima"
	AlgorithmSARIMA      AlgorithmType = "sarima"
	AlgorithmProphet     AlgorithmType = "prophet"
	AlgorithmLSTM        AlgorithmType = "lstm"
	AlgorithmLinear      AlgorithmType = "linear"
	AlgorithmEnsemble    AlgorithmType = "ensemble"
)

// ModelResult holds the result of a single model's prediction.
type ModelResult struct {
	Algorithm   AlgorithmType
	Predictions []float64
	MAPE        float64
	RMSE        float64
	MAE         float64
	R2          float64
	TrainTime   time.Duration
}

// EnsemblePredictor runs multiple algorithms and selects the best one.
type EnsemblePredictor struct {
	mu sync.RWMutex

	// config holds the predictor configuration.
	config types.PredictorConfig

	// dataPoints stores historical data.
	dataPoints []types.MemoryDataPoint

	// models stores the individual model instances.
	holtWinters *HoltWinters
	arima       *ARIMA
	sarima      *SARIMA
	prophet     *ProphetLike
	lstm        *SimpleLSTM
	trend       *TrendAnalyzer

	// lastEvaluation stores the last model evaluation results.
	lastEvaluation []ModelResult

	// selectedAlgorithm is the currently selected best algorithm.
	selectedAlgorithm AlgorithmType

	// ensembleWeights stores weights for ensemble prediction.
	ensembleWeights map[AlgorithmType]float64

	// currentBufferPool is the current buffer pool size.
	currentBufferPool int64

	// bufferPoolChanges stores buffer pool change events.
	bufferPoolChanges []types.BufferPoolChangeEvent
}

// NewEnsemblePredictor creates a new EnsemblePredictor.
func NewEnsemblePredictor(config types.PredictorConfig) *EnsemblePredictor {
	return &EnsemblePredictor{
		config:            config,
		dataPoints:        make([]types.MemoryDataPoint, 0),
		holtWinters:       NewHoltWinters(config.Alpha, config.Beta, config.Gamma, config.SeasonalPeriod),
		arima:             NewARIMA(2, 1, 2), // Default ARIMA(2,1,2)
		sarima:            NewSARIMA(1, 1, 1, 1, 1, 1, config.SeasonalPeriod),
		prophet:           NewProphetLike(config.SeasonalPeriod),
		lstm:              NewSimpleLSTM(48, 24, config.SeasonalPeriod),
		trend:             NewTrendAnalyzer(24, 72),
		selectedAlgorithm: AlgorithmHoltWinters,
		ensembleWeights:   make(map[AlgorithmType]float64),
		bufferPoolChanges: make([]types.BufferPoolChangeEvent, 0),
	}
}

// AddDataPoint adds a new data point and triggers model evaluation.
func (e *EnsemblePredictor) AddDataPoint(point types.MemoryDataPoint) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Calculate usage ratio if not set
	if point.BufferPoolBytes > 0 && point.UsageRatio == 0 {
		point.UsageRatio = float64(point.ActualUsageBytes) / float64(point.BufferPoolBytes)
	}

	e.currentBufferPool = point.BufferPoolBytes
	e.dataPoints = append(e.dataPoints, point)

	// Sort by timestamp
	sort.Slice(e.dataPoints, func(i, j int) bool {
		return e.dataPoints[i].Timestamp.Before(e.dataPoints[j].Timestamp)
	})

	// Prune old data
	cutoff := time.Now().AddDate(0, 0, -e.config.HistoryDays)
	newPoints := make([]types.MemoryDataPoint, 0)
	for _, dp := range e.dataPoints {
		if dp.Timestamp.After(cutoff) {
			newPoints = append(newPoints, dp)
		}
	}
	e.dataPoints = newPoints

	return nil
}

// EvaluateModels evaluates all models and selects the best one.
func (e *EnsemblePredictor) EvaluateModels() ([]ModelResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.dataPoints) < e.config.SeasonalPeriod*2 {
		return nil, nil // Not enough data
	}

	// Extract usage ratios
	ratios := make([]float64, len(e.dataPoints))
	for i, dp := range e.dataPoints {
		ratios[i] = dp.UsageRatio
	}

	// Split data: 80% train, 20% validation
	splitIdx := int(float64(len(ratios)) * 0.8)
	trainData := ratios[:splitIdx]
	valData := ratios[splitIdx:]

	results := make([]ModelResult, 0)

	// Evaluate each model
	// 1. Holt-Winters
	hwResult := e.evaluateHoltWinters(trainData, valData)
	results = append(results, hwResult)

	// 2. ARIMA (with auto-selection)
	arimaResult := e.evaluateARIMA(trainData, valData)
	results = append(results, arimaResult)

	// 3. SARIMA
	sarimaResult := e.evaluateSARIMA(trainData, valData)
	results = append(results, sarimaResult)

	// 4. Prophet-like
	prophetResult := e.evaluateProphet(trainData, valData)
	results = append(results, prophetResult)

	// 5. LSTM
	lstmResult := e.evaluateLSTM(trainData, valData)
	results = append(results, lstmResult)

	// 6. Linear (baseline)
	linearResult := e.evaluateLinear(trainData, valData)
	results = append(results, linearResult)

	// Sort by MAPE (ascending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].MAPE < results[j].MAPE
	})

	e.lastEvaluation = results

	// Select best model
	if len(results) > 0 {
		e.selectedAlgorithm = results[0].Algorithm
	}

	// Calculate ensemble weights based on inverse MAPE
	e.calculateEnsembleWeights(results)

	return results, nil
}

// evaluateHoltWinters evaluates the Holt-Winters model.
func (e *EnsemblePredictor) evaluateHoltWinters(train, val []float64) ModelResult {
	start := time.Now()
	predictions, err := e.holtWinters.Predict(train, len(val))

	result := ModelResult{
		Algorithm: AlgorithmHoltWinters,
		TrainTime: time.Since(start),
	}

	if err != nil || len(predictions) != len(val) {
		result.MAPE = math.MaxFloat64
		return result
	}

	result.Predictions = predictions
	result.MAPE = calculateMAPE(predictions, val)
	result.RMSE = math.Sqrt(calculateMSE(predictions, val))
	result.MAE = calculateMAE(predictions, val)
	result.R2 = calculateR2(predictions, val)

	return result
}

// evaluateARIMA evaluates the ARIMA model with auto-parameter selection.
func (e *EnsemblePredictor) evaluateARIMA(train, val []float64) ModelResult {
	start := time.Now()

	// Use auto-ARIMA to find best parameters
	bestModel, _ := AutoARIMA(train, 3, 2, 3)
	predictions, err := bestModel.Predict(train, len(val))

	result := ModelResult{
		Algorithm: AlgorithmARIMA,
		TrainTime: time.Since(start),
	}

	if err != nil || len(predictions) != len(val) {
		result.MAPE = math.MaxFloat64
		return result
	}

	// Store the best model
	e.arima = bestModel

	result.Predictions = predictions
	result.MAPE = calculateMAPE(predictions, val)
	result.RMSE = math.Sqrt(calculateMSE(predictions, val))
	result.MAE = calculateMAE(predictions, val)
	result.R2 = calculateR2(predictions, val)

	return result
}

// evaluateSARIMA evaluates the SARIMA model.
func (e *EnsemblePredictor) evaluateSARIMA(train, val []float64) ModelResult {
	start := time.Now()
	predictions, err := e.sarima.Predict(train, len(val))

	result := ModelResult{
		Algorithm: AlgorithmSARIMA,
		TrainTime: time.Since(start),
	}

	if err != nil || len(predictions) != len(val) {
		result.MAPE = math.MaxFloat64
		return result
	}

	result.Predictions = predictions
	result.MAPE = calculateMAPE(predictions, val)
	result.RMSE = math.Sqrt(calculateMSE(predictions, val))
	result.MAE = calculateMAE(predictions, val)
	result.R2 = calculateR2(predictions, val)

	return result
}

// evaluateProphet evaluates the Prophet-like model.
func (e *EnsemblePredictor) evaluateProphet(train, val []float64) ModelResult {
	start := time.Now()
	predictions, err := e.prophet.Predict(train, len(val))

	result := ModelResult{
		Algorithm: AlgorithmProphet,
		TrainTime: time.Since(start),
	}

	if err != nil || len(predictions) != len(val) {
		result.MAPE = math.MaxFloat64
		return result
	}

	result.Predictions = predictions
	result.MAPE = calculateMAPE(predictions, val)
	result.RMSE = math.Sqrt(calculateMSE(predictions, val))
	result.MAE = calculateMAE(predictions, val)
	result.R2 = calculateR2(predictions, val)

	return result
}

// evaluateLSTM evaluates the LSTM model.
func (e *EnsemblePredictor) evaluateLSTM(train, val []float64) ModelResult {
	start := time.Now()
	predictions, err := e.lstm.Predict(train, len(val))

	result := ModelResult{
		Algorithm: AlgorithmLSTM,
		TrainTime: time.Since(start),
	}

	if err != nil || len(predictions) != len(val) {
		result.MAPE = math.MaxFloat64
		return result
	}

	result.Predictions = predictions
	result.MAPE = calculateMAPE(predictions, val)
	result.RMSE = math.Sqrt(calculateMSE(predictions, val))
	result.MAE = calculateMAE(predictions, val)
	result.R2 = calculateR2(predictions, val)

	return result
}

// evaluateLinear evaluates a simple linear regression model.
func (e *EnsemblePredictor) evaluateLinear(train, val []float64) ModelResult {
	start := time.Now()
	predictions := e.trend.PredictWithRegression(train, len(val))

	result := ModelResult{
		Algorithm:   AlgorithmLinear,
		Predictions: predictions,
		TrainTime:   time.Since(start),
	}

	if len(predictions) != len(val) {
		result.MAPE = math.MaxFloat64
		return result
	}

	result.MAPE = calculateMAPE(predictions, val)
	result.RMSE = math.Sqrt(calculateMSE(predictions, val))
	result.MAE = calculateMAE(predictions, val)
	result.R2 = calculateR2(predictions, val)

	return result
}

// calculateEnsembleWeights calculates weights for ensemble prediction.
func (e *EnsemblePredictor) calculateEnsembleWeights(results []ModelResult) {
	// Use inverse MAPE as weight (lower MAPE = higher weight)
	totalInvMAPE := 0.0
	for _, r := range results {
		if r.MAPE > 0 && r.MAPE < math.MaxFloat64 {
			totalInvMAPE += 1.0 / r.MAPE
		}
	}

	if totalInvMAPE == 0 {
		// Equal weights if all models failed
		for _, r := range results {
			e.ensembleWeights[r.Algorithm] = 1.0 / float64(len(results))
		}
		return
	}

	for _, r := range results {
		if r.MAPE > 0 && r.MAPE < math.MaxFloat64 {
			e.ensembleWeights[r.Algorithm] = (1.0 / r.MAPE) / totalInvMAPE
		} else {
			e.ensembleWeights[r.Algorithm] = 0
		}
	}
}

// Predict generates predictions using the best or ensemble model.
func (e *EnsemblePredictor) Predict(forecastHours int) ([]types.PredictionResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.dataPoints) < e.config.SeasonalPeriod*2 {
		return nil, nil
	}

	// Extract ratios
	ratios := make([]float64, len(e.dataPoints))
	for i, dp := range e.dataPoints {
		ratios[i] = dp.UsageRatio
	}

	// Get predictions from all models
	predictions := make(map[AlgorithmType][]float64)

	if hwPred, err := e.holtWinters.Predict(ratios, forecastHours); err == nil {
		predictions[AlgorithmHoltWinters] = hwPred
	}

	if arimaPred, err := e.arima.Predict(ratios, forecastHours); err == nil {
		predictions[AlgorithmARIMA] = arimaPred
	}

	if sarimaPred, err := e.sarima.Predict(ratios, forecastHours); err == nil {
		predictions[AlgorithmSARIMA] = sarimaPred
	}

	if prophetPred, err := e.prophet.Predict(ratios, forecastHours); err == nil {
		predictions[AlgorithmProphet] = prophetPred
	}

	if lstmPred, err := e.lstm.Predict(ratios, forecastHours); err == nil {
		predictions[AlgorithmLSTM] = lstmPred
	}

	// Use ensemble or best model based on configuration
	var finalPredictions []float64

	if len(e.ensembleWeights) > 0 {
		// Ensemble prediction (weighted average of top models)
		finalPredictions = e.ensemblePrediction(predictions, forecastHours)
	} else if preds, ok := predictions[e.selectedAlgorithm]; ok {
		finalPredictions = preds
	} else {
		// Fallback to any available model
		for _, preds := range predictions {
			finalPredictions = preds
			break
		}
	}

	if finalPredictions == nil {
		return nil, nil
	}

	// Check if in buffer pool change grace period
	inGracePeriod := e.isInBufferPoolChangeGracePeriod()

	// Convert to PredictionResult
	results := make([]types.PredictionResult, forecastHours)
	now := time.Now()
	stdDev := e.calculateStdDev(ratios)

	for i := 0; i < forecastHours; i++ {
		ratio := finalPredictions[i]

		// Apply safety margin during grace period
		if inGracePeriod {
			ratio = ratio * 1.15
		}

		// Clamp to valid range
		ratio = math.Max(0, math.Min(1, ratio))

		predictedBytes := int64(ratio * float64(e.currentBufferPool))
		confidenceMultiplier := 1.96 // 95% confidence

		results[i] = types.PredictionResult{
			Timestamp:           now.Add(time.Duration(i+1) * time.Hour),
			PredictedUsageBytes: predictedBytes,
			PredictedUsageRatio: ratio,
			ConfidenceLower:     int64(math.Max(0, float64(predictedBytes)-confidenceMultiplier*stdDev*float64(e.currentBufferPool))),
			ConfidenceUpper:     int64(math.Min(float64(e.currentBufferPool), float64(predictedBytes)+confidenceMultiplier*stdDev*float64(e.currentBufferPool))),
			ConfidenceLevel:     0.95,
		}
	}

	return results, nil
}

// ensemblePrediction calculates weighted average of predictions.
func (e *EnsemblePredictor) ensemblePrediction(predictions map[AlgorithmType][]float64, hours int) []float64 {
	result := make([]float64, hours)

	for i := 0; i < hours; i++ {
		weightedSum := 0.0
		totalWeight := 0.0

		for alg, preds := range predictions {
			if i < len(preds) {
				weight := e.ensembleWeights[alg]
				weightedSum += preds[i] * weight
				totalWeight += weight
			}
		}

		if totalWeight > 0 {
			result[i] = weightedSum / totalWeight
		}
	}

	return result
}

// isInBufferPoolChangeGracePeriod checks if we're in grace period.
func (e *EnsemblePredictor) isInBufferPoolChangeGracePeriod() bool {
	if len(e.bufferPoolChanges) == 0 {
		return false
	}
	lastChange := e.bufferPoolChanges[len(e.bufferPoolChanges)-1]
	return time.Since(lastChange.Timestamp) < e.config.BufferPoolChangeGracePeriod
}

// calculateStdDev calculates standard deviation.
func (e *EnsemblePredictor) calculateStdDev(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}

	mean := 0.0
	for _, v := range data {
		mean += v
	}
	mean /= float64(len(data))

	variance := 0.0
	for _, v := range data {
		variance += (v - mean) * (v - mean)
	}
	variance /= float64(len(data))

	return math.Sqrt(variance)
}

// HandleBufferPoolChange handles a buffer pool change event.
func (e *EnsemblePredictor) HandleBufferPoolChange(event types.BufferPoolChangeEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.bufferPoolChanges = append(e.bufferPoolChanges, event)
	e.currentBufferPool = event.NewSizeBytes

	return nil
}

// GetSelectedAlgorithm returns the currently selected algorithm.
func (e *EnsemblePredictor) GetSelectedAlgorithm() AlgorithmType {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.selectedAlgorithm
}

// GetLastEvaluation returns the last model evaluation results.
func (e *EnsemblePredictor) GetLastEvaluation() []ModelResult {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]ModelResult, len(e.lastEvaluation))
	copy(result, e.lastEvaluation)
	return result
}

// GetRecommendedOversellRatio returns the recommended oversell ratio.
func (e *EnsemblePredictor) GetRecommendedOversellRatio(forecastHours int, safetyFactor float64) (float64, error) {
	predictions, err := e.Predict(forecastHours)
	if err != nil || len(predictions) == 0 {
		return 1.0, err
	}

	// Find maximum predicted usage ratio
	maxRatio := 0.0
	for _, pred := range predictions {
		upperRatio := float64(pred.ConfidenceUpper) / float64(e.currentBufferPool)
		if upperRatio > maxRatio {
			maxRatio = upperRatio
		}
	}

	if maxRatio <= 0 {
		return 1.0, nil
	}

	// Calculate oversell ratio
	oversellRatio := (1.0 / maxRatio) * safetyFactor
	return math.Max(1.0, math.Min(3.0, oversellRatio)), nil
}

// GetDataPointCount returns the number of data points.
func (e *EnsemblePredictor) GetDataPointCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.dataPoints)
}

// Helper functions for metrics calculation
func calculateMAPE(predicted, actual []float64) float64 {
	if len(predicted) != len(actual) || len(predicted) == 0 {
		return math.MaxFloat64
	}

	sum := 0.0
	count := 0
	for i := range predicted {
		if actual[i] != 0 {
			sum += math.Abs(predicted[i]-actual[i]) / math.Abs(actual[i])
			count++
		}
	}

	if count == 0 {
		return math.MaxFloat64
	}

	return (sum / float64(count)) * 100 // Percentage
}

func calculateMAE(predicted, actual []float64) float64 {
	if len(predicted) != len(actual) || len(predicted) == 0 {
		return math.MaxFloat64
	}

	sum := 0.0
	for i := range predicted {
		sum += math.Abs(predicted[i] - actual[i])
	}

	return sum / float64(len(predicted))
}

func calculateR2(predicted, actual []float64) float64 {
	if len(predicted) != len(actual) || len(predicted) == 0 {
		return 0
	}

	// Calculate mean of actual
	meanActual := 0.0
	for _, v := range actual {
		meanActual += v
	}
	meanActual /= float64(len(actual))

	// Calculate SS_res and SS_tot
	ssRes := 0.0
	ssTot := 0.0
	for i := range actual {
		ssRes += (actual[i] - predicted[i]) * (actual[i] - predicted[i])
		ssTot += (actual[i] - meanActual) * (actual[i] - meanActual)
	}

	if ssTot == 0 {
		return 0
	}

	return 1 - (ssRes / ssTot)
}



