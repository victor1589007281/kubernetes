// Package core - Agent 工厂函数
package core

import (
	"context"
)

// NodeManagerAgent Node Manager Agent (定义在 core 包以避免循环依赖)
type NodeManagerAgent struct {
	manager *AgentManager
	id      string
}

// NewNodeManagerAgent 创建 Node Manager Agent
func NewNodeManagerAgent(manager *AgentManager) *NodeManagerAgent {
	return &NodeManagerAgent{
		manager: manager,
		id:      "node-manager-1",
	}
}

// GetRole 获取角色
func (a *NodeManagerAgent) GetRole() AgentRole {
	return RoleNodeManager
}

// GetID 获取 ID
func (a *NodeManagerAgent) GetID() string {
	return a.id
}

// Process 处理请求
func (a *NodeManagerAgent) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	loop := NewReActLoop(a.manager, a)
	return loop.Run(ctx, input)
}

// IOExpertAgent IO 专家 Agent
type IOExpertAgent struct {
	manager *AgentManager
	id      string
}

// NewIOExpertAgent 创建 IO Expert Agent
func NewIOExpertAgent(manager *AgentManager) *IOExpertAgent {
	return &IOExpertAgent{
		manager: manager,
		id:      "io-expert-1",
	}
}

// GetRole 获取角色
func (a *IOExpertAgent) GetRole() AgentRole {
	return RoleIOExpert
}

// GetID 获取 ID
func (a *IOExpertAgent) GetID() string {
	return a.id
}

// Process 处理请求
func (a *IOExpertAgent) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	loop := NewReActLoop(a.manager, a)
	return loop.Run(ctx, input)
}

// WorkerAgent Worker Agent
type WorkerAgent struct {
	manager    *AgentManager
	id         string
	workerType string
}

// NewWorkerAgent 创建 Worker Agent
func NewWorkerAgent(manager *AgentManager, workerType string) *WorkerAgent {
	return &WorkerAgent{
		manager:    manager,
		id:         "worker-" + workerType,
		workerType: workerType,
	}
}

// GetRole 获取角色
func (w *WorkerAgent) GetRole() AgentRole {
	return RoleWorker
}

// GetID 获取 ID
func (w *WorkerAgent) GetID() string {
	return w.id
}

// Process 处理请求
func (w *WorkerAgent) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	// Worker 直接执行，不需要 ReAct 循环
	output := &AgentOutput{
		Response: "Worker 执行完成",
		Finished: true,
		Metadata: make(map[string]interface{}),
	}
	return output, nil
}
