// Package collector - 数据采集器类型定义
package collector

import "time"

// CollectedData 采集的完整数据
type CollectedData struct {
	Timestamp time.Time
	NodeName  string

	// 磁盘级别数据
	Disks map[string]*DiskStats

	// Pod 级别数据
	Pods map[string]*PodIOStats

	// 进程级别数据
	Processes map[int]*ProcessIOStats

	// 系统级别数据
	System *SystemIOStats
}

// DiskStats 磁盘统计数据
type DiskStats struct {
	Device string
	Major  int
	Minor  int

	// 基础指标
	ReadsCompleted  uint64
	ReadsMerged     uint64
	SectorsRead     uint64
	ReadTimeMs      uint64
	WritesCompleted uint64
	WritesMerged    uint64
	SectorsWritten  uint64
	WriteTimeMs     uint64

	// 队列指标
	IOsInProgress   uint64
	IOTimeMs        uint64
	WeightedIOTimeMs uint64

	// 计算指标
	ReadIOPS        float64
	WriteIOPS       float64
	ReadBytesPerSec float64
	WriteBytesPerSec float64
	AvgReadLatencyMs float64
	AvgWriteLatencyMs float64
	Utilization     float64
	AvgQueueDepth   float64

	// IO 类型分析
	SequentialReadRatio  float64
	SequentialWriteRatio float64
	AvgReadSize          float64
	AvgWriteSize         float64
}

// PodIOStats Pod IO 统计数据
type PodIOStats struct {
	PodUID      string
	PodName     string
	Namespace   string
	ContainerID string

	// cgroup 路径
	CgroupPath string

	// IO 统计 (来自 cgroup v2 io.stat)
	ReadBytes   uint64
	WriteBytes  uint64
	ReadIOs     uint64
	WriteIOs    uint64
	DiscardBytes uint64
	DiscardIOs  uint64

	// 限流统计
	ReadThrottled  uint64
	WriteThrottled uint64

	// 计算指标
	ReadBytesPerSec  float64
	WriteBytesPerSec float64
	ReadIOPS         float64
	WriteIOPS        float64

	// IO 权重
	IOWeight int

	// 关联的磁盘设备
	Devices []string
}

// ProcessIOStats 进程 IO 统计数据
type ProcessIOStats struct {
	PID       int
	TID       int // 线程 ID，0 表示主进程
	Comm      string
	State     string // R, S, D, Z, T, etc.
	PPid      int

	// /proc/[pid]/io 数据
	ReadChars      uint64
	WriteChars     uint64
	SyscallReads   uint64
	SyscallWrites  uint64
	ReadBytes      uint64
	WriteBytes     uint64
	CancelledWrite uint64

	// 计算指标
	ReadBytesPerSec  float64
	WriteBytesPerSec float64
	ReadIOPS         float64
	WriteIOPS        float64

	// D 状态相关
	IsDState    bool
	WaitChannel string // /proc/[pid]/wchan
	DStateDuration time.Duration

	// 关联信息
	PodUID    string
	PodName   string
	Namespace string
}

// SystemIOStats 系统级 IO 统计
type SystemIOStats struct {
	// /proc/stat 数据
	IOWaitPercent float64
	ProcsBlocked  int

	// 总体指标
	TotalReadIOPS    float64
	TotalWriteIOPS   float64
	TotalReadBPS     float64
	TotalWriteBPS    float64
	TotalUtilization float64

	// D 状态进程统计
	DStateProcessCount int
	DStateProcesses    []int // PIDs
}

// IOTypeStats IO 类型统计（来自 blktrace/eBPF）
type IOTypeStats struct {
	Device string

	// 请求大小分布
	SizeHistogram map[string]uint64 // "4k": count, "8k": count, etc.

	// 顺序/随机 IO 比例
	SequentialPercent float64
	RandomPercent     float64

	// 延迟分布
	LatencyP50Ms float64
	LatencyP95Ms float64
	LatencyP99Ms float64
	LatencyMaxMs float64

	// 请求类型
	ReadPercent   float64
	WritePercent  float64
	SyncPercent   float64
	AsyncPercent  float64
}

// PodMetrics 用于评分的 Pod 指标汇总
type PodMetrics struct {
	PodUID    string
	PodName   string
	Namespace string

	// IO 指标
	ReadBPS   float64
	WriteBPS  float64
	ReadIOPS  float64
	WriteIOPS float64
	TotalIOPS float64
	TotalBPS  float64

	// 占比
	IOPSPercent float64 // 占节点总 IOPS 百分比
	BPSPercent  float64 // 占节点总带宽百分比

	// 行为特征
	SequentialRatio float64
	AvgIOSize       float64

	// 状态
	IsThrottled    bool
	ThrottledCount uint64
	HasDStateProc  bool
	DStateProcCount int

	// 时间戳
	CollectedAt time.Time
}
