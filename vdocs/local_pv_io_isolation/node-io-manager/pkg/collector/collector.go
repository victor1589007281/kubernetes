// Package collector - 多维度数据采集器
package collector

import (
	"context"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// Collector 数据采集器
type Collector struct {
	config config.CollectorConfig

	// 子采集器
	diskCollector    *DiskCollector
	processCollector *ProcessCollector
	cgroupCollector  *CgroupCollector

	// 最新数据
	latestData *CollectedData
	dataMu     sync.RWMutex

	// Pod 指标缓存
	podMetrics map[string]*PodMetrics
	metricsMu  sync.RWMutex

	// 历史数据（用于计算速率）
	prevData     *CollectedData
	prevDataTime time.Time
}

// New 创建采集器
func New(cfg config.CollectorConfig) *Collector {
	return &Collector{
		config:           cfg,
		diskCollector:    NewDiskCollector(cfg),
		processCollector: NewProcessCollector(cfg),
		cgroupCollector:  NewCgroupCollector(cfg),
		podMetrics:       make(map[string]*PodMetrics),
	}
}

// Run 运行采集循环
func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(c.config.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	log.Infof("Collector started with interval %ds", c.config.IntervalSeconds)

	// 立即执行一次
	c.collect()

	for {
		select {
		case <-ctx.Done():
			log.Info("Collector stopped")
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// collect 执行一次数据采集
func (c *Collector) collect() {
	startTime := time.Now()

	data := &CollectedData{
		Timestamp: startTime,
		Disks:     make(map[string]*DiskStats),
		Pods:      make(map[string]*PodIOStats),
		Processes: make(map[int]*ProcessIOStats),
	}

	var wg sync.WaitGroup

	// 并行采集各维度数据
	wg.Add(3)

	go func() {
		defer wg.Done()
		data.Disks = c.diskCollector.Collect()
	}()

	go func() {
		defer wg.Done()
		data.Processes, data.System = c.processCollector.Collect()
	}()

	go func() {
		defer wg.Done()
		data.Pods = c.cgroupCollector.Collect()
	}()

	wg.Wait()

	// 计算速率指标
	c.calculateRates(data)

	// 关联 Pod 和进程
	c.associatePodProcesses(data)

	// 更新 Pod 指标
	c.updatePodMetrics(data)

	// 保存数据
	c.dataMu.Lock()
	c.prevData = c.latestData
	c.prevDataTime = c.latestData.Timestamp
	c.latestData = data
	c.dataMu.Unlock()

	elapsed := time.Since(startTime)
	log.Debugf("Collection completed in %v: %d disks, %d pods, %d processes",
		elapsed, len(data.Disks), len(data.Pods), len(data.Processes))
}

// calculateRates 计算速率指标
func (c *Collector) calculateRates(data *CollectedData) {
	c.dataMu.RLock()
	prev := c.prevData
	prevTime := c.prevDataTime
	c.dataMu.RUnlock()

	if prev == nil {
		return
	}

	interval := data.Timestamp.Sub(prevTime).Seconds()
	if interval <= 0 {
		return
	}

	// 计算磁盘速率
	for device, disk := range data.Disks {
		if prevDisk, ok := prev.Disks[device]; ok {
			// IOPS
			disk.ReadIOPS = float64(disk.ReadsCompleted-prevDisk.ReadsCompleted) / interval
			disk.WriteIOPS = float64(disk.WritesCompleted-prevDisk.WritesCompleted) / interval

			// 带宽 (sector = 512 bytes)
			disk.ReadBytesPerSec = float64(disk.SectorsRead-prevDisk.SectorsRead) * 512 / interval
			disk.WriteBytesPerSec = float64(disk.SectorsWritten-prevDisk.SectorsWritten) * 512 / interval

			// 延迟
			readOps := disk.ReadsCompleted - prevDisk.ReadsCompleted
			if readOps > 0 {
				disk.AvgReadLatencyMs = float64(disk.ReadTimeMs-prevDisk.ReadTimeMs) / float64(readOps)
			}
			writeOps := disk.WritesCompleted - prevDisk.WritesCompleted
			if writeOps > 0 {
				disk.AvgWriteLatencyMs = float64(disk.WriteTimeMs-prevDisk.WriteTimeMs) / float64(writeOps)
			}

			// 利用率
			ioTimeMs := disk.IOTimeMs - prevDisk.IOTimeMs
			disk.Utilization = float64(ioTimeMs) / (interval * 1000) * 100
			if disk.Utilization > 100 {
				disk.Utilization = 100
			}

			// 平均队列深度
			weightedIOTime := disk.WeightedIOTimeMs - prevDisk.WeightedIOTimeMs
			disk.AvgQueueDepth = float64(weightedIOTime) / (interval * 1000)
		}
	}

	// 计算 Pod IO 速率
	for podUID, pod := range data.Pods {
		if prevPod, ok := prev.Pods[podUID]; ok {
			pod.ReadBytesPerSec = float64(pod.ReadBytes-prevPod.ReadBytes) / interval
			pod.WriteBytesPerSec = float64(pod.WriteBytes-prevPod.WriteBytes) / interval
			pod.ReadIOPS = float64(pod.ReadIOs-prevPod.ReadIOs) / interval
			pod.WriteIOPS = float64(pod.WriteIOs-prevPod.WriteIOs) / interval
		}
	}

	// 计算进程 IO 速率
	for pid, proc := range data.Processes {
		if prevProc, ok := prev.Processes[pid]; ok {
			proc.ReadBytesPerSec = float64(proc.ReadBytes-prevProc.ReadBytes) / interval
			proc.WriteBytesPerSec = float64(proc.WriteBytes-prevProc.WriteBytes) / interval
			proc.ReadIOPS = float64(proc.SyscallReads-prevProc.SyscallReads) / interval
			proc.WriteIOPS = float64(proc.SyscallWrites-prevProc.SyscallWrites) / interval
		}
	}

	// 计算系统级总指标
	data.System.TotalReadIOPS = 0
	data.System.TotalWriteIOPS = 0
	data.System.TotalReadBPS = 0
	data.System.TotalWriteBPS = 0

	for _, disk := range data.Disks {
		data.System.TotalReadIOPS += disk.ReadIOPS
		data.System.TotalWriteIOPS += disk.WriteIOPS
		data.System.TotalReadBPS += disk.ReadBytesPerSec
		data.System.TotalWriteBPS += disk.WriteBytesPerSec
	}
}

// associatePodProcesses 关联 Pod 和进程
func (c *Collector) associatePodProcesses(data *CollectedData) {
	// 通过 cgroup 路径关联
	for _, proc := range data.Processes {
		for _, pod := range data.Pods {
			if proc.PodUID == pod.PodUID {
				proc.PodName = pod.PodName
				proc.Namespace = pod.Namespace
				break
			}
		}
	}
}

// updatePodMetrics 更新 Pod 指标汇总
func (c *Collector) updatePodMetrics(data *CollectedData) {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()

	totalIOPS := data.System.TotalReadIOPS + data.System.TotalWriteIOPS
	totalBPS := data.System.TotalReadBPS + data.System.TotalWriteBPS

	for podUID, pod := range data.Pods {
		metrics := &PodMetrics{
			PodUID:       podUID,
			PodName:      pod.PodName,
			Namespace:    pod.Namespace,
			ReadBPS:      pod.ReadBytesPerSec,
			WriteBPS:     pod.WriteBytesPerSec,
			ReadIOPS:     pod.ReadIOPS,
			WriteIOPS:    pod.WriteIOPS,
			TotalIOPS:    pod.ReadIOPS + pod.WriteIOPS,
			TotalBPS:     pod.ReadBytesPerSec + pod.WriteBytesPerSec,
			IsThrottled:  pod.ReadThrottled > 0 || pod.WriteThrottled > 0,
			ThrottledCount: pod.ReadThrottled + pod.WriteThrottled,
			CollectedAt:  data.Timestamp,
		}

		// 计算占比
		if totalIOPS > 0 {
			metrics.IOPSPercent = metrics.TotalIOPS / totalIOPS * 100
		}
		if totalBPS > 0 {
			metrics.BPSPercent = metrics.TotalBPS / totalBPS * 100
		}

		// 检查是否有 D 状态进程
		for _, proc := range data.Processes {
			if proc.PodUID == podUID && proc.IsDState {
				metrics.HasDStateProc = true
				metrics.DStateProcCount++
			}
		}

		c.podMetrics[podUID] = metrics
	}
}

// GetLatestData 获取最新采集数据
func (c *Collector) GetLatestData() *CollectedData {
	c.dataMu.RLock()
	defer c.dataMu.RUnlock()
	return c.latestData
}

// GetPodMetrics 获取 Pod 指标
func (c *Collector) GetPodMetrics() map[string]*PodMetrics {
	c.metricsMu.RLock()
	defer c.metricsMu.RUnlock()

	result := make(map[string]*PodMetrics, len(c.podMetrics))
	for k, v := range c.podMetrics {
		result[k] = v
	}
	return result
}

// GetPodMetricsByName 根据名称获取 Pod 指标
func (c *Collector) GetPodMetricsByName(namespace, name string) *PodMetrics {
	c.metricsMu.RLock()
	defer c.metricsMu.RUnlock()

	for _, m := range c.podMetrics {
		if m.Namespace == namespace && m.PodName == name {
			return m
		}
	}
	return nil
}
