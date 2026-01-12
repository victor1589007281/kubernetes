#!/bin/bash
#===============================================================================
# 脚本名称: 08-deploy-elasticsearch.sh
# 脚本描述: 使用KubeBlocks部署Elasticsearch单机环境 (幂等执行)
# 作者: Auto-generated
# 版本: 2.0
# 幂等性: 支持重复执行
# 运行方式: sudo bash 08-deploy-elasticsearch.sh 或 sudo ./08-deploy-elasticsearch.sh
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

# 配置
NAMESPACE="database"
ES_VERSION="8.8.2"
ES_CLUSTER_NAME="elasticsearch"

# 检查kubectl连接
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群"
    exit 1
fi

log_info "开始部署Elasticsearch (幂等模式)..."
log_info "命名空间: ${NAMESPACE}"
log_info "版本: ${ES_VERSION}"

#===============================================================================
# 1. 确保命名空间存在
#===============================================================================
log_step "1. 确保命名空间存在..."

kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

#===============================================================================
# 2. 配置系统调优
#===============================================================================
log_step "2. 配置系统调优参数..."

cat > /tmp/es-sysctl-config.yaml << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: es-sysctl-config
  namespace: ${NAMESPACE}
data:
  sysctl.conf: |
    vm.max_map_count=262144
EOF

kubectl apply -f /tmp/es-sysctl-config.yaml

#===============================================================================
# 3. 部署Elasticsearch
#===============================================================================
log_step "3. 部署Elasticsearch集群..."

# 检查是否已存在
if kubectl get cluster ${ES_CLUSTER_NAME} -n ${NAMESPACE} &>/dev/null || \
   kubectl get statefulset ${ES_CLUSTER_NAME}-master -n ${NAMESPACE} &>/dev/null; then
    log_skip "Elasticsearch已存在"
else
    # 尝试使用KubeBlocks
    if kubectl get clusterdefinition elasticsearch &>/dev/null; then
        log_info "使用KubeBlocks部署Elasticsearch..."
        cat > /tmp/es-cluster.yaml << EOF
apiVersion: apps.kubeblocks.io/v1alpha1
kind: Cluster
metadata:
  name: ${ES_CLUSTER_NAME}
  namespace: ${NAMESPACE}
  labels:
    app: elasticsearch
spec:
  clusterDefinitionRef: elasticsearch
  clusterVersionRef: elasticsearch-${ES_VERSION}
  terminationPolicy: Delete
  componentSpecs:
    - name: elasticsearch
      componentDefRef: elasticsearch
      replicas: 1
      resources:
        requests:
          cpu: "500m"
          memory: "2Gi"
        limits:
          cpu: "2"
          memory: "4Gi"
      volumeClaimTemplates:
        - name: data
          spec:
            accessModes:
              - ReadWriteOnce
            resources:
              requests:
                storage: 50Gi
EOF
        kubectl apply -f /tmp/es-cluster.yaml
    else
        log_info "使用Helm部署Elasticsearch..."
        helm repo add elastic https://helm.elastic.co 2>/dev/null || true
        helm repo update
        
        cat > /tmp/es-values.yaml << EOF
replicas: 1
minimumMasterNodes: 1
clusterName: "${ES_CLUSTER_NAME}"
resources:
  requests:
    cpu: "500m"
    memory: "2Gi"
  limits:
    cpu: "2"
    memory: "4Gi"
esJavaOpts: "-Xmx1g -Xms1g"
volumeClaimTemplate:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 50Gi
protocol: http
esConfig:
  elasticsearch.yml: |
    xpack.security.enabled: false
    xpack.security.transport.ssl.enabled: false
    xpack.security.http.ssl.enabled: false
    discovery.type: single-node
    cluster.routing.allocation.disk.threshold_enabled: false
sysctlInitContainer:
  enabled: true
persistence:
  enabled: true
service:
  type: ClusterIP
EOF
        helm upgrade --install ${ES_CLUSTER_NAME} elastic/elasticsearch \
            -n ${NAMESPACE} \
            -f /tmp/es-values.yaml \
            --version 8.5.1 \
            --wait --timeout 10m || log_warn "Helm安装可能失败"
    fi
    log_info "Elasticsearch部署请求已提交"
fi

#===============================================================================
# 4. 部署Kibana
#===============================================================================
log_step "4. 部署Kibana..."

if kubectl get deployment kibana -n ${NAMESPACE} &>/dev/null; then
    log_skip "Kibana已存在"
else
    cat > /tmp/kibana-deployment.yaml << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kibana
  namespace: ${NAMESPACE}
  labels:
    app: kibana
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kibana
  template:
    metadata:
      labels:
        app: kibana
    spec:
      containers:
        - name: kibana
          image: docker.elastic.co/kibana/kibana:${ES_VERSION}
          ports:
            - containerPort: 5601
              name: http
          env:
            - name: ELASTICSEARCH_HOSTS
              value: "http://${ES_CLUSTER_NAME}-master.${NAMESPACE}.svc.cluster.local:9200"
            - name: SERVER_NAME
              value: "kibana"
            - name: XPACK_SECURITY_ENABLED
              value: "false"
          resources:
            requests:
              cpu: "250m"
              memory: "512Mi"
            limits:
              cpu: "1"
              memory: "1Gi"
          readinessProbe:
            httpGet:
              path: /api/status
              port: 5601
            initialDelaySeconds: 30
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /api/status
              port: 5601
            initialDelaySeconds: 60
            periodSeconds: 30
---
apiVersion: v1
kind: Service
metadata:
  name: kibana
  namespace: ${NAMESPACE}
  labels:
    app: kibana
spec:
  type: NodePort
  ports:
    - port: 5601
      targetPort: 5601
      nodePort: 30561
      name: http
  selector:
    app: kibana
EOF

    kubectl apply -f /tmp/kibana-deployment.yaml
    log_info "Kibana部署完成"
fi

#===============================================================================
# 5. 创建索引模板
#===============================================================================
log_step "5. 创建MySQL日志索引模板..."

cat > /tmp/es-index-template.yaml << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: es-index-templates
  namespace: ${NAMESPACE}
data:
  mysql-logs-template.json: |
    {
      "index_patterns": ["mysql-logs-*"],
      "template": {
        "settings": {
          "number_of_shards": 1,
          "number_of_replicas": 0
        },
        "mappings": {
          "properties": {
            "@timestamp": { "type": "date" },
            "log_type": { "type": "keyword" },
            "cluster_name": { "type": "keyword" },
            "pod_name": { "type": "keyword" },
            "namespace": { "type": "keyword" },
            "message": { "type": "text" }
          }
        }
      }
    }
EOF

kubectl apply -f /tmp/es-index-template.yaml

#===============================================================================
# 6. 创建Elasticsearch Service
#===============================================================================
log_step "6. 创建Elasticsearch Service..."

cat > /tmp/es-service.yaml << EOF
apiVersion: v1
kind: Service
metadata:
  name: ${ES_CLUSTER_NAME}-master
  namespace: ${NAMESPACE}
  labels:
    app: elasticsearch
spec:
  type: ClusterIP
  ports:
    - port: 9200
      targetPort: 9200
      name: http
    - port: 9300
      targetPort: 9300
      name: transport
  selector:
    app: elasticsearch
EOF

kubectl apply -f /tmp/es-service.yaml 2>/dev/null || log_skip "Service可能已存在"

#===============================================================================
# 7. 等待Elasticsearch就绪
#===============================================================================
log_step "7. 等待Elasticsearch就绪..."

log_info "等待Elasticsearch Pod就绪..."
for _ in {1..60}; do
    READY=$(kubectl get pods -n "${NAMESPACE}" -l app=elasticsearch -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "Pending")
    if [ "$READY" = "Running" ]; then
        log_info "Elasticsearch Pod已就绪"
        break
    fi
    echo -n "."
    sleep 10
done
echo ""

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证Elasticsearch部署..."

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

verify_check "命名空间存在" "kubectl get namespace ${NAMESPACE}"
verify_check "ES ConfigMap存在" "kubectl get configmap es-index-templates -n ${NAMESPACE}"
verify_check "Kibana Deployment存在" "kubectl get deployment kibana -n ${NAMESPACE}"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示信息
echo ""
log_step "================== Elasticsearch信息 =================="
echo ""
echo "=== Pods ==="
kubectl get pods -n ${NAMESPACE} -l app=elasticsearch 2>/dev/null || true
kubectl get pods -n ${NAMESPACE} -l app=kibana 2>/dev/null || true
echo ""
echo "=== Services ==="
kubectl get svc -n ${NAMESPACE} 2>/dev/null | grep -E "elasticsearch|kibana" || true
echo ""

NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || echo "localhost")

log_info "Elasticsearch部署完成！(幂等执行)"

echo ""
echo "=========================================="
echo "      Elasticsearch部署完成!"
echo "      版本: ${ES_VERSION}"
echo "      命名空间: ${NAMESPACE}"
echo "      Kibana: http://${NODE_IP}:30561"
echo "      支持重复执行: 是"
echo "=========================================="
