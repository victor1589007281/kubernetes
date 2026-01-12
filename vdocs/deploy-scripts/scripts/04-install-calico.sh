#!/bin/bash
#===============================================================================
# 脚本名称: 04-install-calico.sh
# 脚本描述: 安装Calico CNI网络插件 (幂等执行)
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

log_info "正在为集群 ${CLUSTER_ID} 安装Calico CNI (幂等模式)..."
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
# 检查Calico是否已安装
#===============================================================================
is_calico_installed() {
    if kubectl get namespace tigera-operator &>/dev/null && \
       kubectl get namespace calico-system &>/dev/null && \
       kubectl get daemonset calico-node -n calico-system &>/dev/null; then
        return 0
    fi
    return 1
}

#===============================================================================
# 2. 下载Calico配置文件
#===============================================================================
log_step "2. 下载Calico配置文件..."

mkdir -p /root/k8s-cluster${CLUSTER_ID}/calico
cd /root/k8s-cluster${CLUSTER_ID}/calico

# 只在文件不存在时下载
if [ ! -f tigera-operator.yaml ]; then
    wget -q "https://raw.githubusercontent.com/projectcalico/calico/v${CALICO_VERSION}/manifests/tigera-operator.yaml" \
        -O tigera-operator.yaml
    log_info "Tigera Operator配置下载完成"
else
    log_skip "Tigera Operator配置已存在"
fi

if [ ! -f custom-resources.yaml ]; then
    wget -q "https://raw.githubusercontent.com/projectcalico/calico/v${CALICO_VERSION}/manifests/custom-resources.yaml" \
        -O custom-resources.yaml
    log_info "Calico自定义资源配置下载完成"
else
    log_skip "Calico自定义资源配置已存在"
fi

#===============================================================================
# 3. 修改Pod CIDR配置
#===============================================================================
log_step "3. 配置Pod CIDR..."

# 修改CIDR（幂等：每次执行都确保正确）
sed -i "s|cidr: 192.168.0.0/16|cidr: ${POD_CIDR}|g" custom-resources.yaml
sed -i "s|cidr: 10.244.0.0/16|cidr: ${POD_CIDR}|g" custom-resources.yaml
sed -i "s|cidr: 10.245.0.0/16|cidr: ${POD_CIDR}|g" custom-resources.yaml

log_info "Pod CIDR配置为: ${POD_CIDR}"

#===============================================================================
# 4. 安装Tigera Operator
#===============================================================================
log_step "4. 安装Tigera Operator..."

if kubectl get namespace tigera-operator &>/dev/null; then
    log_skip "Tigera Operator命名空间已存在"
    # 使用apply更新
    kubectl apply -f tigera-operator.yaml 2>/dev/null || true
else
    kubectl apply -f tigera-operator.yaml
    log_info "Tigera Operator安装完成"
fi

# 等待Operator就绪
log_info "等待Tigera Operator就绪..."
kubectl wait --for=condition=Available deployment/tigera-operator \
    -n tigera-operator --timeout=120s 2>/dev/null || true

#===============================================================================
# 5. 安装Calico
#===============================================================================
log_step "5. 安装Calico..."

# 使用apply而不是create（幂等）
kubectl apply -f custom-resources.yaml

log_info "Calico安装/更新完成"

#===============================================================================
# 6. 等待Calico组件就绪
#===============================================================================
log_step "6. 等待Calico组件就绪..."

# 等待calico-system命名空间创建
log_info "等待calico-system命名空间..."
for i in {1..60}; do
    if kubectl get namespace calico-system &>/dev/null; then
        log_info "calico-system命名空间已创建"
        break
    fi
    sleep 2
done

# 等待calico-node DaemonSet就绪
log_info "等待calico-node就绪..."
kubectl rollout status daemonset/calico-node -n calico-system --timeout=300s 2>/dev/null || true

# 等待calico-kube-controllers就绪
log_info "等待calico-kube-controllers就绪..."
kubectl rollout status deployment/calico-kube-controllers -n calico-system --timeout=120s 2>/dev/null || true

#===============================================================================
# 7. 安装calicoctl
#===============================================================================
log_step "7. 安装calicoctl..."

# 检测架构
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
esac

# 检查是否需要安装
if command -v calicoctl &>/dev/null; then
    CURRENT_VERSION=$(calicoctl version 2>/dev/null | grep "Client Version" | awk '{print $3}' || echo "")
    if [ "$CURRENT_VERSION" = "v${CALICO_VERSION}" ]; then
        log_skip "calicoctl v${CALICO_VERSION} 已安装"
    else
        wget -q "https://github.com/projectcalico/calico/releases/download/v${CALICO_VERSION}/calicoctl-linux-${ARCH}" \
            -O /usr/local/bin/calicoctl
        chmod +x /usr/local/bin/calicoctl
        log_info "calicoctl已更新"
    fi
else
    wget -q "https://github.com/projectcalico/calico/releases/download/v${CALICO_VERSION}/calicoctl-linux-${ARCH}" \
        -O /usr/local/bin/calicoctl
    chmod +x /usr/local/bin/calicoctl
    log_info "calicoctl安装完成"
fi

# 配置calicoctl（幂等：覆盖写入）
mkdir -p /etc/calico
cat > /etc/calico/calicoctl.cfg << EOF
apiVersion: projectcalico.org/v3
kind: CalicoAPIConfig
metadata:
spec:
  datastoreType: "kubernetes"
  kubeconfig: "/root/.kube/config"
EOF

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证Calico安装..."

# 等待组件稳定
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

verify_check "Tigera Operator运行中" "kubectl get deployment tigera-operator -n tigera-operator -o jsonpath='{.status.availableReplicas}' | grep -q '1'"
verify_check "calico-node DaemonSet运行中" "kubectl get daemonset calico-node -n calico-system -o jsonpath='{.status.numberReady}' | grep -qv '^0'"
verify_check "calico-kube-controllers运行中" "kubectl get deployment calico-kube-controllers -n calico-system -o jsonpath='{.status.availableReplicas}' | grep -q '1'"
verify_check "节点状态为Ready" "kubectl get nodes | grep -q ' Ready'"
verify_check "calicoctl已安装" "calicoctl version"
verify_check "Calico IP Pool已创建" "kubectl get ippools.crd.projectcalico.org"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示状态
echo ""
log_step "================== Calico状态 =================="
echo ""
echo "=== Calico Pods ==="
kubectl get pods -n calico-system -o wide 2>/dev/null || true
echo ""
echo "=== 节点状态 ==="
kubectl get nodes -o wide 2>/dev/null || true
echo ""

#===============================================================================
# 8. 测试网络连通性
#===============================================================================
log_step "8. 测试网络连通性..."

# 清理可能存在的测试Pod
kubectl delete pod network-test --ignore-not-found=true &>/dev/null || true
sleep 2

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
kubectl wait --for=condition=Ready pod/network-test --timeout=120s 2>/dev/null || true

# 测试网络
if kubectl get pod network-test -o jsonpath='{.status.phase}' 2>/dev/null | grep -q Running; then
    log_info "✓ 测试Pod运行正常"
    POD_IP=$(kubectl get pod network-test -o jsonpath='{.status.podIP}' 2>/dev/null)
    log_info "测试Pod IP: ${POD_IP}"
    
    if kubectl exec network-test -- nslookup kubernetes.default &>/dev/null; then
        log_info "✓ DNS解析正常"
    else
        log_warn "✗ DNS解析可能有问题"
    fi
fi

# 清理测试Pod
kubectl delete pod network-test --ignore-not-found=true &>/dev/null || true

log_info "Calico CNI安装和配置完成！(幂等执行)"

echo ""
echo "=========================================="
echo "      Calico CNI安装完成!"
echo "      集群编号: ${CLUSTER_ID}"
echo "      版本: v${CALICO_VERSION}"
echo "      Pod CIDR: ${POD_CIDR}"
echo "      支持重复执行: 是"
echo "=========================================="
