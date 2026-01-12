#!/bin/bash
#===============================================================================
# 脚本名称: 06-install-kubeblocks.sh
# 脚本描述: 安装KubeBlocks数据库管理平台 (幂等执行)
# 作者: Auto-generated
# 版本: 2.0
# 幂等性: 支持重复执行
# 运行方式: sudo bash 06-install-kubeblocks.sh 或 sudo ./06-install-kubeblocks.sh
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

# 检查kubectl连接
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群"
    exit 1
fi

# 配置
KUBEBLOCKS_VERSION="0.9.2"
KUBEBLOCKS_NAMESPACE="kb-system"

log_info "KubeBlocks版本: v${KUBEBLOCKS_VERSION}"
log_info "命名空间: ${KUBEBLOCKS_NAMESPACE}"
log_info "幂等模式: 已启用"

#===============================================================================
# 检查KubeBlocks是否已安装
#===============================================================================
is_kubeblocks_installed() {
    if kubectl get deployment kubeblocks -n ${KUBEBLOCKS_NAMESPACE} &>/dev/null; then
        return 0
    fi
    return 1
}

#===============================================================================
# 1. 安装kbcli命令行工具
#===============================================================================
log_step "1. 安装kbcli命令行工具..."

if command -v kbcli &> /dev/null; then
    CURRENT_VERSION=$(kbcli version --client 2>/dev/null | grep -oP '\d+\.\d+\.\d+' | head -1 || echo "")
    if [ "$CURRENT_VERSION" = "$KUBEBLOCKS_VERSION" ]; then
        log_skip "kbcli v${KUBEBLOCKS_VERSION} 已安装"
    else
        curl -fsSL https://kubeblocks.io/installer/install_cli.sh | bash -s ${KUBEBLOCKS_VERSION}
        log_info "kbcli已更新到 v${KUBEBLOCKS_VERSION}"
    fi
else
    curl -fsSL https://kubeblocks.io/installer/install_cli.sh | bash -s ${KUBEBLOCKS_VERSION}
    export PATH=$PATH:/usr/local/bin
    log_info "kbcli安装完成"
fi

#===============================================================================
# 2. 安装Helm
#===============================================================================
log_step "2. 检查并安装Helm..."

if command -v helm &> /dev/null; then
    log_skip "Helm已安装: $(helm version --short)"
else
    curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
    log_info "Helm安装完成: $(helm version --short)"
fi

#===============================================================================
# 3. 添加KubeBlocks Helm仓库
#===============================================================================
log_step "3. 添加KubeBlocks Helm仓库..."

# 添加仓库（幂等：已存在则跳过）
if helm repo list 2>/dev/null | grep -q kubeblocks; then
    log_skip "KubeBlocks Helm仓库已添加"
else
    helm repo add kubeblocks https://apecloud.github.io/helm-charts
fi

helm repo update
log_info "Helm仓库更新完成"

#===============================================================================
# 4. 安装KubeBlocks
#===============================================================================
log_step "4. 安装KubeBlocks..."

# 确保命名空间存在
kubectl create namespace ${KUBEBLOCKS_NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

if is_kubeblocks_installed; then
    log_skip "KubeBlocks已安装"
    # 检查是否需要升级
    log_info "检查KubeBlocks状态..."
    kbcli kubeblocks status 2>/dev/null || true
else
    kbcli kubeblocks install \
        --version ${KUBEBLOCKS_VERSION} \
        --set image.pullPolicy=IfNotPresent \
        --set dataProtection.enabled=true \
        --set prometheus.enabled=true \
        --set grafana.enabled=true \
        2>/dev/null || {
            log_warn "kbcli安装失败，尝试使用Helm安装..."
            helm upgrade --install kubeblocks kubeblocks/kubeblocks \
                -n ${KUBEBLOCKS_NAMESPACE} \
                --version ${KUBEBLOCKS_VERSION} \
                --set image.pullPolicy=IfNotPresent \
                --wait --timeout 10m
        }
    log_info "KubeBlocks安装完成"
fi

#===============================================================================
# 5. 等待KubeBlocks就绪
#===============================================================================
log_step "5. 等待KubeBlocks组件就绪..."

log_info "等待KubeBlocks Pods就绪..."
kubectl wait --for=condition=Ready pods --all -n ${KUBEBLOCKS_NAMESPACE} --timeout=600s 2>/dev/null || true

#===============================================================================
# 6. 安装数据库Addons
#===============================================================================
log_step "6. 安装数据库Addons..."

# 定义需要安装的addons
ADDONS=("mysql" "elasticsearch" "clickhouse" "prometheus" "grafana")

for addon in "${ADDONS[@]}"; do
    # 检查addon是否已启用
    if kbcli addon list 2>/dev/null | grep -q "$addon.*Enabled"; then
        log_skip "$addon addon已启用"
    else
        log_info "安装 $addon addon..."
        kbcli addon enable "$addon" 2>/dev/null || log_warn "$addon addon安装失败或不可用"
    fi
done

# 等待addons启用
sleep 10

#===============================================================================
# 7. 查看已安装的Addons
#===============================================================================
log_step "7. 查看已安装的Addons..."

kbcli addon list 2>/dev/null || kubectl get addons -n ${KUBEBLOCKS_NAMESPACE} 2>/dev/null || true

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证KubeBlocks安装..."

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

verify_check "kbcli已安装" "kbcli version"
verify_check "KubeBlocks Operator运行中" "kubectl get deployment kubeblocks -n ${KUBEBLOCKS_NAMESPACE} -o jsonpath='{.status.availableReplicas}' | grep -qv '^0'"
verify_check "Cluster CRD已安装" "kubectl get crd clusters.apps.kubeblocks.io"
verify_check "ClusterDefinition CRD已安装" "kubectl get crd clusterdefinitions.apps.kubeblocks.io"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示状态
echo ""
log_step "================== KubeBlocks状态 =================="
echo ""
echo "=== KubeBlocks Pods ==="
kubectl get pods -n ${KUBEBLOCKS_NAMESPACE} 2>/dev/null || true
echo ""
echo "=== 已安装的Addons ==="
kbcli addon list 2>/dev/null || true
echo ""

#===============================================================================
# 8. 配置存储类
#===============================================================================
log_step "8. 配置存储..."

if kubectl get storageclass 2>/dev/null | grep -q "(default)"; then
    log_skip "默认存储类已存在"
else
    log_info "安装local-path-provisioner..."
    kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/master/deploy/local-path-storage.yaml 2>/dev/null || log_warn "local-path-provisioner安装失败"
fi

echo ""
echo "=== 存储类 ==="
kubectl get storageclass 2>/dev/null || true
echo ""

# 保存使用说明（幂等：覆盖）
cat > /root/kubeblocks-usage.txt << 'EOF'
KubeBlocks使用说明
==================

1. 查看KubeBlocks状态:
   kbcli kubeblocks status

2. 查看可用的数据库类型:
   kbcli clusterdefinition list

3. 查看可用的版本:
   kbcli clusterversion list

4. 创建MySQL集群:
   kbcli cluster create mysql-cluster \
     --cluster-definition mysql \
     --cluster-version mysql-8.0.33

5. 创建Elasticsearch集群:
   kbcli cluster create es-cluster \
     --cluster-definition elasticsearch

6. 创建ClickHouse集群:
   kbcli cluster create ch-cluster \
     --cluster-definition clickhouse

7. 查看所有集群:
   kbcli cluster list

8. 查看集群详情:
   kbcli cluster describe <cluster-name>

9. 删除集群:
   kbcli cluster delete <cluster-name>

10. 查看Addons:
    kbcli addon list
EOF

log_info "KubeBlocks安装和配置完成！(幂等执行)"

echo ""
echo "=========================================="
echo "      KubeBlocks安装完成!"
echo "      版本: v${KUBEBLOCKS_VERSION}"
echo "      命名空间: ${KUBEBLOCKS_NAMESPACE}"
echo "      支持重复执行: 是"
echo "=========================================="
