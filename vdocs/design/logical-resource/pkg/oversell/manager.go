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

// Package oversell provides memory overselling management capabilities.
package oversell

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/predictor"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// Manager manages memory overselling operations.
type Manager interface {
	// Start starts the oversell manager.
	Start(ctx context.Context) error

	// Stop stops the oversell manager.
	Stop() error

	// GetStatus returns the current oversell status.
	GetStatus() types.OversellStatus

	// SetRatio sets the oversell ratio.
	SetRatio(ratio float64) error

	// GetRecommendedRatio returns the recommended oversell ratio based on predictions.
	GetRecommendedRatio() (float64, error)

	// AddPod adds a pod to oversell management.
	AddPod(info types.PodMemoryInfo) error

	// RemovePod removes a pod from oversell management.
	RemovePod(namespace, podName string) error

	// UpdatePodMemory updates the memory usage for a pod.
	UpdatePodMemory(namespace, podName string, memoryBytes int64) error

	// GetPods returns all managed pods.
	GetPods() []types.PodMemoryInfo
}

// OversellManager implements the Manager interface.
type OversellManager struct {
	mu sync.RWMutex

	// config holds the oversell configuration.
	config types.OversellConfig

	// predictor is used to predict memory usage.
	predictor predictor.Predictor

	// cgroupManager manages cgroup operations.
	cgroupManager *CgroupManager

	// calculator calculates oversell ratios.
	calculator *Calculator

	// pods stores information about managed pods.
	pods map[string]*types.PodMemoryInfo

	// nodeMemory stores node memory information.
	nodeMemory types.NodeMemoryInfo

	// status stores the current oversell status.
	status types.OversellStatus

	// running indicates whether the manager is running.
	running bool

	// stopCh is used to signal stop.
	stopCh chan struct{}
}

// NewOversellManager creates a new OversellManager.
func NewOversellManager(
	config types.OversellConfig,
	pred predictor.Predictor,
	nodeMemory types.NodeMemoryInfo,
) *OversellManager {
	return &OversellManager{
		config:        config,
		predictor:     pred,
		cgroupManager: NewCgroupManager(),
		calculator:    NewCalculator(config),
		pods:          make(map[string]*types.PodMemoryInfo),
		nodeMemory:    nodeMemory,
		status: types.OversellStatus{
			CurrentRatio:        1.0,
			SafeRatio:           1.0,
			PhysicalMemoryBytes: nodeMemory.TotalBytes,
			LogicalMemoryBytes:  nodeMemory.TotalBytes,
			LastUpdated:         time.Now(),
		},
		stopCh: make(chan struct{}),
	}
}

// Start starts the oversell manager.
func (m *OversellManager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("manager is already running")
	}
	m.running = true
	m.mu.Unlock()

	// Initialize cgroup structure
	if err := m.initializeCgroups(); err != nil {
		return fmt.Errorf("failed to initialize cgroups: %w", err)
	}

	// Start background reconciliation
	go m.reconcileLoop(ctx)

	log.Info("Oversell manager started")
	return nil
}

// Stop stops the oversell manager.
func (m *OversellManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	close(m.stopCh)
	m.running = false

	log.Info("Oversell manager stopped")
	return nil
}

// initializeCgroups initializes the cgroup hierarchy for overselling.
func (m *OversellManager) initializeCgroups() error {
	// Create the logical-memory cgroup
	config := types.CgroupMemoryConfig{
		Path:       "/sys/fs/cgroup/logical-memory",
		MemoryMax:  m.nodeMemory.TotalBytes,
		MemoryHigh: int64(float64(m.nodeMemory.TotalBytes) * 0.9),
		Version:    types.CgroupV2,
	}

	if err := m.cgroupManager.CreateCgroup(config); err != nil {
		log.WithError(err).Warn("Failed to create logical-memory cgroup, may already exist")
	}

	// Create the oversell-pods subcgroup
	podsConfig := types.CgroupMemoryConfig{
		Path:       "/sys/fs/cgroup/logical-memory/oversell-pods",
		MemoryMax:  m.nodeMemory.TotalBytes,
		MemoryHigh: int64(float64(m.nodeMemory.TotalBytes) * 0.85),
		Version:    types.CgroupV2,
	}

	return m.cgroupManager.CreateCgroup(podsConfig)
}

// reconcileLoop runs the reconciliation loop.
func (m *OversellManager) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.reconcile()
		}
	}
}

// reconcile performs reconciliation of oversell state.
func (m *OversellManager) reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Calculate recommended ratio based on predictions
	recommendedRatio, err := m.calculateRecommendedRatio()
	if err != nil {
		log.WithError(err).Warn("Failed to calculate recommended ratio")
		return
	}

	m.status.SafeRatio = recommendedRatio

	// Update status
	m.updateStatus()

	log.WithFields(logrus.Fields{
		"current_ratio":  m.status.CurrentRatio,
		"safe_ratio":     m.status.SafeRatio,
		"actual_usage":   m.status.ActualUsageBytes,
		"logical_memory": m.status.LogicalMemoryBytes,
	}).Debug("Reconciliation completed")
}

// calculateRecommendedRatio calculates the recommended oversell ratio.
func (m *OversellManager) calculateRecommendedRatio() (float64, error) {
	// Get prediction for next 72 hours (3 days)
	recommendedRatio, err := m.predictor.GetRecommendedOversellRatio(72, m.config.SafetyFactor)
	if err != nil {
		return 1.0, err
	}

	// Clamp to configured limits
	if recommendedRatio < m.config.MinRatio {
		recommendedRatio = m.config.MinRatio
	}
	if recommendedRatio > m.config.MaxRatio {
		recommendedRatio = m.config.MaxRatio
	}

	return recommendedRatio, nil
}

// updateStatus updates the current oversell status.
func (m *OversellManager) updateStatus() {
	// Calculate total allocated logical memory
	var totalAllocated int64
	var actualUsage int64
	for _, pod := range m.pods {
		totalAllocated += pod.MemoryLimitBytes
		actualUsage += pod.CurrentRSSBytes
	}

	m.status.AllocatedLogicalBytes = totalAllocated
	m.status.ActualUsageBytes = actualUsage
	m.status.LogicalMemoryBytes = int64(float64(m.nodeMemory.TotalBytes) * m.status.CurrentRatio)
	m.status.LastUpdated = time.Now()
}

// GetStatus returns the current oversell status.
func (m *OversellManager) GetStatus() types.OversellStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// SetRatio sets the oversell ratio.
func (m *OversellManager) SetRatio(ratio float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ratio < m.config.MinRatio {
		return fmt.Errorf("ratio %f is below minimum %f", ratio, m.config.MinRatio)
	}
	if ratio > m.config.MaxRatio {
		return fmt.Errorf("ratio %f exceeds maximum %f", ratio, m.config.MaxRatio)
	}

	oldRatio := m.status.CurrentRatio
	m.status.CurrentRatio = ratio
	m.status.LogicalMemoryBytes = int64(float64(m.nodeMemory.TotalBytes) * ratio)

	// Update cgroup limits
	config := types.CgroupMemoryConfig{
		Path:       "/sys/fs/cgroup/logical-memory",
		MemoryMax:  m.status.LogicalMemoryBytes,
		MemoryHigh: int64(float64(m.status.LogicalMemoryBytes) * 0.9),
		Version:    types.CgroupV2,
	}

	if err := m.cgroupManager.UpdateCgroup(config); err != nil {
		// Rollback
		m.status.CurrentRatio = oldRatio
		m.status.LogicalMemoryBytes = int64(float64(m.nodeMemory.TotalBytes) * oldRatio)
		return fmt.Errorf("failed to update cgroup: %w", err)
	}

	log.WithFields(logrus.Fields{
		"old_ratio": oldRatio,
		"new_ratio": ratio,
	}).Info("Oversell ratio updated")

	return nil
}

// GetRecommendedRatio returns the recommended oversell ratio based on predictions.
func (m *OversellManager) GetRecommendedRatio() (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.calculateRecommendedRatio()
}

// AddPod adds a pod to oversell management.
func (m *OversellManager) AddPod(info types.PodMemoryInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := podKey(info.Namespace, info.PodName)
	if _, exists := m.pods[key]; exists {
		return fmt.Errorf("pod %s already exists", key)
	}

	// Create pod cgroup under oversell-pods
	podCgroupPath := fmt.Sprintf("/sys/fs/cgroup/logical-memory/oversell-pods/%s", info.PodName)
	config := types.CgroupMemoryConfig{
		Path:       podCgroupPath,
		MemoryMax:  info.MemoryLimitBytes,
		MemoryHigh: int64(float64(info.MemoryLimitBytes) * 0.85),
		Version:    types.CgroupV2,
	}

	if err := m.cgroupManager.CreateCgroup(config); err != nil {
		return fmt.Errorf("failed to create pod cgroup: %w", err)
	}

	info.CgroupPath = podCgroupPath
	info.IsOversellEnabled = true
	m.pods[key] = &info

	m.updateStatus()

	log.WithFields(logrus.Fields{
		"namespace": info.Namespace,
		"pod":       info.PodName,
		"limit":     info.MemoryLimitBytes,
	}).Info("Pod added to oversell management")

	return nil
}

// RemovePod removes a pod from oversell management.
func (m *OversellManager) RemovePod(namespace, podName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := podKey(namespace, podName)
	pod, exists := m.pods[key]
	if !exists {
		return fmt.Errorf("pod %s not found", key)
	}

	// Remove pod cgroup
	if err := m.cgroupManager.DeleteCgroup(pod.CgroupPath); err != nil {
		log.WithError(err).Warn("Failed to delete pod cgroup")
	}

	delete(m.pods, key)
	m.updateStatus()

	log.WithFields(logrus.Fields{
		"namespace": namespace,
		"pod":       podName,
	}).Info("Pod removed from oversell management")

	return nil
}

// UpdatePodMemory updates the memory usage for a pod.
func (m *OversellManager) UpdatePodMemory(namespace, podName string, memoryBytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := podKey(namespace, podName)
	pod, exists := m.pods[key]
	if !exists {
		return fmt.Errorf("pod %s not found", key)
	}

	pod.CurrentRSSBytes = memoryBytes
	m.updateStatus()

	return nil
}

// GetPods returns all managed pods.
func (m *OversellManager) GetPods() []types.PodMemoryInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pods := make([]types.PodMemoryInfo, 0, len(m.pods))
	for _, pod := range m.pods {
		pods = append(pods, *pod)
	}
	return pods
}

// AdjustRatioGradually adjusts the oversell ratio gradually.
func (m *OversellManager) AdjustRatioGradually(targetRatio float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	currentRatio := m.status.CurrentRatio
	diff := targetRatio - currentRatio

	// If the difference is small, just set it directly
	if abs(diff) <= m.config.AdjustmentStep {
		return m.setRatioLocked(targetRatio)
	}

	// Adjust by step
	var newRatio float64
	if diff > 0 {
		newRatio = currentRatio + m.config.AdjustmentStep
	} else {
		newRatio = currentRatio - m.config.AdjustmentStep
	}

	return m.setRatioLocked(newRatio)
}

// setRatioLocked sets the ratio (caller must hold the lock).
func (m *OversellManager) setRatioLocked(ratio float64) error {
	if ratio < m.config.MinRatio || ratio > m.config.MaxRatio {
		return fmt.Errorf("ratio %f out of range [%f, %f]", ratio, m.config.MinRatio, m.config.MaxRatio)
	}

	m.status.CurrentRatio = ratio
	m.status.LogicalMemoryBytes = int64(float64(m.nodeMemory.TotalBytes) * ratio)

	return nil
}

// GetNodeMemory returns the node memory information.
func (m *OversellManager) GetNodeMemory() types.NodeMemoryInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodeMemory
}

// UpdateNodeMemory updates the node memory information.
func (m *OversellManager) UpdateNodeMemory(info types.NodeMemoryInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nodeMemory = info
	m.status.PhysicalMemoryBytes = info.TotalBytes
	m.updateStatus()
}

// podKey generates a unique key for a pod.
func podKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// abs returns the absolute value of a float64.
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

