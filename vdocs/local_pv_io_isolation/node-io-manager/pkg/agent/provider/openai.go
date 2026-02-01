// Package provider - OpenAI Provider
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

// OpenAIProvider OpenAI Provider
type OpenAIProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewOpenAIProvider 创建 OpenAI Provider
func NewOpenAIProvider(cfg Config) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120
	}

	return &OpenAIProvider{
		config:  cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Chat 聊天
func (p *OpenAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "gpt-4-turbo-preview"
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": p.convertMessages(req.Messages),
	}

	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
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
		body["stop"] = req.Stop
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
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

	var result openAIResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := result.Choices[0]
	chatResp := &ChatResponse{
		ID:           result.ID,
		Model:        result.Model,
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
	}

	if result.Usage != nil {
		chatResp.Usage = &Usage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	// 处理工具调用
	if len(choice.Message.ToolCalls) > 0 {
		chatResp.ToolCalls = make([]ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			chatResp.ToolCalls[i] = ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}

	return chatResp, nil
}

// ChatStream 流式聊天
func (p *OpenAIProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	req.Stream = true
	ch := make(chan *StreamChunk, 100)

	go func() {
		defer close(ch)
		// TODO: 实现 SSE 流式处理
		// 暂时使用非流式
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
func (p *OpenAIProvider) convertMessages(messages []Message) []map[string]interface{} {
	result := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.Name != "" {
			m["name"] = msg.Name
		}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		result[i] = m
	}
	return result
}

// GetName 获取名称
func (p *OpenAIProvider) GetName() string {
	return "openai"
}

// GetModel 获取模型
func (p *OpenAIProvider) GetModel() string {
	return p.config.Model
}

// ListModels 列出模型
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"gpt-4-turbo-preview",
		"gpt-4-0125-preview",
		"gpt-4-1106-preview",
		"gpt-4",
		"gpt-4-32k",
		"gpt-3.5-turbo",
		"gpt-3.5-turbo-16k",
	}, nil
}

// SupportsTools 是否支持工具
func (p *OpenAIProvider) SupportsTools() bool {
	return true
}

// SupportsVision 是否支持视觉
func (p *OpenAIProvider) SupportsVision() bool {
	return true
}

// openAIResponse OpenAI 响应结构
type openAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// AzureOpenAIProvider Azure OpenAI Provider
type AzureOpenAIProvider struct {
	*OpenAIProvider
	deployment string
	apiVersion string
}

// NewAzureOpenAIProvider 创建 Azure OpenAI Provider
func NewAzureOpenAIProvider(cfg Config) (*AzureOpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Azure OpenAI API key is required")
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("Azure OpenAI endpoint is required")
	}

	deployment := cfg.AzureDeployment
	if deployment == "" {
		deployment = cfg.Model
	}

	apiVersion := cfg.AzureAPIVersion
	if apiVersion == "" {
		apiVersion = "2024-02-15-preview"
	}

	baseProvider, err := NewOpenAIProvider(cfg)
	if err != nil {
		return nil, err
	}

	// 修改 baseURL 为 Azure 格式
	baseProvider.baseURL = fmt.Sprintf("%s/openai/deployments/%s", cfg.BaseURL, deployment)

	return &AzureOpenAIProvider{
		OpenAIProvider: baseProvider,
		deployment:     deployment,
		apiVersion:     apiVersion,
	}, nil
}

// Chat Azure 聊天 (覆盖认证方式)
func (p *AzureOpenAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Azure 使用 api-key header
	model := req.Model
	if model == "" {
		model = p.config.Model
	}

	body := map[string]interface{}{
		"messages": p.convertMessages(req.Messages),
	}

	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/chat/completions?api-version=%s", p.baseURL, p.apiVersion)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", p.config.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result openAIResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := result.Choices[0]
	return &ChatResponse{
		ID:           result.ID,
		Model:        result.Model,
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
	}, nil
}

// GetName 获取名称
func (p *AzureOpenAIProvider) GetName() string {
	return "azure_openai"
}
