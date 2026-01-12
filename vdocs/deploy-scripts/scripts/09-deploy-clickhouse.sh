#!/bin/bash
#===============================================================================
# 脚本名称: 09-deploy-clickhouse.sh
# 脚本描述: 使用KubeBlocks部署ClickHouse单机环境
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
CH_CLUSTER_NAME="clickhouse"
CH_VERSION="24.3.2"

# 检查kubectl连接
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群"
    exit 1
fi

log_info "开始部署ClickHouse..."
log_info "命名空间: ${NAMESPACE}"
log_info "版本: ${CH_VERSION}"

#===============================================================================
# 1. 确保命名空间存在
#===============================================================================
log_step "1. 确保命名空间存在..."

kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

#===============================================================================
# 2. 创建ClickHouse配置
#===============================================================================
log_step "2. 创建ClickHouse配置..."

cat > /tmp/clickhouse-config.yaml << 'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: clickhouse-config
  namespace: database
data:
  users.xml: |
    <?xml version="1.0"?>
    <clickhouse>
        <users>
            <default>
                <password></password>
                <networks>
                    <ip>::/0</ip>
                </networks>
                <profile>default</profile>
                <quota>default</quota>
                <access_management>1</access_management>
            </default>
            <admin>
                <password>admin123</password>
                <networks>
                    <ip>::/0</ip>
                </networks>
                <profile>default</profile>
                <quota>default</quota>
                <access_management>1</access_management>
            </admin>
        </users>
        <profiles>
            <default>
                <max_memory_usage>10000000000</max_memory_usage>
                <use_uncompressed_cache>0</use_uncompressed_cache>
                <load_balancing>random</load_balancing>
            </default>
        </profiles>
        <quotas>
            <default>
                <interval>
                    <duration>3600</duration>
                    <queries>0</queries>
                    <errors>0</errors>
                    <result_rows>0</result_rows>
                    <read_rows>0</read_rows>
                    <execution_time>0</execution_time>
                </interval>
            </default>
        </quotas>
    </clickhouse>
  config.xml: |
    <?xml version="1.0"?>
    <clickhouse>
        <logger>
            <level>information</level>
            <log>/var/log/clickhouse-server/clickhouse-server.log</log>
            <errorlog>/var/log/clickhouse-server/clickhouse-server.err.log</errorlog>
            <size>1000M</size>
            <count>10</count>
        </logger>
        <http_port>8123</http_port>
        <tcp_port>9000</tcp_port>
        <interserver_http_port>9009</interserver_http_port>
        <listen_host>0.0.0.0</listen_host>
        <max_connections>4096</max_connections>
        <keep_alive_timeout>3</keep_alive_timeout>
        <max_concurrent_queries>100</max_concurrent_queries>
        <max_server_memory_usage_to_ram_ratio>0.9</max_server_memory_usage_to_ram_ratio>
        <path>/var/lib/clickhouse/</path>
        <tmp_path>/var/lib/clickhouse/tmp/</tmp_path>
        <user_files_path>/var/lib/clickhouse/user_files/</user_files_path>
        <format_schema_path>/var/lib/clickhouse/format_schemas/</format_schema_path>
        <mark_cache_size>5368709120</mark_cache_size>
        <mlock_executable>true</mlock_executable>
    </clickhouse>
EOF

kubectl apply -f /tmp/clickhouse-config.yaml
log_info "ClickHouse配置创建完成"

#===============================================================================
# 3. 部署ClickHouse (使用KubeBlocks)
#===============================================================================
log_step "3. 部署ClickHouse集群..."

# 检查是否有可用的clickhouse clusterdefinition
if kubectl get clusterdefinition clickhouse &>/dev/null; then
    log_info "使用KubeBlocks部署ClickHouse..."
    
    cat > /tmp/ch-cluster.yaml << EOF
apiVersion: apps.kubeblocks.io/v1alpha1
kind: Cluster
metadata:
  name: ${CH_CLUSTER_NAME}
  namespace: ${NAMESPACE}
  labels:
    app: clickhouse
spec:
  clusterDefinitionRef: clickhouse
  clusterVersionRef: clickhouse-${CH_VERSION}
  terminationPolicy: Delete
  componentSpecs:
    - name: clickhouse
      componentDefRef: clickhouse
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

    kubectl apply -f /tmp/ch-cluster.yaml
else
    log_info "KubeBlocks clickhouse addon未启用，使用原生Deployment部署..."
    
    cat > /tmp/ch-deployment.yaml << EOF
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: ${CH_CLUSTER_NAME}
  namespace: ${NAMESPACE}
  labels:
    app: clickhouse
spec:
  serviceName: ${CH_CLUSTER_NAME}
  replicas: 1
  selector:
    matchLabels:
      app: clickhouse
  template:
    metadata:
      labels:
        app: clickhouse
    spec:
      containers:
        - name: clickhouse
          image: clickhouse/clickhouse-server:${CH_VERSION}
          ports:
            - containerPort: 8123
              name: http
            - containerPort: 9000
              name: native
            - containerPort: 9009
              name: interserver
          env:
            - name: CLICKHOUSE_DB
              value: "default"
            - name: CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT
              value: "1"
          resources:
            requests:
              cpu: "500m"
              memory: "2Gi"
            limits:
              cpu: "2"
              memory: "4Gi"
          volumeMounts:
            - name: data
              mountPath: /var/lib/clickhouse
            - name: logs
              mountPath: /var/log/clickhouse-server
            - name: config
              mountPath: /etc/clickhouse-server/config.d/config.xml
              subPath: config.xml
            - name: config
              mountPath: /etc/clickhouse-server/users.d/users.xml
              subPath: users.xml
          livenessProbe:
            httpGet:
              path: /ping
              port: 8123
            initialDelaySeconds: 30
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /ping
              port: 8123
            initialDelaySeconds: 10
            periodSeconds: 5
      volumes:
        - name: config
          configMap:
            name: clickhouse-config
        - name: logs
          emptyDir: {}
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 50Gi
---
apiVersion: v1
kind: Service
metadata:
  name: ${CH_CLUSTER_NAME}
  namespace: ${NAMESPACE}
  labels:
    app: clickhouse
spec:
  type: ClusterIP
  ports:
    - port: 8123
      targetPort: 8123
      name: http
    - port: 9000
      targetPort: 9000
      name: native
    - port: 9009
      targetPort: 9009
      name: interserver
  selector:
    app: clickhouse
---
apiVersion: v1
kind: Service
metadata:
  name: ${CH_CLUSTER_NAME}-nodeport
  namespace: ${NAMESPACE}
  labels:
    app: clickhouse
spec:
  type: NodePort
  ports:
    - port: 8123
      targetPort: 8123
      nodePort: 30123
      name: http
    - port: 9000
      targetPort: 9000
      nodePort: 30900
      name: native
  selector:
    app: clickhouse
EOF

    kubectl apply -f /tmp/ch-deployment.yaml
fi

log_info "ClickHouse部署请求已提交"

#===============================================================================
# 4. 创建ClickHouse Exporter (用于Prometheus监控)
#===============================================================================
log_step "4. 部署ClickHouse Exporter..."

cat > /tmp/ch-exporter.yaml << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: clickhouse-exporter
  namespace: ${NAMESPACE}
  labels:
    app: clickhouse-exporter
spec:
  replicas: 1
  selector:
    matchLabels:
      app: clickhouse-exporter
  template:
    metadata:
      labels:
        app: clickhouse-exporter
    spec:
      containers:
        - name: exporter
          image: f1yegor/clickhouse-exporter:latest
          ports:
            - containerPort: 9116
              name: metrics
          env:
            - name: CLICKHOUSE_SERVER
              value: "${CH_CLUSTER_NAME}.${NAMESPACE}.svc.cluster.local"
            - name: CLICKHOUSE_PORT
              value: "8123"
          args:
            - -scrape_uri=http://${CH_CLUSTER_NAME}.${NAMESPACE}.svc.cluster.local:8123/
          resources:
            requests:
              cpu: "50m"
              memory: "64Mi"
            limits:
              cpu: "100m"
              memory: "128Mi"
---
apiVersion: v1
kind: Service
metadata:
  name: clickhouse-exporter
  namespace: ${NAMESPACE}
  labels:
    app: clickhouse-exporter
    component: exporter
spec:
  ports:
    - port: 9116
      targetPort: 9116
      name: metrics
  selector:
    app: clickhouse-exporter
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: clickhouse-exporter
  namespace: ${NAMESPACE}
  labels:
    app: clickhouse
    release: prometheus
spec:
  selector:
    matchLabels:
      app: clickhouse-exporter
  namespaceSelector:
    matchNames:
      - ${NAMESPACE}
  endpoints:
    - port: metrics
      interval: 30s
      path: /metrics
EOF

kubectl apply -f /tmp/ch-exporter.yaml 2>/dev/null || log_warn "部分资源可能已存在或CRD未安装"

log_info "ClickHouse Exporter部署完成"

#===============================================================================
# 5. 等待ClickHouse就绪
#===============================================================================
log_step "5. 等待ClickHouse就绪..."

log_info "等待ClickHouse Pod就绪..."
for i in {1..60}; do
    READY=$(kubectl get pods -n ${NAMESPACE} -l app=clickhouse -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "Pending")
    if [ "$READY" = "Running" ]; then
        log_info "ClickHouse Pod已就绪"
        break
    fi
    echo -n "."
    sleep 10
done
echo ""

#===============================================================================
# 6. 初始化ClickHouse数据库
#===============================================================================
log_step "6. 初始化ClickHouse数据库..."

cat > /tmp/ch-init.yaml << EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: clickhouse-init
  namespace: ${NAMESPACE}
spec:
  template:
    spec:
      restartPolicy: OnFailure
      initContainers:
        - name: wait-for-ch
          image: busybox:latest
          command:
            - /bin/sh
            - -c
            - |
              until wget -q -O- http://${CH_CLUSTER_NAME}:8123/ping; do
                echo "Waiting for ClickHouse..."
                sleep 5
              done
      containers:
        - name: init
          image: yandex/clickhouse-client:latest
          command:
            - /bin/sh
            - -c
            - |
              clickhouse-client --host ${CH_CLUSTER_NAME} --query "
              CREATE DATABASE IF NOT EXISTS monitoring;
              
              CREATE TABLE IF NOT EXISTS monitoring.metrics (
                  timestamp DateTime,
                  metric_name String,
                  metric_value Float64,
                  tags Map(String, String)
              ) ENGINE = MergeTree()
              PARTITION BY toYYYYMM(timestamp)
              ORDER BY (metric_name, timestamp);
              
              CREATE TABLE IF NOT EXISTS monitoring.logs (
                  timestamp DateTime,
                  level String,
                  message String,
                  source String,
                  metadata Map(String, String)
              ) ENGINE = MergeTree()
              PARTITION BY toYYYYMM(timestamp)
              ORDER BY (source, timestamp);
              "
              echo "ClickHouse initialized successfully"
EOF

kubectl apply -f /tmp/ch-init.yaml

log_info "ClickHouse初始化Job已创建"

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证ClickHouse部署..."

# 等待一段时间
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

# 验证Pod
verify_check "ClickHouse Pod运行中" "kubectl get pods -n ${NAMESPACE} -l app=clickhouse | grep -q Running"

# 验证Service
verify_check "ClickHouse Service存在" "kubectl get svc ${CH_CLUSTER_NAME} -n ${NAMESPACE}"

# 验证Exporter
verify_check "ClickHouse Exporter运行中" "kubectl get pods -n ${NAMESPACE} -l app=clickhouse-exporter | grep -q Running"

# 验证ConfigMap
verify_check "ClickHouse ConfigMap存在" "kubectl get configmap clickhouse-config -n ${NAMESPACE}"

# 验证HTTP端口
CH_POD=$(kubectl get pods -n ${NAMESPACE} -l app=clickhouse -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "$CH_POD" ]; then
    verify_check "ClickHouse HTTP端口可访问" "kubectl exec -n ${NAMESPACE} ${CH_POD} -- wget -q -O- http://localhost:8123/ping"
fi

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示集群信息
echo ""
log_step "================== ClickHouse信息 =================="
echo ""
echo "=== Pods ==="
kubectl get pods -n ${NAMESPACE} -l app=clickhouse
kubectl get pods -n ${NAMESPACE} -l app=clickhouse-exporter
echo ""
echo "=== Services ==="
kubectl get svc -n ${NAMESPACE} | grep clickhouse
echo ""

# 获取访问信息
log_step "获取ClickHouse访问信息..."
echo ""
echo "=== ClickHouse访问信息 ==="
echo "内部HTTP: http://${CH_CLUSTER_NAME}.${NAMESPACE}.svc.cluster.local:8123"
echo "内部Native: ${CH_CLUSTER_NAME}.${NAMESPACE}.svc.cluster.local:9000"
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
echo "外部HTTP: http://${NODE_IP}:30123"
echo "外部Native: ${NODE_IP}:30900"
echo ""
echo "用户: admin / admin123"
echo ""

# 测试ClickHouse连接
log_step "7. 测试ClickHouse连接..."

if [ -n "$CH_POD" ]; then
    log_info "测试查询..."
    kubectl exec -n ${NAMESPACE} ${CH_POD} -- clickhouse-client --query "SELECT version()" 2>/dev/null || log_warn "无法执行测试查询"
fi

log_info "ClickHouse部署完成！"

echo ""
echo "=========================================="
echo "      ClickHouse部署完成!"
echo "      版本: ${CH_VERSION}"
echo "      命名空间: ${NAMESPACE}"
echo "=========================================="
echo ""
echo "访问方式:"
echo "  - HTTP: http://${CH_CLUSTER_NAME}:8123"
echo "  - Native: ${CH_CLUSTER_NAME}:9000"
echo "  - 用户: admin / admin123"
echo "=========================================="
