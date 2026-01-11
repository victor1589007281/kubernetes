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
	"math/rand"
)

// SimpleLSTM implements a simplified LSTM-like model for time series prediction.
// This is a pure Go implementation that mimics LSTM behavior using
// exponential smoothing with memory cells.
//
// For production use with complex patterns, consider:
// - Using TensorFlow/PyTorch via gRPC service
// - Using ONNX runtime for Go
// - Calling external prediction API
type SimpleLSTM struct {
	inputSize    int
	hiddenSize   int
	outputSize   int
	sequenceLen  int

	// Weights (simplified representation)
	inputWeights  [][]float64
	hiddenWeights [][]float64
	outputWeights [][]float64

	// Cell state and hidden state
	cellState   []float64
	hiddenState []float64

	// Forget gate parameters
	forgetBias float64

	// Learning parameters
	learningRate float64
	epochs       int

	// Normalization parameters
	mean   float64
	stdDev float64
}

// NewSimpleLSTM creates a new SimpleLSTM model.
func NewSimpleLSTM(inputSize, hiddenSize, sequenceLen int) *SimpleLSTM {
	lstm := &SimpleLSTM{
		inputSize:    inputSize,
		hiddenSize:   hiddenSize,
		outputSize:   1,
		sequenceLen:  sequenceLen,
		forgetBias:   1.0,
		learningRate: 0.01,
		epochs:       100,
	}

	lstm.initializeWeights()
	return lstm
}

// initializeWeights initializes the weight matrices.
func (l *SimpleLSTM) initializeWeights() {
	// Xavier initialization
	scale := math.Sqrt(2.0 / float64(l.inputSize+l.hiddenSize))

	l.inputWeights = make([][]float64, l.hiddenSize)
	l.hiddenWeights = make([][]float64, l.hiddenSize)
	l.outputWeights = make([][]float64, l.outputSize)

	for i := 0; i < l.hiddenSize; i++ {
		l.inputWeights[i] = make([]float64, l.inputSize)
		l.hiddenWeights[i] = make([]float64, l.hiddenSize)
		for j := 0; j < l.inputSize; j++ {
			l.inputWeights[i][j] = (rand.Float64()*2 - 1) * scale
		}
		for j := 0; j < l.hiddenSize; j++ {
			l.hiddenWeights[i][j] = (rand.Float64()*2 - 1) * scale
		}
	}

	for i := 0; i < l.outputSize; i++ {
		l.outputWeights[i] = make([]float64, l.hiddenSize)
		for j := 0; j < l.hiddenSize; j++ {
			l.outputWeights[i][j] = (rand.Float64()*2 - 1) * scale
		}
	}

	l.cellState = make([]float64, l.hiddenSize)
	l.hiddenState = make([]float64, l.hiddenSize)
}

// normalize normalizes the data.
func (l *SimpleLSTM) normalize(data []float64) []float64 {
	n := len(data)
	if n == 0 {
		return data
	}

	// Calculate mean
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	l.mean = sum / float64(n)

	// Calculate standard deviation
	variance := 0.0
	for _, v := range data {
		variance += (v - l.mean) * (v - l.mean)
	}
	l.stdDev = math.Sqrt(variance / float64(n))

	if l.stdDev == 0 {
		l.stdDev = 1
	}

	// Normalize
	result := make([]float64, n)
	for i, v := range data {
		result[i] = (v - l.mean) / l.stdDev
	}

	return result
}

// denormalize reverses the normalization.
func (l *SimpleLSTM) denormalize(data []float64) []float64 {
	result := make([]float64, len(data))
	for i, v := range data {
		result[i] = v*l.stdDev + l.mean
	}
	return result
}

// sigmoid activation function.
func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

// tanh activation function.
func tanh(x float64) float64 {
	return math.Tanh(x)
}

// forward performs a forward pass through one LSTM cell.
func (l *SimpleLSTM) forward(input []float64) float64 {
	// Simplified LSTM-like computation
	// Real LSTM has forget gate, input gate, output gate, cell state updates

	// Input processing
	inputSum := make([]float64, l.hiddenSize)
	for i := 0; i < l.hiddenSize; i++ {
		for j := 0; j < len(input) && j < l.inputSize; j++ {
			inputSum[i] += l.inputWeights[i][j] * input[j]
		}
		for j := 0; j < l.hiddenSize; j++ {
			inputSum[i] += l.hiddenWeights[i][j] * l.hiddenState[j]
		}
	}

	// Forget gate (simplified)
	forgetGate := make([]float64, l.hiddenSize)
	for i := 0; i < l.hiddenSize; i++ {
		forgetGate[i] = sigmoid(inputSum[i] + l.forgetBias)
	}

	// Update cell state
	for i := 0; i < l.hiddenSize; i++ {
		l.cellState[i] = forgetGate[i]*l.cellState[i] + (1-forgetGate[i])*tanh(inputSum[i])
		l.hiddenState[i] = tanh(l.cellState[i])
	}

	// Output
	output := 0.0
	for i := 0; i < l.hiddenSize; i++ {
		output += l.outputWeights[0][i] * l.hiddenState[i]
	}

	return sigmoid(output) // Output in [0, 1]
}

// reset resets the cell and hidden states.
func (l *SimpleLSTM) reset() {
	for i := 0; i < l.hiddenSize; i++ {
		l.cellState[i] = 0
		l.hiddenState[i] = 0
	}
}

// createSequences creates input-output sequences for training.
func (l *SimpleLSTM) createSequences(data []float64) ([][]float64, []float64) {
	n := len(data)
	if n <= l.sequenceLen {
		return nil, nil
	}

	numSequences := n - l.sequenceLen
	inputs := make([][]float64, numSequences)
	outputs := make([]float64, numSequences)

	for i := 0; i < numSequences; i++ {
		inputs[i] = make([]float64, l.sequenceLen)
		copy(inputs[i], data[i:i+l.sequenceLen])
		outputs[i] = data[i+l.sequenceLen]
	}

	return inputs, outputs
}

// Fit trains the model on the data.
func (l *SimpleLSTM) Fit(data []float64) error {
	if len(data) <= l.sequenceLen {
		return errors.New("insufficient data for LSTM training")
	}

	// Normalize data
	normalized := l.normalize(data)

	// Create sequences
	inputs, outputs := l.createSequences(normalized)
	if inputs == nil {
		return errors.New("failed to create training sequences")
	}

	// Training loop (simplified gradient descent)
	for epoch := 0; epoch < l.epochs; epoch++ {
		totalLoss := 0.0

		for i := range inputs {
			l.reset()

			// Forward pass through sequence
			var prediction float64
			for _, val := range inputs[i] {
				prediction = l.forward([]float64{val})
			}

			// Calculate loss
			loss := outputs[i] - prediction
			totalLoss += loss * loss

			// Simplified backpropagation (gradient descent on output weights)
			for j := 0; j < l.hiddenSize; j++ {
				l.outputWeights[0][j] += l.learningRate * loss * l.hiddenState[j]
			}
		}

		// Early stopping if loss is small enough
		avgLoss := totalLoss / float64(len(inputs))
		if avgLoss < 0.0001 {
			break
		}
	}

	return nil
}

// Predict generates predictions for h steps ahead.
func (l *SimpleLSTM) Predict(data []float64, h int) ([]float64, error) {
	if err := l.Fit(data); err != nil {
		return nil, err
	}

	// Normalize input
	normalized := l.normalize(data)

	// Initialize with last sequence
	l.reset()
	startIdx := len(normalized) - l.sequenceLen
	if startIdx < 0 {
		startIdx = 0
	}

	// Warm up with historical data
	for _, val := range normalized[startIdx:] {
		l.forward([]float64{val})
	}

	// Generate predictions
	predictions := make([]float64, h)
	lastInput := normalized[len(normalized)-1]

	for i := 0; i < h; i++ {
		prediction := l.forward([]float64{lastInput})
		predictions[i] = prediction
		lastInput = prediction
	}

	// Denormalize
	predictions = l.denormalize(predictions)

	// Clamp to valid range
	for i := range predictions {
		predictions[i] = math.Max(0, math.Min(1, predictions[i]))
	}

	return predictions, nil
}

// SetHyperparameters sets training hyperparameters.
func (l *SimpleLSTM) SetHyperparameters(learningRate float64, epochs int) {
	l.learningRate = learningRate
	l.epochs = epochs
}

// GRU implements a simplified GRU (Gated Recurrent Unit) model.
// GRU is similar to LSTM but with fewer parameters.
type GRU struct {
	inputSize   int
	hiddenSize  int
	sequenceLen int

	// GRU has update gate and reset gate
	updateWeights [][]float64
	resetWeights  [][]float64
	outputWeights [][]float64

	hiddenState []float64

	learningRate float64
	epochs       int

	mean   float64
	stdDev float64
}

// NewGRU creates a new GRU model.
func NewGRU(inputSize, hiddenSize, sequenceLen int) *GRU {
	gru := &GRU{
		inputSize:    inputSize,
		hiddenSize:   hiddenSize,
		sequenceLen:  sequenceLen,
		learningRate: 0.01,
		epochs:       100,
	}

	gru.initializeWeights()
	return gru
}

// initializeWeights initializes the GRU weights.
func (g *GRU) initializeWeights() {
	scale := math.Sqrt(2.0 / float64(g.inputSize+g.hiddenSize))

	g.updateWeights = make([][]float64, g.hiddenSize)
	g.resetWeights = make([][]float64, g.hiddenSize)
	g.outputWeights = make([][]float64, 1)

	for i := 0; i < g.hiddenSize; i++ {
		g.updateWeights[i] = make([]float64, g.inputSize+g.hiddenSize)
		g.resetWeights[i] = make([]float64, g.inputSize+g.hiddenSize)
		for j := 0; j < g.inputSize+g.hiddenSize; j++ {
			g.updateWeights[i][j] = (rand.Float64()*2 - 1) * scale
			g.resetWeights[i][j] = (rand.Float64()*2 - 1) * scale
		}
	}

	g.outputWeights[0] = make([]float64, g.hiddenSize)
	for j := 0; j < g.hiddenSize; j++ {
		g.outputWeights[0][j] = (rand.Float64()*2 - 1) * scale
	}

	g.hiddenState = make([]float64, g.hiddenSize)
}

// forward performs a forward pass through the GRU.
func (g *GRU) forward(input float64) float64 {
	// Concatenate input and hidden state
	concat := make([]float64, g.inputSize+g.hiddenSize)
	concat[0] = input
	copy(concat[1:], g.hiddenState)

	// Update gate
	updateGate := make([]float64, g.hiddenSize)
	for i := 0; i < g.hiddenSize; i++ {
		sum := 0.0
		for j := 0; j < len(concat); j++ {
			sum += g.updateWeights[i][j] * concat[j]
		}
		updateGate[i] = sigmoid(sum)
	}

	// Reset gate
	resetGate := make([]float64, g.hiddenSize)
	for i := 0; i < g.hiddenSize; i++ {
		sum := 0.0
		for j := 0; j < len(concat); j++ {
			sum += g.resetWeights[i][j] * concat[j]
		}
		resetGate[i] = sigmoid(sum)
	}

	// Candidate hidden state
	for i := 0; i < g.hiddenSize; i++ {
		candidate := tanh(input + resetGate[i]*g.hiddenState[i])
		g.hiddenState[i] = updateGate[i]*g.hiddenState[i] + (1-updateGate[i])*candidate
	}

	// Output
	output := 0.0
	for i := 0; i < g.hiddenSize; i++ {
		output += g.outputWeights[0][i] * g.hiddenState[i]
	}

	return sigmoid(output)
}

// Predict generates predictions using GRU.
func (g *GRU) Predict(data []float64, h int) ([]float64, error) {
	if len(data) < g.sequenceLen {
		return nil, errors.New("insufficient data for GRU prediction")
	}

	// Normalize
	mean := 0.0
	for _, v := range data {
		mean += v
	}
	mean /= float64(len(data))

	variance := 0.0
	for _, v := range data {
		variance += (v - mean) * (v - mean)
	}
	stdDev := math.Sqrt(variance / float64(len(data)))
	if stdDev == 0 {
		stdDev = 1
	}

	// Warm up
	g.hiddenState = make([]float64, g.hiddenSize)
	for _, v := range data[len(data)-g.sequenceLen:] {
		normalized := (v - mean) / stdDev
		g.forward(normalized)
	}

	// Predict
	predictions := make([]float64, h)
	lastVal := (data[len(data)-1] - mean) / stdDev

	for i := 0; i < h; i++ {
		pred := g.forward(lastVal)
		denormalized := pred*stdDev + mean
		predictions[i] = math.Max(0, math.Min(1, denormalized))
		lastVal = pred
	}

	return predictions, nil
}



