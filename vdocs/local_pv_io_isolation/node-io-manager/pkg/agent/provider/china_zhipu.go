// Package provider - 智谱 ChatGLM Provider
package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ZhipuProvider 智谱 Provider
type ZhipuProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewZhipuProvider 创建智谱 Provider
func NewZhipuProvider(cfg Config) (*ZhipuProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Zhipu API key is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://open.bigmodel.cn/api/paas/v4"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120
	}

	return &ZhipuProvider{
		config:  cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// generateToken 生成 JWT Token
func (p *ZhipuProvider) generateToken() (string, error) {
	parts := strings.Split(p.config.APIKey, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid API key format")
	}

	apiKey := parts[0]
	secret := parts[1]

	now := time.Now()
	exp := now.Add(time.Hour)

	// Header
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","sign_type":"SIGN"}`))

	// Payload
	payload := fmt.Sprintf(`{"api_key":"%s","exp":%d,"timestamp":%d}`, apiKey, exp.UnixMilli(), now.UnixMilli())
	payloadEncoded := base64.RawURLEncoding.EncodeToString([]byte(payload))

	// Signature
	signInput := header + "." + payloadEncoded
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signInput + "." + signature, nil
}

// Chat 聊天
func (p *ZhipuProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	token, err := p.generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "glm-4"
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": p.convertMessages(req.Messages),
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

	// 工具调用
	if len(req.Tools) > 0 {
		body["tools"] = p.convertTools(req.Tools)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

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

	var result zhipuResponse
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

	// 处理工具调用
	if len(choice.Message.ToolCalls) > 0 {
		for _, tc := range choice.Message.ToolCalls {
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
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	return chatResp, nil
}

// ChatStream 流式聊天
func (p *ZhipuProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
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
func (p *ZhipuProvider) convertMessages(messages []Message) []map[string]interface{} {
	result := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		result[i] = map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.ToolCallID != "" {
			result[i]["tool_call_id"] = msg.ToolCallID
		}
	}
	return result
}

// convertTools 转换工具格式
func (p *ZhipuProvider) convertTools(tools []Tool) []map[string]interface{} {
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
func (p *ZhipuProvider) GetName() string { return "zhipu" }

// GetModel 获取模型
func (p *ZhipuProvider) GetModel() string { return p.config.Model }

// ListModels 列出模型
func (p *ZhipuProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"glm-4",
		"glm-4-plus",
		"glm-4-air",
		"glm-4-airx",
		"glm-4-flash",
		"glm-4-long",
		"glm-4v",
		"glm-3-turbo",
	}, nil
}

// SupportsTools 是否支持工具
func (p *ZhipuProvider) SupportsTools() bool { return true }

// SupportsVision 是否支持视觉
func (p *ZhipuProvider) SupportsVision() bool { return true }

// zhipuResponse 智谱响应结构
type zhipuResponse struct {
	ID      string `json:"id"`
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
