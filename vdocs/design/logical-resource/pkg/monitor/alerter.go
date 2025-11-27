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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
	"github.com/sirupsen/logrus"
)

// AlertHandler is a function that handles alerts.
type AlertHandler func(alert types.Alert) error

// Alerter manages alert notifications.
type Alerter struct {
	mu sync.RWMutex

	// handlers stores registered alert handlers.
	handlers map[string]AlertHandler

	// webhookURLs stores configured webhook URLs.
	webhookURLs []string

	// httpClient is the HTTP client for sending webhooks.
	httpClient *http.Client

	// lastAlerts stores the last alert of each level for rate limiting.
	lastAlerts map[types.AlertLevel]time.Time

	// minInterval is the minimum interval between alerts of the same level.
	minInterval time.Duration
}

// NewAlerter creates a new Alerter.
func NewAlerter() *Alerter {
	return &Alerter{
		handlers:    make(map[string]AlertHandler),
		webhookURLs: make([]string, 0),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		lastAlerts:  make(map[types.AlertLevel]time.Time),
		minInterval: 5 * time.Minute,
	}
}

// RegisterHandler registers an alert handler.
func (a *Alerter) RegisterHandler(name string, handler AlertHandler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handlers[name] = handler
}

// UnregisterHandler unregisters an alert handler.
func (a *Alerter) UnregisterHandler(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.handlers, name)
}

// AddWebhook adds a webhook URL for alert notifications.
func (a *Alerter) AddWebhook(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.webhookURLs = append(a.webhookURLs, url)
}

// RemoveWebhook removes a webhook URL.
func (a *Alerter) RemoveWebhook(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	newURLs := make([]string, 0)
	for _, u := range a.webhookURLs {
		if u != url {
			newURLs = append(newURLs, u)
		}
	}
	a.webhookURLs = newURLs
}

// SetMinInterval sets the minimum interval between alerts.
func (a *Alerter) SetMinInterval(interval time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.minInterval = interval
}

// SendAlert sends an alert through all registered handlers and webhooks.
func (a *Alerter) SendAlert(alert types.Alert) {
	a.mu.RLock()
	// Check rate limiting
	if lastTime, exists := a.lastAlerts[alert.Level]; exists {
		if time.Since(lastTime) < a.minInterval {
			a.mu.RUnlock()
			log.WithField("level", alert.Level.String()).Debug("Alert rate limited")
			return
		}
	}
	a.mu.RUnlock()

	// Update last alert time
	a.mu.Lock()
	a.lastAlerts[alert.Level] = time.Now()
	a.mu.Unlock()

	// Log the alert
	a.logAlert(alert)

	// Send to all handlers
	a.mu.RLock()
	handlers := make(map[string]AlertHandler)
	for k, v := range a.handlers {
		handlers[k] = v
	}
	webhookURLs := make([]string, len(a.webhookURLs))
	copy(webhookURLs, a.webhookURLs)
	a.mu.RUnlock()

	for name, handler := range handlers {
		go func(n string, h AlertHandler) {
			if err := h(alert); err != nil {
				log.WithError(err).WithField("handler", n).Error("Alert handler failed")
			}
		}(name, handler)
	}

	// Send webhooks
	for _, url := range webhookURLs {
		go func(u string) {
			if err := a.sendWebhook(u, alert); err != nil {
				log.WithError(err).WithField("url", u).Error("Webhook failed")
			}
		}(url)
	}
}

// logAlert logs an alert with appropriate level.
func (a *Alerter) logAlert(alert types.Alert) {
	fields := logrus.Fields{
		"level":            alert.Level.String(),
		"message":          alert.Message,
		"memory_usage":     fmt.Sprintf("%.1f%%", alert.MemoryUsageRatio*100),
		"recommended_ratio": alert.RecommendedRatio,
		"actions":          alert.Actions,
	}

	switch alert.Level {
	case types.AlertLevelEmergency:
		log.WithFields(fields).Error("EMERGENCY ALERT")
	case types.AlertLevelCritical:
		log.WithFields(fields).Error("CRITICAL ALERT")
	case types.AlertLevelWarning:
		log.WithFields(fields).Warn("WARNING ALERT")
	case types.AlertLevelInfo:
		log.WithFields(fields).Info("INFO ALERT")
	}
}

// WebhookPayload represents the payload sent to webhooks.
type WebhookPayload struct {
	Alert     types.Alert `json:"alert"`
	Timestamp string      `json:"timestamp"`
	Source    string      `json:"source"`
}

// sendWebhook sends an alert to a webhook URL.
func (a *Alerter) sendWebhook(url string, alert types.Alert) error {
	payload := WebhookPayload{
		Alert:     alert,
		Timestamp: time.Now().Format(time.RFC3339),
		Source:    "logical-memory-oversell",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// SlackPayload represents a Slack webhook payload.
type SlackPayload struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment represents a Slack attachment.
type Attachment struct {
	Color  string  `json:"color"`
	Title  string  `json:"title"`
	Text   string  `json:"text"`
	Fields []Field `json:"fields,omitempty"`
}

// Field represents a Slack attachment field.
type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// SendSlackAlert sends an alert to a Slack webhook.
func (a *Alerter) SendSlackAlert(webhookURL string, alert types.Alert) error {
	color := "#00FF00" // Green for info
	switch alert.Level {
	case types.AlertLevelWarning:
		color = "#FFFF00" // Yellow
	case types.AlertLevelCritical:
		color = "#FF8000" // Orange
	case types.AlertLevelEmergency:
		color = "#FF0000" // Red
	}

	payload := SlackPayload{
		Text: fmt.Sprintf("Memory Alert: %s", alert.Level.String()),
		Attachments: []Attachment{
			{
				Color: color,
				Title: alert.Message,
				Fields: []Field{
					{
						Title: "Memory Usage",
						Value: fmt.Sprintf("%.1f%%", alert.MemoryUsageRatio*100),
						Short: true,
					},
					{
						Title: "Recommended Ratio",
						Value: fmt.Sprintf("%.2f", alert.RecommendedRatio),
						Short: true,
					},
					{
						Title: "Actions",
						Value: fmt.Sprintf("%v", alert.Actions),
						Short: false,
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// CreateDefaultHandlers creates and registers default alert handlers.
func (a *Alerter) CreateDefaultHandlers() {
	// Console handler (always enabled)
	a.RegisterHandler("console", func(alert types.Alert) error {
		fmt.Printf("[%s] %s - Usage: %.1f%%, Recommended Ratio: %.2f\n",
			alert.Level.String(),
			alert.Message,
			alert.MemoryUsageRatio*100,
			alert.RecommendedRatio,
		)
		return nil
	})
}

// AlertStats holds statistics about alerts.
type AlertStats struct {
	TotalAlerts       int
	InfoCount         int
	WarningCount      int
	CriticalCount     int
	EmergencyCount    int
	LastAlertTime     time.Time
	LastAlertLevel    types.AlertLevel
	AverageUsageRatio float64
}

// GetStats returns alert statistics.
func (a *Alerter) GetStats(alerts []types.Alert) AlertStats {
	stats := AlertStats{
		TotalAlerts: len(alerts),
	}

	if len(alerts) == 0 {
		return stats
	}

	var totalRatio float64
	for _, alert := range alerts {
		switch alert.Level {
		case types.AlertLevelInfo:
			stats.InfoCount++
		case types.AlertLevelWarning:
			stats.WarningCount++
		case types.AlertLevelCritical:
			stats.CriticalCount++
		case types.AlertLevelEmergency:
			stats.EmergencyCount++
		}
		totalRatio += alert.MemoryUsageRatio
	}

	lastAlert := alerts[len(alerts)-1]
	stats.LastAlertTime = lastAlert.Timestamp
	stats.LastAlertLevel = lastAlert.Level
	stats.AverageUsageRatio = totalRatio / float64(len(alerts))

	return stats
}

