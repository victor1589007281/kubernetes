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

// ARIMA implements the AutoRegressive Integrated Moving Average model.
// ARIMA(p, d, q) where:
//   - p: order of autoregressive part
//   - d: degree of differencing
//   - q: order of moving average part
type ARIMA struct {
	p int // AR order
	d int // Differencing order
	q int // MA order

	// Estimated coefficients
	arCoeffs []float64 // AR coefficients (φ)
	maCoeffs []float64 // MA coefficients (θ)
	constant float64   // Constant term

	// Training data
	residuals []float64
	diffData  []float64
}

// NewARIMA creates a new ARIMA model with specified orders.
func NewARIMA(p, d, q int) *ARIMA {
	return &ARIMA{
		p:        p,
		d:        d,
		q:        q,
		arCoeffs: make([]float64, p),
		maCoeffs: make([]float64, q),
	}
}

// Fit fits the ARIMA model to the data.
func (a *ARIMA) Fit(data []float64) error {
	if len(data) < a.p+a.d+a.q+10 {
		return errors.New("insufficient data for ARIMA fitting")
	}

	// Step 1: Differencing
	a.diffData = a.difference(data, a.d)

	// Step 2: Estimate AR coefficients using Yule-Walker equations
	if a.p > 0 {
		a.arCoeffs = a.estimateARCoeffs(a.diffData)
	}

	// Step 3: Estimate MA coefficients (simplified approach)
	if a.q > 0 {
		a.residuals = a.calculateResiduals(a.diffData)
		a.maCoeffs = a.estimateMACoeffs(a.residuals)
	}

	// Estimate constant
	sum := 0.0
	for _, v := range a.diffData {
		sum += v
	}
	a.constant = sum / float64(len(a.diffData))

	return nil
}

// difference applies differencing d times.
func (a *ARIMA) difference(data []float64, d int) []float64 {
	result := make([]float64, len(data))
	copy(result, data)

	for i := 0; i < d; i++ {
		newResult := make([]float64, len(result)-1)
		for j := 1; j < len(result); j++ {
			newResult[j-1] = result[j] - result[j-1]
		}
		result = newResult
	}

	return result
}

// undifference reverses the differencing operation.
func (a *ARIMA) undifference(predictions []float64, lastValues []float64) []float64 {
	result := make([]float64, len(predictions))
	copy(result, predictions)

	for i := a.d - 1; i >= 0; i-- {
		lastVal := lastValues[i]
		for j := 0; j < len(result); j++ {
			result[j] = result[j] + lastVal
			lastVal = result[j]
		}
	}

	return result
}

// estimateARCoeffs estimates AR coefficients using Yule-Walker equations.
func (a *ARIMA) estimateARCoeffs(data []float64) []float64 {
	n := len(data)
	if n <= a.p {
		return make([]float64, a.p)
	}

	// Calculate autocorrelations
	mean := 0.0
	for _, v := range data {
		mean += v
	}
	mean /= float64(n)

	// Calculate autocovariances
	gamma := make([]float64, a.p+1)
	for k := 0; k <= a.p; k++ {
		sum := 0.0
		for t := k; t < n; t++ {
			sum += (data[t] - mean) * (data[t-k] - mean)
		}
		gamma[k] = sum / float64(n)
	}

	if gamma[0] == 0 {
		return make([]float64, a.p)
	}

	// Solve Yule-Walker equations using Levinson-Durbin algorithm
	phi := make([]float64, a.p)
	if a.p == 1 {
		phi[0] = gamma[1] / gamma[0]
	} else {
		// Simplified: use partial autocorrelations
		for i := 0; i < a.p; i++ {
			if i == 0 {
				phi[i] = gamma[1] / gamma[0]
			} else {
				// Approximate higher order coefficients
				phi[i] = gamma[i+1] / gamma[0] * math.Pow(0.9, float64(i))
			}
		}
	}

	return phi
}

// calculateResiduals calculates residuals from AR model.
func (a *ARIMA) calculateResiduals(data []float64) []float64 {
	n := len(data)
	residuals := make([]float64, n)

	for t := a.p; t < n; t++ {
		predicted := a.constant
		for i := 0; i < a.p; i++ {
			if t-i-1 >= 0 {
				predicted += a.arCoeffs[i] * data[t-i-1]
			}
		}
		residuals[t] = data[t] - predicted
	}

	return residuals
}

// estimateMACoeffs estimates MA coefficients from residuals.
func (a *ARIMA) estimateMACoeffs(residuals []float64) []float64 {
	n := len(residuals)
	if n <= a.q {
		return make([]float64, a.q)
	}

	// Calculate autocorrelation of residuals
	mean := 0.0
	count := 0
	for _, r := range residuals {
		if r != 0 {
			mean += r
			count++
		}
	}
	if count > 0 {
		mean /= float64(count)
	}

	theta := make([]float64, a.q)
	for k := 1; k <= a.q; k++ {
		numerator := 0.0
		denominator := 0.0
		for t := k; t < n; t++ {
			numerator += (residuals[t] - mean) * (residuals[t-k] - mean)
			denominator += (residuals[t] - mean) * (residuals[t] - mean)
		}
		if denominator != 0 {
			theta[k-1] = numerator / denominator
		}
	}

	return theta
}

// Predict generates predictions for h steps ahead.
func (a *ARIMA) Predict(data []float64, h int) ([]float64, error) {
	if err := a.Fit(data); err != nil {
		return nil, err
	}

	n := len(a.diffData)
	predictions := make([]float64, h)

	// Prepare last values for undifferencing
	lastValues := make([]float64, a.d)
	for i := 0; i < a.d && i < len(data); i++ {
		lastValues[i] = data[len(data)-1-i]
	}

	// Generate predictions on differenced data
	extendedData := make([]float64, n+h)
	copy(extendedData, a.diffData)

	extendedResiduals := make([]float64, n+h)
	copy(extendedResiduals, a.residuals)

	for t := 0; t < h; t++ {
		idx := n + t
		prediction := a.constant

		// AR component
		for i := 0; i < a.p; i++ {
			if idx-i-1 >= 0 {
				prediction += a.arCoeffs[i] * extendedData[idx-i-1]
			}
		}

		// MA component
		for i := 0; i < a.q; i++ {
			if idx-i-1 >= 0 && idx-i-1 < len(extendedResiduals) {
				prediction += a.maCoeffs[i] * extendedResiduals[idx-i-1]
			}
		}

		extendedData[idx] = prediction
		predictions[t] = prediction
	}

	// Undifference to get actual predictions
	if a.d > 0 {
		predictions = a.undifference(predictions, lastValues)
	}

	// Ensure predictions are in valid range [0, 1] for usage ratios
	for i := range predictions {
		predictions[i] = math.Max(0, math.Min(1, predictions[i]))
	}

	return predictions, nil
}

// AutoARIMA automatically selects the best ARIMA parameters.
func AutoARIMA(data []float64, maxP, maxD, maxQ int) (*ARIMA, float64) {
	if len(data) < 20 {
		return NewARIMA(1, 1, 1), math.MaxFloat64
	}

	// Split data for validation
	splitIdx := int(float64(len(data)) * 0.8)
	trainData := data[:splitIdx]
	testData := data[splitIdx:]

	bestAIC := math.MaxFloat64
	var bestModel *ARIMA

	// Grid search over parameters
	for p := 0; p <= maxP; p++ {
		for d := 0; d <= maxD; d++ {
			for q := 0; q <= maxQ; q++ {
				if p == 0 && q == 0 {
					continue // Skip trivial model
				}

				model := NewARIMA(p, d, q)
				predictions, err := model.Predict(trainData, len(testData))
				if err != nil {
					continue
				}

				// Calculate AIC-like criterion
				mse := calculateMSE(predictions, testData)
				k := float64(p + q + 1) // Number of parameters
				n := float64(len(testData))
				aic := n*math.Log(mse) + 2*k

				if aic < bestAIC {
					bestAIC = aic
					bestModel = model
				}
			}
		}
	}

	if bestModel == nil {
		return NewARIMA(1, 1, 1), math.MaxFloat64
	}

	return bestModel, bestAIC
}

// GetParameters returns the ARIMA parameters.
func (a *ARIMA) GetParameters() (p, d, q int) {
	return a.p, a.d, a.q
}

// GetCoefficients returns the estimated coefficients.
func (a *ARIMA) GetCoefficients() (ar, ma []float64, constant float64) {
	arCopy := make([]float64, len(a.arCoeffs))
	maCopy := make([]float64, len(a.maCoeffs))
	copy(arCopy, a.arCoeffs)
	copy(maCopy, a.maCoeffs)
	return arCopy, maCopy, a.constant
}

// SARIMA extends ARIMA with seasonal components.
type SARIMA struct {
	*ARIMA
	P      int // Seasonal AR order
	D      int // Seasonal differencing
	Q      int // Seasonal MA order
	Season int // Seasonal period

	sarCoeffs []float64
	smaCoeffs []float64
}

// NewSARIMA creates a new SARIMA model.
func NewSARIMA(p, d, q, P, D, Q, season int) *SARIMA {
	return &SARIMA{
		ARIMA:     NewARIMA(p, d, q),
		P:         P,
		D:         D,
		Q:         Q,
		Season:    season,
		sarCoeffs: make([]float64, P),
		smaCoeffs: make([]float64, Q),
	}
}

// Predict generates predictions using SARIMA.
func (s *SARIMA) Predict(data []float64, h int) ([]float64, error) {
	// Apply seasonal differencing
	seasonalDiff := s.seasonalDifference(data)

	// Fit non-seasonal ARIMA on seasonally differenced data
	predictions, err := s.ARIMA.Predict(seasonalDiff, h)
	if err != nil {
		return nil, err
	}

	// Add seasonal component back
	predictions = s.addSeasonality(predictions, data)

	return predictions, nil
}

// seasonalDifference applies seasonal differencing.
func (s *SARIMA) seasonalDifference(data []float64) []float64 {
	if len(data) <= s.Season {
		return data
	}

	result := make([]float64, len(data)-s.Season)
	for i := s.Season; i < len(data); i++ {
		result[i-s.Season] = data[i] - data[i-s.Season]
	}

	return result
}

// addSeasonality adds the seasonal component back to predictions.
func (s *SARIMA) addSeasonality(predictions []float64, originalData []float64) []float64 {
	n := len(originalData)
	result := make([]float64, len(predictions))

	for i := range predictions {
		seasonalIdx := (n + i) % s.Season
		if seasonalIdx < len(originalData) {
			// Add back the seasonal pattern from the last observed cycle
			lastCycleIdx := n - s.Season + seasonalIdx
			if lastCycleIdx >= 0 && lastCycleIdx < n {
				result[i] = predictions[i] + originalData[lastCycleIdx]
			} else {
				result[i] = predictions[i]
			}
		} else {
			result[i] = predictions[i]
		}
	}

	return result
}



