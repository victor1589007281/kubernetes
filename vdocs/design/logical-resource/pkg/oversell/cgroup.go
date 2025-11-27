/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package oversell

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kubernetes/kubernetes/vdocs/design/logical-resource/pkg/types"
)

// CgroupManager manages cgroup operations for memory overselling.
type CgroupManager struct {
	// cgroupRoot is the root path of the cgroup filesystem.
	cgroupRoot string

	// version is the cgroup version.
	version types.CgroupVersion
}

// NewCgroupManager creates a new CgroupManager.
func NewCgroupManager() *CgroupManager {
	return &CgroupManager{
		cgroupRoot: "/sys/fs/cgroup",
		version:    detectCgroupVersion(),
	}
}

// NewCgroupManagerWithRoot creates a new CgroupManager with a custom root.
func NewCgroupManagerWithRoot(root string) *CgroupManager {
	return &CgroupManager{
		cgroupRoot: root,
		version:    detectCgroupVersion(),
	}
}

// detectCgroupVersion detects the cgroup version.
func detectCgroupVersion() types.CgroupVersion {
	// Check if cgroup v2 is available by looking for cgroup.controllers
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return types.CgroupV2
	}
	return types.CgroupV1
}

// GetVersion returns the detected cgroup version.
func (m *CgroupManager) GetVersion() types.CgroupVersion {
	return m.version
}

// CreateCgroup creates a new cgroup with the specified configuration.
func (m *CgroupManager) CreateCgroup(config types.CgroupMemoryConfig) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(config.Path, 0755); err != nil {
		return fmt.Errorf("failed to create cgroup directory: %w", err)
	}

	// Set memory limits based on cgroup version
	if m.version == types.CgroupV2 {
		return m.setCgroupV2Memory(config)
	}
	return m.setCgroupV1Memory(config)
}

// UpdateCgroup updates an existing cgroup configuration.
func (m *CgroupManager) UpdateCgroup(config types.CgroupMemoryConfig) error {
	// Check if cgroup exists
	if _, err := os.Stat(config.Path); os.IsNotExist(err) {
		return fmt.Errorf("cgroup %s does not exist", config.Path)
	}

	if m.version == types.CgroupV2 {
		return m.setCgroupV2Memory(config)
	}
	return m.setCgroupV1Memory(config)
}

// DeleteCgroup deletes a cgroup.
func (m *CgroupManager) DeleteCgroup(path string) error {
	// Check if cgroup exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // Already deleted
	}

	// rmdir only works on empty cgroups
	return os.Remove(path)
}

// setCgroupV2Memory sets memory limits for cgroup v2.
func (m *CgroupManager) setCgroupV2Memory(config types.CgroupMemoryConfig) error {
	// Set memory.max
	if config.MemoryMax > 0 {
		memMaxPath := filepath.Join(config.Path, "memory.max")
		if err := writeFile(memMaxPath, strconv.FormatInt(config.MemoryMax, 10)); err != nil {
			return fmt.Errorf("failed to set memory.max: %w", err)
		}
	}

	// Set memory.high
	if config.MemoryHigh > 0 {
		memHighPath := filepath.Join(config.Path, "memory.high")
		if err := writeFile(memHighPath, strconv.FormatInt(config.MemoryHigh, 10)); err != nil {
			return fmt.Errorf("failed to set memory.high: %w", err)
		}
	}

	// Set memory.low (memory protection)
	if config.MemoryLow > 0 {
		memLowPath := filepath.Join(config.Path, "memory.low")
		if err := writeFile(memLowPath, strconv.FormatInt(config.MemoryLow, 10)); err != nil {
			return fmt.Errorf("failed to set memory.low: %w", err)
		}
	}

	return nil
}

// setCgroupV1Memory sets memory limits for cgroup v1.
func (m *CgroupManager) setCgroupV1Memory(config types.CgroupMemoryConfig) error {
	// Set memory.limit_in_bytes
	if config.MemoryMax > 0 {
		memLimitPath := filepath.Join(config.Path, "memory.limit_in_bytes")
		if err := writeFile(memLimitPath, strconv.FormatInt(config.MemoryMax, 10)); err != nil {
			return fmt.Errorf("failed to set memory.limit_in_bytes: %w", err)
		}
	}

	// Set memory.soft_limit_in_bytes (similar to memory.high in v2)
	if config.MemoryHigh > 0 {
		softLimitPath := filepath.Join(config.Path, "memory.soft_limit_in_bytes")
		if err := writeFile(softLimitPath, strconv.FormatInt(config.MemoryHigh, 10)); err != nil {
			return fmt.Errorf("failed to set memory.soft_limit_in_bytes: %w", err)
		}
	}

	return nil
}

// GetMemoryUsage returns the current memory usage for a cgroup.
func (m *CgroupManager) GetMemoryUsage(path string) (int64, error) {
	var usagePath string
	if m.version == types.CgroupV2 {
		usagePath = filepath.Join(path, "memory.current")
	} else {
		usagePath = filepath.Join(path, "memory.usage_in_bytes")
	}

	return readInt64File(usagePath)
}

// GetMemoryLimit returns the memory limit for a cgroup.
func (m *CgroupManager) GetMemoryLimit(path string) (int64, error) {
	var limitPath string
	if m.version == types.CgroupV2 {
		limitPath = filepath.Join(path, "memory.max")
	} else {
		limitPath = filepath.Join(path, "memory.limit_in_bytes")
	}

	return readInt64File(limitPath)
}

// GetMemoryStat returns memory statistics for a cgroup.
func (m *CgroupManager) GetMemoryStat(path string) (map[string]int64, error) {
	statPath := filepath.Join(path, "memory.stat")
	return readStatFile(statPath)
}

// GetCgroupConfig returns the current configuration for a cgroup.
func (m *CgroupManager) GetCgroupConfig(path string) (types.CgroupMemoryConfig, error) {
	config := types.CgroupMemoryConfig{
		Path:    path,
		Version: m.version,
	}

	var err error

	// Get memory max
	config.MemoryMax, err = m.GetMemoryLimit(path)
	if err != nil {
		return config, err
	}

	// Get memory high (v2 only)
	if m.version == types.CgroupV2 {
		highPath := filepath.Join(path, "memory.high")
		config.MemoryHigh, _ = readInt64File(highPath) // Ignore error, may not exist

		lowPath := filepath.Join(path, "memory.low")
		config.MemoryLow, _ = readInt64File(lowPath)
	}

	return config, nil
}

// MoveProcess moves a process to a cgroup.
func (m *CgroupManager) MoveProcess(cgroupPath string, pid int) error {
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	return writeFile(procsPath, strconv.Itoa(pid))
}

// ListProcesses lists all processes in a cgroup.
func (m *CgroupManager) ListProcesses(cgroupPath string) ([]int, error) {
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")

	file, err := os.Open(procsPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var pids []int
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		pid, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if err == nil {
			pids = append(pids, pid)
		}
	}

	return pids, scanner.Err()
}

// EnableMemoryController enables the memory controller for a cgroup (v2 only).
func (m *CgroupManager) EnableMemoryController(path string) error {
	if m.version != types.CgroupV2 {
		return nil // Not needed for v1
	}

	controllersPath := filepath.Join(path, "cgroup.subtree_control")
	return writeFile(controllersPath, "+memory")
}

// IsMemoryControllerEnabled checks if the memory controller is enabled.
func (m *CgroupManager) IsMemoryControllerEnabled(path string) (bool, error) {
	if m.version != types.CgroupV2 {
		return true, nil // Always enabled in v1
	}

	controllersPath := filepath.Join(path, "cgroup.controllers")
	content, err := os.ReadFile(controllersPath)
	if err != nil {
		return false, err
	}

	return strings.Contains(string(content), "memory"), nil
}

// SetMemoryMin sets the minimum memory guarantee (v2 only).
func (m *CgroupManager) SetMemoryMin(path string, bytes int64) error {
	if m.version != types.CgroupV2 {
		return errors.New("memory.min is only available in cgroup v2")
	}

	memMinPath := filepath.Join(path, "memory.min")
	return writeFile(memMinPath, strconv.FormatInt(bytes, 10))
}

// GetOOMEvents returns the number of OOM events for a cgroup.
func (m *CgroupManager) GetOOMEvents(path string) (int64, error) {
	if m.version == types.CgroupV2 {
		eventsPath := filepath.Join(path, "memory.events")
		stats, err := readStatFile(eventsPath)
		if err != nil {
			return 0, err
		}
		return stats["oom_kill"], nil
	}

	// For v1, check oom_control
	oomPath := filepath.Join(path, "memory.oom_control")
	stats, err := readStatFile(oomPath)
	if err != nil {
		return 0, err
	}
	return stats["oom_kill"], nil
}

// writeFile writes content to a file.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// readInt64File reads an int64 value from a file.
func readInt64File(path string) (int64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	str := strings.TrimSpace(string(content))

	// Handle "max" value in cgroup v2
	if str == "max" {
		return 0, nil // Or return MaxInt64
	}

	return strconv.ParseInt(str, 10, 64)
}

// readStatFile reads a stat file and returns a map of key-value pairs.
func readStatFile(path string) (map[string]int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stats := make(map[string]int64)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			value, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil {
				stats[parts[0]] = value
			}
		}
	}

	return stats, scanner.Err()
}

// CgroupExists checks if a cgroup exists.
func CgroupExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetCgroupRoot returns the cgroup root path.
func (m *CgroupManager) GetCgroupRoot() string {
	return m.cgroupRoot
}

