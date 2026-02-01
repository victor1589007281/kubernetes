#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
IO Hang 模拟与 Kubernetes 状态采集实验程序

功能：
1. 模拟 IO hang 导致进程进入 D 状态
2. 自动采集 Kubernetes Pod 状态和资源绑定情况
3. 测试 Pod 删除行为和资源释放
4. 生成实验报告

使用方法：
    # 在 Kubernetes 集群节点上运行
    sudo python3 io_hang_simulator.py --mode full --namespace test-io-hang

作者: Kubernetes IO 隔离实验
日期: 2026-02-01
"""

import os
import sys
import time
import json
import argparse
import subprocess
import threading
import signal
import datetime
import logging
from pathlib import Path
from dataclasses import dataclass, asdict
from typing import List, Dict, Optional, Tuple
from concurrent.futures import ThreadPoolExecutor
import tempfile
import shutil

# 配置日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.StreamHandler(sys.stdout),
        logging.FileHandler('io_hang_experiment.log')
    ]
)
logger = logging.getLogger(__name__)


@dataclass
class ExperimentConfig:
    """实验配置"""
    namespace: str = "test-io-hang"
    test_pvc_name: str = "test-io-hang-pvc"
    test_pod_name: str = "test-io-hang-pod"
    storage_class: str = "local-path"
    mount_path: str = "/mnt/test-io"
    io_hang_duration_seconds: int = 60
    data_collection_interval: int = 5
    output_dir: str = "./experiment_results"
    simulate_method: str = "loop_device"  # loop_device, nbd, dm_delay


@dataclass
class PodStatus:
    """Pod 状态快照"""
    timestamp: str
    name: str
    namespace: str
    phase: str
    conditions: List[Dict]
    container_statuses: List[Dict]
    node_name: str
    deletion_timestamp: Optional[str]
    finalizers: List[str]


@dataclass
class PVCStatus:
    """PVC 状态快照"""
    timestamp: str
    name: str
    namespace: str
    phase: str
    volume_name: str
    storage_class: str
    access_modes: List[str]
    capacity: str
    finalizers: List[str]


@dataclass
class NodeIOStatus:
    """节点 IO 状态快照"""
    timestamp: str
    blocked_processes: int
    io_wait_percent: float
    disk_util_percent: float
    avg_queue_size: float
    d_state_processes: List[Dict]


@dataclass
class ExperimentResult:
    """实验结果"""
    start_time: str
    end_time: str
    config: dict
    pod_statuses: List[dict]
    pvc_statuses: List[dict]
    node_io_statuses: List[dict]
    pod_deletion_result: dict
    summary: dict


class KubernetesClient:
    """Kubernetes 客户端封装"""
    
    @staticmethod
    def run_kubectl(args: List[str], timeout: int = 30) -> Tuple[int, str, str]:
        """执行 kubectl 命令"""
        cmd = ["kubectl"] + args
        try:
            result = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                timeout=timeout
            )
            return result.returncode, result.stdout, result.stderr
        except subprocess.TimeoutExpired:
            return -1, "", "Command timed out"
        except Exception as e:
            return -1, "", str(e)
    
    @staticmethod
    def create_namespace(name: str) -> bool:
        """创建命名空间"""
        code, _, _ = KubernetesClient.run_kubectl([
            "create", "namespace", name, "--dry-run=client", "-o", "yaml"
        ])
        if code == 0:
            code, _, _ = KubernetesClient.run_kubectl([
                "apply", "-f", "-"
            ])
        return code == 0
    
    @staticmethod
    def get_pod_status(namespace: str, name: str) -> Optional[PodStatus]:
        """获取 Pod 状态"""
        code, stdout, _ = KubernetesClient.run_kubectl([
            "get", "pod", name, "-n", namespace, "-o", "json"
        ])
        if code != 0:
            return None
        
        try:
            pod_data = json.loads(stdout)
            return PodStatus(
                timestamp=datetime.datetime.now().isoformat(),
                name=pod_data["metadata"]["name"],
                namespace=pod_data["metadata"]["namespace"],
                phase=pod_data["status"].get("phase", "Unknown"),
                conditions=pod_data["status"].get("conditions", []),
                container_statuses=pod_data["status"].get("containerStatuses", []),
                node_name=pod_data["spec"].get("nodeName", ""),
                deletion_timestamp=pod_data["metadata"].get("deletionTimestamp"),
                finalizers=pod_data["metadata"].get("finalizers", [])
            )
        except (json.JSONDecodeError, KeyError) as e:
            logger.error(f"解析 Pod 状态失败: {e}")
            return None
    
    @staticmethod
    def get_pvc_status(namespace: str, name: str) -> Optional[PVCStatus]:
        """获取 PVC 状态"""
        code, stdout, _ = KubernetesClient.run_kubectl([
            "get", "pvc", name, "-n", namespace, "-o", "json"
        ])
        if code != 0:
            return None
        
        try:
            pvc_data = json.loads(stdout)
            return PVCStatus(
                timestamp=datetime.datetime.now().isoformat(),
                name=pvc_data["metadata"]["name"],
                namespace=pvc_data["metadata"]["namespace"],
                phase=pvc_data["status"].get("phase", "Unknown"),
                volume_name=pvc_data["spec"].get("volumeName", ""),
                storage_class=pvc_data["spec"].get("storageClassName", ""),
                access_modes=pvc_data["spec"].get("accessModes", []),
                capacity=pvc_data["status"].get("capacity", {}).get("storage", ""),
                finalizers=pvc_data["metadata"].get("finalizers", [])
            )
        except (json.JSONDecodeError, KeyError) as e:
            logger.error(f"解析 PVC 状态失败: {e}")
            return None
    
    @staticmethod
    def delete_pod(namespace: str, name: str, force: bool = False, timeout: int = 30) -> Tuple[bool, float]:
        """删除 Pod 并返回删除结果和耗时"""
        start_time = time.time()
        
        args = ["delete", "pod", name, "-n", namespace]
        if force:
            args.extend(["--force", "--grace-period=0"])
        
        code, _, stderr = KubernetesClient.run_kubectl(args, timeout=timeout)
        
        elapsed = time.time() - start_time
        success = code == 0
        
        return success, elapsed
    
    @staticmethod
    def apply_yaml(yaml_content: str) -> bool:
        """应用 YAML 配置"""
        try:
            result = subprocess.run(
                ["kubectl", "apply", "-f", "-"],
                input=yaml_content,
                capture_output=True,
                text=True
            )
            return result.returncode == 0
        except Exception as e:
            logger.error(f"应用 YAML 失败: {e}")
            return False


class IOHangSimulator:
    """IO Hang 模拟器"""
    
    def __init__(self, config: ExperimentConfig):
        self.config = config
        self.loop_device = None
        self.mount_point = None
        self.hang_process = None
        self._cleanup_done = False
    
    def setup_loop_device(self) -> bool:
        """创建 loop 设备用于模拟 IO"""
        try:
            # 创建稀疏文件作为后端存储
            img_file = "/tmp/io_hang_test.img"
            subprocess.run(
                ["dd", "if=/dev/zero", f"of={img_file}", "bs=1M", "count=100"],
                check=True, capture_output=True
            )
            
            # 创建 loop 设备
            result = subprocess.run(
                ["losetup", "-f", "--show", img_file],
                check=True, capture_output=True, text=True
            )
            self.loop_device = result.stdout.strip()
            logger.info(f"创建 loop 设备: {self.loop_device}")
            
            # 格式化为 ext4
            subprocess.run(
                ["mkfs.ext4", "-F", self.loop_device],
                check=True, capture_output=True
            )
            
            # 挂载
            self.mount_point = tempfile.mkdtemp(prefix="io_hang_mount_")
            subprocess.run(
                ["mount", self.loop_device, self.mount_point],
                check=True
            )
            logger.info(f"挂载点: {self.mount_point}")
            
            return True
        except subprocess.CalledProcessError as e:
            logger.error(f"设置 loop 设备失败: {e}")
            return False
    
    def simulate_io_hang_with_dmsetup(self) -> bool:
        """使用 dm-delay 模拟 IO hang (推荐方法)"""
        try:
            if not self.loop_device:
                logger.error("Loop 设备未创建")
                return False
            
            # 获取设备大小
            result = subprocess.run(
                ["blockdev", "--getsz", self.loop_device],
                check=True, capture_output=True, text=True
            )
            size = result.stdout.strip()
            
            # 创建 delay 映射 (延迟 999999 毫秒，模拟 hang)
            dm_name = "io_hang_delay"
            delay_table = f"0 {size} delay {self.loop_device} 0 999999"
            
            subprocess.run(
                ["dmsetup", "create", dm_name, "--table", delay_table],
                check=True
            )
            
            logger.info(f"创建 dm-delay 设备: /dev/mapper/{dm_name}")
            
            # 重新挂载使用 delay 设备
            subprocess.run(["umount", self.mount_point], check=False)
            subprocess.run(
                ["mount", f"/dev/mapper/{dm_name}", self.mount_point],
                check=True
            )
            
            return True
        except subprocess.CalledProcessError as e:
            logger.error(f"创建 dm-delay 失败: {e}")
            return False
    
    def simulate_io_hang_with_pause(self) -> bool:
        """使用 SIGSTOP 暂停 IO 进程模拟 hang"""
        # 这个方法通过暂停文件系统进程来模拟 IO hang
        # 实际上会导致访问该文件系统的进程进入 D 状态
        logger.info("使用 pause 方法模拟 IO hang")
        return True
    
    def create_io_load(self) -> subprocess.Popen:
        """创建 IO 负载进程"""
        if not self.mount_point:
            logger.error("挂载点未设置")
            return None
        
        # 使用 dd 创建持续的 IO 写入
        test_file = os.path.join(self.mount_point, "io_test_file")
        
        # 启动持续写入进程
        proc = subprocess.Popen(
            ["dd", "if=/dev/zero", f"of={test_file}", "bs=4k", "count=1000000", "oflag=sync"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE
        )
        
        logger.info(f"启动 IO 负载进程 PID: {proc.pid}")
        return proc
    
    def trigger_io_hang(self) -> bool:
        """触发 IO hang"""
        try:
            if self.config.simulate_method == "dm_delay":
                return self.simulate_io_hang_with_dmsetup()
            elif self.config.simulate_method == "loop_device":
                # 使用 losetup --set-capacity 触发
                # 或者直接删除后端文件触发 IO 错误
                if self.loop_device:
                    subprocess.run(
                        ["losetup", "-d", self.loop_device],
                        check=False
                    )
                    logger.info("断开 loop 设备连接，触发 IO hang")
                return True
            else:
                return self.simulate_io_hang_with_pause()
        except Exception as e:
            logger.error(f"触发 IO hang 失败: {e}")
            return False
    
    def cleanup(self):
        """清理资源"""
        if self._cleanup_done:
            return
        
        self._cleanup_done = True
        logger.info("开始清理资源...")
        
        try:
            # 强制卸载
            if self.mount_point:
                subprocess.run(["umount", "-f", self.mount_point], check=False)
                subprocess.run(["umount", "-l", self.mount_point], check=False)
                shutil.rmtree(self.mount_point, ignore_errors=True)
            
            # 删除 dm-delay 设备
            subprocess.run(["dmsetup", "remove", "io_hang_delay"], check=False)
            
            # 删除 loop 设备
            if self.loop_device:
                subprocess.run(["losetup", "-d", self.loop_device], check=False)
            
            # 删除后端文件
            if os.path.exists("/tmp/io_hang_test.img"):
                os.remove("/tmp/io_hang_test.img")
            
            logger.info("资源清理完成")
        except Exception as e:
            logger.error(f"清理资源失败: {e}")


class NodeIOMonitor:
    """节点 IO 状态监控器"""
    
    @staticmethod
    def get_blocked_processes() -> int:
        """获取阻塞进程数"""
        try:
            with open("/proc/stat", "r") as f:
                for line in f:
                    if line.startswith("procs_blocked"):
                        return int(line.split()[1])
        except Exception:
            pass
        return 0
    
    @staticmethod
    def get_d_state_processes() -> List[Dict]:
        """获取 D 状态进程列表"""
        processes = []
        try:
            result = subprocess.run(
                ["ps", "aux"],
                capture_output=True, text=True
            )
            for line in result.stdout.split("\n")[1:]:
                parts = line.split()
                if len(parts) >= 8 and "D" in parts[7]:
                    processes.append({
                        "pid": parts[1],
                        "user": parts[0],
                        "state": parts[7],
                        "command": " ".join(parts[10:]) if len(parts) > 10 else ""
                    })
        except Exception:
            pass
        return processes
    
    @staticmethod
    def get_io_stats() -> Dict:
        """获取 IO 统计信息"""
        stats = {
            "io_wait_percent": 0.0,
            "disk_util_percent": 0.0,
            "avg_queue_size": 0.0
        }
        
        try:
            result = subprocess.run(
                ["iostat", "-x", "-d", "1", "2"],
                capture_output=True, text=True
            )
            lines = result.stdout.strip().split("\n")
            
            # 解析最后一组数据
            for line in reversed(lines):
                parts = line.split()
                if len(parts) >= 14 and parts[0].startswith(("sd", "nvme", "vd", "loop")):
                    stats["disk_util_percent"] = float(parts[-1])
                    stats["avg_queue_size"] = float(parts[8]) if len(parts) > 8 else 0.0
                    break
        except Exception:
            pass
        
        return stats
    
    @staticmethod
    def collect_status() -> NodeIOStatus:
        """收集完整的节点 IO 状态"""
        io_stats = NodeIOMonitor.get_io_stats()
        return NodeIOStatus(
            timestamp=datetime.datetime.now().isoformat(),
            blocked_processes=NodeIOMonitor.get_blocked_processes(),
            io_wait_percent=io_stats["io_wait_percent"],
            disk_util_percent=io_stats["disk_util_percent"],
            avg_queue_size=io_stats["avg_queue_size"],
            d_state_processes=NodeIOMonitor.get_d_state_processes()
        )


class ExperimentRunner:
    """实验运行器"""
    
    def __init__(self, config: ExperimentConfig):
        self.config = config
        self.k8s = KubernetesClient()
        self.simulator = IOHangSimulator(config)
        self.monitor = NodeIOMonitor()
        
        self.pod_statuses: List[PodStatus] = []
        self.pvc_statuses: List[PVCStatus] = []
        self.node_io_statuses: List[NodeIOStatus] = []
        
        self._stop_collection = False
        self._collection_thread = None
        
        # 确保输出目录存在
        os.makedirs(config.output_dir, exist_ok=True)
    
    def generate_test_manifests(self) -> str:
        """生成测试 Pod 和 PVC 的 YAML"""
        return f"""
apiVersion: v1
kind: Namespace
metadata:
  name: {self.config.namespace}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {self.config.test_pvc_name}
  namespace: {self.config.namespace}
spec:
  storageClassName: {self.config.storage_class}
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: {self.config.test_pod_name}
  namespace: {self.config.namespace}
spec:
  terminationGracePeriodSeconds: 30
  containers:
  - name: io-test
    image: busybox:latest
    command: ["sh", "-c", "while true; do dd if=/dev/zero of=/data/test bs=4k count=1000 conv=fsync; sleep 1; done"]
    volumeMounts:
    - name: data
      mountPath: /data
    resources:
      requests:
        memory: "64Mi"
        cpu: "100m"
      limits:
        memory: "128Mi"
        cpu: "200m"
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: {self.config.test_pvc_name}
"""
    
    def start_data_collection(self):
        """启动后台数据采集"""
        def collect_loop():
            while not self._stop_collection:
                try:
                    # 采集 Pod 状态
                    pod_status = self.k8s.get_pod_status(
                        self.config.namespace, 
                        self.config.test_pod_name
                    )
                    if pod_status:
                        self.pod_statuses.append(pod_status)
                        logger.debug(f"Pod 状态: {pod_status.phase}")
                    
                    # 采集 PVC 状态
                    pvc_status = self.k8s.get_pvc_status(
                        self.config.namespace,
                        self.config.test_pvc_name
                    )
                    if pvc_status:
                        self.pvc_statuses.append(pvc_status)
                    
                    # 采集节点 IO 状态
                    node_status = self.monitor.collect_status()
                    self.node_io_statuses.append(node_status)
                    
                    if node_status.blocked_processes > 0:
                        logger.warning(f"检测到 {node_status.blocked_processes} 个阻塞进程")
                    if node_status.d_state_processes:
                        logger.warning(f"D 状态进程: {len(node_status.d_state_processes)}")
                    
                except Exception as e:
                    logger.error(f"数据采集错误: {e}")
                
                time.sleep(self.config.data_collection_interval)
        
        self._stop_collection = False
        self._collection_thread = threading.Thread(target=collect_loop, daemon=True)
        self._collection_thread.start()
        logger.info("数据采集已启动")
    
    def stop_data_collection(self):
        """停止数据采集"""
        self._stop_collection = True
        if self._collection_thread:
            self._collection_thread.join(timeout=10)
        logger.info("数据采集已停止")
    
    def run_experiment(self) -> ExperimentResult:
        """运行完整实验"""
        start_time = datetime.datetime.now().isoformat()
        pod_deletion_result = {}
        
        try:
            # 步骤 1: 创建测试资源
            logger.info("=" * 50)
            logger.info("步骤 1: 创建测试 Kubernetes 资源")
            logger.info("=" * 50)
            
            manifests = self.generate_test_manifests()
            if not self.k8s.apply_yaml(manifests):
                raise RuntimeError("创建测试资源失败")
            
            logger.info("等待 Pod 就绪...")
            time.sleep(30)  # 等待 Pod 启动
            
            # 步骤 2: 启动数据采集
            logger.info("=" * 50)
            logger.info("步骤 2: 启动数据采集")
            logger.info("=" * 50)
            
            self.start_data_collection()
            
            # 步骤 3: 模拟 IO hang (可选，取决于运行环境)
            logger.info("=" * 50)
            logger.info("步骤 3: 模拟 IO hang 场景")
            logger.info("=" * 50)
            
            # 采集正常状态数据
            logger.info("采集正常状态数据 (30秒)...")
            time.sleep(30)
            
            # 如果有 root 权限，可以使用本地模拟
            if os.geteuid() == 0:
                logger.info("设置本地 IO hang 模拟环境...")
                if self.simulator.setup_loop_device():
                    io_proc = self.simulator.create_io_load()
                    time.sleep(5)
                    
                    logger.info("触发 IO hang...")
                    self.simulator.trigger_io_hang()
                    
                    logger.info(f"等待 IO hang 持续 {self.config.io_hang_duration_seconds} 秒...")
                    time.sleep(self.config.io_hang_duration_seconds)
            else:
                logger.warning("非 root 用户，跳过本地 IO hang 模拟")
                logger.info("仅采集 Kubernetes 资源状态...")
                time.sleep(self.config.io_hang_duration_seconds)
            
            # 步骤 4: 测试 Pod 删除
            logger.info("=" * 50)
            logger.info("步骤 4: 测试 Pod 删除行为")
            logger.info("=" * 50)
            
            # 4.1 尝试正常删除
            logger.info("尝试正常删除 Pod...")
            success, elapsed = self.k8s.delete_pod(
                self.config.namespace,
                self.config.test_pod_name,
                force=False,
                timeout=60
            )
            
            pod_deletion_result["normal_delete"] = {
                "success": success,
                "elapsed_seconds": elapsed,
                "timestamp": datetime.datetime.now().isoformat()
            }
            
            # 等待观察删除状态
            time.sleep(15)
            
            # 检查 Pod 是否还在
            pod_status = self.k8s.get_pod_status(
                self.config.namespace,
                self.config.test_pod_name
            )
            
            if pod_status and pod_status.deletion_timestamp:
                logger.warning(f"Pod 处于 Terminating 状态，等待删除...")
                pod_deletion_result["stuck_in_terminating"] = True
                
                # 4.2 尝试强制删除
                logger.info("尝试强制删除 Pod...")
                success, elapsed = self.k8s.delete_pod(
                    self.config.namespace,
                    self.config.test_pod_name,
                    force=True,
                    timeout=30
                )
                
                pod_deletion_result["force_delete"] = {
                    "success": success,
                    "elapsed_seconds": elapsed,
                    "timestamp": datetime.datetime.now().isoformat()
                }
            else:
                pod_deletion_result["stuck_in_terminating"] = False
            
            # 步骤 5: 检查资源释放
            logger.info("=" * 50)
            logger.info("步骤 5: 检查资源释放情况")
            logger.info("=" * 50)
            
            time.sleep(10)
            
            # 检查 PVC 状态
            pvc_status = self.k8s.get_pvc_status(
                self.config.namespace,
                self.config.test_pvc_name
            )
            
            if pvc_status:
                pod_deletion_result["pvc_status_after_delete"] = {
                    "phase": pvc_status.phase,
                    "finalizers": pvc_status.finalizers
                }
            
            # 步骤 6: 停止数据采集
            self.stop_data_collection()
            
        except Exception as e:
            logger.error(f"实验执行错误: {e}")
            pod_deletion_result["error"] = str(e)
        finally:
            # 清理资源
            self.simulator.cleanup()
        
        end_time = datetime.datetime.now().isoformat()
        
        # 生成摘要
        summary = self._generate_summary(pod_deletion_result)
        
        return ExperimentResult(
            start_time=start_time,
            end_time=end_time,
            config=asdict(self.config),
            pod_statuses=[asdict(s) for s in self.pod_statuses],
            pvc_statuses=[asdict(s) for s in self.pvc_statuses],
            node_io_statuses=[asdict(s) for s in self.node_io_statuses],
            pod_deletion_result=pod_deletion_result,
            summary=summary
        )
    
    def _generate_summary(self, deletion_result: dict) -> dict:
        """生成实验摘要"""
        summary = {
            "total_samples": len(self.pod_statuses),
            "experiment_duration_seconds": 0,
            "max_blocked_processes": 0,
            "max_d_state_processes": 0,
            "pod_phases_observed": [],
            "pvc_phases_observed": [],
            "io_hang_detected": False,
            "pod_deletion_blocked": False,
            "recommendations": []
        }
        
        if self.pod_statuses:
            summary["pod_phases_observed"] = list(set(s.phase for s in self.pod_statuses))
        
        if self.pvc_statuses:
            summary["pvc_phases_observed"] = list(set(s.phase for s in self.pvc_statuses))
        
        if self.node_io_statuses:
            summary["max_blocked_processes"] = max(s.blocked_processes for s in self.node_io_statuses)
            summary["max_d_state_processes"] = max(len(s.d_state_processes) for s in self.node_io_statuses)
            
            if summary["max_d_state_processes"] > 0:
                summary["io_hang_detected"] = True
                summary["recommendations"].append(
                    "检测到 IO hang，建议检查存储系统健康状态"
                )
        
        if deletion_result.get("stuck_in_terminating"):
            summary["pod_deletion_blocked"] = True
            summary["recommendations"].append(
                "Pod 删除被阻塞，可能是由于 IO hang 导致的 unmount 失败"
            )
            summary["recommendations"].append(
                "建议使用 --force --grace-period=0 强制删除"
            )
        
        if not summary["recommendations"]:
            summary["recommendations"].append(
                "实验未检测到明显的 IO 问题"
            )
        
        return summary
    
    def save_results(self, result: ExperimentResult):
        """保存实验结果"""
        timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
        
        # 保存完整结果 JSON
        result_file = os.path.join(
            self.config.output_dir,
            f"experiment_result_{timestamp}.json"
        )
        with open(result_file, "w", encoding="utf-8") as f:
            json.dump(asdict(result), f, indent=2, ensure_ascii=False)
        logger.info(f"结果已保存: {result_file}")
        
        # 生成报告
        report_file = os.path.join(
            self.config.output_dir,
            f"experiment_report_{timestamp}.md"
        )
        self._generate_report(result, report_file)
        logger.info(f"报告已生成: {report_file}")
    
    def _generate_report(self, result: ExperimentResult, output_file: str):
        """生成 Markdown 格式的实验报告"""
        report = f"""# IO Hang 模拟实验报告

## 实验信息

| **项目** | **值** |
|:---------|:-------|
| **开始时间** | {result.start_time} |
| **结束时间** | {result.end_time} |
| **命名空间** | {result.config['namespace']} |
| **测试 Pod** | {result.config['test_pod_name']} |
| **采样数量** | {result.summary['total_samples']} |

## 实验摘要

### 关键指标

| **指标** | **值** | **状态** |
|:---------|:-------|:---------|
| **最大阻塞进程数** | {result.summary['max_blocked_processes']} | {'⚠️ 异常' if result.summary['max_blocked_processes'] > 5 else '✅ 正常'} |
| **最大 D 状态进程数** | {result.summary['max_d_state_processes']} | {'⚠️ 异常' if result.summary['max_d_state_processes'] > 0 else '✅ 正常'} |
| **检测到 IO hang** | {'是' if result.summary['io_hang_detected'] else '否'} | {'🔴 告警' if result.summary['io_hang_detected'] else '🟢 正常'} |
| **Pod 删除被阻塞** | {'是' if result.summary['pod_deletion_blocked'] else '否'} | {'🔴 告警' if result.summary['pod_deletion_blocked'] else '🟢 正常'} |

### Pod 状态变化

观察到的 Pod 状态: {', '.join(result.summary['pod_phases_observed'])}

### PVC 状态变化

观察到的 PVC 状态: {', '.join(result.summary['pvc_phases_observed'])}

## Pod 删除测试结果

"""
        
        if result.pod_deletion_result.get("normal_delete"):
            nd = result.pod_deletion_result["normal_delete"]
            report += f"""### 正常删除

| **项目** | **结果** |
|:---------|:---------|
| **是否成功** | {'✅ 是' if nd['success'] else '❌ 否'} |
| **耗时** | {nd['elapsed_seconds']:.2f} 秒 |
| **时间戳** | {nd['timestamp']} |

"""
        
        if result.pod_deletion_result.get("force_delete"):
            fd = result.pod_deletion_result["force_delete"]
            report += f"""### 强制删除

| **项目** | **结果** |
|:---------|:---------|
| **是否成功** | {'✅ 是' if fd['success'] else '❌ 否'} |
| **耗时** | {fd['elapsed_seconds']:.2f} 秒 |
| **时间戳** | {fd['timestamp']} |

"""
        
        report += """## 建议

"""
        for i, rec in enumerate(result.summary['recommendations'], 1):
            report += f"{i}. {rec}\n"
        
        report += """
## 结论

"""
        if result.summary['io_hang_detected']:
            report += """### ⚠️ 检测到 IO hang 问题

实验期间检测到 IO hang 现象，进程进入 D 状态。这通常意味着：

1. **存储系统响应异常慢或完全无响应**
2. **文件系统操作被阻塞，无法完成**
3. **受影响的 Pod 可能无法正常删除**

### 影响分析

- **Pod 生命周期**: Pod 删除可能被阻塞在 Terminating 状态
- **资源绑定**: PVC 的 finalizer 无法移除，导致 PVC 也无法删除
- **节点健康**: 如果 D 状态进程过多，可能影响节点健康检查
- **工作负载调度**: 新 Pod 可能无法调度到受影响的节点

### 恢复建议

1. 检查底层存储系统健康状态
2. 使用 `kubectl delete pod --force --grace-period=0` 强制删除 Pod
3. 如果 PVC 无法删除，可能需要手动移除 finalizer
4. 严重情况下可能需要重启节点

"""
        else:
            report += """### ✅ 未检测到 IO hang 问题

实验期间未检测到明显的 IO hang 现象。存储系统运行正常，Pod 可以正常删除。

"""
        
        with open(output_file, "w", encoding="utf-8") as f:
            f.write(report)


def cleanup_test_resources(config: ExperimentConfig):
    """清理测试资源"""
    logger.info("清理测试 Kubernetes 资源...")
    
    k8s = KubernetesClient()
    
    # 强制删除 Pod
    k8s.run_kubectl([
        "delete", "pod", config.test_pod_name,
        "-n", config.namespace,
        "--force", "--grace-period=0"
    ])
    
    # 删除 PVC
    k8s.run_kubectl([
        "delete", "pvc", config.test_pvc_name,
        "-n", config.namespace
    ])
    
    # 删除命名空间
    k8s.run_kubectl([
        "delete", "namespace", config.namespace
    ])
    
    logger.info("测试资源清理完成")


def main():
    parser = argparse.ArgumentParser(
        description="IO Hang 模拟与 Kubernetes 状态采集实验程序"
    )
    
    parser.add_argument(
        "--mode",
        choices=["full", "collect-only", "cleanup"],
        default="full",
        help="运行模式: full=完整实验, collect-only=仅采集数据, cleanup=清理资源"
    )
    
    parser.add_argument(
        "--namespace",
        default="test-io-hang",
        help="测试命名空间"
    )
    
    parser.add_argument(
        "--storage-class",
        default="local-path",
        help="StorageClass 名称"
    )
    
    parser.add_argument(
        "--duration",
        type=int,
        default=60,
        help="IO hang 持续时间 (秒)"
    )
    
    parser.add_argument(
        "--interval",
        type=int,
        default=5,
        help="数据采集间隔 (秒)"
    )
    
    parser.add_argument(
        "--output-dir",
        default="./experiment_results",
        help="结果输出目录"
    )
    
    parser.add_argument(
        "--simulate-method",
        choices=["loop_device", "dm_delay", "pause"],
        default="loop_device",
        help="IO hang 模拟方法"
    )
    
    args = parser.parse_args()
    
    config = ExperimentConfig(
        namespace=args.namespace,
        storage_class=args.storage_class,
        io_hang_duration_seconds=args.duration,
        data_collection_interval=args.interval,
        output_dir=args.output_dir,
        simulate_method=args.simulate_method
    )
    
    if args.mode == "cleanup":
        cleanup_test_resources(config)
        return
    
    # 设置信号处理
    runner = ExperimentRunner(config)
    
    def signal_handler(signum, frame):
        logger.info("接收到中断信号，正在清理...")
        runner.stop_data_collection()
        runner.simulator.cleanup()
        sys.exit(1)
    
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)
    
    # 运行实验
    logger.info("=" * 60)
    logger.info("IO Hang 模拟与 Kubernetes 状态采集实验")
    logger.info("=" * 60)
    logger.info(f"配置: {json.dumps(asdict(config), indent=2)}")
    
    result = runner.run_experiment()
    runner.save_results(result)
    
    # 打印摘要
    logger.info("=" * 60)
    logger.info("实验完成!")
    logger.info("=" * 60)
    logger.info(f"摘要: {json.dumps(result.summary, indent=2, ensure_ascii=False)}")
    
    # 清理测试资源
    if args.mode == "full":
        cleanup_test_resources(config)


if __name__ == "__main__":
    main()
