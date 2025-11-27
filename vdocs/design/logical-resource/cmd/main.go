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

// Package main provides the entry point for the logical memory oversell system.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/monitor"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/oversell"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/predictor"
	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// Config holds the application configuration.
type Config struct {
	// LogLevel is the logging level.
	LogLevel string `json:"log_level"`

	// HTTPPort is the port for the HTTP server.
	HTTPPort int `json:"http_port"`

	// MetricsPort is the port for Prometheus metrics.
	MetricsPort int `json:"metrics_port"`

	// PredictorConfig is the configuration for the predictor.
	PredictorConfig types.PredictorConfig `json:"predictor"`

	// OversellConfig is the configuration for overselling.
	OversellConfig types.OversellConfig `json:"oversell"`

	// MonitorConfig is the configuration for monitoring.
	MonitorConfig types.MonitorConfig `json:"monitor"`

	// WebhookURLs are the URLs for alert webhooks.
	WebhookURLs []string `json:"webhook_urls"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		LogLevel:        "info",
		HTTPPort:        8080,
		MetricsPort:     9090,
		PredictorConfig: types.DefaultPredictorConfig(),
		OversellConfig:  types.DefaultOversellConfig(),
		MonitorConfig:   types.DefaultMonitorConfig(),
		WebhookURLs:     []string{},
	}
}

// Application is the main application.
type Application struct {
	config          Config
	predictor       *predictor.MemoryPredictor
	oversellManager *oversell.OversellManager
	monitor         *monitor.Monitor
	httpServer      *http.Server
	metricsServer   *http.Server
}

// NewApplication creates a new Application.
func NewApplication(config Config) (*Application, error) {
	// Get node memory info
	nodeMemory := getNodeMemoryInfo()

	// Create predictor
	pred := predictor.NewMemoryPredictor(config.PredictorConfig)

	// Create oversell manager
	mgr := oversell.NewOversellManager(config.OversellConfig, pred, nodeMemory)

	// Create monitor
	mon := monitor.NewMonitor(config.MonitorConfig, mgr)

	return &Application{
		config:          config,
		predictor:       pred,
		oversellManager: mgr,
		monitor:         mon,
	}, nil
}

// Start starts the application.
func (app *Application) Start(ctx context.Context) error {
	// Configure logging
	level, err := logrus.ParseLevel(app.config.LogLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	log.SetLevel(level)
	log.SetFormatter(&logrus.JSONFormatter{})

	log.Info("Starting Logical Memory Oversell System")

	// Start oversell manager
	if err := app.oversellManager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start oversell manager: %w", err)
	}

	// Start monitor
	if err := app.monitor.Start(ctx); err != nil {
		return fmt.Errorf("failed to start monitor: %w", err)
	}

	// Start HTTP server
	if err := app.startHTTPServer(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Start metrics server
	if err := app.startMetricsServer(); err != nil {
		return fmt.Errorf("failed to start metrics server: %w", err)
	}

	// Register Prometheus metrics
	app.registerMetrics()

	log.WithFields(logrus.Fields{
		"http_port":    app.config.HTTPPort,
		"metrics_port": app.config.MetricsPort,
	}).Info("Application started successfully")

	return nil
}

// Stop stops the application.
func (app *Application) Stop() error {
	log.Info("Stopping application...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop HTTP server
	if app.httpServer != nil {
		if err := app.httpServer.Shutdown(ctx); err != nil {
			log.WithError(err).Error("Failed to shutdown HTTP server")
		}
	}

	// Stop metrics server
	if app.metricsServer != nil {
		if err := app.metricsServer.Shutdown(ctx); err != nil {
			log.WithError(err).Error("Failed to shutdown metrics server")
		}
	}

	// Stop monitor
	if err := app.monitor.Stop(); err != nil {
		log.WithError(err).Error("Failed to stop monitor")
	}

	// Stop oversell manager
	if err := app.oversellManager.Stop(); err != nil {
		log.WithError(err).Error("Failed to stop oversell manager")
	}

	log.Info("Application stopped")
	return nil
}

// startHTTPServer starts the HTTP API server.
func (app *Application) startHTTPServer() error {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", app.healthHandler)
	mux.HandleFunc("/ready", app.readyHandler)

	// Status endpoints
	mux.HandleFunc("/api/v1/status", app.statusHandler)
	mux.HandleFunc("/api/v1/oversell", app.oversellHandler)
	mux.HandleFunc("/api/v1/predictions", app.predictionsHandler)
	mux.HandleFunc("/api/v1/alerts", app.alertsHandler)
	mux.HandleFunc("/api/v1/pods", app.podsHandler)

	// Data input endpoint
	mux.HandleFunc("/api/v1/datapoint", app.dataPointHandler)

	app.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", app.config.HTTPPort),
		Handler: mux,
	}

	go func() {
		if err := app.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.WithError(err).Error("HTTP server error")
		}
	}()

	return nil
}

// startMetricsServer starts the Prometheus metrics server.
func (app *Application) startMetricsServer() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	app.metricsServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", app.config.MetricsPort),
		Handler: mux,
	}

	go func() {
		if err := app.metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			log.WithError(err).Error("Metrics server error")
		}
	}()

	return nil
}

// registerMetrics registers Prometheus metrics.
func (app *Application) registerMetrics() {
	// Memory usage ratio gauge
	memoryUsageRatio := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "logical_memory_usage_ratio",
			Help: "Current memory usage ratio",
		},
		func() float64 {
			return app.monitor.GetCurrentUsageRatio()
		},
	)

	// Oversell ratio gauge
	oversellRatio := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "logical_memory_oversell_ratio",
			Help: "Current oversell ratio",
		},
		func() float64 {
			status := app.oversellManager.GetStatus()
			return status.CurrentRatio
		},
	)

	// Logical memory gauge
	logicalMemory := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "logical_memory_bytes",
			Help: "Total logical memory in bytes",
		},
		func() float64 {
			status := app.oversellManager.GetStatus()
			return float64(status.LogicalMemoryBytes)
		},
	)

	// Prediction accuracy gauge
	predictionAccuracy := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "logical_memory_prediction_accuracy",
			Help: "Prediction accuracy (0-1)",
		},
		func() float64 {
			return app.predictor.GetAccuracy()
		},
	)

	prometheus.MustRegister(memoryUsageRatio, oversellRatio, logicalMemory, predictionAccuracy)
}

// HTTP Handlers

func (app *Application) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (app *Application) readyHandler(w http.ResponseWriter, r *http.Request) {
	if app.monitor.IsHealthy() {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ready"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Not Ready"))
	}
}

func (app *Application) statusHandler(w http.ResponseWriter, r *http.Request) {
	status := struct {
		OversellStatus types.OversellStatus `json:"oversell_status"`
		MemoryUsage    float64              `json:"memory_usage_ratio"`
		IsHealthy      bool                 `json:"is_healthy"`
		DataPoints     int                  `json:"data_points"`
		Accuracy       float64              `json:"prediction_accuracy"`
	}{
		OversellStatus: app.oversellManager.GetStatus(),
		MemoryUsage:    app.monitor.GetCurrentUsageRatio(),
		IsHealthy:      app.monitor.IsHealthy(),
		DataPoints:     app.predictor.GetDataPointCount(),
		Accuracy:       app.predictor.GetAccuracy(),
	}

	respondJSON(w, http.StatusOK, status)
}

func (app *Application) oversellHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status := app.oversellManager.GetStatus()
		respondJSON(w, http.StatusOK, status)

	case http.MethodPut:
		var req struct {
			Ratio float64 `json:"ratio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if err := app.oversellManager.SetRatio(req.Ratio); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}

		respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (app *Application) predictionsHandler(w http.ResponseWriter, r *http.Request) {
	predictions, err := app.predictor.Predict(72) // 3 days
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	recommendedRatio, _ := app.predictor.GetRecommendedOversellRatio(72, app.config.OversellConfig.SafetyFactor)

	response := struct {
		Predictions      []types.PredictionResult `json:"predictions"`
		RecommendedRatio float64                  `json:"recommended_ratio"`
	}{
		Predictions:      predictions,
		RecommendedRatio: recommendedRatio,
	}

	respondJSON(w, http.StatusOK, response)
}

func (app *Application) alertsHandler(w http.ResponseWriter, r *http.Request) {
	alerts := app.monitor.GetAlertHistory()
	respondJSON(w, http.StatusOK, alerts)
}

func (app *Application) podsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pods := app.oversellManager.GetPods()
		respondJSON(w, http.StatusOK, pods)

	case http.MethodPost:
		var pod types.PodMemoryInfo
		if err := json.NewDecoder(r.Body).Decode(&pod); err != nil {
			respondError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if err := app.oversellManager.AddPod(pod); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}

		respondJSON(w, http.StatusCreated, map[string]string{"status": "added"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (app *Application) dataPointHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var dataPoint types.MemoryDataPoint
	if err := json.NewDecoder(r.Body).Decode(&dataPoint); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if dataPoint.Timestamp.IsZero() {
		dataPoint.Timestamp = time.Now()
	}

	if err := app.predictor.AddDataPoint(dataPoint); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, map[string]string{"status": "added"})
}

// Helper functions

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func getNodeMemoryInfo() types.NodeMemoryInfo {
	// Default values for development/testing
	return types.NodeMemoryInfo{
		NodeName:       "localhost",
		TotalBytes:     128 * 1024 * 1024 * 1024, // 128GB
		AvailableBytes: 64 * 1024 * 1024 * 1024,  // 64GB
		UsedBytes:      64 * 1024 * 1024 * 1024,  // 64GB
		CachedBytes:    16 * 1024 * 1024 * 1024,  // 16GB
		BuffersBytes:   4 * 1024 * 1024 * 1024,   // 4GB
		HugePagesTotal: 0,
		HugePagesFree:  0,
		HugePageSize:   2 * 1024 * 1024, // 2MB
	}
}

func loadConfig(configPath string) (Config, error) {
	config := DefaultConfig()

	if configPath == "" {
		return config, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config, err
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}

	return config, nil
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file")
	flag.Parse()

	// Load configuration
	config, err := loadConfig(*configPath)
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Create application
	app, err := NewApplication(config)
	if err != nil {
		log.WithError(err).Fatal("Failed to create application")
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start application
	if err := app.Start(ctx); err != nil {
		log.WithError(err).Fatal("Failed to start application")
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	log.Info("Shutdown signal received")

	// Stop application
	if err := app.Stop(); err != nil {
		log.WithError(err).Error("Error during shutdown")
	}
}

