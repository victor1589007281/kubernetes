// Package analyzer - 受害者分析器
package analyzer

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/config"
	"github.com/node-io-manager/pkg/profile"
)

// Severity 严重程度
type Severity int

const (
	SeverityLow Severity = iota
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// VictimResult 受害者分析结果
type VictimResult struct {
	PodUID    string
	PodName   string
	Namespace string

	// 受害者评分 (0-100)
	Score     float64
	Severity  Severity

	// 受害原因
	Reasons   []VictimReason

	// 相关的噪声邻居
	Aggressors []AggressorInfo

	// 分析时间
	AnalyzedAt time.Time
}

// VictimReason 受害原因
type VictimReason struct {
	Type        string  // latency_increase, iops_drop, throttled
	Description string
	Contribution float64 // 贡献度 (0-1)
}

// AggressorInfo 攻击者信息
type AggressorInfo struct {
	PodUID      string
	PodName     string
	Namespace   string
	Correlation float64 // 相关性系数
	IOPSShare   float64 // IOPS 占比
	BPSShare    float64 // 带宽占比
}

// VictimAnalyzer 受害者分析器
type VictimAnalyzer struct {
	config config.AnalyzerConfig

	// 当前受害者列表
	victims   []*VictimResult
	victimsMu sync.RWMutex

	// 历史数据用于 Z-Score 计算
	baselineData map[string]*baselineStats
	baselineMu   sync.RWMutex
}

// baselineStats 基线统计
type baselineStats struct {
	PodUID string

	// IOPS 基线
	MeanIOPS   float64
	StdDevIOPS float64

	// 延迟基线
	MeanLatency   float64
	StdDevLatency float64

	// 样本数
	SampleCount int
}

// NewVictimAnalyzer 创建受害者分析器
func NewVictimAnalyzer(cfg config.AnalyzerConfig) *VictimAnalyzer {
	return &VictimAnalyzer{
		config:       cfg,
		victims:      make([]*VictimResult, 0),
		baselineData: make(map[string]*baselineStats),
	}
}

// Analyze 分析受害者
func (a *VictimAnalyzer) Analyze(profiles map[string]*profile.IOProfile) []*VictimResult {
	if len(profiles) == 0 {
		return nil
	}

	// 更新基线
	a.updateBaseline(profiles)

	// 识别受害者
	victims := a.identifyVictims(profiles)

	// 识别攻击者
	a.identifyAggressors(victims, profiles)

	// 排序
	sort.Slice(victims, func(i, j int) bool {
		return victims[i].Score > victims[j].Score
	})

	// 保存结果
	a.victimsMu.Lock()
	a.victims = victims
	a.victimsMu.Unlock()

	return victims
}

// updateBaseline 更新基线数据
func (a *VictimAnalyzer) updateBaseline(profiles map[string]*profile.IOProfile) {
	a.baselineMu.Lock()
	defer a.baselineMu.Unlock()

	for podUID, p := range profiles {
		baseline, exists := a.baselineData[podUID]
		if !exists {
			baseline = &baselineStats{PodUID: podUID}
			a.baselineData[podUID] = baseline
		}

		// 增量更新基线 (Welford's online algorithm)
		baseline.SampleCount++
		n := float64(baseline.SampleCount)

		// 更新 IOPS 基线
		delta := p.AvgIOPS - baseline.MeanIOPS
		baseline.MeanIOPS += delta / n
		delta2 := p.AvgIOPS - baseline.MeanIOPS
		baseline.StdDevIOPS = math.Sqrt((math.Pow(baseline.StdDevIOPS, 2)*(n-1) + delta*delta2) / n)
	}
}

// identifyVictims 识别受害者
func (a *VictimAnalyzer) identifyVictims(profiles map[string]*profile.IOProfile) []*VictimResult {
	victims := make([]*VictimResult, 0)

	a.baselineMu.RLock()
	defer a.baselineMu.RUnlock()

	for podUID, p := range profiles {
		baseline := a.baselineData[podUID]
		if baseline == nil || baseline.SampleCount < 10 {
			continue // 基线数据不足
		}

		victim := &VictimResult{
			PodUID:     podUID,
			PodName:    p.PodName,
			Namespace:  p.Namespace,
			AnalyzedAt: time.Now(),
			Reasons:    make([]VictimReason, 0),
		}

		score := 0.0

		// Z-Score 异常检测: IOPS 下降
		if baseline.StdDevIOPS > 0 {
			zScore := (baseline.MeanIOPS - p.AvgIOPS) / baseline.StdDevIOPS
			if zScore > a.config.ZScoreThreshold {
				contribution := math.Min(zScore/5, 1.0) // 归一化
				score += contribution * 40

				victim.Reasons = append(victim.Reasons, VictimReason{
					Type:         "iops_drop",
					Description:  "IOPS significantly below baseline",
					Contribution: contribution,
				})
			}
		}

		// 波动性异常高
		if p.Volatility > 0.8 {
			contribution := (p.Volatility - 0.8) / 0.2
			score += contribution * 20

			victim.Reasons = append(victim.Reasons, VictimReason{
				Type:         "high_volatility",
				Description:  "IO performance highly unstable",
				Contribution: contribution,
			})
		}

		// 突发性异常高（说明被其他 Pod 影响）
		if p.BurstScore > 2.0 {
			contribution := math.Min((p.BurstScore-2.0)/3.0, 1.0)
			score += contribution * 20

			victim.Reasons = append(victim.Reasons, VictimReason{
				Type:         "burst_impact",
				Description:  "Experiencing IO bursts from neighbors",
				Contribution: contribution,
			})
		}

		// 下降趋势
		if p.Trend < -0.5 {
			contribution := math.Abs(p.Trend)
			score += contribution * 20

			victim.Reasons = append(victim.Reasons, VictimReason{
				Type:         "declining_trend",
				Description:  "IO performance declining over time",
				Contribution: contribution,
			})
		}

		// 如果有受害特征
		if len(victim.Reasons) > 0 && score >= a.config.VictimScoreThreshold*100 {
			victim.Score = math.Min(score, 100)
			victim.Severity = a.scoreToseverity(victim.Score)
			victims = append(victims, victim)
		}
	}

	return victims
}

// identifyAggressors 识别攻击者（噪声邻居）
func (a *VictimAnalyzer) identifyAggressors(victims []*VictimResult, profiles map[string]*profile.IOProfile) {
	// 找出高 IO 占比的 Pod 作为潜在攻击者
	var totalIOPS, totalBPS float64
	for _, p := range profiles {
		totalIOPS += p.AvgIOPS
		totalBPS += p.AvgBPS
	}

	for _, victim := range victims {
		victim.Aggressors = make([]AggressorInfo, 0)

		for podUID, p := range profiles {
			if podUID == victim.PodUID {
				continue
			}

			iopsShare := 0.0
			bpsShare := 0.0
			if totalIOPS > 0 {
				iopsShare = p.IOPSScore
			}
			if totalBPS > 0 {
				bpsShare = p.BPSScore
			}

			// 高 IO 占比的 Pod 是潜在攻击者
			if iopsShare > 20 || bpsShare > 20 {
				// 计算相关性 (简化: 使用 IO 占比作为相关性指标)
				correlation := math.Max(iopsShare, bpsShare) / 100

				victim.Aggressors = append(victim.Aggressors, AggressorInfo{
					PodUID:      podUID,
					PodName:     p.PodName,
					Namespace:   p.Namespace,
					Correlation: correlation,
					IOPSShare:   iopsShare,
					BPSShare:    bpsShare,
				})
			}
		}

		// 按相关性排序
		sort.Slice(victim.Aggressors, func(i, j int) bool {
			return victim.Aggressors[i].Correlation > victim.Aggressors[j].Correlation
		})

		// 只保留前 5 个
		if len(victim.Aggressors) > 5 {
			victim.Aggressors = victim.Aggressors[:5]
		}
	}
}

// scoreToseverity 评分转严重程度
func (a *VictimAnalyzer) scoreToseverity(score float64) Severity {
	switch {
	case score >= 80:
		return SeverityCritical
	case score >= 60:
		return SeverityHigh
	case score >= 40:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// GetVictims 获取受害者列表
func (a *VictimAnalyzer) GetVictims() []*VictimResult {
	a.victimsMu.RLock()
	defer a.victimsMu.RUnlock()

	result := make([]*VictimResult, len(a.victims))
	copy(result, a.victims)
	return result
}

// GetVictimByPod 获取指定 Pod 的受害者信息
func (a *VictimAnalyzer) GetVictimByPod(namespace, name string) *VictimResult {
	a.victimsMu.RLock()
	defer a.victimsMu.RUnlock()

	for _, v := range a.victims {
		if v.Namespace == namespace && v.PodName == name {
			return v
		}
	}
	return nil
}

// CalculateCorrelation 计算两个 Pod 之间的 IO 相关性
func CalculateCorrelation(p1, p2 *profile.IOProfile) float64 {
	// 使用画像的指纹相似度作为相关性的近似
	// 实际实现中应该使用时间序列数据计算皮尔逊相关系数
	return profile.CompareProfiles(p1, p2)
}
