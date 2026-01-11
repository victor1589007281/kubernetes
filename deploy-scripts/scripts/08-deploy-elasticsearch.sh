#!/bin/bash
#===============================================================================
# 脚本名称: 08-deploy-elasticsearch.sh
# 脚本描述: 使用KubeBlocks部署Elasticsearch单机环境
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

# 配置
NAMESPACE="database"
ES_VERSION="8.8.2"
ES_CLUSTER_NAME="elasticsearch"

# 检查kubectl连接
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群"
    exit 1
fi

log_info "开始部署Elasticsearch..."
log_info "命名空间: ${NAMESPACE}"
log_info "版本: ${ES_VERSION}"

#===============================================================================
# 1. 确保命名空间存在
#===============================================================================
log_step "1. 确保命名空间存在..."

kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

#===============================================================================
# 2. 配置Elasticsearch节点调优
#===============================================================================
log_step "2. 配置系统调优参数..."

# 创建init容器配置，用于设置vm.max_map_count
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
# 3. 部署Elasticsearch (使用KubeBlocks)
#===============================================================================
log_step "3. 部署Elasticsearch集群..."

# 检查是否有可用的elasticsearch clusterdefinition
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
    log_info "KubeBlocks elasticsearch addon未启用，使用Helm部署..."
    
    # 添加Elastic Helm仓库
    helm repo add elastic https://helm.elastic.co 2>/dev/null || true
    helm repo update
    
    # 部署Elasticsearch
    cat > /tmp/es-values.yaml << EOF
replicas: 1
minimumMasterNodes: 1

# 单节点模式
clusterName: "${ES_CLUSTER_NAME}"

# 资源配置
resources:
  requests:
    cpu: "500m"
    memory: "2Gi"
  limits:
    cpu: "2"
    memory: "4Gi"

# JVM堆内存
esJavaOpts: "-Xmx1g -Xms1g"

# 存储配置
volumeClaimTemplate:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 50Gi

# 安全配置（开发环境禁用）
protocol: http
esConfig:
  elasticsearch.yml: |
    xpack.security.enabled: false
    xpack.security.transport.ssl.enabled: false
    xpack.security.http.ssl.enabled: false
    discovery.type: single-node
    cluster.routing.allocation.disk.threshold_enabled: false

# 单节点发现
discovery.type: single-node

# 初始化容器设置vm.max_map_count
sysctlInitContainer:
  enabled: true

# 持久化
persistence:
  enabled: true

# 服务类型
service:
  type: ClusterIP

# 健康检查
healthCheck:
  enabled: true
EOF

    helm upgrade --install ${ES_CLUSTER_NAME} elastic/elasticsearch \
        -n ${NAMESPACE} \
        -f /tmp/es-values.yaml \
        --version 8.5.1 \
        --wait --timeout 10m
fi

log_info "Elasticsearch部署请求已提交"

#===============================================================================
# 4. 部署Kibana (可选，用于日志可视化)
#===============================================================================
log_step "4. 部署Kibana..."

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

#===============================================================================
# 5. 创建Elasticsearch索引模板
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
          "number_of_replicas": 0,
          "index.lifecycle.name": "mysql-logs-policy",
          "index.lifecycle.rollover_alias": "mysql-logs"
        },
        "mappings": {
          "properties": {
            "@timestamp": { "type": "date" },
            "log_type": { "type": "keyword" },
            "cluster_name": { "type": "keyword" },
            "pod_name": { "type": "keyword" },
            "namespace": { "type": "keyword" },
            "message": { "type": "text" },
            "query_time": { "type": "float" },
            "lock_time": { "type": "float" },
            "rows_sent": { "type": "integer" },
            "rows_examined": { "type": "integer" }
          }
        }
      }
    }
---
apiVersion: batch/v1
kind: Job
metadata:
  name: es-init-templates
  namespace: ${NAMESPACE}
spec:
  template:
    spec:
      restartPolicy: OnFailure
      initContainers:
        - name: wait-for-es
          image: busybox:latest
          command:
            - /bin/sh
            - -c
            - |
              until wget -q -O- http://${ES_CLUSTER_NAME}-master:9200/_cluster/health; do
                echo "Waiting for Elasticsearch..."
                sleep 5
              done
      containers:
        - name: init-templates
          image: curlimages/curl:latest
          command:
            - /bin/sh
            - -c
            - |
              # 创建ILM策略
              curl -X PUT "http://${ES_CLUSTER_NAME}-master:9200/_ilm/policy/mysql-logs-policy" \
                -H 'Content-Type: application/json' \
                -d '{
                  "policy": {
                    "phases": {
                      "hot": {
                        "min_age": "0ms",
                        "actions": {
                          "rollover": {
                            "max_age": "7d",
                            "max_size": "10gb"
                          }
                        }
                      },
                      "delete": {
                        "min_age": "30d",
                        "actions": {
                          "delete": {}
                        }
                      }
                    }
                  }
                }'
              
              # 创建索引模板
              curl -X PUT "http://${ES_CLUSTER_NAME}-master:9200/_index_template/mysql-logs-template" \
                -H 'Content-Type: application/json' \
                -d @/config/mysql-logs-template.json
              
              echo "Index templates created successfully"
          volumeMounts:
            - name: templates
              mountPath: /config
      volumes:
        - name: templates
          configMap:
            name: es-index-templates
EOF

kubectl apply -f /tmp/es-index-template.yaml

log_info "索引模板配置完成"

#===============================================================================
# 6. 创建Elasticsearch Service (确保可访问)
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

kubectl apply -f /tmp/es-service.yaml 2>/dev/null || log_info "Service可能已存在"

#===============================================================================
# 7. 等待Elasticsearch就绪
#===============================================================================
log_step "7. 等待Elasticsearch就绪..."

log_info "等待Elasticsearch Pod就绪..."
for i in {1..60}; do
    READY=$(kubectl get pods -n ${NAMESPACE} -l app=elasticsearch -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "Pending")
    if [ "$READY" = "Running" ]; then
        log_info "Elasticsearch Pod已就绪"
        break
    fi
    echo -n "."
    sleep 10
done
echo ""

# 等待服务可访问
log_info "等待Elasticsearch服务可访问..."
for i in {1..30}; do
    if kubectl exec -n ${NAMESPACE} -it $(kubectl get pods -n ${NAMESPACE} -l app=elasticsearch -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) -- curl -s http://localhost:9200/_cluster/health 2>/dev/null | grep -q "status"; then
        log_info "Elasticsearch服务已就绪"
        break
    fi
    echo -n "."
    sleep 5
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

# 验证Pod
verify_check "Elasticsearch Pod运行中" "kubectl get pods -n ${NAMESPACE} -l app=elasticsearch | grep -q Running"

# 验证Service
verify_check "Elasticsearch Service存在" "kubectl get svc ${ES_CLUSTER_NAME}-master -n ${NAMESPACE}"

# 验证Kibana
verify_check "Kibana Deployment存在" "kubectl get deployment kibana -n ${NAMESPACE}"
verify_check "Kibana Pod运行中" "kubectl get pods -n ${NAMESPACE} -l app=kibana | grep -q Running"

# 验证ConfigMap
verify_check "索引模板ConfigMap存在" "kubectl get configmap es-index-templates -n ${NAMESPACE}"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示集群信息
echo ""
log_step "================== Elasticsearch信息 =================="
echo ""
echo "=== Pods ==="
kubectl get pods -n ${NAMESPACE} -l app=elasticsearch
kubectl get pods -n ${NAMESPACE} -l app=kibana
echo ""
echo "=== Services ==="
kubectl get svc -n ${NAMESPACE} | grep -E "elasticsearch|kibana"
echo ""

# 获取访问信息
log_step "获取Elasticsearch访问信息..."
echo ""
echo "=== Elasticsearch访问信息 ==="
echo "内部地址: http://${ES_CLUSTER_NAME}-master.${NAMESPACE}.svc.cluster.local:9200"
echo ""
echo "=== Kibana访问信息 ==="
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
echo "访问地址: http://${NODE_IP}:30561"
echo ""

# 测试Elasticsearch连接
log_step "8. 测试Elasticsearch连接..."

ES_POD=$(kubectl get pods -n ${NAMESPACE} -l app=elasticsearch -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "$ES_POD" ]; then
    log_info "测试集群健康状态..."
    kubectl exec -n ${NAMESPACE} ${ES_POD} -- curl -s http://localhost:9200/_cluster/health?pretty 2>/dev/null || log_warn "无法获取集群健康状态"
fi

log_info "Elasticsearch部署完成！"

echo ""
echo "=========================================="
echo "      Elasticsearch部署完成!"
echo "      版本: ${ES_VERSION}"
echo "      命名空间: ${NAMESPACE}"
echo "=========================================="
echo ""
echo "访问方式:"
echo "  - ES: http://${ES_CLUSTER_NAME}-master.${NAMESPACE}.svc:9200"
echo "  - Kibana: http://<node-ip>:30561"
echo "=========================================="
