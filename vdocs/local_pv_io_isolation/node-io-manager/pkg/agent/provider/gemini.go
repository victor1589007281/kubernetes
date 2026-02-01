// Package provider - Google Gemini Provider
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

// GeminiProvider Google Gemini Provider
type GeminiProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewGeminiProvider 创建 Gemini Provider
func NewGeminiProvider(cfg Config) (*GeminiProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Gemini API key is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120
	}

	return &GeminiProvider{
		config:  cfg,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}, nil
}

// Chat 聊天
func (p *GeminiProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "gemini-pro"
	}

	contents := p.convertMessages(req.Messages)

	body := map[string]interface{}{
		"contents": contents,
		"generationConfig": map[string]interface{}{
			"temperature":     req.Temperature,
			"maxOutputTokens": req.MaxTokens,
			"topP":            req.TopP,
		},
	}

	if len(req.Tools) > 0 {
		body["tools"] = []map[string]interface{}{
			{"functionDeclarations": p.convertTools(req.Tools)},
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", p.baseURL, model, p.config.APIKey)
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	candidate := result.Candidates[0]
	chatResp := &ChatResponse{
		Model:        model,
		FinishReason: candidate.FinishReason,
	}

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			chatResp.Content += part.Text
		}
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID:   fmt.Sprintf("call_%d", len(chatResp.ToolCalls)),
				Type: "function",
				Function: FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	if result.UsageMetadata != nil {
		chatResp.Usage = &Usage{
			PromptTokens:     result.UsageMetadata.PromptTokenCount,
			CompletionTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      result.UsageMetadata.TotalTokenCount,
		}
	}

	return chatResp, nil
}

// ChatStream 流式聊天
func (p *GeminiProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	ch := make(chan *StreamChunk, 100)
	go func() {
		defer close(ch)
		resp, err := p.Chat(ctx, req)
		if err != nil {
			ch <- &StreamChunk{Error: err, Done: true}
			return
		}
		ch <- &StreamChunk{
			Delta:        resp.Content,
			ToolCalls:    resp.ToolCalls,
			FinishReason: resp.FinishReason,
			Done:         true,
		}
	}()
	return ch, nil
}

// convertMessages 转换消息格式
func (p *GeminiProvider) convertMessages(messages []Message) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		if msg.Role == "system" {
			// Gemini 将 system 作为第一条 user 消息
			result = append(result, map[string]interface{}{
				"role":  "user",
				"parts": []map[string]string{{"text": msg.Content}},
			})
			continue
		}

		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		m := map[string]interface{}{
			"role":  role,
			"parts": []map[string]string{{"text": msg.Content}},
		}

		result = append(result, m)
	}

	return result
}

// convertTools 转换工具格式
func (p *GeminiProvider) convertTools(tools []Tool) []map[string]interface{} {
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
func (p *GeminiProvider) GetName() string { return "gemini" }

// GetModel 获取模型
func (p *GeminiProvider) GetModel() string { return p.config.Model }

// ListModels 列出模型
func (p *GeminiProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"gemini-pro",
		"gemini-pro-vision",
		"gemini-1.5-pro-latest",
		"gemini-1.5-flash-latest",
	}, nil
}

// SupportsTools 是否支持工具
func (p *GeminiProvider) SupportsTools() bool { return true }

// SupportsVision 是否支持视觉
func (p *GeminiProvider) SupportsVision() bool { return true }

// geminiResponse Gemini 响应结构
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string `json:"text,omitempty"`
				FunctionCall *struct {
					Name string                 `json:"name"`
					Args map[string]interface{} `json:"args"`
				} `json:"functionCall,omitempty"`
			} `json:"parts"`
			Role string `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}
