// Package provider - Anthropic Claude Provider
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

// ClaudeProvider Claude Provider
type ClaudeProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewClaudeProvider 创建 Claude Provider
func NewClaudeProvider(cfg Config) (*ClaudeProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Claude API key is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120
	}

	return &ClaudeProvider{
		config:  cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Chat 聊天
func (p *ClaudeProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "claude-3-opus-20240229"
	}

	// Claude 使用不同的消息格式
	messages, systemPrompt := p.convertMessages(req.Messages)

	body := map[string]interface{}{
		"model":      model,
		"messages":   messages,
		"max_tokens": 4096,
	}

	if systemPrompt != "" {
		body["system"] = systemPrompt
	}
	if len(req.Tools) > 0 {
		body["tools"] = p.convertTools(req.Tools)
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop_sequences"] = req.Stop
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.config.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

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

	var result claudeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	chatResp := &ChatResponse{
		ID:           result.ID,
		Model:        result.Model,
		FinishReason: result.StopReason,
	}

	// 提取文本内容
	for _, content := range result.Content {
		if content.Type == "text" {
			chatResp.Content += content.Text
		} else if content.Type == "tool_use" {
			argsJSON, _ := json.Marshal(content.Input)
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID:   content.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      content.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	if result.Usage != nil {
		chatResp.Usage = &Usage{
			PromptTokens:     result.Usage.InputTokens,
			CompletionTokens: result.Usage.OutputTokens,
			TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
		}
	}

	return chatResp, nil
}

// ChatStream 流式聊天
func (p *ClaudeProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
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
func (p *ClaudeProvider) convertMessages(messages []Message) ([]map[string]interface{}, string) {
	var systemPrompt string
	result := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		if msg.Role == "system" {
			systemPrompt = msg.Content
			continue
		}

		m := map[string]interface{}{
			"role": msg.Role,
		}

		// Claude 使用 content 数组
		if msg.ToolCallID != "" {
			// 工具结果
			m["content"] = []map[string]interface{}{
				{
					"type":        "tool_result",
					"tool_use_id": msg.ToolCallID,
					"content":     msg.Content,
				},
			}
		} else {
			m["content"] = msg.Content
		}

		result = append(result, m)
	}

	return result, systemPrompt
}

// convertTools 转换工具格式
func (p *ClaudeProvider) convertTools(tools []Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		result[i] = map[string]interface{}{
			"name":         tool.Function.Name,
			"description":  tool.Function.Description,
			"input_schema": tool.Function.Parameters,
		}
	}
	return result
}

// GetName 获取名称
func (p *ClaudeProvider) GetName() string {
	return "claude"
}

// GetModel 获取模型
func (p *ClaudeProvider) GetModel() string {
	return p.config.Model
}

// ListModels 列出模型
func (p *ClaudeProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"claude-3-opus-20240229",
		"claude-3-sonnet-20240229",
		"claude-3-haiku-20240307",
		"claude-2.1",
		"claude-2.0",
		"claude-instant-1.2",
	}, nil
}

// SupportsTools 是否支持工具
func (p *ClaudeProvider) SupportsTools() bool {
	return true
}

// SupportsVision 是否支持视觉
func (p *ClaudeProvider) SupportsVision() bool {
	return true
}

// claudeResponse Claude 响应结构
type claudeResponse struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string                 `json:"type"`
		Text  string                 `json:"text,omitempty"`
		ID    string                 `json:"id,omitempty"`
		Name  string                 `json:"name,omitempty"`
		Input map[string]interface{} `json:"input,omitempty"`
	} `json:"content"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}
