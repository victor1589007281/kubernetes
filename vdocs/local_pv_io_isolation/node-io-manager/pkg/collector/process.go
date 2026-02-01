// Package collector - 进程数据采集器
package collector

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
)

// ProcessCollector 进程数据采集器
type ProcessCollector struct {
	config   config.CollectorConfig
	procPath string

	// D 状态跟踪
	dStateTracker map[int]time.Time // pid -> 进入 D 状态时间
}

// NewProcessCollector 创建进程采集器
func NewProcessCollector(cfg config.CollectorConfig) *ProcessCollector {
	return &ProcessCollector{
		config:        cfg,
		procPath:      cfg.ProcPath,
		dStateTracker: make(map[int]time.Time),
	}
}

// Collect 采集进程数据
func (c *ProcessCollector) Collect() (map[int]*ProcessIOStats, *SystemIOStats) {
	processes := make(map[int]*ProcessIOStats)
	system := &SystemIOStats{}

	// 读取 /proc/stat 获取系统级数据
	c.readProcStat(system)

	// 遍历所有进程
	procDir, err := os.Open(c.procPath)
	if err != nil {
		log.Errorf("Failed to open %s: %v", c.procPath, err)
		return processes, system
	}
	defer procDir.Close()

	entries, err := procDir.Readdirnames(-1)
	if err != nil {
		log.Errorf("Failed to read proc directory: %v", err)
		return processes, system
	}

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry)
		if err != nil {
			continue // 不是进程目录
		}

		stats, err := c.collectProcess(pid)
		if err != nil {
			continue
		}

		processes[pid] = stats

		// 统计 D 状态进程
		if stats.IsDState {
			system.DStateProcessCount++
			system.DStateProcesses = append(system.DStateProcesses, pid)
		}
	}

	// 更新系统阻塞进程数
	system.ProcsBlocked = system.DStateProcessCount

	return processes, system
}

// collectProcess 采集单个进程数据
func (c *ProcessCollector) collectProcess(pid int) (*ProcessIOStats, error) {
	procPath := filepath.Join(c.procPath, strconv.Itoa(pid))

	stats := &ProcessIOStats{
		PID: pid,
	}

	// 读取 /proc/[pid]/stat
	if err := c.readProcPidStat(procPath, stats); err != nil {
		return nil, err
	}

	// 读取 /proc/[pid]/io
	c.readProcPidIO(procPath, stats)

	// 读取 /proc/[pid]/wchan
	c.readProcPidWchan(procPath, stats)

	// 读取 /proc/[pid]/cgroup 获取 Pod 关联
	c.readProcPidCgroup(procPath, stats)

	// 跟踪 D 状态持续时间
	c.trackDState(stats)

	return stats, nil
}

// readProcPidStat 读取 /proc/[pid]/stat
func (c *ProcessCollector) readProcPidStat(procPath string, stats *ProcessIOStats) error {
	path := filepath.Join(procPath, "stat")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	content := string(data)

	// 解析 comm (在括号内)
	commStart := strings.Index(content, "(")
	commEnd := strings.LastIndex(content, ")")
	if commStart < 0 || commEnd < 0 {
		return fmt.Errorf("invalid stat format")
	}

	stats.Comm = content[commStart+1 : commEnd]

	// 解析括号后的字段
	fields := strings.Fields(content[commEnd+2:])
	if len(fields) < 2 {
		return fmt.Errorf("invalid stat format")
	}

	stats.State = fields[0]
	stats.PPid, _ = strconv.Atoi(fields[1])

	// 检查是否为 D 状态
	stats.IsDState = stats.State == "D"

	return nil
}

// readProcPidIO 读取 /proc/[pid]/io
func (c *ProcessCollector) readProcPidIO(procPath string, stats *ProcessIOStats) {
	path := filepath.Join(procPath, "io")
	file, err := os.Open(path)
	if err != nil {
		return // io 文件可能不存在或无权限
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)

		switch key {
		case "rchar":
			stats.ReadChars = value
		case "wchar":
			stats.WriteChars = value
		case "syscr":
			stats.SyscallReads = value
		case "syscw":
			stats.SyscallWrites = value
		case "read_bytes":
			stats.ReadBytes = value
		case "write_bytes":
			stats.WriteBytes = value
		case "cancelled_write_bytes":
			stats.CancelledWrite = value
		}
	}
}

// readProcPidWchan 读取 /proc/[pid]/wchan
func (c *ProcessCollector) readProcPidWchan(procPath string, stats *ProcessIOStats) {
	path := filepath.Join(procPath, "wchan")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	stats.WaitChannel = strings.TrimSpace(string(data))
}

// readProcPidCgroup 读取 /proc/[pid]/cgroup 获取 Pod 关联
func (c *ProcessCollector) readProcPidCgroup(procPath string, stats *ProcessIOStats) {
	path := filepath.Join(procPath, "cgroup")
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// 格式: hierarchy-ID:controller-list:cgroup-path
		// 例如: 0::/kubepods.slice/kubepods-pod123.slice/cri-containerd-xxx.scope

		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}

		cgroupPath := parts[2]

		// 提取 Pod UID
		if strings.Contains(cgroupPath, "kubepods") {
			stats.PodUID = extractPodUID(cgroupPath)
		}
	}
}

// extractPodUID 从 cgroup 路径提取 Pod UID
func extractPodUID(cgroupPath string) string {
	// 处理各种格式的 cgroup 路径
	// kubepods-pod<uid>.slice
	// kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod<uid>.slice

	parts := strings.Split(cgroupPath, "/")
	for _, part := range parts {
		if strings.Contains(part, "-pod") && strings.HasSuffix(part, ".slice") {
			// 提取 pod 后面的 UID
			idx := strings.Index(part, "-pod")
			if idx >= 0 {
				uid := part[idx+4:] // 跳过 "-pod"
				uid = strings.TrimSuffix(uid, ".slice")
				// 将下划线转换为连字符（Kubernetes UID 格式）
				uid = strings.ReplaceAll(uid, "_", "-")
				return uid
			}
		}
	}
	return ""
}

// trackDState 跟踪 D 状态持续时间
func (c *ProcessCollector) trackDState(stats *ProcessIOStats) {
	now := time.Now()

	if stats.IsDState {
		if startTime, exists := c.dStateTracker[stats.PID]; exists {
			stats.DStateDuration = now.Sub(startTime)
		} else {
			c.dStateTracker[stats.PID] = now
			stats.DStateDuration = 0
		}
	} else {
		// 进程不再是 D 状态，清除跟踪
		delete(c.dStateTracker, stats.PID)
	}

	// 清理已不存在的进程
	// (这里可以定期执行，避免内存泄漏)
}

// readProcStat 读取 /proc/stat
func (c *ProcessCollector) readProcStat(system *SystemIOStats) {
	path := filepath.Join(c.procPath, "stat")
	file, err := os.Open(path)
	if err != nil {
		log.Errorf("Failed to open %s: %v", path, err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch fields[0] {
		case "cpu":
			// 计算 IO wait 百分比
			if len(fields) >= 6 {
				user, _ := strconv.ParseUint(fields[1], 10, 64)
				nice, _ := strconv.ParseUint(fields[2], 10, 64)
				system, _ := strconv.ParseUint(fields[3], 10, 64)
				idle, _ := strconv.ParseUint(fields[4], 10, 64)
				iowait, _ := strconv.ParseUint(fields[5], 10, 64)

				total := user + nice + system + idle + iowait
				if total > 0 {
					// 注意：这是累计值，需要计算差值才能得到百分比
					// 这里暂时返回原始值，在 calculateRates 中计算
					_ = float64(iowait) / float64(total) * 100
				}
			}
		case "procs_blocked":
			system.ProcsBlocked, _ = strconv.Atoi(fields[1])
		}
	}
}
