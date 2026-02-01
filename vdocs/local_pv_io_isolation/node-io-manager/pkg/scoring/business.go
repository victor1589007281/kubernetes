// Package scoring - 业务优先级配置
package scoring

import (
	"regexp"
	"sync"
)

// ProtectionLevel 保护级别
type ProtectionLevel string

const (
	ProtectionCritical ProtectionLevel = "critical"
	ProtectionHigh     ProtectionLevel = "high"
	ProtectionMedium   ProtectionLevel = "medium"
	ProtectionLow      ProtectionLevel = "low"
)

// BusinessPriorityConfig 业务优先级配置
type BusinessPriorityConfig struct {
	// 命名空间优先级
	Namespaces map[string]NamespacePriority

	// 标签规则
	LabelRules []LabelRule

	// Pod 覆盖配置
	PodOverrides []PodOverride

	mu sync.RWMutex
}

// NamespacePriority 命名空间优先级
type NamespacePriority struct {
	Priority        int             // 0-100
	ProtectionLevel ProtectionLevel
}

// LabelRule 标签规则
type LabelRule struct {
	Selector        map[string]string // 标签选择器
	Priority        int
	ProtectionLevel ProtectionLevel
}

// PodOverride Pod 覆盖配置
type PodOverride struct {
	Namespace       string
	NamePattern     string // 支持通配符
	Priority        int
	ProtectionLevel ProtectionLevel
	NeverThrottle   bool
}

// NewBusinessPriorityConfig 创建默认配置
func NewBusinessPriorityConfig() *BusinessPriorityConfig {
	return &BusinessPriorityConfig{
		Namespaces: map[string]NamespacePriority{
			"kube-system": {Priority: 100, ProtectionLevel: ProtectionCritical},
			"production":  {Priority: 90, ProtectionLevel: ProtectionHigh},
			"staging":     {Priority: 50, ProtectionLevel: ProtectionMedium},
			"dev":         {Priority: 20, ProtectionLevel: ProtectionLow},
			"default":     {Priority: 30, ProtectionLevel: ProtectionLow},
		},
		LabelRules: []LabelRule{
			{
				Selector:        map[string]string{"app.kubernetes.io/tier": "database"},
				Priority:        90,
				ProtectionLevel: ProtectionHigh,
			},
			{
				Selector:        map[string]string{"app.kubernetes.io/tier": "cache"},
				Priority:        80,
				ProtectionLevel: ProtectionHigh,
			},
			{
				Selector:        map[string]string{"batch": "true"},
				Priority:        30,
				ProtectionLevel: ProtectionLow,
			},
		},
		PodOverrides: []PodOverride{},
	}
}

// GetPriority 获取 Pod 优先级
func (c *BusinessPriorityConfig) GetPriority(namespace, podName string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 1. 检查 Pod 覆盖配置
	for _, override := range c.PodOverrides {
		if override.Namespace == namespace || override.Namespace == "*" {
			matched, _ := regexp.MatchString(override.NamePattern, podName)
			if matched {
				return override.Priority
			}
		}
	}

	// 2. 检查命名空间优先级
	if nsPriority, ok := c.Namespaces[namespace]; ok {
		return nsPriority.Priority
	}

	// 3. 默认优先级
	return 50
}

// GetProtectionLevel 获取保护级别
func (c *BusinessPriorityConfig) GetProtectionLevel(namespace, podName string) ProtectionLevel {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 1. 检查 Pod 覆盖配置
	for _, override := range c.PodOverrides {
		if override.Namespace == namespace || override.Namespace == "*" {
			matched, _ := regexp.MatchString(override.NamePattern, podName)
			if matched {
				return override.ProtectionLevel
			}
		}
	}

	// 2. 检查命名空间
	if nsPriority, ok := c.Namespaces[namespace]; ok {
		return nsPriority.ProtectionLevel
	}

	return ProtectionLow
}

// IsNeverThrottle 检查是否禁止限流
func (c *BusinessPriorityConfig) IsNeverThrottle(namespace, podName string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 检查 Pod 覆盖配置
	for _, override := range c.PodOverrides {
		if override.Namespace == namespace || override.Namespace == "*" {
			matched, _ := regexp.MatchString(override.NamePattern, podName)
			if matched && override.NeverThrottle {
				return true
			}
		}
	}

	// Critical 级别默认不限流
	if nsPriority, ok := c.Namespaces[namespace]; ok {
		return nsPriority.ProtectionLevel == ProtectionCritical
	}

	return false
}

// UpdateNamespace 更新命名空间优先级
func (c *BusinessPriorityConfig) UpdateNamespace(namespace string, priority NamespacePriority) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Namespaces[namespace] = priority
}

// AddLabelRule 添加标签规则
func (c *BusinessPriorityConfig) AddLabelRule(rule LabelRule) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LabelRules = append(c.LabelRules, rule)
}

// AddPodOverride 添加 Pod 覆盖配置
func (c *BusinessPriorityConfig) AddPodOverride(override PodOverride) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PodOverrides = append(c.PodOverrides, override)
}

// MatchLabels 检查标签是否匹配
func (c *BusinessPriorityConfig) MatchLabels(labels map[string]string) (int, ProtectionLevel) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, rule := range c.LabelRules {
		matched := true
		for k, v := range rule.Selector {
			if labels[k] != v {
				matched = false
				break
			}
		}
		if matched {
			return rule.Priority, rule.ProtectionLevel
		}
	}

	return 50, ProtectionLow // 默认
}

// LoadFromYAML 从 YAML 加载配置（预留）
func (c *BusinessPriorityConfig) LoadFromYAML(path string) error {
	// TODO: 实现 YAML 配置加载
	return nil
}
