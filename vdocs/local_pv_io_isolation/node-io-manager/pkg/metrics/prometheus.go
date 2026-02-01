// Package metrics - Prometheus 指标服务
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/node-io-manager/pkg/analyzer"
	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/profile"
	"github.com/node-io-manager/pkg/scoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

// Server Prometheus metrics 服务器
type Server struct {
	config   config.MetricsConfig
	registry *prometheus.Registry
	server   *http.Server

	// 磁盘指标
	diskIOPS          *prometheus.GaugeVec
	diskBPS           *prometheus.GaugeVec
	diskLatency       *prometheus.GaugeVec
	diskUtilization   *prometheus.GaugeVec
	diskQueueDepth    *prometheus.GaugeVec

	// Pod 指标
	podIOPS           *prometheus.GaugeVec
	podBPS            *prometheus.GaugeVec
	podIOPercent      *prometheus.GaugeVec
	podThrottled      *prometheus.CounterVec
	podIOWeight       *prometheus.GaugeVec

	// 进程指标
	processDState     *prometheus.GaugeVec
	dStateCount       prometheus.Gauge

	// 系统指标
	systemIOWait      prometheus.Gauge
	systemProcsBlocked prometheus.Gauge

	// 画像指标
	podIOProfile      *prometheus.GaugeVec
	podSequentialRatio *prometheus.GaugeVec

	// 受害者分析指标
	podVictimScore    *prometheus.GaugeVec
	victimCount       prometheus.Gauge

	// 评分指标
	podOperationScore *prometheus.GaugeVec
	podBusinessPriority *prometheus.GaugeVec
	podRecidivismProb *prometheus.GaugeVec

	// 队列指标
	queuePendingCount prometheus.Gauge
	observationActive prometheus.Gauge
	actionSuccessRate prometheus.Gauge

	// 预测指标
	predictionAlert   *prometheus.GaugeVec
}

// NewServer 创建 metrics 服务器
func NewServer(cfg config.MetricsConfig) *Server {
	s := &Server{
		config:   cfg,
		registry: prometheus.NewRegistry(),
	}

	s.registerMetrics()

	return s
}

// registerMetrics 注册所有指标
func (s *Server) registerMetrics() {
	// 磁盘指标
	s.diskIOPS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_disk_iops",
		Help: "Disk IOPS by device and direction",
	}, []string{"device", "direction"})

	s.diskBPS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_disk_bps",
		Help: "Disk bytes per second by device and direction",
	}, []string{"device", "direction"})

	s.diskLatency = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_disk_latency_ms",
		Help: "Disk IO latency in milliseconds",
	}, []string{"device", "direction"})

	s.diskUtilization = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_disk_utilization_percent",
		Help: "Disk utilization percentage",
	}, []string{"device"})

	s.diskQueueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_disk_queue_depth",
		Help: "Average disk queue depth",
	}, []string{"device"})

	// Pod 指标
	s.podIOPS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_iops",
		Help: "Pod IOPS by direction",
	}, []string{"pod", "namespace", "direction"})

	s.podBPS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_bps",
		Help: "Pod bytes per second by direction",
	}, []string{"pod", "namespace", "direction"})

	s.podIOPercent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_percent",
		Help: "Pod IO percentage of total node IO",
	}, []string{"pod", "namespace", "metric"})

	s.podThrottled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_io_pod_throttled_total",
		Help: "Total number of times pod was IO throttled",
	}, []string{"pod", "namespace"})

	s.podIOWeight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_weight",
		Help: "Pod IO weight (cgroup)",
	}, []string{"pod", "namespace"})

	// 进程指标
	s.processDState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_process_d_state",
		Help: "Process in D (uninterruptible sleep) state",
	}, []string{"pid", "command", "pod", "namespace"})

	s.dStateCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_io_d_state_count",
		Help: "Total number of processes in D state",
	})

	// 系统指标
	s.systemIOWait = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_io_system_iowait_percent",
		Help: "System IO wait percentage",
	})

	s.systemProcsBlocked = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_io_system_procs_blocked",
		Help: "Number of blocked processes",
	})

	// 画像指标
	s.podIOProfile = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_profile_score",
		Help: "Pod IO profile score",
	}, []string{"pod", "namespace", "dimension"})

	s.podSequentialRatio = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_sequential_ratio",
		Help: "Pod sequential IO ratio",
	}, []string{"pod", "namespace"})

	// 受害者分析指标
	s.podVictimScore = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_victim_score",
		Help: "Pod victim score (higher means more affected by others)",
	}, []string{"pod", "namespace"})

	s.victimCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_io_victim_count",
		Help: "Number of identified victim pods",
	})

	// 评分指标
	s.podOperationScore = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_operation_score",
		Help: "Pod operation score (higher means should take action)",
	}, []string{"pod", "namespace"})

	s.podBusinessPriority = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_business_priority",
		Help: "Pod business priority",
	}, []string{"pod", "namespace"})

	s.podRecidivismProb = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_pod_recidivism_probability",
		Help: "Pod recidivism probability",
	}, []string{"pod", "namespace"})

	// 队列指标
	s.queuePendingCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_io_queue_pending_count",
		Help: "Number of pending items in decision queue",
	})

	s.observationActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_io_observation_active_count",
		Help: "Number of active observation periods",
	})

	s.actionSuccessRate = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_io_action_success_rate",
		Help: "Action success rate",
	})

	// 预测指标
	s.predictionAlert = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_io_prediction_alert",
		Help: "Prediction alert (1=alert, 0=normal)",
	}, []string{"metric", "severity"})

	// 注册所有指标
	s.registry.MustRegister(
		s.diskIOPS, s.diskBPS, s.diskLatency, s.diskUtilization, s.diskQueueDepth,
		s.podIOPS, s.podBPS, s.podIOPercent, s.podThrottled, s.podIOWeight,
		s.processDState, s.dStateCount,
		s.systemIOWait, s.systemProcsBlocked,
		s.podIOProfile, s.podSequentialRatio,
		s.podVictimScore, s.victimCount,
		s.podOperationScore, s.podBusinessPriority, s.podRecidivismProb,
		s.queuePendingCount, s.observationActive, s.actionSuccessRate,
		s.predictionAlert,
	)
}

// Run 运行 metrics 服务器
func (s *Server) Run(ctx context.Context) {
	if !s.config.Enabled {
		log.Info("Metrics server disabled")
		return
	}

	mux := http.NewServeMux()
	mux.Handle(s.config.Path, promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: mux,
	}

	log.Infof("Metrics server starting on :%d%s", s.config.Port, s.config.Path)

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("Metrics server error: %v", err)
		}
	}()

	<-ctx.Done()
	s.Shutdown()
}

// Shutdown 关闭服务器
func (s *Server) Shutdown() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
	}
}

// UpdateDiskStats 更新磁盘指标
func (s *Server) UpdateDiskStats(disks map[string]*collector.DiskStats) {
	for device, stats := range disks {
		s.diskIOPS.WithLabelValues(device, "read").Set(stats.ReadIOPS)
		s.diskIOPS.WithLabelValues(device, "write").Set(stats.WriteIOPS)

		s.diskBPS.WithLabelValues(device, "read").Set(stats.ReadBytesPerSec)
		s.diskBPS.WithLabelValues(device, "write").Set(stats.WriteBytesPerSec)

		s.diskLatency.WithLabelValues(device, "read").Set(stats.AvgReadLatencyMs)
		s.diskLatency.WithLabelValues(device, "write").Set(stats.AvgWriteLatencyMs)

		s.diskUtilization.WithLabelValues(device).Set(stats.Utilization)
		s.diskQueueDepth.WithLabelValues(device).Set(stats.AvgQueueDepth)
	}
}

// UpdatePodStats 更新 Pod 指标
func (s *Server) UpdatePodStats(pods map[string]*collector.PodMetrics) {
	for _, pod := range pods {
		s.podIOPS.WithLabelValues(pod.PodName, pod.Namespace, "read").Set(pod.ReadIOPS)
		s.podIOPS.WithLabelValues(pod.PodName, pod.Namespace, "write").Set(pod.WriteIOPS)

		s.podBPS.WithLabelValues(pod.PodName, pod.Namespace, "read").Set(pod.ReadBPS)
		s.podBPS.WithLabelValues(pod.PodName, pod.Namespace, "write").Set(pod.WriteBPS)

		s.podIOPercent.WithLabelValues(pod.PodName, pod.Namespace, "iops").Set(pod.IOPSPercent)
		s.podIOPercent.WithLabelValues(pod.PodName, pod.Namespace, "bps").Set(pod.BPSPercent)
	}
}

// UpdateProfiles 更新画像指标
func (s *Server) UpdateProfiles(profiles map[string]*profile.IOProfile) {
	for podUID, p := range profiles {
		s.podIOProfile.WithLabelValues(p.PodName, p.Namespace, "iops_score").Set(p.IOPSScore)
		s.podIOProfile.WithLabelValues(p.PodName, p.Namespace, "bps_score").Set(p.BPSScore)
		s.podIOProfile.WithLabelValues(p.PodName, p.Namespace, "latency_score").Set(p.LatencyScore)

		s.podSequentialRatio.WithLabelValues(p.PodName, p.Namespace).Set(p.SequentialRatio)
		_ = podUID
	}
}

// UpdateVictims 更新受害者指标
func (s *Server) UpdateVictims(victims []*analyzer.VictimResult) {
	s.victimCount.Set(float64(len(victims)))

	for _, v := range victims {
		s.podVictimScore.WithLabelValues(v.PodName, v.Namespace).Set(v.Score)
	}
}

// UpdateScores 更新评分指标
func (s *Server) UpdateScores(scores []*scoring.PodOperationScore) {
	for _, score := range scores {
		s.podOperationScore.WithLabelValues(score.PodName, score.Namespace).Set(score.FinalScore)
		s.podBusinessPriority.WithLabelValues(score.PodName, score.Namespace).Set(score.BusinessScore)
		s.podRecidivismProb.WithLabelValues(score.PodName, score.Namespace).Set(score.HistoryScore)
	}
}

// UpdateQueueStats 更新队列指标
func (s *Server) UpdateQueueStats(pending, observing int, successRate float64) {
	s.queuePendingCount.Set(float64(pending))
	s.observationActive.Set(float64(observing))
	s.actionSuccessRate.Set(successRate)
}

// SetPredictionAlert 设置预测告警
func (s *Server) SetPredictionAlert(metric, severity string, alert bool) {
	value := 0.0
	if alert {
		value = 1.0
	}
	s.predictionAlert.WithLabelValues(metric, severity).Set(value)
}
