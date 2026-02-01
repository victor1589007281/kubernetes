// Package config - 配置管理
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

// Config 主配置结构
type Config struct {
	// 基础配置
	NodeName string `mapstructure:"nodeName"`
	LogLevel string `mapstructure:"logLevel"`

	// 各模块配置
	Collector   CollectorConfig   `mapstructure:"collector"`
	Metrics     MetricsConfig     `mapstructure:"metrics"`
	API         APIConfig         `mapstructure:"api"`
	Profile     ProfileConfig     `mapstructure:"profile"`
	Analyzer    AnalyzerConfig    `mapstructure:"analyzer"`
	Scoring     ScoringConfig     `mapstructure:"scoring"`
	Queue       QueueConfig       `mapstructure:"queue"`
	Observation ObservationConfig `mapstructure:"observation"`
	Agent       AgentConfig       `mapstructure:"agent"`
	Toolbox     ToolboxConfig     `mapstructure:"toolbox"`
	Business    BusinessConfig    `mapstructure:"business"`
}

// CollectorConfig 数据采集器配置
type CollectorConfig struct {
	IntervalSeconds int      `mapstructure:"intervalSeconds"`
	Devices         []string `mapstructure:"devices"`         // 监控的磁盘设备
	EnableBlktrace  bool     `mapstructure:"enableBlktrace"`  // 是否启用 blktrace
	EnableEBPF      bool     `mapstructure:"enableEBPF"`      // 是否启用 eBPF
	ProcPath        string   `mapstructure:"procPath"`        // /proc 路径
	SysPath         string   `mapstructure:"sysPath"`         // /sys 路径
	CgroupPath      string   `mapstructure:"cgroupPath"`      // cgroup 路径
}

// MetricsConfig Prometheus metrics 配置
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Port    int    `mapstructure:"port"`
	Path    string `mapstructure:"path"`
}

// APIConfig REST API 配置
type APIConfig struct {
	Port         int    `mapstructure:"port"`
	ReadTimeout  int    `mapstructure:"readTimeout"`
	WriteTimeout int    `mapstructure:"writeTimeout"`
	EnableCORS   bool   `mapstructure:"enableCORS"`
	AuthToken    string `mapstructure:"authToken"`
}

// ProfileConfig IO 画像引擎配置
type ProfileConfig struct {
	IntervalSeconds   int     `mapstructure:"intervalSeconds"`
	WindowSize        int     `mapstructure:"windowSize"`        // 滑动窗口大小
	RetentionHours    int     `mapstructure:"retentionHours"`    // 数据保留时间
	SequentialIOThreshold float64 `mapstructure:"sequentialIOThreshold"` // 顺序IO判定阈值
}

// AnalyzerConfig 分析器配置
type AnalyzerConfig struct {
	ZScoreThreshold     float64 `mapstructure:"zScoreThreshold"`     // Z-Score 异常阈值
	CorrelationMinimum  float64 `mapstructure:"correlationMinimum"`  // 相关性最小阈值
	VictimScoreThreshold float64 `mapstructure:"victimScoreThreshold"` // 受害者评分阈值
	EnableML            bool    `mapstructure:"enableML"`            // 是否启用 ML
}

// ScoringConfig 评分引擎配置
type ScoringConfig struct {
	IntervalSeconds int `mapstructure:"intervalSeconds"`

	// 基础权重
	Weights WeightsConfig `mapstructure:"weights"`

	// 复犯模型参数
	RecidivismBaseRate    float64 `mapstructure:"recidivismBaseRate"`
	RecidivismDecayHours  int     `mapstructure:"recidivismDecayHours"`

	// 历史数据库
	HistoryDBPath string `mapstructure:"historyDBPath"`
}

// WeightsConfig 评分权重配置
type WeightsConfig struct {
	BusinessImportance float64 `mapstructure:"businessImportance"`
	HistoryBehavior    float64 `mapstructure:"historyBehavior"`
	ActionEffect       float64 `mapstructure:"actionEffect"`
	CurrentImpact      float64 `mapstructure:"currentImpact"`
}

// QueueConfig 决策队列配置
type QueueConfig struct {
	MaxPendingItems    int `mapstructure:"maxPendingItems"`
	ProcessIntervalSec int `mapstructure:"processIntervalSec"`
	ScoreDecayMinutes  int `mapstructure:"scoreDecayMinutes"`
}

// ObservationConfig 观察期配置
type ObservationConfig struct {
	DefaultDuration time.Duration `mapstructure:"defaultDuration"`
	MinDuration     time.Duration `mapstructure:"minDuration"`
	MaxDuration     time.Duration `mapstructure:"maxDuration"`

	// 成功判定
	SuccessCriteria []CriteriaConfig `mapstructure:"successCriteria"`

	// 升级策略
	Escalation []EscalationConfig `mapstructure:"escalation"`
}

// CriteriaConfig 判定条件配置
type CriteriaConfig struct {
	Metric    string        `mapstructure:"metric"`
	Operator  string        `mapstructure:"operator"`
	Threshold float64       `mapstructure:"threshold"`
	Duration  time.Duration `mapstructure:"duration"`
}

// EscalationConfig 升级策略配置
type EscalationConfig struct {
	Level           int           `mapstructure:"level"`
	Action          string        `mapstructure:"action"`
	NextObservation time.Duration `mapstructure:"nextObservation"`
}

// AgentConfig AI Agent 配置
type AgentConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Provider string `mapstructure:"provider"` // openai, claude, ollama

	// 各 Provider 配置
	Providers ProvidersConfig `mapstructure:"providers"`

	// 触发条件
	Triggers []TriggerConfig `mapstructure:"triggers"`

	// Agent 行为配置
	MaxIterations   int           `mapstructure:"maxIterations"`
	Timeout         time.Duration `mapstructure:"timeout"`
	EnableSubAgents bool          `mapstructure:"enableSubAgents"`
}

// ProvidersConfig LLM Provider 配置
type ProvidersConfig struct {
	OpenAI OpenAIConfig `mapstructure:"openai"`
	Claude ClaudeConfig `mapstructure:"claude"`
	Ollama OllamaConfig `mapstructure:"ollama"`
}

// OpenAIConfig OpenAI 配置
type OpenAIConfig struct {
	Model       string `mapstructure:"model"`
	APIKey      string `mapstructure:"apiKey"`
	BaseURL     string `mapstructure:"baseURL"`
	MaxTokens   int    `mapstructure:"maxTokens"`
	Temperature float64 `mapstructure:"temperature"`
}

// ClaudeConfig Claude 配置
type ClaudeConfig struct {
	Model       string `mapstructure:"model"`
	APIKey      string `mapstructure:"apiKey"`
	MaxTokens   int    `mapstructure:"maxTokens"`
	Temperature float64 `mapstructure:"temperature"`
}

// OllamaConfig Ollama 配置
type OllamaConfig struct {
	Model    string `mapstructure:"model"`
	Endpoint string `mapstructure:"endpoint"`
}

// TriggerConfig Agent 触发条件
type TriggerConfig struct {
	Type      string `mapstructure:"type"`      // threshold, prediction, manual
	Condition string `mapstructure:"condition"` // 条件表达式
}

// ToolboxConfig 工具箱配置
type ToolboxConfig struct {
	EnableIOLimit     bool `mapstructure:"enableIOLimit"`
	EnableSchedule    bool `mapstructure:"enableSchedule"`
	EnableAlert       bool `mapstructure:"enableAlert"`
	DryRun            bool `mapstructure:"dryRun"`

	// IO 限制配置
	IOLimit IOLimitConfig `mapstructure:"ioLimit"`

	// 告警配置
	Alert AlertConfig `mapstructure:"alert"`
}

// IOLimitConfig IO 限制配置
type IOLimitConfig struct {
	DefaultReadIOPS  int64 `mapstructure:"defaultReadIOPS"`
	DefaultWriteIOPS int64 `mapstructure:"defaultWriteIOPS"`
	DefaultReadBPS   int64 `mapstructure:"defaultReadBPS"`
	DefaultWriteBPS  int64 `mapstructure:"defaultWriteBPS"`
	MinIOPS          int64 `mapstructure:"minIOPS"`
}

// AlertConfig 告警配置
type AlertConfig struct {
	WebhookURL  string `mapstructure:"webhookURL"`
	SlackURL    string `mapstructure:"slackURL"`
	EnableEmail bool   `mapstructure:"enableEmail"`
}

// BusinessConfig 业务优先级配置
type BusinessConfig struct {
	ConfigPath string `mapstructure:"configPath"` // 业务优先级配置文件路径
}

// Load 加载配置
func Load(path string) (*Config, error) {
	v := viper.New()

	// 设置默认值
	setDefaults(v)

	// 读取配置文件
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(*os.PathError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// 配置文件不存在，使用默认值
	}

	// 环境变量覆盖
	v.AutomaticEnv()
	v.SetEnvPrefix("NIO")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 从环境变量读取敏感信息
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		cfg.Agent.Providers.OpenAI.APIKey = apiKey
	}
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		cfg.Agent.Providers.Claude.APIKey = apiKey
	}

	// 获取节点名称
	if cfg.NodeName == "" {
		cfg.NodeName = os.Getenv("NODE_NAME")
		if cfg.NodeName == "" {
			hostname, _ := os.Hostname()
			cfg.NodeName = hostname
		}
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	// 基础配置
	v.SetDefault("logLevel", "info")

	// 采集器配置
	v.SetDefault("collector.intervalSeconds", 5)
	v.SetDefault("collector.procPath", "/proc")
	v.SetDefault("collector.sysPath", "/sys")
	v.SetDefault("collector.cgroupPath", "/sys/fs/cgroup")
	v.SetDefault("collector.enableBlktrace", false)
	v.SetDefault("collector.enableEBPF", false)

	// Metrics 配置
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.port", 9100)
	v.SetDefault("metrics.path", "/metrics")

	// API 配置
	v.SetDefault("api.port", 8080)
	v.SetDefault("api.readTimeout", 30)
	v.SetDefault("api.writeTimeout", 30)
	v.SetDefault("api.enableCORS", true)

	// 画像引擎配置
	v.SetDefault("profile.intervalSeconds", 10)
	v.SetDefault("profile.windowSize", 60)
	v.SetDefault("profile.retentionHours", 24)
	v.SetDefault("profile.sequentialIOThreshold", 0.7)

	// 分析器配置
	v.SetDefault("analyzer.zScoreThreshold", 2.0)
	v.SetDefault("analyzer.correlationMinimum", 0.6)
	v.SetDefault("analyzer.victimScoreThreshold", 0.5)
	v.SetDefault("analyzer.enableML", false)

	// 评分配置
	v.SetDefault("scoring.intervalSeconds", 10)
	v.SetDefault("scoring.weights.businessImportance", 0.3)
	v.SetDefault("scoring.weights.historyBehavior", 0.2)
	v.SetDefault("scoring.weights.actionEffect", 0.3)
	v.SetDefault("scoring.weights.currentImpact", 0.2)
	v.SetDefault("scoring.recidivismBaseRate", 0.1)
	v.SetDefault("scoring.recidivismDecayHours", 24)
	v.SetDefault("scoring.historyDBPath", "/var/lib/node-io-manager/history.db")

	// 队列配置
	v.SetDefault("queue.maxPendingItems", 100)
	v.SetDefault("queue.processIntervalSec", 5)
	v.SetDefault("queue.scoreDecayMinutes", 10)

	// 观察期配置
	v.SetDefault("observation.defaultDuration", "5m")
	v.SetDefault("observation.minDuration", "1m")
	v.SetDefault("observation.maxDuration", "30m")

	// Agent 配置
	v.SetDefault("agent.enabled", false)
	v.SetDefault("agent.provider", "openai")
	v.SetDefault("agent.maxIterations", 10)
	v.SetDefault("agent.timeout", "5m")
	v.SetDefault("agent.enableSubAgents", true)
	v.SetDefault("agent.providers.openai.model", "gpt-4")
	v.SetDefault("agent.providers.openai.maxTokens", 4096)
	v.SetDefault("agent.providers.openai.temperature", 0.7)
	v.SetDefault("agent.providers.claude.model", "claude-3-opus-20240229")
	v.SetDefault("agent.providers.claude.maxTokens", 4096)
	v.SetDefault("agent.providers.ollama.model", "qwen2:7b")
	v.SetDefault("agent.providers.ollama.endpoint", "http://localhost:11434")

	// 工具箱配置
	v.SetDefault("toolbox.enableIOLimit", true)
	v.SetDefault("toolbox.enableSchedule", true)
	v.SetDefault("toolbox.enableAlert", true)
	v.SetDefault("toolbox.dryRun", false)
	v.SetDefault("toolbox.ioLimit.defaultReadIOPS", 5000)
	v.SetDefault("toolbox.ioLimit.defaultWriteIOPS", 3000)
	v.SetDefault("toolbox.ioLimit.minIOPS", 100)
}
