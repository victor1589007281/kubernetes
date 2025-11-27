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
	"context"
	"testing"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/predictor"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
)

func createTestManager() *OversellManager {
	config := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{
		NodeName:       "test-node",
		TotalBytes:     128 * 1024 * 1024 * 1024, // 128GB
		AvailableBytes: 64 * 1024 * 1024 * 1024,
		UsedBytes:      64 * 1024 * 1024 * 1024,
	}

	return NewOversellManager(config, pred, nodeMemory)
}

func TestNewOversellManager(t *testing.T) {
	manager := createTestManager()

	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	status := manager.GetStatus()
	if status.CurrentRatio != 1.0 {
		t.Errorf("Expected initial ratio 1.0, got %f", status.CurrentRatio)
	}

	if status.PhysicalMemoryBytes != 128*1024*1024*1024 {
		t.Errorf("Expected 128GB physical memory, got %d", status.PhysicalMemoryBytes)
	}
}

func TestSetRatio(t *testing.T) {
	manager := createTestManager()

	// Test valid ratio
	err := manager.SetRatio(1.5)
	if err != nil {
		t.Fatalf("Failed to set ratio: %v", err)
	}

	status := manager.GetStatus()
	if status.CurrentRatio != 1.5 {
		t.Errorf("Expected ratio 1.5, got %f", status.CurrentRatio)
	}

	// Test ratio below minimum
	err = manager.SetRatio(0.5)
	if err == nil {
		t.Error("Expected error for ratio below minimum")
	}

	// Test ratio above maximum
	err = manager.SetRatio(3.0)
	if err == nil {
		t.Error("Expected error for ratio above maximum")
	}
}

func TestAddPod(t *testing.T) {
	manager := createTestManager()

	pod := types.PodMemoryInfo{
		PodName:            "mysql-0",
		Namespace:          "default",
		MemoryLimitBytes:   8 * 1024 * 1024 * 1024, // 8GB
		MemoryRequestBytes: 4 * 1024 * 1024 * 1024,
		BufferPoolBytes:    6 * 1024 * 1024 * 1024,
		Priority:           100,
	}

	err := manager.AddPod(pod)
	if err != nil {
		t.Fatalf("Failed to add pod: %v", err)
	}

	pods := manager.GetPods()
	if len(pods) != 1 {
		t.Errorf("Expected 1 pod, got %d", len(pods))
	}

	if pods[0].PodName != "mysql-0" {
		t.Errorf("Expected pod name 'mysql-0', got '%s'", pods[0].PodName)
	}

	// Test adding duplicate pod
	err = manager.AddPod(pod)
	if err == nil {
		t.Error("Expected error for duplicate pod")
	}
}

func TestRemovePod(t *testing.T) {
	manager := createTestManager()

	pod := types.PodMemoryInfo{
		PodName:          "mysql-0",
		Namespace:        "default",
		MemoryLimitBytes: 8 * 1024 * 1024 * 1024,
	}

	manager.AddPod(pod)

	err := manager.RemovePod("default", "mysql-0")
	if err != nil {
		t.Fatalf("Failed to remove pod: %v", err)
	}

	pods := manager.GetPods()
	if len(pods) != 0 {
		t.Errorf("Expected 0 pods, got %d", len(pods))
	}

	// Test removing non-existent pod
	err = manager.RemovePod("default", "non-existent")
	if err == nil {
		t.Error("Expected error for non-existent pod")
	}
}

func TestUpdatePodMemory(t *testing.T) {
	manager := createTestManager()

	pod := types.PodMemoryInfo{
		PodName:          "mysql-0",
		Namespace:        "default",
		MemoryLimitBytes: 8 * 1024 * 1024 * 1024,
		CurrentRSSBytes:  2 * 1024 * 1024 * 1024,
	}

	manager.AddPod(pod)

	// Update memory
	newMemory := int64(4 * 1024 * 1024 * 1024)
	err := manager.UpdatePodMemory("default", "mysql-0", newMemory)
	if err != nil {
		t.Fatalf("Failed to update pod memory: %v", err)
	}

	pods := manager.GetPods()
	if pods[0].CurrentRSSBytes != newMemory {
		t.Errorf("Expected RSS %d, got %d", newMemory, pods[0].CurrentRSSBytes)
	}

	// Test updating non-existent pod
	err = manager.UpdatePodMemory("default", "non-existent", newMemory)
	if err == nil {
		t.Error("Expected error for non-existent pod")
	}
}

func TestAdjustRatioGradually(t *testing.T) {
	manager := createTestManager()

	// Start at 1.0
	initialStatus := manager.GetStatus()
	if initialStatus.CurrentRatio != 1.0 {
		t.Fatalf("Expected initial ratio 1.0, got %f", initialStatus.CurrentRatio)
	}

	// Try to adjust to 1.5 (should only move by step)
	err := manager.AdjustRatioGradually(1.5)
	if err != nil {
		t.Fatalf("Failed to adjust ratio: %v", err)
	}

	status := manager.GetStatus()
	// Should have moved by the adjustment step (0.05)
	expectedRatio := 1.0 + types.DefaultOversellConfig().AdjustmentStep
	if status.CurrentRatio != expectedRatio {
		t.Errorf("Expected ratio %f, got %f", expectedRatio, status.CurrentRatio)
	}
}

func TestGetNodeMemory(t *testing.T) {
	manager := createTestManager()

	nodeMemory := manager.GetNodeMemory()
	if nodeMemory.TotalBytes != 128*1024*1024*1024 {
		t.Errorf("Expected 128GB, got %d", nodeMemory.TotalBytes)
	}
}

func TestUpdateNodeMemory(t *testing.T) {
	manager := createTestManager()

	newMemory := types.NodeMemoryInfo{
		NodeName:       "test-node",
		TotalBytes:     256 * 1024 * 1024 * 1024,
		AvailableBytes: 128 * 1024 * 1024 * 1024,
		UsedBytes:      128 * 1024 * 1024 * 1024,
	}

	manager.UpdateNodeMemory(newMemory)

	status := manager.GetStatus()
	if status.PhysicalMemoryBytes != newMemory.TotalBytes {
		t.Errorf("Expected %d, got %d", newMemory.TotalBytes, status.PhysicalMemoryBytes)
	}
}

func TestStartStop(t *testing.T) {
	manager := createTestManager()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start should succeed
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Starting again should fail
	err = manager.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting already running manager")
	}

	// Stop should succeed
	err = manager.Stop()
	if err != nil {
		t.Fatalf("Failed to stop manager: %v", err)
	}

	// Stopping again should be idempotent
	err = manager.Stop()
	if err != nil {
		t.Errorf("Expected no error on second stop, got %v", err)
	}
}

// Calculator tests
func TestCalculatorOversellRatio(t *testing.T) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	// Test basic calculation
	totalBufferPool := int64(64 * 1024 * 1024 * 1024)  // 64GB
	predictedMaxUsage := int64(32 * 1024 * 1024 * 1024) // 32GB

	ratio := calc.CalculateOversellRatio(totalBufferPool, predictedMaxUsage)

	// Base ratio = 64/32 = 2.0
	// Safe ratio = 2.0 * 0.85 = 1.7
	expectedRatio := 2.0 * config.SafetyFactor
	if ratio != expectedRatio {
		t.Errorf("Expected ratio %f, got %f", expectedRatio, ratio)
	}
}

func TestCalculatorSafeRatio(t *testing.T) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	physicalMemory := int64(128 * 1024 * 1024 * 1024)
	allocatedMemory := int64(96 * 1024 * 1024 * 1024)
	actualUsage := int64(64 * 1024 * 1024 * 1024)
	predictedMaxUsage := int64(80 * 1024 * 1024 * 1024)

	ratio := calc.CalculateSafeRatio(physicalMemory, allocatedMemory, actualUsage, predictedMaxUsage)

	// Should be between min and max
	if ratio < config.MinRatio || ratio > config.MaxRatio {
		t.Errorf("Ratio %f out of range [%f, %f]", ratio, config.MinRatio, config.MaxRatio)
	}
}

func TestCalculatorCanAllocate(t *testing.T) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	logicalMemory := int64(100 * 1024 * 1024 * 1024)
	allocatedMemory := int64(80 * 1024 * 1024 * 1024)

	// Should be able to allocate 15GB more
	if !calc.CanAllocate(logicalMemory, allocatedMemory, 15*1024*1024*1024) {
		t.Error("Expected to be able to allocate 15GB")
	}

	// Should not be able to allocate 25GB more
	if calc.CanAllocate(logicalMemory, allocatedMemory, 25*1024*1024*1024) {
		t.Error("Expected to not be able to allocate 25GB")
	}
}

func TestCalculatorMemoryPressure(t *testing.T) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	physicalMemory := int64(100 * 1024 * 1024 * 1024)
	actualUsage := int64(80 * 1024 * 1024 * 1024)
	cachedMemory := int64(20 * 1024 * 1024 * 1024)

	pressure := calc.CalculateMemoryPressure(physicalMemory, actualUsage, cachedMemory)

	// Effective usage = 80 - 10 (50% of cache) = 70
	// Pressure = 70/100 = 0.7
	if pressure < 0 || pressure > 1 {
		t.Errorf("Pressure %f out of range [0, 1]", pressure)
	}
}

func TestCalculatorEvictionScore(t *testing.T) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	highPriorityPod := types.PodMemoryInfo{
		PodName:           "mysql-primary",
		CurrentRSSBytes:   4 * 1024 * 1024 * 1024,
		BufferPoolBytes:   8 * 1024 * 1024 * 1024,
		Priority:          100,
		IsOversellEnabled: true,
	}

	lowPriorityPod := types.PodMemoryInfo{
		PodName:           "mysql-replica",
		CurrentRSSBytes:   6 * 1024 * 1024 * 1024,
		BufferPoolBytes:   8 * 1024 * 1024 * 1024,
		Priority:          10,
		IsOversellEnabled: true,
	}

	highScore := calc.CalculateEvictionScore(highPriorityPod)
	lowScore := calc.CalculateEvictionScore(lowPriorityPod)

	// Low priority pod should have higher eviction score
	if lowScore <= highScore {
		t.Errorf("Expected low priority pod to have higher score: %f <= %f", lowScore, highScore)
	}
}

func TestCalculatorSelectPodsForEviction(t *testing.T) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	pods := []types.PodMemoryInfo{
		{
			PodName:         "pod1",
			CurrentRSSBytes: 10 * 1024 * 1024 * 1024,
			BufferPoolBytes: 20 * 1024 * 1024 * 1024,
			Priority:        100,
		},
		{
			PodName:         "pod2",
			CurrentRSSBytes: 5 * 1024 * 1024 * 1024,
			BufferPoolBytes: 10 * 1024 * 1024 * 1024,
			Priority:        50,
		},
		{
			PodName:         "pod3",
			CurrentRSSBytes: 8 * 1024 * 1024 * 1024,
			BufferPoolBytes: 10 * 1024 * 1024 * 1024,
			Priority:        10,
		},
	}

	// Need to free 10GB
	selected := calc.SelectPodsForEviction(pods, 10*1024*1024*1024)

	if len(selected) == 0 {
		t.Error("Expected at least one pod selected for eviction")
	}

	// Verify we selected enough memory
	var total int64
	for _, pod := range selected {
		total += pod.CurrentRSSBytes
	}

	if total < 10*1024*1024*1024 {
		t.Errorf("Selected pods don't free enough memory: %d", total)
	}
}

func TestCalculatorBufferPoolUtilization(t *testing.T) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	pods := []types.PodMemoryInfo{
		{
			CurrentRSSBytes: 4 * 1024 * 1024 * 1024,
			BufferPoolBytes: 8 * 1024 * 1024 * 1024,
		},
		{
			CurrentRSSBytes: 6 * 1024 * 1024 * 1024,
			BufferPoolBytes: 12 * 1024 * 1024 * 1024,
		},
	}

	utilization := calc.CalculateBufferPoolUtilization(pods)

	// Total usage: 10GB, Total buffer pool: 20GB
	// Utilization = 10/20 = 0.5
	expectedUtilization := 0.5
	if utilization != expectedUtilization {
		t.Errorf("Expected utilization %f, got %f", expectedUtilization, utilization)
	}
}

// Benchmark tests
func BenchmarkAddPod(b *testing.B) {
	manager := createTestManager()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pod := types.PodMemoryInfo{
			PodName:          "pod-" + string(rune(i)),
			Namespace:        "default",
			MemoryLimitBytes: 8 * 1024 * 1024 * 1024,
		}
		manager.AddPod(pod)
		manager.RemovePod("default", pod.PodName)
	}
}

func BenchmarkCalculateEvictionScore(b *testing.B) {
	config := types.DefaultOversellConfig()
	calc := NewCalculator(config)

	pod := types.PodMemoryInfo{
		PodName:         "mysql-0",
		CurrentRSSBytes: 4 * 1024 * 1024 * 1024,
		BufferPoolBytes: 8 * 1024 * 1024 * 1024,
		Priority:        50,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		calc.CalculateEvictionScore(pod)
	}
}

