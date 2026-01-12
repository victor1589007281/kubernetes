#!/bin/bash
#===============================================================================
# 脚本名称: 06-install-kubeblocks.sh
# 脚本描述: 安装KubeBlocks数据库管理平台
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

#===============================================================================
# 1. 安装kbcli命令行工具
#===============================================================================
log_step "1. 安装kbcli命令行工具..."

# 检测架构
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
esac

# 下载kbcli
curl -fsSL https://kubeblocks.io/installer/install_cli.sh | bash -s ${KUBEBLOCKS_VERSION}

# 验证安装
if command -v kbcli &> /dev/null; then
    log_info "kbcli安装完成: $(kbcli version --client 2>/dev/null || echo 'installed')"
else
    # 手动添加到PATH
    export PATH=$PATH:/usr/local/bin
    log_info "kbcli已安装"
fi

#===============================================================================
# 2. 安装Helm (如果未安装)
#===============================================================================
log_step "2. 检查并安装Helm..."

if ! command -v helm &> /dev/null; then
    curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
    log_info "Helm安装完成: $(helm version --short)"
else
    log_info "Helm已安装: $(helm version --short)"
fi

#===============================================================================
# 3. 添加KubeBlocks Helm仓库
#===============================================================================
log_step "3. 添加KubeBlocks Helm仓库..."

helm repo add kubeblocks https://apecloud.github.io/helm-charts
helm repo update

log_info "Helm仓库添加完成"

#===============================================================================
# 4. 安装KubeBlocks
#===============================================================================
log_step "4. 安装KubeBlocks..."

# 创建命名空间
kubectl create namespace ${KUBEBLOCKS_NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

# 使用kbcli安装KubeBlocks
kbcli kubeblocks install \
    --version ${KUBEBLOCKS_VERSION} \
    --set image.pullPolicy=IfNotPresent \
    --set dataProtection.enabled=true \
    --set prometheus.enabled=true \
    --set grafana.enabled=true

log_info "KubeBlocks安装请求已提交"

#===============================================================================
# 5. 等待KubeBlocks就绪
#===============================================================================
log_step "5. 等待KubeBlocks组件就绪..."

# 等待所有Pod就绪
log_info "等待KubeBlocks Pods就绪 (这可能需要几分钟)..."

kubectl wait --for=condition=Ready pods --all -n ${KUBEBLOCKS_NAMESPACE} --timeout=600s || true

#===============================================================================
# 6. 安装数据库Addons
#===============================================================================
log_step "6. 安装数据库Addons..."

# 安装MySQL addon (包含Percona)
log_info "安装MySQL addon..."
kbcli addon enable mysql || true

# 安装Elasticsearch addon
log_info "安装Elasticsearch addon..."
kbcli addon enable elasticsearch || true

# 安装ClickHouse addon
log_info "安装ClickHouse addon..."
kbcli addon enable clickhouse || true

# 安装Prometheus监控
log_info "安装Prometheus监控..."
kbcli addon enable prometheus || true

# 安装Grafana可视化
log_info "安装Grafana可视化..."
kbcli addon enable grafana || true

# 等待addons启用
sleep 30

#===============================================================================
# 7. 查看已安装的Addons
#===============================================================================
log_step "7. 查看已安装的Addons..."

kbcli addon list

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

# 验证kbcli
verify_check "kbcli已安装" "kbcli version"

# 验证KubeBlocks Operator
verify_check "KubeBlocks Operator运行中" "kubectl get deployment kubeblocks -n ${KUBEBLOCKS_NAMESPACE} -o jsonpath='{.status.availableReplicas}' | grep -qv '^0'"

# 验证KubeBlocks状态
verify_check "KubeBlocks状态正常" "kbcli kubeblocks status | grep -q 'KubeBlocks is running'"

# 验证CRDs
verify_check "Cluster CRD已安装" "kubectl get crd clusters.apps.kubeblocks.io"
verify_check "ClusterDefinition CRD已安装" "kubectl get crd clusterdefinitions.apps.kubeblocks.io"
verify_check "ClusterVersion CRD已安装" "kubectl get crd clusterversions.apps.kubeblocks.io"

# 验证Addons
verify_check "MySQL addon已启用" "kbcli addon list | grep mysql | grep -q Enabled"
verify_check "Elasticsearch addon已启用" "kbcli addon list | grep elasticsearch | grep -q Enabled"
verify_check "ClickHouse addon已启用" "kbcli addon list | grep clickhouse | grep -q Enabled"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示状态信息
echo ""
log_step "================== KubeBlocks状态 =================="
echo ""
echo "=== KubeBlocks Pods ==="
kubectl get pods -n ${KUBEBLOCKS_NAMESPACE}
echo ""
echo "=== 已安装的Addons ==="
kbcli addon list
echo ""
echo "=== 可用的ClusterDefinitions ==="
kubectl get clusterdefinitions
echo ""

#===============================================================================
# 8. 创建存储类 (如果使用本地存储)
#===============================================================================
log_step "8. 配置存储..."

# 检查是否有可用的StorageClass
if ! kubectl get storageclass 2>/dev/null | grep -q "(default)"; then
    log_info "创建本地存储类..."
    
    # 创建本地路径存储类
    cat > /tmp/local-storage.yaml << 'EOF'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: local-path
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: rancher.io/local-path
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
EOF

    # 安装local-path-provisioner
    kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/master/deploy/local-path-storage.yaml
    
    log_info "本地存储类创建完成"
else
    log_info "已存在默认存储类"
fi

# 显示存储类
echo ""
echo "=== 存储类 ==="
kubectl get storageclass
echo ""

log_info "KubeBlocks安装和配置完成！"

echo ""
echo "=========================================="
echo "      KubeBlocks安装完成!"
echo "      版本: v${KUBEBLOCKS_VERSION}"
echo "      命名空间: ${KUBEBLOCKS_NAMESPACE}"
echo "=========================================="

# 保存使用说明
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

log_info "使用说明已保存到: /root/kubeblocks-usage.txt"
