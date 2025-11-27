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
	"errors"
	"math"
)

// HoltWinters implements the Holt-Winters triple exponential smoothing algorithm.
// It handles level, trend, and seasonality components for time series forecasting.
type HoltWinters struct {
	// alpha is the data smoothing coefficient (0 < alpha < 1).
	alpha float64

	// beta is the trend smoothing coefficient (0 < beta < 1).
	beta float64

	// gamma is the seasonality smoothing coefficient (0 < gamma < 1).
	gamma float64

	// seasonalPeriod is the number of time points in one seasonal cycle.
	seasonalPeriod int

	// level stores the current level component.
	level float64

	// trend stores the current trend component.
	trend float64

	// seasonal stores the seasonal components for one period.
	seasonal []float64

	// initialized indicates whether the model has been initialized.
	initialized bool
}

// NewHoltWinters creates a new HoltWinters predictor.
func NewHoltWinters(alpha, beta, gamma float64, seasonalPeriod int) *HoltWinters {
	return &HoltWinters{
		alpha:          alpha,
		beta:           beta,
		gamma:          gamma,
		seasonalPeriod: seasonalPeriod,
		seasonal:       make([]float64, seasonalPeriod),
		initialized:    false,
	}
}

// initialize initializes the Holt-Winters model with historical data.
func (hw *HoltWinters) initialize(data []float64) error {
	n := len(data)
	if n < hw.seasonalPeriod*2 {
		return errors.New("insufficient data for initialization")
	}

	// Initialize level as the average of the first seasonal period
	sum := 0.0
	for i := 0; i < hw.seasonalPeriod; i++ {
		sum += data[i]
	}
	hw.level = sum / float64(hw.seasonalPeriod)

	// Initialize trend using the difference between first two seasonal periods
	sum1 := 0.0
	sum2 := 0.0
	for i := 0; i < hw.seasonalPeriod; i++ {
		sum1 += data[i]
		sum2 += data[hw.seasonalPeriod+i]
	}
	hw.trend = (sum2 - sum1) / float64(hw.seasonalPeriod*hw.seasonalPeriod)

	// Initialize seasonal components
	for i := 0; i < hw.seasonalPeriod; i++ {
		if hw.level != 0 {
			hw.seasonal[i] = data[i] / hw.level
		} else {
			hw.seasonal[i] = 1.0
		}
	}

	hw.initialized = true
	return nil
}

// Fit fits the model to the historical data.
func (hw *HoltWinters) Fit(data []float64) error {
	if len(data) < hw.seasonalPeriod*2 {
		return errors.New("insufficient data for fitting")
	}

	// Initialize if not done
	if !hw.initialized {
		if err := hw.initialize(data); err != nil {
			return err
		}
	}

	// Update model with each data point
	for i := hw.seasonalPeriod; i < len(data); i++ {
		hw.update(data[i], i)
	}

	return nil
}

// update updates the model with a new observation.
func (hw *HoltWinters) update(observation float64, index int) {
	seasonalIndex := index % hw.seasonalPeriod

	// Store old values
	oldLevel := hw.level
	oldSeasonal := hw.seasonal[seasonalIndex]

	// Prevent division by zero
	if oldSeasonal == 0 {
		oldSeasonal = 1.0
	}

	// Update level
	// L_t = alpha * (Y_t / S_{t-s}) + (1 - alpha) * (L_{t-1} + T_{t-1})
	hw.level = hw.alpha*(observation/oldSeasonal) + (1-hw.alpha)*(oldLevel+hw.trend)

	// Update trend
	// T_t = beta * (L_t - L_{t-1}) + (1 - beta) * T_{t-1}
	hw.trend = hw.beta*(hw.level-oldLevel) + (1-hw.beta)*hw.trend

	// Update seasonal component
	// S_t = gamma * (Y_t / L_t) + (1 - gamma) * S_{t-s}
	if hw.level != 0 {
		hw.seasonal[seasonalIndex] = hw.gamma*(observation/hw.level) + (1-hw.gamma)*oldSeasonal
	}
}

// Predict generates predictions for the next h time periods.
func (hw *HoltWinters) Predict(data []float64, h int) ([]float64, error) {
	if err := hw.Fit(data); err != nil {
		return nil, err
	}

	predictions := make([]float64, h)
	lastIndex := len(data) - 1

	for i := 0; i < h; i++ {
		// F_{t+h} = (L_t + h * T_t) * S_{t+h-s}
		seasonalIndex := (lastIndex + i + 1) % hw.seasonalPeriod
		prediction := (hw.level + float64(i+1)*hw.trend) * hw.seasonal[seasonalIndex]

		// Clamp prediction to valid range
		predictions[i] = math.Max(0, prediction)
	}

	return predictions, nil
}

// GetComponents returns the current level, trend, and seasonal components.
func (hw *HoltWinters) GetComponents() (level, trend float64, seasonal []float64) {
	seasonalCopy := make([]float64, len(hw.seasonal))
	copy(seasonalCopy, hw.seasonal)
	return hw.level, hw.trend, seasonalCopy
}

// SetParameters updates the smoothing parameters.
func (hw *HoltWinters) SetParameters(alpha, beta, gamma float64) error {
	if alpha < 0 || alpha > 1 {
		return errors.New("alpha must be between 0 and 1")
	}
	if beta < 0 || beta > 1 {
		return errors.New("beta must be between 0 and 1")
	}
	if gamma < 0 || gamma > 1 {
		return errors.New("gamma must be between 0 and 1")
	}

	hw.alpha = alpha
	hw.beta = beta
	hw.gamma = gamma
	return nil
}

// Reset resets the model to its initial state.
func (hw *HoltWinters) Reset() {
	hw.level = 0
	hw.trend = 0
	hw.seasonal = make([]float64, hw.seasonalPeriod)
	hw.initialized = false
}

// OptimizeParameters uses grid search to find optimal parameters.
// This is a simple implementation; production use might need more sophisticated optimization.
func (hw *HoltWinters) OptimizeParameters(data []float64, validationRatio float64) (alpha, beta, gamma float64, mse float64) {
	if len(data) < hw.seasonalPeriod*3 {
		return hw.alpha, hw.beta, hw.gamma, math.MaxFloat64
	}

	splitIndex := int(float64(len(data)) * (1 - validationRatio))
	trainData := data[:splitIndex]
	validData := data[splitIndex:]

	bestMSE := math.MaxFloat64
	bestAlpha, bestBeta, bestGamma := hw.alpha, hw.beta, hw.gamma

	// Grid search over parameter space
	alphaValues := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	betaValues := []float64{0.05, 0.1, 0.15, 0.2}
	gammaValues := []float64{0.1, 0.2, 0.3}

	for _, a := range alphaValues {
		for _, b := range betaValues {
			for _, g := range gammaValues {
				// Create temporary predictor
				temp := NewHoltWinters(a, b, g, hw.seasonalPeriod)
				predictions, err := temp.Predict(trainData, len(validData))
				if err != nil {
					continue
				}

				// Calculate MSE
				mseVal := calculateMSE(predictions, validData)
				if mseVal < bestMSE {
					bestMSE = mseVal
					bestAlpha, bestBeta, bestGamma = a, b, g
				}
			}
		}
	}

	return bestAlpha, bestBeta, bestGamma, bestMSE
}

// calculateMSE calculates the Mean Squared Error between predictions and actual values.
func calculateMSE(predictions, actual []float64) float64 {
	if len(predictions) != len(actual) || len(predictions) == 0 {
		return math.MaxFloat64
	}

	sumSquaredError := 0.0
	for i := range predictions {
		diff := predictions[i] - actual[i]
		sumSquaredError += diff * diff
	}

	return sumSquaredError / float64(len(predictions))
}

// AdditiveHoltWinters implements additive Holt-Winters (useful when seasonal variation is constant).
type AdditiveHoltWinters struct {
	alpha          float64
	beta           float64
	gamma          float64
	seasonalPeriod int
	level          float64
	trend          float64
	seasonal       []float64
	initialized    bool
}

// NewAdditiveHoltWinters creates a new additive Holt-Winters predictor.
func NewAdditiveHoltWinters(alpha, beta, gamma float64, seasonalPeriod int) *AdditiveHoltWinters {
	return &AdditiveHoltWinters{
		alpha:          alpha,
		beta:           beta,
		gamma:          gamma,
		seasonalPeriod: seasonalPeriod,
		seasonal:       make([]float64, seasonalPeriod),
		initialized:    false,
	}
}

// Predict generates predictions using additive seasonal model.
func (hw *AdditiveHoltWinters) Predict(data []float64, h int) ([]float64, error) {
	n := len(data)
	if n < hw.seasonalPeriod*2 {
		return nil, errors.New("insufficient data for prediction")
	}

	// Initialize
	sum := 0.0
	for i := 0; i < hw.seasonalPeriod; i++ {
		sum += data[i]
	}
	hw.level = sum / float64(hw.seasonalPeriod)

	// Initialize trend
	sum1 := 0.0
	sum2 := 0.0
	for i := 0; i < hw.seasonalPeriod; i++ {
		sum1 += data[i]
		sum2 += data[hw.seasonalPeriod+i]
	}
	hw.trend = (sum2 - sum1) / float64(hw.seasonalPeriod*hw.seasonalPeriod)

	// Initialize seasonal (additive)
	for i := 0; i < hw.seasonalPeriod; i++ {
		hw.seasonal[i] = data[i] - hw.level
	}

	// Fit to remaining data
	for i := hw.seasonalPeriod; i < n; i++ {
		seasonalIndex := i % hw.seasonalPeriod
		oldLevel := hw.level
		oldSeasonal := hw.seasonal[seasonalIndex]

		// Update level (additive)
		hw.level = hw.alpha*(data[i]-oldSeasonal) + (1-hw.alpha)*(oldLevel+hw.trend)

		// Update trend
		hw.trend = hw.beta*(hw.level-oldLevel) + (1-hw.beta)*hw.trend

		// Update seasonal (additive)
		hw.seasonal[seasonalIndex] = hw.gamma*(data[i]-hw.level) + (1-hw.gamma)*oldSeasonal
	}

	// Generate predictions
	predictions := make([]float64, h)
	for i := 0; i < h; i++ {
		seasonalIndex := (n + i) % hw.seasonalPeriod
		predictions[i] = hw.level + float64(i+1)*hw.trend + hw.seasonal[seasonalIndex]
		predictions[i] = math.Max(0, predictions[i])
	}

	return predictions, nil
}

