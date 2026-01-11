#!/bin/bash
#===============================================================================
# 脚本名称: 03-install-kubernetes.sh
# 脚本描述: 安装Kubernetes集群 (kubeadm, kubelet, kubectl)
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

# 获取集群编号
CLUSTER_ID=${1:-1}
log_info "正在为集群 ${CLUSTER_ID} 安装Kubernetes..."

# Kubernetes版本
K8S_VERSION="1.32.0"

# Pod网络CIDR (不同集群使用不同网段)
case $CLUSTER_ID in
    1) POD_CIDR="10.244.0.0/16"; SERVICE_CIDR="10.96.0.0/12" ;;
    2) POD_CIDR="10.245.0.0/16"; SERVICE_CIDR="10.112.0.0/12" ;;
    *) POD_CIDR="10.244.0.0/16"; SERVICE_CIDR="10.96.0.0/12" ;;
esac

log_info "Kubernetes版本: v${K8S_VERSION}"
log_info "Pod CIDR: ${POD_CIDR}"
log_info "Service CIDR: ${SERVICE_CIDR}"

#===============================================================================
# 1. 添加Kubernetes APT仓库
#===============================================================================
log_step "1. 添加Kubernetes APT仓库..."

# 创建keyrings目录
mkdir -p /etc/apt/keyrings

# 下载Kubernetes APT仓库签名密钥
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.32/deb/Release.key | \
    gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg

# 添加Kubernetes APT仓库
cat > /etc/apt/sources.list.d/kubernetes.list << EOF
deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.32/deb/ /
EOF

# 备用：使用阿里云镜像源
# curl -fsSL https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.32/deb/Release.key | \
#     gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
# cat > /etc/apt/sources.list.d/kubernetes.list << EOF
# deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.32/deb/ /
# EOF

apt-get update

#===============================================================================
# 2. 安装kubeadm, kubelet, kubectl
#===============================================================================
log_step "2. 安装kubeadm, kubelet, kubectl..."

apt-get install -y kubelet kubeadm kubectl

# 锁定版本，防止自动更新
apt-mark hold kubelet kubeadm kubectl

log_info "kubeadm版本: $(kubeadm version -o short)"
log_info "kubelet版本: $(kubelet --version)"
log_info "kubectl版本: $(kubectl version --client -o yaml | grep gitVersion | head -1)"

#===============================================================================
# 3. 配置kubelet
#===============================================================================
log_step "3. 配置kubelet..."

# 创建kubelet配置目录
mkdir -p /var/lib/kubelet

# 配置kubelet使用systemd作为cgroup驱动
cat > /var/lib/kubelet/config.yaml << EOF
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd
containerRuntimeEndpoint: unix:///run/containerd/containerd.sock
EOF

# 配置kubelet默认参数
cat > /etc/default/kubelet << EOF
KUBELET_EXTRA_ARGS="--container-runtime-endpoint=unix:///run/containerd/containerd.sock"
EOF

# 启用kubelet服务
systemctl enable kubelet

#===============================================================================
# 4. 预拉取Kubernetes镜像
#===============================================================================
log_step "4. 预拉取Kubernetes镜像..."

# 使用阿里云镜像加速
kubeadm config images pull --image-repository=registry.aliyuncs.com/google_containers

log_info "Kubernetes镜像拉取完成"

#===============================================================================
# 5. 初始化Kubernetes Master节点
#===============================================================================
log_step "5. 初始化Kubernetes Master节点..."

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

#===============================================================================
# 6. 配置kubectl
#===============================================================================
log_step "6. 配置kubectl..."

# 为root用户配置
mkdir -p /root/.kube
cp -f /etc/kubernetes/admin.conf /root/.kube/config
chown root:root /root/.kube/config

# 为当前用户配置（如果不是root）
if [ -n "$SUDO_USER" ]; then
    USER_HOME=$(getent passwd $SUDO_USER | cut -d: -f6)
    mkdir -p $USER_HOME/.kube
    cp -f /etc/kubernetes/admin.conf $USER_HOME/.kube/config
    chown -R $SUDO_USER:$SUDO_USER $USER_HOME/.kube
fi

# 配置kubectl自动补全
kubectl completion bash > /etc/bash_completion.d/kubectl
echo 'alias k=kubectl' >> /root/.bashrc
echo 'complete -o default -F __start_kubectl k' >> /root/.bashrc

#===============================================================================
# 7. 允许Master节点调度Pod（单节点集群）
#===============================================================================
log_step "7. 配置Master节点调度（单节点模式）..."

# 移除master节点的taint，允许调度Pod
kubectl taint nodes --all node-role.kubernetes.io/control-plane- 2>/dev/null || true

#===============================================================================
# 8. 保存join命令
#===============================================================================
log_step "8. 保存Worker节点加入命令..."

mkdir -p /root/k8s-cluster${CLUSTER_ID}
kubeadm token create --print-join-command > /root/k8s-cluster${CLUSTER_ID}/join-command.sh
chmod +x /root/k8s-cluster${CLUSTER_ID}/join-command.sh

log_info "Worker节点加入命令已保存到: /root/k8s-cluster${CLUSTER_ID}/join-command.sh"

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证Kubernetes安装..."

# 等待API Server就绪
log_info "等待API Server就绪..."
sleep 10

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
verify_check "kubeadm已安装" "kubeadm version"
verify_check "kubelet已安装" "kubelet --version"
verify_check "kubectl已安装" "kubectl version --client"

# 验证服务状态
verify_check "kubelet服务运行中" "systemctl is-active kubelet"

# 验证集群连接
verify_check "kubectl可以连接到集群" "kubectl cluster-info"

# 验证节点状态（可能还是NotReady，因为还没有CNI）
verify_check "节点已注册" "kubectl get nodes | grep -q $(hostname)"

# 验证核心组件
verify_check "kube-apiserver Pod运行中" "kubectl get pods -n kube-system | grep kube-apiserver | grep -q Running"
verify_check "kube-controller-manager Pod运行中" "kubectl get pods -n kube-system | grep kube-controller-manager | grep -q Running"
verify_check "kube-scheduler Pod运行中" "kubectl get pods -n kube-system | grep kube-scheduler | grep -q Running"
verify_check "etcd Pod运行中" "kubectl get pods -n kube-system | grep etcd | grep -q Running"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项 (部分失败可能是因为还未安装CNI)"
fi

# 显示集群信息
echo ""
log_step "================== 集群信息 =================="
echo "集群名称: k8s-cluster${CLUSTER_ID}"
echo "Kubernetes版本: v${K8S_VERSION}"
echo "Pod CIDR: ${POD_CIDR}"
echo "Service CIDR: ${SERVICE_CIDR}"
echo ""

kubectl cluster-info
echo ""
kubectl get nodes -o wide
echo ""

log_warn "注意: 节点状态为NotReady是正常的，需要安装CNI插件后才会变为Ready"
log_info "请运行 04-install-calico.sh 安装Calico CNI"

echo ""
echo "=========================================="
echo "      Kubernetes安装完成!"
echo "      集群编号: ${CLUSTER_ID}"
echo "      版本: v${K8S_VERSION}"
echo "=========================================="
