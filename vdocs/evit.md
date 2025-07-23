# Kubernetes Pod Eviction (Pod 驱逐) 机制深度解读

## 目录

1. [概述](#概述)
2. [Pod Eviction 核心概念](#pod-eviction-核心概念)
3. [Pod Eviction 整体架构图](#pod-eviction-整体架构图)
4. [驱逐管理器实现原理](#驱逐管理器实现原理)
5. [驱逐决策流程](#驱逐决策流程)
6. [驱逐阈值配置体系](#驱逐阈值配置体系)
7. [Pod 优先级排序算法](#pod-优先级排序算法)
8. [资源压力条件管理](#资源压力条件管理)
9. [驱逐策略配置与调优](#驱逐策略配置与调优)
10. [监控与故障排除](#监控与故障排除)
11. [最佳实践](#最佳实践)
12. [总结](#总结)

---

## 概述

Pod Eviction (Pod 驱逐) 是 Kubernetes 中的一种节点自我保护机制，当节点资源（内存、磁盘、PID等）出现压力时，Kubelet 会主动驱逐低优先级的 Pod 来维护节点的稳定性。本文档基于 Kubernetes 源码深入分析 Pod 驱逐的完整机制。

### 核心特性

- **资源压力检测**：监控内存、磁盘、inode、PID等关键资源
- **阈值管理**：支持硬阈值和软阈值的灵活配置
- **智能排序**：基于 QoS 类别和资源使用情况的驱逐优先级
- **关键Pod保护**：确保系统关键 Pod 不被驱逐

---

## Pod Eviction 核心概念

### 1. 驱逐管理器核心结构

基于源码 `pkg/kubelet/eviction/eviction_manager.go`：

```go
// managerImpl 实现 Pod 驱逐管理器
type managerImpl struct {
    // 时钟接口，用于时间跟踪
    clock clock.WithTicker
    // 驱逐管理器配置
    config Config
    // 用于杀死 Pod 的函数
    killPodFunc KillPodFunc
    // 镜像垃圾回收接口
    imageGC ImageGC
    // 容器垃圾回收接口
    containerGC ContainerGC
    // 保护内部状态的读写锁
    sync.RWMutex
    // 当前节点条件集合
    nodeConditions []v1.NodeConditionType
    // 记录节点条件最后观察时间
    nodeConditionsLastObservedAt nodeConditionsObservedAt
    // 节点引用
    nodeRef *v1.ObjectReference
    // 事件记录器
    recorder record.EventRecorder
    // 系统使用统计提供者
    summaryProvider stats.SummaryProvider
    // 记录阈值首次观察时间
    thresholdsFirstObservedAt thresholdsObservedAt
    // 已达到但未解决的阈值集合
    thresholdsMet []evictionapi.Threshold
    // 信号到排序函数的映射
    signalToRankFunc map[evictionapi.Signal]rankFunc
    // 信号到节点资源回收函数的映射
    signalToNodeReclaimFuncs map[evictionapi.Signal]nodeReclaimFuncs
    // 最后观察到的信号数据
    lastObservations signalObservations
    // 是否有独立的镜像文件系统
    dedicatedImageFs *bool
    // 内存阈值通知器列表
    thresholdNotifiers []ThresholdNotifier
    // 阈值通知器最后更新时间
    thresholdsLastUpdated time.Time
    // 是否支持本地存储容量隔离
    localStorageCapacityIsolation bool
}
```

### 2. 驱逐配置结构

基于源码 `pkg/kubelet/eviction/types.go`：

```go
// Config 保存驱逐配置信息
type Config struct {
    // 压力状态转换期：kubelet 在退出压力条件前必须等待的时间
    PressureTransitionPeriod time.Duration
    // 软驱逐阈值时允许的最大Pod宽限期（秒）
    MaxPodGracePeriodSeconds int64
    // 阈值定义：监控用于触发驱逐的条件集合
    Thresholds []evictionapi.Threshold
    // 内核memcg通知：如果为true，将集成内核memcg通知来确定是否跨越内存阈值
    KernelMemcgNotification bool
    // Pod cgroup根路径：包含所有Pod的cgroup
    PodCgroupRoot string
}

// Manager 评估节点稳定性的驱逐阈值
type Manager interface {
    // Start 启动控制循环，以指定间隔监控驱逐阈值
    Start(diskInfoProvider DiskInfoProvider, podFunc ActivePodsFunc, podCleanedUpFunc PodCleanedUpFunc, monitoringInterval time.Duration)

    // IsUnderMemoryPressure 如果节点处于内存压力下返回true
    IsUnderMemoryPressure() bool

    // IsUnderDiskPressure 如果节点处于磁盘压力下返回true
    IsUnderDiskPressure() bool

    // IsUnderPIDPressure 如果节点处于PID压力下返回true
    IsUnderPIDPressure() bool
}
```

### 3. 驱逐信号和资源映射

基于源码 `pkg/kubelet/eviction/helpers.go`：

```go
var (
    // signalToNodeCondition 将信号映射到节点条件
    signalToNodeCondition map[evictionapi.Signal]v1.NodeConditionType
    // signalToResource 将信号映射到其关联的资源
    signalToResource map[evictionapi.Signal]v1.ResourceName
)

func init() {
    // 映射驱逐信号到节点条件
    signalToNodeCondition = map[evictionapi.Signal]v1.NodeConditionType{}
    signalToNodeCondition[evictionapi.SignalMemoryAvailable] = v1.NodeMemoryPressure
    signalToNodeCondition[evictionapi.SignalAllocatableMemoryAvailable] = v1.NodeMemoryPressure
    signalToNodeCondition[evictionapi.SignalImageFsAvailable] = v1.NodeDiskPressure
    signalToNodeCondition[evictionapi.SignalNodeFsAvailable] = v1.NodeDiskPressure
    signalToNodeCondition[evictionapi.SignalImageFsInodesFree] = v1.NodeDiskPressure
    signalToNodeCondition[evictionapi.SignalNodeFsInodesFree] = v1.NodeDiskPressure
    signalToNodeCondition[evictionapi.SignalPIDAvailable] = v1.NodePIDPressure

    // 映射信号到资源
    signalToResource = map[evictionapi.Signal]v1.ResourceName{}
    signalToResource[evictionapi.SignalMemoryAvailable] = v1.ResourceMemory
    signalToResource[evictionapi.SignalAllocatableMemoryAvailable] = v1.ResourceMemory
    signalToResource[evictionapi.SignalImageFsAvailable] = v1.ResourceEphemeralStorage
    signalToResource[evictionapi.SignalImageFsInodesFree] = resourceInodes
    signalToResource[evictionapi.SignalNodeFsAvailable] = v1.ResourceEphemeralStorage
    signalToResource[evictionapi.SignalNodeFsInodesFree] = resourceInodes
    signalToResource[evictionapi.SignalPIDAvailable] = resourcePids
}
```

---

## Pod Eviction 整体架构图

上方的架构图展示了 Pod 驱逐机制在 Kubernetes 集群中的完整架构，包括：

1. **Kubelet 组件**：Eviction Manager、Pod Manager、Resource Monitor 等
2. **资源监控**：Memory、Disk、Inodes、PIDs 的实时监控
3. **驱逐策略**：Hard/Soft Thresholds、QoS Classes、Pod Priority 等
4. **系统监控**：cAdvisor、procfs、sysfs 等底层监控组件

---

## 驱逐管理器实现原理

### 1. 驱逐管理器初始化

```go
// NewManager 创建配置好的Manager和关联的准入处理器
func NewManager(
    summaryProvider stats.SummaryProvider,
    config Config,
    killPodFunc KillPodFunc,
    imageGC ImageGC,
    containerGC ContainerGC,
    recorder record.EventRecorder,
    nodeRef *v1.ObjectReference,
    clock clock.WithTicker,
    localStorageCapacityIsolation bool,
) (Manager, lifecycle.PodAdmitHandler) {
    manager := &managerImpl{
        clock:                         clock,
        killPodFunc:                   killPodFunc,
        imageGC:                       imageGC,
        containerGC:                   containerGC,
        config:                        config,
        recorder:                      recorder,
        summaryProvider:               summaryProvider,
        nodeRef:                       nodeRef,
        nodeConditionsLastObservedAt:  nodeConditionsObservedAt{},
        thresholdsFirstObservedAt:     thresholdsObservedAt{},
        dedicatedImageFs:              nil,
        thresholdNotifiers:            []ThresholdNotifier{},
        localStorageCapacityIsolation: localStorageCapacityIsolation,
    }
    return manager, manager
}
```

### 2. Pod 准入控制

```go
// Admit 如果Pod对节点稳定性不安全则拒绝准入
func (m *managerImpl) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
    m.RLock()
    defer m.RUnlock()
    if len(m.nodeConditions) == 0 {
        return lifecycle.PodAdmitResult{Admit: true}
    }
    
    // 即使在资源压力下也准入关键Pod，因为它们对系统稳定性是必需的
    // https://github.com/kubernetes/kubernetes/issues/40573 有更多细节
    if kubelettypes.IsCriticalPod(attrs.Pod) {
        return lifecycle.PodAdmitResult{Admit: true}
    }

    // 检查Pod是否会增加现有的资源压力
    nodeOnlyHasMemoryPressureCondition := hasNodeCondition(m.nodeConditions, v1.NodeMemoryPressure) && len(m.nodeConditions) == 1
    if nodeOnlyHasMemoryPressureCondition {
        // Pod的内存请求必须设置且必须小于节点的可分配内存容量
        podMemoryRequests := resource.Quantity{}
        if attrs.Pod.Spec.Containers != nil {
            podMemoryRequests = resourcehelper.GetResourceRequest(attrs.Pod, v1.ResourceMemory)
        }
        if podMemoryRequests.Cmp(m.capacity[v1.ResourceMemory]) > 0 {
            return lifecycle.PodAdmitResult{
                Admit:   false,
                Reason:  Reason,
                Message: fmt.Sprintf(message, v1.ResourceMemory),
            }
        }
    }
    
    return lifecycle.PodAdmitResult{Admit: false, Reason: Reason, Message: message}
}
```

### 3. 同步驱逐循环

```go
// synchronize 是管理驱逐的控制循环
func (m *managerImpl) synchronize(diskInfoProvider DiskInfoProvider, activePodsFunc ActivePodsFunc) []*v1.Pod {
    // 如果未设置专用镜像文件系统，则查询
    if m.dedicatedImageFs == nil {
        hasImageFs, ok := m.isImageFsDedicated(diskInfoProvider)
        if ok != nil {
            return nil
        }
        m.dedicatedImageFs = &hasImageFs
    }

    activePods := activePodsFunc()
    updateStats := true
    summary, err := m.summaryProvider.Get(updateStats)
    if err != nil {
        klog.ErrorS(err, "Failed to get summary stats")
        return nil
    }

    if m.clock.Since(m.thresholdsLastUpdated) > notifierRefreshInterval {
        m.thresholdsLastUpdated = m.clock.Now()
        for _, notifier := range m.thresholdNotifiers {
            if err := notifier.UpdateThreshold(summary); err != nil {
                klog.ErrorS(err, "Failed to update mem notify threshold", "notifier", notifier.Description())
            }
        }
    }

    // 进行观察并获取信号功能值
    observations, statsFunc := makeSignalObservations(summary)
    debugLogObservations("observations", observations)

    // 确定需要驱逐的阈值
    thresholds := thresholdsMet(m.config.Thresholds, observations, false)
    debugLogThresholdsWithObservation("thresholds - ignoring grace period", thresholds, observations)

    // 跟踪已满足阈值的时间
    now := m.clock.Now()
    thresholdsFirstObservedAt := thresholdsFirstObservedAt(thresholds, m.thresholdsFirstObservedAt, now)

    // 确定满足其宽限期的阈值
    thresholds = thresholdsMet(m.config.Thresholds, observations, true)
    debugLogThresholdsWithObservation("thresholds - respecting grace period", thresholds, observations)

    // 跟踪正在进行的监测
    m.Lock()
    m.lastObservations = observations
    m.thresholdsFirstObservedAt = thresholdsFirstObservedAt
    m.thresholdsMet = thresholds
    
    // 确定节点条件
    nodeConditions := nodeConditions(thresholds)
    if len(nodeConditions) > 0 {
        klog.V(3).InfoS("eviction manager: node conditions - observed", "nodeCondition", nodeConditions)
    }

    // 更新内部状态
    m.nodeConditions = nodeConditions
    m.nodeConditionsLastObservedAt = nodeConditionsLastObservedAt(nodeConditions, m.nodeConditionsLastObservedAt, now)

    // 驱逐Pod
    if len(thresholds) > 0 {
        evictionResults := m.evictPods(activePods, statsFunc, thresholds)
        m.Unlock()
        return evictionResults
    }
    m.Unlock()
    return nil
}
```

---

## 驱逐决策流程

驱逐决策流程图展示了从资源监控到 Pod 驱逐的完整决策过程：

### 1. 资源监控阶段

- **内存监控**：监控可用内存和可分配内存
- **磁盘监控**：监控节点文件系统和镜像文件系统的可用空间
- **Inode监控**：监控文件系统的可用 inode 数量
- **PID监控**：监控系统可用的进程ID数量

### 2. 阈值评估阶段

```go
// thresholdsMet 返回满足的阈值集合
func thresholdsMet(thresholds []evictionapi.Threshold, observations signalObservations, enforceMinReclaim bool) []evictionapi.Threshold {
    results := []evictionapi.Threshold{}
    for i := range thresholds {
        threshold := thresholds[i]
        observed, found := observations[threshold.Signal]
        if !found {
            klog.V(5).InfoS("Eviction manager: no observation found for eviction signal", "signal", threshold.Signal)
            continue
        }
        
        // 确定是否已达到阈值
        thresholdMet := false
        quantity := evictionapi.GetThresholdQuantity(threshold.Value, observed.capacity)
        if threshold.Operator == evictionapi.OpLessThan {
            thresholdMet = observed.available.Cmp(quantity) < 0
        }
        if thresholdMet {
            results = append(results, threshold)
        }
    }
    return results
}
```

### 3. Pod选择和驱逐

```go
// evictPods 驱逐Pod以回收压力资源
func (m *managerImpl) evictPods(activePods []*v1.Pod, statsFunc statsFunc, thresholds []evictionapi.Threshold) []*v1.Pod {
    sort.Sort(byEvictionPriority(activePods))
    
    // 按照阈值优先级排序
    sort.Sort(byEvictionThresholdPriority(thresholds))
    thresholdToReclaim, resourceToReclaim, foundAny := getReclaimableThreshold(thresholds)
    if !foundAny {
        return nil
    }
    
    klog.V(3).InfoS("eviction manager: attempting to reclaim", "resourceName", resourceToReclaim)
    
    // 按驱逐优先级排序Pod
    rank, ok := m.signalToRankFunc[thresholdToReclaim.Signal]
    if !ok {
        klog.ErrorS(nil, "eviction manager: no ranking function for signal", "signal", thresholdToReclaim.Signal)
        return nil
    }
    
    // 对Pod进行排序
    rank(activePods, statsFunc)
    
    klog.V(4).InfoS("eviction manager: pods ranked for eviction", "pods", format.Pods(activePods))
    
    // 记录我们正在回收的资源
    m.recorder.Eventf(m.nodeRef, v1.EventTypeWarning, "EvictionThresholdMet", "Attempting to reclaim %s", resourceToReclaim)
    
    // 驱逐Pod直到满足阈值或没有更多Pod可驱逐
    podsForEviction := []*v1.Pod{}
    for i := range activePods {
        pod := activePods[i]
        if kubelettypes.IsCriticalPod(pod) {
            continue
        }
        
        // 如果Pod标记为不可驱逐，跳过
        if podutil.IsPodTerminating(pod) {
            continue
        }
        
        // 将Pod添加到驱逐列表
        podsForEviction = append(podsForEviction, pod)
        
        // 检查是否已满足回收要求
        if len(podsForEviction) > 0 {
            break
        }
    }
    
    // 驱逐选中的Pod
    if len(podsForEviction) > 0 {
        m.evictPod(podsForEviction[0], 0, "node has conditions", nil)
        return []*v1.Pod{podsForEviction[0]}
    }
    
    klog.V(3).InfoS("eviction manager: unable to evict any pods from the node")
    return nil
}
```

---

## 驱逐阈值配置体系

### 1. 硬阈值配置 (Hard Thresholds)

硬阈值一旦触发会立即驱逐Pod，不提供宽限期：

```yaml
# Kubelet 配置示例
evictionHard:
  memory.available: "100Mi"      # 可用内存低于100Mi时立即驱逐
  nodefs.available: "1Gi"        # 节点文件系统可用空间低于1Gi时立即驱逐
  nodefs.inodesFree: "5%"        # 节点文件系统可用inode低于5%时立即驱逐
  pid.available: "1000"          # 可用PID低于1000时立即驱逐
```

### 2. 软阈值配置 (Soft Thresholds)

软阈值提供宽限期，只有超过宽限期后才会驱逐Pod：

```yaml
# Kubelet 配置示例
evictionSoft:
  memory.available: "300Mi"      # 可用内存低于300Mi
  nodefs.available: "1.5Gi"      # 节点文件系统可用空间低于1.5Gi
  nodefs.inodesFree: "10%"       # 节点文件系统可用inode低于10%
  imagefs.available: "2Gi"       # 镜像文件系统可用空间低于2Gi

evictionSoftGracePeriod:
  memory.available: "1m30s"      # 内存软阈值宽限期
  nodefs.available: "2m"         # 磁盘软阈值宽限期
  nodefs.inodesFree: "2m"        # Inode软阈值宽限期
  imagefs.available: "2m"        # 镜像存储软阈值宽限期

evictionMaxPodGracePeriod: 60    # 最大Pod宽限期（秒）
```

### 3. 完整的Kubelet驱逐配置

```yaml
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
# 硬驱逐阈值
evictionHard:
  memory.available: "100Mi"
  nodefs.available: "1Gi"
  nodefs.inodesFree: "5%"
  imagefs.available: "1Gi"
  imagefs.inodesFree: "5%"
  pid.available: "1000"

# 软驱逐阈值
evictionSoft:
  memory.available: "300Mi"
  nodefs.available: "1.5Gi"
  nodefs.inodesFree: "10%"
  imagefs.available: "2Gi"
  imagefs.inodesFree: "10%"

# 软阈值宽限期
evictionSoftGracePeriod:
  memory.available: "2m"
  nodefs.available: "2m"
  nodefs.inodesFree: "2m"
  imagefs.available: "2m"
  imagefs.inodesFree: "2m"

# 其他驱逐配置
evictionMaxPodGracePeriod: 60        # 最大Pod终止宽限期
evictionMinimumReclaim:              # 每次驱逐的最小资源回收量
  memory.available: "0Mi"
  nodefs.available: "500Mi"
  nodefs.inodesFree: "5%"
  imagefs.available: "1Gi"
  imagefs.inodesFree: "5%"

evictionPressureTransitionPeriod: 5m  # 压力状态转换期
```

---

## Pod 优先级排序算法

### 1. QoS 类别优先级

Pod按照QoS类别进行排序，优先级从高到低：

1. **BestEffort**：没有设置资源requests和limits的Pod
2. **Burstable**：设置了部分资源requests/limits的Pod
3. **Guaranteed**：资源requests等于limits的Pod

```go
// byEvictionPriority 实现Pod驱逐优先级排序
type byEvictionPriority []*v1.Pod

func (a byEvictionPriority) Len() int { return len(a) }

func (a byEvictionPriority) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

func (a byEvictionPriority) Less(i, j int) bool {
    // 首先按QoS类别比较
    qosComparison := v1qos.GetPodQOS(a[i]).Compare(v1qos.GetPodQOS(a[j]))
    if qosComparison != 0 {
        return qosComparison < 0
    }
    
    // 同一QoS类别内，按优先级类别比较
    if priorityClass.IsEnabled() {
        podPriority := corev1helpers.PodPriority(a[i])
        otherPodPriority := corev1helpers.PodPriority(a[j])
        return podPriority < otherPodPriority
    }
    
    return false
}
```

### 2. 资源使用量排序

对于同一QoS类别的Pod，按照资源使用情况进行排序：

```go
// rankMemoryPressure 根据内存压力对Pod进行排序
func rankMemoryPressure(pods []*v1.Pod, stats statsFunc) {
    sort.Sort(sort.Reverse(byMemoryUsage{pods, stats}))
}

type byMemoryUsage struct {
    pods []*v1.Pod
    stats func(*v1.Pod) (statsapi.PodStats, bool)
}

func (m byMemoryUsage) Len() int { return len(m.pods) }

func (m byMemoryUsage) Swap(i, j int) { m.pods[i], m.pods[j] = m.pods[j], m.pods[i] }

func (m byMemoryUsage) Less(i, j int) bool {
    _, podi := m.stats(m.pods[i])
    _, podj := m.stats(m.pods[j])
    if !podi || !podj {
        return podi
    }
    
    // 对于BestEffort Pod，按内存使用量排序
    if v1qos.GetPodQOS(m.pods[i]) == v1.PodQOSBestEffort {
        podStats, found := m.stats(m.pods[i])
        if !found {
            return false
        }
        
        otherPodStats, found := m.stats(m.pods[j])
        if !found {
            return true
        }
        
        return podStats.Memory.WorkingSetBytes > otherPodStats.Memory.WorkingSetBytes
    }
    
    // 对于Burstable Pod，按超出requests的量排序
    if v1qos.GetPodQOS(m.pods[i]) == v1.PodQOSBurstable {
        podMemoryUsage := podMemoryUsage(m.pods[i], m.stats)
        podMemoryRequests := resourcehelper.GetResourceRequest(m.pods[i], v1.ResourceMemory)
        
        otherPodMemoryUsage := podMemoryUsage(m.pods[j], m.stats)
        otherPodMemoryRequests := resourcehelper.GetResourceRequest(m.pods[j], v1.ResourceMemory)
        
        return podMemoryUsage.Sub(podMemoryRequests).Cmp(otherPodMemoryUsage.Sub(otherPodMemoryRequests)) > 0
    }
    
    return false
}
```

### 3. 关键Pod保护

关键系统Pod永远不会被驱逐：

```go
// IsCriticalPod 检查Pod是否为关键系统Pod
func IsCriticalPod(pod *v1.Pod) bool {
    if IsStaticPod(pod) {
        return true
    }
    if IsMirrorPod(pod) {
        return true
    }
    
    // 检查优先级类别
    if pod.Spec.PriorityClassName == scheduling.SystemClusterCritical || 
       pod.Spec.PriorityClassName == scheduling.SystemNodeCritical {
        return true
    }
    
    return false
}

// 在驱逐过程中跳过关键Pod
for i := range activePods {
    pod := activePods[i]
    if kubelettypes.IsCriticalPod(pod) {
        continue  // 跳过关键Pod
    }
    // ... 继续驱逐逻辑
}
```

---

## 资源压力条件管理

### 1. 节点条件类型

Kubernetes定义了三种主要的资源压力条件：

```go
// 节点条件类型
const (
    // NodeMemoryPressure 表示节点内存不足
    NodeMemoryPressure NodeConditionType = "MemoryPressure"
    
    // NodeDiskPressure 表示节点磁盘不足  
    NodeDiskPressure NodeConditionType = "DiskPressure"
    
    // NodePIDPressure 表示节点PID不足
    NodePIDPressure NodeConditionType = "PIDPressure"
)
```

### 2. 节点条件更新

```go
// nodeConditions 根据观察到的阈值返回节点条件
func nodeConditions(thresholds []evictionapi.Threshold) []v1.NodeConditionType {
    conditions := []v1.NodeConditionType{}
    for _, threshold := range thresholds {
        if nodeCondition, found := signalToNodeCondition[threshold.Signal]; found {
            conditions = append(conditions, nodeCondition)
        }
    }
    return removeDuplicates(conditions)
}

// 更新节点状态
func (m *managerImpl) updateNodeCondition(condition v1.NodeConditionType, now time.Time) {
    m.recorder.Eventf(m.nodeRef, v1.EventTypeWarning, "EvictionThresholdMet", 
        "Node %s has condition %s", m.nodeRef.Name, condition)
        
    // 这将在节点状态管理器中更新节点条件
    // 影响Pod调度和集群决策
}
```

### 3. 调度器集成

当节点处于压力状态时，调度器会避免向该节点调度新的Pod：

```go
// 在调度过程中检查节点条件
func (pl *NodeResourcesFit) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
    node := nodeInfo.Node()
    
    // 检查节点是否有压力条件
    for _, condition := range node.Status.Conditions {
        switch condition.Type {
        case v1.NodeMemoryPressure:
            if condition.Status == v1.ConditionTrue {
                return framework.NewStatus(framework.Unschedulable, "Node has memory pressure")
            }
        case v1.NodeDiskPressure:
            if condition.Status == v1.ConditionTrue {
                return framework.NewStatus(framework.Unschedulable, "Node has disk pressure")
            }
        case v1.NodePIDPressure:
            if condition.Status == v1.ConditionTrue {
                return framework.NewStatus(framework.Unschedulable, "Node has PID pressure")
            }
        }
    }
    
    // ... 继续其他过滤逻辑
    return nil
}
```

---

## 驱逐策略配置与调优

### 1. 生产环境推荐配置

```yaml
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration

# 内存优化配置
evictionHard:
  memory.available: "500Mi"        # 为系统保留更多内存
  nodefs.available: "2Gi"          # 为系统保留足够磁盘空间
  nodefs.inodesFree: "5%"
  imagefs.available: "2Gi"
  imagefs.inodesFree: "5%"
  pid.available: "10%"             # 基于百分比更灵活

evictionSoft:
  memory.available: "1Gi"          # 较高的软阈值
  nodefs.available: "5Gi"
  nodefs.inodesFree: "10%"
  imagefs.available: "5Gi"
  imagefs.inodesFree: "10%"

evictionSoftGracePeriod:
  memory.available: "2m"           # 给应用更多时间优雅关闭
  nodefs.available: "5m"
  nodefs.inodesFree: "2m"
  imagefs.available: "5m"
  imagefs.inodesFree: "2m"

evictionMaxPodGracePeriod: 120     # 增加最大宽限期

# 最小回收量 - 避免频繁的小量驱逐
evictionMinimumReclaim:
  memory.available: "500Mi"
  nodefs.available: "1Gi"
  nodefs.inodesFree: "5%"
  imagefs.available: "1Gi"
  imagefs.inodesFree: "5%"

evictionPressureTransitionPeriod: 5m

# 系统保留资源
systemReserved:
  cpu: "1000m"
  memory: "2Gi"
  ephemeral-storage: "10Gi"
  pid: "1000"

kubeReserved:
  cpu: "500m" 
  memory: "1Gi"
  ephemeral-storage: "5Gi"
  pid: "500"

# 强制执行节点分配器
enforceNodeAllocatable:
  - "pods"
  - "system-reserved" 
  - "kube-reserved"
```

### 2. 高内存工作负载优化

```yaml
# 针对内存密集型工作负载的配置
evictionHard:
  memory.available: "1Gi"          # 更高的内存保留
  nodefs.available: "1Gi"
  pid.available: "1000"

evictionSoft:
  memory.available: "2Gi"          # 较高的软阈值提前预警
  nodefs.available: "2Gi"

evictionSoftGracePeriod:
  memory.available: "5m"           # 更长的宽限期

systemReserved:
  memory: "4Gi"                    # 为系统保留更多内存

# 内存管理策略
memorySwap: {}                     # 禁用swap
memoryQoS: {}                      # 启用内存QoS管理
```

### 3. 存储密集型优化

```yaml
# 针对存储密集型工作负载的配置
evictionHard:
  nodefs.available: "5Gi"          # 更多磁盘空间保留
  imagefs.available: "5Gi"
  nodefs.inodesFree: "5%"
  imagefs.inodesFree: "5%"

evictionSoft:
  nodefs.available: "10Gi"
  imagefs.available: "10Gi"
  nodefs.inodesFree: "10%"
  imagefs.inodesFree: "10%"

evictionMinimumReclaim:
  nodefs.available: "2Gi"          # 较大的最小回收量
  imagefs.available: "2Gi"
  nodefs.inodesFree: "5%"
  imagefs.inodesFree: "5%"

# 镜像垃圾回收配置
imageGCHighThresholdPercent: 80    # 镜像占用超过80%时开始GC
imageGCLowThresholdPercent: 70     # GC到70%时停止
imageMinimumGCAge: "5m"           # 镜像最小保留时间

# 容器日志管理
containerLogMaxSize: "50Mi"        # 限制单个容器日志大小
containerLogMaxFiles: 3            # 最大日志文件数量
```

---

## 监控与故障排除

### 1. 驱逐事件监控

```bash
# 查看驱逐相关事件
kubectl get events --all-namespaces | grep -i evict

# 查看特定节点的驱逐事件
kubectl get events --field-selector involvedObject.kind=Node,involvedObject.name=<node-name>

# 查看Pod驱逐事件详情
kubectl describe events <event-name>
```

### 2. 节点资源状态检查

```bash
# 查看节点条件
kubectl describe node <node-name> | grep -A 10 "Conditions:"

# 查看节点资源分配情况
kubectl describe node <node-name> | grep -A 20 "Allocated resources:"

# 查看节点容量和可分配资源
kubectl get node <node-name> -o yaml | grep -A 10 "allocatable:\|capacity:"
```

### 3. Kubelet 日志分析

```bash
# 查看驱逐相关日志
journalctl -u kubelet | grep -i eviction

# 实时监控驱逐事件
journalctl -u kubelet -f | grep -i "eviction\|pressure\|threshold"

# 查看资源压力日志
journalctl -u kubelet | grep -i "MemoryPressure\|DiskPressure\|PIDPressure"
```

### 4. 资源使用监控

```bash
# 系统资源使用情况
free -h                              # 内存使用
df -h                               # 磁盘使用
df -i                               # inode使用
ps aux | wc -l                      # 进程数量

# 容器资源使用
docker stats                        # 容器资源统计
crictl stats                        # CRI容器统计

# 使用kubectl top
kubectl top nodes                   # 节点资源使用
kubectl top pods --all-namespaces  # Pod资源使用
```

### 5. 驱逐监控指标

```yaml
# Prometheus 监控规则示例
groups:
- name: kubelet-eviction
  rules:
  - alert: NodeMemoryPressure
    expr: kube_node_status_condition{condition="MemoryPressure",status="true"} == 1
    for: 2m
    labels:
      severity: warning
    annotations:
      summary: "Node {{ $labels.node }} has memory pressure"
      description: "Node {{ $labels.node }} has been under memory pressure for more than 2 minutes"

  - alert: NodeDiskPressure
    expr: kube_node_status_condition{condition="DiskPressure",status="true"} == 1
    for: 2m
    labels:
      severity: warning
    annotations:
      summary: "Node {{ $labels.node }} has disk pressure"
      description: "Node {{ $labels.node }} has been under disk pressure for more than 2 minutes"

  - alert: PodEvicted
    expr: increase(kube_pod_status_reason{reason="Evicted"}[5m]) > 0
    labels:
      severity: info
    annotations:
      summary: "Pod eviction detected"
      description: "{{ $value }} pod(s) have been evicted in the last 5 minutes"

  - alert: HighPodEvictionRate
    expr: rate(kubelet_evictions_total[5m]) > 0.1
    labels:
      severity: critical
    annotations:
      summary: "High pod eviction rate detected"
      description: "Pod eviction rate is {{ $value }} evictions/second"
```

### 6. 故障排除脚本

```bash
#!/bin/bash
# node-eviction-debug.sh - 节点驱逐故障排除脚本

NODE_NAME=${1:-$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')}

echo "=== Node Eviction Debug Report for $NODE_NAME ==="
echo

echo "1. Node Conditions:"
kubectl describe node $NODE_NAME | grep -A 10 "Conditions:"
echo

echo "2. Node Resource Allocation:"
kubectl describe node $NODE_NAME | grep -A 20 "Allocated resources:"
echo

echo "3. Recent Eviction Events:"
kubectl get events --field-selector involvedObject.kind=Node,involvedObject.name=$NODE_NAME | grep -i evict
echo

echo "4. Pods on Node:"
kubectl get pods --all-namespaces --field-selector spec.nodeName=$NODE_NAME -o wide
echo

echo "5. System Resource Usage:"
echo "Memory:"
ssh $NODE_NAME 'free -h'
echo
echo "Disk:"
ssh $NODE_NAME 'df -h'
echo
echo "Inodes:"
ssh $NODE_NAME 'df -i'
echo

echo "6. Kubelet Configuration:"
ssh $NODE_NAME 'ps aux | grep kubelet' | head -1
echo

echo "7. Recent Kubelet Logs:"
ssh $NODE_NAME 'journalctl -u kubelet --since "1 hour ago" | grep -i eviction | tail -10'
```

---

## 最佳实践

### 1. 预防性措施

#### 合理的资源配置

```yaml
# Pod资源配置最佳实践
apiVersion: v1
kind: Pod
metadata:
  name: well-configured-pod
spec:
  containers:
  - name: app
    image: myapp:latest
    resources:
      requests:              # 设置合理的requests
        memory: "128Mi"
        cpu: "100m"
        ephemeral-storage: "1Gi"
      limits:                # 设置limits防止资源泄露
        memory: "256Mi"
        cpu: "200m"
        ephemeral-storage: "2Gi"
    livenessProbe:           # 健康检查确保应用正常
      httpGet:
        path: /health
        port: 8080
      initialDelaySeconds: 30
      periodSeconds: 10
```

#### 节点资源预留

```yaml
# 合理配置节点资源预留
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration

# 系统组件资源预留
systemReserved:
  cpu: "1000m"              # 为操作系统保留1个CPU核心
  memory: "2Gi"             # 为系统进程保留2GB内存
  ephemeral-storage: "10Gi" # 为系统保留10GB存储
  pid: "1000"              # 为系统保留1000个PID

# Kubernetes组件资源预留  
kubeReserved:
  cpu: "500m"               # 为kubelet等组件保留500m CPU
  memory: "1Gi"             # 为Kubernetes组件保留1GB内存
  ephemeral-storage: "5Gi"  # 为Kubernetes组件保留5GB存储
  pid: "500"               # 为Kubernetes组件保留500个PID

# 强制执行资源预留
enforceNodeAllocatable:
  - "pods"                  # 对Pod强制执行分配限制
  - "system-reserved"       # 强制执行系统预留
  - "kube-reserved"        # 强制执行Kubernetes预留
```

### 2. 监控和告警策略

#### 全面的监控覆盖

```yaml
# Grafana Dashboard 配置示例
dashboard:
  title: "Kubernetes Node Eviction Monitoring"
  panels:
    - title: "Node Memory Usage"
      targets:
        - expr: '(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100'
          legendFormat: "{{ instance }}"
      thresholds:
        - value: 80
          color: "yellow"
        - value: 90
          color: "red"
          
    - title: "Node Disk Usage"  
      targets:
        - expr: '(1 - (node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"})) * 100'
          legendFormat: "{{ instance }}"
      thresholds:
        - value: 80
          color: "yellow"
        - value: 90
          color: "red"

    - title: "Pod Eviction Rate"
      targets:
        - expr: 'rate(kubelet_evictions_total[5m])'
          legendFormat: "{{ node }}"
```

#### 预警机制

```yaml
# AlertManager 配置
route:
  group_by: ['alertname']
  group_wait: 10s
  group_interval: 10s
  repeat_interval: 1h
  receiver: 'kubernetes-eviction'

receivers:
- name: 'kubernetes-eviction'
  webhook_configs:
  - url: 'http://alertmanager-webhook/eviction'
    send_resolved: true
    
# 预警规则
groups:
- name: eviction-prevention
  rules:
  - alert: HighMemoryUsage
    expr: (1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) > 0.85
    for: 2m
    labels:
      severity: warning
    annotations:
      summary: "High memory usage on node {{ $labels.instance }}"
      runbook_url: "https://wiki.company.com/runbooks/high-memory"
```

### 3. 应用层面的最佳实践

#### 优雅关闭处理

```go
// 应用程序优雅关闭示例
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"
)

func main() {
    server := &http.Server{
        Addr:    ":8080",
        Handler: http.DefaultServeMux,
    }
    
    // 启动服务器
    go func() {
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server failed to start: %v", err)
        }
    }()
    
    // 优雅关闭处理
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    <-c
    
    log.Println("Shutting down gracefully...")
    
    // 创建关闭上下文，给应用30秒时间完成清理
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    // 关闭HTTP服务器
    if err := server.Shutdown(ctx); err != nil {
        log.Printf("Server shutdown failed: %v", err)
    } else {
        log.Println("Server shutdown completed")
    }
}
```

#### Pod Disruption Budget

```yaml
# 设置PDB保护关键应用
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: critical-app-pdb
spec:
  minAvailable: 2
  selector:
    matchLabels:
      app: critical-app
---
# 或者使用百分比
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: web-app-pdb
spec:
  maxUnavailable: 25%
  selector:
    matchLabels:
      app: web-app
```

### 4. 集群级别优化

#### 多层次监控

```bash
# 集群监控脚本
#!/bin/bash
# cluster-health-check.sh

echo "=== Cluster Eviction Health Check ==="

echo "1. Nodes with Pressure Conditions:"
kubectl get nodes -o custom-columns=NAME:.metadata.name,MEMORY:.status.conditions[?(@.type==\"MemoryPressure\")].status,DISK:.status.conditions[?(@.type==\"DiskPressure\")].status,PID:.status.conditions[?(@.type==\"PIDPressure\")].status

echo -e "\n2. Recent Eviction Events:"
kubectl get events --all-namespaces | grep -i evicted | head -10

echo -e "\n3. Nodes Resource Usage:"
kubectl top nodes

echo -e "\n4. Pods in Evicted State:"
kubectl get pods --all-namespaces | grep Evicted | wc -l

echo -e "\n5. High Memory Usage Pods:"
kubectl top pods --all-namespaces --sort-by=memory | head -10

echo -e "\n6. Nodes Capacity vs Allocation:"
for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
    echo "Node: $node"
    kubectl describe node $node | grep -A 5 "Allocated resources:"
    echo "---"
done
```

---

## 总结

### 🔑 **核心要点**

1. **资源保护机制**：Pod Eviction 是 Kubernetes 节点自我保护的关键机制，通过主动驱逐Pod避免节点资源耗尽

2. **智能优先级排序**：基于QoS类别、资源使用情况和Pod优先级的多层次排序算法确保关键应用的可用性

3. **灵活阈值配置**：支持硬阈值和软阈值的组合配置，平衡系统稳定性和应用可用性

4. **全面资源监控**：涵盖内存、磁盘、inode、PID等多维度资源的实时监控和压力检测

### 🏆 **最佳实践**

- **预防优于治疗**：通过合理的资源配置和节点预留避免频繁的驱逐事件
- **完善监控体系**：建立多层次的资源监控和告警机制
- **优雅关闭处理**：应用程序实现优雅关闭机制配合驱逐流程
- **PDB保护**：为关键应用配置Pod Disruption Budget提供额外保护

### 🎯 **生产环境建议**

- **保守的阈值配置**：在生产环境中使用较为保守的阈值设置
- **充足的系统预留**：为系统组件和Kubernetes组件预留足够的资源
- **定期容量规划**：根据业务增长定期评估和调整节点容量
- **应急响应流程**：建立驱逐事件的应急响应和问题排查流程

Pod Eviction 机制是 Kubernetes 集群稳定性的重要保障，正确理解和配置这一机制对于构建可靠的生产环境至关重要。
