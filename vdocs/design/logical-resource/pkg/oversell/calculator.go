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

package oversell

import (
	"math"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
)

// Calculator calculates oversell ratios and related metrics.
type Calculator struct {
	config types.OversellConfig
}

// NewCalculator creates a new Calculator.
func NewCalculator(config types.OversellConfig) *Calculator {
	return &Calculator{
		config: config,
	}
}

// CalculateOversellRatio calculates the oversell ratio based on buffer pool and predicted usage.
func (c *Calculator) CalculateOversellRatio(totalBufferPool, predictedMaxUsage int64) float64 {
	if predictedMaxUsage <= 0 {
		return c.config.MinRatio
	}

	// Base ratio: total buffer pool / predicted max usage
	ratio := float64(totalBufferPool) / float64(predictedMaxUsage)

	// Apply safety factor
	safeRatio := ratio * c.config.SafetyFactor

	// Clamp to configured limits
	return c.clampRatio(safeRatio)
}

// CalculateSafeRatio calculates a safe oversell ratio considering multiple factors.
func (c *Calculator) CalculateSafeRatio(
	physicalMemory int64,
	allocatedMemory int64,
	actualUsage int64,
	predictedMaxUsage int64,
) float64 {
	// Calculate current utilization
	currentUtilization := float64(actualUsage) / float64(physicalMemory)

	// Calculate allocation ratio
	allocationRatio := float64(allocatedMemory) / float64(physicalMemory)

	// Base ratio from prediction
	var baseRatio float64
	if predictedMaxUsage > 0 {
		baseRatio = float64(physicalMemory) / float64(predictedMaxUsage)
	} else {
		baseRatio = 1.0
	}

	// Adjust based on current utilization
	// If current utilization is high, be more conservative
	utilizationFactor := 1.0
	if currentUtilization > 0.7 {
		utilizationFactor = 1.0 - (currentUtilization-0.7)*2 // Reduce ratio as utilization increases
	}

	safeRatio := baseRatio * utilizationFactor * c.config.SafetyFactor

	// Don't allow ratio lower than allocation ratio (would cause immediate issues)
	if safeRatio < allocationRatio {
		safeRatio = allocationRatio * 1.1 // Allow 10% headroom
	}

	return c.clampRatio(safeRatio)
}

// CalculateLogicalMemory calculates the logical memory size.
func (c *Calculator) CalculateLogicalMemory(physicalMemory int64, ratio float64) int64 {
	return int64(float64(physicalMemory) * ratio)
}

// CalculateAvailableLogical calculates the available logical memory.
func (c *Calculator) CalculateAvailableLogical(logicalMemory, allocatedMemory int64) int64 {
	available := logicalMemory - allocatedMemory
	if available < 0 {
		return 0
	}
	return available
}

// CanAllocate checks if the requested memory can be allocated.
func (c *Calculator) CanAllocate(
	logicalMemory int64,
	allocatedMemory int64,
	requestedMemory int64,
) bool {
	return (allocatedMemory + requestedMemory) <= logicalMemory
}

// CalculateEffectiveRatio calculates the effective oversell ratio based on actual usage.
func (c *Calculator) CalculateEffectiveRatio(allocatedMemory, actualUsage int64) float64 {
	if actualUsage <= 0 {
		return 0
	}
	return float64(allocatedMemory) / float64(actualUsage)
}

// ShouldAdjustRatio determines if the ratio should be adjusted.
func (c *Calculator) ShouldAdjustRatio(currentRatio, targetRatio float64, threshold float64) bool {
	return math.Abs(currentRatio-targetRatio) > threshold
}

// CalculateAdjustmentStep calculates the adjustment step for gradual ratio change.
func (c *Calculator) CalculateAdjustmentStep(currentRatio, targetRatio float64) float64 {
	diff := targetRatio - currentRatio

	// Use configured step or smaller if approaching target
	step := c.config.AdjustmentStep
	if math.Abs(diff) < step {
		return diff
	}

	if diff > 0 {
		return step
	}
	return -step
}

// clampRatio clamps the ratio to configured limits.
func (c *Calculator) clampRatio(ratio float64) float64 {
	if ratio < c.config.MinRatio {
		return c.config.MinRatio
	}
	if ratio > c.config.MaxRatio {
		return c.config.MaxRatio
	}
	return ratio
}

// CalculateMemoryPressure calculates the memory pressure level (0.0 to 1.0).
func (c *Calculator) CalculateMemoryPressure(physicalMemory, actualUsage, cachedMemory int64) float64 {
	// Effective usage = actual usage - reclaimable cache
	reclaimableCache := cachedMemory / 2 // Assume 50% of cache is reclaimable
	effectiveUsage := actualUsage - reclaimableCache
	if effectiveUsage < 0 {
		effectiveUsage = 0
	}

	pressure := float64(effectiveUsage) / float64(physicalMemory)
	if pressure > 1.0 {
		return 1.0
	}
	return pressure
}

// RecommendRatioChange recommends whether to increase or decrease the ratio.
// Returns: positive = increase, negative = decrease, 0 = no change.
func (c *Calculator) RecommendRatioChange(
	currentRatio float64,
	memoryPressure float64,
	allocationUtilization float64,
) float64 {
	// High memory pressure: decrease ratio
	if memoryPressure > 0.85 {
		return -c.config.AdjustmentStep * 2 // Faster decrease when under pressure
	}
	if memoryPressure > 0.75 {
		return -c.config.AdjustmentStep
	}

	// Low pressure and low allocation: can increase ratio
	if memoryPressure < 0.5 && allocationUtilization > 0.8 {
		// High allocation utilization with low pressure means we're overselling efficiently
		if currentRatio < c.config.MaxRatio {
			return c.config.AdjustmentStep
		}
	}

	// Moderate pressure: maintain current ratio
	return 0
}

// CalculateEvictionScore calculates an eviction score for a pod.
// Higher score = more likely to be evicted.
func (c *Calculator) CalculateEvictionScore(pod types.PodMemoryInfo) float64 {
	score := 0.0

	// Factor 1: Memory usage ratio (higher usage = higher score)
	if pod.BufferPoolBytes > 0 {
		usageRatio := float64(pod.CurrentRSSBytes) / float64(pod.BufferPoolBytes)
		score += usageRatio * 30
	}

	// Factor 2: Priority (lower priority = higher score)
	score += float64(100-pod.Priority) * 0.5

	// Factor 3: Oversell enabled pods are preferred for eviction
	if pod.IsOversellEnabled {
		score += 10
	}

	return score
}

// SelectPodsForEviction selects pods for eviction based on scores.
func (c *Calculator) SelectPodsForEviction(
	pods []types.PodMemoryInfo,
	targetBytes int64,
) []types.PodMemoryInfo {
	if len(pods) == 0 || targetBytes <= 0 {
		return nil
	}

	// Calculate scores and sort
	type podScore struct {
		pod   types.PodMemoryInfo
		score float64
	}

	scores := make([]podScore, len(pods))
	for i, pod := range pods {
		scores[i] = podScore{
			pod:   pod,
			score: c.CalculateEvictionScore(pod),
		}
	}

	// Sort by score (descending)
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	// Select pods until we reach target
	selected := make([]types.PodMemoryInfo, 0)
	var freedBytes int64

	for _, ps := range scores {
		if freedBytes >= targetBytes {
			break
		}
		selected = append(selected, ps.pod)
		freedBytes += ps.pod.CurrentRSSBytes
	}

	return selected
}

// CalculateBufferPoolUtilization calculates the overall buffer pool utilization.
func (c *Calculator) CalculateBufferPoolUtilization(pods []types.PodMemoryInfo) float64 {
	var totalBufferPool int64
	var totalUsage int64

	for _, pod := range pods {
		totalBufferPool += pod.BufferPoolBytes
		totalUsage += pod.CurrentRSSBytes
	}

	if totalBufferPool <= 0 {
		return 0
	}

	return float64(totalUsage) / float64(totalBufferPool)
}

