// Package collector - cgroup 数据采集器
package collector

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// CgroupCollector cgroup 数据采集器
type CgroupCollector struct {
	config     config.CollectorConfig
	cgroupPath string
	isCgroupV2 bool
}

// NewCgroupCollector 创建 cgroup 采集器
func NewCgroupCollector(cfg config.CollectorConfig) *CgroupCollector {
	c := &CgroupCollector{
		config:     cfg,
		cgroupPath: cfg.CgroupPath,
	}

	// 检测 cgroup 版本
	c.detectCgroupVersion()

	return c
}

// detectCgroupVersion 检测 cgroup 版本
func (c *CgroupCollector) detectCgroupVersion() {
	// cgroup v2 有统一的 cgroup.controllers 文件
	controllersPath := filepath.Join(c.cgroupPath, "cgroup.controllers")
	if _, err := os.Stat(controllersPath); err == nil {
		c.isCgroupV2 = true
		log.Info("Detected cgroup v2")
	} else {
		c.isCgroupV2 = false
		log.Info("Detected cgroup v1")
	}
}

// Collect 采集 Pod IO 数据
func (c *CgroupCollector) Collect() map[string]*PodIOStats {
	pods := make(map[string]*PodIOStats)

	if c.isCgroupV2 {
		c.collectCgroupV2(pods)
	} else {
		c.collectCgroupV1(pods)
	}

	return pods
}

// collectCgroupV2 采集 cgroup v2 数据
func (c *CgroupCollector) collectCgroupV2(pods map[string]*PodIOStats) {
	// kubepods.slice 路径
	kubepodsPath := filepath.Join(c.cgroupPath, "kubepods.slice")
	if _, err := os.Stat(kubepodsPath); os.IsNotExist(err) {
		// 尝试其他可能的路径
		kubepodsPath = filepath.Join(c.cgroupPath, "kubepods")
	}

	// 遍历所有 QoS 类别
	qosClasses := []string{
		"",                        // Guaranteed (直接在 kubepods.slice 下)
		"kubepods-burstable.slice",
		"kubepods-besteffort.slice",
	}

	for _, qos := range qosClasses {
		var searchPath string
		if qos == "" {
			searchPath = kubepodsPath
		} else {
			searchPath = filepath.Join(kubepodsPath, qos)
		}

		c.scanPodDirs(searchPath, pods)
	}
}

// scanPodDirs 扫描 Pod 目录
func (c *CgroupCollector) scanPodDirs(basePath string, pods map[string]*PodIOStats) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// 检查是否是 Pod 目录
		if strings.Contains(name, "-pod") && strings.HasSuffix(name, ".slice") {
			podPath := filepath.Join(basePath, name)
			stats := c.collectPodIOStats(podPath, name)
			if stats != nil {
				pods[stats.PodUID] = stats
			}
		}
	}
}

// collectPodIOStats 采集单个 Pod 的 IO 统计
func (c *CgroupCollector) collectPodIOStats(podPath string, dirName string) *PodIOStats {
	stats := &PodIOStats{
		CgroupPath: podPath,
	}

	// 从目录名提取 Pod UID
	stats.PodUID = extractPodUIDFromDir(dirName)

	// 读取 io.stat
	ioStatPath := filepath.Join(podPath, "io.stat")
	c.readIOStat(ioStatPath, stats)

	// 读取 io.weight
	ioWeightPath := filepath.Join(podPath, "io.weight")
	c.readIOWeight(ioWeightPath, stats)

	// 获取 Pod 名称（从 Kubernetes API 或缓存）
	// 这里暂时留空，由上层补充
	stats.PodName = ""
	stats.Namespace = ""

	return stats
}

// extractPodUIDFromDir 从目录名提取 Pod UID
func extractPodUIDFromDir(dirName string) string {
	// 格式: kubepods-pod<uid>.slice 或 kubepods-burstable-pod<uid>.slice
	idx := strings.LastIndex(dirName, "-pod")
	if idx < 0 {
		return ""
	}

	uid := dirName[idx+4:] // 跳过 "-pod"
	uid = strings.TrimSuffix(uid, ".slice")
	uid = strings.ReplaceAll(uid, "_", "-")

	return uid
}

// readIOStat 读取 io.stat 文件
func (c *CgroupCollector) readIOStat(path string, stats *PodIOStats) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// 格式: 259:0 rbytes=1234 wbytes=5678 rios=10 wios=20 dbytes=0 dios=0
		c.parseIOStatLine(line, stats)
	}
}

// parseIOStatLine 解析 io.stat 行
func (c *CgroupCollector) parseIOStatLine(line string, stats *PodIOStats) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}

	// 第一个字段是设备号
	deviceID := fields[0]
	stats.Devices = append(stats.Devices, deviceID)

	// 解析各指标
	for _, field := range fields[1:] {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value, _ := strconv.ParseUint(parts[1], 10, 64)

		switch key {
		case "rbytes":
			stats.ReadBytes += value
		case "wbytes":
			stats.WriteBytes += value
		case "rios":
			stats.ReadIOs += value
		case "wios":
			stats.WriteIOs += value
		case "dbytes":
			stats.DiscardBytes += value
		case "dios":
			stats.DiscardIOs += value
		}
	}
}

// readIOWeight 读取 io.weight 文件
func (c *CgroupCollector) readIOWeight(path string, stats *PodIOStats) {
	data, err := os.ReadFile(path)
	if err != nil {
		stats.IOWeight = 100 // 默认权重
		return
	}

	// 格式: default 100 或 259:0 100
	line := strings.TrimSpace(string(data))
	fields := strings.Fields(line)

	for i, field := range fields {
		if field == "default" && i+1 < len(fields) {
			stats.IOWeight, _ = strconv.Atoi(fields[i+1])
			return
		}
	}

	// 尝试直接解析数字
	if weight, err := strconv.Atoi(line); err == nil {
		stats.IOWeight = weight
	} else {
		stats.IOWeight = 100
	}
}

// collectCgroupV1 采集 cgroup v1 数据
func (c *CgroupCollector) collectCgroupV1(pods map[string]*PodIOStats) {
	// cgroup v1 的 blkio 控制器路径
	blkioPath := filepath.Join(c.cgroupPath, "blkio", "kubepods")
	if _, err := os.Stat(blkioPath); os.IsNotExist(err) {
		blkioPath = filepath.Join(c.cgroupPath, "blkio", "kubepods.slice")
	}

	// 遍历 QoS 类别
	qosClasses := []string{"", "burstable", "besteffort"}

	for _, qos := range qosClasses {
		var searchPath string
		if qos == "" {
			searchPath = blkioPath
		} else {
			searchPath = filepath.Join(blkioPath, qos)
		}

		c.scanPodDirsV1(searchPath, pods)
	}
}

// scanPodDirsV1 扫描 cgroup v1 Pod 目录
func (c *CgroupCollector) scanPodDirsV1(basePath string, pods map[string]*PodIOStats) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// 检查是否是 Pod 目录
		if strings.HasPrefix(name, "pod") {
			podPath := filepath.Join(basePath, name)
			stats := c.collectPodIOStatsV1(podPath, name)
			if stats != nil {
				pods[stats.PodUID] = stats
			}
		}
	}
}

// collectPodIOStatsV1 采集 cgroup v1 Pod IO 统计
func (c *CgroupCollector) collectPodIOStatsV1(podPath string, dirName string) *PodIOStats {
	stats := &PodIOStats{
		CgroupPath: podPath,
		PodUID:     strings.TrimPrefix(dirName, "pod"),
	}

	// 读取 blkio.throttle.io_service_bytes
	c.readBlkioServiceBytes(podPath, stats)

	// 读取 blkio.throttle.io_serviced
	c.readBlkioServiced(podPath, stats)

	// 读取 blkio.weight
	c.readBlkioWeight(podPath, stats)

	return stats
}

// readBlkioServiceBytes 读取 blkio.throttle.io_service_bytes
func (c *CgroupCollector) readBlkioServiceBytes(podPath string, stats *PodIOStats) {
	path := filepath.Join(podPath, "blkio.throttle.io_service_bytes")
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		op := fields[1]
		value, _ := strconv.ParseUint(fields[2], 10, 64)

		switch op {
		case "Read":
			stats.ReadBytes += value
		case "Write":
			stats.WriteBytes += value
		}
	}
}

// readBlkioServiced 读取 blkio.throttle.io_serviced
func (c *CgroupCollector) readBlkioServiced(podPath string, stats *PodIOStats) {
	path := filepath.Join(podPath, "blkio.throttle.io_serviced")
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		op := fields[1]
		value, _ := strconv.ParseUint(fields[2], 10, 64)

		switch op {
		case "Read":
			stats.ReadIOs += value
		case "Write":
			stats.WriteIOs += value
		}
	}
}

// readBlkioWeight 读取 blkio.weight
func (c *CgroupCollector) readBlkioWeight(podPath string, stats *PodIOStats) {
	path := filepath.Join(podPath, "blkio.weight")
	data, err := os.ReadFile(path)
	if err != nil {
		stats.IOWeight = 500 // cgroup v1 默认权重
		return
	}

	stats.IOWeight, _ = strconv.Atoi(strings.TrimSpace(string(data)))
}
