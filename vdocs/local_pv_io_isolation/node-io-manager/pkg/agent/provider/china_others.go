// Package provider - 其他国内大模型 Provider
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

// ======================== 百川 Baichuan ========================

// BaichuanProvider 百川 Provider
type BaichuanProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewBaichuanProvider 创建百川 Provider
func NewBaichuanProvider(cfg Config) (*BaichuanProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Baichuan API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.baichuan-ai.com/v1"
	}
	return &BaichuanProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *BaichuanProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "Baichuan4", req)
}
func (p *BaichuanProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *BaichuanProvider) GetName() string  { return "baichuan" }
func (p *BaichuanProvider) GetModel() string { return p.config.Model }
func (p *BaichuanProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"Baichuan4", "Baichuan3-Turbo", "Baichuan3-Turbo-128k", "Baichuan2-Turbo"}, nil
}
func (p *BaichuanProvider) SupportsTools() bool  { return true }
func (p *BaichuanProvider) SupportsVision() bool { return false }

// ======================== MiniMax ========================

// MiniMaxProvider MiniMax Provider
type MiniMaxProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewMiniMaxProvider 创建 MiniMax Provider
func NewMiniMaxProvider(cfg Config) (*MiniMaxProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("MiniMax API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.minimax.chat/v1"
	}
	return &MiniMaxProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *MiniMaxProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "abab6.5-chat", req)
}
func (p *MiniMaxProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *MiniMaxProvider) GetName() string  { return "minimax" }
func (p *MiniMaxProvider) GetModel() string { return p.config.Model }
func (p *MiniMaxProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"abab6.5-chat", "abab6-chat", "abab5.5-chat"}, nil
}
func (p *MiniMaxProvider) SupportsTools() bool  { return true }
func (p *MiniMaxProvider) SupportsVision() bool { return false }

// ======================== 零一万物 Yi ========================

// YiProvider 零一万物 Provider
type YiProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewYiProvider 创建零一万物 Provider
func NewYiProvider(cfg Config) (*YiProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Yi API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.lingyiwanwu.com/v1"
	}
	return &YiProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *YiProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "yi-large", req)
}
func (p *YiProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *YiProvider) GetName() string  { return "yi" }
func (p *YiProvider) GetModel() string { return p.config.Model }
func (p *YiProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"yi-large", "yi-large-turbo", "yi-medium", "yi-spark"}, nil
}
func (p *YiProvider) SupportsTools() bool  { return true }
func (p *YiProvider) SupportsVision() bool { return true }

// ======================== 豆包 Doubao (字节) ========================

// DoubaoProvider 豆包 Provider
type DoubaoProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewDoubaoProvider 创建豆包 Provider
func NewDoubaoProvider(cfg Config) (*DoubaoProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Doubao API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://ark.cn-beijing.volces.com/api/v3"
	}
	return &DoubaoProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *DoubaoProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "doubao-pro-32k", req)
}
func (p *DoubaoProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *DoubaoProvider) GetName() string  { return "doubao" }
func (p *DoubaoProvider) GetModel() string { return p.config.Model }
func (p *DoubaoProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"doubao-pro-32k", "doubao-pro-128k", "doubao-lite-32k"}, nil
}
func (p *DoubaoProvider) SupportsTools() bool  { return true }
func (p *DoubaoProvider) SupportsVision() bool { return true }

// ======================== 混元 Hunyuan (腾讯) ========================

// HunyuanProvider 混元 Provider
type HunyuanProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewHunyuanProvider 创建混元 Provider
func NewHunyuanProvider(cfg Config) (*HunyuanProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Hunyuan API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://hunyuan.tencentcloudapi.com"
	}
	return &HunyuanProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *HunyuanProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// 腾讯混元使用 OpenAI 兼容接口
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "hunyuan-pro", req)
}
func (p *HunyuanProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *HunyuanProvider) GetName() string  { return "hunyuan" }
func (p *HunyuanProvider) GetModel() string { return p.config.Model }
func (p *HunyuanProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"hunyuan-pro", "hunyuan-standard", "hunyuan-lite"}, nil
}
func (p *HunyuanProvider) SupportsTools() bool  { return true }
func (p *HunyuanProvider) SupportsVision() bool { return true }

// ======================== 商汤日日新 SenseNova ========================

// SenseNovaProvider 商汤日日新 Provider
type SenseNovaProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewSenseNovaProvider 创建商汤日日新 Provider
func NewSenseNovaProvider(cfg Config) (*SenseNovaProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("SenseNova API key is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.sensenova.cn/v1"
	}
	return &SenseNovaProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *SenseNovaProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey, p.config.Model, "SenseChat-5", req)
}
func (p *SenseNovaProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *SenseNovaProvider) GetName() string  { return "sensenova" }
func (p *SenseNovaProvider) GetModel() string { return p.config.Model }
func (p *SenseNovaProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"SenseChat-5", "SenseChat-Turbo", "SenseChat-128K"}, nil
}
func (p *SenseNovaProvider) SupportsTools() bool  { return true }
func (p *SenseNovaProvider) SupportsVision() bool { return true }

// ======================== 讯飞星火 Spark ========================

// SparkProvider 讯飞星火 Provider
type SparkProvider struct {
	config  Config
	client  *http.Client
	baseURL string
}

// NewSparkProvider 创建讯飞星火 Provider
func NewSparkProvider(cfg Config) (*SparkProvider, error) {
	if cfg.APIKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("Spark API key and secret are required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://spark-api-open.xf-yun.com/v1"
	}
	return &SparkProvider{
		config:  cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
	}, nil
}

func (p *SparkProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// 讯飞星火支持 OpenAI 兼容接口
	return openAICompatibleChat(ctx, p.client, p.baseURL, p.config.APIKey+":"+p.config.SecretKey, p.config.Model, "spark-v3.5", req)
}
func (p *SparkProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error) {
	return defaultChatStream(ctx, p, req)
}
func (p *SparkProvider) GetName() string  { return "spark" }
func (p *SparkProvider) GetModel() string { return p.config.Model }
func (p *SparkProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"spark-v3.5", "spark-v3", "spark-v2", "spark-lite"}, nil
}
func (p *SparkProvider) SupportsTools() bool  { return true }
func (p *SparkProvider) SupportsVision() bool { return false }

// ======================== 辅助函数 ========================

// openAICompatibleChat OpenAI 兼容的聊天请求
func openAICompatibleChat(ctx context.Context, client *http.Client, baseURL, apiKey, modelCfg, defaultModel string, req *ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = modelCfg
	}
	if model == "" {
		model = defaultModel
	}

	messages := make([]map[string]interface{}, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.ToolCallID != "" {
			messages[i]["tool_call_id"] = msg.ToolCallID
		}
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": messages,
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

	httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(httpReq)
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
	chatResp := &ChatResponse{
		ID:           result.ID,
		Model:        result.Model,
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
	}

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

// defaultChatStream 默认流式处理
func defaultChatStream(ctx context.Context, p Provider, req *ChatRequest) (<-chan *StreamChunk, error) {
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
