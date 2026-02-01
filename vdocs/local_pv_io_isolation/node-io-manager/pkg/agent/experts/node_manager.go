// Package experts - Node Manager Agent
package experts

import (
	"context"

	"github.com/node-io-manager/pkg/agent/core"
)

// NodeManagerAgent Node Manager Agent 实现
type NodeManagerAgent struct {
	manager *core.AgentManager
	id      string
}

// NewNodeManagerAgent 创建 Node Manager Agent
func NewNodeManagerAgent(manager *core.AgentManager) *NodeManagerAgent {
	return &NodeManagerAgent{
		manager: manager,
		id:      "node-manager-1",
	}
}

// GetRole 获取角色
func (a *NodeManagerAgent) GetRole() core.AgentRole {
	return core.RoleNodeManager
}

// GetID 获取 ID
func (a *NodeManagerAgent) GetID() string {
	return a.id
}

// Process 处理请求
func (a *NodeManagerAgent) Process(ctx context.Context, input *core.AgentInput) (*core.AgentOutput, error) {
	// 使用 ReAct 循环处理
	loop := core.NewReActLoop(a.manager, a)
	return loop.Run(ctx, input)
}

// 为了保持向后兼容，在 core 包中创建别名
func init() {
	// 这里可以注册到某个全局注册表
}
