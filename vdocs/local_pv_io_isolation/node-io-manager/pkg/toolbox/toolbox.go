// Package toolbox - 工具箱
package toolbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/node-io-manager/pkg/config"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Toolbox 工具箱
type Toolbox struct {
	config     config.ToolboxConfig
	k8sClient  kubernetes.Interface
	nodeName   string
	cgroupPath string
}

// NewToolbox 创建工具箱
func NewToolbox(cfg config.ToolboxConfig) *Toolbox {
	t := &Toolbox{
		config:     cfg,
		nodeName:   os.Getenv("NODE_NAME"),
		cgroupPath: "/sys/fs/cgroup",
	}

	// 初始化 Kubernetes 客户端
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Warnf("Failed to create in-cluster config: %v", err)
	} else {
		t.k8sClient, err = kubernetes.NewForConfig(config)
		if err != nil {
			log.Warnf("Failed to create Kubernetes client: %v", err)
		}
	}

	return t
}

// LimitIO 限制 Pod IO
func (t *Toolbox) LimitIO(namespace, podName string, readIOPS, writeIOPS, readBPS, writeBPS int64) error {
	if t.config.DryRun {
		log.Infof("[DryRun] Would limit IO for %s/%s: riops=%d wiops=%d rbps=%d wbps=%d",
			namespace, podName, readIOPS, writeIOPS, readBPS, writeBPS)
		return nil
	}

	if !t.config.EnableIOLimit {
		return fmt.Errorf("IO limit is disabled")
	}

	// 获取 Pod 的 cgroup 路径
	cgroupPath, err := t.getPodCgroupPath(namespace, podName)
	if err != nil {
		return fmt.Errorf("failed to get cgroup path: %w", err)
	}

	// 获取设备号
	devices, err := t.getBlockDevices()
	if err != nil {
		return fmt.Errorf("failed to get block devices: %w", err)
	}

	// 写入 io.max 配置
	for _, dev := range devices {
		limit := t.buildIOLimit(dev, readIOPS, writeIOPS, readBPS, writeBPS)
		ioMaxPath := filepath.Join(cgroupPath, "io.max")

		if err := t.writeToFile(ioMaxPath, limit); err != nil {
			log.Warnf("Failed to write io.max for device %s: %v", dev, err)
		}
	}

	log.Infof("Applied IO limit to %s/%s", namespace, podName)
	return nil
}

// RemoveLimit 移除 IO 限制
func (t *Toolbox) RemoveLimit(namespace, podName string) error {
	if t.config.DryRun {
		log.Infof("[DryRun] Would remove IO limit for %s/%s", namespace, podName)
		return nil
	}

	// 获取 Pod 的 cgroup 路径
	cgroupPath, err := t.getPodCgroupPath(namespace, podName)
	if err != nil {
		return fmt.Errorf("failed to get cgroup path: %w", err)
	}

	// 获取设备号
	devices, err := t.getBlockDevices()
	if err != nil {
		return fmt.Errorf("failed to get block devices: %w", err)
	}

	// 写入 max (无限制)
	for _, dev := range devices {
		limit := fmt.Sprintf("%s riops=max wiops=max rbps=max wbps=max", dev)
		ioMaxPath := filepath.Join(cgroupPath, "io.max")

		if err := t.writeToFile(ioMaxPath, limit); err != nil {
			log.Warnf("Failed to remove io.max limit for device %s: %v", dev, err)
		}
	}

	log.Infof("Removed IO limit from %s/%s", namespace, podName)
	return nil
}

// SetIOWeight 设置 IO 权重
func (t *Toolbox) SetIOWeight(namespace, podName string, weight int) error {
	if t.config.DryRun {
		log.Infof("[DryRun] Would set IO weight for %s/%s: %d", namespace, podName, weight)
		return nil
	}

	cgroupPath, err := t.getPodCgroupPath(namespace, podName)
	if err != nil {
		return fmt.Errorf("failed to get cgroup path: %w", err)
	}

	ioWeightPath := filepath.Join(cgroupPath, "io.weight")
	return t.writeToFile(ioWeightPath, fmt.Sprintf("default %d", weight))
}

// CordonNode 标记节点不可调度
func (t *Toolbox) CordonNode() error {
	if t.config.DryRun {
		log.Infof("[DryRun] Would cordon node %s", t.nodeName)
		return nil
	}

	if !t.config.EnableSchedule {
		return fmt.Errorf("schedule control is disabled")
	}

	if t.k8sClient == nil {
		return t.cordonNodeWithKubectl()
	}

	ctx := context.Background()
	node, err := t.k8sClient.CoreV1().Nodes().Get(ctx, t.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	node.Spec.Unschedulable = true

	_, err = t.k8sClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	log.Infof("Cordoned node %s", t.nodeName)
	return nil
}

// UncordonNode 取消节点不可调度标记
func (t *Toolbox) UncordonNode() error {
	if t.config.DryRun {
		log.Infof("[DryRun] Would uncordon node %s", t.nodeName)
		return nil
	}

	if !t.config.EnableSchedule {
		return fmt.Errorf("schedule control is disabled")
	}

	if t.k8sClient == nil {
		return t.uncordonNodeWithKubectl()
	}

	ctx := context.Background()
	node, err := t.k8sClient.CoreV1().Nodes().Get(ctx, t.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	node.Spec.Unschedulable = false

	_, err = t.k8sClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	log.Infof("Uncordoned node %s", t.nodeName)
	return nil
}

// getPodCgroupPath 获取 Pod 的 cgroup 路径
func (t *Toolbox) getPodCgroupPath(namespace, podName string) (string, error) {
	if t.k8sClient == nil {
		return "", fmt.Errorf("kubernetes client not available")
	}

	ctx := context.Background()
	pod, err := t.k8sClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get pod: %w", err)
	}

	// 获取 Pod UID
	uid := string(pod.UID)
	uid = strings.ReplaceAll(uid, "-", "_")

	// 构建 cgroup 路径 (cgroup v2)
	// 格式: /sys/fs/cgroup/kubepods.slice/kubepods-pod<uid>.slice
	// 或: /sys/fs/cgroup/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod<uid>.slice

	qos := string(pod.Status.QOSClass)
	var cgroupPath string

	switch qos {
	case "Guaranteed":
		cgroupPath = filepath.Join(t.cgroupPath, "kubepods.slice", fmt.Sprintf("kubepods-pod%s.slice", uid))
	case "Burstable":
		cgroupPath = filepath.Join(t.cgroupPath, "kubepods.slice", "kubepods-burstable.slice", fmt.Sprintf("kubepods-burstable-pod%s.slice", uid))
	case "BestEffort":
		cgroupPath = filepath.Join(t.cgroupPath, "kubepods.slice", "kubepods-besteffort.slice", fmt.Sprintf("kubepods-besteffort-pod%s.slice", uid))
	default:
		cgroupPath = filepath.Join(t.cgroupPath, "kubepods.slice", fmt.Sprintf("kubepods-pod%s.slice", uid))
	}

	// 检查路径是否存在
	if _, err := os.Stat(cgroupPath); os.IsNotExist(err) {
		return "", fmt.Errorf("cgroup path not found: %s", cgroupPath)
	}

	return cgroupPath, nil
}

// getBlockDevices 获取块设备列表
func (t *Toolbox) getBlockDevices() ([]string, error) {
	var devices []string

	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		name := entry.Name()
		// 只处理 nvme、sd、vd 设备
		if strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "vd") {
			// 读取设备号
			devPath := filepath.Join("/sys/block", name, "dev")
			data, err := os.ReadFile(devPath)
			if err != nil {
				continue
			}
			devNum := strings.TrimSpace(string(data))
			devices = append(devices, devNum)
		}
	}

	return devices, nil
}

// buildIOLimit 构建 io.max 限制字符串
func (t *Toolbox) buildIOLimit(device string, readIOPS, writeIOPS, readBPS, writeBPS int64) string {
	parts := []string{device}

	if readIOPS > 0 {
		parts = append(parts, fmt.Sprintf("riops=%d", readIOPS))
	} else {
		parts = append(parts, "riops=max")
	}

	if writeIOPS > 0 {
		parts = append(parts, fmt.Sprintf("wiops=%d", writeIOPS))
	} else {
		parts = append(parts, "wiops=max")
	}

	if readBPS > 0 {
		parts = append(parts, fmt.Sprintf("rbps=%d", readBPS))
	} else {
		parts = append(parts, "rbps=max")
	}

	if writeBPS > 0 {
		parts = append(parts, fmt.Sprintf("wbps=%d", writeBPS))
	} else {
		parts = append(parts, "wbps=max")
	}

	return strings.Join(parts, " ")
}

// writeToFile 写入文件
func (t *Toolbox) writeToFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(content)
	return err
}

// cordonNodeWithKubectl 使用 kubectl cordon
func (t *Toolbox) cordonNodeWithKubectl() error {
	cmd := exec.Command("kubectl", "cordon", t.nodeName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl cordon failed: %s", string(output))
	}
	return nil
}

// uncordonNodeWithKubectl 使用 kubectl uncordon
func (t *Toolbox) uncordonNodeWithKubectl() error {
	cmd := exec.Command("kubectl", "uncordon", t.nodeName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl uncordon failed: %s", string(output))
	}
	return nil
}

// GetPodIOStats 获取 Pod IO 统计
func (t *Toolbox) GetPodIOStats(namespace, podName string) (map[string]uint64, error) {
	cgroupPath, err := t.getPodCgroupPath(namespace, podName)
	if err != nil {
		return nil, err
	}

	ioStatPath := filepath.Join(cgroupPath, "io.stat")
	data, err := os.ReadFile(ioStatPath)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]uint64)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		for _, field := range fields[1:] {
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 {
				continue
			}
			value, _ := strconv.ParseUint(parts[1], 10, 64)
			stats[parts[0]] += value
		}
	}

	return stats, nil
}
