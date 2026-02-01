#!/bin/bash
# d_state_simulator.sh
# D 状态（不可中断睡眠）进程模拟器
#
# 此脚本提供多种方法模拟进程进入 D 状态，用于测试 Kubernetes 在 IO hang 场景下的行为
#
# 使用方法:
#   sudo ./d_state_simulator.sh <method> [options]
#
# 方法:
#   dm-delay     - 使用 device-mapper delay 目标
#   nbd          - 使用网络块设备
#   fuse-hang    - 使用 FUSE 文件系统 hang
#   loop-detach  - 使用 loop 设备分离
#
# 示例:
#   sudo ./d_state_simulator.sh dm-delay --duration 60
#   sudo ./d_state_simulator.sh loop-detach
#
# 警告: 此脚本可能导致系统不稳定，仅在测试环境使用！

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查 root 权限
check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_error "请使用 root 权限运行此脚本"
        exit 1
    fi
}

# 清理函数
cleanup_dm_delay() {
    log_info "清理 dm-delay 设备..."
    
    # 尝试卸载
    umount -f /mnt/io_hang_test 2>/dev/null || true
    umount -l /mnt/io_hang_test 2>/dev/null || true
    
    # 删除 dm 设备
    dmsetup remove io_hang_delay 2>/dev/null || true
    
    # 删除 loop 设备
    if [ -n "$LOOP_DEVICE" ]; then
        losetup -d "$LOOP_DEVICE" 2>/dev/null || true
    fi
    
    # 删除临时文件
    rm -f /tmp/io_hang_backend.img
    rmdir /mnt/io_hang_test 2>/dev/null || true
    
    log_info "清理完成"
}

cleanup_loop_detach() {
    log_info "清理 loop 设备..."
    
    # 强制卸载
    umount -f /mnt/io_hang_loop 2>/dev/null || true
    umount -l /mnt/io_hang_loop 2>/dev/null || true
    
    # 删除 loop 设备
    losetup -D 2>/dev/null || true
    
    # 删除临时文件
    rm -f /tmp/io_hang_loop.img
    rmdir /mnt/io_hang_loop 2>/dev/null || true
    
    log_info "清理完成"
}

# 方法 1: dm-delay (推荐)
# 使用 device-mapper 的 delay 目标创建一个超长延迟的块设备
simulate_dm_delay() {
    local DELAY_MS=${1:-999999999}  # 默认延迟接近无限
    local DURATION=${2:-60}
    
    log_info "使用 dm-delay 方法模拟 IO hang"
    log_info "延迟: ${DELAY_MS}ms, 持续时间: ${DURATION}s"
    
    # 创建后端文件
    log_info "创建后端存储文件..."
    dd if=/dev/zero of=/tmp/io_hang_backend.img bs=1M count=100 status=progress
    
    # 创建 loop 设备
    LOOP_DEVICE=$(losetup -f --show /tmp/io_hang_backend.img)
    log_info "创建 loop 设备: $LOOP_DEVICE"
    
    # 获取设备大小
    SIZE=$(blockdev --getsz "$LOOP_DEVICE")
    
    # 创建 dm-delay 设备
    # 格式: start_sector num_sectors delay <device> <offset> <read_delay> [<write_delay>]
    echo "0 $SIZE delay $LOOP_DEVICE 0 $DELAY_MS" | dmsetup create io_hang_delay
    log_info "创建 dm-delay 设备: /dev/mapper/io_hang_delay"
    
    # 创建挂载点
    mkdir -p /mnt/io_hang_test
    
    # 格式化并挂载
    log_info "格式化设备..."
    mkfs.ext4 -F /dev/mapper/io_hang_delay
    
    log_info "挂载设备..."
    mount /dev/mapper/io_hang_delay /mnt/io_hang_test
    
    log_info "=========================================="
    log_info "IO hang 环境已设置完成!"
    log_info "挂载点: /mnt/io_hang_test"
    log_info ""
    log_info "测试命令 (在另一个终端运行):"
    log_info "  # 这个命令会导致进程进入 D 状态"
    log_info "  dd if=/dev/zero of=/mnt/io_hang_test/testfile bs=4k count=1"
    log_info ""
    log_info "观察 D 状态进程:"
    log_info "  ps aux | grep ' D '"
    log_info "  watch -n1 'cat /proc/\$(pgrep -f testfile)/status | grep State'"
    log_info "=========================================="
    
    # 启动后台 IO 进程
    log_info "启动后台 IO 进程..."
    (dd if=/dev/zero of=/mnt/io_hang_test/background_io bs=4k count=10000 oflag=direct 2>/dev/null) &
    IO_PID=$!
    log_info "IO 进程 PID: $IO_PID"
    
    log_info "等待 ${DURATION} 秒..."
    sleep "$DURATION"
    
    # 清理
    log_info "实验结束，开始清理..."
    kill -9 $IO_PID 2>/dev/null || true
    cleanup_dm_delay
}

# 方法 2: loop 设备分离
# 通过分离正在使用的 loop 设备后端来触发 IO 错误
simulate_loop_detach() {
    local DURATION=${1:-60}
    
    log_info "使用 loop 设备分离方法模拟 IO hang"
    
    # 创建后端文件
    log_info "创建后端存储文件..."
    dd if=/dev/zero of=/tmp/io_hang_loop.img bs=1M count=100 status=progress
    
    # 创建并设置 loop 设备
    LOOP_DEVICE=$(losetup -f --show /tmp/io_hang_loop.img)
    log_info "创建 loop 设备: $LOOP_DEVICE"
    
    # 格式化
    mkfs.ext4 -F "$LOOP_DEVICE"
    
    # 创建挂载点并挂载
    mkdir -p /mnt/io_hang_loop
    mount "$LOOP_DEVICE" /mnt/io_hang_loop
    
    log_info "=========================================="
    log_info "准备触发 IO hang"
    log_info "挂载点: /mnt/io_hang_loop"
    log_info "=========================================="
    
    # 启动后台持续写入
    log_info "启动后台 IO 进程..."
    (while true; do
        dd if=/dev/zero of=/mnt/io_hang_loop/test_$RANDOM bs=4k count=100 oflag=sync 2>/dev/null
        sleep 0.1
    done) &
    IO_PID=$!
    log_info "IO 进程 PID: $IO_PID"
    
    sleep 5
    
    # 删除后端文件触发 IO 错误
    log_info "删除后端文件以触发 IO hang..."
    rm -f /tmp/io_hang_loop.img
    
    # 分离 loop 设备 (这会导致正在进行的 IO 进入 hang 状态)
    log_info "分离 loop 设备..."
    losetup -d "$LOOP_DEVICE" 2>/dev/null || true
    
    log_info "=========================================="
    log_info "IO hang 已触发!"
    log_info ""
    log_info "观察 D 状态进程:"
    log_info "  ps aux | grep ' D '"
    log_info "=========================================="
    
    log_info "等待 ${DURATION} 秒..."
    sleep "$DURATION"
    
    # 清理
    log_info "实验结束，开始清理..."
    kill -9 $IO_PID 2>/dev/null || true
    cleanup_loop_detach
}

# 方法 3: NFS hang 模拟
# 通过防火墙阻断 NFS 连接模拟网络存储 hang
simulate_nfs_hang() {
    log_warn "NFS hang 模拟需要配置 NFS 服务器"
    log_info "基本步骤:"
    log_info "1. 挂载 NFS 存储"
    log_info "2. 启动 IO 操作"
    log_info "3. 使用 iptables 阻断 NFS 连接:"
    log_info "   iptables -A OUTPUT -p tcp --dport 2049 -j DROP"
    log_info "4. 观察进程进入 D 状态"
    log_info "5. 恢复: iptables -D OUTPUT -p tcp --dport 2049 -j DROP"
}

# 方法 4: 使用内核模块触发 hang
# 这个方法更危险，但更精确
create_hang_module_source() {
    cat > /tmp/io_hang_module.c << 'EOF'
/*
 * io_hang_module.c - 模拟 IO hang 的内核模块
 * 警告: 仅用于测试，可能导致系统不稳定
 */

#include <linux/module.h>
#include <linux/kernel.h>
#include <linux/init.h>
#include <linux/fs.h>
#include <linux/sched.h>
#include <linux/delay.h>

MODULE_LICENSE("GPL");
MODULE_AUTHOR("Test");
MODULE_DESCRIPTION("IO Hang Simulator Module");

static int hang_duration = 60;
module_param(hang_duration, int, 0644);

static int __init io_hang_init(void) {
    printk(KERN_INFO "IO Hang module loaded, will hang for %d seconds\n", hang_duration);
    
    // 设置进程状态为 TASK_UNINTERRUPTIBLE (D 状态)
    set_current_state(TASK_UNINTERRUPTIBLE);
    
    // 使用 schedule_timeout 进入不可中断睡眠
    schedule_timeout(hang_duration * HZ);
    
    printk(KERN_INFO "IO Hang completed\n");
    return 0;
}

static void __exit io_hang_exit(void) {
    printk(KERN_INFO "IO Hang module unloaded\n");
}

module_init(io_hang_init);
module_exit(io_hang_exit);
EOF

    cat > /tmp/Makefile << 'EOF'
obj-m := io_hang_module.o
KDIR := /lib/modules/$(shell uname -r)/build

all:
	make -C $(KDIR) M=$(PWD) modules

clean:
	make -C $(KDIR) M=$(PWD) clean
EOF
    
    log_info "内核模块源码已生成: /tmp/io_hang_module.c"
    log_info "编译并加载模块:"
    log_info "  cd /tmp && make"
    log_info "  insmod io_hang_module.ko hang_duration=60"
}

# 监控 D 状态进程
monitor_d_state() {
    local INTERVAL=${1:-2}
    local COUNT=0
    
    log_info "开始监控 D 状态进程 (间隔 ${INTERVAL}s)..."
    log_info "按 Ctrl+C 停止监控"
    
    while true; do
        COUNT=$((COUNT + 1))
        echo ""
        echo "========== 监控 #$COUNT $(date '+%Y-%m-%d %H:%M:%S') =========="
        
        # 统计 D 状态进程
        D_COUNT=$(ps aux | awk '$8 ~ /D/ {count++} END {print count+0}')
        echo "D 状态进程数: $D_COUNT"
        
        # 显示 D 状态进程详情
        if [ "$D_COUNT" -gt 0 ]; then
            echo ""
            echo "D 状态进程列表:"
            ps aux | head -1
            ps aux | awk '$8 ~ /D/ {print}'
            
            # 显示等待通道
            echo ""
            echo "等待通道:"
            for pid in $(ps aux | awk '$8 ~ /D/ {print $2}'); do
                wchan=$(cat /proc/$pid/wchan 2>/dev/null || echo "N/A")
                echo "  PID $pid: $wchan"
            done
        fi
        
        # 显示 IO 统计
        echo ""
        echo "IO 统计:"
        iostat -x 1 1 2>/dev/null | grep -E "^(Device|nvme|sd|vd|loop)" || echo "iostat 不可用"
        
        # 显示阻塞进程数
        BLOCKED=$(cat /proc/stat | grep procs_blocked | awk '{print $2}')
        echo ""
        echo "内核阻塞进程数: $BLOCKED"
        
        sleep "$INTERVAL"
    done
}

# 使用说明
usage() {
    cat << EOF
使用方法: $0 <command> [options]

命令:
  dm-delay [--duration N]    使用 dm-delay 模拟 IO hang (推荐)
  loop-detach [--duration N] 使用 loop 设备分离模拟 IO hang
  nfs-hang                   显示 NFS hang 模拟说明
  kernel-module              生成内核模块源码
  monitor [--interval N]     监控 D 状态进程
  cleanup                    清理所有测试资源

选项:
  --duration N    实验持续时间 (秒), 默认 60
  --interval N    监控间隔 (秒), 默认 2

示例:
  # 使用 dm-delay 模拟 60 秒的 IO hang
  sudo $0 dm-delay --duration 60

  # 监控 D 状态进程
  sudo $0 monitor --interval 1

  # 清理所有资源
  sudo $0 cleanup

注意:
  - 必须以 root 权限运行
  - 仅在测试环境使用
  - 可能导致系统不稳定

EOF
}

# 主函数
main() {
    if [ $# -lt 1 ]; then
        usage
        exit 1
    fi
    
    COMMAND=$1
    shift
    
    # 解析选项
    DURATION=60
    INTERVAL=2
    
    while [ $# -gt 0 ]; do
        case "$1" in
            --duration)
                DURATION=$2
                shift 2
                ;;
            --interval)
                INTERVAL=$2
                shift 2
                ;;
            *)
                log_error "未知选项: $1"
                usage
                exit 1
                ;;
        esac
    done
    
    case "$COMMAND" in
        dm-delay)
            check_root
            trap cleanup_dm_delay EXIT
            simulate_dm_delay 999999999 "$DURATION"
            ;;
        loop-detach)
            check_root
            trap cleanup_loop_detach EXIT
            simulate_loop_detach "$DURATION"
            ;;
        nfs-hang)
            simulate_nfs_hang
            ;;
        kernel-module)
            create_hang_module_source
            ;;
        monitor)
            check_root
            monitor_d_state "$INTERVAL"
            ;;
        cleanup)
            check_root
            cleanup_dm_delay
            cleanup_loop_detach
            ;;
        *)
            log_error "未知命令: $COMMAND"
            usage
            exit 1
            ;;
    esac
}

main "$@"
