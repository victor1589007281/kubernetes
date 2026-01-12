#!/bin/bash
#===============================================================================
# 脚本名称: 01-prepare-system.sh
# 脚本描述: Ubuntu 24.04 系统环境准备脚本
# 作者: Auto-generated
# 版本: 1.0
#===============================================================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 日志函数
log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }

# 检查是否为root用户
check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "此脚本需要root权限运行"
        exit 1
    fi
}

# 获取集群编号 (1 或 2)
CLUSTER_ID=${1:-1}
log_info "正在为集群 ${CLUSTER_ID} 准备系统环境..."

#===============================================================================
# 1. 更新系统
#===============================================================================
log_step "1. 更新系统软件包..."
apt-get update -y
apt-get upgrade -y

#===============================================================================
# 2. 安装必要工具
#===============================================================================
log_step "2. 安装必要工具..."
apt-get install -y \
    apt-transport-https \
    ca-certificates \
    curl \
    gnupg \
    lsb-release \
    software-properties-common \
    wget \
    vim \
    net-tools \
    ipvsadm \
    ipset \
    jq \
    bash-completion \
    socat \
    conntrack \
    ebtables \
    ethtool

#===============================================================================
# 3. 关闭Swap
#===============================================================================
log_step "3. 关闭Swap..."
swapoff -a
sed -i '/swap/d' /etc/fstab

# 验证swap关闭
if free | grep -i swap | awk '{print $2}' | grep -q "^0$"; then
    log_info "Swap已成功关闭"
else
    log_warn "Swap可能未完全关闭，请手动检查"
fi

#===============================================================================
# 4. 禁用SELinux (如果存在)
#===============================================================================
log_step "4. 检查并禁用SELinux..."
if command -v setenforce &> /dev/null; then
    setenforce 0 || true
    sed -i 's/^SELINUX=enforcing$/SELINUX=permissive/' /etc/selinux/config 2>/dev/null || true
fi

#===============================================================================
# 5. 配置内核参数
#===============================================================================
log_step "5. 配置内核参数..."

# 加载必要的内核模块
cat > /etc/modules-load.d/k8s.conf << EOF
overlay
br_netfilter
ip_vs
ip_vs_rr
ip_vs_wrr
ip_vs_sh
nf_conntrack
EOF

# 立即加载模块
modprobe overlay
modprobe br_netfilter
modprobe ip_vs
modprobe ip_vs_rr
modprobe ip_vs_wrr
modprobe ip_vs_sh
modprobe nf_conntrack || modprobe nf_conntrack_ipv4 || true

# 配置sysctl参数
cat > /etc/sysctl.d/k8s.conf << EOF
# 网络参数
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
net.ipv4.conf.all.forwarding        = 1

# 连接跟踪参数
net.netfilter.nf_conntrack_max = 1000000
net.nf_conntrack_max = 1000000

# 文件描述符
fs.file-max = 1000000
fs.inotify.max_user_instances = 8192
fs.inotify.max_user_watches = 524288

# 网络性能
net.core.somaxconn = 32768
net.core.netdev_max_backlog = 16384
net.ipv4.tcp_max_syn_backlog = 8096
net.ipv4.tcp_slow_start_after_idle = 0

# ARP参数
net.ipv4.neigh.default.gc_thresh1 = 80000
net.ipv4.neigh.default.gc_thresh2 = 90000
net.ipv4.neigh.default.gc_thresh3 = 100000

# 虚拟内存
vm.max_map_count = 262144
vm.swappiness = 0
EOF

# 应用sysctl参数
sysctl --system

#===============================================================================
# 6. 配置时间同步
#===============================================================================
log_step "6. 配置时间同步..."
apt-get install -y chrony
systemctl enable chrony
systemctl start chrony

# 设置时区
timedatectl set-timezone Asia/Shanghai

#===============================================================================
# 7. 配置防火墙
#===============================================================================
log_step "7. 配置防火墙..."
# 在测试环境中，我们禁用ufw
systemctl stop ufw 2>/dev/null || true
systemctl disable ufw 2>/dev/null || true

#===============================================================================
# 8. 设置主机名
#===============================================================================
log_step "8. 设置主机名..."
hostnamectl set-hostname "k8s-cluster${CLUSTER_ID}-master"

# 更新/etc/hosts
cat >> /etc/hosts << EOF

# Kubernetes Cluster ${CLUSTER_ID}
127.0.0.1 k8s-cluster${CLUSTER_ID}-master
EOF

#===============================================================================
# 9. 配置文件描述符限制
#===============================================================================
log_step "9. 配置文件描述符限制..."
cat >> /etc/security/limits.conf << EOF
* soft nofile 65536
* hard nofile 65536
* soft nproc 65536
* hard nproc 65536
EOF

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证系统配置..."

VERIFY_PASSED=0
VERIFY_FAILED=0

verify_check() {
    local name=$1
    local cmd=$2
    if eval "$cmd" &>/dev/null; then
        log_info "✓ $name"
        ((VERIFY_PASSED++))
    else
        log_error "✗ $name"
        ((VERIFY_FAILED++))
    fi
}

echo ""
log_step "================== 验证结果 =================="

# 验证swap关闭
verify_check "Swap已关闭" "[[ \$(free | grep -i swap | awk '{print \$2}') -eq 0 ]]"

# 验证内核模块
verify_check "overlay模块已加载" "lsmod | grep -q overlay"
verify_check "br_netfilter模块已加载" "lsmod | grep -q br_netfilter"

# 验证sysctl参数
verify_check "IPv4转发已启用" "[[ \$(sysctl -n net.ipv4.ip_forward) -eq 1 ]]"
verify_check "bridge-nf-call-iptables已启用" "[[ \$(sysctl -n net.bridge.bridge-nf-call-iptables) -eq 1 ]]"

# 验证时间同步
verify_check "chrony服务已运行" "systemctl is-active chrony"

# 验证时区
verify_check "时区设置为Asia/Shanghai" "timedatectl | grep -q 'Asia/Shanghai'"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_error "验证失败: ${VERIFY_FAILED} 项"
    exit 1
fi

log_info "系统准备完成！建议重启系统以确保所有配置生效。"
log_info "重启命令: reboot"

echo ""
echo "=========================================="
echo "      系统准备脚本执行完成!"
echo "      集群编号: ${CLUSTER_ID}"
echo "=========================================="
