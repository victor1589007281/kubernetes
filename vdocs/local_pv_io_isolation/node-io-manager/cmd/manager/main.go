// Package main - Node IO Manager 主程序入口
// 作为 DaemonSet 运行在每个 Kubernetes 节点上
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/node-io-manager/pkg/agent/core"
	"github.com/node-io-manager/pkg/analyzer"
	"github.com/node-io-manager/pkg/api"
	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/metrics"
	"github.com/node-io-manager/pkg/observation"
	"github.com/node-io-manager/pkg/profile"
	"github.com/node-io-manager/pkg/queue"
	"github.com/node-io-manager/pkg/scoring"
	"github.com/node-io-manager/pkg/toolbox"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	configPath string
	logLevel   string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "node-io-manager",
		Short: "Node-level IO monitoring and management service",
		Long: `Node IO Manager 是一个节点级别的 IO 监控和管理服务，
提供多维度 IO 统计、智能分析、预测告警和自动化工具箱能力。`,
		RunE: run,
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "/etc/node-io-manager/config.yaml", "配置文件路径")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "日志级别 (debug, info, warn, error)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// 初始化日志
	level, err := log.ParseLevel(logLevel)
	if err != nil {
		level = log.InfoLevel
	}
	log.SetLevel(level)
	log.SetFormatter(&log.JSONFormatter{TimestampFormat: time.RFC3339})

	log.Info("Starting Node IO Manager...")

	// 加载配置
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	log.Infof("Configuration loaded from %s", configPath)

	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 初始化组件
	manager, err := initializeManager(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize manager: %w", err)
	}

	// 启动服务
	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %w", err)
	}

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("Shutting down Node IO Manager...")
	cancel()

	// 优雅关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := manager.Shutdown(shutdownCtx); err != nil {
		log.Errorf("Error during shutdown: %v", err)
	}

	log.Info("Node IO Manager stopped")
	return nil
}

// Manager 主管理器
type Manager struct {
	config             *config.Config
	collector          *collector.Collector
	metricsServer      *metrics.Server
	apiServer          *api.Server
	profileEngine      *profile.Engine
	victimAnalyzer     *analyzer.VictimAnalyzer
	scoringEngine      *scoring.Engine
	decisionQueue      *queue.Manager
	observationManager *observation.Manager
	agentManager       *core.AgentManager
	toolbox            *toolbox.Toolbox
}

func initializeManager(ctx context.Context, cfg *config.Config) (*Manager, error) {
	m := &Manager{config: cfg}

	// Phase 1: 基础组件
	// 初始化数据采集器
	m.collector = collector.New(cfg.Collector)
	log.Info("Collector initialized")

	// 初始化 Prometheus metrics
	m.metricsServer = metrics.NewServer(cfg.Metrics)
	log.Info("Metrics server initialized")

	// Phase 2: 分析引擎
	// 初始化 IO 画像引擎
	m.profileEngine = profile.NewEngine(cfg.Profile)
	log.Info("Profile engine initialized")

	// 初始化受害者分析器
	m.victimAnalyzer = analyzer.NewVictimAnalyzer(cfg.Analyzer)
	log.Info("Victim analyzer initialized")

	// Phase 3: 评分决策系统
	// 初始化评分引擎
	m.scoringEngine = scoring.NewEngine(cfg.Scoring)
	log.Info("Scoring engine initialized")

	// Phase 4: 决策队列与观察期
	// 初始化决策队列
	m.decisionQueue = queue.NewManager(cfg.Queue)
	log.Info("Decision queue initialized")

	// 初始化观察期管理器
	m.observationManager = observation.NewManager(cfg.Observation)
	log.Info("Observation manager initialized")

	// Phase 5: AI Agent
	if cfg.Agent.Enabled {
		var err error
		m.agentManager, err = core.NewAgentManager(cfg.Agent)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize agent manager: %w", err)
		}
		log.Info("AI Agent manager initialized")
	}

	// Phase 6: 工具箱
	m.toolbox = toolbox.NewToolbox(cfg.Toolbox)
	log.Info("Toolbox initialized")

	// 初始化 REST API 服务
	m.apiServer = api.NewServer(cfg.API, api.Dependencies{
		Collector:          m.collector,
		ProfileEngine:      m.profileEngine,
		VictimAnalyzer:     m.victimAnalyzer,
		ScoringEngine:      m.scoringEngine,
		DecisionQueue:      m.decisionQueue,
		ObservationManager: m.observationManager,
		AgentManager:       m.agentManager,
		Toolbox:            m.toolbox,
	})
	log.Info("API server initialized")

	return m, nil
}

func (m *Manager) Start(ctx context.Context) error {
	// 启动数据采集
	go m.collector.Run(ctx)

	// 启动 Prometheus metrics 服务
	go m.metricsServer.Run(ctx)

	// 启动分析引擎
	go m.runAnalysisLoop(ctx)

	// 启动评分引擎
	go m.runScoringLoop(ctx)

	// 启动决策队列处理
	go m.decisionQueue.Run(ctx)

	// 启动观察期管理
	go m.observationManager.Run(ctx)

	// 启动 AI Agent（如果启用）
	if m.agentManager != nil {
		go m.agentManager.Run(ctx)
	}

	// 启动 API 服务
	go m.apiServer.Run(ctx)

	log.Info("All components started")
	return nil
}

func (m *Manager) runAnalysisLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.config.Profile.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 获取采集数据
			data := m.collector.GetLatestData()

			// 更新 IO 画像
			profiles := m.profileEngine.Update(data)

			// 受害者分析
			victims := m.victimAnalyzer.Analyze(profiles)

			// 更新 metrics
			m.metricsServer.UpdateProfiles(profiles)
			m.metricsServer.UpdateVictims(victims)

			// 检查是否需要触发 AI Agent
			if m.agentManager != nil && m.shouldTriggerAgent(victims) {
				m.agentManager.TriggerAnalysis(ctx, victims)
			}
		}
	}
}

func (m *Manager) runScoringLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.config.Scoring.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 获取当前 Pod IO 数据
			podMetrics := m.collector.GetPodMetrics()
			profiles := m.profileEngine.GetProfiles()

			// 计算评分
			scores := m.scoringEngine.CalculateScores(podMetrics, profiles)

			// 更新决策队列
			m.decisionQueue.UpdateScores(scores)

			// 更新 metrics
			m.metricsServer.UpdateScores(scores)
		}
	}
}

func (m *Manager) shouldTriggerAgent(victims []*analyzer.VictimResult) bool {
	// 检查是否达到触发 AI 分析的条件
	for _, v := range victims {
		if v.Severity >= analyzer.SeverityHigh {
			return true
		}
	}
	return false
}

func (m *Manager) Shutdown(ctx context.Context) error {
	// 优雅关闭各组件
	if err := m.apiServer.Shutdown(ctx); err != nil {
		log.Errorf("API server shutdown error: %v", err)
	}

	if m.agentManager != nil {
		m.agentManager.Shutdown()
	}

	m.decisionQueue.Stop()
	m.observationManager.Stop()

	return nil
}
