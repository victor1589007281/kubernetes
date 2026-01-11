#!/bin/bash
#===============================================================================
# 脚本名称: 04-install-calico.sh
# 脚本描述: 安装Calico CNI网络插件
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

# 获取集群编号
CLUSTER_ID=${1:-1}

# Calico版本
CALICO_VERSION="3.29.1"

# Pod CIDR
case $CLUSTER_ID in
    1) POD_CIDR="10.244.0.0/16" ;;
    2) POD_CIDR="10.245.0.0/16" ;;
    *) POD_CIDR="10.244.0.0/16" ;;
esac

log_info "正在为集群 ${CLUSTER_ID} 安装Calico CNI..."
log_info "Calico版本: v${CALICO_VERSION}"
log_info "Pod CIDR: ${POD_CIDR}"

#===============================================================================
# 1. 检查kubectl连接
#===============================================================================
log_step "1. 检查kubectl连接..."

if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群，请确保已正确配置kubectl"
    exit 1
fi

log_info "kubectl连接正常"

#===============================================================================
# 2. 下载Calico配置文件
#===============================================================================
log_step "2. 下载Calico配置文件..."

mkdir -p /root/k8s-cluster${CLUSTER_ID}/calico
cd /root/k8s-cluster${CLUSTER_ID}/calico

# 下载Calico Operator
wget -q "https://raw.githubusercontent.com/projectcalico/calico/v${CALICO_VERSION}/manifests/tigera-operator.yaml" \
    -O tigera-operator.yaml

# 下载Calico自定义资源定义
wget -q "https://raw.githubusercontent.com/projectcalico/calico/v${CALICO_VERSION}/manifests/custom-resources.yaml" \
    -O custom-resources.yaml

log_info "Calico配置文件下载完成"

#===============================================================================
# 3. 修改Pod CIDR配置
#===============================================================================
log_step "3. 配置Pod CIDR..."

# 修改custom-resources.yaml中的CIDR
sed -i "s|cidr: 192.168.0.0/16|cidr: ${POD_CIDR}|g" custom-resources.yaml

# 显示配置
log_info "当前Calico配置:"
grep -A5 "cidr:" custom-resources.yaml | head -10

#===============================================================================
# 4. 安装Tigera Operator
#===============================================================================
log_step "4. 安装Tigera Operator..."

kubectl create -f tigera-operator.yaml

# 等待Operator就绪
log_info "等待Tigera Operator就绪..."
kubectl wait --for=condition=Available deployment/tigera-operator \
    -n tigera-operator --timeout=120s || true

#===============================================================================
# 5. 安装Calico
#===============================================================================
log_step "5. 安装Calico..."

kubectl create -f custom-resources.yaml

log_info "Calico安装请求已提交"

#===============================================================================
# 6. 等待Calico组件就绪
#===============================================================================
log_step "6. 等待Calico组件就绪..."

# 等待calico-system命名空间创建
log_info "等待calico-system命名空间创建..."
for i in {1..60}; do
    if kubectl get namespace calico-system &>/dev/null; then
        log_info "calico-system命名空间已创建"
        break
    fi
    sleep 2
done

# 等待calico-node DaemonSet就绪
log_info "等待calico-node就绪 (这可能需要几分钟)..."
kubectl rollout status daemonset/calico-node -n calico-system --timeout=300s || true

# 等待calico-kube-controllers就绪
log_info "等待calico-kube-controllers就绪..."
kubectl rollout status deployment/calico-kube-controllers -n calico-system --timeout=120s || true

#===============================================================================
# 7. 安装calicoctl (可选)
#===============================================================================
log_step "7. 安装calicoctl..."

# 检测架构
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
esac

wget -q "https://github.com/projectcalico/calico/releases/download/v${CALICO_VERSION}/calicoctl-linux-${ARCH}" \
    -O /usr/local/bin/calicoctl
chmod +x /usr/local/bin/calicoctl

# 配置calicoctl使用Kubernetes datastore
cat > /etc/calico/calicoctl.cfg << EOF
apiVersion: projectcalico.org/v3
kind: CalicoAPIConfig
metadata:
spec:
  datastoreType: "kubernetes"
  kubeconfig: "/root/.kube/config"
EOF

log_info "calicoctl安装完成: $(calicoctl version 2>/dev/null | head -1 || echo 'installed')"

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证Calico安装..."

# 等待一段时间让所有组件稳定
sleep 30

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

# 验证Operator
verify_check "Tigera Operator运行中" "kubectl get deployment tigera-operator -n tigera-operator -o jsonpath='{.status.availableReplicas}' | grep -q '1'"

# 验证calico-node
verify_check "calico-node DaemonSet运行中" "kubectl get daemonset calico-node -n calico-system -o jsonpath='{.status.numberReady}' | grep -qv '^0'"

# 验证calico-kube-controllers
verify_check "calico-kube-controllers运行中" "kubectl get deployment calico-kube-controllers -n calico-system -o jsonpath='{.status.availableReplicas}' | grep -q '1'"

# 验证节点状态
verify_check "节点状态为Ready" "kubectl get nodes | grep -q Ready"

# 验证calicoctl
verify_check "calicoctl已安装" "calicoctl version"

# 验证IP池
verify_check "Calico IP Pool已创建" "kubectl get ippools.crd.projectcalico.org"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示状态信息
echo ""
log_step "================== Calico状态 =================="
echo ""
echo "=== Calico Pods ==="
kubectl get pods -n calico-system -o wide
echo ""
echo "=== Calico IP Pools ==="
kubectl get ippools.crd.projectcalico.org -o yaml 2>/dev/null | grep -A2 "cidr:" || true
echo ""
echo "=== 节点状态 ==="
kubectl get nodes -o wide
echo ""

#===============================================================================
# 8. 测试网络连通性
#===============================================================================
log_step "8. 测试网络连通性..."

# 创建测试Pod
cat > /tmp/network-test.yaml << EOF
apiVersion: v1
kind: Pod
metadata:
  name: network-test
  namespace: default
spec:
  containers:
  - name: busybox
    image: busybox:latest
    command: ["sleep", "3600"]
EOF

kubectl apply -f /tmp/network-test.yaml

# 等待Pod就绪
log_info "等待测试Pod就绪..."
kubectl wait --for=condition=Ready pod/network-test --timeout=120s || true

# 测试网络
if kubectl get pod network-test -o jsonpath='{.status.phase}' | grep -q Running; then
    log_info "✓ 测试Pod运行正常"
    
    # 获取Pod IP
    POD_IP=$(kubectl get pod network-test -o jsonpath='{.status.podIP}')
    log_info "测试Pod IP: ${POD_IP}"
    
    # 测试DNS
    if kubectl exec network-test -- nslookup kubernetes.default &>/dev/null; then
        log_info "✓ DNS解析正常"
    else
        log_warn "✗ DNS解析可能有问题"
    fi
fi

# 清理测试Pod
kubectl delete pod network-test --ignore-not-found=true &>/dev/null || true

log_info "Calico CNI安装和配置完成！"

echo ""
echo "=========================================="
echo "      Calico CNI安装完成!"
echo "      集群编号: ${CLUSTER_ID}"
echo "      版本: v${CALICO_VERSION}"
echo "      Pod CIDR: ${POD_CIDR}"
echo "=========================================="
