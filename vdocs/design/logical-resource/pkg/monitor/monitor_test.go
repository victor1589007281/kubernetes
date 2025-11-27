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
	"context"
	"testing"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/oversell"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/predictor"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
)

func createTestMonitor() (*Monitor, *oversell.OversellManager) {
	config := types.DefaultMonitorConfig()
	config.CollectInterval = 100 * time.Millisecond // Fast for testing

	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{
		NodeName:       "test-node",
		TotalBytes:     128 * 1024 * 1024 * 1024,
		AvailableBytes: 64 * 1024 * 1024 * 1024,
		UsedBytes:      64 * 1024 * 1024 * 1024,
	}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	mon := NewMonitor(config, mgr)

	return mon, mgr
}

func TestNewMonitor(t *testing.T) {
	mon, _ := createTestMonitor()

	if mon == nil {
		t.Fatal("Expected non-nil monitor")
	}
}

func TestMonitorStartStop(t *testing.T) {
	mon, _ := createTestMonitor()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start should succeed
	err := mon.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}

	// Starting again should fail
	err = mon.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting already running monitor")
	}

	// Stop should succeed
	err = mon.Stop()
	if err != nil {
		t.Fatalf("Failed to stop monitor: %v", err)
	}
}

func TestMonitorGetCurrentUsageRatio(t *testing.T) {
	mon, _ := createTestMonitor()

	// Initially should be 0 (no collection yet)
	ratio := mon.GetCurrentUsageRatio()
	if ratio != 0 {
		t.Errorf("Expected 0 initial ratio, got %f", ratio)
	}
}

func TestMonitorIsHealthy(t *testing.T) {
	mon, mgr := createTestMonitor()

	// Update with healthy memory state
	healthyMemory := types.NodeMemoryInfo{
		TotalBytes:     100 * 1024 * 1024 * 1024,
		AvailableBytes: 70 * 1024 * 1024 * 1024, // 70% available = 30% usage
		UsedBytes:      30 * 1024 * 1024 * 1024,
	}
	mgr.UpdateNodeMemory(healthyMemory)

	// Force a collection
	mon.ForceCollect()

	if !mon.IsHealthy() {
		t.Error("Expected system to be healthy with 30% usage")
	}
}

func TestMonitorSetAlertThresholds(t *testing.T) {
	mon, _ := createTestMonitor()

	newThresholds := types.AlertThresholds{
		Info:      0.50,
		Warning:   0.65,
		Critical:  0.80,
		Emergency: 0.90,
	}

	mon.SetAlertThresholds(newThresholds)

	// The thresholds should be updated internally
	// This is verified indirectly through alert behavior
}

func TestMonitorGetMetrics(t *testing.T) {
	mon, _ := createTestMonitor()

	// Initially no metrics
	metrics := mon.GetMetrics()
	if len(metrics) != 0 {
		t.Errorf("Expected 0 initial metrics, got %d", len(metrics))
	}
}

func TestMonitorGetAlertHistory(t *testing.T) {
	mon, _ := createTestMonitor()

	history := mon.GetAlertHistory()
	if len(history) != 0 {
		t.Errorf("Expected 0 initial alerts, got %d", len(history))
	}
}

func TestMonitorAverageUsage(t *testing.T) {
	mon, _ := createTestMonitor()

	// Without data, should be 0
	avg := mon.GetAverageUsage(time.Hour)
	if avg != 0 {
		t.Errorf("Expected 0 average usage without data, got %f", avg)
	}
}

func TestMonitorPeakUsage(t *testing.T) {
	mon, _ := createTestMonitor()

	// Without data, should be 0
	peak := mon.GetPeakUsage(time.Hour)
	if peak != 0 {
		t.Errorf("Expected 0 peak usage without data, got %f", peak)
	}
}

// Alerter tests
func TestNewAlerter(t *testing.T) {
	alerter := NewAlerter()

	if alerter == nil {
		t.Fatal("Expected non-nil alerter")
	}
}

func TestAlerterRegisterHandler(t *testing.T) {
	alerter := NewAlerter()

	called := false
	handler := func(alert types.Alert) error {
		called = true
		return nil
	}

	alerter.RegisterHandler("test", handler)

	// Send an alert
	alert := types.Alert{
		Level:            types.AlertLevelInfo,
		Message:          "Test alert",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.5,
	}

	alerter.SendAlert(alert)

	// Wait a bit for async handler
	time.Sleep(100 * time.Millisecond)

	if !called {
		t.Error("Expected handler to be called")
	}
}

func TestAlerterUnregisterHandler(t *testing.T) {
	alerter := NewAlerter()

	called := false
	handler := func(alert types.Alert) error {
		called = true
		return nil
	}

	alerter.RegisterHandler("test", handler)
	alerter.UnregisterHandler("test")

	alert := types.Alert{
		Level:     types.AlertLevelInfo,
		Message:   "Test alert",
		Timestamp: time.Now(),
	}

	alerter.SendAlert(alert)
	time.Sleep(100 * time.Millisecond)

	if called {
		t.Error("Handler should not be called after unregistering")
	}
}

func TestAlerterAddWebhook(t *testing.T) {
	alerter := NewAlerter()

	alerter.AddWebhook("http://example.com/webhook")
	alerter.AddWebhook("http://example.com/webhook2")
	alerter.RemoveWebhook("http://example.com/webhook")

	// No way to directly verify, but this tests the API
}

func TestAlerterRateLimiting(t *testing.T) {
	alerter := NewAlerter()
	alerter.SetMinInterval(1 * time.Second)

	callCount := 0
	handler := func(alert types.Alert) error {
		callCount++
		return nil
	}

	alerter.RegisterHandler("test", handler)

	// Send multiple alerts quickly
	for i := 0; i < 5; i++ {
		alert := types.Alert{
			Level:     types.AlertLevelWarning,
			Message:   "Test alert",
			Timestamp: time.Now(),
		}
		alerter.SendAlert(alert)
	}

	time.Sleep(100 * time.Millisecond)

	// Due to rate limiting, only 1 should have been sent
	if callCount != 1 {
		t.Errorf("Expected 1 call due to rate limiting, got %d", callCount)
	}
}

func TestAlerterStats(t *testing.T) {
	alerter := NewAlerter()

	alerts := []types.Alert{
		{Level: types.AlertLevelInfo, MemoryUsageRatio: 0.5},
		{Level: types.AlertLevelWarning, MemoryUsageRatio: 0.7},
		{Level: types.AlertLevelCritical, MemoryUsageRatio: 0.9},
	}

	stats := alerter.GetStats(alerts)

	if stats.TotalAlerts != 3 {
		t.Errorf("Expected 3 total alerts, got %d", stats.TotalAlerts)
	}

	if stats.InfoCount != 1 {
		t.Errorf("Expected 1 info alert, got %d", stats.InfoCount)
	}

	if stats.WarningCount != 1 {
		t.Errorf("Expected 1 warning alert, got %d", stats.WarningCount)
	}

	if stats.CriticalCount != 1 {
		t.Errorf("Expected 1 critical alert, got %d", stats.CriticalCount)
	}
}

// Adjuster tests
func TestNewAdjuster(t *testing.T) {
	config := types.DefaultMonitorConfig()
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{
		TotalBytes:     128 * 1024 * 1024 * 1024,
		AvailableBytes: 64 * 1024 * 1024 * 1024,
		UsedBytes:      64 * 1024 * 1024 * 1024,
	}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	adjuster := NewAdjuster(config, mgr)

	if adjuster == nil {
		t.Fatal("Expected non-nil adjuster")
	}
}

func TestAdjusterEnableDisable(t *testing.T) {
	config := types.DefaultMonitorConfig()
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{TotalBytes: 128 * 1024 * 1024 * 1024}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	adjuster := NewAdjuster(config, mgr)

	// Initially enabled based on config
	if !adjuster.IsEnabled() {
		t.Error("Expected adjuster to be enabled")
	}

	adjuster.Disable()
	if adjuster.IsEnabled() {
		t.Error("Expected adjuster to be disabled")
	}

	adjuster.Enable()
	if !adjuster.IsEnabled() {
		t.Error("Expected adjuster to be enabled again")
	}
}

func TestAdjusterHandleWarningAlert(t *testing.T) {
	config := types.DefaultMonitorConfig()
	config.AdjustmentCooldown = 0 // No cooldown for testing
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{TotalBytes: 128 * 1024 * 1024 * 1024}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	
	// Set a higher ratio first
	mgr.SetRatio(1.5)

	adjuster := NewAdjuster(config, mgr)

	alert := types.Alert{
		Level:            types.AlertLevelWarning,
		Message:          "Memory usage elevated",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.75,
		RecommendedRatio: 1.2,
	}

	adjuster.HandleAlert(alert)

	// Check that ratio was adjusted
	status := mgr.GetStatus()
	if status.CurrentRatio >= 1.5 {
		t.Errorf("Expected ratio to decrease from 1.5, got %f", status.CurrentRatio)
	}
}

func TestAdjusterHandleCriticalAlert(t *testing.T) {
	config := types.DefaultMonitorConfig()
	config.AdjustmentCooldown = 0
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{TotalBytes: 128 * 1024 * 1024 * 1024}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	mgr.SetRatio(1.8)

	adjuster := NewAdjuster(config, mgr)

	alert := types.Alert{
		Level:            types.AlertLevelCritical,
		Message:          "Memory usage critical",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.85,
		RecommendedRatio: 1.0,
	}

	adjuster.HandleAlert(alert)

	// Check that ratio was set to 1.0
	status := mgr.GetStatus()
	if status.CurrentRatio != 1.0 {
		t.Errorf("Expected ratio 1.0 after critical alert, got %f", status.CurrentRatio)
	}
}

func TestAdjusterHandleEmergencyAlert(t *testing.T) {
	config := types.DefaultMonitorConfig()
	config.AdjustmentCooldown = 0
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{TotalBytes: 128 * 1024 * 1024 * 1024}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	mgr.SetRatio(2.0)

	adjuster := NewAdjuster(config, mgr)

	alert := types.Alert{
		Level:            types.AlertLevelEmergency,
		Message:          "Memory emergency",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.95,
		RecommendedRatio: 1.0,
	}

	adjuster.HandleAlert(alert)

	// Check that ratio was immediately set to 1.0
	status := mgr.GetStatus()
	if status.CurrentRatio != 1.0 {
		t.Errorf("Expected ratio 1.0 after emergency, got %f", status.CurrentRatio)
	}
}

func TestAdjusterCooldown(t *testing.T) {
	config := types.DefaultMonitorConfig()
	config.AdjustmentCooldown = 1 * time.Hour // Long cooldown
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{TotalBytes: 128 * 1024 * 1024 * 1024}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	mgr.SetRatio(1.5)

	adjuster := NewAdjuster(config, mgr)

	// First alert should trigger adjustment
	alert := types.Alert{
		Level:            types.AlertLevelWarning,
		Message:          "First warning",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.75,
		RecommendedRatio: 1.2,
	}
	adjuster.HandleAlert(alert)

	firstRatio := mgr.GetStatus().CurrentRatio

	// Second alert immediately after should be ignored due to cooldown
	alert2 := types.Alert{
		Level:            types.AlertLevelWarning,
		Message:          "Second warning",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.80,
		RecommendedRatio: 1.1,
	}
	adjuster.HandleAlert(alert2)

	secondRatio := mgr.GetStatus().CurrentRatio

	// Ratio should not have changed
	if firstRatio != secondRatio {
		t.Errorf("Expected ratio unchanged due to cooldown: %f != %f", firstRatio, secondRatio)
	}
}

func TestAdjusterHistory(t *testing.T) {
	config := types.DefaultMonitorConfig()
	config.AdjustmentCooldown = 0
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{TotalBytes: 128 * 1024 * 1024 * 1024}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)
	mgr.SetRatio(1.5)

	adjuster := NewAdjuster(config, mgr)

	alert := types.Alert{
		Level:            types.AlertLevelWarning,
		Message:          "Warning",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.75,
		RecommendedRatio: 1.2,
	}
	adjuster.HandleAlert(alert)

	history := adjuster.GetHistory()
	if len(history) == 0 {
		t.Error("Expected adjustment history to be recorded")
	}
}

func TestAdjusterStats(t *testing.T) {
	config := types.DefaultMonitorConfig()
	config.AdjustmentCooldown = 0
	oversellConfig := types.DefaultOversellConfig()
	pred := predictor.NewMemoryPredictor(types.DefaultPredictorConfig())
	nodeMemory := types.NodeMemoryInfo{TotalBytes: 128 * 1024 * 1024 * 1024}

	mgr := oversell.NewOversellManager(oversellConfig, pred, nodeMemory)

	adjuster := NewAdjuster(config, mgr)

	// Trigger some adjustments
	mgr.SetRatio(1.5)
	alert1 := types.Alert{
		Level:            types.AlertLevelWarning,
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.75,
		RecommendedRatio: 1.2,
	}
	adjuster.HandleAlert(alert1)

	mgr.SetRatio(1.8)
	alert2 := types.Alert{
		Level:            types.AlertLevelCritical,
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.85,
		RecommendedRatio: 1.0,
	}
	adjuster.HandleAlert(alert2)

	stats := adjuster.GetAdjustmentStats()

	if stats.TotalAdjustments < 2 {
		t.Errorf("Expected at least 2 adjustments, got %d", stats.TotalAdjustments)
	}
}

// Alert level string tests
func TestAlertLevelString(t *testing.T) {
	tests := []struct {
		level    types.AlertLevel
		expected string
	}{
		{types.AlertLevelInfo, "INFO"},
		{types.AlertLevelWarning, "WARNING"},
		{types.AlertLevelCritical, "CRITICAL"},
		{types.AlertLevelEmergency, "EMERGENCY"},
		{types.AlertLevel(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		result := tt.level.String()
		if result != tt.expected {
			t.Errorf("Expected %s, got %s", tt.expected, result)
		}
	}
}

// Benchmark tests
func BenchmarkMonitorCollect(b *testing.B) {
	mon, _ := createTestMonitor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mon.ForceCollect()
	}
}

func BenchmarkAlerterSendAlert(b *testing.B) {
	alerter := NewAlerter()
	alerter.SetMinInterval(0) // Disable rate limiting

	alert := types.Alert{
		Level:            types.AlertLevelInfo,
		Message:          "Benchmark alert",
		Timestamp:        time.Now(),
		MemoryUsageRatio: 0.5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alerter.SendAlert(alert)
	}
}

