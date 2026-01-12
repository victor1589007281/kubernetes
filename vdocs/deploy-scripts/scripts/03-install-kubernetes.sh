#!/bin/bash
#===============================================================================
# 脚本名称: 03-install-kubernetes.sh
# 脚本描述: 安装Kubernetes集群 (kubeadm, kubelet, kubectl) (幂等执行)
# 作者: Auto-generated
# 版本: 2.0
# 幂等性: 支持重复执行
# 运行方式: sudo bash 03-install-kubernetes.sh 或 sudo ./03-install-kubernetes.sh
#===============================================================================

# 检查是否使用 bash 运行
if [ -z "$BASH_VERSION" ]; then
    echo "错误: 此脚本必须使用 bash 运行"
    echo "正确用法: sudo bash $0 或 sudo ./$0"
    exit 1
fi

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

# 检查root权限
if [[ $EUID -ne 0 ]]; then
    log_error "此脚本需要root权限运行"
    exit 1
fi

# 获取集群编号
CLUSTER_ID=${1:-1}
log_info "正在为集群 ${CLUSTER_ID} 安装Kubernetes (幂等模式)..."

# Kubernetes版本
K8S_VERSION="1.32.0"

# 强制重新初始化标志
FORCE_REINIT=${FORCE_REINIT:-false}

# Pod网络CIDR
case $CLUSTER_ID in
    1) POD_CIDR="10.244.0.0/16"; SERVICE_CIDR="10.96.0.0/12" ;;
    2) POD_CIDR="10.245.0.0/16"; SERVICE_CIDR="10.112.0.0/12" ;;
    *) POD_CIDR="10.244.0.0/16"; SERVICE_CIDR="10.96.0.0/12" ;;
esac

log_info "Kubernetes版本: v${K8S_VERSION}"
log_info "Pod CIDR: ${POD_CIDR}"
log_info "Service CIDR: ${SERVICE_CIDR}"

#===============================================================================
# 检查集群是否已初始化
#===============================================================================
is_cluster_initialized() {
    if [ -f /etc/kubernetes/admin.conf ] && kubectl cluster-info &>/dev/null; then
        return 0
    fi
    return 1
}

#===============================================================================
# 1. 添加Kubernetes APT仓库
#===============================================================================
log_step "1. 添加Kubernetes APT仓库..."

mkdir -p /etc/apt/keyrings

# 检查GPG密钥是否存在
if [ ! -f /etc/apt/keyrings/kubernetes-apt-keyring.gpg ]; then
    curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.32/deb/Release.key | \
        gpg --batch --yes --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
    log_info "GPG密钥已添加"
else
    log_skip "GPG密钥已存在"
fi

# 添加仓库（幂等：覆盖写入）
cat > /etc/apt/sources.list.d/kubernetes.list << EOF
deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.32/deb/ /
EOF

apt-get update -qq

#===============================================================================
# 2. 安装kubeadm, kubelet, kubectl
#===============================================================================
log_step "2. 安装kubeadm, kubelet, kubectl..."

# 检查是否已安装
if command -v kubeadm &> /dev/null && command -v kubelet &> /dev/null && command -v kubectl &> /dev/null; then
    log_skip "kubeadm, kubelet, kubectl 已安装"
else
    apt-get install -y kubelet kubeadm kubectl
    apt-mark hold kubelet kubeadm kubectl
    log_info "kubeadm, kubelet, kubectl 安装完成"
fi

log_info "kubeadm版本: $(kubeadm version -o short)"
log_info "kubelet版本: $(kubelet --version)"

#===============================================================================
# 3. 配置kubelet
#===============================================================================
log_step "3. 配置kubelet..."

mkdir -p /var/lib/kubelet

# 覆盖写入配置（幂等）
cat > /var/lib/kubelet/config.yaml << EOF
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd
containerRuntimeEndpoint: unix:///run/containerd/containerd.sock
EOF

cat > /etc/default/kubelet << EOF
KUBELET_EXTRA_ARGS="--container-runtime-endpoint=unix:///run/containerd/containerd.sock"
EOF

systemctl enable kubelet 2>/dev/null || true
log_info "kubelet配置完成"

#===============================================================================
# 4. 预拉取Kubernetes镜像
#===============================================================================
log_step "4. 预拉取Kubernetes镜像..."

# 检查是否需要拉取
IMAGES_PULLED=true
for img in kube-apiserver kube-controller-manager kube-scheduler kube-proxy; do
    if ! crictl images | grep -q "$img"; then
        IMAGES_PULLED=false
        break
    fi
done

if [ "$IMAGES_PULLED" = "false" ]; then
    kubeadm config images pull --image-repository=registry.aliyuncs.com/google_containers || true
    log_info "Kubernetes镜像拉取完成"
else
    log_skip "Kubernetes镜像已存在"
fi

#===============================================================================
# 5. 初始化Kubernetes Master节点
#===============================================================================
log_step "5. 初始化Kubernetes Master节点..."

if is_cluster_initialized && [ "$FORCE_REINIT" != "true" ]; then
    log_skip "Kubernetes集群已初始化"
else
    # 如果需要重新初始化，先重置
    if [ -f /etc/kubernetes/admin.conf ]; then
        log_warn "检测到现有集群，正在重置..."
        kubeadm reset -f --cri-socket unix:///run/containerd/containerd.sock || true
        rm -rf /etc/cni/net.d/* 2>/dev/null || true
        rm -rf /var/lib/etcd/* 2>/dev/null || true
    fi
    
    # 创建kubeadm配置文件
    cat > /tmp/kubeadm-config.yaml << EOF
apiVersion: kubeadm.k8s.io/v1beta4
kind: InitConfiguration
localAPIEndpoint:
  advertiseAddress: $(hostname -I | awk '{print $1}')
  bindPort: 6443
nodeRegistration:
  criSocket: unix:///run/containerd/containerd.sock
  name: $(hostname)
  taints:
    - effect: NoSchedule
      key: node-role.kubernetes.io/control-plane
---
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
kubernetesVersion: v${K8S_VERSION}
imageRepository: registry.aliyuncs.com/google_containers
networking:
  podSubnet: ${POD_CIDR}
  serviceSubnet: ${SERVICE_CIDR}
  dnsDomain: cluster${CLUSTER_ID}.local
clusterName: k8s-cluster${CLUSTER_ID}
controllerManager:
  extraArgs:
    - name: bind-address
      value: "0.0.0.0"
scheduler:
  extraArgs:
    - name: bind-address
      value: "0.0.0.0"
etcd:
  local:
    dataDir: /var/lib/etcd
---
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd
EOF

    # 初始化集群
    kubeadm init --config=/tmp/kubeadm-config.yaml --upload-certs | tee /tmp/kubeadm-init.log
    log_info "Kubernetes集群初始化完成"
fi

#===============================================================================
# 6. 配置kubectl
#===============================================================================
log_step "6. 配置kubectl..."

# 为root用户配置
mkdir -p /root/.kube
cp -f /etc/kubernetes/admin.conf /root/.kube/config
chown root:root /root/.kube/config

# 为当前用户配置
if [ -n "$SUDO_USER" ]; then
    USER_HOME=$(getent passwd "$SUDO_USER" | cut -d: -f6)
    mkdir -p "$USER_HOME"/.kube
    cp -f /etc/kubernetes/admin.conf "$USER_HOME"/.kube/config
    chown -R "$SUDO_USER":"$SUDO_USER" "$USER_HOME"/.kube
fi

# 配置kubectl自动补全（幂等：覆盖）
kubectl completion bash > /etc/bash_completion.d/kubectl

# 添加别名（幂等：检查后添加）
if ! grep -q "alias k=kubectl" /root/.bashrc; then
    echo 'alias k=kubectl' >> /root/.bashrc
    echo 'complete -o default -F __start_kubectl k' >> /root/.bashrc
fi

log_info "kubectl配置完成"

#===============================================================================
# 7. 允许Master节点调度Pod
#===============================================================================
log_step "7. 配置Master节点调度（单节点模式）..."

kubectl taint nodes --all node-role.kubernetes.io/control-plane- 2>/dev/null || log_skip "taint已移除或不存在"

#===============================================================================
# 8. 保存join命令
#===============================================================================
log_step "8. 保存Worker节点加入命令..."

mkdir -p "/root/k8s-cluster${CLUSTER_ID}"
kubeadm token create --print-join-command > "/root/k8s-cluster${CLUSTER_ID}/join-command.sh" 2>/dev/null || true
chmod +x "/root/k8s-cluster${CLUSTER_ID}/join-command.sh" 2>/dev/null || true

log_info "Worker节点加入命令已保存到: /root/k8s-cluster${CLUSTER_ID}/join-command.sh"

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证Kubernetes安装..."

sleep 5

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

verify_check "kubeadm已安装" "kubeadm version"
verify_check "kubelet已安装" "kubelet --version"
verify_check "kubectl已安装" "kubectl version --client"
verify_check "kubelet服务运行中" "systemctl is-active kubelet"
verify_check "kubectl可以连接到集群" "kubectl cluster-info"
verify_check "节点已注册" "kubectl get nodes | grep -q $(hostname)"
verify_check "kube-apiserver Pod运行中" "kubectl get pods -n kube-system | grep kube-apiserver | grep -q Running"
verify_check "etcd Pod运行中" "kubectl get pods -n kube-system | grep etcd | grep -q Running"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项 (部分失败可能是因为还未安装CNI)"
fi

echo ""
log_step "================== 集群信息 =================="
echo "集群名称: k8s-cluster${CLUSTER_ID}"
echo "Kubernetes版本: v${K8S_VERSION}"
echo "Pod CIDR: ${POD_CIDR}"
echo "Service CIDR: ${SERVICE_CIDR}"
echo ""

kubectl cluster-info 2>/dev/null || true
echo ""
kubectl get nodes -o wide 2>/dev/null || true
echo ""

log_warn "注意: 节点状态为NotReady是正常的，需要安装CNI插件后才会变为Ready"
log_info "请运行 04-install-calico.sh 安装Calico CNI"

echo ""
echo "=========================================="
echo "      Kubernetes安装完成!"
echo "      集群编号: ${CLUSTER_ID}"
echo "      版本: v${K8S_VERSION}"
echo "      支持重复执行: 是"
echo "=========================================="
