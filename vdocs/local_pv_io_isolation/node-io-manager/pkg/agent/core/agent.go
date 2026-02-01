// Package core - AI Agent 核心框架
package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/analyzer"
	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// AgentRole Agent 角色
type AgentRole string

const (
	RoleNodeManager AgentRole = "node_manager" // 协调者
	RoleIOExpert    AgentRole = "io_expert"    // IO 专家
	RoleWorker      AgentRole = "worker"       // 执行者
)

// Agent 代理接口
type Agent interface {
	GetRole() AgentRole
	GetID() string
	Process(ctx context.Context, input *AgentInput) (*AgentOutput, error)
}

// AgentInput Agent 输入
type AgentInput struct {
	SessionID   string
	Query       string
	Context     string
	Data        map[string]interface{}
	ParentAgent string
}

// AgentOutput Agent 输出
type AgentOutput struct {
	SessionID      string
	Response       string
	Actions        []RecommendedAction
	SubAgentTasks  []SubAgentTask
	Finished       bool
	FinishReason   string
	ToolCalls      []ToolCall
	Metadata       map[string]interface{}
}

// RecommendedAction 推荐的操作
type RecommendedAction struct {
	Type        string
	Target      string
	Namespace   string
	Parameters  map[string]interface{}
	Priority    int
	Confidence  float64
	Reason      string
}

// SubAgentTask 子代理任务
type SubAgentTask struct {
	AgentRole   AgentRole
	TaskType    string
	Description string
	Input       map[string]interface{}
}

// ToolCall 工具调用
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
	Result    string
}

// Session Agent 会话
type Session struct {
	ID          string
	StartTime   time.Time
	EndTime     *time.Time
	Status      string // active, completed, error
	Messages    []Message
	AgentID     string
	ParentID    string
	Metadata    map[string]interface{}
}

// Message 消息
type Message struct {
	ID        string
	Role      string // user, assistant, tool
	Content   string
	Timestamp time.Time
	ToolCalls []ToolCall
}

// AgentManager Agent 管理器
type AgentManager struct {
	config config.AgentConfig

	// Provider
	provider Provider

	// Agents
	nodeManager *NodeManagerAgent
	ioExpert    *IOExpertAgent
	workers     map[string]*WorkerAgent

	// Sessions
	sessions   map[string]*Session
	sessionsMu sync.RWMutex

	// Tools
	toolRegistry *ToolRegistry

	// 控制
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewAgentManager 创建 Agent 管理器
func NewAgentManager(cfg config.AgentConfig) (*AgentManager, error) {
	// 创建 Provider
	provider, err := NewProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}

	m := &AgentManager{
		config:       cfg,
		provider:     provider,
		sessions:     make(map[string]*Session),
		workers:      make(map[string]*WorkerAgent),
		toolRegistry: NewToolRegistry(),
		stopCh:       make(chan struct{}),
	}

	// 注册内置工具
	m.registerBuiltinTools()

	// 创建 Agents
	m.nodeManager = NewNodeManagerAgent(m)
	m.ioExpert = NewIOExpertAgent(m)

	return m, nil
}

// registerBuiltinTools 注册内置工具
func (m *AgentManager) registerBuiltinTools() {
	// 注册 IO 分析工具
	m.toolRegistry.Register(&Tool{
		Name:        "analyze_io",
		Description: "分析节点 IO 状态，返回磁盘利用率、Pod IO 占比、受害者等信息",
		Parameters: map[string]ToolParameter{
			"detail_level": {Type: "string", Description: "详细级别: summary, detailed", Required: false},
		},
	})

	m.toolRegistry.Register(&Tool{
		Name:        "get_pod_profile",
		Description: "获取指定 Pod 的 IO 画像",
		Parameters: map[string]ToolParameter{
			"namespace": {Type: "string", Description: "Pod 命名空间", Required: true},
			"name":      {Type: "string", Description: "Pod 名称", Required: true},
		},
	})

	m.toolRegistry.Register(&Tool{
		Name:        "get_victims",
		Description: "获取当前受害者 Pod 列表",
		Parameters:  map[string]ToolParameter{},
	})

	m.toolRegistry.Register(&Tool{
		Name:        "get_scores",
		Description: "获取所有 Pod 的操作评分",
		Parameters:  map[string]ToolParameter{},
	})

	m.toolRegistry.Register(&Tool{
		Name:        "limit_pod_io",
		Description: "限制 Pod 的 IO",
		Parameters: map[string]ToolParameter{
			"namespace":  {Type: "string", Description: "Pod 命名空间", Required: true},
			"name":       {Type: "string", Description: "Pod 名称", Required: true},
			"read_iops":  {Type: "number", Description: "读 IOPS 限制", Required: false},
			"write_iops": {Type: "number", Description: "写 IOPS 限制", Required: false},
		},
	})

	m.toolRegistry.Register(&Tool{
		Name:        "remove_io_limit",
		Description: "移除 Pod 的 IO 限制",
		Parameters: map[string]ToolParameter{
			"namespace": {Type: "string", Description: "Pod 命名空间", Required: true},
			"name":      {Type: "string", Description: "Pod 名称", Required: true},
		},
	})

	m.toolRegistry.Register(&Tool{
		Name:        "delegate_to_expert",
		Description: "将任务委托给 IO 专家进行深度分析",
		Parameters: map[string]ToolParameter{
			"task":    {Type: "string", Description: "任务描述", Required: true},
			"context": {Type: "string", Description: "上下文信息", Required: false},
		},
	})
}

// Run 运行 Agent 管理器
func (m *AgentManager) Run(ctx context.Context) {
	m.wg.Add(1)
	defer m.wg.Done()

	log.Info("Agent manager started")

	<-ctx.Done()

	log.Info("Agent manager stopped")
}

// Shutdown 关闭
func (m *AgentManager) Shutdown() {
	close(m.stopCh)
	m.wg.Wait()
}

// Analyze 执行分析
func (m *AgentManager) Analyze(ctx context.Context, query, contextStr string) (*AgentOutput, error) {
	// 创建会话
	session := m.createSession(string(RoleNodeManager))

	input := &AgentInput{
		SessionID: session.ID,
		Query:     query,
		Context:   contextStr,
	}

	// 调用 Node Manager 处理
	output, err := m.nodeManager.Process(ctx, input)
	if err != nil {
		session.Status = "error"
		return nil, err
	}

	// 更新会话
	m.updateSession(session.ID, output)

	return output, nil
}

// TriggerAnalysis 触发自动分析
func (m *AgentManager) TriggerAnalysis(ctx context.Context, victims []*analyzer.VictimResult) {
	if len(victims) == 0 {
		return
	}

	// 构建分析请求
	query := fmt.Sprintf("检测到 %d 个受害者 Pod，请分析 IO 问题并给出处理建议。", len(victims))

	contextStr := "受害者列表:\n"
	for _, v := range victims {
		contextStr += fmt.Sprintf("- %s/%s: 评分 %.1f, 严重程度 %s\n",
			v.Namespace, v.PodName, v.Score, v.Severity.String())
	}

	go func() {
		output, err := m.Analyze(ctx, query, contextStr)
		if err != nil {
			log.Errorf("Auto analysis failed: %v", err)
			return
		}

		log.WithFields(log.Fields{
			"actions_count": len(output.Actions),
			"finished":      output.Finished,
		}).Info("Auto analysis completed")

		// 处理推荐的操作
		for _, action := range output.Actions {
			log.WithFields(log.Fields{
				"action":     action.Type,
				"target":     action.Target,
				"confidence": action.Confidence,
			}).Info("Recommended action")
		}
	}()
}

// createSession 创建会话
func (m *AgentManager) createSession(agentID string) *Session {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()

	id := fmt.Sprintf("sess-%d", time.Now().UnixNano())
	session := &Session{
		ID:        id,
		StartTime: time.Now(),
		Status:    "active",
		AgentID:   agentID,
		Messages:  make([]Message, 0),
		Metadata:  make(map[string]interface{}),
	}

	m.sessions[id] = session
	return session
}

// updateSession 更新会话
func (m *AgentManager) updateSession(sessionID string, output *AgentOutput) {
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return
	}

	// 添加助手消息
	session.Messages = append(session.Messages, Message{
		ID:        fmt.Sprintf("msg-%d", len(session.Messages)),
		Role:      "assistant",
		Content:   output.Response,
		Timestamp: time.Now(),
		ToolCalls: output.ToolCalls,
	})

	if output.Finished {
		session.Status = "completed"
		now := time.Now()
		session.EndTime = &now
	}
}

// GetSessions 获取会话列表
func (m *AgentManager) GetSessions() []*Session {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()

	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// GetSession 获取会话
func (m *AgentManager) GetSession(id string) *Session {
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	return m.sessions[id]
}

// GetProvider 获取 Provider
func (m *AgentManager) GetProvider() Provider {
	return m.provider
}

// GetToolRegistry 获取工具注册表
func (m *AgentManager) GetToolRegistry() *ToolRegistry {
	return m.toolRegistry
}

// GetConfig 获取配置
func (m *AgentManager) GetConfig() config.AgentConfig {
	return m.config
}
