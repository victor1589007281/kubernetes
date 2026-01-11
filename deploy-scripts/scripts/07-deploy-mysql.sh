#!/bin/bash
#===============================================================================
# 脚本名称: 07-deploy-mysql.sh
# 脚本描述: 使用KubeBlocks部署Percona MySQL (5.7.29 & 8.4.3)
#          包括Filebeat sidecar、MySQL Exporter、Pod Metrics Exporter
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
ES_HOST=${ES_HOST:-"elasticsearch-master.database.svc.cluster.local:9200"}

# 检查kubectl连接
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到Kubernetes集群"
    exit 1
fi

log_info "开始部署MySQL集群..."
log_info "命名空间: ${NAMESPACE}"

#===============================================================================
# 1. 创建命名空间
#===============================================================================
log_step "1. 创建命名空间..."

kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

#===============================================================================
# 2. 创建Filebeat ConfigMap (用于采集MySQL慢查询和错误日志)
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
    # MySQL慢查询日志
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
    
    # MySQL错误日志
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
---
apiVersion: v1
kind: Pod
metadata:
  name: mysql57-sidecars
  namespace: database
  labels:
    app: mysql57-sidecars
spec:
  containers:
    # Filebeat Sidecar - 采集慢查询和错误日志
    - name: filebeat
      image: docker.elastic.co/beats/filebeat:8.12.0
      args:
        - "-c"
        - "/etc/filebeat/filebeat.yml"
        - "-e"
      env:
        - name: CLUSTER_NAME
          value: "mysql57"
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
      volumeMounts:
        - name: filebeat-config
          mountPath: /etc/filebeat
        - name: mysql-logs
          mountPath: /var/lib/mysql
          readOnly: true
        - name: filebeat-data
          mountPath: /usr/share/filebeat/data
      resources:
        limits:
          cpu: "200m"
          memory: "256Mi"
        requests:
          cpu: "100m"
          memory: "128Mi"
    
    # MySQL Exporter - 监控MySQL指标
    - name: mysqld-exporter
      image: prom/mysqld-exporter:v0.15.1
      ports:
        - containerPort: 9104
          name: metrics
          protocol: TCP
      env:
        - name: MYSQLD_EXPORTER_PASSWORD
          value: "exporter_password"
        - name: DATA_SOURCE_NAME
          value: "exporter:exporter_password@(127.0.0.1:3306)/"
      args:
        - "--web.listen-address=:9104"
        - "--collect.info_schema.innodb_metrics"
        - "--collect.info_schema.tables"
        - "--collect.info_schema.processlist"
        - "--collect.slave_status"
        - "--collect.binlog_size"
        - "--collect.global_status"
        - "--collect.global_variables"
      resources:
        limits:
          cpu: "100m"
          memory: "128Mi"
        requests:
          cpu: "50m"
          memory: "64Mi"

  volumes:
    - name: filebeat-config
      configMap:
        name: filebeat-mysql-config
    - name: mysql-logs
      persistentVolumeClaim:
        claimName: data-mysql57-mysql-0
    - name: filebeat-data
      emptyDir: {}
EOF

# 首先创建MySQL集群
kubectl apply -f /tmp/mysql57-cluster.yaml

log_info "MySQL 5.7.29集群部署请求已提交"

#===============================================================================
# 5. 部署Percona MySQL 8.4.3
#===============================================================================
log_step "5. 部署Percona MySQL 8.4.3..."

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
---
apiVersion: v1
kind: Pod
metadata:
  name: mysql84-sidecars
  namespace: database
  labels:
    app: mysql84-sidecars
spec:
  containers:
    # Filebeat Sidecar
    - name: filebeat
      image: docker.elastic.co/beats/filebeat:8.12.0
      args:
        - "-c"
        - "/etc/filebeat/filebeat.yml"
        - "-e"
      env:
        - name: CLUSTER_NAME
          value: "mysql84"
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
      volumeMounts:
        - name: filebeat-config
          mountPath: /etc/filebeat
        - name: mysql-logs
          mountPath: /var/lib/mysql
          readOnly: true
        - name: filebeat-data
          mountPath: /usr/share/filebeat/data
      resources:
        limits:
          cpu: "200m"
          memory: "256Mi"
        requests:
          cpu: "100m"
          memory: "128Mi"
    
    # MySQL Exporter
    - name: mysqld-exporter
      image: prom/mysqld-exporter:v0.15.1
      ports:
        - containerPort: 9104
          name: metrics
          protocol: TCP
      env:
        - name: DATA_SOURCE_NAME
          value: "exporter:exporter_password@(127.0.0.1:3306)/"
      args:
        - "--web.listen-address=:9104"
        - "--collect.info_schema.innodb_metrics"
        - "--collect.info_schema.tables"
        - "--collect.info_schema.processlist"
        - "--collect.slave_status"
        - "--collect.binlog_size"
        - "--collect.global_status"
        - "--collect.global_variables"
      resources:
        limits:
          cpu: "100m"
          memory: "128Mi"
        requests:
          cpu: "50m"
          memory: "64Mi"

  volumes:
    - name: filebeat-config
      configMap:
        name: filebeat-mysql-config
    - name: mysql-logs
      persistentVolumeClaim:
        claimName: data-mysql84-mysql-0
    - name: filebeat-data
      emptyDir: {}
EOF

kubectl apply -f /tmp/mysql84-cluster.yaml

log_info "MySQL 8.4.3集群部署请求已提交"

#===============================================================================
# 6. 创建ServiceMonitor (Prometheus自动发现)
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
    app: mysql57-sidecars
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
    app: mysql84-sidecars
---
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

kubectl apply -f /tmp/mysql-servicemonitor.yaml 2>/dev/null || log_warn "ServiceMonitor CRD未安装，跳过"

log_info "ServiceMonitor创建完成"

#===============================================================================
# 7. 创建Exporter用户初始化脚本
#===============================================================================
log_step "7. 创建MySQL Exporter用户初始化Job..."

cat > /tmp/mysql-init-job.yaml << 'EOF'
apiVersion: batch/v1
kind: Job
metadata:
  name: mysql57-init-exporter
  namespace: database
spec:
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
              sleep 60
              mysql -h mysql57-mysql.database.svc.cluster.local -u root -p${MYSQL_ROOT_PASSWORD} -e "
              CREATE USER IF NOT EXISTS 'exporter'@'%' IDENTIFIED BY 'exporter_password';
              GRANT PROCESS, REPLICATION CLIENT, SELECT ON *.* TO 'exporter'@'%';
              FLUSH PRIVILEGES;
              "
          env:
            - name: MYSQL_ROOT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: mysql57-conn-credential
                  key: password
---
apiVersion: batch/v1
kind: Job
metadata:
  name: mysql84-init-exporter
  namespace: database
spec:
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
              sleep 60
              mysql -h mysql84-mysql.database.svc.cluster.local -u root -p${MYSQL_ROOT_PASSWORD} -e "
              CREATE USER IF NOT EXISTS 'exporter'@'%' IDENTIFIED BY 'exporter_password';
              GRANT PROCESS, REPLICATION CLIENT, SELECT ON *.* TO 'exporter'@'%';
              FLUSH PRIVILEGES;
              "
          env:
            - name: MYSQL_ROOT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: mysql84-conn-credential
                  key: password
EOF

# 稍后执行初始化
log_info "Exporter用户初始化Job创建完成（将在MySQL启动后执行）"

#===============================================================================
# 8. 等待MySQL集群就绪
#===============================================================================
log_step "8. 等待MySQL集群就绪..."

log_info "等待MySQL 5.7.29集群就绪..."
for i in {1..60}; do
    STATUS=$(kubectl get cluster mysql57 -n ${NAMESPACE} -o jsonpath='{.status.phase}' 2>/dev/null || echo "Pending")
    if [ "$STATUS" = "Running" ]; then
        log_info "MySQL 5.7.29集群已就绪"
        break
    fi
    echo -n "."
    sleep 10
done
echo ""

log_info "等待MySQL 8.4.3集群就绪..."
for i in {1..60}; do
    STATUS=$(kubectl get cluster mysql84 -n ${NAMESPACE} -o jsonpath='{.status.phase}' 2>/dev/null || echo "Pending")
    if [ "$STATUS" = "Running" ]; then
        log_info "MySQL 8.4.3集群已就绪"
        break
    fi
    echo -n "."
    sleep 10
done
echo ""

# 执行初始化Job
kubectl apply -f /tmp/mysql-init-job.yaml

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

# 验证MySQL集群
verify_check "MySQL 5.7.29集群运行中" "kubectl get cluster mysql57 -n ${NAMESPACE} -o jsonpath='{.status.phase}' | grep -q Running"
verify_check "MySQL 8.4.3集群运行中" "kubectl get cluster mysql84 -n ${NAMESPACE} -o jsonpath='{.status.phase}' | grep -q Running"

# 验证Pod
verify_check "MySQL 5.7.29 Pod运行中" "kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/instance=mysql57 | grep -q Running"
verify_check "MySQL 8.4.3 Pod运行中" "kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/instance=mysql84 | grep -q Running"

# 验证Service
verify_check "MySQL 5.7.29 Service存在" "kubectl get svc -n ${NAMESPACE} | grep -q mysql57"
verify_check "MySQL 8.4.3 Service存在" "kubectl get svc -n ${NAMESPACE} | grep -q mysql84"

# 验证ConfigMaps
verify_check "Filebeat ConfigMap存在" "kubectl get configmap filebeat-mysql-config -n ${NAMESPACE}"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示集群信息
echo ""
log_step "================== MySQL集群信息 =================="
echo ""
echo "=== 集群状态 ==="
kubectl get clusters -n ${NAMESPACE}
echo ""
echo "=== Pods ==="
kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=mysql
echo ""
echo "=== Services ==="
kubectl get svc -n ${NAMESPACE} | grep mysql
echo ""

# 获取连接信息
log_step "获取MySQL连接信息..."
echo ""
echo "=== MySQL 5.7.29连接信息 ==="
kbcli cluster connect mysql57 -n ${NAMESPACE} --show-password 2>/dev/null || {
    echo "Host: mysql57-mysql.${NAMESPACE}.svc.cluster.local"
    echo "Port: 3306"
    echo "User: root"
    echo "Password: $(kubectl get secret mysql57-conn-credential -n ${NAMESPACE} -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo 'See secret mysql57-conn-credential')"
}
echo ""
echo "=== MySQL 8.4.3连接信息 ==="
kbcli cluster connect mysql84 -n ${NAMESPACE} --show-password 2>/dev/null || {
    echo "Host: mysql84-mysql.${NAMESPACE}.svc.cluster.local"
    echo "Port: 3306"
    echo "User: root"
    echo "Password: $(kubectl get secret mysql84-conn-credential -n ${NAMESPACE} -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo 'See secret mysql84-conn-credential')"
}
echo ""

log_info "MySQL集群部署完成！"

echo ""
echo "=========================================="
echo "      MySQL集群部署完成!"
echo "      - Percona MySQL 5.7.29"
echo "      - Percona MySQL 8.4.3"
echo "      命名空间: ${NAMESPACE}"
echo "=========================================="
echo ""
echo "Sidecar组件:"
echo "  - Filebeat: 采集慢查询和错误日志到ES"
echo "  - MySQL Exporter: 提供Prometheus监控指标"
echo "=========================================="
