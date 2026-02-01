# IO Hang 模拟与 Kubernetes 状态采集实验

## 概述

本实验程序用于模拟 IO hang（进程进入 D 状态）场景，观察 Kubernetes 的 Pod 状态变化、资源绑定情况，以及测试 Pod 删除行为。

## 目录结构

```
io_hang_experiment/
├── README.md                 # 本说明文档
├── io_hang_simulator.py      # 主实验程序 (Python)
├── d_state_simulator.sh      # D 状态模拟器 (Shell)
├── k8s_io_hang_pod.yaml      # Kubernetes 测试资源
└── experiment_results/       # 实验结果输出目录
```

## 实验原理

### D 状态 (Uninterruptible Sleep)

当进程执行 IO 操作时，可能进入 **D 状态**（不可中断睡眠）：
- 进程在等待 IO 完成
- 无法被普通信号中断（包括 SIGKILL）
- 在 `ps` 输出中显示为 `D` 或 `D+`

### IO Hang 对 Kubernetes 的影响

```
┌─────────────────────────────────────────────────────────────────┐
│                    IO Hang 影响链路                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  IO Hang 发生                                                    │
│       │                                                          │
│       ▼                                                          │
│  进程进入 D 状态                                                 │
│       │                                                          │
│       ├────────────────────────────────────────────────────┐    │
│       │                                                     │    │
│       ▼                                                     ▼    │
│  容器无法正常停止                                    存活探针失败 │
│       │                                                     │    │
│       ▼                                                     ▼    │
│  Pod Terminating 状态卡住                          Pod 被标记不健康│
│       │                                                     │    │
│       ▼                                                     ▼    │
│  kubelet 无法 unmount volume                       新流量不再路由  │
│       │                                                          │
│       ▼                                                          │
│  PVC finalizer 无法移除                                          │
│       │                                                          │
│       ▼                                                          │
│  资源无法释放，新 Pod 无法调度                                    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 快速开始

### 前置条件

- Linux 系统（推荐 Ubuntu 20.04+）
- Root 权限
- Python 3.8+
- kubectl 已配置
- Kubernetes 集群可访问

### 安装依赖

```bash
# Python 依赖（标准库，无需额外安装）
python3 --version  # 确保 >= 3.8

# 系统工具检查
which kubectl iostat dmsetup losetup
```

### 运行实验

#### 方式 1: 使用 Python 主程序（推荐）

```bash
# 完整实验
sudo python3 io_hang_simulator.py --mode full --namespace test-io-hang

# 仅采集 Kubernetes 状态数据
python3 io_hang_simulator.py --mode collect-only --namespace test-io-hang

# 清理资源
python3 io_hang_simulator.py --mode cleanup --namespace test-io-hang
```

#### 方式 2: 使用 Shell 脚本手动模拟

```bash
# 终端 1: 启动 D 状态模拟
sudo ./d_state_simulator.sh dm-delay --duration 120

# 终端 2: 监控 D 状态进程
sudo ./d_state_simulator.sh monitor --interval 2

# 终端 3: 部署 Kubernetes 测试资源
kubectl apply -f k8s_io_hang_pod.yaml
kubectl get pods -n io-hang-test -w
```

## 实验场景

### 场景 1: 基础 IO Hang 观察

**目的**: 观察进程进入 D 状态的过程

```bash
# 1. 创建 dm-delay 设备
sudo ./d_state_simulator.sh dm-delay --duration 60

# 2. 在另一个终端观察
watch -n1 "ps aux | grep ' D '"
```

### 场景 2: Kubernetes Pod 删除测试

**目的**: 测试 IO hang 场景下 Pod 删除行为

```bash
# 1. 部署测试 Pod
kubectl apply -f k8s_io_hang_pod.yaml

# 2. 等待 Pod 运行
kubectl wait --for=condition=Ready pod/io-load-generator -n io-hang-test --timeout=60s

# 3. 在节点上触发 IO hang
sudo ./d_state_simulator.sh dm-delay --duration 120

# 4. 尝试删除 Pod
kubectl delete pod io-load-generator -n io-hang-test

# 5. 观察 Pod 状态
kubectl get pod -n io-hang-test -w

# 6. 如果 Pod 卡在 Terminating，强制删除
kubectl delete pod io-load-generator -n io-hang-test --force --grace-period=0
```

### 场景 3: PVC 资源释放测试

**目的**: 验证 IO hang 对 PVC 释放的影响

```bash
# 1. 部署带 PVC 的 Pod
kubectl apply -f k8s_io_hang_pod.yaml

# 2. 触发 IO hang 后删除 Pod
kubectl delete pod io-load-generator -n io-hang-test

# 3. 检查 PVC 状态
kubectl get pvc -n io-hang-test

# 4. 检查 PVC finalizers
kubectl get pvc io-hang-test-pvc -n io-hang-test -o jsonpath='{.metadata.finalizers}'

# 5. 如果需要强制释放，移除 finalizer
kubectl patch pvc io-hang-test-pvc -n io-hang-test -p '{"metadata":{"finalizers":null}}'
```

## 观察指标

### D 状态进程监控

```bash
# 查看 D 状态进程数量
cat /proc/stat | grep procs_blocked

# 列出所有 D 状态进程
ps aux | awk '$8 ~ /D/ {print}'

# 查看进程等待通道
for pid in $(ps aux | awk '$8 ~ /D/ {print $2}'); do
    echo "PID $pid: $(cat /proc/$pid/wchan 2>/dev/null)"
done

# 查看进程堆栈
cat /proc/<PID>/stack
```

### Kubernetes 资源状态

```bash
# Pod 状态
kubectl get pods -n io-hang-test -o wide

# Pod 详细信息
kubectl describe pod io-load-generator -n io-hang-test

# PVC 状态
kubectl get pvc -n io-hang-test -o yaml

# 事件
kubectl get events -n io-hang-test --sort-by='.lastTimestamp'

# 节点状态
kubectl get nodes -o wide
kubectl describe node <node-name>
```

### IO 统计

```bash
# 磁盘 IO 统计
iostat -x 1

# 块设备队列
cat /sys/block/nvme0n1/queue/nr_requests
cat /sys/block/nvme0n1/inflight
```

## 实验结果分析

实验完成后，结果将保存在 `experiment_results/` 目录：

- `experiment_result_<timestamp>.json` - 完整数据
- `experiment_report_<timestamp>.md` - Markdown 报告

### 关键指标解读

| 指标 | 正常值 | 异常值 | 说明 |
|:-----|:-------|:-------|:-----|
| D 状态进程数 | 0-2 | >5 | 大量 D 状态进程表示 IO 问题 |
| Pod 删除时间 | <30s | >60s | 超时可能是 unmount 失败 |
| PVC finalizer | 已移除 | 存在 | finalizer 残留表示清理未完成 |
| procs_blocked | 0-1 | >5 | 内核级别的阻塞进程计数 |

## 故障恢复

### 强制清理 Pod

```bash
# 强制删除 Terminating Pod
kubectl delete pod <pod-name> -n io-hang-test --force --grace-period=0

# 移除 Pod finalizer
kubectl patch pod <pod-name> -n io-hang-test -p '{"metadata":{"finalizers":null}}'
```

### 强制清理 PVC

```bash
# 移除 PVC finalizer
kubectl patch pvc <pvc-name> -n io-hang-test -p '{"metadata":{"finalizers":null}}'

# 强制删除 PVC
kubectl delete pvc <pvc-name> -n io-hang-test --force --grace-period=0
```

### 节点恢复

```bash
# 如果节点有大量 D 状态进程，可能需要重启
# 首先驱逐 Pod
kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data --force

# 标记节点不可调度
kubectl cordon <node-name>

# 重启节点（最后手段）
ssh <node> "sudo reboot"
```

### 清理测试资源

```bash
# 使用清理脚本
kubectl exec -it io-monitor -n io-hang-test -- sh /scripts/cleanup.sh

# 或手动清理
kubectl delete namespace io-hang-test --force --grace-period=0
```

## 安全注意事项

⚠️ **警告**：本实验可能导致系统不稳定！

1. **仅在测试环境运行** - 不要在生产环境执行
2. **准备回滚方案** - 确保可以重启节点
3. **限制影响范围** - 使用独立的命名空间和节点
4. **监控系统状态** - 随时准备中断实验
5. **保存重要数据** - 实验前备份关键数据

## 常见问题

### Q: 实验后节点无法恢复怎么办？

A: 尝试以下步骤：
1. 运行 `sudo ./d_state_simulator.sh cleanup`
2. 强制卸载文件系统 `umount -l /mnt/*`
3. 如果仍无法恢复，重启节点

### Q: Pod 一直处于 Terminating 状态？

A: 
1. 首先检查是否有 D 状态进程
2. 使用 `kubectl delete pod --force --grace-period=0`
3. 如果有 finalizer，使用 patch 移除

### Q: 如何模拟更真实的 IO hang？

A: 
1. 使用 `dm-delay` 方法最接近真实场景
2. 也可以通过阻断 NFS/iSCSI 网络连接
3. 或者使用故障注入工具如 chaos-mesh

## 参考资料

- [Linux D 状态进程解析](https://www.kernel.org/doc/html/latest/admin-guide/sysrq.html)
- [Kubernetes Pod 生命周期](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)
- [Device Mapper 文档](https://www.kernel.org/doc/html/latest/admin-guide/device-mapper/)
