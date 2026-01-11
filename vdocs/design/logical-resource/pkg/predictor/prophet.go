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

// ProphetLike implements a simplified Prophet-like forecasting model.
// Prophet decomposes time series into: y(t) = g(t) + s(t) + h(t) + ε
// where g(t) is the trend, s(t) is seasonality, h(t) is holiday effects.
type ProphetLike struct {
	seasonalPeriod int

	// Trend parameters
	trendSlope     float64
	trendIntercept float64
	changepoints   []int
	changeDeltas   []float64

	// Seasonality parameters (Fourier series coefficients)
	seasonalA []float64 // Cosine coefficients
	seasonalB []float64 // Sine coefficients
	fourierN  int       // Number of Fourier terms

	// Fitted values
	fitted bool
}

// NewProphetLike creates a new ProphetLike model.
func NewProphetLike(seasonalPeriod int) *ProphetLike {
	return &ProphetLike{
		seasonalPeriod: seasonalPeriod,
		fourierN:       3, // Number of Fourier terms for seasonality
		seasonalA:      make([]float64, 3),
		seasonalB:      make([]float64, 3),
	}
}

// Fit fits the model to the data.
func (p *ProphetLike) Fit(data []float64) error {
	n := len(data)
	if n < p.seasonalPeriod*2 {
		return nil
	}

	// Step 1: Fit trend using piecewise linear regression
	p.fitTrend(data)

	// Step 2: Detrend the data
	detrended := make([]float64, n)
	for i := 0; i < n; i++ {
		detrended[i] = data[i] - p.trendAt(i)
	}

	// Step 3: Fit seasonality using Fourier series
	p.fitSeasonality(detrended)

	p.fitted = true
	return nil
}

// fitTrend fits a piecewise linear trend with automatic changepoint detection.
func (p *ProphetLike) fitTrend(data []float64) {
	n := len(data)

	// Simple linear regression for now
	// More sophisticated implementation would detect changepoints
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i := 0; i < n; i++ {
		x := float64(i)
		y := data[i]
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	nf := float64(n)
	denominator := nf*sumX2 - sumX*sumX
	if denominator == 0 {
		p.trendSlope = 0
		p.trendIntercept = sumY / nf
	} else {
		p.trendSlope = (nf*sumXY - sumX*sumY) / denominator
		p.trendIntercept = (sumY - p.trendSlope*sumX) / nf
	}

	// Detect changepoints using simple method
	p.detectChangepoints(data)
}

// detectChangepoints detects potential changepoints in the trend.
func (p *ProphetLike) detectChangepoints(data []float64) {
	n := len(data)
	if n < 20 {
		return
	}

	// Look for significant changes in local trend
	windowSize := n / 10
	if windowSize < 5 {
		windowSize = 5
	}

	p.changepoints = make([]int, 0)
	p.changeDeltas = make([]float64, 0)

	for i := windowSize; i < n-windowSize; i += windowSize {
		// Calculate local slopes before and after point i
		slopeBefore := p.localSlope(data, i-windowSize, i)
		slopeAfter := p.localSlope(data, i, i+windowSize)

		// If slopes differ significantly, mark as changepoint
		if math.Abs(slopeAfter-slopeBefore) > 0.01 {
			p.changepoints = append(p.changepoints, i)
			p.changeDeltas = append(p.changeDeltas, slopeAfter-slopeBefore)
		}
	}
}

// localSlope calculates the slope in a window.
func (p *ProphetLike) localSlope(data []float64, start, end int) float64 {
	if end <= start {
		return 0
	}

	n := end - start
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i := start; i < end && i < len(data); i++ {
		x := float64(i - start)
		y := data[i]
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	nf := float64(n)
	denominator := nf*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}
	return (nf*sumXY - sumX*sumY) / denominator
}

// trendAt returns the trend value at time t.
func (p *ProphetLike) trendAt(t int) float64 {
	// Base trend
	trend := p.trendIntercept + p.trendSlope*float64(t)

	// Add changepoint adjustments
	for i, cp := range p.changepoints {
		if t >= cp {
			trend += p.changeDeltas[i] * float64(t-cp)
		}
	}

	return trend
}

// fitSeasonality fits the seasonal component using Fourier series.
func (p *ProphetLike) fitSeasonality(detrended []float64) {
	n := len(detrended)
	period := float64(p.seasonalPeriod)

	// Fit Fourier coefficients using least squares
	for k := 0; k < p.fourierN; k++ {
		kf := float64(k + 1)
		sumCos := 0.0
		sumSin := 0.0
		sumCos2 := 0.0
		sumSin2 := 0.0
		sumYCos := 0.0
		sumYSin := 0.0

		for t := 0; t < n; t++ {
			tf := float64(t)
			angle := 2 * math.Pi * kf * tf / period
			cos := math.Cos(angle)
			sin := math.Sin(angle)

			sumCos += cos
			sumSin += sin
			sumCos2 += cos * cos
			sumSin2 += sin * sin
			sumYCos += detrended[t] * cos
			sumYSin += detrended[t] * sin
		}

		if sumCos2 > 0 {
			p.seasonalA[k] = sumYCos / sumCos2
		}
		if sumSin2 > 0 {
			p.seasonalB[k] = sumYSin / sumSin2
		}
	}
}

// seasonAt returns the seasonal component at time t.
func (p *ProphetLike) seasonAt(t int) float64 {
	period := float64(p.seasonalPeriod)
	tf := float64(t)
	seasonal := 0.0

	for k := 0; k < p.fourierN; k++ {
		kf := float64(k + 1)
		angle := 2 * math.Pi * kf * tf / period
		seasonal += p.seasonalA[k]*math.Cos(angle) + p.seasonalB[k]*math.Sin(angle)
	}

	return seasonal
}

// Predict generates predictions for h steps ahead.
func (p *ProphetLike) Predict(data []float64, h int) ([]float64, error) {
	if err := p.Fit(data); err != nil {
		return nil, err
	}

	n := len(data)
	predictions := make([]float64, h)

	for i := 0; i < h; i++ {
		t := n + i
		prediction := p.trendAt(t) + p.seasonAt(t)

		// Clamp to valid range
		predictions[i] = math.Max(0, math.Min(1, prediction))
	}

	return predictions, nil
}

// GetComponents returns the decomposed components at each time point.
func (p *ProphetLike) GetComponents(data []float64) (trend, seasonal, residual []float64) {
	n := len(data)
	trend = make([]float64, n)
	seasonal = make([]float64, n)
	residual = make([]float64, n)

	for t := 0; t < n; t++ {
		trend[t] = p.trendAt(t)
		seasonal[t] = p.seasonAt(t)
		residual[t] = data[t] - trend[t] - seasonal[t]
	}

	return
}

// SetFourierOrder sets the number of Fourier terms.
func (p *ProphetLike) SetFourierOrder(n int) {
	p.fourierN = n
	p.seasonalA = make([]float64, n)
	p.seasonalB = make([]float64, n)
}

// PredictWithUncertainty generates predictions with confidence intervals.
func (p *ProphetLike) PredictWithUncertainty(data []float64, h int) (predictions, lower, upper []float64, err error) {
	predictions, err = p.Predict(data, h)
	if err != nil {
		return nil, nil, nil, err
	}

	// Calculate residual standard deviation
	n := len(data)
	residuals := make([]float64, n)
	sumSquared := 0.0

	for t := 0; t < n; t++ {
		predicted := p.trendAt(t) + p.seasonAt(t)
		residuals[t] = data[t] - predicted
		sumSquared += residuals[t] * residuals[t]
	}

	stdDev := math.Sqrt(sumSquared / float64(n))

	// Generate confidence intervals (95%)
	lower = make([]float64, h)
	upper = make([]float64, h)
	multiplier := 1.96 // 95% confidence

	for i := 0; i < h; i++ {
		// Uncertainty grows with forecast horizon
		horizonFactor := math.Sqrt(float64(i + 1))
		interval := multiplier * stdDev * horizonFactor

		lower[i] = math.Max(0, predictions[i]-interval)
		upper[i] = math.Min(1, predictions[i]+interval)
	}

	return predictions, lower, upper, nil
}



