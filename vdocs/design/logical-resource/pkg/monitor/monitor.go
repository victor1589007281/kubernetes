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

// Package monitor provides memory monitoring and alerting capabilities.
package monitor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/oversell"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// Monitor monitors memory usage and triggers alerts.
type Monitor struct {
	mu sync.RWMutex

	// config holds the monitor configuration.
	config types.MonitorConfig

	// oversellManager is the oversell manager to monitor and adjust.
	oversellManager *oversell.OversellManager

	// alerter handles alert notifications.
	alerter *Alerter

	// adjuster handles automatic ratio adjustments.
	adjuster *Adjuster

	// lastNodeMemory stores the last collected node memory info.
	lastNodeMemory types.NodeMemoryInfo

	// lastAlert stores the last triggered alert.
	lastAlert *types.Alert

	// running indicates whether the monitor is running.
	running bool

	// stopCh is used to signal stop.
	stopCh chan struct{}

	// metrics stores historical metrics for analysis.
	metrics []MemoryMetric

	// alertHistory stores alert history.
	alertHistory []types.Alert
}

// MemoryMetric represents a memory metric sample.
type MemoryMetric struct {
	Timestamp      time.Time
	NodeMemory     types.NodeMemoryInfo
	OversellStatus types.OversellStatus
	UsageRatio     float64
}

// NewMonitor creates a new Monitor.
func NewMonitor(
	config types.MonitorConfig,
	manager *oversell.OversellManager,
) *Monitor {
	return &Monitor{
		config:          config,
		oversellManager: manager,
		alerter:         NewAlerter(),
		adjuster:        NewAdjuster(config, manager),
		stopCh:          make(chan struct{}),
		metrics:         make([]MemoryMetric, 0),
		alertHistory:    make([]types.Alert, 0),
	}
}

// Start starts the monitor.
func (m *Monitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("monitor is already running")
	}
	m.running = true
	m.mu.Unlock()

	go m.monitorLoop(ctx)

	log.Info("Memory monitor started")
	return nil
}

// Stop stops the monitor.
func (m *Monitor) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	close(m.stopCh)
	m.running = false

	log.Info("Memory monitor stopped")
	return nil
}

// monitorLoop runs the monitoring loop.
func (m *Monitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.CollectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.collect()
		}
	}
}

// collect collects memory metrics and checks for alerts.
func (m *Monitor) collect() {
	// Collect node memory info
	nodeMemory, err := m.collectNodeMemory()
	if err != nil {
		log.WithError(err).Warn("Failed to collect node memory")
		return
	}

	m.mu.Lock()
	m.lastNodeMemory = nodeMemory
	m.mu.Unlock()

	// Update oversell manager with current node memory
	m.oversellManager.UpdateNodeMemory(nodeMemory)

	// Get oversell status
	status := m.oversellManager.GetStatus()

	// Calculate usage ratio
	usageRatio := float64(nodeMemory.UsedBytes) / float64(nodeMemory.TotalBytes)

	// Store metric
	metric := MemoryMetric{
		Timestamp:      time.Now(),
		NodeMemory:     nodeMemory,
		OversellStatus: status,
		UsageRatio:     usageRatio,
	}

	m.mu.Lock()
	m.metrics = append(m.metrics, metric)
	// Keep only last 24 hours of metrics
	cutoff := time.Now().Add(-24 * time.Hour)
	newMetrics := make([]MemoryMetric, 0)
	for _, met := range m.metrics {
		if met.Timestamp.After(cutoff) {
			newMetrics = append(newMetrics, met)
		}
	}
	m.metrics = newMetrics
	m.mu.Unlock()

	// Check alert thresholds
	m.checkAlerts(usageRatio, status)

	log.WithFields(logrus.Fields{
		"usage_ratio":    usageRatio,
		"used_bytes":     nodeMemory.UsedBytes,
		"total_bytes":    nodeMemory.TotalBytes,
		"oversell_ratio": status.CurrentRatio,
	}).Debug("Memory metrics collected")
}

// collectNodeMemory collects node memory information from /proc/meminfo.
func (m *Monitor) collectNodeMemory() (types.NodeMemoryInfo, error) {
	info := types.NodeMemoryInfo{}

	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return info, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		// Convert from KB to bytes
		value *= 1024

		switch key {
		case "MemTotal":
			info.TotalBytes = value
		case "MemFree":
			// Free memory (not including buffers/cache)
		case "MemAvailable":
			info.AvailableBytes = value
		case "Buffers":
			info.BuffersBytes = value
		case "Cached":
			info.CachedBytes = value
		case "HugePages_Total":
			info.HugePagesTotal = value / 1024 // Convert back to count
		case "HugePages_Free":
			info.HugePagesFree = value / 1024
		case "Hugepagesize":
			info.HugePageSize = value
		}
	}

	// Calculate used memory
	info.UsedBytes = info.TotalBytes - info.AvailableBytes

	return info, scanner.Err()
}

// checkAlerts checks if any alert thresholds are exceeded.
func (m *Monitor) checkAlerts(usageRatio float64, status types.OversellStatus) {
	thresholds := m.config.AlertThresholds

	var level types.AlertLevel
	var actions []string

	switch {
	case usageRatio >= thresholds.Emergency:
		level = types.AlertLevelEmergency
		actions = []string{
			"Immediately evict low-priority pods",
			"Send PagerDuty alert",
			"Disable overselling",
		}
	case usageRatio >= thresholds.Critical:
		level = types.AlertLevelCritical
		actions = []string{
			"Reduce oversell ratio to 1.0",
			"Trigger pod eviction",
			"Block new pod scheduling",
		}
	case usageRatio >= thresholds.Warning:
		level = types.AlertLevelWarning
		actions = []string{
			"Send alert notification",
			"Start reducing oversell ratio",
			"Limit new pod scheduling",
		}
	case usageRatio >= thresholds.Info:
		level = types.AlertLevelInfo
		actions = []string{
			"Log warning",
			"Send notification",
		}
	default:
		return // No alert needed
	}

	alert := types.Alert{
		Level:            level,
		Message:          fmt.Sprintf("Memory usage at %.1f%%", usageRatio*100),
		Timestamp:        time.Now(),
		MemoryUsageRatio: usageRatio,
		RecommendedRatio: m.calculateRecommendedRatio(usageRatio),
		Actions:          actions,
	}

	// Check if we should send this alert (avoid spam)
	if m.shouldSendAlert(alert) {
		m.triggerAlert(alert)
	}
}

// shouldSendAlert determines if an alert should be sent.
func (m *Monitor) shouldSendAlert(alert types.Alert) bool {
	m.mu.RLock()
	lastAlert := m.lastAlert
	m.mu.RUnlock()

	if lastAlert == nil {
		return true
	}

	// Don't send same level alert within 5 minutes
	if alert.Level == lastAlert.Level && time.Since(lastAlert.Timestamp) < 5*time.Minute {
		return false
	}

	// Always send higher level alerts
	if alert.Level > lastAlert.Level {
		return true
	}

	return time.Since(lastAlert.Timestamp) >= 5*time.Minute
}

// triggerAlert triggers an alert.
func (m *Monitor) triggerAlert(alert types.Alert) {
	m.mu.Lock()
	m.lastAlert = &alert
	m.alertHistory = append(m.alertHistory, alert)
	// Keep only last 100 alerts
	if len(m.alertHistory) > 100 {
		m.alertHistory = m.alertHistory[len(m.alertHistory)-100:]
	}
	m.mu.Unlock()

	// Send alert via alerter
	m.alerter.SendAlert(alert)

	// Take automatic actions if enabled
	if m.config.EnableAutoAdjust {
		m.adjuster.HandleAlert(alert)
	}

	log.WithFields(logrus.Fields{
		"level":   alert.Level.String(),
		"message": alert.Message,
		"ratio":   alert.MemoryUsageRatio,
	}).Warn("Alert triggered")
}

// calculateRecommendedRatio calculates the recommended oversell ratio based on current usage.
func (m *Monitor) calculateRecommendedRatio(usageRatio float64) float64 {
	// If usage is high, recommend lower oversell ratio
	if usageRatio >= 0.9 {
		return 1.0 // No overselling
	}
	if usageRatio >= 0.8 {
		return 1.2
	}
	if usageRatio >= 0.7 {
		return 1.5
	}
	return 2.0 // Maximum recommended
}

// GetLastNodeMemory returns the last collected node memory info.
func (m *Monitor) GetLastNodeMemory() types.NodeMemoryInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastNodeMemory
}

// GetMetrics returns the collected metrics.
func (m *Monitor) GetMetrics() []MemoryMetric {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]MemoryMetric, len(m.metrics))
	copy(result, m.metrics)
	return result
}

// GetAlertHistory returns the alert history.
func (m *Monitor) GetAlertHistory() []types.Alert {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]types.Alert, len(m.alertHistory))
	copy(result, m.alertHistory)
	return result
}

// GetCurrentUsageRatio returns the current memory usage ratio.
func (m *Monitor) GetCurrentUsageRatio() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.lastNodeMemory.TotalBytes == 0 {
		return 0
	}
	return float64(m.lastNodeMemory.UsedBytes) / float64(m.lastNodeMemory.TotalBytes)
}

// IsHealthy returns whether the system is healthy based on memory usage.
func (m *Monitor) IsHealthy() bool {
	ratio := m.GetCurrentUsageRatio()
	return ratio < m.config.AlertThresholds.Warning
}

// ForceCollect forces an immediate collection of metrics.
func (m *Monitor) ForceCollect() error {
	m.collect()
	return nil
}

// SetAlertThresholds updates the alert thresholds.
func (m *Monitor) SetAlertThresholds(thresholds types.AlertThresholds) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.AlertThresholds = thresholds
}

// GetAverageUsage returns the average memory usage over the specified duration.
func (m *Monitor) GetAverageUsage(duration time.Duration) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cutoff := time.Now().Add(-duration)
	var sum float64
	var count int

	for _, metric := range m.metrics {
		if metric.Timestamp.After(cutoff) {
			sum += metric.UsageRatio
			count++
		}
	}

	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// GetPeakUsage returns the peak memory usage over the specified duration.
func (m *Monitor) GetPeakUsage(duration time.Duration) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cutoff := time.Now().Add(-duration)
	var peak float64

	for _, metric := range m.metrics {
		if metric.Timestamp.After(cutoff) && metric.UsageRatio > peak {
			peak = metric.UsageRatio
		}
	}

	return peak
}

