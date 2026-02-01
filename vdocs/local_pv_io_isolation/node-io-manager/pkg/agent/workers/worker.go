// Package workers - Worker Agent
package workers

import (
	"context"
	"fmt"

	"github.com/node-io-manager/pkg/agent/core"
)

// WorkerType Worker 类型
type WorkerType string

const (
	WorkerDiagnostic   WorkerType = "diagnostic"
	WorkerRemediation  WorkerType = "remediation"
	WorkerPrediction   WorkerType = "prediction"
	WorkerDataCollect  WorkerType = "data_collect"
)

// WorkerAgent Worker Agent
type WorkerAgent struct {
	manager    *core.AgentManager
	id         string
	workerType WorkerType
}

// NewWorkerAgent 创建 Worker Agent
func NewWorkerAgent(manager *core.AgentManager, workerType WorkerType) *WorkerAgent {
	return &WorkerAgent{
		manager:    manager,
		id:         fmt.Sprintf("worker-%s-1", workerType),
		workerType: workerType,
	}
}

// GetRole 获取角色
func (w *WorkerAgent) GetRole() core.AgentRole {
	return core.RoleWorker
}

// GetID 获取 ID
func (w *WorkerAgent) GetID() string {
	return w.id
}

// GetWorkerType 获取 Worker 类型
func (w *WorkerAgent) GetWorkerType() WorkerType {
	return w.workerType
}

// Process 处理请求
func (w *WorkerAgent) Process(ctx context.Context, input *core.AgentInput) (*core.AgentOutput, error) {
	// Worker 直接执行任务，不需要 ReAct 循环
	switch w.workerType {
	case WorkerDiagnostic:
		return w.executeDiagnostic(ctx, input)
	case WorkerRemediation:
		return w.executeRemediation(ctx, input)
	case WorkerPrediction:
		return w.executePrediction(ctx, input)
	case WorkerDataCollect:
		return w.executeDataCollection(ctx, input)
	default:
		return nil, fmt.Errorf("unknown worker type: %s", w.workerType)
	}
}

// executeDiagnostic 执行诊断任务
func (w *WorkerAgent) executeDiagnostic(ctx context.Context, input *core.AgentInput) (*core.AgentOutput, error) {
	// 执行诊断工具
	output := &core.AgentOutput{
		Response: "诊断任务执行完成",
		Finished: true,
		Metadata: make(map[string]interface{}),
	}

	// 根据输入执行相应的诊断
	if podName, ok := input.Data["pod_name"].(string); ok {
		// 收集 Pod 诊断信息
		output.Metadata["pod_name"] = podName
		output.Metadata["diagnostic_type"] = "pod_io"

		// 执行工具调用
		if w.manager != nil && w.manager.GetToolRegistry() != nil {
			result, err := w.manager.GetToolRegistry().ExecuteTool(ctx, "get_pod_profile", map[string]interface{}{
				"name":      podName,
				"namespace": input.Data["namespace"],
			})
			if err == nil {
				output.Metadata["profile"] = result
			}
		}
	}

	return output, nil
}

// executeRemediation 执行修复任务
func (w *WorkerAgent) executeRemediation(ctx context.Context, input *core.AgentInput) (*core.AgentOutput, error) {
	output := &core.AgentOutput{
		Response: "修复任务执行完成",
		Finished: true,
		Actions:  make([]core.RecommendedAction, 0),
		Metadata: make(map[string]interface{}),
	}

	// 获取修复参数
	actionType, _ := input.Data["action_type"].(string)
	targetPod, _ := input.Data["target_pod"].(string)
	namespace, _ := input.Data["namespace"].(string)

	switch actionType {
	case "limit_io":
		readIOPS, _ := input.Data["read_iops"].(float64)
		writeIOPS, _ := input.Data["write_iops"].(float64)

		if w.manager != nil && w.manager.GetToolRegistry() != nil {
			_, err := w.manager.GetToolRegistry().ExecuteTool(ctx, "limit_pod_io", map[string]interface{}{
				"name":       targetPod,
				"namespace":  namespace,
				"read_iops":  readIOPS,
				"write_iops": writeIOPS,
			})
			if err != nil {
				output.Response = fmt.Sprintf("修复失败: %v", err)
			} else {
				output.Response = fmt.Sprintf("已对 %s/%s 应用 IO 限制", namespace, targetPod)
				output.Actions = append(output.Actions, core.RecommendedAction{
					Type:       "limit_io",
					Target:     targetPod,
					Namespace:  namespace,
					Confidence: 1.0,
					Reason:     "Worker 执行的 IO 限制",
				})
			}
		}

	case "remove_limit":
		if w.manager != nil && w.manager.GetToolRegistry() != nil {
			_, err := w.manager.GetToolRegistry().ExecuteTool(ctx, "remove_io_limit", map[string]interface{}{
				"name":      targetPod,
				"namespace": namespace,
			})
			if err != nil {
				output.Response = fmt.Sprintf("移除限制失败: %v", err)
			} else {
				output.Response = fmt.Sprintf("已移除 %s/%s 的 IO 限制", namespace, targetPod)
			}
		}
	}

	return output, nil
}

// executePrediction 执行预测任务
func (w *WorkerAgent) executePrediction(ctx context.Context, input *core.AgentInput) (*core.AgentOutput, error) {
	output := &core.AgentOutput{
		Response: "预测任务执行完成",
		Finished: true,
		Metadata: make(map[string]interface{}),
	}

	// TODO: 调用预测引擎
	output.Metadata["predictions"] = []map[string]interface{}{
		{
			"metric":         "disk_util",
			"current_value":  75.0,
			"predicted_value": 90.0,
			"time_to_threshold": "5m",
			"confidence":     0.8,
		},
	}

	return output, nil
}

// executeDataCollection 执行数据收集任务
func (w *WorkerAgent) executeDataCollection(ctx context.Context, input *core.AgentInput) (*core.AgentOutput, error) {
	output := &core.AgentOutput{
		Response: "数据收集任务执行完成",
		Finished: true,
		Metadata: make(map[string]interface{}),
	}

	// 收集系统 IO 数据
	if w.manager != nil && w.manager.GetToolRegistry() != nil {
		result, err := w.manager.GetToolRegistry().ExecuteTool(ctx, "analyze_io", map[string]interface{}{
			"detail_level": "detailed",
		})
		if err == nil {
			output.Metadata["io_analysis"] = result
		}

		victims, err := w.manager.GetToolRegistry().ExecuteTool(ctx, "get_victims", map[string]interface{}{})
		if err == nil {
			output.Metadata["victims"] = victims
		}

		scores, err := w.manager.GetToolRegistry().ExecuteTool(ctx, "get_scores", map[string]interface{}{})
		if err == nil {
			output.Metadata["scores"] = scores
		}
	}

	return output, nil
}

// DiagnosticWorker 诊断 Worker 快捷创建
func DiagnosticWorker(manager *core.AgentManager) *WorkerAgent {
	return NewWorkerAgent(manager, WorkerDiagnostic)
}

// RemediationWorker 修复 Worker 快捷创建
func RemediationWorker(manager *core.AgentManager) *WorkerAgent {
	return NewWorkerAgent(manager, WorkerRemediation)
}

// PredictionWorker 预测 Worker 快捷创建
func PredictionWorker(manager *core.AgentManager) *WorkerAgent {
	return NewWorkerAgent(manager, WorkerPrediction)
}
