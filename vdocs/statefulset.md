# Kubernetes StatefulSet 架构与原理深度解读

## 目录

1. [概述](#概述)
2. [StatefulSet 核心概念](#statefulset-核心概念)
3. [StatefulSet 整体架构](#statefulset-整体架构)
4. [StatefulSet 控制器实现原理](#statefulset-控制器实现原理)
5. [有序部署与终止机制](#有序部署与终止机制)
6. [持久化存储管理](#持久化存储管理)
7. [网络标识与服务发现](#网络标识与服务发现)
8. [滚动更新策略](#滚动更新策略)
9. [Pod 管理策略](#pod-管理策略)
10. [PVC 自动删除策略](#pvc-自动删除策略)
11. [使用场景与最佳实践](#使用场景与最佳实践)
12. [总结](#总结)

---

## 概述

StatefulSet 是 Kubernetes 中用于管理有状态应用的工作负载控制器。与 Deployment 管理无状态应用不同，StatefulSet 为每个 Pod 提供稳定的网络标识、有序的部署和终止序列，以及持久化存储。本文档基于 Kubernetes 源码深入解读 StatefulSet 的架构设计、工作原理和实现机制。

### 核心特性

- **稳定的网络标识**：每个 Pod 拥有唯一且持久的 DNS 名称
- **有序部署和扩缩容**：Pod 按照固定顺序创建和删除
- **持久化存储**：每个 Pod 绑定专用的持久卷声明（PVC）
- **有序滚动更新**：按顺序更新 Pod，确保服务连续性
- **头节点服务**：通过 Headless Service 提供稳定的网络访问

---

## StatefulSet 核心概念

### 1. 基本概念关系

- **StatefulSet**：管理有状态应用的控制器
- **Pod Template**：定义 Pod 规格的模板
- **Volume Claim Templates**：定义存储需求的 PVC 模板
- **Headless Service**：提供网络标识的无头服务
- **Ordinal Index**：Pod 的序号索引（0, 1, 2, ...）

### 2. 核心组件架构图

```mermaid
graph TB
    subgraph "**StatefulSet 核心架构**"
        
        subgraph "**控制平面**"
            
            API[**API Server**<br/>• StatefulSet 资源管理<br/>• Pod 状态协调<br/>• PVC 生命周期管理]
            
            subgraph "**Controller Manager**"
                
                STS_CTRL[**StatefulSet Controller**<br/>• 有序创建/删除逻辑<br/>• Pod 标识管理<br/>• 存储绑定协调]
                
                PVC_CTRL[**PV Controller**<br/>• 卷动态供应<br/>• PV/PVC 绑定<br/>• 存储调度]
            end
            
            ETCD[**etcd**<br/>• StatefulSet 状态存储<br/>• Pod 序号记录<br/>• 修订版本管理]
        end
        
        subgraph "**数据平面**"
            
            HEADLESS_SVC[**Headless Service**<br/>• 稳定 DNS 解析<br/>• Pod 网络标识<br/>• 服务发现]
            
            subgraph "**工作节点**"
                
                KUBELET[**Kubelet**<br/>• Pod 生命周期管理<br/>• 卷挂载管理<br/>• 容器运行时交互]
                
                subgraph "**Pod 集合**"
                    
                    POD0[**app-0**<br/>• 序号: 0<br/>• DNS: app-0.service<br/>• PVC: data-app-0]
                    POD1[**app-1**<br/>• 序号: 1<br/>• DNS: app-1.service<br/>• PVC: data-app-1]
                    POD2[**app-2**<br/>• 序号: 2<br/>• DNS: app-2.service<br/>• PVC: data-app-2]
                end
                
                subgraph "**存储层**"
                    
                    PVC0[**PVC: data-app-0**]
                    PVC1[**PVC: data-app-1**]
                    PVC2[**PVC: data-app-2**]
                    
                    PV0[**PV-0**]
                    PV1[**PV-1**]
                    PV2[**PV-2**]
                end
            end
        end
    end
    
    API --> STS_CTRL
    STS_CTRL --> ETCD
    STS_CTRL --> KUBELET
    PVC_CTRL --> API
    
    HEADLESS_SVC --> POD0
    HEADLESS_SVC --> POD1
    HEADLESS_SVC --> POD2
    
    POD0 --> PVC0
    POD1 --> PVC1
    POD2 --> PVC2
    
    PVC0 --> PV0
    PVC1 --> PV1
    PVC2 --> PV2
    
    KUBELET --> POD0
    KUBELET --> POD1
    KUBELET --> POD2
```

---

## StatefulSet 整体架构

### 1. 系统层次架构

```mermaid
graph TB
    subgraph "**StatefulSet 系统层次架构**"
        
        subgraph "**用户接口层**"
            
            KUBECTL[**kubectl**<br/>• StatefulSet 创建<br/>• 扩缩容操作<br/>• 状态查询]
            
            YAML[**YAML 配置**<br/>• StatefulSet 规格<br/>• PVC 模板<br/>• 更新策略]
        end
        
        subgraph "**控制器层**"
            
            STS_CONTROLLER[**StatefulSet Controller**<br/>• 期望状态计算<br/>• Pod 有序管理<br/>• 存储协调]
            
            subgraph "**核心组件**"
                
                POD_CONTROL[**Pod Control**<br/>• Pod 创建/删除<br/>• 标识管理<br/>• 状态同步]
                
                STATUS_UPDATER[**Status Updater**<br/>• StatefulSet 状态<br/>• 就绪副本数<br/>• 当前修订版本]
                
                REVISION_CTRL[**Revision Control**<br/>• 修订版本管理<br/>• 历史记录清理<br/>• 回滚支持]
            end
        end
        
        subgraph "**调度与存储层**"
            
            SCHEDULER[**Scheduler**<br/>• Pod 调度决策<br/>• 亲和性处理<br/>• 资源分配]
            
            VOLUME_BINDER[**Volume Binder**<br/>• PVC 绑定<br/>• 存储调度<br/>• 拓扑感知]
        end
        
        subgraph "**执行层**"
            
            NODE_AGENT[**Node Agent**<br/>• Kubelet<br/>• Container Runtime<br/>• Volume Plugin]
            
            STORAGE_BACKEND[**Storage Backend**<br/>• 持久卷<br/>• 存储驱动<br/>• 数据持久化]
        end
    end
    
    KUBECTL --> STS_CONTROLLER
    YAML --> STS_CONTROLLER
    
    STS_CONTROLLER --> POD_CONTROL
    STS_CONTROLLER --> STATUS_UPDATER
    STS_CONTROLLER --> REVISION_CTRL
    
    POD_CONTROL --> SCHEDULER
    POD_CONTROL --> VOLUME_BINDER
    
    SCHEDULER --> NODE_AGENT
    VOLUME_BINDER --> STORAGE_BACKEND
    NODE_AGENT --> STORAGE_BACKEND
```

---

## StatefulSet 控制器实现原理

### 1. 控制器同步流程

基于源码 `pkg/controller/statefulset/stateful_set_control.go`，StatefulSet 控制器的核心同步逻辑：

```go
// UpdateStatefulSet 执行 StatefulSet 的核心逻辑循环
func (ssc *defaultStatefulSetControl) UpdateStatefulSet(ctx context.Context, set *apps.StatefulSet, pods []*v1.Pod) (*apps.StatefulSetStatus, error) {
    // 深拷贝 StatefulSet，避免在创建新修订版本时发生变更错误
    set = set.DeepCopy- 

    // 列出所有修订版本并排序
    revisions, err := ssc.ListRevisions- set
    if err != nil {
        return nil, err
    }
    history.SortControllerRevisions- revisions

    // 执行更新操作
    currentRevision, updateRevision, status, err := ssc.performUpdate(ctx, set, pods, revisions)
    if err != nil {
        return nil, err
    }

    // 维护修订历史限制
    return status, ssc.truncateHistory(set, pods, revisions, currentRevision, updateRevision)
}
```

### 2. Pod 处理逻辑

```go
func (ssc *defaultStatefulSetControl) processReplica(
    ctx context.Context,
    set *apps.StatefulSet,
    currentRevision *apps.ControllerRevision,
    updateRevision *apps.ControllerRevision,
    currentSet *apps.StatefulSet,
    updateSet *apps.StatefulSet,
    monotonic bool,
    replicas []*v1.Pod,
    i int) (bool, error) {
    
    // 删除并重新创建失败的 Pod
    if isFailed(replicas[i]) {
        ssc.recorder.Eventf(set, v1.EventTypeWarning, "RecreatingFailedPod",
            "StatefulSet %s/%s is recreating failed Pod %s",
            set.Namespace, set.Name, replicas[i].Name)
        if err := ssc.podControl.DeleteStatefulPod(set, replicas[i]); err != nil {
            return true, err
        }
        // 创建新版本的 Pod
        replicaOrd := i + getStartOrdinal- set
        replicas[i] = newVersionedStatefulSetPod(
            currentSet, updateSet,
            currentRevision.Name, updateRevision.Name, replicaOrd)
    }
    
    // 如果 Pod 尚未创建，则创建 Pod
    if !isCreated(replicas[i]) {
        if err := ssc.podControl.CreateStatefulPod(ctx, set, replicas[i]); err != nil {
            return true, err
        }
        if monotonic {
            // 如果不允许突发模式，立即返回
            return true, nil
        }
    }
    
    return false, nil
}
```

### 3. 控制器状态机图

```mermaid
stateDiagram-v2
    [*] --> **监听事件**
    
    **监听事件** --> **获取StatefulSet** : StatefulSet/Pod 事件
    **获取StatefulSet** --> **列出Pod** : StatefulSet 存在
    **获取StatefulSet** --> **清理期望** : StatefulSet 已删除
    
    **列出Pod** --> **计算状态** : Pod 列表获取成功
    **计算状态** --> **版本管理** : 状态计算完成
    
    **版本管理** --> **有序处理** : 需要更新
    **版本管理** --> **更新状态** : 无需操作
    
    **有序处理** --> **创建Pod** : 需要扩容
    **有序处理** --> **删除Pod** : 需要缩容
    **有序处理** --> **更新Pod** : 需要更新
    
    **创建Pod** --> **等待就绪** : Pod 创建中
    **删除Pod** --> **等待终止** : Pod 删除中
    **更新Pod** --> **等待更新** : Pod 更新中
    
    **等待就绪** --> **更新状态** : Pod 就绪
    **等待终止** --> **更新状态** : Pod 终止
    **等待更新** --> **更新状态** : Pod 更新完成
    
    **等待就绪** --> **重新入队** : Pod 未就绪
    **等待终止** --> **重新入队** : Pod 未终止
    **等待更新** --> **重新入队** : Pod 更新中
    
    **更新状态** --> **清理历史** : 状态更新完成
    **清理历史** --> [*] : 同步完成
    
    **清理期望** --> [*] : 清理完成
    **重新入队** --> [*] : 稍后重试
    
    
```

---

## 有序部署与终止机制

### 1. 有序部署流程图

```mermaid
sequenceDiagram
    participant CTRL as **StatefulSet Controller**
    participant API as **API Server**
    participant SCHED as **Scheduler**
    participant KUBELET as **Kubelet**
    participant STORAGE as **Storage System**
    
    Note over CTRL,STORAGE: **StatefulSet 有序部署流程**
    
    CTRL->>CTRL: **1. 计算需要创建的Pod**
    Note right of CTRL: **• 目标副本数: 3**<br/>**• 当前副本数: 0**<br/>**• 需要创建: app-0**
    
    CTRL->>API: **2. 创建 PVC (data-app-0)**
    API->>STORAGE: **3. 动态供应 PV**
    STORAGE->>API: **4. PV 创建完成**
    API->>CTRL: **5. PVC 绑定成功**
    
    CTRL->>API: **6. 创建 Pod (app-0)**
    API->>SCHED: **7. Pod 调度请求**
    SCHED->>API: **8. Pod 调度到节点**
    API->>KUBELET: **9. Pod 创建指令**
    
    KUBELET->>KUBELET: **10. 挂载存储卷**
    KUBELET->>KUBELET: **11. 启动容器**
    KUBELET->>API: **12. Pod 状态更新**
    
    Note over API: **Pod app-0 变为 Running & Ready**
    
    API->>CTRL: **13. Pod 就绪事件**
    CTRL->>CTRL: **14. 验证 Pod 健康状态**
    
    Note over CTRL: **单调模式：只有当 app-0 完全就绪后，才创建 app-1**
    
    CTRL->>API: **15. 创建 PVC (data-app-1)**
    Note right of CTRL: **重复步骤 2-12**
    
    API->>CTRL: **16. app-1 就绪事件**
    CTRL->>API: **17. 创建 PVC (data-app-2)**
    Note right of CTRL: **重复步骤 2-12**
    
    API->>CTRL: **18. app-2 就绪事件**
    CTRL->>API: **19. 更新 StatefulSet 状态**
    Note right of CTRL: **• ReadyReplicas: 3**<br/>**• Replicas: 3**<br/>**• CurrentReplicas: 3**
```

### 2. 有序终止流程

```mermaid
sequenceDiagram
    participant CTRL as **StatefulSet Controller**
    participant API as **API Server**
    participant KUBELET as **Kubelet**
    
    Note over CTRL,KUBELET: **StatefulSet 有序终止流程（缩容：3→1）**
    
    CTRL->>CTRL: **1. 计算需要删除的Pod**
    Note right of CTRL: **• 目标副本数: 1**<br/>**• 当前副本数: 3**<br/>**• 删除顺序: app-2 → app-1**
    
    CTRL->>API: **2. 删除 Pod (app-2)**
    API->>KUBELET: **3. Pod 终止指令**
    
    KUBELET->>KUBELET: **4. 发送 SIGTERM**
    KUBELET->>KUBELET: **5. 等待优雅关闭**
    Note right of KUBELET: **terminationGracePeriodSeconds**
    
    KUBELET->>KUBELET: **6. 卸载存储卷**
    KUBELET->>API: **7. Pod 删除确认**
    
    Note over API: **Pod app-2 完全删除**
    
    API->>CTRL: **8. Pod 删除事件**
    CTRL->>CTRL: **9. 验证删除完成**
    
    Note over CTRL: **单调模式：只有当 app-2 完全删除后，才删除 app-1**
    
    CTRL->>API: **10. 删除 Pod (app-1)**
    Note right of CTRL: **重复步骤 3-7**
    
    API->>CTRL: **11. app-1 删除事件**
    CTRL->>API: **12. 更新 StatefulSet 状态**
    Note right of CTRL: **• ReadyReplicas: 1**<br/>**• Replicas: 1**<br/>**• CurrentReplicas: 1**
```

---

## 持久化存储管理

### 1. PVC 生命周期管理

基于源码 `pkg/controller/statefulset/stateful_pod_control.go`：

```go
// CreateStatefulPod 创建 StatefulSet 的 Pod
func (spc *StatefulPodControl) CreateStatefulPod(ctx context.Context, set *apps.StatefulSet, pod *v1.Pod) error {
    // 在创建 Pod 之前先创建 Pod 的 PVC
    if err := spc.createPersistentVolumeClaims(set, pod); err != nil {
        spc.recordPodEvent("create", set, pod, err)
        return err
    }
    
    // 如果成功创建了 PVC，尝试创建 Pod
    err := spc.objectMgr.CreatePod(ctx, pod)
    if apierrors.IsAlreadyExists- err {
        return err
    }
    
    // 根据保留策略设置 PVC 策略
    if utilfeature.DefaultFeatureGate.Enabled(features.StatefulSetAutoDeletePVC) {
        if err := spc.UpdatePodClaimForRetentionPolicy(ctx, set, pod); err != nil {
            spc.recordPodEvent("update", set, pod, err)
            return err
        }
    }
    
    return err
}
```

### 2. PVC 命名规范

```go
// getPersistentVolumeClaimName 获取 PVC 名称
func getPersistentVolumeClaimName(set *apps.StatefulSet, claim *v1.PersistentVolumeClaim, ordinal int) string {
    // 格式：{claimName}-{statefulsetName}-{ordinal}
    return fmt.Sprintf("%s-%s-%d", claim.Name, set.Name, ordinal)
}
```

### 3. 存储管理架构图

```mermaid
graph TB
    subgraph "**StatefulSet 存储管理架构**"
        
        subgraph "**StatefulSet 层**"
            
            STS[**StatefulSet**<br/>**web**<br/>• replicas: 3<br/>• serviceName: web-svc]
            
            VCT[**VolumeClaimTemplate**<br/>• name: data<br/>• size: 10Gi<br/>• storageClass: fast]
        end
        
        subgraph "**Pod 层**"
            
            POD0[**Pod: web-0**<br/>• hostname: web-0<br/>• subdomain: web-svc]
            POD1[**Pod: web-1**<br/>• hostname: web-1<br/>• subdomain: web-svc]
            POD2[**Pod: web-2**<br/>• hostname: web-2<br/>• subdomain: web-svc]
        end
        
        subgraph "**PVC 层**"
            
            PVC0[**PVC: data-web-0**<br/>• size: 10Gi<br/>• storageClass: fast<br/>• bound: pv-001]
            PVC1[**PVC: data-web-1**<br/>• size: 10Gi<br/>• storageClass: fast<br/>• bound: pv-002]
            PVC2[**PVC: data-web-2**<br/>• size: 10Gi<br/>• storageClass: fast<br/>• bound: pv-003]
        end
        
        subgraph "**PV 层**"
            
            PV0[**PV: pv-001**<br/>• capacity: 10Gi<br/>• accessModes: RWO<br/>• path: /data/vol001]
            PV1[**PV: pv-002**<br/>• capacity: 10Gi<br/>• accessModes: RWO<br/>• path: /data/vol002]
            PV2[**PV: pv-003**<br/>• capacity: 10Gi<br/>• accessModes: RWO<br/>• path: /data/vol003]
        end
        
        subgraph "**存储后端**"
            
            STORAGE[**存储系统**<br/>• 本地存储<br/>• 网络存储<br/>• 云存储]
        end
    end
    
    STS --> VCT
    VCT --> POD0
    VCT --> POD1
    VCT --> POD2
    
    POD0 --> PVC0
    POD1 --> PVC1
    POD2 --> PVC2
    
    PVC0 --> PV0
    PVC1 --> PV1
    PVC2 --> PV2
    
    PV0 --> STORAGE
    PV1 --> STORAGE
    PV2 --> STORAGE
    
    style STS fill:#e6f3ff,stroke:#0066cc,stroke-width:3px
    style VCT fill:#e6f3ff,stroke:#0066cc,stroke-width:2px
```

---

## 网络标识与服务发现

### 1. Headless Service 机制

基于源码 `pkg/controller/statefulset/stateful_set_utils.go`：

```go
func initIdentity(set *apps.StatefulSet, pod *v1.Pod) {
    updateIdentity(set, pod)
    // 仅在初始 Pod 创建时设置这些不可变字段，不用于更新
    pod.Spec.Hostname = pod.Name
    pod.Spec.Subdomain = set.Spec.ServiceName
}

// updateIdentity 更新 Pod 的名称、主机名和子域名
func updateIdentity(set *apps.StatefulSet, pod *v1.Pod) {
    ordinal := getOrdinal- pod
    pod.Name = getPodName(set, ordinal)
    pod.Namespace = set.Namespace
    if pod.Labels == nil {
        pod.Labels = make(map[string]string)
    }
    pod.Labels[apps.StatefulSetPodNameLabel] = pod.Name
}
```

### 2. DNS 解析机制图

```mermaid
graph TB
    subgraph "**StatefulSet DNS 解析机制**"
        
        subgraph "**DNS 查询层**"
            
            CLIENT[**客户端应用**<br/>• 查询: web-0.web-svc<br/>• 查询: web-svc<br/>• 查询: web-1.web-svc.default]
        end
        
        subgraph "**DNS 服务层**"
            
            COREDNS[**CoreDNS**<br/>• A记录解析<br/>• SRV记录管理<br/>• 服务发现]
            
            ENDPOINTS[**Endpoints Controller**<br/>• Pod IP 收集<br/>• 端点管理<br/>• 状态同步]
        end
        
        subgraph "**服务层**"
            
            HEADLESS_SVC[**Headless Service**<br/>**web-svc**<br/>• clusterIP: None<br/>• selector: app=web<br/>• ports: 80]
            
            subgraph "**DNS 记录**"
                
                DNS_A[**A 记录**<br/>• web-0.web-svc → 10.1.1.10<br/>• web-1.web-svc → 10.1.1.11<br/>• web-2.web-svc → 10.1.1.12]
                
                DNS_SRV[**SRV 记录**<br/>• _http._tcp.web-svc<br/>• 权重和优先级<br/>• 端口信息]
            end
        end
        
        subgraph "**Pod 层**"
            
            POD0[**Pod: web-0**<br/>• IP: 10.1.1.10<br/>• hostname: web-0<br/>• subdomain: web-svc<br/>• FQDN: web-0.web-svc.default.svc.cluster.local]
            
            POD1[**Pod: web-1**<br/>• IP: 10.1.1.11<br/>• hostname: web-1<br/>• subdomain: web-svc<br/>• FQDN: web-1.web-svc.default.svc.cluster.local]
            
            POD2[**Pod: web-2**<br/>• IP: 10.1.1.12<br/>• hostname: web-2<br/>• subdomain: web-svc<br/>• FQDN: web-2.web-svc.default.svc.cluster.local]
        end
    end
    
    CLIENT --> COREDNS
    COREDNS --> HEADLESS_SVC
    HEADLESS_SVC --> ENDPOINTS
    ENDPOINTS --> POD0
    ENDPOINTS --> POD1
    ENDPOINTS --> POD2
    
    COREDNS --> DNS_A
    COREDNS --> DNS_SRV
    DNS_A --> POD0
    DNS_A --> POD1
    DNS_A --> POD2
```

---

## 滚动更新策略

### 1. 更新策略类型

基于源码 `pkg/apis/apps/types.go`：

```go
const (
    // RollingUpdateStatefulSetStrategyType 滚动更新策略
    // 按照 StatefulSet 排序约束应用到所有 Pod
    RollingUpdateStatefulSetStrategyType StatefulSetUpdateStrategyType = "RollingUpdate"
    
    // OnDeleteStatefulSetStrategyType 删除时更新策略
    // 触发传统行为，禁用版本跟踪和有序滚动重启
    OnDeleteStatefulSetStrategyType StatefulSetUpdateStrategyType = "OnDelete"
)

type RollingUpdateStatefulSetStrategy struct {
    // Partition 指示 StatefulSet 应该在哪个序号进行分区更新
    // 在滚动更新期间，所有序号从 Replicas-1 到 Partition 的 Pod 都会被更新
    // 所有序号从 Partition-1 到 0 的 Pod 保持不变
    Partition int32
    
    // MaxUnavailable 更新期间可以不可用的 Pod 的最大数量
    MaxUnavailable *intstr.IntOrString
}
```

### 2. 滚动更新流程图

```mermaid
sequenceDiagram
    participant USER as **用户**
    participant API as **API Server**
    participant CTRL as **StatefulSet Controller**
    participant KUBELET as **Kubelet**
    
    Note over USER,KUBELET: **StatefulSet 滚动更新流程**
    
    USER->>API: **1. 更新 StatefulSet**
    Note right of USER: **• 更新镜像版本**<br/>**• 更新配置参数**<br/>**• 触发滚动更新**
    
    API->>CTRL: **2. StatefulSet 变更事件**
    CTRL->>CTRL: **3. 创建新修订版本**
    Note right of CTRL: **• ControllerRevision-v2**<br/>**• 包含新配置**<br/>**• 更新标识**
    
    CTRL->>CTRL: **4. 计算分区策略**
    Note right of CTRL: **• Partition: 0 默认**<br/>**• 从最大序号开始更新**<br/>**• 更新范围: [partition, replicas-1]**
    
    Note over CTRL: **开始逆序更新（从 web-2 开始）**
    
    CTRL->>API: **5. 删除 Pod web-2**
    API->>KUBELET: **6. Pod 终止指令**
    KUBELET->>KUBELET: **7. 优雅关闭容器**
    KUBELET->>API: **8. Pod 删除确认**
    
    API->>CTRL: **9. Pod 删除事件**
    CTRL->>API: **10. 创建新 Pod web-2**
    Note right of CTRL: **• 使用新修订版本**<br/>**• 保持相同 PVC**<br/>**• 相同网络标识**
    
    API->>KUBELET: **11. 新 Pod 创建指令**
    KUBELET->>KUBELET: **12. 启动新容器**
    KUBELET->>API: **13. Pod 就绪状态**
    
    Note over CTRL: **等待 web-2 完全就绪后，继续 web-1**
    
    API->>CTRL: **14. Pod 就绪事件**
    CTRL->>CTRL: **15. 验证健康状态**
    
    CTRL->>API: **16. 删除 Pod web-1**
    Note right of CTRL: **重复步骤 6-13**
    
    CTRL->>API: **17. 删除 Pod web-0**
    Note right of CTRL: **重复步骤 6-13**
    
    CTRL->>API: **18. 更新 StatefulSet 状态**
    Note right of CTRL: **• UpdatedReplicas: 3**<br/>**• CurrentRevision: v2**<br/>**• 滚动更新完成**
```

### 3. 分区更新示例图

```mermaid
graph TB
    subgraph "**分区更新策略示例（Partition=1）**"
        
        subgraph "**更新范围 [1, 2]**"
            
            POD2_NEW[**web-2 新版本**<br/>• Revision: v2<br/>• Image: app:2.0<br/>• 状态: 更新完成]
            
            POD1_NEW[**web-1 新版本**<br/>• Revision: v2<br/>• Image: app:2.0<br/>• 状态: 更新完成]
        end
        
        subgraph "**保持范围 [0, 0]**"
            
            POD0_OLD[**web-0 旧版本**<br/>• Revision: v1<br/>• Image: app:1.0<br/>• 状态: 保持不变]
        end
        
        subgraph "**配置信息**"
            
            CONFIG[**更新策略**<br/>• Type: RollingUpdate<br/>• Partition: 1<br/>• MaxUnavailable: 1]
        end
    end
    
    CONFIG --> POD2_NEW
    CONFIG --> POD1_NEW
    CONFIG --> POD0_OLD
    
    style POD2_NEW fill:#90EE90,stroke:#006400,stroke-width:2px
    style POD1_NEW fill:#90EE90,stroke:#006400,stroke-width:2px
    style POD0_OLD fill:#FFA07A,stroke:#FF4500,stroke-width:2px
```

---

## Pod 管理策略

### 1. 管理策略对比

基于源码 `pkg/apis/apps/types.go`：

```go
const (
    // OrderedReadyPodManagement 有序就绪 Pod 管理
    // 在扩容时严格按递增顺序创建 Pod，在缩容时按递减顺序，
    // 只有当前一个 Pod 就绪或终止时才继续处理。一次最多只会更改一个 Pod
    OrderedReadyPodManagement PodManagementPolicyType = "OrderedReady"
    
    // ParallelPodManagement 并行 Pod 管理  
    // 当 StatefulSet 副本数发生变化时，立即创建和删除 Pod，
    // 不等待 Pod 就绪或完全终止
    ParallelPodManagement PodManagementPolicyType = "Parallel"
)
```

### 2. 策略对比图

```mermaid
graph TB
    subgraph "**Pod 管理策略对比**"
        
        subgraph "**OrderedReady 策略**"
            
            OR_TITLE[**有序就绪管理**<br/>**（默认策略）**]
            
            subgraph "**扩容过程**"
                
                OR_SCALE_UP[**扩容：0 → 3**<br/>1. 创建 web-0<br/>2. 等待 web-0 就绪<br/>3. 创建 web-1<br/>4. 等待 web-1 就绪<br/>5. 创建 web-2<br/>6. 等待 web-2 就绪]
            end
            
            subgraph "**缩容过程**"
                
                OR_SCALE_DOWN[**缩容：3 → 1**<br/>1. 删除 web-2<br/>2. 等待 web-2 终止<br/>3. 删除 web-1<br/>4. 等待 web-1 终止]
            end
            
            OR_FEATURES[**特性**<br/>• 严格有序<br/>• 单调操作<br/>• 高可靠性<br/>• 适合有依赖关系的应用]
        end
        
        subgraph "**Parallel 策略**"
            
            PAR_TITLE[**并行管理**<br/>**（高性能策略）**]
            
            subgraph "**扩容过程**"
                
                PAR_SCALE_UP[**扩容：0 → 3**<br/>1. 同时创建 web-0、web-1、web-2<br/>2. 并行启动<br/>3. 不等待就绪状态]
            end
            
            subgraph "**缩容过程**"
                
                PAR_SCALE_DOWN[**缩容：3 → 1**<br/>1. 同时删除 web-2、web-1<br/>2. 并行终止<br/>3. 不等待终止完成]
            end
            
            PAR_FEATURES[**特性**<br/>• 高并发<br/>• 快速扩缩容<br/>• 无序操作<br/>• 适合无依赖关系的应用]
        end
    end
    
    OR_TITLE --> OR_SCALE_UP
    OR_TITLE --> OR_SCALE_DOWN
    OR_TITLE --> OR_FEATURES
    
    PAR_TITLE --> PAR_SCALE_UP
    PAR_TITLE --> PAR_SCALE_DOWN
    PAR_TITLE --> PAR_FEATURES
```

---

## PVC 自动删除策略

### 1. 保留策略配置

基于源码 `pkg/apis/apps/types.go`：

```go
type StatefulSetPersistentVolumeClaimRetentionPolicy struct {
    // WhenDeleted 指定当 StatefulSet 被删除时，
    // 从 StatefulSet VolumeClaimTemplates 创建的 PVC 会发生什么
    // 默认策略 `Retain` 使 PVC 不受 StatefulSet 删除影响
    // `Delete` 策略会删除这些 PVC
    WhenDeleted PersistentVolumeClaimRetentionPolicyType
    
    // WhenScaled 指定当 StatefulSet 缩容时，
    // 从 StatefulSet VolumeClaimTemplates 创建的 PVC 会发生什么  
    // 默认策略 `Retain` 使 PVC 不受缩容影响
    // `Delete` 策略会删除超出副本数的相关 PVC
    WhenScaled PersistentVolumeClaimRetentionPolicyType
}

const (
    // RetainPersistentVolumeClaimRetentionPolicyType 保留策略
    RetainPersistentVolumeClaimRetentionPolicyType PersistentVolumeClaimRetentionPolicyType = "Retain"
    
    // DeletePersistentVolumeClaimRetentionPolicyType 删除策略  
    DeletePersistentVolumeClaimRetentionPolicyType PersistentVolumeClaimRetentionPolicyType = "Delete"
)
```

### 2. PVC 生命周期策略图

```mermaid
graph TB
    subgraph "**PVC 自动删除策略**"
        
        subgraph "**策略配置**"
            
            POLICY[**PVC 保留策略**<br/>• **WhenDeleted**: Retain/Delete<br/>• **WhenScaled**: Retain/Delete<br/>• 默认值: Retain]
        end
        
        subgraph "**WhenDeleted 策略**"
            
            DELETE_RETAIN[**Retain 策略**<br/>• StatefulSet 删除<br/>• PVC 保留<br/>• 数据持久化<br/>• 手动清理]
            
            DELETE_DELETE[**Delete 策略**<br/>• StatefulSet 删除<br/>• PVC 自动删除<br/>• 数据丢失<br/>• 自动清理]
        end
        
        subgraph "**WhenScaled 策略**"
            
            SCALE_RETAIN[**Retain 策略**<br/>• StatefulSet 缩容<br/>• 多余 PVC 保留<br/>• 扩容时重用<br/>• 数据保护]
            
            SCALE_DELETE[**Delete 策略**<br/>• StatefulSet 缩容<br/>• 多余 PVC 删除<br/>• 数据清理<br/>• 存储回收]
        end
        
        subgraph "**场景示例**"
            
            SCENARIO1[**场景一：数据库**<br/>• WhenDeleted: Retain<br/>• WhenScaled: Retain<br/>• 数据安全优先]
            
            SCENARIO2[**场景二：缓存**<br/>• WhenDeleted: Delete<br/>• WhenScaled: Delete<br/>• 存储成本优化]
            
            SCENARIO3[**场景三：混合**<br/>• WhenDeleted: Retain<br/>• WhenScaled: Delete<br/>• 灵活策略]
        end
    end
    
    POLICY --> DELETE_RETAIN
    POLICY --> DELETE_DELETE
    POLICY --> SCALE_RETAIN
    POLICY --> SCALE_DELETE
    
    DELETE_RETAIN --> SCENARIO1
    SCALE_RETAIN --> SCENARIO1
    
    DELETE_DELETE --> SCENARIO2
    SCALE_DELETE --> SCENARIO2
    
    DELETE_RETAIN --> SCENARIO3
    SCALE_DELETE --> SCENARIO3
    
    style SCENARIO1 fill:#90EE90,stroke:#006400,stroke-width:2px
    style SCENARIO2 fill:#FFB6C1,stroke:#DC143C,stroke-width:2px
    style SCENARIO3 fill:#FFFFE0,stroke:#FFD700,stroke-width:2px
```

---

## 使用场景与最佳实践

### 1. 主要使用场景

#### **有状态数据库集群**
- **MySQL 主从复制**：主节点固定为 mysql-0，从节点按序启动
- **MongoDB 分片集群**：每个分片维护独立的存储和网络标识
- **Redis 主从**：主节点和从节点需要稳定的网络标识

#### **分布式存储系统**  
- **Elasticsearch 集群**：节点发现依赖稳定的主机名
- **Cassandra 环**：节点间通过网络标识建立环状拓扑
- **Ceph 存储**：OSD 节点需要持久化存储和稳定标识

#### **消息队列集群**
- **Kafka 集群**：broker 需要稳定的 ID 和存储
- **RabbitMQ 集群**：节点间通过主机名建立集群关系
- **Pulsar 集群**：BookKeeper 和 Broker 需要有序部署

#### **协调服务**
- **Zookeeper 集群**：myid 文件需要持久化存储
- **etcd 集群**：成员发现依赖稳定的网络标识
- **Consul 集群**：节点加入需要知道其他成员地址

### 2. 最佳实践建议

#### **配置优化**

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: web
spec:
  serviceName: web-service
  replicas: 3
  # 有序就绪管理，确保部署顺序
  podManagementPolicy: OrderedReady
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      # 分区更新，支持金丝雀部署
      partition: 0
      # 最大不可用数量
      maxUnavailable: 1
  # PVC 保留策略
  persistentVolumeClaimRetentionPolicy:
    whenDeleted: Retain
    whenScaled: Delete
  # 修订历史限制
  revisionHistoryLimit: 10
```

#### **存储配置**

```yaml
volumeClaimTemplates:
- metadata:
    name: data
  spec:
    accessModes: ["ReadWriteOnce"]
    storageClassName: "fast-ssd"
    resources:
      requests:
        storage: 100Gi
```

#### **服务配置**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: web-service
spec:
  clusterIP: None  # Headless Service
  selector:
    app: web
  ports:
  - port: 80
    targetPort: 8080
```

### 3. 使用场景决策图

```mermaid
flowchart TD
    START([**应用部署决策**]) --> STATEFUL{**需要状态持久化？**}
    
    STATEFUL -->|是| IDENTITY{**需要稳定网络标识？**}
    STATEFUL -->|否| DEPLOYMENT[**使用 Deployment**<br/>• 无状态应用<br/>• 负载均衡<br/>• 快速扩缩容]
    
    IDENTITY -->|是| ORDER{**需要有序部署？**}
    IDENTITY -->|否| PERSISTENT_VOLUME{**需要持久化存储？**}
    
    ORDER -->|是| STATEFULSET_ORDERED[**StatefulSet**<br/>**OrderedReady 策略**<br/>• 数据库集群<br/>• 分布式存储<br/>• 依赖关系明确]
    
    ORDER -->|否| STATEFULSET_PARALLEL[**StatefulSet**<br/>**Parallel 策略**<br/>• 缓存集群<br/>• 计算节点<br/>• 独立有状态实例]
    
    PERSISTENT_VOLUME -->|是| DEPLOYMENT_PVC[**Deployment + PVC**<br/>• 共享存储<br/>• 读写分离<br/>• 简单持久化]
    
    PERSISTENT_VOLUME -->|否| DAEMONSET{**每节点一个实例？**}
    
    DAEMONSET -->|是| DAEMONSET_CHOICE[**DaemonSet**<br/>• 系统代理<br/>• 日志收集<br/>• 监控组件]
    
    DAEMONSET -->|否| JOB{**批处理任务？**}
    
    JOB -->|是| JOB_CHOICE[**Job/CronJob**<br/>• 数据处理<br/>• 定期任务<br/>• 批量计算]
    
    JOB -->|否| DEPLOYMENT
    
    style START fill:#e6f3ff,stroke:#0066cc,stroke-width:2px
    style STATEFULSET_ORDERED fill:#90EE90,stroke:#006400,stroke-width:3px
    style STATEFULSET_PARALLEL fill:#98FB98,stroke:#006400,stroke-width:2px
    style DEPLOYMENT fill:#FFE4B5,stroke:#FF8C00,stroke-width:2px
    style DEPLOYMENT_PVC fill:#F0E68C,stroke:#DAA520,stroke-width:2px
```

---

## 总结

StatefulSet 是 Kubernetes 中管理有状态应用的核心工作负载控制器，通过提供稳定的网络标识、有序的部署和终止、以及持久化存储绑定，满足了有状态应用对一致性和持久化的严格要求。

### 核心价值

1. **稳定标识**：为每个 Pod 提供唯一且持久的网络标识
2. **有序管理**：确保 Pod 按照预定义顺序创建、更新和删除
3. **存储绑定**：每个 Pod 拥有专属的持久化存储卷
4. **滚动更新**：支持有序的滚动更新，保证服务连续性
5. **灵活策略**：提供多种管理策略适应不同场景需求

### 技术特点

- **控制器架构**：基于声明式 API 和控制循环实现
- **修订版本管理**：支持版本追踪和回滚操作  
- **存储生命周期**：精细化的 PVC 管理和自动删除策略
- **网络集成**：与 Headless Service 深度集成提供服务发现
- **扩展性设计**：支持自定义存储类和调度策略

StatefulSet 的设计体现了 Kubernetes 对有状态应用管理的深度理解，为数据库、分布式存储、消息队列等关键基础设施应用提供了可靠的容器化运行环境。通过合理配置和最佳实践，StatefulSet 能够满足企业级有状态应用的生产要求。

