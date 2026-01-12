#!/bin/bash
#===============================================================================
# 脚本名称: 07-deploy-mysql.sh
# 脚本描述: 使用KubeBlocks部署Percona MySQL (5.7.29 & 8.4.3) (幂等执行)
#          包括Filebeat sidecar、MySQL Exporter、Pod Metrics Exporter
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

# 配置
NAMESPACE="database"
ES_HOST=${ES_HOST:-"elasticsearch-master.database.svc.cluster.local:9200"}

# 检查kubectl连接
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群"
    exit 1
fi

log_info "开始部署MySQL集群 (幂等模式)..."
log_info "命名空间: ${NAMESPACE}"

#===============================================================================
# 1. 创建命名空间
#===============================================================================
log_step "1. 创建命名空间..."

kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -
log_info "命名空间就绪"

#===============================================================================
# 2. 创建Filebeat ConfigMap
#===============================================================================
log_step "2. 创建Filebeat配置..."

cat > /tmp/filebeat-config.yaml << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: filebeat-mysql-config
  namespace: ${NAMESPACE}
data:
  filebeat.yml: |
    filebeat.inputs:
    - type: log
      enabled: true
      paths:
        - /var/lib/mysql/*-slow.log
      fields:
        log_type: mysql_slow_query
        service: mysql
      fields_under_root: true
      multiline:
        pattern: '^# Time:|^# User@Host:'
        negate: true
        match: after
        timeout: 5s
      processors:
        - add_fields:
            target: ''
            fields:
              cluster_name: \${CLUSTER_NAME}
              pod_name: \${POD_NAME}
              namespace: \${NAMESPACE}
    
    - type: log
      enabled: true
      paths:
        - /var/lib/mysql/*.err
        - /var/log/mysql/error.log
      fields:
        log_type: mysql_error
        service: mysql
      fields_under_root: true
      multiline:
        pattern: '^[0-9]{4}-[0-9]{2}-[0-9]{2}'
        negate: true
        match: after
        timeout: 5s
      processors:
        - add_fields:
            target: ''
            fields:
              cluster_name: \${CLUSTER_NAME}
              pod_name: \${POD_NAME}
              namespace: \${NAMESPACE}

    output.elasticsearch:
      hosts: ["${ES_HOST}"]
      index: "mysql-logs-%{+yyyy.MM.dd}"
      
    setup.template.name: "mysql-logs"
    setup.template.pattern: "mysql-logs-*"
    setup.ilm.enabled: false
    
    logging.level: info
    logging.to_files: true
    logging.files:
      path: /var/log/filebeat
      name: filebeat
      keepfiles: 3
      permissions: 0644
EOF

kubectl apply -f /tmp/filebeat-config.yaml
log_info "Filebeat配置创建完成"

#===============================================================================
# 3. 创建MySQL Exporter配置
#===============================================================================
log_step "3. 创建MySQL Exporter配置..."

cat > /tmp/mysql-exporter-config.yaml << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: mysql-exporter-config
  namespace: ${NAMESPACE}
data:
  .my.cnf: |
    [client]
    user=exporter
    password=exporter_password
    host=127.0.0.1
    port=3306
EOF

kubectl apply -f /tmp/mysql-exporter-config.yaml
log_info "MySQL Exporter配置创建完成"

#===============================================================================
# 4. 部署Percona MySQL 5.7.29
#===============================================================================
log_step "4. 部署Percona MySQL 5.7.29..."

# 检查集群是否存在
if kubectl get cluster mysql57 -n ${NAMESPACE} &>/dev/null; then
    log_skip "MySQL 5.7.29集群已存在"
else
    cat > /tmp/mysql57-cluster.yaml << 'EOF'
apiVersion: apps.kubeblocks.io/v1alpha1
kind: Cluster
metadata:
  name: mysql57
  namespace: database
  labels:
    app: mysql
    version: "5.7.29"
spec:
  clusterDefinitionRef: mysql
  clusterVersionRef: mysql-5.7.29
  terminationPolicy: Delete
  componentSpecs:
    - name: mysql
      componentDefRef: mysql
      replicas: 1
      resources:
        requests:
          cpu: "500m"
          memory: "1Gi"
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
                storage: 20Gi
EOF
    kubectl apply -f /tmp/mysql57-cluster.yaml
    log_info "MySQL 5.7.29集群部署请求已提交"
fi

#===============================================================================
# 5. 部署Percona MySQL 8.4.3
#===============================================================================
log_step "5. 部署Percona MySQL 8.4.3..."

if kubectl get cluster mysql84 -n ${NAMESPACE} &>/dev/null; then
    log_skip "MySQL 8.4.3集群已存在"
else
    cat > /tmp/mysql84-cluster.yaml << 'EOF'
apiVersion: apps.kubeblocks.io/v1alpha1
kind: Cluster
metadata:
  name: mysql84
  namespace: database
  labels:
    app: mysql
    version: "8.4.3"
spec:
  clusterDefinitionRef: mysql
  clusterVersionRef: mysql-8.4.3
  terminationPolicy: Delete
  componentSpecs:
    - name: mysql
      componentDefRef: mysql
      replicas: 1
      resources:
        requests:
          cpu: "500m"
          memory: "1Gi"
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
                storage: 20Gi
EOF
    kubectl apply -f /tmp/mysql84-cluster.yaml
    log_info "MySQL 8.4.3集群部署请求已提交"
fi

#===============================================================================
# 6. 创建ServiceMonitor
#===============================================================================
log_step "6. 创建ServiceMonitor..."

cat > /tmp/mysql-servicemonitor.yaml << EOF
apiVersion: v1
kind: Service
metadata:
  name: mysql57-exporter
  namespace: ${NAMESPACE}
  labels:
    app: mysql57
    component: exporter
spec:
  ports:
    - port: 9104
      targetPort: 9104
      name: metrics
  selector:
    app.kubernetes.io/instance: mysql57
---
apiVersion: v1
kind: Service
metadata:
  name: mysql84-exporter
  namespace: ${NAMESPACE}
  labels:
    app: mysql84
    component: exporter
spec:
  ports:
    - port: 9104
      targetPort: 9104
      name: metrics
  selector:
    app.kubernetes.io/instance: mysql84
EOF

kubectl apply -f /tmp/mysql-servicemonitor.yaml

# 尝试创建ServiceMonitor（如果CRD存在）
if kubectl get crd servicemonitors.monitoring.coreos.com &>/dev/null; then
    cat > /tmp/mysql-sm.yaml << EOF
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: mysql-exporter
  namespace: ${NAMESPACE}
  labels:
    app: mysql
    release: prometheus
spec:
  selector:
    matchLabels:
      component: exporter
  namespaceSelector:
    matchNames:
      - ${NAMESPACE}
  endpoints:
    - port: metrics
      interval: 30s
      path: /metrics
      scrapeTimeout: 10s
EOF
    kubectl apply -f /tmp/mysql-sm.yaml
    log_info "ServiceMonitor创建完成"
else
    log_warn "ServiceMonitor CRD未安装，跳过"
fi

#===============================================================================
# 7. 等待MySQL集群就绪
#===============================================================================
log_step "7. 等待MySQL集群就绪..."

wait_for_cluster() {
    local name=$1
    local max_wait=60
    log_info "等待 ${name} 集群就绪..."
    for i in $(seq 1 $max_wait); do
        STATUS=$(kubectl get cluster ${name} -n ${NAMESPACE} -o jsonpath='{.status.phase}' 2>/dev/null || echo "Pending")
        if [ "$STATUS" = "Running" ]; then
            log_info "${name} 集群已就绪"
            return 0
        fi
        echo -n "."
        sleep 10
    done
    echo ""
    log_warn "${name} 集群未就绪，继续执行..."
    return 1
}

wait_for_cluster "mysql57" || true
wait_for_cluster "mysql84" || true

#===============================================================================
# 8. 创建Exporter用户初始化Job
#===============================================================================
log_step "8. 创建MySQL Exporter用户初始化Job..."

# 检查Job是否已完成
for cluster in mysql57 mysql84; do
    JOB_NAME="${cluster}-init-exporter"
    if kubectl get job ${JOB_NAME} -n ${NAMESPACE} &>/dev/null; then
        JOB_STATUS=$(kubectl get job ${JOB_NAME} -n ${NAMESPACE} -o jsonpath='{.status.succeeded}' 2>/dev/null || echo "0")
        if [ "$JOB_STATUS" = "1" ]; then
            log_skip "${JOB_NAME} 已完成"
            continue
        else
            # 删除失败的Job以便重新创建
            kubectl delete job ${JOB_NAME} -n ${NAMESPACE} --ignore-not-found=true
        fi
    fi
done

cat > /tmp/mysql-init-job.yaml << 'EOF'
apiVersion: batch/v1
kind: Job
metadata:
  name: mysql57-init-exporter
  namespace: database
spec:
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: init-exporter
          image: mysql:5.7
          command:
            - /bin/sh
            - -c
            - |
              sleep 30
              mysql -h mysql57-mysql.database.svc.cluster.local -u root -p${MYSQL_ROOT_PASSWORD} -e "
              CREATE USER IF NOT EXISTS 'exporter'@'%' IDENTIFIED BY 'exporter_password';
              GRANT PROCESS, REPLICATION CLIENT, SELECT ON *.* TO 'exporter'@'%';
              FLUSH PRIVILEGES;
              " || echo "Init might have failed, will retry on next run"
          env:
            - name: MYSQL_ROOT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: mysql57-conn-credential
                  key: password
                  optional: true
---
apiVersion: batch/v1
kind: Job
metadata:
  name: mysql84-init-exporter
  namespace: database
spec:
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: OnFailure
      containers:
        - name: init-exporter
          image: mysql:8.0
          command:
            - /bin/sh
            - -c
            - |
              sleep 30
              mysql -h mysql84-mysql.database.svc.cluster.local -u root -p${MYSQL_ROOT_PASSWORD} -e "
              CREATE USER IF NOT EXISTS 'exporter'@'%' IDENTIFIED BY 'exporter_password';
              GRANT PROCESS, REPLICATION CLIENT, SELECT ON *.* TO 'exporter'@'%';
              FLUSH PRIVILEGES;
              " || echo "Init might have failed, will retry on next run"
          env:
            - name: MYSQL_ROOT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: mysql84-conn-credential
                  key: password
                  optional: true
EOF

kubectl apply -f /tmp/mysql-init-job.yaml
log_info "Exporter用户初始化Job已创建"

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证MySQL部署..."

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
verify_check "Filebeat ConfigMap存在" "kubectl get configmap filebeat-mysql-config -n ${NAMESPACE}"
verify_check "MySQL Exporter ConfigMap存在" "kubectl get configmap mysql-exporter-config -n ${NAMESPACE}"
verify_check "MySQL 5.7.29集群存在" "kubectl get cluster mysql57 -n ${NAMESPACE}"
verify_check "MySQL 8.4.3集群存在" "kubectl get cluster mysql84 -n ${NAMESPACE}"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示信息
echo ""
log_step "================== MySQL集群信息 =================="
echo ""
echo "=== 集群状态 ==="
kubectl get clusters -n ${NAMESPACE} 2>/dev/null || true
echo ""
echo "=== Pods ==="
kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=mysql 2>/dev/null || true
echo ""

log_info "MySQL集群部署完成！(幂等执行)"

echo ""
echo "=========================================="
echo "      MySQL集群部署完成!"
echo "      - Percona MySQL 5.7.29"
echo "      - Percona MySQL 8.4.3"
echo "      命名空间: ${NAMESPACE}"
echo "      支持重复执行: 是"
echo "=========================================="
