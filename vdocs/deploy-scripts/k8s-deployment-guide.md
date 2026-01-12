# Kubernetes 完整部署指南

## 📋 部署概览

本指南提供在 **Ubuntu 24.04.2 LTS** 环境下部署完整 Kubernetes 环境的一键脚本，包括两套 K8S 集群、私有镜像仓库以及数据库服务。

### 架构图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           Ubuntu 24.04.2 LTS Mini主机                            │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  ┌────────────────────────────┐     ┌────────────────────────────┐              │
│  │     K8S Cluster 1          │     │     K8S Cluster 2          │              │
│  │  ┌──────────────────────┐  │     │  ┌──────────────────────┐  │              │
│  │  │ CRI: containerd      │  │     │  │ CRI: containerd      │  │              │
│  │  │ CNI: Calico          │  │     │  │ CNI: Calico          │  │              │
│  │  │ OCI: runc            │  │     │  │ OCI: runc            │  │              │
│  │  │ Pod CIDR: 10.244.0.0 │  │     │  │ Pod CIDR: 10.245.0.0 │  │              │
│  │  └──────────────────────┘  │     │  └──────────────────────┘  │              │
│  └────────────────────────────┘     └────────────────────────────┘              │
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                        Harbor 私有镜像仓库                               │    │
│  │                        https://registry.local                            │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                           KubeBlocks                                     │    │
│  │  ┌───────────────┐ ┌───────────────┐ ┌────────────┐ ┌────────────────┐  │    │
│  │  │ MySQL 5.7.29  │ │ MySQL 8.4.3   │ │ ES 8.8.2   │ │ ClickHouse     │  │    │
│  │  │ + Filebeat    │ │ + Filebeat    │ │ + Kibana   │ │ 24.3.2         │  │    │
│  │  │ + Exporter    │ │ + Exporter    │ │            │ │ + Exporter     │  │    │
│  │  └───────────────┘ └───────────────┘ └────────────┘ └────────────────┘  │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐    │
│  │                         监控系统                                         │    │
│  │  ┌────────────────┐  ┌────────────────┐  ┌────────────────────────────┐ │    │
│  │  │  Prometheus    │  │  Alertmanager  │  │  Grafana                   │ │    │
│  │  │  :30090        │  │  :30093        │  │  :30030                    │ │    │
│  │  └────────────────┘  └────────────────┘  └────────────────────────────┘ │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 组件版本

| **组件** | **版本** | **说明** |
|:---------|:---------|:---------|
| **Kubernetes** | v1.32.0 | 容器编排平台 |
| **containerd** | v1.7.24 | 容器运行时 (CRI) |
| **runc** | v1.2.3 | OCI 运行时 |
| **Calico** | v3.29.1 | 网络插件 (CNI) |
| **Harbor** | v2.11.2 | 私有镜像仓库 |
| **KubeBlocks** | v0.9.2 | 数据库管理平台 |
| **MySQL** | 5.7.29 / 8.4.3 | Percona MySQL |
| **Elasticsearch** | 8.8.2 | 日志存储 |
| **ClickHouse** | 24.3.2 | OLAP数据库 |
| **Prometheus** | v25.27.0 | 监控系统 |
| **Grafana** | latest | 可视化平台 |

---

## 🚀 快速开始

### 前置要求

- **操作系统**: Ubuntu 24.04.2 LTS
- **硬件要求**:
  - CPU: 至少 4 核
  - 内存: 至少 16GB (推荐 32GB)
  - 磁盘: 至少 200GB SSD
- **网络**: 可访问互联网

### 部署顺序

```bash
# 1. 克隆脚本到服务器
cd /root
git clone <repository> deploy-scripts
cd deploy-scripts/scripts

# 2. 添加执行权限
chmod +x *.sh

# 3. 按顺序执行脚本
./01-prepare-system.sh 1      # 集群1系统准备
./02-install-containerd.sh    # 安装containerd
./03-install-kubernetes.sh 1  # 安装K8S集群1
./04-install-calico.sh 1      # 安装Calico CNI
./05-setup-registry.sh        # 部署Harbor镜像仓库
./06-install-kubeblocks.sh    # 安装KubeBlocks
./08-deploy-elasticsearch.sh  # 部署ES（先部署，MySQL日志需要）
./07-deploy-mysql.sh          # 部署MySQL
./09-deploy-clickhouse.sh     # 部署ClickHouse
./10-deploy-monitoring.sh     # 部署监控系统
```

---

## 📜 脚本详解

### 1️⃣ 01-prepare-system.sh - 系统准备

**功能**:
- 更新系统软件包
- 安装必要工具
- 关闭 Swap
- 配置内核参数
- 配置时间同步
- 设置主机名

**用法**:
```bash
sudo ./01-prepare-system.sh [集群编号]
# 示例
sudo ./01-prepare-system.sh 1  # 集群1
sudo ./01-prepare-system.sh 2  # 集群2
```

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ Swap已关闭 | `free -h` 显示 Swap 为 0 |
| ✓ overlay模块已加载 | `lsmod \| grep overlay` |
| ✓ br_netfilter已加载 | `lsmod \| grep br_netfilter` |
| ✓ IPv4转发已启用 | `sysctl net.ipv4.ip_forward` |
| ✓ chrony服务运行 | `systemctl status chrony` |

---

### 2️⃣ 02-install-containerd.sh - 安装 containerd

**功能**:
- 安装 runc (OCI runtime)
- 安装 CNI 插件
- 安装 containerd
- 配置 SystemdCgroup
- 安装 crictl 和 nerdctl

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ runc已安装 | `runc --version` |
| ✓ containerd已安装 | `containerd --version` |
| ✓ containerd服务运行中 | `systemctl status containerd` |
| ✓ crictl可以连接 | `crictl info` |
| ✓ SystemdCgroup已启用 | 配置文件验证 |

---

### 3️⃣ 03-install-kubernetes.sh - 安装 Kubernetes

**功能**:
- 添加 Kubernetes APT 仓库
- 安装 kubeadm, kubelet, kubectl
- 初始化 Master 节点
- 配置 kubectl
- 保存 Worker 节点加入命令

**用法**:
```bash
sudo ./03-install-kubernetes.sh [集群编号]
```

**网络配置**:
| 集群 | Pod CIDR | Service CIDR |
|:-----|:---------|:-------------|
| 集群1 | 10.244.0.0/16 | 10.96.0.0/12 |
| 集群2 | 10.245.0.0/16 | 10.112.0.0/12 |

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ kubeadm已安装 | `kubeadm version` |
| ✓ kubectl可以连接 | `kubectl cluster-info` |
| ✓ 核心组件运行中 | kube-apiserver, etcd 等 |

---

### 4️⃣ 04-install-calico.sh - 安装 Calico CNI

**功能**:
- 安装 Tigera Operator
- 部署 Calico
- 配置 Pod 网络 CIDR
- 安装 calicoctl

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ calico-node运行中 | DaemonSet 就绪 |
| ✓ 节点状态为Ready | `kubectl get nodes` |
| ✓ IP Pool已创建 | 网络配置验证 |
| ✓ DNS解析正常 | Pod 网络测试 |

---

### 5️⃣ 05-setup-registry.sh - 部署 Harbor 镜像仓库

**功能**:
- 安装 Docker
- 创建自签名 SSL 证书
- 部署 Harbor (带 Trivy 扫描)
- 配置 containerd 信任仓库
- 创建 Kubernetes 镜像拉取 Secret

**访问信息**:
| 项目 | 值 |
|:-----|:---|
| URL | https://registry.local |
| 用户名 | admin |
| 密码 | Admin@123456 |

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ Harbor容器运行中 | Docker ps 验证 |
| ✓ Harbor API可访问 | 健康检查 |
| ✓ Docker可以登录 | `docker login` |
| ✓ 镜像推送成功 | 测试推送 |

---

### 6️⃣ 06-install-kubeblocks.sh - 安装 KubeBlocks

**功能**:
- 安装 kbcli 命令行工具
- 安装 Helm
- 部署 KubeBlocks Operator
- 启用数据库 Addons (MySQL, ES, ClickHouse)
- 配置本地存储

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ kbcli已安装 | `kbcli version` |
| ✓ KubeBlocks Operator运行中 | Pod 状态检查 |
| ✓ MySQL addon已启用 | Addon 列表验证 |
| ✓ CRDs已安装 | Cluster, ClusterDefinition 等 |

---

### 7️⃣ 07-deploy-mysql.sh - 部署 MySQL

**功能**:
- 部署 Percona MySQL 5.7.29
- 部署 Percona MySQL 8.4.3
- 配置 Filebeat Sidecar (采集日志到 ES)
- 部署 MySQL Exporter
- 创建 ServiceMonitor

**Sidecar 配置**:

```
┌─────────────────────────────────────────────────────────────────┐
│                        MySQL Pod                                 │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  │
│  │ MySQL Container │  │ Filebeat        │  │ MySQL Exporter  │  │
│  │                 │  │ Sidecar         │  │ Sidecar         │  │
│  │ - 慢查询日志     │──│ - 采集慢查询     │  │ - 暴露 :9104    │  │
│  │ - 错误日志      │  │ - 采集错误日志   │  │ - Prometheus    │  │
│  │                 │  │ - 发送到 ES      │  │   metrics       │  │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘  │
│           │                    │                    │            │
│           ▼                    ▼                    ▼            │
│     /var/lib/mysql      Elasticsearch         Prometheus        │
└─────────────────────────────────────────────────────────────────┘
```

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ MySQL 5.7.29集群运行中 | Cluster 状态 |
| ✓ MySQL 8.4.3集群运行中 | Cluster 状态 |
| ✓ Filebeat ConfigMap存在 | 日志配置 |
| ✓ Service存在 | 服务发现 |

---

### 8️⃣ 08-deploy-elasticsearch.sh - 部署 Elasticsearch

**功能**:
- 部署 Elasticsearch 单节点
- 部署 Kibana
- 创建 MySQL 日志索引模板
- 配置 ILM 策略

**访问信息**:
| 服务 | 地址 | 端口 |
|:-----|:-----|:-----|
| Elasticsearch | ClusterIP | 9200 |
| Kibana | NodePort | 30561 |

**验证项目**:
| 验证项 | 说明 |
|:-------|:-----|
| ✓ ES Pod运行中 | Pod 状态检查 |
| ✓ Kibana运行中 | Deployment 状态 |
| ✓ 索引模板已创建 | ConfigMap 验证 |
| ✓ 集群健康 | `/_cluster/health` |

---

### 9️⃣ 09-deploy-clickhouse.sh - 部署 ClickHouse

**功能**:
- 部署 ClickHouse 单节点
- 配置用户和权限
- 部署 ClickHouse Exporter
- 初始化监控数据库

**访问信息**:
| 协议 | 内部端口 | NodePort |
|:-----|:---------|:---------|
| HTTP | 8123 | 30123 |
| Native | 9000 | 30900 |

**用户**:
| 用户名 | 密码 |
|:-------|:-----|
| admin | admin123 |

---

### 🔟 10-deploy-monitoring.sh - 部署监控系统

**功能**:
- 部署 kube-prometheus-stack
- 配置 Prometheus 抓取规则
- 创建告警规则
- 配置 Grafana 数据源和 Dashboard
- 部署 Pod Metrics Exporter

**访问信息**:
| 服务 | NodePort | 凭据 |
|:-----|:---------|:-----|
| Prometheus | 30090 | - |
| Alertmanager | 30093 | - |
| Grafana | 30030 | admin / admin123 |

**告警规则**:

| 告警名称 | 条件 | 严重程度 |
|:---------|:-----|:---------|
| MySQLDown | MySQL实例不可用 | Critical |
| MySQLHighConnections | 连接数 > 100 | Warning |
| MySQLSlowQueries | 慢查询率 > 0.1/s | Warning |
| MySQLReplicationLag | 复制延迟 > 30s | Warning |
| ElasticsearchClusterRed | 集群状态为红色 | Critical |
| ElasticsearchHeapUsageHigh | 堆内存使用 > 90% | Warning |
| ClickHouseDown | ClickHouse不可用 | Critical |
| ClickHouseHighMemoryUsage | 内存使用 > 8GB | Warning |

---

## 📊 数据流图

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                              数据流向                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                               │
│  ┌─────────────────┐                                                         │
│  │  MySQL 5.7/8.4  │                                                         │
│  │                 │                                                         │
│  │  ┌───────────┐  │     ┌──────────────┐     ┌─────────────────────────┐   │
│  │  │ 慢查询日志 │──────▶│  Filebeat    │────▶│  Elasticsearch          │   │
│  │  │ 错误日志   │  │     │  Sidecar     │     │  mysql-logs-* 索引      │   │
│  │  └───────────┘  │     └──────────────┘     └──────────┬──────────────┘   │
│  │                 │                                      │                  │
│  │  ┌───────────┐  │     ┌──────────────┐                ▼                  │
│  │  │ MySQL     │──────▶│  Prometheus  │     ┌─────────────────────────┐   │
│  │  │ Exporter  │  │     │  :9104       │     │  Kibana                 │   │
│  │  │ :9104     │  │     └──────┬───────┘     │  日志可视化             │   │
│  │  └───────────┘  │            │             └─────────────────────────┘   │
│  └─────────────────┘            │                                            │
│                                 ▼                                            │
│  ┌─────────────────┐     ┌──────────────────────────────────────────────┐   │
│  │  ClickHouse     │     │              Prometheus                       │   │
│  │                 │     │                                               │   │
│  │  ┌───────────┐  │     │  ┌─────────────────────────────────────────┐ │   │
│  │  │ Exporter  │──────▶│  │ 抓取目标:                                │ │   │
│  │  │ :9116     │  │     │  │ - mysql-exporter                        │ │   │
│  │  └───────────┘  │     │  │ - clickhouse-exporter                   │ │   │
│  └─────────────────┘     │  │ - node-exporter                         │ │   │
│                          │  │ - kube-state-metrics                    │ │   │
│  ┌─────────────────┐     │  │ - kubernetes-pods                       │ │   │
│  │  Node Exporter  │     │  └─────────────────────────────────────────┘ │   │
│  │  Pod Metrics    │─────│                                               │   │
│  └─────────────────┘     └──────────────────┬───────────────────────────┘   │
│                                              │                               │
│                                              ▼                               │
│                          ┌──────────────────────────────────────────────┐   │
│                          │              Grafana                          │   │
│                          │  ┌────────────────────────────────────────┐  │   │
│                          │  │ 数据源:                                 │  │   │
│                          │  │ - Prometheus (默认)                    │  │   │
│                          │  │ - Elasticsearch                        │  │   │
│                          │  │ - ClickHouse                           │  │   │
│                          │  └────────────────────────────────────────┘  │   │
│                          │  ┌────────────────────────────────────────┐  │   │
│                          │  │ Dashboard:                              │  │   │
│                          │  │ - MySQL Overview                        │  │   │
│                          │  │ - Kubernetes / Compute Resources        │  │   │
│                          │  │ - Node Exporter Full                    │  │   │
│                          │  └────────────────────────────────────────┘  │   │
│                          └──────────────────────────────────────────────┘   │
│                                                                               │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 🔧 常用命令

### Kubernetes 集群管理

```bash
# 查看节点状态
kubectl get nodes -o wide

# 查看所有Pod
kubectl get pods -A

# 查看集群信息
kubectl cluster-info

# 查看组件状态
kubectl get componentstatuses
```

### KubeBlocks 管理

```bash
# 查看所有数据库集群
kbcli cluster list -A

# 查看集群详情
kbcli cluster describe mysql57 -n database

# 连接MySQL
kbcli cluster connect mysql57 -n database

# 查看集群日志
kbcli cluster logs mysql57 -n database
```

### Harbor 管理

```bash
# Docker登录
docker login registry.local -u admin -p Admin@123456

# 推送镜像
docker tag myimage:tag registry.local/library/myimage:tag
docker push registry.local/library/myimage:tag
```

### 监控管理

```bash
# 查看Prometheus targets
curl http://localhost:30090/api/v1/targets

# 查看告警规则
kubectl get prometheusrule -n monitoring

# 查看告警
curl http://localhost:30093/api/v1/alerts
```

---

## ❗ 常见问题

### Q1: 节点状态一直是 NotReady？

**原因**: CNI 插件未正确安装

**解决**:
```bash
# 检查Calico状态
kubectl get pods -n calico-system
kubectl describe pods -n calico-system

# 重新安装Calico
./04-install-calico.sh
```

### Q2: Pod 一直处于 Pending 状态？

**原因**: 存储类未配置或资源不足

**解决**:
```bash
# 检查存储类
kubectl get storageclass

# 检查PVC状态
kubectl get pvc -A

# 安装local-path-provisioner
kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/master/deploy/local-path-storage.yaml
```

### Q3: Harbor 无法访问？

**原因**: 证书问题或服务未启动

**解决**:
```bash
# 检查Harbor状态
cd /opt/harbor
docker compose ps

# 重启Harbor
docker compose down
docker compose up -d

# 检查证书
openssl s_client -connect registry.local:443
```

### Q4: MySQL Exporter 无法采集指标？

**原因**: exporter 用户未创建

**解决**:
```bash
# 手动创建exporter用户
kubectl exec -it mysql57-mysql-0 -n database -- mysql -u root -p

CREATE USER 'exporter'@'%' IDENTIFIED BY 'exporter_password';
GRANT PROCESS, REPLICATION CLIENT, SELECT ON *.* TO 'exporter'@'%';
FLUSH PRIVILEGES;
```

---

## 📁 文件结构

```
deploy-scripts/
├── k8s-deployment-guide.md          # 本文档
├── scripts/
│   ├── 01-prepare-system.sh         # 系统准备
│   ├── 02-install-containerd.sh     # 安装containerd
│   ├── 03-install-kubernetes.sh     # 安装Kubernetes
│   ├── 04-install-calico.sh         # 安装Calico CNI
│   ├── 05-setup-registry.sh         # 部署Harbor
│   ├── 06-install-kubeblocks.sh     # 安装KubeBlocks
│   ├── 07-deploy-mysql.sh           # 部署MySQL
│   ├── 08-deploy-elasticsearch.sh   # 部署Elasticsearch
│   ├── 09-deploy-clickhouse.sh      # 部署ClickHouse
│   └── 10-deploy-monitoring.sh      # 部署监控系统
├── manifests/                        # K8S资源清单
└── configs/                          # 配置文件
```

---

## 📞 服务端口汇总

| 服务 | 类型 | 端口 | 备注 |
|:-----|:-----|:-----|:-----|
| **Kubernetes API** | ClusterIP | 6443 | K8S API Server |
| **Harbor** | HTTPS | 443 | 镜像仓库 |
| **MySQL 5.7** | ClusterIP | 3306 | 数据库 |
| **MySQL 8.4** | ClusterIP | 3306 | 数据库 |
| **Elasticsearch** | ClusterIP | 9200 | 搜索引擎 |
| **Kibana** | NodePort | 30561 | 日志可视化 |
| **ClickHouse HTTP** | NodePort | 30123 | OLAP数据库 |
| **ClickHouse Native** | NodePort | 30900 | OLAP数据库 |
| **Prometheus** | NodePort | 30090 | 监控 |
| **Alertmanager** | NodePort | 30093 | 告警 |
| **Grafana** | NodePort | 30030 | 可视化 |

---

## ✅ 部署检查清单

- [ ] 系统准备完成 (Swap关闭, 内核参数配置)
- [ ] containerd 安装并运行
- [ ] Kubernetes Master 初始化成功
- [ ] Calico CNI 安装，节点状态 Ready
- [ ] Harbor 部署，可以推送/拉取镜像
- [ ] KubeBlocks 安装，Addons 启用
- [ ] Elasticsearch 部署并运行
- [ ] MySQL 5.7.29 部署并运行
- [ ] MySQL 8.4.3 部署并运行
- [ ] Filebeat Sidecar 配置正确
- [ ] MySQL Exporter 可以采集指标
- [ ] ClickHouse 部署并运行
- [ ] Prometheus 部署并抓取指标
- [ ] Grafana 可以访问，数据源配置正确
- [ ] 告警规则配置完成

---

## 📝 维护建议

1. **定期备份**:
   - etcd 数据备份
   - MySQL 数据备份
   - Harbor 数据备份

2. **监控告警**:
   - 配置告警通知 (邮件/钉钉/企业微信)
   - 定期检查告警规则

3. **日志管理**:
   - 配置 Elasticsearch ILM 策略
   - 定期清理过期日志

4. **安全加固**:
   - 更新组件版本
   - 配置 RBAC
   - 启用网络策略

---

**文档版本**: v1.0  
**更新日期**: 2026-01-11  
**作者**: Auto-generated
