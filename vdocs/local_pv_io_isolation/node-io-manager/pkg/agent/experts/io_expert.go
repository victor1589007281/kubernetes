// Package experts - IO Expert Agent
package experts

import (
	"context"

	"github.com/node-io-manager/pkg/agent/core"
)

// IOExpertAgent IO 专家 Agent
type IOExpertAgent struct {
	manager *core.AgentManager
	id      string
}

// NewIOExpertAgent 创建 IO Expert Agent
func NewIOExpertAgent(manager *core.AgentManager) *IOExpertAgent {
	return &IOExpertAgent{
		manager: manager,
		id:      "io-expert-1",
	}
}

// GetRole 获取角色
func (a *IOExpertAgent) GetRole() core.AgentRole {
	return core.RoleIOExpert
}

// GetID 获取 ID
func (a *IOExpertAgent) GetID() string {
	return a.id
}

// Process 处理请求
func (a *IOExpertAgent) Process(ctx context.Context, input *core.AgentInput) (*core.AgentOutput, error) {
	// 使用 ReAct 循环处理
	loop := core.NewReActLoop(a.manager, a)
	return loop.Run(ctx, input)
}

// AnalyzeVictims 分析受害者
func (a *IOExpertAgent) AnalyzeVictims(ctx context.Context, data map[string]interface{}) (*core.AgentOutput, error) {
	input := &core.AgentInput{
		Query: "请深度分析以下受害者 Pod 的 IO 问题，找出根因和最佳解决方案。",
		Data:  data,
	}
	return a.Process(ctx, input)
}

// DiagnoseIOProblem 诊断 IO 问题
func (a *IOExpertAgent) DiagnoseIOProblem(ctx context.Context, podName, namespace string) (*core.AgentOutput, error) {
	input := &core.AgentInput{
		Query: "请诊断指定 Pod 的 IO 问题。",
		Data: map[string]interface{}{
			"pod_name":  podName,
			"namespace": namespace,
		},
	}
	return a.Process(ctx, input)
}
