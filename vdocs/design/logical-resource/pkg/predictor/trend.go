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
)

// TrendAnalyzer performs trend analysis using moving averages.
type TrendAnalyzer struct {
	// windowSize is the size of the moving average window.
	windowSize int

	// trendPeriod is the period for trend calculation.
	trendPeriod int
}

// NewTrendAnalyzer creates a new TrendAnalyzer.
func NewTrendAnalyzer(windowSize, trendPeriod int) *TrendAnalyzer {
	return &TrendAnalyzer{
		windowSize:  windowSize,
		trendPeriod: trendPeriod,
	}
}

// MovingAverage calculates the simple moving average for each point in the data.
func (ta *TrendAnalyzer) MovingAverage(data []float64) []float64 {
	n := len(data)
	if n == 0 {
		return []float64{}
	}

	result := make([]float64, n)

	for i := 0; i < n; i++ {
		start := i - ta.windowSize + 1
		if start < 0 {
			start = 0
		}

		sum := 0.0
		count := 0
		for j := start; j <= i; j++ {
			sum += data[j]
			count++
		}
		result[i] = sum / float64(count)
	}

	return result
}

// ExponentialMovingAverage calculates the exponential moving average.
func (ta *TrendAnalyzer) ExponentialMovingAverage(data []float64, alpha float64) []float64 {
	n := len(data)
	if n == 0 {
		return []float64{}
	}

	result := make([]float64, n)
	result[0] = data[0]

	for i := 1; i < n; i++ {
		result[i] = alpha*data[i] + (1-alpha)*result[i-1]
	}

	return result
}

// CalculateTrend calculates the trend component from the data.
func (ta *TrendAnalyzer) CalculateTrend(data []float64) float64 {
	n := len(data)
	if n < ta.trendPeriod {
		return 0
	}

	ma := ta.MovingAverage(data)

	// Calculate trend as the difference between recent and earlier moving averages
	recent := ma[n-1]
	earlier := ma[n-ta.trendPeriod]

	return (recent - earlier) / float64(ta.trendPeriod)
}

// Predict predicts future values based on trend analysis.
func (ta *TrendAnalyzer) Predict(data []float64, h int) []float64 {
	n := len(data)
	if n < ta.windowSize {
		// Not enough data, return last known value
		predictions := make([]float64, h)
		lastValue := 0.0
		if n > 0 {
			lastValue = data[n-1]
		}
		for i := 0; i < h; i++ {
			predictions[i] = lastValue
		}
		return predictions
	}

	ma := ta.MovingAverage(data)
	trend := ta.CalculateTrend(data)

	predictions := make([]float64, h)
	lastMA := ma[n-1]

	for i := 0; i < h; i++ {
		prediction := lastMA + trend*float64(i+1)
		// Clamp to valid range
		predictions[i] = math.Max(0, math.Min(1, prediction))
	}

	return predictions
}

// WeightedMovingAverage calculates a weighted moving average where recent values have higher weight.
func (ta *TrendAnalyzer) WeightedMovingAverage(data []float64) []float64 {
	n := len(data)
	if n == 0 {
		return []float64{}
	}

	result := make([]float64, n)

	for i := 0; i < n; i++ {
		start := i - ta.windowSize + 1
		if start < 0 {
			start = 0
		}

		weightSum := 0.0
		valueSum := 0.0
		for j := start; j <= i; j++ {
			weight := float64(j - start + 1)
			weightSum += weight
			valueSum += data[j] * weight
		}
		result[i] = valueSum / weightSum
	}

	return result
}

// DetectAnomaly detects anomalies using the moving average and standard deviation.
func (ta *TrendAnalyzer) DetectAnomaly(data []float64, threshold float64) []bool {
	n := len(data)
	if n == 0 {
		return []bool{}
	}

	ma := ta.MovingAverage(data)
	result := make([]bool, n)

	for i := 0; i < n; i++ {
		start := i - ta.windowSize + 1
		if start < 0 {
			start = 0
		}

		// Calculate local standard deviation
		variance := 0.0
		count := 0
		for j := start; j <= i; j++ {
			diff := data[j] - ma[i]
			variance += diff * diff
			count++
		}
		if count > 0 {
			stdDev := math.Sqrt(variance / float64(count))
			if stdDev > 0 {
				deviation := math.Abs(data[i]-ma[i]) / stdDev
				result[i] = deviation > threshold
			}
		}
	}

	return result
}

// CalculateSeasonality extracts the seasonal component from the data.
func (ta *TrendAnalyzer) CalculateSeasonality(data []float64, seasonLength int) []float64 {
	n := len(data)
	if n < seasonLength*2 {
		return nil
	}

	// Detrend the data
	ma := ta.MovingAverage(data)
	detrended := make([]float64, n)
	for i := 0; i < n; i++ {
		detrended[i] = data[i] - ma[i]
	}

	// Calculate average seasonal pattern
	seasonal := make([]float64, seasonLength)
	counts := make([]int, seasonLength)

	for i := 0; i < n; i++ {
		idx := i % seasonLength
		seasonal[idx] += detrended[i]
		counts[idx]++
	}

	for i := 0; i < seasonLength; i++ {
		if counts[i] > 0 {
			seasonal[i] /= float64(counts[i])
		}
	}

	return seasonal
}

// LinearRegression performs simple linear regression and returns slope and intercept.
func (ta *TrendAnalyzer) LinearRegression(data []float64) (slope, intercept float64) {
	n := len(data)
	if n < 2 {
		return 0, 0
	}

	// Calculate means
	sumX := 0.0
	sumY := 0.0
	for i := 0; i < n; i++ {
		sumX += float64(i)
		sumY += data[i]
	}
	meanX := sumX / float64(n)
	meanY := sumY / float64(n)

	// Calculate slope
	numerator := 0.0
	denominator := 0.0
	for i := 0; i < n; i++ {
		xDiff := float64(i) - meanX
		yDiff := data[i] - meanY
		numerator += xDiff * yDiff
		denominator += xDiff * xDiff
	}

	if denominator == 0 {
		return 0, meanY
	}

	slope = numerator / denominator
	intercept = meanY - slope*meanX

	return slope, intercept
}

// PredictWithRegression predicts using linear regression.
func (ta *TrendAnalyzer) PredictWithRegression(data []float64, h int) []float64 {
	n := len(data)
	slope, intercept := ta.LinearRegression(data)

	predictions := make([]float64, h)
	for i := 0; i < h; i++ {
		predictions[i] = intercept + slope*float64(n+i)
		predictions[i] = math.Max(0, predictions[i])
	}

	return predictions
}

// GetTrendDirection returns the trend direction: 1 for up, -1 for down, 0 for flat.
func (ta *TrendAnalyzer) GetTrendDirection(data []float64) int {
	trend := ta.CalculateTrend(data)
	threshold := 0.001 // Minimum trend to be considered significant

	if trend > threshold {
		return 1
	} else if trend < -threshold {
		return -1
	}
	return 0
}

// CalculateVolatility calculates the volatility (standard deviation) of the data.
func (ta *TrendAnalyzer) CalculateVolatility(data []float64) float64 {
	n := len(data)
	if n < 2 {
		return 0
	}

	// Calculate mean
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	mean := sum / float64(n)

	// Calculate variance
	variance := 0.0
	for _, v := range data {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(n - 1)

	return math.Sqrt(variance)
}

