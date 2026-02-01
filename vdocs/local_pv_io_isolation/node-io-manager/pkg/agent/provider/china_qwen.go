// Package provider - 通义千问 Provider (阿里云 DashScope)
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// QwenProvider 通义千问 Provider
type QwenProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewQwenProvider 创建通义千问 Provider
func NewQwenProvider(cfg Config) (*QwenProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("DashScope API key is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com/api/v1"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120
	}

	return &QwenProvider{
		config:  cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Chat 聊天
func (p *QwenProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "qwen-max"
	}

	body := map[string]interface{}{
		"model": model,
		"input": map[string]interface{}{
			"messages": p.convertMessages(req.Messages),
		},
		"parameters": map[string]interface{}{},
	}

	params := body["parameters"].(map[string]interface{})
	if req.Temperature > 0 {
		params["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		params["max_tokens"] = req.MaxTokens
	}
	if req.TopP > 0 {
		params["top_p"] = req.TopP
	}
	if len(req.Stop) > 0 {
		params["stop"] = req.Stop
	}

	// 工具调用
	if len(req.Tools) > 0 {
		params["tools"] = p.convertTools(req.Tools)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := p.baseURL + "/services/aigc/text-generation/generation"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result qwenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Code != "" {
		return nil, fmt.Errorf("API error: %s - %s", result.Code, result.Message)
	}

	chatResp := &ChatResponse{
		ID:           result.RequestID,
		Model:        model,
		Content:      result.Output.Text,
		FinishReason: result.Output.FinishReason,
	}

	// 处理工具调用
	if len(result.Output.ToolCalls) > 0 {
		for _, tc := range result.Output.ToolCalls {
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	if result.Usage != nil {
		chatResp.Usage = &Usage{
			PromptTokens:     result.Usage.InputTokens,
			CompletionTokens: result.Usage.OutputTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	return chatResp, nil
}

// ChatStream 流式聊天
func (p *QwenProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	ch := make(chan *StreamChunk, 100)
	go func() {
		defer close(ch)
		resp, err := p.Chat(ctx, req)
		if err != nil {
			ch <- &StreamChunk{Error: err, Done: true}
			return
		}
		ch <- &StreamChunk{
			ID:           resp.ID,
			Delta:        resp.Content,
			ToolCalls:    resp.ToolCalls,
			FinishReason: resp.FinishReason,
			Done:         true,
		}
	}()
	return ch, nil
}

// convertMessages 转换消息格式
func (p *QwenProvider) convertMessages(messages []Message) []map[string]interface{} {
	result := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		result[i] = map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.ToolCallID != "" {
			result[i]["tool_call_id"] = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			result[i]["tool_calls"] = msg.ToolCalls
		}
	}
	return result
}

// convertTools 转换工具格式
func (p *QwenProvider) convertTools(tools []Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		result[i] = map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Function.Name,
				"description": tool.Function.Description,
				"parameters":  tool.Function.Parameters,
			},
		}
	}
	return result
}

// GetName 获取名称
func (p *QwenProvider) GetName() string { return "qwen" }

// GetModel 获取模型
func (p *QwenProvider) GetModel() string { return p.config.Model }

// ListModels 列出模型
func (p *QwenProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"qwen-max",
		"qwen-max-longcontext",
		"qwen-plus",
		"qwen-turbo",
		"qwen-72b-chat",
		"qwen-14b-chat",
		"qwen-7b-chat",
		"qwen2-72b-instruct",
		"qwen2-57b-a14b-instruct",
		"qwen2-7b-instruct",
	}, nil
}

// SupportsTools 是否支持工具
func (p *QwenProvider) SupportsTools() bool { return true }

// SupportsVision 是否支持视觉
func (p *QwenProvider) SupportsVision() bool { return true }

// qwenResponse 通义千问响应结构
type qwenResponse struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Output    struct {
		Text         string `json:"text"`
		FinishReason string `json:"finish_reason"`
		ToolCalls    []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls,omitempty"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}
