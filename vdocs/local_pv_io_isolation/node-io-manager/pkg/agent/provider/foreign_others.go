// Package provider - 其他国外大模型 Provider
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

// ======================== Mistral ========================

// MistralProvider Mistral Provider
type MistralProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewMistralProvider 创建 Mistral Provider
func NewMistralProvider(cfg Config) (*MistralProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Mistral API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.mistral.ai/v1"
	}
	return &MistralProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *MistralProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "mistral-large-latest", req)
}
func (p *MistralProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *MistralProvider) GetName() string  { return "mistral" }
func (p *MistralProvider) GetModel() string { return p.config.Model }
func (p *MistralProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"mistral-large-latest",
		"mistral-medium-latest",
		"mistral-small-latest",
		"open-mixtral-8x22b",
		"open-mixtral-8x7b",
		"open-mistral-7b",
		"codestral-latest",
	}, nil
}
func (p *MistralProvider) SupportsTools() bool  { return true }
func (p *MistralProvider) SupportsVision() bool { return false }

// ======================== Cohere ========================

// CohereProvider Cohere Provider
type CohereProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewCohereProvider 创建 Cohere Provider
func NewCohereProvider(cfg Config) (*CohereProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Cohere API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.cohere.ai/v1"
	}
	return &CohereProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *CohereProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "command-r-plus"
	}

	// Cohere 使用不同的 API 格式
	body := map[string]interface{}{
		"model": model,
	}

	// 转换消息
	var chatHistory []map[string]string
	var message string
	var preamble string

	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			preamble = msg.Content
		case "user":
			if message == "" {
				message = msg.Content
			} else {
				chatHistory = append(chatHistory, map[string]string{
					"role":    "USER",
					"message": msg.Content,
				})
			}
		case "assistant":
			chatHistory = append(chatHistory, map[string]string{
				"role":    "CHATBOT",
				"message": msg.Content,
			})
		}
	}

	body["message"] = message
	if preamble != "" {
		body["preamble"] = preamble
	}
	if len(chatHistory) > 0 {
		body["chat_history"] = chatHistory
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	// 工具调用
	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = map[string]interface{}{
				"name":                 tool.Function.Name,
				"description":          tool.Function.Description,
				"parameter_definitions": tool.Function.Parameters,
			}
		}
		body["tools"] = tools
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

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

	var result struct {
		Text         string `json:"text"`
		GenerationID string `json:"generation_id"`
		FinishReason string `json:"finish_reason"`
		ToolCalls    []struct {
			Name       string                 `json:"name"`
			Parameters map[string]interface{} `json:"parameters"`
		} `json:"tool_calls,omitempty"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	chatResp := &ChatResponse{
		ID:           result.GenerationID,
		Model:        model,
		Content:      result.Text,
		FinishReason: result.FinishReason,
	}

	// 处理工具调用
	for i, tc := range result.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Parameters)
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID:   fmt.Sprintf("call_%d", i),
			Type: "function",
			Function: FunctionCall{
				Name:      tc.Name,
				Arguments: string(argsJSON),
			},
		})
	}

	return chatResp, nil
}

func (p *CohereProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *CohereProvider) GetName() string  { return "cohere" }
func (p *CohereProvider) GetModel() string { return p.config.Model }
func (p *CohereProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"command-r-plus", "command-r", "command", "command-light"}, nil
}
func (p *CohereProvider) SupportsTools() bool  { return true }
func (p *CohereProvider) SupportsVision() bool { return false }

// ======================== Groq ========================

// GroqProvider Groq Provider (高速推理)
type GroqProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewGroqProvider 创建 Groq Provider
func NewGroqProvider(cfg Config) (*GroqProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Groq API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.groq.com/openai/v1"
	}
	return &GroqProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *GroqProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "mixtral-8x7b-32768", req)
}
func (p *GroqProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *GroqProvider) GetName() string  { return "groq" }
func (p *GroqProvider) GetModel() string { return p.config.Model }
func (p *GroqProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"llama-3.1-405b-reasoning",
		"llama-3.1-70b-versatile",
		"llama-3.1-8b-instant",
		"mixtral-8x7b-32768",
		"gemma2-9b-it",
	}, nil
}
func (p *GroqProvider) SupportsTools() bool  { return true }
func (p *GroqProvider) SupportsVision() bool { return false }

// ======================== Together AI ========================

// TogetherProvider Together AI Provider
type TogetherProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewTogetherProvider 创建 Together AI Provider
func NewTogetherProvider(cfg Config) (*TogetherProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Together API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.together.xyz/v1"
	}
	return &TogetherProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *TogetherProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "mistralai/Mixtral-8x7B-Instruct-v0.1", req)
}
func (p *TogetherProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *TogetherProvider) GetName() string  { return "together" }
func (p *TogetherProvider) GetModel() string { return p.config.Model }
func (p *TogetherProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{
		"mistralai/Mixtral-8x7B-Instruct-v0.1",
		"mistralai/Mistral-7B-Instruct-v0.2",
		"meta-llama/Llama-3-70b-chat-hf",
		"meta-llama/Llama-3-8b-chat-hf",
		"Qwen/Qwen2-72B-Instruct",
		"deepseek-ai/deepseek-coder-33b-instruct",
	}, nil
}
func (p *TogetherProvider) SupportsTools() bool  { return true }
func (p *TogetherProvider) SupportsVision() bool { return false }

// ======================== Ollama (本地) ========================

// OllamaProvider Ollama 本地 Provider
type OllamaProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewOllamaProvider 创建 Ollama Provider
func NewOllamaProvider(cfg Config) (*OllamaProvider, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 300 // 本地模型可能较慢
	}
	return &OllamaProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}, nil
}

func (p *OllamaProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	if model == "" {
		model = "qwen2:7b"
	}

	messages := make([]map[string]string, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		}
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}

	if req.Temperature > 0 {
		body["options"] = map[string]interface{}{
			"temperature": req.Temperature,
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")

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

	var result struct {
		Model   string `json:"model"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Done          bool `json:"done"`
		TotalDuration int  `json:"total_duration"`
		EvalCount     int  `json:"eval_count"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	return &ChatResponse{
		Model:        result.Model,
		Content:      result.Message.Content,
		FinishReason: "stop",
	}, nil
}

func (p *OllamaProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *OllamaProvider) GetName() string  { return "ollama" }
func (p *OllamaProvider) GetModel() string { return p.config.Model }
func (p *OllamaProvider) ListModels(ctx context.Context) ([]string, error) {
	// 从 Ollama API 获取本地模型列表
	resp, err := p.client.Get(p.baseURL + "/api/tags")
	if err != nil {
		return []string{"qwen2:7b", "llama3:8b", "mistral:7b"}, nil
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return []string{"qwen2:7b", "llama3:8b", "mistral:7b"}, nil
	}

	models := make([]string, len(result.Models))
	for i, m := range result.Models {
		models[i] = m.Name
	}

	if len(models) == 0 {
		return []string{"qwen2:7b", "llama3:8b", "mistral:7b"}, nil
	}

	return models, nil
}
func (p *OllamaProvider) SupportsTools() bool  { return false }
func (p *OllamaProvider) SupportsVision() bool { return true }
