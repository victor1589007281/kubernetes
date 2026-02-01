// Package provider - LLM Provider 统一接口
package provider

import (
	"context"
	"fmt"
)

// Provider LLM Provider 统一接口
type Provider interface {
	// Chat 发送聊天请求
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream 流式聊天
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan *StreamChunk, error)

	// GetName 获取 Provider 名称
	GetName() string

	// GetModel 获取当前模型
	GetModel() string

	// ListModels 列出可用模型
	ListModels(ctx context.Context) ([]string, error)

	// SupportsTools 是否支持工具调用
	SupportsTools() bool

	// SupportsVision 是否支持视觉
	SupportsVision() bool
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Messages    []Message       `json:"messages"`
	Tools       []Tool          `json:"tools,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	TopP        float64         `json:"top_p,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Model       string          `json:"model,omitempty"` // 可覆盖默认模型
}

// ChatResponse 聊天响应
type ChatResponse struct {
	ID           string      `json:"id"`
	Model        string      `json:"model"`
	Content      string      `json:"content"`
	ToolCalls    []ToolCall  `json:"tool_calls,omitempty"`
	FinishReason string      `json:"finish_reason"`
	Usage        *Usage      `json:"usage,omitempty"`
}

// StreamChunk 流式响应块
type StreamChunk struct {
	ID           string     `json:"id"`
	Delta        string     `json:"delta"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Done         bool       `json:"done"`
	Error        error      `json:"error,omitempty"`
}

// Message 消息
type Message struct {
	Role       string     `json:"role"` // system, user, assistant, tool
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Tool 工具定义
type Tool struct {
	Type     string       `json:"type"` // function
	Function FunctionDef  `json:"function"`
}

// FunctionDef 函数定义
type FunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCall 工具调用
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // function
	Function FunctionCall `json:"function"`
}

// FunctionCall 函数调用
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Usage Token 使用统计
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ProviderType Provider 类型
type ProviderType string

const (
	// 国外大模型
	ProviderOpenAI      ProviderType = "openai"
	ProviderAzureOpenAI ProviderType = "azure_openai"
	ProviderClaude      ProviderType = "claude"
	ProviderGemini      ProviderType = "gemini"
	ProviderMistral     ProviderType = "mistral"
	ProviderCohere      ProviderType = "cohere"
	ProviderGroq        ProviderType = "groq"
	ProviderTogether    ProviderType = "together"
	ProviderOllama      ProviderType = "ollama"

	// 国内大模型
	ProviderQwen      ProviderType = "qwen"      // 通义千问 (阿里)
	ProviderErnie     ProviderType = "ernie"     // 文心一言 (百度)
	ProviderZhipu     ProviderType = "zhipu"     // 智谱 ChatGLM
	ProviderSpark     ProviderType = "spark"     // 讯飞星火
	ProviderMoonshot  ProviderType = "moonshot"  // 月之暗面 Kimi
	ProviderDeepSeek  ProviderType = "deepseek"  // 深度求索
	ProviderBaichuan  ProviderType = "baichuan"  // 百川
	ProviderMiniMax   ProviderType = "minimax"   // MiniMax
	ProviderYi        ProviderType = "yi"        // 零一万物
	ProviderDoubao    ProviderType = "doubao"    // 豆包 (字节)
	ProviderHunyuan   ProviderType = "hunyuan"   // 混元 (腾讯)
	ProviderSenseNova ProviderType = "sensenova" // 商汤日日新
)

// Config Provider 配置
type Config struct {
	Type        ProviderType `mapstructure:"type"`
	Model       string       `mapstructure:"model"`
	APIKey      string       `mapstructure:"apiKey"`
	SecretKey   string       `mapstructure:"secretKey"` // 部分国内模型需要
	BaseURL     string       `mapstructure:"baseURL"`
	MaxTokens   int          `mapstructure:"maxTokens"`
	Temperature float64      `mapstructure:"temperature"`
	Timeout     int          `mapstructure:"timeout"` // 秒

	// Azure 特定
	AzureDeployment string `mapstructure:"azureDeployment"`
	AzureAPIVersion string `mapstructure:"azureApiVersion"`

	// 讯飞特定
	SparkAppID  string `mapstructure:"sparkAppId"`
	SparkDomain string `mapstructure:"sparkDomain"`
}

// Factory Provider 工厂
type Factory struct {
	configs map[ProviderType]Config
}

// NewFactory 创建工厂
func NewFactory() *Factory {
	return &Factory{
		configs: make(map[ProviderType]Config),
	}
}

// Register 注册 Provider 配置
func (f *Factory) Register(cfg Config) {
	f.configs[cfg.Type] = cfg
}

// Create 创建 Provider
func (f *Factory) Create(providerType ProviderType) (Provider, error) {
	cfg, ok := f.configs[providerType]
	if !ok {
		return nil, fmt.Errorf("provider not registered: %s", providerType)
	}

	switch providerType {
	// 国外大模型
	case ProviderOpenAI:
		return NewOpenAIProvider(cfg)
	case ProviderAzureOpenAI:
		return NewAzureOpenAIProvider(cfg)
	case ProviderClaude:
		return NewClaudeProvider(cfg)
	case ProviderGemini:
		return NewGeminiProvider(cfg)
	case ProviderMistral:
		return NewMistralProvider(cfg)
	case ProviderCohere:
		return NewCohereProvider(cfg)
	case ProviderGroq:
		return NewGroqProvider(cfg)
	case ProviderTogether:
		return NewTogetherProvider(cfg)
	case ProviderOllama:
		return NewOllamaProvider(cfg)

	// 国内大模型
	case ProviderQwen:
		return NewQwenProvider(cfg)
	case ProviderErnie:
		return NewErnieProvider(cfg)
	case ProviderZhipu:
		return NewZhipuProvider(cfg)
	case ProviderSpark:
		return NewSparkProvider(cfg)
	case ProviderMoonshot:
		return NewMoonshotProvider(cfg)
	case ProviderDeepSeek:
		return NewDeepSeekProvider(cfg)
	case ProviderBaichuan:
		return NewBaichuanProvider(cfg)
	case ProviderMiniMax:
		return NewMiniMaxProvider(cfg)
	case ProviderYi:
		return NewYiProvider(cfg)
	case ProviderDoubao:
		return NewDoubaoProvider(cfg)
	case ProviderHunyuan:
		return NewHunyuanProvider(cfg)
	case ProviderSenseNova:
		return NewSenseNovaProvider(cfg)

	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerType)
	}
}

// GetDefaultModels 获取各 Provider 默认模型
func GetDefaultModels() map[ProviderType]string {
	return map[ProviderType]string{
		// 国外
		ProviderOpenAI:      "gpt-4-turbo-preview",
		ProviderAzureOpenAI: "gpt-4",
		ProviderClaude:      "claude-3-opus-20240229",
		ProviderGemini:      "gemini-pro",
		ProviderMistral:     "mistral-large-latest",
		ProviderCohere:      "command-r-plus",
		ProviderGroq:        "mixtral-8x7b-32768",
		ProviderTogether:    "mistralai/Mixtral-8x7B-Instruct-v0.1",
		ProviderOllama:      "qwen2:7b",

		// 国内
		ProviderQwen:      "qwen-max",
		ProviderErnie:     "ernie-4.0-8k",
		ProviderZhipu:     "glm-4",
		ProviderSpark:     "spark-v3.5",
		ProviderMoonshot:  "moonshot-v1-128k",
		ProviderDeepSeek:  "deepseek-chat",
		ProviderBaichuan:  "Baichuan4",
		ProviderMiniMax:   "abab6.5-chat",
		ProviderYi:        "yi-large",
		ProviderDoubao:    "doubao-pro-32k",
		ProviderHunyuan:   "hunyuan-pro",
		ProviderSenseNova: "SenseChat-5",
	}
}
