// Package core - LLM Provider 接口
package core

import (
	"context"
	"fmt"

	"github.com/node-io-manager/pkg/config"
)

// Provider LLM Provider 接口
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	GetModel() string
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Messages []ChatMessage
	Tools    []ToolDefinition
}

// ChatMessage 聊天消息
type ChatMessage struct {
	Role       string        // user, assistant, system, tool
	Content    string
	ToolCalls  []LLMToolCall
	ToolCallID string // for tool messages
}

// LLMToolCall LLM 工具调用
type LLMToolCall struct {
	ID       string
	Type     string // function
	Function FunctionCall
}

// FunctionCall 函数调用
type FunctionCall struct {
	Name      string
	Arguments map[string]interface{}
}

// ChatResponse 聊天响应
type ChatResponse struct {
	Content      string
	ToolCalls    []LLMToolCall
	FinishReason string
}

// ToolDefinition 工具定义（用于 LLM）
type ToolDefinition struct {
	Type     string           `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition 函数定义
type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// NewProvider 创建 Provider
func NewProvider(cfg config.AgentConfig) (Provider, error) {
	switch cfg.Provider {
	case "openai":
		return NewOpenAIProvider(cfg.Providers.OpenAI)
	case "claude":
		return NewClaudeProvider(cfg.Providers.Claude)
	case "ollama":
		return NewOllamaProvider(cfg.Providers.Ollama)
	default:
		// 默认使用 OpenAI
		return NewOpenAIProvider(cfg.Providers.OpenAI)
	}
}

// OpenAIProvider OpenAI Provider
type OpenAIProvider struct {
	config config.OpenAIConfig
}

// NewOpenAIProvider 创建 OpenAI Provider
func NewOpenAIProvider(cfg config.OpenAIConfig) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	return &OpenAIProvider{config: cfg}, nil
}

// Chat 聊天
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// TODO: 实现 OpenAI API 调用
	// 使用 github.com/sashabaranov/go-openai 库

	// 暂时返回模拟响应
	return &ChatResponse{
		Content:      "这是一个模拟响应。实际实现需要调用 OpenAI API。",
		FinishReason: "stop",
	}, nil
}

// GetModel 获取模型
func (p *OpenAIProvider) GetModel() string {
	return p.config.Model
}

// ClaudeProvider Claude Provider
type ClaudeProvider struct {
	config config.ClaudeConfig
}

// NewClaudeProvider 创建 Claude Provider
func NewClaudeProvider(cfg config.ClaudeConfig) (*ClaudeProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Claude API key is required")
	}
	return &ClaudeProvider{config: cfg}, nil
}

// Chat 聊天
func (p *ClaudeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// TODO: 实现 Claude API 调用

	return &ChatResponse{
		Content:      "这是一个模拟响应。实际实现需要调用 Claude API。",
		FinishReason: "stop",
	}, nil
}

// GetModel 获取模型
func (p *ClaudeProvider) GetModel() string {
	return p.config.Model
}

// OllamaProvider Ollama Provider
type OllamaProvider struct {
	config config.OllamaConfig
}

// NewOllamaProvider 创建 Ollama Provider
func NewOllamaProvider(cfg config.OllamaConfig) (*OllamaProvider, error) {
	return &OllamaProvider{config: cfg}, nil
}

// Chat 聊天
func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// TODO: 实现 Ollama API 调用

	return &ChatResponse{
		Content:      "这是一个模拟响应。实际实现需要调用 Ollama API。",
		FinishReason: "stop",
	}, nil
}

// GetModel 获取模型
func (p *OllamaProvider) GetModel() string {
	return p.config.Model
}
