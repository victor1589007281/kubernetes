// Package provider - 文心一言 Provider (百度)
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ErnieProvider 文心一言 Provider
type ErnieProvider struct {
	config      Config
	client      *http.Client
	baseURL     string
	accessToken string
	tokenMu     sync.RWMutex
	tokenExpiry time.Time
}

// NewErnieProvider 创建文心一言 Provider
func NewErnieProvider(cfg Config) (*ErnieProvider, error) {
	if cfg.APIKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("Ernie API key and secret key are required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://aip.baidubce.com"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120
	}

	return &ErnieProvider{
		config:  cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// getAccessToken 获取 access_token
func (p *ErnieProvider) getAccessToken(ctx context.Context) (string, error) {
	p.tokenMu.RLock()
	if p.accessToken != "" && time.Now().Before(p.tokenExpiry) {
		token := p.accessToken
		p.tokenMu.RUnlock()
		return token, nil
	}
	p.tokenMu.RUnlock()

	// 获取新 token
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	// 双重检查
	if p.accessToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.accessToken, nil
	}

	url := fmt.Sprintf("%s/oauth/2.0/token?grant_type=client_credentials&client_id=%s&client_secret=%s",
		p.baseURL, p.config.APIKey, p.config.SecretKey)

	resp, err := p.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != "" {
		return "", fmt.Errorf("token error: %s", result.Error)
	}

	p.accessToken = result.AccessToken
	p.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-300) * time.Second) // 提前5分钟过期

	return p.accessToken, nil
}

// getModelEndpoint 获取模型端点
func (p *ErnieProvider) getModelEndpoint(model string) string {
	endpoints := map[string]string{
		"ernie-4.0-8k":       "completions_pro",
		"ernie-4.0-turbo-8k": "ernie-4.0-turbo-8k",
		"ernie-3.5-8k":       "completions",
		"ernie-3.5-128k":     "ernie-3.5-128k",
		"ernie-speed-8k":     "ernie_speed",
		"ernie-speed-128k":   "ernie-speed-128k",
		"ernie-lite-8k":      "ernie-lite-8k",
		"ernie-tiny-8k":      "ernie-tiny-8k",
	}
	if endpoint, ok := endpoints[model]; ok {
		return endpoint
	}
	return "completions_pro"
}

// Chat 聊天
func (p *ErnieProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "ernie-4.0-8k"
	}

	body := map[string]interface{}{
		"messages": p.convertMessages(req.Messages),
	}

	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}

	// 工具调用
	if len(req.Tools) > 0 {
		body["functions"] = p.convertTools(req.Tools)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := p.getModelEndpoint(model)
	url := fmt.Sprintf("%s/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/%s?access_token=%s",
		p.baseURL, endpoint, accessToken)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result ernieResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if result.ErrorCode != 0 {
		return nil, fmt.Errorf("API error %d: %s", result.ErrorCode, result.ErrorMsg)
	}

	chatResp := &ChatResponse{
		ID:           result.ID,
		Model:        model,
		Content:      result.Result,
		FinishReason: "stop",
	}

	// 处理函数调用
	if result.FunctionCall != nil {
		argsJSON, _ := json.Marshal(result.FunctionCall.Arguments)
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID:   "call_1",
			Type: "function",
			Function: FunctionCall{
				Name:      result.FunctionCall.Name,
				Arguments: string(argsJSON),
			},
		})
		chatResp.FinishReason = "tool_calls"
	}

	chatResp.Usage = &Usage{
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
	}

	return chatResp, nil
}

// ChatStream 流式聊天
func (p *ErnieProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
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
func (p *ErnieProvider) convertMessages(messages []Message) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			// ERNIE 将 system 作为单独的参数
			continue
		}
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		result = append(result, m)
	}
	return result
}

// convertTools 转换工具格式
func (p *ErnieProvider) convertTools(tools []Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		result[i] = map[string]interface{}{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		}
	}
	return result
}

// GetName 获取名称
func (p *ErnieProvider) GetName() string { return "ernie" }

// GetModel 获取模型
func (p *ErnieProvider) GetModel() string { return p.config.Model }

// ListModels 列出模型
func (p *ErnieProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"ernie-4.0-8k",
		"ernie-4.0-turbo-8k",
		"ernie-3.5-8k",
		"ernie-3.5-128k",
		"ernie-speed-8k",
		"ernie-speed-128k",
		"ernie-lite-8k",
		"ernie-tiny-8k",
	}, nil
}

// SupportsTools 是否支持工具
func (p *ErnieProvider) SupportsTools() bool { return true }

// SupportsVision 是否支持视觉
func (p *ErnieProvider) SupportsVision() bool { return false }

// ernieResponse 文心一言响应结构
type ernieResponse struct {
	ID           string `json:"id"`
	Object       string `json:"object"`
	Created      int64  `json:"created"`
	Result       string `json:"result"`
	IsTruncated  bool   `json:"is_truncated"`
	NeedClearHistory bool `json:"need_clear_history"`
	FunctionCall *struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function_call,omitempty"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	ErrorCode int    `json:"error_code,omitempty"`
	ErrorMsg  string `json:"error_msg,omitempty"`
}
