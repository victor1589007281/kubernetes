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

package monitor

import (
	"sync"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/oversell"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
	"github.com/sirupsen/logrus"
)

// Adjuster handles automatic adjustment of oversell ratio based on alerts.
type Adjuster struct {
	mu sync.RWMutex

	// config holds the monitor configuration.
	config types.MonitorConfig

	// manager is the oversell manager to adjust.
	manager *oversell.OversellManager

	// lastAdjustment stores the time of the last adjustment.
	lastAdjustment time.Time

	// adjustmentHistory stores the history of adjustments.
	adjustmentHistory []AdjustmentRecord

	// enabled indicates whether auto-adjustment is enabled.
	enabled bool
}

// AdjustmentRecord records an adjustment operation.
type AdjustmentRecord struct {
	Timestamp  time.Time
	OldRatio   float64
	NewRatio   float64
	Reason     string
	AlertLevel types.AlertLevel
}

// NewAdjuster creates a new Adjuster.
func NewAdjuster(config types.MonitorConfig, manager *oversell.OversellManager) *Adjuster {
	return &Adjuster{
		config:            config,
		manager:           manager,
		adjustmentHistory: make([]AdjustmentRecord, 0),
		enabled:           config.EnableAutoAdjust,
	}
}

// Enable enables auto-adjustment.
func (a *Adjuster) Enable() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enabled = true
}

// Disable disables auto-adjustment.
func (a *Adjuster) Disable() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enabled = false
}

// IsEnabled returns whether auto-adjustment is enabled.
func (a *Adjuster) IsEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.enabled
}

// HandleAlert handles an alert and adjusts the oversell ratio if necessary.
func (a *Adjuster) HandleAlert(alert types.Alert) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.enabled {
		return
	}

	// Check cooldown
	if time.Since(a.lastAdjustment) < a.config.AdjustmentCooldown {
		log.WithField("cooldown_remaining", a.config.AdjustmentCooldown-time.Since(a.lastAdjustment)).
			Debug("Adjustment skipped due to cooldown")
		return
	}

	// Determine action based on alert level
	switch alert.Level {
	case types.AlertLevelEmergency:
		a.handleEmergency(alert)
	case types.AlertLevelCritical:
		a.handleCritical(alert)
	case types.AlertLevelWarning:
		a.handleWarning(alert)
	case types.AlertLevelInfo:
		// No adjustment for info alerts
		return
	}
}

// handleEmergency handles an emergency alert.
func (a *Adjuster) handleEmergency(alert types.Alert) {
	status := a.manager.GetStatus()
	oldRatio := status.CurrentRatio

	// Immediately set ratio to 1.0 (no overselling)
	if err := a.manager.SetRatio(1.0); err != nil {
		log.WithError(err).Error("Failed to set emergency ratio")
		return
	}

	a.recordAdjustment(oldRatio, 1.0, "Emergency: Memory usage critical", alert.Level)

	// Trigger pod eviction if enabled
	if a.config.EnableEviction {
		a.triggerEviction(alert)
	}

	log.WithFields(logrus.Fields{
		"old_ratio": oldRatio,
		"new_ratio": 1.0,
		"reason":    "emergency",
	}).Warn("Emergency adjustment: Overselling disabled")
}

// handleCritical handles a critical alert.
func (a *Adjuster) handleCritical(alert types.Alert) {
	status := a.manager.GetStatus()
	oldRatio := status.CurrentRatio

	// Set ratio to 1.0 or recommended, whichever is lower
	targetRatio := 1.0
	if alert.RecommendedRatio < targetRatio {
		targetRatio = alert.RecommendedRatio
	}

	if err := a.manager.SetRatio(targetRatio); err != nil {
		log.WithError(err).Error("Failed to set critical ratio")
		return
	}

	a.recordAdjustment(oldRatio, targetRatio, "Critical: Memory usage high", alert.Level)

	log.WithFields(logrus.Fields{
		"old_ratio": oldRatio,
		"new_ratio": targetRatio,
		"reason":    "critical",
	}).Warn("Critical adjustment: Reduced oversell ratio")
}

// handleWarning handles a warning alert.
func (a *Adjuster) handleWarning(alert types.Alert) {
	status := a.manager.GetStatus()
	oldRatio := status.CurrentRatio

	// Gradually reduce ratio
	if err := a.manager.AdjustRatioGradually(alert.RecommendedRatio); err != nil {
		log.WithError(err).Error("Failed to adjust ratio gradually")
		return
	}

	newStatus := a.manager.GetStatus()
	a.recordAdjustment(oldRatio, newStatus.CurrentRatio, "Warning: Memory usage elevated", alert.Level)

	log.WithFields(logrus.Fields{
		"old_ratio": oldRatio,
		"new_ratio": newStatus.CurrentRatio,
		"reason":    "warning",
	}).Info("Warning adjustment: Gradually reducing oversell ratio")
}

// triggerEviction triggers pod eviction.
func (a *Adjuster) triggerEviction(alert types.Alert) {
	log.Warn("Triggering pod eviction due to memory emergency")

	pods := a.manager.GetPods()
	if len(pods) == 0 {
		return
	}

	// Calculate how much memory needs to be freed
	nodeMemory := a.manager.GetNodeMemory()
	targetUsage := float64(nodeMemory.TotalBytes) * 0.7 // Target 70% usage
	currentUsage := float64(nodeMemory.UsedBytes)
	bytesToFree := int64(currentUsage - targetUsage)

	if bytesToFree <= 0 {
		return
	}

	// Use calculator to select pods for eviction
	calculator := oversell.NewCalculator(types.DefaultOversellConfig())
	podsToEvict := calculator.SelectPodsForEviction(pods, bytesToFree)

	for _, pod := range podsToEvict {
		log.WithFields(logrus.Fields{
			"namespace": pod.Namespace,
			"pod":       pod.PodName,
			"rss":       pod.CurrentRSSBytes,
		}).Warn("Pod selected for eviction")

		// In a real implementation, this would call the Kubernetes API to evict the pod
		// For now, we just log the intent
		if err := a.manager.RemovePod(pod.Namespace, pod.PodName); err != nil {
			log.WithError(err).Error("Failed to remove pod from oversell management")
		}
	}
}

// recordAdjustment records an adjustment in history.
func (a *Adjuster) recordAdjustment(oldRatio, newRatio float64, reason string, level types.AlertLevel) {
	a.lastAdjustment = time.Now()

	record := AdjustmentRecord{
		Timestamp:  time.Now(),
		OldRatio:   oldRatio,
		NewRatio:   newRatio,
		Reason:     reason,
		AlertLevel: level,
	}

	a.adjustmentHistory = append(a.adjustmentHistory, record)

	// Keep only last 100 records
	if len(a.adjustmentHistory) > 100 {
		a.adjustmentHistory = a.adjustmentHistory[len(a.adjustmentHistory)-100:]
	}
}

// GetHistory returns the adjustment history.
func (a *Adjuster) GetHistory() []AdjustmentRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]AdjustmentRecord, len(a.adjustmentHistory))
	copy(result, a.adjustmentHistory)
	return result
}

// GetLastAdjustment returns the last adjustment time.
func (a *Adjuster) GetLastAdjustment() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastAdjustment
}

// SetCooldown sets the adjustment cooldown period.
func (a *Adjuster) SetCooldown(cooldown time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.AdjustmentCooldown = cooldown
}

// RecoverRatio attempts to recover the oversell ratio to a higher value.
// This should be called when memory pressure has decreased.
func (a *Adjuster) RecoverRatio(targetRatio float64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.enabled {
		return nil
	}

	// Check cooldown
	if time.Since(a.lastAdjustment) < a.config.AdjustmentCooldown {
		return nil
	}

	status := a.manager.GetStatus()
	if status.CurrentRatio >= targetRatio {
		return nil
	}

	// Gradually increase ratio
	if err := a.manager.AdjustRatioGradually(targetRatio); err != nil {
		return err
	}

	newStatus := a.manager.GetStatus()
	a.recordAdjustment(status.CurrentRatio, newStatus.CurrentRatio, "Recovery: Memory pressure decreased", types.AlertLevelInfo)

	log.WithFields(logrus.Fields{
		"old_ratio": status.CurrentRatio,
		"new_ratio": newStatus.CurrentRatio,
		"reason":    "recovery",
	}).Info("Recovery adjustment: Increasing oversell ratio")

	return nil
}

// GetAdjustmentStats returns statistics about adjustments.
func (a *Adjuster) GetAdjustmentStats() AdjustmentStats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	stats := AdjustmentStats{
		TotalAdjustments: len(a.adjustmentHistory),
	}

	if len(a.adjustmentHistory) == 0 {
		return stats
	}

	for _, record := range a.adjustmentHistory {
		switch record.AlertLevel {
		case types.AlertLevelWarning:
			stats.WarningAdjustments++
		case types.AlertLevelCritical:
			stats.CriticalAdjustments++
		case types.AlertLevelEmergency:
			stats.EmergencyAdjustments++
		}
	}

	// Calculate average adjustment size
	var totalChange float64
	for _, record := range a.adjustmentHistory {
		totalChange += abs(record.NewRatio - record.OldRatio)
	}
	stats.AverageAdjustmentSize = totalChange / float64(len(a.adjustmentHistory))

	return stats
}

// AdjustmentStats holds statistics about adjustments.
type AdjustmentStats struct {
	TotalAdjustments      int
	WarningAdjustments    int
	CriticalAdjustments   int
	EmergencyAdjustments  int
	AverageAdjustmentSize float64
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

