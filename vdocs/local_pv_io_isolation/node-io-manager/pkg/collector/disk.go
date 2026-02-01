// Package collector - 磁盘数据采集器
package collector

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// DiskCollector 磁盘数据采集器
type DiskCollector struct {
	config   config.CollectorConfig
	procPath string
	sysPath  string
}

// NewDiskCollector 创建磁盘采集器
func NewDiskCollector(cfg config.CollectorConfig) *DiskCollector {
	return &DiskCollector{
		config:   cfg,
		procPath: cfg.ProcPath,
		sysPath:  cfg.SysPath,
	}
}

// Collect 采集磁盘数据
func (c *DiskCollector) Collect() map[string]*DiskStats {
	disks := make(map[string]*DiskStats)

	// 读取 /proc/diskstats
	diskstats := c.readDiskstats()
	for device, stats := range diskstats {
		// 过滤设备
		if !c.shouldCollect(device) {
			continue
		}

		disks[device] = stats

		// 读取额外的 sysfs 数据
		c.enrichFromSysfs(stats)
	}

	return disks
}

// readDiskstats 读取 /proc/diskstats
func (c *DiskCollector) readDiskstats() map[string]*DiskStats {
	result := make(map[string]*DiskStats)

	path := filepath.Join(c.procPath, "diskstats")
	file, err := os.Open(path)
	if err != nil {
		log.Errorf("Failed to open %s: %v", path, err)
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		stats, err := c.parseDiskstatsLine(line)
		if err != nil {
			continue
		}
		result[stats.Device] = stats
	}

	return result
}

// parseDiskstatsLine 解析 diskstats 行
// 格式: major minor name reads_completed reads_merged sectors_read read_time
//       writes_completed writes_merged sectors_written write_time
//       ios_in_progress io_time weighted_io_time
func (c *DiskCollector) parseDiskstatsLine(line string) (*DiskStats, error) {
	fields := strings.Fields(line)
	if len(fields) < 14 {
		return nil, fmt.Errorf("invalid diskstats line: %s", line)
	}

	stats := &DiskStats{}

	major, _ := strconv.Atoi(fields[0])
	minor, _ := strconv.Atoi(fields[1])
	stats.Major = major
	stats.Minor = minor
	stats.Device = fields[2]

	stats.ReadsCompleted, _ = strconv.ParseUint(fields[3], 10, 64)
	stats.ReadsMerged, _ = strconv.ParseUint(fields[4], 10, 64)
	stats.SectorsRead, _ = strconv.ParseUint(fields[5], 10, 64)
	stats.ReadTimeMs, _ = strconv.ParseUint(fields[6], 10, 64)

	stats.WritesCompleted, _ = strconv.ParseUint(fields[7], 10, 64)
	stats.WritesMerged, _ = strconv.ParseUint(fields[8], 10, 64)
	stats.SectorsWritten, _ = strconv.ParseUint(fields[9], 10, 64)
	stats.WriteTimeMs, _ = strconv.ParseUint(fields[10], 10, 64)

	stats.IOsInProgress, _ = strconv.ParseUint(fields[11], 10, 64)
	stats.IOTimeMs, _ = strconv.ParseUint(fields[12], 10, 64)
	stats.WeightedIOTimeMs, _ = strconv.ParseUint(fields[13], 10, 64)

	return stats, nil
}

// shouldCollect 判断是否应该采集该设备
func (c *DiskCollector) shouldCollect(device string) bool {
	// 跳过分区（只采集整盘）
	if strings.HasSuffix(device, "p1") || strings.HasSuffix(device, "p2") {
		return false
	}
	// 跳过 loop 设备
	if strings.HasPrefix(device, "loop") {
		return false
	}
	// 跳过 ram 设备
	if strings.HasPrefix(device, "ram") {
		return false
	}
	// 跳过 dm- 设备（除非明确配置）
	if strings.HasPrefix(device, "dm-") {
		for _, d := range c.config.Devices {
			if d == device {
				return true
			}
		}
		return false
	}

	// 如果配置了特定设备列表，只采集列表中的
	if len(c.config.Devices) > 0 {
		for _, d := range c.config.Devices {
			if d == device {
				return true
			}
		}
		return false
	}

	// 采集 nvme、sd、vd 设备
	if strings.HasPrefix(device, "nvme") ||
		strings.HasPrefix(device, "sd") ||
		strings.HasPrefix(device, "vd") {
		// 跳过分区
		for i := '0'; i <= '9'; i++ {
			if strings.HasSuffix(device, string(i)) && !strings.HasPrefix(device, "nvme") {
				return false
			}
		}
		return true
	}

	return false
}

// enrichFromSysfs 从 sysfs 读取额外信息
func (c *DiskCollector) enrichFromSysfs(stats *DiskStats) {
	basePath := filepath.Join(c.sysPath, "block", stats.Device)

	// 读取队列信息
	queuePath := filepath.Join(basePath, "queue")

	// 读取 inflight (当前在处理的 IO)
	inflightPath := filepath.Join(basePath, "inflight")
	if data, err := os.ReadFile(inflightPath); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			read, _ := strconv.ParseUint(fields[0], 10, 64)
			write, _ := strconv.ParseUint(fields[1], 10, 64)
			stats.IOsInProgress = read + write
		}
	}

	// 读取调度器
	schedulerPath := filepath.Join(queuePath, "scheduler")
	if data, err := os.ReadFile(schedulerPath); err == nil {
		_ = string(data) // 可以用于后续分析
	}
}

// GetDeviceMajorMinor 获取设备的主次设备号
func GetDeviceMajorMinor(device string) (int, int, error) {
	path := filepath.Join("/sys/block", device, "dev")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}

	parts := strings.Split(strings.TrimSpace(string(data)), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid device number format")
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}

	return major, minor, nil
}
