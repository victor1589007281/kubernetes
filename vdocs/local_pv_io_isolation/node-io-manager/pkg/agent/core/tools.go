// Package core - 工具系统
package core

import (
	"context"
	"fmt"
	"sync"
)

// ToolParameter 工具参数
type ToolParameter struct {
	Type        string
	Description string
	Required    bool
	Default     interface{}
}

// Tool 工具
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]ToolParameter
	Execute     func(ctx context.Context, args map[string]interface{}) (interface{}, error)
}

// ToolRegistry 工具注册表
type ToolRegistry struct {
	tools map[string]*Tool
	mu    sync.RWMutex
}

// NewToolRegistry 创建工具注册表
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*Tool),
	}
}

// Register 注册工具
func (r *ToolRegistry) Register(tool *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = tool
}

// Get 获取工具
func (r *ToolRegistry) Get(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List 列出所有工具
func (r *ToolRegistry) List() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}

// GetToolDefinitions 获取工具定义（用于 LLM）
func (r *ToolRegistry) GetToolDefinitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	definitions := make([]ToolDefinition, 0, len(r.tools))

	for _, tool := range r.tools {
		// 构建 JSON Schema 格式的参数定义
		properties := make(map[string]interface{})
		required := make([]string, 0)

		for name, param := range tool.Parameters {
			properties[name] = map[string]interface{}{
				"type":        param.Type,
				"description": param.Description,
			}
			if param.Required {
				required = append(required, name)
			}
		}

		definitions = append(definitions, ToolDefinition{
			Type: "function",
			Function: FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": properties,
					"required":   required,
				},
			},
		})
	}

	return definitions
}

// ExecuteTool 执行工具
func (r *ToolRegistry) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (interface{}, error) {
	tool := r.Get(name)
	if tool == nil {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	if tool.Execute == nil {
		return nil, fmt.Errorf("tool %s has no execute function", name)
	}

	return tool.Execute(ctx, args)
}

// ValidateArgs 验证参数
func (r *ToolRegistry) ValidateArgs(name string, args map[string]interface{}) error {
	tool := r.Get(name)
	if tool == nil {
		return fmt.Errorf("tool not found: %s", name)
	}

	for paramName, param := range tool.Parameters {
		if param.Required {
			if _, ok := args[paramName]; !ok {
				return fmt.Errorf("missing required parameter: %s", paramName)
			}
		}
	}

	return nil
}
