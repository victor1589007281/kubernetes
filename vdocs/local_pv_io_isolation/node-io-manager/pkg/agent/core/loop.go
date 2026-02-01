// Package core - ReAct 循环实现
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// ReActLoop ReAct 推理-行动循环
type ReActLoop struct {
	manager       *AgentManager
	agent         Agent
	maxIterations int
	timeout       time.Duration
}

// NewReActLoop 创建 ReAct 循环
func NewReActLoop(manager *AgentManager, agent Agent) *ReActLoop {
	return &ReActLoop{
		manager:       manager,
		agent:         agent,
		maxIterations: manager.config.MaxIterations,
		timeout:       manager.config.Timeout,
	}
}

// Run 运行循环
func (r *ReActLoop) Run(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	// 设置超时
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// 准备消息历史
	messages := []ChatMessage{
		{
			Role:    "system",
			Content: r.buildSystemPrompt(),
		},
		{
			Role:    "user",
			Content: r.buildUserMessage(input),
		},
	}

	var lastOutput *AgentOutput

	for iteration := 0; iteration < r.maxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		log.WithFields(log.Fields{
			"agent":     r.agent.GetID(),
			"iteration": iteration + 1,
		}).Debug("ReAct loop iteration")

		// 调用 LLM
		response, err := r.manager.provider.Chat(ctx, ChatRequest{
			Messages: messages,
			Tools:    r.manager.toolRegistry.GetToolDefinitions(),
		})
		if err != nil {
			return nil, fmt.Errorf("LLM call failed: %w", err)
		}

		// 解析响应
		output := r.parseResponse(response)
		lastOutput = output

		// 添加助手消息到历史
		messages = append(messages, ChatMessage{
			Role:      "assistant",
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
		})

		// 检查是否完成
		if output.Finished {
			log.WithFields(log.Fields{
				"agent":        r.agent.GetID(),
				"iterations":   iteration + 1,
				"finish_reason": output.FinishReason,
			}).Info("ReAct loop completed")
			return output, nil
		}

		// 检查是否有工具调用
		if len(response.ToolCalls) == 0 {
			// 没有工具调用且未明确结束，视为完成
			output.Finished = true
			output.FinishReason = "no_more_actions"
			return output, nil
		}

		// 执行工具调用
		for _, tc := range response.ToolCalls {
			result, err := r.executeTool(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			// 添加工具结果到历史
			messages = append(messages, ChatMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})

			output.ToolCalls = append(output.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
				Result:    result,
			})
		}
	}

	// 达到最大迭代次数
	if lastOutput != nil {
		lastOutput.Finished = true
		lastOutput.FinishReason = "max_iterations"
	}

	return lastOutput, nil
}

// buildSystemPrompt 构建系统提示词
func (r *ReActLoop) buildSystemPrompt() string {
	role := r.agent.GetRole()

	basePrompt := `你是一个 Kubernetes 节点 IO 管理专家系统的一部分。你的任务是帮助分析和解决节点级别的 IO 问题。

你可以使用以下工具来完成任务。每次只能调用一个工具，等待结果后再决定下一步。

当你完成任务或无法继续时，请直接给出最终答案，不要再调用工具。

`

	switch role {
	case RoleNodeManager:
		return basePrompt + `
## 你的角色: Node Manager (节点管理者)

作为节点管理者，你负责：
1. 接收用户请求或系统告警
2. 分析整体情况
3. 协调专家和工作者完成任务
4. 汇总结果并给出最终建议

如果需要深度分析，可以使用 delegate_to_expert 工具委托给 IO 专家。

## 输出格式

分析完成后，请给出：
1. 问题概述
2. 根因分析
3. 推荐操作（按优先级排序）
4. 预期效果
`

	case RoleIOExpert:
		return basePrompt + `
## 你的角色: IO Expert (IO 专家)

作为 IO 专家，你负责：
1. 深度分析 IO 问题
2. 识别受害者和攻击者
3. 评估操作影响
4. 给出精确的技术建议

专注于技术细节，提供数据支撑的分析。
`

	case RoleWorker:
		return basePrompt + `
## 你的角色: Worker (执行者)

作为执行者，你负责：
1. 执行具体的诊断或修复任务
2. 收集数据
3. 报告执行结果
`

	default:
		return basePrompt
	}
}

// buildUserMessage 构建用户消息
func (r *ReActLoop) buildUserMessage(input *AgentInput) string {
	msg := input.Query

	if input.Context != "" {
		msg += "\n\n## 上下文信息\n\n" + input.Context
	}

	if len(input.Data) > 0 {
		dataJSON, _ := json.MarshalIndent(input.Data, "", "  ")
		msg += "\n\n## 附加数据\n\n```json\n" + string(dataJSON) + "\n```"
	}

	return msg
}

// parseResponse 解析响应
func (r *ReActLoop) parseResponse(response *ChatResponse) *AgentOutput {
	output := &AgentOutput{
		Response: response.Content,
		Actions:  make([]RecommendedAction, 0),
		Metadata: make(map[string]interface{}),
	}

	// 检查结束原因
	switch response.FinishReason {
	case "stop":
		output.Finished = true
		output.FinishReason = "completed"
	case "tool_calls":
		output.Finished = false
		output.FinishReason = "tool_calls"
	case "length":
		output.Finished = true
		output.FinishReason = "max_tokens"
	default:
		output.Finished = false
		output.FinishReason = response.FinishReason
	}

	// 解析响应中的推荐操作
	output.Actions = r.extractActions(response.Content)

	return output
}

// extractActions 从响应中提取推荐操作
func (r *ReActLoop) extractActions(content string) []RecommendedAction {
	// TODO: 使用更精确的解析逻辑
	// 暂时返回空列表
	return []RecommendedAction{}
}

// executeTool 执行工具
func (r *ReActLoop) executeTool(ctx context.Context, tc LLMToolCall) (string, error) {
	tool := r.manager.toolRegistry.Get(tc.Function.Name)
	if tool == nil {
		return "", fmt.Errorf("unknown tool: %s", tc.Function.Name)
	}

	log.WithFields(log.Fields{
		"tool": tc.Function.Name,
		"args": tc.Function.Arguments,
	}).Debug("Executing tool")

	// 执行工具
	result, err := tool.Execute(ctx, tc.Function.Arguments)
	if err != nil {
		return "", err
	}

	// 如果结果是结构体，转换为 JSON
	if _, ok := result.(string); !ok {
		jsonResult, err := json.Marshal(result)
		if err != nil {
			return fmt.Sprintf("%v", result), nil
		}
		return string(jsonResult), nil
	}

	return result.(string), nil
}
