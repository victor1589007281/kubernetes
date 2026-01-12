#!/bin/bash
#===============================================================================
# 脚本名称: 02-install-containerd.sh
# 脚本描述: 安装和配置containerd (CRI) + runc (OCI)
# 作者: Auto-generated
# 版本: 1.0
#===============================================================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }

# 检查root权限
if [[ $EUID -ne 0 ]]; then
    log_error "此脚本需要root权限运行"
    exit 1
fi

# 版本配置
CONTAINERD_VERSION="1.7.24"
RUNC_VERSION="1.2.3"
CNI_PLUGINS_VERSION="1.6.1"
NERDCTL_VERSION="2.0.2"
CRICTL_VERSION="1.32.0"

# 架构检测
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *) log_error "不支持的架构: $ARCH"; exit 1 ;;
esac

log_info "检测到系统架构: $ARCH"
log_info "containerd版本: $CONTAINERD_VERSION"
log_info "runc版本: $RUNC_VERSION"

#===============================================================================
# 1. 卸载旧版本Docker/containerd
#===============================================================================
log_step "1. 清理旧版本..."
apt-get remove -y docker docker-engine docker.io containerd runc 2>/dev/null || true
rm -rf /var/lib/docker /var/lib/containerd

#===============================================================================
# 2. 安装runc (OCI runtime)
#===============================================================================
log_step "2. 安装runc (OCI runtime) v${RUNC_VERSION}..."

wget -q "https://github.com/opencontainers/runc/releases/download/v${RUNC_VERSION}/runc.${ARCH}" \
    -O /usr/local/sbin/runc
chmod +x /usr/local/sbin/runc

# 创建符号链接
ln -sf /usr/local/sbin/runc /usr/sbin/runc
ln -sf /usr/local/sbin/runc /usr/bin/runc

log_info "runc安装完成: $(runc --version | head -1)"

#===============================================================================
# 3. 安装CNI插件
#===============================================================================
log_step "3. 安装CNI插件 v${CNI_PLUGINS_VERSION}..."

mkdir -p /opt/cni/bin
wget -q "https://github.com/containernetworking/plugins/releases/download/v${CNI_PLUGINS_VERSION}/cni-plugins-linux-${ARCH}-v${CNI_PLUGINS_VERSION}.tgz" \
    -O /tmp/cni-plugins.tgz
tar -xzf /tmp/cni-plugins.tgz -C /opt/cni/bin
rm -f /tmp/cni-plugins.tgz

log_info "CNI插件安装完成"

#===============================================================================
# 4. 安装containerd
#===============================================================================
log_step "4. 安装containerd v${CONTAINERD_VERSION}..."

wget -q "https://github.com/containerd/containerd/releases/download/v${CONTAINERD_VERSION}/containerd-${CONTAINERD_VERSION}-linux-${ARCH}.tar.gz" \
    -O /tmp/containerd.tar.gz
tar -xzf /tmp/containerd.tar.gz -C /usr/local
rm -f /tmp/containerd.tar.gz

log_info "containerd安装完成: $(containerd --version)"

#===============================================================================
# 5. 配置containerd
#===============================================================================
log_step "5. 配置containerd..."

mkdir -p /etc/containerd

# 生成默认配置
containerd config default > /etc/containerd/config.toml

# 配置SystemdCgroup
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml

# 配置sandbox image (使用阿里云镜像加速)
sed -i 's|sandbox_image = "registry.k8s.io/pause:.*"|sandbox_image = "registry.aliyuncs.com/google_containers/pause:3.10"|' /etc/containerd/config.toml

# 配置镜像加速器
cat >> /etc/containerd/config.toml << 'EOF'

# 镜像加速器配置
[plugins."io.containerd.grpc.v1.cri".registry]
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."docker.io"]
      endpoint = ["https://mirror.ccs.tencentyun.com", "https://registry-1.docker.io"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."registry.k8s.io"]
      endpoint = ["https://registry.aliyuncs.com/google_containers"]
EOF

#===============================================================================
# 6. 创建systemd服务
#===============================================================================
log_step "6. 创建containerd系统服务..."

cat > /etc/systemd/system/containerd.service << 'EOF'
[Unit]
Description=containerd container runtime
Documentation=https://containerd.io
After=network.target local-fs.target

[Service]
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/containerd

Type=notify
Delegate=yes
KillMode=process
Restart=always
RestartSec=5

LimitNPROC=infinity
LimitCORE=infinity
LimitNOFILE=infinity

TasksMax=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable containerd
systemctl start containerd

#===============================================================================
# 7. 安装crictl
#===============================================================================
log_step "7. 安装crictl v${CRICTL_VERSION}..."

wget -q "https://github.com/kubernetes-sigs/cri-tools/releases/download/v${CRICTL_VERSION}/crictl-v${CRICTL_VERSION}-linux-${ARCH}.tar.gz" \
    -O /tmp/crictl.tar.gz
tar -xzf /tmp/crictl.tar.gz -C /usr/local/bin
rm -f /tmp/crictl.tar.gz

# 配置crictl
cat > /etc/crictl.yaml << EOF
runtime-endpoint: unix:///run/containerd/containerd.sock
image-endpoint: unix:///run/containerd/containerd.sock
timeout: 10
debug: false
EOF

log_info "crictl安装完成: $(crictl --version)"

#===============================================================================
# 8. 安装nerdctl (可选，Docker兼容CLI)
#===============================================================================
log_step "8. 安装nerdctl v${NERDCTL_VERSION}..."

wget -q "https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/nerdctl-${NERDCTL_VERSION}-linux-${ARCH}.tar.gz" \
    -O /tmp/nerdctl.tar.gz
tar -xzf /tmp/nerdctl.tar.gz -C /usr/local/bin nerdctl
rm -f /tmp/nerdctl.tar.gz

log_info "nerdctl安装完成: $(nerdctl --version)"

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证containerd安装..."

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

# 验证二进制文件
verify_check "runc已安装" "runc --version"
verify_check "containerd已安装" "containerd --version"
verify_check "crictl已安装" "crictl --version"
verify_check "nerdctl已安装" "nerdctl --version"

# 验证服务状态
verify_check "containerd服务运行中" "systemctl is-active containerd"

# 验证CNI插件
verify_check "CNI插件已安装" "ls /opt/cni/bin/bridge"

# 验证crictl可以连接
sleep 2
verify_check "crictl可以连接到containerd" "crictl info"

# 验证SystemdCgroup配置
verify_check "SystemdCgroup已启用" "grep 'SystemdCgroup = true' /etc/containerd/config.toml"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_error "验证失败: ${VERIFY_FAILED} 项"
    exit 1
fi

# 显示版本信息
echo ""
log_step "================== 版本信息 =================="
echo "containerd: $(containerd --version)"
echo "runc: $(runc --version | head -1)"
echo "crictl: $(crictl --version)"
echo "nerdctl: $(nerdctl --version)"
echo ""

log_info "containerd安装和配置完成！"

echo ""
echo "=========================================="
echo "      containerd安装完成!"
echo "      CRI: containerd v${CONTAINERD_VERSION}"
echo "      OCI: runc v${RUNC_VERSION}"
echo "=========================================="
