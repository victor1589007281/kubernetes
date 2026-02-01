// Package api - REST API 服务
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/node-io-manager/pkg/agent/core"
	"github.com/node-io-manager/pkg/analyzer"
	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/observation"
	"github.com/node-io-manager/pkg/profile"
	"github.com/node-io-manager/pkg/queue"
	"github.com/node-io-manager/pkg/scoring"
	"github.com/node-io-manager/pkg/toolbox"
	log "github.com/sirupsen/logrus"
)

// Dependencies API 依赖
type Dependencies struct {
	Collector          *collector.Collector
	ProfileEngine      *profile.Engine
	VictimAnalyzer     *analyzer.VictimAnalyzer
	ScoringEngine      *scoring.Engine
	DecisionQueue      *queue.Manager
	ObservationManager *observation.Manager
	AgentManager       *core.AgentManager
	Toolbox            *toolbox.Toolbox
}

// Server REST API 服务器
type Server struct {
	config config.APIConfig
	deps   Dependencies
	server *http.Server
	router *gin.Engine
}

// NewServer 创建 API 服务器
func NewServer(cfg config.APIConfig, deps Dependencies) *Server {
	gin.SetMode(gin.ReleaseMode)

	s := &Server{
		config: cfg,
		deps:   deps,
		router: gin.New(),
	}

	s.setupRoutes()

	return s
}

// setupRoutes 设置路由
func (s *Server) setupRoutes() {
	// 中间件
	s.router.Use(gin.Recovery())
	s.router.Use(s.loggingMiddleware())

	if s.config.EnableCORS {
		s.router.Use(s.corsMiddleware())
	}

	if s.config.AuthToken != "" {
		s.router.Use(s.authMiddleware())
	}

	// API v1
	v1 := s.router.Group("/api/v1")
	{
		// 健康检查
		v1.GET("/health", s.healthHandler)
		v1.GET("/ready", s.readyHandler)

		// 数据采集
		v1.GET("/collect/disks", s.getDisksHandler)
		v1.GET("/collect/pods", s.getPodsHandler)
		v1.GET("/collect/processes", s.getProcessesHandler)
		v1.GET("/collect/system", s.getSystemHandler)

		// IO 画像
		v1.GET("/profile/pods", s.getProfilesHandler)
		v1.GET("/profile/pod/:namespace/:name", s.getPodProfileHandler)

		// 受害者分析
		v1.GET("/analyze/victims", s.getVictimsHandler)

		// 评分
		v1.GET("/scoring/pods", s.getScoresHandler)
		v1.GET("/scoring/pod/:namespace/:name", s.getPodScoreHandler)
		v1.GET("/scoring/factors", s.getFactorsHandler)
		v1.GET("/scoring/weights", s.getWeightsHandler)
		v1.PUT("/scoring/weights", s.updateWeightsHandler)

		// 决策队列
		v1.GET("/queue", s.getQueueHandler)
		v1.POST("/queue/:id/cancel", s.cancelQueueItemHandler)
		v1.POST("/queue/:id/execute", s.executeQueueItemHandler)

		// 观察期
		v1.GET("/observation", s.getObservationsHandler)
		v1.GET("/observation/:id", s.getObservationHandler)

		// 历史数据
		v1.GET("/history/pod/:namespace/:name", s.getPodHistoryHandler)

		// 工具箱
		v1.POST("/toolbox/limit-io", s.limitIOHandler)
		v1.POST("/toolbox/remove-limit", s.removeLimitHandler)
		v1.POST("/toolbox/cordon", s.cordonHandler)
		v1.POST("/toolbox/uncordon", s.uncordonHandler)

		// AI Agent
		v1.POST("/agent/analyze", s.agentAnalyzeHandler)
		v1.GET("/agent/sessions", s.getAgentSessionsHandler)
		v1.GET("/agent/session/:id", s.getAgentSessionHandler)
	}
}

// 中间件

func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		log.WithFields(log.Fields{
			"status":  status,
			"method":  c.Request.Method,
			"path":    path,
			"latency": latency,
			"ip":      c.ClientIP(),
		}).Debug("API request")
	}
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token != "Bearer "+s.config.AuthToken {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// 处理器

func (s *Server) healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

func (s *Server) readyHandler(c *gin.Context) {
	// 检查各组件是否就绪
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

func (s *Server) getDisksHandler(c *gin.Context) {
	data := s.deps.Collector.GetLatestData()
	if data == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no data available"})
		return
	}
	c.JSON(http.StatusOK, data.Disks)
}

func (s *Server) getPodsHandler(c *gin.Context) {
	pods := s.deps.Collector.GetPodMetrics()
	c.JSON(http.StatusOK, pods)
}

func (s *Server) getProcessesHandler(c *gin.Context) {
	data := s.deps.Collector.GetLatestData()
	if data == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no data available"})
		return
	}

	// 只返回 D 状态进程或指定过滤条件的进程
	dState := c.Query("d_state")
	if dState == "true" {
		dStateProcs := make(map[int]*collector.ProcessIOStats)
		for pid, proc := range data.Processes {
			if proc.IsDState {
				dStateProcs[pid] = proc
			}
		}
		c.JSON(http.StatusOK, dStateProcs)
		return
	}

	c.JSON(http.StatusOK, data.Processes)
}

func (s *Server) getSystemHandler(c *gin.Context) {
	data := s.deps.Collector.GetLatestData()
	if data == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no data available"})
		return
	}
	c.JSON(http.StatusOK, data.System)
}

func (s *Server) getProfilesHandler(c *gin.Context) {
	profiles := s.deps.ProfileEngine.GetProfiles()
	c.JSON(http.StatusOK, profiles)
}

func (s *Server) getPodProfileHandler(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")

	profile := s.deps.ProfileEngine.GetPodProfile(namespace, name)
	if profile == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}
	c.JSON(http.StatusOK, profile)
}

func (s *Server) getVictimsHandler(c *gin.Context) {
	victims := s.deps.VictimAnalyzer.GetVictims()
	c.JSON(http.StatusOK, victims)
}

func (s *Server) getScoresHandler(c *gin.Context) {
	scores := s.deps.ScoringEngine.GetScores()
	c.JSON(http.StatusOK, scores)
}

func (s *Server) getPodScoreHandler(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")

	score := s.deps.ScoringEngine.GetPodScore(namespace, name)
	if score == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "score not found"})
		return
	}
	c.JSON(http.StatusOK, score)
}

func (s *Server) getFactorsHandler(c *gin.Context) {
	factors := s.deps.ScoringEngine.GetFactors()
	c.JSON(http.StatusOK, factors)
}

func (s *Server) getWeightsHandler(c *gin.Context) {
	weights := s.deps.ScoringEngine.GetWeights()
	c.JSON(http.StatusOK, weights)
}

func (s *Server) updateWeightsHandler(c *gin.Context) {
	var weights map[string]float64
	if err := c.BindJSON(&weights); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.deps.ScoringEngine.UpdateWeights(weights); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (s *Server) getQueueHandler(c *gin.Context) {
	items := s.deps.DecisionQueue.GetItems()
	c.JSON(http.StatusOK, items)
}

func (s *Server) cancelQueueItemHandler(c *gin.Context) {
	id := c.Param("id")

	if err := s.deps.DecisionQueue.Cancel(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

func (s *Server) executeQueueItemHandler(c *gin.Context) {
	id := c.Param("id")

	if err := s.deps.DecisionQueue.ExecuteNow(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "executing"})
}

func (s *Server) getObservationsHandler(c *gin.Context) {
	observations := s.deps.ObservationManager.GetAll()
	c.JSON(http.StatusOK, observations)
}

func (s *Server) getObservationHandler(c *gin.Context) {
	id := c.Param("id")

	obs := s.deps.ObservationManager.Get(id)
	if obs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "observation not found"})
		return
	}
	c.JSON(http.StatusOK, obs)
}

func (s *Server) getPodHistoryHandler(c *gin.Context) {
	namespace := c.Param("namespace")
	name := c.Param("name")

	history := s.deps.ScoringEngine.GetPodHistory(namespace, name)
	if history == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "history not found"})
		return
	}
	c.JSON(http.StatusOK, history)
}

// 工具箱处理器

type LimitIORequest struct {
	Namespace string `json:"namespace"`
	PodName   string `json:"podName"`
	ReadIOPS  int64  `json:"readIOPS"`
	WriteIOPS int64  `json:"writeIOPS"`
	ReadBPS   int64  `json:"readBPS"`
	WriteBPS  int64  `json:"writeBPS"`
}

func (s *Server) limitIOHandler(c *gin.Context) {
	var req LimitIORequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.deps.Toolbox.LimitIO(req.Namespace, req.PodName, req.ReadIOPS, req.WriteIOPS, req.ReadBPS, req.WriteBPS); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "limited"})
}

func (s *Server) removeLimitHandler(c *gin.Context) {
	var req struct {
		Namespace string `json:"namespace"`
		PodName   string `json:"podName"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.deps.Toolbox.RemoveLimit(req.Namespace, req.PodName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

func (s *Server) cordonHandler(c *gin.Context) {
	if err := s.deps.Toolbox.CordonNode(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cordoned"})
}

func (s *Server) uncordonHandler(c *gin.Context) {
	if err := s.deps.Toolbox.UncordonNode(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "uncordoned"})
}

// AI Agent 处理器

type AgentAnalyzeRequest struct {
	Query   string `json:"query"`
	Context string `json:"context"`
}

func (s *Server) agentAnalyzeHandler(c *gin.Context) {
	if s.deps.AgentManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent not enabled"})
		return
	}

	var req AgentAnalyzeRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := s.deps.AgentManager.Analyze(c.Request.Context(), req.Query, req.Context)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) getAgentSessionsHandler(c *gin.Context) {
	if s.deps.AgentManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent not enabled"})
		return
	}

	sessions := s.deps.AgentManager.GetSessions()
	c.JSON(http.StatusOK, sessions)
}

func (s *Server) getAgentSessionHandler(c *gin.Context) {
	if s.deps.AgentManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent not enabled"})
		return
	}

	id := c.Param("id")
	session := s.deps.AgentManager.GetSession(id)
	if session == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, session)
}

// Run 运行 API 服务器
func (s *Server) Run(ctx context.Context) {
	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Port),
		Handler:      s.router,
		ReadTimeout:  time.Duration(s.config.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.config.WriteTimeout) * time.Second,
	}

	log.Infof("API server starting on :%d", s.config.Port)

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("API server error: %v", err)
		}
	}()

	<-ctx.Done()
}

// Shutdown 关闭服务器
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
