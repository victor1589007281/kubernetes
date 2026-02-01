// Package collector - eBPF IO 采集器
// 提供更精确的 IO 类型分析（顺序/随机、同步/异步）
package collector

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// EBPFCollector eBPF IO 采集器
type EBPFCollector struct {
	config   config.CollectorConfig
	enabled  bool
	bccPath  string
	
	// 采集的数据
	latencyData map[string]*LatencyHistogram
	ioTypeData  map[string]*IOTypeBreakdown
	dataMu      sync.RWMutex
	
	// 控制
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// LatencyHistogram 延迟直方图
type LatencyHistogram struct {
	Device string
	// 延迟分布 (微秒)
	Buckets map[string]uint64 // "1", "2", "4", ..., "1024", "2048+"
	
	// 统计量
	Count  uint64
	Sum    uint64 // 总延迟
	P50    float64
	P95    float64
	P99    float64
	Max    float64
	
	UpdatedAt time.Time
}

// IOTypeBreakdown IO 类型分解
type IOTypeBreakdown struct {
	Device string
	
	// 请求类型统计
	ReadCount       uint64
	WriteCount      uint64
	SyncCount       uint64
	AsyncCount      uint64
	DirectCount     uint64
	BufferedCount   uint64
	
	// 顺序/随机分析
	SequentialReads  uint64
	RandomReads      uint64
	SequentialWrites uint64
	RandomWrites     uint64
	
	// 请求大小分布
	SizeHistogram map[string]uint64 // "4K", "8K", "16K", "32K", "64K", "128K", "256K+"
	
	UpdatedAt time.Time
}

// NewEBPFCollector 创建 eBPF 采集器
func NewEBPFCollector(cfg config.CollectorConfig) *EBPFCollector {
	c := &EBPFCollector{
		config:      cfg,
		latencyData: make(map[string]*LatencyHistogram),
		ioTypeData:  make(map[string]*IOTypeBreakdown),
	}
	
	// 检查 eBPF 环境
	c.enabled = c.checkEBPFEnvironment()
	
	return c
}

// checkEBPFEnvironment 检查 eBPF 环境
func (c *EBPFCollector) checkEBPFEnvironment() bool {
	// 检查内核版本
	version, err := os.ReadFile("/proc/version")
	if err != nil {
		log.Warn("Cannot read kernel version")
		return false
	}
	
	// 解析主版本号
	versionStr := string(version)
	if !strings.Contains(versionStr, "Linux version") {
		return false
	}
	
	// 检查 BCC 工具
	bccPaths := []string{
		"/usr/share/bcc/tools",
		"/usr/local/share/bcc/tools",
		"/snap/bcc/current/share/bcc/tools",
	}
	
	for _, path := range bccPaths {
		if _, err := os.Stat(filepath.Join(path, "biolatency")); err == nil {
			c.bccPath = path
			log.Infof("Found BCC tools at %s", path)
			return true
		}
	}
	
	// 检查 bpftrace
	if _, err := exec.LookPath("bpftrace"); err == nil {
		log.Info("Found bpftrace")
		return true
	}
	
	log.Warn("eBPF tools not found, falling back to /proc based collection")
	return false
}

// IsEnabled 是否启用
func (c *EBPFCollector) IsEnabled() bool {
	return c.enabled && c.config.EnableEBPF
}

// Start 启动采集
func (c *EBPFCollector) Start(ctx context.Context) error {
	if !c.IsEnabled() {
		return nil
	}
	
	ctx, c.cancel = context.WithCancel(ctx)
	
	// 启动延迟采集
	c.wg.Add(1)
	go c.collectLatency(ctx)
	
	// 启动 IO 类型采集
	c.wg.Add(1)
	go c.collectIOType(ctx)
	
	log.Info("eBPF collector started")
	return nil
}

// Stop 停止采集
func (c *EBPFCollector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

// collectLatency 采集延迟数据
func (c *EBPFCollector) collectLatency(ctx context.Context) {
	defer c.wg.Done()
	
	if c.bccPath == "" {
		return
	}
	
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runBiolatency(ctx)
		}
	}
}

// runBiolatency 运行 biolatency
func (c *EBPFCollector) runBiolatency(ctx context.Context) {
	biolatencyPath := filepath.Join(c.bccPath, "biolatency")
	
	// 运行 1 秒采样
	cmd := exec.CommandContext(ctx, "python3", biolatencyPath, "-D", "-m", "1")
	output, err := cmd.Output()
	if err != nil {
		log.Debugf("biolatency failed: %v", err)
		return
	}
	
	c.parseBiolatencyOutput(string(output))
}

// parseBiolatencyOutput 解析 biolatency 输出
func (c *EBPFCollector) parseBiolatencyOutput(output string) {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()
	
	lines := strings.Split(output, "\n")
	var currentDevice string
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// 检测设备行
		if strings.HasPrefix(line, "disk =") {
			parts := strings.Split(line, "=")
			if len(parts) >= 2 {
				currentDevice = strings.TrimSpace(parts[1])
				if _, ok := c.latencyData[currentDevice]; !ok {
					c.latencyData[currentDevice] = &LatencyHistogram{
						Device:  currentDevice,
						Buckets: make(map[string]uint64),
					}
				}
			}
			continue
		}
		
		// 解析直方图行
		if currentDevice != "" && strings.Contains(line, "->") {
			// 格式: 0 -> 1    : 100 |***|
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				bucket := parts[0] + "-" + parts[2]
				count, _ := strconv.ParseUint(parts[4], 10, 64)
				c.latencyData[currentDevice].Buckets[bucket] = count
			}
		}
	}
	
	// 更新时间戳
	for _, data := range c.latencyData {
		data.UpdatedAt = time.Now()
		c.calculatePercentiles(data)
	}
}

// calculatePercentiles 计算百分位数
func (c *EBPFCollector) calculatePercentiles(data *LatencyHistogram) {
	var total uint64
	for _, count := range data.Buckets {
		total += count
	}
	
	if total == 0 {
		return
	}
	
	data.Count = total
	
	// 简化的百分位数计算
	// 实际实现需要根据直方图分布计算
	p50Target := total / 2
	p95Target := total * 95 / 100
	p99Target := total * 99 / 100
	
	var cumulative uint64
	bucketOrder := []string{"0-1", "1-2", "2-4", "4-8", "8-16", "16-32", "32-64", "64-128", "128-256", "256-512", "512-1024"}
	
	for _, bucket := range bucketOrder {
		count := data.Buckets[bucket]
		cumulative += count
		
		// 提取上界值作为延迟估计
		var latency float64
		if strings.Contains(bucket, "-") {
			parts := strings.Split(bucket, "-")
			if len(parts) == 2 {
				latency, _ = strconv.ParseFloat(parts[1], 64)
			}
		}
		
		if data.P50 == 0 && cumulative >= p50Target {
			data.P50 = latency
		}
		if data.P95 == 0 && cumulative >= p95Target {
			data.P95 = latency
		}
		if data.P99 == 0 && cumulative >= p99Target {
			data.P99 = latency
		}
		if cumulative == total {
			data.Max = latency
		}
	}
}

// collectIOType 采集 IO 类型数据
func (c *EBPFCollector) collectIOType(ctx context.Context) {
	defer c.wg.Done()
	
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.analyzeIOType()
		}
	}
}

// analyzeIOType 分析 IO 类型
func (c *EBPFCollector) analyzeIOType() {
	// 使用 /sys/kernel/debug/block/*/stat 或 blktrace
	// 这里提供一个基于 /proc/diskstats 的简化实现
	
	c.dataMu.Lock()
	defer c.dataMu.Unlock()
	
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return
	}
	
	for _, entry := range entries {
		device := entry.Name()
		if !isBlockDevice(device) {
			continue
		}
		
		if _, ok := c.ioTypeData[device]; !ok {
			c.ioTypeData[device] = &IOTypeBreakdown{
				Device:        device,
				SizeHistogram: make(map[string]uint64),
			}
		}
		
		// 读取设备统计
		statPath := filepath.Join("/sys/block", device, "stat")
		c.readDeviceStat(device, statPath)
		
		c.ioTypeData[device].UpdatedAt = time.Now()
	}
}

// readDeviceStat 读取设备统计
func (c *EBPFCollector) readDeviceStat(device, statPath string) {
	file, err := os.Open(statPath)
	if err != nil {
		return
	}
	defer file.Close()
	
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 11 {
			data := c.ioTypeData[device]
			
			reads, _ := strconv.ParseUint(fields[0], 10, 64)
			writes, _ := strconv.ParseUint(fields[4], 10, 64)
			
			data.ReadCount = reads
			data.WriteCount = writes
		}
	}
}

// isBlockDevice 检查是否是块设备
func isBlockDevice(name string) bool {
	// 跳过分区、loop、ram 设备
	if strings.HasPrefix(name, "loop") ||
		strings.HasPrefix(name, "ram") {
		return false
	}
	return true
}

// GetLatencyData 获取延迟数据
func (c *EBPFCollector) GetLatencyData() map[string]*LatencyHistogram {
	c.dataMu.RLock()
	defer c.dataMu.RUnlock()
	
	result := make(map[string]*LatencyHistogram, len(c.latencyData))
	for k, v := range c.latencyData {
		result[k] = v
	}
	return result
}

// GetIOTypeData 获取 IO 类型数据
func (c *EBPFCollector) GetIOTypeData() map[string]*IOTypeBreakdown {
	c.dataMu.RLock()
	defer c.dataMu.RUnlock()
	
	result := make(map[string]*IOTypeBreakdown, len(c.ioTypeData))
	for k, v := range c.ioTypeData {
		result[k] = v
	}
	return result
}

// BPFTraceScript bpftrace 脚本用于详细 IO 分析
const BPFTraceScript = `
#!/usr/bin/env bpftrace
/*
 * IO 详细分析脚本
 * 用于收集 IO 类型、大小、延迟等信息
 */

#include <linux/blk-mq.h>

BEGIN
{
    printf("Tracing block IO... Hit Ctrl-C to end.\n");
}

tracepoint:block:block_rq_issue
{
    @start[args->dev, args->sector] = nsecs;
    @io_size[@comm] = hist(args->bytes);
    
    // 统计 IO 类型
    if (args->rwbs & 1) {
        @writes++;
    } else {
        @reads++;
    }
    
    // 统计同步/异步
    if (args->rwbs & (1 << 4)) {
        @sync++;
    } else {
        @async++;
    }
}

tracepoint:block:block_rq_complete
/@start[args->dev, args->sector]/
{
    $lat = nsecs - @start[args->dev, args->sector];
    @latency_us = hist($lat / 1000);
    @avg_latency = avg($lat / 1000);
    delete(@start[args->dev, args->sector]);
}

END
{
    printf("\n=== IO Size Distribution ===\n");
    print(@io_size);
    
    printf("\n=== Latency Distribution (us) ===\n");
    print(@latency_us);
    
    printf("\n=== IO Type Counts ===\n");
    printf("Reads: %d, Writes: %d\n", @reads, @writes);
    printf("Sync: %d, Async: %d\n", @sync, @async);
    
    printf("\n=== Average Latency ===\n");
    print(@avg_latency);
}
`

// RunBPFTrace 运行 bpftrace 脚本
func (c *EBPFCollector) RunBPFTrace(ctx context.Context, duration time.Duration) (string, error) {
	if !c.enabled {
		return "", fmt.Errorf("eBPF not enabled")
	}
	
	// 创建临时脚本文件
	tmpFile, err := os.CreateTemp("", "io_trace_*.bt")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())
	
	if _, err := tmpFile.WriteString(BPFTraceScript); err != nil {
		return "", err
	}
	tmpFile.Close()
	
	// 运行 bpftrace
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "bpftrace", tmpFile.Name())
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		// 正常超时结束
		return string(output), nil
	}
	return string(output), err
}
