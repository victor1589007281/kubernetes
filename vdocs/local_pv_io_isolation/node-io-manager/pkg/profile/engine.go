// Package profile - IO 画像引擎
package profile

import (
	"math"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/collector"
	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// IOProfile Pod IO 画像
type IOProfile struct {
	PodUID    string
	PodName   string
	Namespace string

	// 画像时间范围
	StartTime time.Time
	EndTime   time.Time
	SampleCount int

	// IO 模式特征
	SequentialRatio float64 // 顺序 IO 比例 (0-1)
	RandomRatio     float64 // 随机 IO 比例 (0-1)
	ReadRatio       float64 // 读比例 (0-1)
	WriteRatio      float64 // 写比例 (0-1)

	// 负载特征
	AvgIOPS       float64
	MaxIOPS       float64
	MinIOPS       float64
	StdDevIOPS    float64

	AvgBPS        float64
	MaxBPS        float64
	MinBPS        float64
	StdDevBPS     float64

	AvgIOSize     float64 // 平均 IO 大小

	// 行为评分
	IOPSScore     float64 // IOPS 评分 (基于节点占比)
	BPSScore      float64 // 带宽评分
	LatencyScore  float64 // 延迟影响评分
	BurstScore    float64 // 突发性评分

	// 时间序列特征
	Periodicity   float64 // 周期性 (0-1)
	Volatility    float64 // 波动性 (0-1)
	Trend         float64 // 趋势 (-1 到 1, 负数下降，正数上升)

	// 指纹
	Fingerprint   IOFingerprint
}

// IOFingerprint IO 指纹
type IOFingerprint struct {
	// IOPS 分布直方图 (桶: 0-100, 100-1000, 1000-10000, 10000+)
	IOPSHistogram [4]float64

	// IO 大小分布 (桶: 4K, 8K, 16K, 32K, 64K, 128K+)
	IOSizeHistogram [6]float64

	// 时间分布 (24小时分桶)
	HourlyPattern [24]float64
}

// Engine IO 画像引擎
type Engine struct {
	config config.ProfileConfig

	// 当前画像
	profiles   map[string]*IOProfile
	profilesMu sync.RWMutex

	// 历史数据（用于计算统计量）
	history    map[string]*profileHistory
	historyMu  sync.RWMutex
}

// profileHistory Pod 历史数据
type profileHistory struct {
	PodUID    string
	PodName   string
	Namespace string

	// 滑动窗口数据
	Samples    []sample
	WindowSize int
}

type sample struct {
	Timestamp time.Time
	IOPS      float64
	BPS       float64
	ReadRatio float64
	IOSize    float64
}

// NewEngine 创建画像引擎
func NewEngine(cfg config.ProfileConfig) *Engine {
	return &Engine{
		config:   cfg,
		profiles: make(map[string]*IOProfile),
		history:  make(map[string]*profileHistory),
	}
}

// Update 更新画像
func (e *Engine) Update(data *collector.CollectedData) map[string]*IOProfile {
	if data == nil {
		return e.GetProfiles()
	}

	e.profilesMu.Lock()
	defer e.profilesMu.Unlock()

	e.historyMu.Lock()
	defer e.historyMu.Unlock()

	// 计算系统总量
	var totalIOPS, totalBPS float64
	for _, disk := range data.Disks {
		totalIOPS += disk.ReadIOPS + disk.WriteIOPS
		totalBPS += disk.ReadBytesPerSec + disk.WriteBytesPerSec
	}

	// 更新每个 Pod 的画像
	for podUID, podStats := range data.Pods {
		// 获取或创建历史记录
		hist, ok := e.history[podUID]
		if !ok {
			hist = &profileHistory{
				PodUID:     podUID,
				PodName:    podStats.PodName,
				Namespace:  podStats.Namespace,
				WindowSize: e.config.WindowSize,
				Samples:    make([]sample, 0, e.config.WindowSize),
			}
			e.history[podUID] = hist
		}

		// 添加样本
		iops := podStats.ReadIOPS + podStats.WriteIOPS
		bps := podStats.ReadBytesPerSec + podStats.WriteBytesPerSec
		readRatio := 0.0
		if iops > 0 {
			readRatio = podStats.ReadIOPS / iops
		}
		ioSize := 0.0
		if iops > 0 {
			ioSize = bps / iops
		}

		s := sample{
			Timestamp: data.Timestamp,
			IOPS:      iops,
			BPS:       bps,
			ReadRatio: readRatio,
			IOSize:    ioSize,
		}

		hist.Samples = append(hist.Samples, s)

		// 保持窗口大小
		if len(hist.Samples) > hist.WindowSize {
			hist.Samples = hist.Samples[len(hist.Samples)-hist.WindowSize:]
		}

		// 计算画像
		profile := e.computeProfile(hist, totalIOPS, totalBPS)
		e.profiles[podUID] = profile
	}

	// 清理不存在的 Pod
	for podUID := range e.profiles {
		if _, exists := data.Pods[podUID]; !exists {
			delete(e.profiles, podUID)
			delete(e.history, podUID)
		}
	}

	// 返回画像副本
	result := make(map[string]*IOProfile, len(e.profiles))
	for k, v := range e.profiles {
		result[k] = v
	}

	return result
}

// computeProfile 计算 Pod 画像
func (e *Engine) computeProfile(hist *profileHistory, totalIOPS, totalBPS float64) *IOProfile {
	if len(hist.Samples) == 0 {
		return &IOProfile{
			PodUID:    hist.PodUID,
			PodName:   hist.PodName,
			Namespace: hist.Namespace,
		}
	}

	profile := &IOProfile{
		PodUID:      hist.PodUID,
		PodName:     hist.PodName,
		Namespace:   hist.Namespace,
		StartTime:   hist.Samples[0].Timestamp,
		EndTime:     hist.Samples[len(hist.Samples)-1].Timestamp,
		SampleCount: len(hist.Samples),
	}

	// 计算统计量
	var sumIOPS, sumBPS, sumReadRatio, sumIOSize float64
	var maxIOPS, maxBPS float64 = 0, 0
	var minIOPS, minBPS float64 = math.MaxFloat64, math.MaxFloat64

	for _, s := range hist.Samples {
		sumIOPS += s.IOPS
		sumBPS += s.BPS
		sumReadRatio += s.ReadRatio
		sumIOSize += s.IOSize

		if s.IOPS > maxIOPS {
			maxIOPS = s.IOPS
		}
		if s.IOPS < minIOPS {
			minIOPS = s.IOPS
		}
		if s.BPS > maxBPS {
			maxBPS = s.BPS
		}
		if s.BPS < minBPS {
			minBPS = s.BPS
		}
	}

	n := float64(len(hist.Samples))
	profile.AvgIOPS = sumIOPS / n
	profile.MaxIOPS = maxIOPS
	profile.MinIOPS = minIOPS
	profile.AvgBPS = sumBPS / n
	profile.MaxBPS = maxBPS
	profile.MinBPS = minBPS
	profile.ReadRatio = sumReadRatio / n
	profile.WriteRatio = 1 - profile.ReadRatio
	profile.AvgIOSize = sumIOSize / n

	// 计算标准差
	var sumSqIOPS, sumSqBPS float64
	for _, s := range hist.Samples {
		sumSqIOPS += math.Pow(s.IOPS-profile.AvgIOPS, 2)
		sumSqBPS += math.Pow(s.BPS-profile.AvgBPS, 2)
	}
	profile.StdDevIOPS = math.Sqrt(sumSqIOPS / n)
	profile.StdDevBPS = math.Sqrt(sumSqBPS / n)

	// 计算评分
	if totalIOPS > 0 {
		profile.IOPSScore = profile.AvgIOPS / totalIOPS * 100
	}
	if totalBPS > 0 {
		profile.BPSScore = profile.AvgBPS / totalBPS * 100
	}

	// 突发性评分 (标准差/平均值)
	if profile.AvgIOPS > 0 {
		profile.BurstScore = profile.StdDevIOPS / profile.AvgIOPS
	}

	// 波动性
	if profile.MaxIOPS > 0 {
		profile.Volatility = (profile.MaxIOPS - profile.MinIOPS) / profile.MaxIOPS
	}

	// 趋势 (简单线性回归斜率)
	profile.Trend = e.computeTrend(hist.Samples)

	// IO 大小判断顺序/随机
	// 大 IO (>=128KB) 通常是顺序 IO
	if profile.AvgIOSize >= 131072 {
		profile.SequentialRatio = math.Min(profile.AvgIOSize/262144, 1.0)
	} else if profile.AvgIOSize <= 4096 {
		profile.RandomRatio = 1.0
		profile.SequentialRatio = 0.0
	} else {
		profile.SequentialRatio = (profile.AvgIOSize - 4096) / (131072 - 4096)
		profile.RandomRatio = 1 - profile.SequentialRatio
	}

	// 计算指纹
	profile.Fingerprint = e.computeFingerprint(hist.Samples)

	return profile
}

// computeTrend 计算趋势
func (e *Engine) computeTrend(samples []sample) float64 {
	n := float64(len(samples))
	if n < 2 {
		return 0
	}

	// 简单线性回归
	var sumX, sumY, sumXY, sumX2 float64
	for i, s := range samples {
		x := float64(i)
		y := s.IOPS
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	denominator := n*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}

	slope := (n*sumXY - sumX*sumY) / denominator

	// 归一化到 -1 到 1
	avgY := sumY / n
	if avgY > 0 {
		normalizedSlope := slope * n / avgY
		return math.Max(-1, math.Min(1, normalizedSlope))
	}

	return 0
}

// computeFingerprint 计算 IO 指纹
func (e *Engine) computeFingerprint(samples []sample) IOFingerprint {
	fp := IOFingerprint{}

	if len(samples) == 0 {
		return fp
	}

	// IOPS 分布
	for _, s := range samples {
		switch {
		case s.IOPS < 100:
			fp.IOPSHistogram[0]++
		case s.IOPS < 1000:
			fp.IOPSHistogram[1]++
		case s.IOPS < 10000:
			fp.IOPSHistogram[2]++
		default:
			fp.IOPSHistogram[3]++
		}

		// IO 大小分布
		switch {
		case s.IOSize < 8192:
			fp.IOSizeHistogram[0]++ // 4K
		case s.IOSize < 16384:
			fp.IOSizeHistogram[1]++ // 8K
		case s.IOSize < 32768:
			fp.IOSizeHistogram[2]++ // 16K
		case s.IOSize < 65536:
			fp.IOSizeHistogram[3]++ // 32K
		case s.IOSize < 131072:
			fp.IOSizeHistogram[4]++ // 64K
		default:
			fp.IOSizeHistogram[5]++ // 128K+
		}

		// 时间分布
		hour := s.Timestamp.Hour()
		fp.HourlyPattern[hour]++
	}

	// 归一化
	n := float64(len(samples))
	for i := range fp.IOPSHistogram {
		fp.IOPSHistogram[i] /= n
	}
	for i := range fp.IOSizeHistogram {
		fp.IOSizeHistogram[i] /= n
	}
	for i := range fp.HourlyPattern {
		fp.HourlyPattern[i] /= n
	}

	return fp
}

// GetProfiles 获取所有画像
func (e *Engine) GetProfiles() map[string]*IOProfile {
	e.profilesMu.RLock()
	defer e.profilesMu.RUnlock()

	result := make(map[string]*IOProfile, len(e.profiles))
	for k, v := range e.profiles {
		result[k] = v
	}
	return result
}

// GetPodProfile 获取指定 Pod 的画像
func (e *Engine) GetPodProfile(namespace, name string) *IOProfile {
	e.profilesMu.RLock()
	defer e.profilesMu.RUnlock()

	for _, p := range e.profiles {
		if p.Namespace == namespace && p.PodName == name {
			return p
		}
	}
	return nil
}

// GetProfileByUID 根据 UID 获取画像
func (e *Engine) GetProfileByUID(podUID string) *IOProfile {
	e.profilesMu.RLock()
	defer e.profilesMu.RUnlock()
	return e.profiles[podUID]
}

// CompareProfiles 比较两个画像的相似度
func CompareProfiles(p1, p2 *IOProfile) float64 {
	if p1 == nil || p2 == nil {
		return 0
	}

	// 使用指纹比较
	similarity := 0.0

	// IOPS 分布相似度 (余弦相似度)
	similarity += cosineSimilarity(p1.Fingerprint.IOPSHistogram[:], p2.Fingerprint.IOPSHistogram[:]) * 0.3

	// IO 大小分布相似度
	similarity += cosineSimilarity(p1.Fingerprint.IOSizeHistogram[:], p2.Fingerprint.IOSizeHistogram[:]) * 0.3

	// 读写比例相似度
	similarity += (1 - math.Abs(p1.ReadRatio-p2.ReadRatio)) * 0.2

	// 顺序/随机比例相似度
	similarity += (1 - math.Abs(p1.SequentialRatio-p2.SequentialRatio)) * 0.2

	return similarity
}

// cosineSimilarity 计算余弦相似度
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

func init() {
	_ = log.Debug // 避免未使用警告
}
