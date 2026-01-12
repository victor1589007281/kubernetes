#!/bin/bash
#===============================================================================
# 脚本名称: 01-prepare-system.sh
# 脚本描述: Ubuntu 24.04 系统环境准备脚本 (幂等执行)
# 作者: Auto-generated
# 版本: 2.0
# 幂等性: 支持重复执行
#===============================================================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }
log_skip() { echo -e "${CYAN}[SKIP]${NC} $1 (已完成)"; }

# 检查是否为root用户
if [[ $EUID -ne 0 ]]; then
    log_error "此脚本需要root权限运行"
    exit 1
fi

# 获取集群编号 (1 或 2)
CLUSTER_ID=${1:-1}
log_info "正在为集群 ${CLUSTER_ID} 准备系统环境 (幂等模式)..."

#===============================================================================
# 1. 更新系统
#===============================================================================
log_step "1. 更新系统软件包..."
apt-get update -y
# 跳过upgrade避免每次都耗时
# apt-get upgrade -y

#===============================================================================
# 2. 安装必要工具
#===============================================================================
log_step "2. 安装必要工具..."

# 定义需要安装的包
PACKAGES=(
    apt-transport-https
    ca-certificates
    curl
    gnupg
    lsb-release
    software-properties-common
    wget
    vim
    net-tools
    ipvsadm
    ipset
    jq
    bash-completion
    socat
    conntrack
    ebtables
    ethtool
)

# 检查并安装缺失的包
MISSING_PACKAGES=()
for pkg in "${PACKAGES[@]}"; do
    if ! dpkg -l | grep -q "^ii  $pkg "; then
        MISSING_PACKAGES+=("$pkg")
    fi
done

if [ ${#MISSING_PACKAGES[@]} -gt 0 ]; then
    log_info "安装缺失的包: ${MISSING_PACKAGES[*]}"
    apt-get install -y "${MISSING_PACKAGES[@]}"
else
    log_skip "必要工具已安装"
fi

#===============================================================================
# 3. 关闭Swap
#===============================================================================
log_step "3. 关闭Swap..."

# 检查swap状态
SWAP_TOTAL=$(free | grep -i swap | awk '{print $2}')
if [[ "$SWAP_TOTAL" != "0" ]]; then
    swapoff -a
    log_info "Swap已关闭"
else
    log_skip "Swap已经关闭"
fi

# 从fstab移除swap条目（幂等操作）
if grep -q "swap" /etc/fstab; then
    sed -i '/swap/d' /etc/fstab
    log_info "已从/etc/fstab移除swap条目"
fi

#===============================================================================
# 4. 禁用SELinux (如果存在)
#===============================================================================
log_step "4. 检查并禁用SELinux..."
if command -v setenforce &> /dev/null; then
    setenforce 0 2>/dev/null || true
    if [ -f /etc/selinux/config ]; then
        sed -i 's/^SELINUX=enforcing$/SELINUX=permissive/' /etc/selinux/config 2>/dev/null || true
    fi
    log_info "SELinux已禁用"
else
    log_skip "SELinux不存在"
fi

#===============================================================================
# 5. 配置内核参数
#===============================================================================
log_step "5. 配置内核参数..."

# 加载必要的内核模块配置（幂等：覆盖写入）
cat > /etc/modules-load.d/k8s.conf << EOF
overlay
br_netfilter
ip_vs
ip_vs_rr
ip_vs_wrr
ip_vs_sh
nf_conntrack
EOF

# 立即加载模块（幂等：已加载不报错）
modprobe overlay 2>/dev/null || true
modprobe br_netfilter 2>/dev/null || true
modprobe ip_vs 2>/dev/null || true
modprobe ip_vs_rr 2>/dev/null || true
modprobe ip_vs_wrr 2>/dev/null || true
modprobe ip_vs_sh 2>/dev/null || true
modprobe nf_conntrack 2>/dev/null || modprobe nf_conntrack_ipv4 2>/dev/null || true

# 配置sysctl参数（幂等：覆盖写入）
cat > /etc/sysctl.d/k8s.conf << EOF
# 网络参数
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
net.ipv4.conf.all.forwarding        = 1

# 连接跟踪参数
net.netfilter.nf_conntrack_max = 1000000

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
sysctl --system > /dev/null 2>&1
log_info "内核参数已配置"

#===============================================================================
# 6. 配置时间同步
#===============================================================================
log_step "6. 配置时间同步..."

if ! dpkg -l | grep -q "^ii  chrony "; then
    apt-get install -y chrony
    log_info "chrony已安装"
else
    log_skip "chrony已安装"
fi

# 确保服务启动（幂等）
systemctl enable chrony 2>/dev/null || true
systemctl start chrony 2>/dev/null || true

# 设置时区（幂等）
CURRENT_TZ=$(timedatectl | grep "Time zone" | awk '{print $3}')
if [ "$CURRENT_TZ" != "Asia/Shanghai" ]; then
    timedatectl set-timezone Asia/Shanghai
    log_info "时区已设置为Asia/Shanghai"
else
    log_skip "时区已是Asia/Shanghai"
fi

#===============================================================================
# 7. 配置防火墙
#===============================================================================
log_step "7. 配置防火墙..."

if systemctl is-active ufw &>/dev/null; then
    systemctl stop ufw
    systemctl disable ufw
    log_info "ufw已禁用"
else
    log_skip "ufw已禁用或不存在"
fi

#===============================================================================
# 8. 设置主机名
#===============================================================================
log_step "8. 设置主机名..."

EXPECTED_HOSTNAME="k8s-cluster${CLUSTER_ID}-master"
CURRENT_HOSTNAME=$(hostname)

if [ "$CURRENT_HOSTNAME" != "$EXPECTED_HOSTNAME" ]; then
    hostnamectl set-hostname "$EXPECTED_HOSTNAME"
    log_info "主机名已设置为: $EXPECTED_HOSTNAME"
else
    log_skip "主机名已是: $EXPECTED_HOSTNAME"
fi

# 更新/etc/hosts（幂等：检查后添加）
if ! grep -q "$EXPECTED_HOSTNAME" /etc/hosts; then
    cat >> /etc/hosts << EOF

# Kubernetes Cluster ${CLUSTER_ID}
127.0.0.1 ${EXPECTED_HOSTNAME}
EOF
    log_info "已更新/etc/hosts"
else
    log_skip "/etc/hosts已包含主机名"
fi

#===============================================================================
# 9. 配置文件描述符限制
#===============================================================================
log_step "9. 配置文件描述符限制..."

# 检查是否已配置（幂等）
if ! grep -q "# K8S limits" /etc/security/limits.conf; then
    cat >> /etc/security/limits.conf << EOF

# K8S limits
* soft nofile 65536
* hard nofile 65536
* soft nproc 65536
* hard nproc 65536
EOF
    log_info "文件描述符限制已配置"
else
    log_skip "文件描述符限制已配置"
fi

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

verify_check "Swap已关闭" "[[ \$(free | grep -i swap | awk '{print \$2}') -eq 0 ]]"
verify_check "overlay模块已加载" "lsmod | grep -q overlay"
verify_check "br_netfilter模块已加载" "lsmod | grep -q br_netfilter"
verify_check "IPv4转发已启用" "[[ \$(sysctl -n net.ipv4.ip_forward) -eq 1 ]]"
verify_check "bridge-nf-call-iptables已启用" "[[ \$(sysctl -n net.bridge.bridge-nf-call-iptables) -eq 1 ]]"
verify_check "chrony服务已运行" "systemctl is-active chrony"
verify_check "时区设置为Asia/Shanghai" "timedatectl | grep -q 'Asia/Shanghai'"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_error "验证失败: ${VERIFY_FAILED} 项"
    exit 1
fi

log_info "系统准备完成！(幂等执行)"

echo ""
echo "=========================================="
echo "      系统准备脚本执行完成!"
echo "      集群编号: ${CLUSTER_ID}"
echo "      支持重复执行: 是"
echo "=========================================="
