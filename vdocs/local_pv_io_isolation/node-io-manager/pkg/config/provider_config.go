// Package config - Provider 配置
package config

import "github.com/node-io-manager/pkg/agent/provider"

// AllProviderConfig 所有 Provider 配置
type AllProviderConfig struct {
	// 当前使用的 Provider
	Default string `mapstructure:"default"`

	// 国外大模型
	OpenAI      ProviderSettings `mapstructure:"openai"`
	AzureOpenAI ProviderSettings `mapstructure:"azureOpenai"`
	Claude      ProviderSettings `mapstructure:"claude"`
	Gemini      ProviderSettings `mapstructure:"gemini"`
	Mistral     ProviderSettings `mapstructure:"mistral"`
	Cohere      ProviderSettings `mapstructure:"cohere"`
	Groq        ProviderSettings `mapstructure:"groq"`
	Together    ProviderSettings `mapstructure:"together"`
	Ollama      ProviderSettings `mapstructure:"ollama"`

	// 国内大模型
	Qwen      ProviderSettings `mapstructure:"qwen"`      // 通义千问
	Ernie     ProviderSettings `mapstructure:"ernie"`     // 文心一言
	Zhipu     ProviderSettings `mapstructure:"zhipu"`     // 智谱 ChatGLM
	Spark     ProviderSettings `mapstructure:"spark"`     // 讯飞星火
	Moonshot  ProviderSettings `mapstructure:"moonshot"`  // 月之暗面
	DeepSeek  ProviderSettings `mapstructure:"deepseek"`  // 深度求索
	Baichuan  ProviderSettings `mapstructure:"baichuan"`  // 百川
	MiniMax   ProviderSettings `mapstructure:"minimax"`   // MiniMax
	Yi        ProviderSettings `mapstructure:"yi"`        // 零一万物
	Doubao    ProviderSettings `mapstructure:"doubao"`    // 豆包
	Hunyuan   ProviderSettings `mapstructure:"hunyuan"`   // 混元
	SenseNova ProviderSettings `mapstructure:"sensenova"` // 商汤日日新
}

// ProviderSettings 单个 Provider 设置
type ProviderSettings struct {
	Enabled     bool    `mapstructure:"enabled"`
	Model       string  `mapstructure:"model"`
	APIKey      string  `mapstructure:"apiKey"`
	SecretKey   string  `mapstructure:"secretKey"` // 部分国内模型需要
	BaseURL     string  `mapstructure:"baseURL"`
	MaxTokens   int     `mapstructure:"maxTokens"`
	Temperature float64 `mapstructure:"temperature"`
	Timeout     int     `mapstructure:"timeout"`

	// Azure 特定
	AzureDeployment string `mapstructure:"azureDeployment"`
	AzureAPIVersion string `mapstructure:"azureApiVersion"`
}

// ToProviderConfig 转换为 Provider 配置
func (s *ProviderSettings) ToProviderConfig(providerType provider.ProviderType) provider.Config {
	return provider.Config{
		Type:            providerType,
		Model:           s.Model,
		APIKey:          s.APIKey,
		SecretKey:       s.SecretKey,
		BaseURL:         s.BaseURL,
		MaxTokens:       s.MaxTokens,
		Temperature:     s.Temperature,
		Timeout:         s.Timeout,
		AzureDeployment: s.AzureDeployment,
		AzureAPIVersion: s.AzureAPIVersion,
	}
}

// GetAllSupportedProviders 获取所有支持的 Provider 列表
func GetAllSupportedProviders() map[string]ProviderInfo {
	return map[string]ProviderInfo{
		// 国外大模型
		"openai": {
			Name:        "OpenAI",
			Description: "OpenAI GPT 系列模型",
			Region:      "国外",
			Models:      []string{"gpt-4-turbo", "gpt-4", "gpt-3.5-turbo"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"azure_openai": {
			Name:        "Azure OpenAI",
			Description: "Microsoft Azure 托管的 OpenAI 服务",
			Region:      "国外",
			Models:      []string{"gpt-4", "gpt-35-turbo"},
			Features:    []string{"chat", "tools"},
		},
		"claude": {
			Name:        "Anthropic Claude",
			Description: "Anthropic Claude 系列模型",
			Region:      "国外",
			Models:      []string{"claude-3-opus", "claude-3-sonnet", "claude-3-haiku"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"gemini": {
			Name:        "Google Gemini",
			Description: "Google Gemini 系列模型",
			Region:      "国外",
			Models:      []string{"gemini-pro", "gemini-1.5-pro", "gemini-1.5-flash"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"mistral": {
			Name:        "Mistral AI",
			Description: "Mistral AI 开源模型",
			Region:      "国外",
			Models:      []string{"mistral-large", "mistral-medium", "codestral"},
			Features:    []string{"chat", "tools"},
		},
		"cohere": {
			Name:        "Cohere",
			Description: "Cohere Command 系列模型",
			Region:      "国外",
			Models:      []string{"command-r-plus", "command-r"},
			Features:    []string{"chat", "tools"},
		},
		"groq": {
			Name:        "Groq",
			Description: "Groq 高速推理平台",
			Region:      "国外",
			Models:      []string{"llama-3.1-405b", "mixtral-8x7b"},
			Features:    []string{"chat", "tools"},
		},
		"together": {
			Name:        "Together AI",
			Description: "Together AI 开源模型托管平台",
			Region:      "国外",
			Models:      []string{"Mixtral-8x7B", "Llama-3-70b"},
			Features:    []string{"chat", "tools"},
		},
		"ollama": {
			Name:        "Ollama",
			Description: "本地运行的开源模型",
			Region:      "本地",
			Models:      []string{"qwen2", "llama3", "mistral"},
			Features:    []string{"chat", "vision"},
		},

		// 国内大模型
		"qwen": {
			Name:        "通义千问",
			Description: "阿里云通义千问大模型",
			Region:      "国内",
			Models:      []string{"qwen-max", "qwen-plus", "qwen-turbo"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"ernie": {
			Name:        "文心一言",
			Description: "百度文心一言大模型",
			Region:      "国内",
			Models:      []string{"ernie-4.0", "ernie-3.5", "ernie-speed"},
			Features:    []string{"chat", "tools"},
		},
		"zhipu": {
			Name:        "智谱 ChatGLM",
			Description: "智谱 AI ChatGLM 系列模型",
			Region:      "国内",
			Models:      []string{"glm-4", "glm-4-plus", "glm-3-turbo"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"spark": {
			Name:        "讯飞星火",
			Description: "科大讯飞星火大模型",
			Region:      "国内",
			Models:      []string{"spark-v3.5", "spark-v3", "spark-lite"},
			Features:    []string{"chat", "tools"},
		},
		"moonshot": {
			Name:        "月之暗面 Kimi",
			Description: "月之暗面 Moonshot 大模型",
			Region:      "国内",
			Models:      []string{"moonshot-v1-128k", "moonshot-v1-32k"},
			Features:    []string{"chat", "tools"},
		},
		"deepseek": {
			Name:        "深度求索",
			Description: "DeepSeek 大模型",
			Region:      "国内",
			Models:      []string{"deepseek-chat", "deepseek-coder", "deepseek-reasoner"},
			Features:    []string{"chat", "tools"},
		},
		"baichuan": {
			Name:        "百川",
			Description: "百川智能大模型",
			Region:      "国内",
			Models:      []string{"Baichuan4", "Baichuan3-Turbo"},
			Features:    []string{"chat", "tools"},
		},
		"minimax": {
			Name:        "MiniMax",
			Description: "MiniMax 大模型",
			Region:      "国内",
			Models:      []string{"abab6.5-chat", "abab6-chat"},
			Features:    []string{"chat", "tools"},
		},
		"yi": {
			Name:        "零一万物",
			Description: "零一万物 Yi 系列模型",
			Region:      "国内",
			Models:      []string{"yi-large", "yi-medium", "yi-spark"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"doubao": {
			Name:        "豆包",
			Description: "字节跳动豆包大模型",
			Region:      "国内",
			Models:      []string{"doubao-pro-32k", "doubao-lite-32k"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"hunyuan": {
			Name:        "混元",
			Description: "腾讯混元大模型",
			Region:      "国内",
			Models:      []string{"hunyuan-pro", "hunyuan-standard"},
			Features:    []string{"chat", "tools", "vision"},
		},
		"sensenova": {
			Name:        "商汤日日新",
			Description: "商汤科技日日新大模型",
			Region:      "国内",
			Models:      []string{"SenseChat-5", "SenseChat-Turbo"},
			Features:    []string{"chat", "tools", "vision"},
		},
	}
}

// ProviderInfo Provider 信息
type ProviderInfo struct {
	Name        string   // 名称
	Description string   // 描述
	Region      string   // 地区 (国内/国外/本地)
	Models      []string // 支持的模型
	Features    []string // 支持的特性 (chat, tools, vision)
}
