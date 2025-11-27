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

// Package types defines common types used across the logical memory oversell system.
package types

import (
	"time"
)

// MemoryDataPoint represents a single memory usage data point.
type MemoryDataPoint struct {
	// Timestamp is the time when this data point was collected.
	Timestamp time.Time `json:"timestamp"`

	// ActualUsageBytes is the actual memory usage in bytes (RSS).
	ActualUsageBytes int64 `json:"actual_usage_bytes"`

	// BufferPoolBytes is the configured buffer pool size in bytes.
	BufferPoolBytes int64 `json:"buffer_pool_bytes"`

	// UsageRatio is the ratio of actual usage to buffer pool size.
	// Calculated as ActualUsageBytes / BufferPoolBytes.
	UsageRatio float64 `json:"usage_ratio"`

	// PodName is the name of the pod this data point belongs to.
	PodName string `json:"pod_name"`

	// Namespace is the namespace of the pod.
	Namespace string `json:"namespace"`
}

// PredictionResult represents the result of a memory usage prediction.
type PredictionResult struct {
	// Timestamp is the time for which the prediction is made.
	Timestamp time.Time `json:"timestamp"`

	// PredictedUsageBytes is the predicted memory usage in bytes.
	PredictedUsageBytes int64 `json:"predicted_usage_bytes"`

	// PredictedUsageRatio is the predicted usage ratio.
	PredictedUsageRatio float64 `json:"predicted_usage_ratio"`

	// ConfidenceLower is the lower bound of the confidence interval.
	ConfidenceLower int64 `json:"confidence_lower"`

	// ConfidenceUpper is the upper bound of the confidence interval.
	ConfidenceUpper int64 `json:"confidence_upper"`

	// ConfidenceLevel is the confidence level (e.g., 0.95 for 95%).
	ConfidenceLevel float64 `json:"confidence_level"`
}

// BufferPoolChangeEvent represents a buffer pool configuration change event.
type BufferPoolChangeEvent struct {
	// Timestamp is when the change occurred.
	Timestamp time.Time `json:"timestamp"`

	// PodName is the name of the affected pod.
	PodName string `json:"pod_name"`

	// Namespace is the namespace of the pod.
	Namespace string `json:"namespace"`

	// OldSizeBytes is the old buffer pool size.
	OldSizeBytes int64 `json:"old_size_bytes"`

	// NewSizeBytes is the new buffer pool size.
	NewSizeBytes int64 `json:"new_size_bytes"`
}

// OversellConfig holds the configuration for memory overselling.
type OversellConfig struct {
	// MaxRatio is the maximum allowed oversell ratio.
	MaxRatio float64 `json:"max_ratio"`

	// SafetyFactor is the safety margin factor (0.0-1.0).
	SafetyFactor float64 `json:"safety_factor"`

	// MinRatio is the minimum oversell ratio (usually 1.0).
	MinRatio float64 `json:"min_ratio"`

	// AdjustmentStep is the step size for ratio adjustments.
	AdjustmentStep float64 `json:"adjustment_step"`
}

// OversellStatus represents the current oversell status.
type OversellStatus struct {
	// CurrentRatio is the current oversell ratio.
	CurrentRatio float64 `json:"current_ratio"`

	// SafeRatio is the calculated safe oversell ratio based on predictions.
	SafeRatio float64 `json:"safe_ratio"`

	// PhysicalMemoryBytes is the total physical memory in bytes.
	PhysicalMemoryBytes int64 `json:"physical_memory_bytes"`

	// LogicalMemoryBytes is the total logical memory (physical * ratio).
	LogicalMemoryBytes int64 `json:"logical_memory_bytes"`

	// AllocatedLogicalBytes is the allocated logical memory.
	AllocatedLogicalBytes int64 `json:"allocated_logical_bytes"`

	// ActualUsageBytes is the actual physical memory usage.
	ActualUsageBytes int64 `json:"actual_usage_bytes"`

	// LastUpdated is when the status was last updated.
	LastUpdated time.Time `json:"last_updated"`
}

// AlertLevel represents the severity level of an alert.
type AlertLevel int

const (
	// AlertLevelInfo is for informational alerts.
	AlertLevelInfo AlertLevel = iota
	// AlertLevelWarning is for warning alerts.
	AlertLevelWarning
	// AlertLevelCritical is for critical alerts.
	AlertLevelCritical
	// AlertLevelEmergency is for emergency alerts.
	AlertLevelEmergency
)

// String returns the string representation of the alert level.
func (l AlertLevel) String() string {
	switch l {
	case AlertLevelInfo:
		return "INFO"
	case AlertLevelWarning:
		return "WARNING"
	case AlertLevelCritical:
		return "CRITICAL"
	case AlertLevelEmergency:
		return "EMERGENCY"
	default:
		return "UNKNOWN"
	}
}

// Alert represents a memory alert.
type Alert struct {
	// Level is the severity level of the alert.
	Level AlertLevel `json:"level"`

	// Message is the alert message.
	Message string `json:"message"`

	// Timestamp is when the alert was triggered.
	Timestamp time.Time `json:"timestamp"`

	// MemoryUsageRatio is the memory usage ratio that triggered the alert.
	MemoryUsageRatio float64 `json:"memory_usage_ratio"`

	// RecommendedRatio is the recommended oversell ratio.
	RecommendedRatio float64 `json:"recommended_ratio"`

	// Actions is the list of actions to be taken.
	Actions []string `json:"actions"`
}

// AlertThresholds defines the thresholds for different alert levels.
type AlertThresholds struct {
	// Info threshold (e.g., 0.60 for 60%).
	Info float64 `json:"info"`

	// Warning threshold (e.g., 0.75 for 75%).
	Warning float64 `json:"warning"`

	// Critical threshold (e.g., 0.85 for 85%).
	Critical float64 `json:"critical"`

	// Emergency threshold (e.g., 0.95 for 95%).
	Emergency float64 `json:"emergency"`
}

// PodMemoryInfo contains memory information for a pod.
type PodMemoryInfo struct {
	// PodName is the name of the pod.
	PodName string `json:"pod_name"`

	// Namespace is the namespace of the pod.
	Namespace string `json:"namespace"`

	// MemoryLimitBytes is the memory limit in bytes.
	MemoryLimitBytes int64 `json:"memory_limit_bytes"`

	// MemoryRequestBytes is the memory request in bytes.
	MemoryRequestBytes int64 `json:"memory_request_bytes"`

	// CurrentRSSBytes is the current RSS memory usage.
	CurrentRSSBytes int64 `json:"current_rss_bytes"`

	// BufferPoolBytes is the MySQL buffer pool size.
	BufferPoolBytes int64 `json:"buffer_pool_bytes"`

	// CgroupPath is the cgroup path for this pod.
	CgroupPath string `json:"cgroup_path"`

	// IsOversellEnabled indicates if overselling is enabled for this pod.
	IsOversellEnabled bool `json:"is_oversell_enabled"`

	// Priority is the pod priority for eviction ordering.
	Priority int32 `json:"priority"`
}

// NodeMemoryInfo contains memory information for a node.
type NodeMemoryInfo struct {
	// NodeName is the name of the node.
	NodeName string `json:"node_name"`

	// TotalBytes is the total physical memory.
	TotalBytes int64 `json:"total_bytes"`

	// AvailableBytes is the available memory.
	AvailableBytes int64 `json:"available_bytes"`

	// UsedBytes is the used memory.
	UsedBytes int64 `json:"used_bytes"`

	// CachedBytes is the cached memory.
	CachedBytes int64 `json:"cached_bytes"`

	// BuffersBytes is the buffer memory.
	BuffersBytes int64 `json:"buffers_bytes"`

	// HugePagesTotal is the total number of huge pages.
	HugePagesTotal int64 `json:"hugepages_total"`

	// HugePagesFree is the number of free huge pages.
	HugePagesFree int64 `json:"hugepages_free"`

	// HugePageSize is the size of each huge page in bytes.
	HugePageSize int64 `json:"hugepage_size"`
}

// PredictorConfig holds the configuration for the predictor.
type PredictorConfig struct {
	// HistoryDays is the number of days of historical data to use.
	HistoryDays int `json:"history_days"`

	// ForecastDays is the number of days to forecast.
	ForecastDays int `json:"forecast_days"`

	// Alpha is the data smoothing coefficient for Holt-Winters.
	Alpha float64 `json:"alpha"`

	// Beta is the trend smoothing coefficient for Holt-Winters.
	Beta float64 `json:"beta"`

	// Gamma is the seasonality smoothing coefficient for Holt-Winters.
	Gamma float64 `json:"gamma"`

	// SeasonalPeriod is the seasonal period in hours (24 for daily).
	SeasonalPeriod int `json:"seasonal_period"`

	// BufferPoolChangeGracePeriod is the grace period after buffer pool change.
	BufferPoolChangeGracePeriod time.Duration `json:"buffer_pool_change_grace_period"`
}

// MonitorConfig holds the configuration for the monitor.
type MonitorConfig struct {
	// CollectInterval is the interval between data collections.
	CollectInterval time.Duration `json:"collect_interval"`

	// AlertThresholds defines the thresholds for alerts.
	AlertThresholds AlertThresholds `json:"alert_thresholds"`

	// AdjustmentCooldown is the minimum time between adjustments.
	AdjustmentCooldown time.Duration `json:"adjustment_cooldown"`

	// EnableAutoAdjust enables automatic ratio adjustment.
	EnableAutoAdjust bool `json:"enable_auto_adjust"`

	// EnableEviction enables pod eviction in emergency.
	EnableEviction bool `json:"enable_eviction"`
}

// CgroupVersion represents the cgroup version.
type CgroupVersion int

const (
	// CgroupV1 is cgroup version 1.
	CgroupV1 CgroupVersion = 1
	// CgroupV2 is cgroup version 2.
	CgroupV2 CgroupVersion = 2
)

// CgroupMemoryConfig holds cgroup memory configuration.
type CgroupMemoryConfig struct {
	// Path is the cgroup path.
	Path string `json:"path"`

	// MemoryMax is the maximum memory limit (memory.max for v2).
	MemoryMax int64 `json:"memory_max"`

	// MemoryHigh is the high memory threshold (memory.high for v2).
	MemoryHigh int64 `json:"memory_high"`

	// MemoryLow is the low memory protection (memory.low for v2).
	MemoryLow int64 `json:"memory_low"`

	// Version is the cgroup version.
	Version CgroupVersion `json:"version"`
}

// DefaultPredictorConfig returns the default predictor configuration.
func DefaultPredictorConfig() PredictorConfig {
	return PredictorConfig{
		HistoryDays:                 14,
		ForecastDays:                3,
		Alpha:                       0.3,
		Beta:                        0.1,
		Gamma:                       0.2,
		SeasonalPeriod:              24,
		BufferPoolChangeGracePeriod: 72 * time.Hour,
	}
}

// DefaultOversellConfig returns the default oversell configuration.
func DefaultOversellConfig() OversellConfig {
	return OversellConfig{
		MaxRatio:       2.0,
		SafetyFactor:   0.85,
		MinRatio:       1.0,
		AdjustmentStep: 0.05,
	}
}

// DefaultMonitorConfig returns the default monitor configuration.
func DefaultMonitorConfig() MonitorConfig {
	return MonitorConfig{
		CollectInterval: 10 * time.Second,
		AlertThresholds: AlertThresholds{
			Info:      0.60,
			Warning:   0.75,
			Critical:  0.85,
			Emergency: 0.95,
		},
		AdjustmentCooldown: 5 * time.Minute,
		EnableAutoAdjust:   true,
		EnableEviction:     false,
	}
}

