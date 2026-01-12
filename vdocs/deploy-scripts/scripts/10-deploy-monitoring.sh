#!/bin/bash
#===============================================================================
# 脚本名称: 10-deploy-monitoring.sh
# 脚本描述: 部署Prometheus + Grafana监控系统
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
NAMESPACE="monitoring"
PROMETHEUS_VERSION="25.27.0"
GRAFANA_ADMIN_PASSWORD="admin123"

# 检查kubectl连接
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群"
    exit 1
fi

log_info "开始部署监控系统..."
log_info "命名空间: ${NAMESPACE}"

#===============================================================================
# 1. 创建命名空间
#===============================================================================
log_step "1. 创建监控命名空间..."

kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

#===============================================================================
# 2. 添加Helm仓库
#===============================================================================
log_step "2. 添加Helm仓库..."

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

log_info "Helm仓库添加完成"

#===============================================================================
# 3. 部署kube-prometheus-stack
#===============================================================================
log_step "3. 部署kube-prometheus-stack..."

cat > /tmp/prometheus-values.yaml << EOF
# Prometheus配置
prometheus:
  prometheusSpec:
    retention: 15d
    retentionSize: "10GB"
    resources:
      requests:
        cpu: 200m
        memory: 512Mi
      limits:
        cpu: 1000m
        memory: 2Gi
    storageSpec:
      volumeClaimTemplate:
        spec:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 50Gi
    # 添加额外的scrape配置
    additionalScrapeConfigs:
      # MySQL Exporter
      - job_name: 'mysql-exporter'
        kubernetes_sd_configs:
          - role: endpoints
            namespaces:
              names:
                - database
        relabel_configs:
          - source_labels: [__meta_kubernetes_service_label_component]
            regex: exporter
            action: keep
          - source_labels: [__meta_kubernetes_service_name]
            regex: mysql.*-exporter
            action: keep
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - source_labels: [__meta_kubernetes_service_name]
            target_label: service
      # ClickHouse Exporter
      - job_name: 'clickhouse-exporter'
        kubernetes_sd_configs:
          - role: endpoints
            namespaces:
              names:
                - database
        relabel_configs:
          - source_labels: [__meta_kubernetes_service_label_app]
            regex: clickhouse-exporter
            action: keep
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
      # Pod Metrics
      - job_name: 'kubernetes-pods'
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
            action: keep
            regex: true
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
            action: replace
            target_label: __metrics_path__
            regex: (.+)
          - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
            action: replace
            regex: ([^:]+)(?::\d+)?;(\d+)
            replacement: \$1:\$2
            target_label: __address__
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - source_labels: [__meta_kubernetes_pod_name]
            target_label: pod
  service:
    type: NodePort
    nodePort: 30090

# Alertmanager配置
alertmanager:
  alertmanagerSpec:
    resources:
      requests:
        cpu: 50m
        memory: 64Mi
      limits:
        cpu: 200m
        memory: 256Mi
    storage:
      volumeClaimTemplate:
        spec:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 10Gi
  service:
    type: NodePort
    nodePort: 30093

# Grafana配置
grafana:
  enabled: true
  adminPassword: "${GRAFANA_ADMIN_PASSWORD}"
  persistence:
    enabled: true
    size: 10Gi
  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi
  service:
    type: NodePort
    nodePort: 30030
  # 预配置数据源
  additionalDataSources:
    - name: Elasticsearch
      type: elasticsearch
      url: http://elasticsearch-master.database.svc.cluster.local:9200
      access: proxy
      isDefault: false
      jsonData:
        esVersion: "8.0.0"
        timeField: "@timestamp"
        logMessageField: message
        logLevelField: level
    - name: ClickHouse
      type: grafana-clickhouse-datasource
      url: http://clickhouse.database.svc.cluster.local:8123
      access: proxy
      isDefault: false
      jsonData:
        defaultDatabase: monitoring
  # 安装ClickHouse插件
  plugins:
    - grafana-clickhouse-datasource
  # 预配置Dashboard
  dashboardProviders:
    dashboardproviders.yaml:
      apiVersion: 1
      providers:
        - name: 'default'
          orgId: 1
          folder: ''
          type: file
          disableDeletion: false
          editable: true
          options:
            path: /var/lib/grafana/dashboards/default
  dashboardsConfigMaps:
    default: grafana-dashboards

# 禁用一些不需要的组件（单机环境）
kubeEtcd:
  enabled: true
kubeControllerManager:
  enabled: true
kubeScheduler:
  enabled: true
kubeProxy:
  enabled: true

# Node Exporter
nodeExporter:
  enabled: true

# Kube State Metrics
kubeStateMetrics:
  enabled: true

# Prometheus Operator
prometheusOperator:
  resources:
    requests:
      cpu: 100m
      memory: 100Mi
    limits:
      cpu: 200m
      memory: 200Mi
EOF

helm upgrade --install prometheus prometheus-community/kube-prometheus-stack \
    -n ${NAMESPACE} \
    -f /tmp/prometheus-values.yaml \
    --version ${PROMETHEUS_VERSION} \
    --wait --timeout 10m

log_info "kube-prometheus-stack部署完成"

#===============================================================================
# 4. 创建MySQL监控Dashboard
#===============================================================================
log_step "4. 创建Grafana Dashboard配置..."

cat > /tmp/grafana-dashboards.yaml << 'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-dashboards
  namespace: monitoring
  labels:
    grafana_dashboard: "1"
data:
  mysql-overview.json: |
    {
      "annotations": {
        "list": []
      },
      "editable": true,
      "fiscalYearStartMonth": 0,
      "graphTooltip": 0,
      "id": null,
      "links": [],
      "liveNow": false,
      "panels": [
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {"color": "green", "value": null},
                  {"color": "red", "value": 80}
                ]
              },
              "unit": "short"
            }
          },
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0},
          "id": 1,
          "options": {},
          "targets": [
            {
              "expr": "mysql_global_status_threads_connected",
              "legendFormat": "{{instance}}",
              "refId": "A"
            }
          ],
          "title": "MySQL Connections",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {"color": "green", "value": null}
                ]
              },
              "unit": "ops"
            }
          },
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0},
          "id": 2,
          "options": {},
          "targets": [
            {
              "expr": "rate(mysql_global_status_queries[5m])",
              "legendFormat": "{{instance}}",
              "refId": "A"
            }
          ],
          "title": "MySQL QPS",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "fieldConfig": {
            "defaults": {
              "color": {
                "mode": "palette-classic"
              },
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {"color": "green", "value": null}
                ]
              },
              "unit": "bytes"
            }
          },
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8},
          "id": 3,
          "options": {},
          "targets": [
            {
              "expr": "mysql_global_status_innodb_buffer_pool_bytes_data",
              "legendFormat": "{{instance}}",
              "refId": "A"
            }
          ],
          "title": "InnoDB Buffer Pool",
          "type": "timeseries"
        },
        {
          "datasource": {
            "type": "prometheus",
            "uid": "prometheus"
          },
          "fieldConfig": {
            "defaults": {
              "mappings": [],
              "thresholds": {
                "mode": "absolute",
                "steps": [
                  {"color": "green", "value": null},
                  {"color": "yellow", "value": 50},
                  {"color": "red", "value": 100}
                ]
              },
              "unit": "short"
            }
          },
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 8},
          "id": 4,
          "options": {},
          "targets": [
            {
              "expr": "mysql_global_status_slow_queries",
              "legendFormat": "{{instance}}",
              "refId": "A"
            }
          ],
          "title": "Slow Queries Total",
          "type": "stat"
        }
      ],
      "refresh": "30s",
      "schemaVersion": 38,
      "style": "dark",
      "tags": ["mysql", "database"],
      "templating": {"list": []},
      "time": {"from": "now-1h", "to": "now"},
      "timepicker": {},
      "timezone": "",
      "title": "MySQL Overview",
      "uid": "mysql-overview",
      "version": 1
    }
EOF

kubectl apply -f /tmp/grafana-dashboards.yaml

log_info "Grafana Dashboard配置完成"

#===============================================================================
# 5. 创建PrometheusRule (告警规则)
#===============================================================================
log_step "5. 创建告警规则..."

cat > /tmp/prometheus-rules.yaml << 'EOF'
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: mysql-alerts
  namespace: monitoring
  labels:
    release: prometheus
spec:
  groups:
    - name: mysql
      rules:
        - alert: MySQLDown
          expr: mysql_up == 0
          for: 1m
          labels:
            severity: critical
          annotations:
            summary: "MySQL instance down"
            description: "MySQL instance {{ $labels.instance }} is down"
        
        - alert: MySQLHighConnections
          expr: mysql_global_status_threads_connected > 100
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "MySQL high connections"
            description: "MySQL instance {{ $labels.instance }} has {{ $value }} connections"
        
        - alert: MySQLSlowQueries
          expr: rate(mysql_global_status_slow_queries[5m]) > 0.1
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "MySQL slow queries detected"
            description: "MySQL instance {{ $labels.instance }} has slow queries"
        
        - alert: MySQLReplicationLag
          expr: mysql_slave_status_seconds_behind_master > 30
          for: 1m
          labels:
            severity: warning
          annotations:
            summary: "MySQL replication lag"
            description: "MySQL slave {{ $labels.instance }} is {{ $value }}s behind master"
    
    - name: elasticsearch
      rules:
        - alert: ElasticsearchClusterRed
          expr: elasticsearch_cluster_health_status{color="red"} == 1
          for: 5m
          labels:
            severity: critical
          annotations:
            summary: "Elasticsearch cluster red"
            description: "Elasticsearch cluster is in red status"
        
        - alert: ElasticsearchHeapUsageHigh
          expr: elasticsearch_jvm_memory_used_bytes{area="heap"} / elasticsearch_jvm_memory_max_bytes{area="heap"} > 0.9
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "Elasticsearch heap usage high"
            description: "Elasticsearch heap usage is above 90%"
    
    - name: clickhouse
      rules:
        - alert: ClickHouseDown
          expr: up{job="clickhouse-exporter"} == 0
          for: 1m
          labels:
            severity: critical
          annotations:
            summary: "ClickHouse down"
            description: "ClickHouse instance is down"
        
        - alert: ClickHouseHighMemoryUsage
          expr: clickhouse_asynchronous_metrics_MemoryTracking > 8589934592
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "ClickHouse high memory usage"
            description: "ClickHouse memory usage is above 8GB"
EOF

kubectl apply -f /tmp/prometheus-rules.yaml

log_info "告警规则创建完成"

#===============================================================================
# 6. 创建Pod Metrics Exporter DaemonSet
#===============================================================================
log_step "6. 部署Pod Metrics Exporter..."

cat > /tmp/pod-metrics-exporter.yaml << 'EOF'
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: pod-metrics-exporter
  namespace: monitoring
  labels:
    app: pod-metrics-exporter
spec:
  selector:
    matchLabels:
      app: pod-metrics-exporter
  template:
    metadata:
      labels:
        app: pod-metrics-exporter
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9102"
    spec:
      serviceAccountName: prometheus-kube-prometheus-prometheus
      containers:
        - name: exporter
          image: prom/node-exporter:v1.7.0
          ports:
            - containerPort: 9102
              name: metrics
          args:
            - --web.listen-address=:9102
            - --path.procfs=/host/proc
            - --path.sysfs=/host/sys
            - --path.rootfs=/host/root
            - --collector.filesystem.mount-points-exclude=^/(dev|proc|sys|var/lib/docker/.+|var/lib/kubelet/.+)($|/)
          volumeMounts:
            - name: proc
              mountPath: /host/proc
              readOnly: true
            - name: sys
              mountPath: /host/sys
              readOnly: true
            - name: root
              mountPath: /host/root
              readOnly: true
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 100m
              memory: 128Mi
      volumes:
        - name: proc
          hostPath:
            path: /proc
        - name: sys
          hostPath:
            path: /sys
        - name: root
          hostPath:
            path: /
---
apiVersion: v1
kind: Service
metadata:
  name: pod-metrics-exporter
  namespace: monitoring
  labels:
    app: pod-metrics-exporter
spec:
  ports:
    - port: 9102
      targetPort: 9102
      name: metrics
  selector:
    app: pod-metrics-exporter
EOF

kubectl apply -f /tmp/pod-metrics-exporter.yaml

log_info "Pod Metrics Exporter部署完成"

#===============================================================================
# 7. 等待所有组件就绪
#===============================================================================
log_step "7. 等待监控组件就绪..."

log_info "等待Prometheus就绪..."
kubectl rollout status statefulset/prometheus-prometheus-kube-prometheus-prometheus -n ${NAMESPACE} --timeout=300s || true

log_info "等待Alertmanager就绪..."
kubectl rollout status statefulset/alertmanager-prometheus-kube-prometheus-alertmanager -n ${NAMESPACE} --timeout=120s || true

log_info "等待Grafana就绪..."
kubectl rollout status deployment/prometheus-grafana -n ${NAMESPACE} --timeout=120s || true

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证监控系统部署..."

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

# 验证Prometheus
verify_check "Prometheus运行中" "kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=prometheus | grep -q Running"

# 验证Alertmanager
verify_check "Alertmanager运行中" "kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=alertmanager | grep -q Running"

# 验证Grafana
verify_check "Grafana运行中" "kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=grafana | grep -q Running"

# 验证Node Exporter
verify_check "Node Exporter运行中" "kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=prometheus-node-exporter | grep -q Running"

# 验证Kube State Metrics
verify_check "Kube State Metrics运行中" "kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=kube-state-metrics | grep -q Running"

# 验证PrometheusRule
verify_check "MySQL告警规则已创建" "kubectl get prometheusrule mysql-alerts -n ${NAMESPACE}"

# 验证Dashboard ConfigMap
verify_check "Grafana Dashboard ConfigMap存在" "kubectl get configmap grafana-dashboards -n ${NAMESPACE}"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示状态信息
echo ""
log_step "================== 监控系统信息 =================="
echo ""
echo "=== Pods ==="
kubectl get pods -n ${NAMESPACE}
echo ""
echo "=== Services ==="
kubectl get svc -n ${NAMESPACE}
echo ""

# 获取访问信息
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
echo ""
log_step "================== 访问信息 =================="
echo ""
echo "Prometheus:   http://${NODE_IP}:30090"
echo "Alertmanager: http://${NODE_IP}:30093"
echo "Grafana:      http://${NODE_IP}:30030"
echo ""
echo "Grafana登录信息:"
echo "  用户名: admin"
echo "  密码: ${GRAFANA_ADMIN_PASSWORD}"
echo ""

log_info "监控系统部署完成！"

echo ""
echo "=========================================="
echo "      监控系统部署完成!"
echo "      Prometheus: http://${NODE_IP}:30090"
echo "      Grafana: http://${NODE_IP}:30030"
echo "=========================================="

# 保存访问信息
cat > /root/monitoring-info.txt << EOF
监控系统访问信息
================

Prometheus
----------
URL: http://${NODE_IP}:30090
内部地址: http://prometheus-kube-prometheus-prometheus.${NAMESPACE}.svc:9090

Alertmanager
------------
URL: http://${NODE_IP}:30093
内部地址: http://prometheus-kube-prometheus-alertmanager.${NAMESPACE}.svc:9093

Grafana
-------
URL: http://${NODE_IP}:30030
用户名: admin
密码: ${GRAFANA_ADMIN_PASSWORD}
内部地址: http://prometheus-grafana.${NAMESPACE}.svc:80

已配置的数据源:
- Prometheus (默认)
- Elasticsearch
- ClickHouse

已创建的告警规则:
- MySQLDown
- MySQLHighConnections
- MySQLSlowQueries
- MySQLReplicationLag
- ElasticsearchClusterRed
- ElasticsearchHeapUsageHigh
- ClickHouseDown
- ClickHouseHighMemoryUsage
EOF

log_info "访问信息已保存到: /root/monitoring-info.txt"
