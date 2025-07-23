# Kubernetes Node Controller 架构与原理深度解读

## 目录

1. [概述](#概述)
2. [Node Controller 核心概念](#node-controller-核心概念)
3. [Node Controller 整体架构](#node-controller-整体架构)
4. [节点生命周期管理](#节点生命周期管理)
5. [节点健康状态监控](#节点健康状态监控)
6. [节点污点和容忍度机制](#节点污点和容忍度机制)
7. [Zone 区域状态管理](#zone-区域状态管理)
8. [Pod 驱逐机制](#pod-驱逐机制)
9. [Node Lease 机制](#node-lease-机制)
10. [性能优化与监控](#性能优化与监控)
11. [故障排除与调试](#故障排除与调试)
12. [总结](#总结)

---

## 概述

Node Controller 是 Kubernetes 控制平面的核心组件之一，负责管理集群中所有节点的生命周期、健康状态监控、污点管理和 Pod 驱逐。它通过持续监控节点状态，及时响应节点故障，确保集群的整体健康和可用性。本文档基于 Kubernetes 源码深入分析 Node Controller 的架构设计和实现原理。

### 核心功能

- **节点生命周期管理**：监控节点加入和离开集群
- **健康状态检测**：持续检测节点的健康状态
- **污点管理**：根据节点状态自动添加和移除污点
- **Pod 驱逐**：在节点不可用时驱逐其上的 Pod
- **区域感知**：支持多可用区的故障域管理

---

## Node Controller 核心概念

### 1. Node Lifecycle Controller

基于源码 `pkg/controller/nodelifecycle/node_lifecycle_controller.go`：

```go
// Controller 是节点生命周期控制器的核心结构
type Controller struct {
    taintManager                   *scheduler.NoExecuteTaintManager
    nodeMonitorPeriod             time.Duration
    nodeStartupGracePeriod        time.Duration  
    nodeMonitorGracePeriod        time.Duration
    evictionLimiterQPS            float32
    secondaryEvictionLimiterQPS   float32
    largeClusterThreshold         int32
    unhealthyThreshold            float32
    
    nodeLister                    corelisters.NodeLister
    nodeInformer                  coreinformers.NodeInformer
    podLister                     corelisters.PodLister
    podInformer                   coreinformers.PodInformer
    
    kubeClient                    clientset.Interface
    recorder                      record.EventRecorder
    
    // Zone 管理相关
    zoneStates                    map[string]ZoneState
    zonePodEvictor               map[string]*scheduler.RateLimitedTimedQueue
    
    // 节点监控相关
    knownNodeSet                  sets.String
    lastObservedHealthy           map[string]time.Time
    enterPartialDisruptionFunc    func(nodeNum int) float32
    enterFullDisruptionFunc       func(nodeNum int) float32
}

// ZoneState 定义区域状态枚举
type ZoneState int

const (
    stateInitial ZoneState = iota
    stateNormal
    stateFullDisruption
    statePartialDisruption
)
```

### 2. 节点状态条件

```go
// 节点关键状态条件
const (
    // NodeReady 表示节点是否准备好接受Pod
    NodeReady api.NodeConditionType = "Ready"
    
    // NodeMemoryPressure 表示节点是否有内存压力
    NodeMemoryPressure api.NodeConditionType = "MemoryPressure"
    
    // NodeDiskPressure 表示节点是否有磁盘压力  
    NodeDiskPressure api.NodeConditionType = "DiskPressure"
    
    // NodePIDPressure 表示节点是否有PID资源压力
    NodePIDPressure api.NodeConditionType = "PIDPressure"
    
    // NodeNetworkUnavailable 表示节点网络是否不可用
    NodeNetworkUnavailable api.NodeConditionType = "NetworkUnavailable"
)

// 节点状态值
const (
    ConditionTrue    api.ConditionStatus = "True"
    ConditionFalse   api.ConditionStatus = "False" 
    ConditionUnknown api.ConditionStatus = "Unknown"
)
```

### 3. 节点污点类型

基于源码分析，节点控制器管理的主要污点类型：

```go
// 系统自动管理的污点
const (
    // NotReady 污点：节点未就绪
    TaintNodeNotReady = "node.kubernetes.io/not-ready"
    
    // Unreachable 污点：节点不可达  
    TaintNodeUnreachable = "node.kubernetes.io/unreachable"
    
    // 资源压力相关污点
    TaintNodeMemoryPressure = "node.kubernetes.io/memory-pressure"
    TaintNodeDiskPressure   = "node.kubernetes.io/disk-pressure"
    TaintNodePIDPressure    = "node.kubernetes.io/pid-pressure"
    
    // 网络不可用污点
    TaintNodeNetworkUnavailable = "node.kubernetes.io/network-unavailable"
    
    // 不可调度污点
    TaintNodeUnschedulable = "node.kubernetes.io/unschedulable"
)

// 污点效果类型
const (
    TaintEffectNoSchedule       api.TaintEffect = "NoSchedule"
    TaintEffectPreferNoSchedule api.TaintEffect = "PreferNoSchedule" 
    TaintEffectNoExecute        api.TaintEffect = "NoExecute"
)
```

---

## Node Controller 整体架构

上方的架构图展示了 Node Controller 的核心组件架构，包括：

1. **Node Lifecycle Controller**：核心控制器组件
2. **Taint Manager**：污点管理和 Pod 驱逐
3. **Zone Management**：区域状态管理和速率限制
4. **Health Monitoring**：节点健康状态监控

---

## 节点生命周期管理

### 1. 节点注册和发现

基于源码分析，节点生命周期管理的核心流程：

```go
// NewNodeLifecycleController 创建节点生命周期控制器
func NewNodeLifecycleController(
    leaseInformer coordinformers.LeaseInformer,
    podInformer coreinformers.PodInformer,
    nodeInformer coreinformers.NodeInformer,
    daemonSetInformer appsv1informers.DaemonSetInformer,
    kubeClient clientset.Interface,
    nodeMonitorPeriod time.Duration,
    nodeStartupGracePeriod time.Duration,
    nodeMonitorGracePeriod time.Duration,
    podEvictionTimeout time.Duration,
    evictionLimiterQPS float32,
    secondaryEvictionLimiterQPS float32,
    largeClusterThreshold int32,
    unhealthyThreshold float32,
    runTaintManager bool) (*Controller, error) {

    nc := &Controller{
        kubeClient:                    kubeClient,
        nodeMonitorPeriod:            nodeMonitorPeriod,
        nodeStartupGracePeriod:       nodeStartupGracePeriod,
        nodeMonitorGracePeriod:       nodeMonitorGracePeriod,
        evictionLimiterQPS:           evictionLimiterQPS,
        secondaryEvictionLimiterQPS:  secondaryEvictionLimiterQPS,
        largeClusterThreshold:        largeClusterThreshold,
        unhealthyThreshold:           unhealthyThreshold,
        knownNodeSet:                 sets.NewString(),
        lastObservedHealthy:          make(map[string]time.Time),
        recorder:                     recorder,
        nodeLister:                   nodeInformer.Lister(),
        nodeInformer:                 nodeInformer,
        podLister:                    podInformer.Lister(),
        podInformer:                  podInformer,
        daemonSetLister:              daemonSetInformer.Lister(),
        daemonSetInformer:            daemonSetInformer,
        enterPartialDisruptionFunc:   nc.ReducedQPSFunc,
        enterFullDisruptionFunc:      nc.HealthyQPSFunc,
    }
    
    // 设置事件处理器
    nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc:    nc.nodeAdd,
        UpdateFunc: nc.nodeUpdate,
        DeleteFunc: nc.nodeDelete,
    })
    
    return nc, nil
}
```

### 2. 节点状态监控循环

```go
// doNodeProcessingPassWorker 节点处理工作循环
func (nc *Controller) doNodeProcessingPassWorker() {
    for {
        if !nc.doNodeProcessingPass() {
            // 如果没有工作要处理，休眠一段时间
            time.Sleep(nc.nodeMonitorPeriod)
        }
    }
}

// doNodeProcessingPass 执行单次节点处理循环
func (nc *Controller) doNodeProcessingPass() bool {
    nodes, err := nc.nodeLister.List(labels.Everything())
    if err != nil {
        utilruntime.HandleError(fmt.Errorf("unable to list nodes: %v", err))
        return false
    }
    
    added, deleted, newZoneRepresentatives := nc.classifyNodes(nodes)
    
    // 处理新增节点
    for i := range added {
        klog.V(1).Infof("Controller observed a new Node: %#v", added[i].Name)
        nc.handleNewNode(added[i])
    }
    
    // 处理删除节点
    for i := range deleted {
        klog.V(1).Infof("Controller observed a Node deletion: %v", deleted[i])
        nc.handleNodeDeletion(deleted[i])
    }
    
    // 更新区域状态
    nc.handleDisruption(newZoneRepresentatives)
    
    return true
}
```

### 3. 节点分类和状态更新

```go
// classifyNodes 对节点进行分类
func (nc *Controller) classifyNodes(nodes []*v1.Node) (added, deleted []*v1.Node, newZoneRepresentatives map[string][]*v1.Node) {
    newNodeSet := sets.NewString()
    newZoneRepresentatives = make(map[string][]*v1.Node)
    
    for i := range nodes {
        node := nodes[i].DeepCopy()
        
        // 构建新的节点集合
        newNodeSet.Insert(node.Name)
        
        // 按区域分组节点
        zone := utilnode.GetZoneKey(node)
        if newZoneRepresentatives[zone] == nil {
            newZoneRepresentatives[zone] = []*v1.Node{}
        }
        newZoneRepresentatives[zone] = append(newZoneRepresentatives[zone], node)
        
        // 检查节点是否为新增
        if !nc.knownNodeSet.Has(node.Name) {
            added = append(added, node)
        }
    }
    
    // 找出已删除的节点
    for nodeName := range nc.knownNodeSet {
        if !newNodeSet.Has(nodeName) {
            deleted = append(deleted, &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}})
        }
    }
    
    nc.knownNodeSet = newNodeSet
    return added, deleted, newZoneRepresentatives
}
```

---

## 节点健康状态监控

### 1. 节点健康状态检查

基于源码 `pkg/controller/nodelifecycle/node_lifecycle_controller.go`：

```go
// tryUpdateNodeHealth 尝试更新节点健康状态
func (nc *Controller) tryUpdateNodeHealth(node *v1.Node) (time.Duration, v1.NodeCondition, error) {
    nodeHealth := nc.computeNodeHealth(node)
    gracePeriod, observedReadyCondition, currentReadyCondition, err := nc.doUpdateNodeHealth(node, nodeHealth)
    if err != nil {
        return gracePeriod, currentReadyCondition, err
    }
    
    // 如果节点健康状态发生变化，记录事件
    if !apiequality.Semantic.DeepEqual(observedReadyCondition, currentReadyCondition) {
        if currentReadyCondition.Status == v1.ConditionTrue {
            nc.recorder.Eventf(node, v1.EventTypeNormal, "NodeReady", "Node %s status is now: NodeReady", node.Name)
        } else {
            nc.recorder.Eventf(node, v1.EventTypeNormal, "NodeNotReady", "Node %s status is now: NodeNotReady", node.Name)
        }
    }
    
    return gracePeriod, currentReadyCondition, nil
}

// computeNodeHealth 计算节点健康状态
func (nc *Controller) computeNodeHealth(node *v1.Node) *NodeHealthData {
    nodeHealth := &NodeHealthData{
        status:                   node.Status.DeepCopy(),
        probeTimestamp:          node.CreationTimestamp,
        readyTransitionTimestamp: node.CreationTimestamp,
    }
    
    // 查找Ready状态条件
    var observedReadyCondition v1.NodeCondition
    _, observedReadyCondition = nodeutil.GetNodeCondition(&node.Status, v1.NodeReady)
    if observedReadyCondition.LastHeartbeatTime.IsZero() {
        nodeHealth.probeTimestamp = observedReadyCondition.LastProbeTime
        nodeHealth.readyTransitionTimestamp = observedReadyCondition.LastTransitionTime
    } else {
        nodeHealth.probeTimestamp = observedReadyCondition.LastHeartbeatTime  
        nodeHealth.readyTransitionTimestamp = observedReadyCondition.LastTransitionTime
    }
    
    return nodeHealth
}
```

### 2. 节点租约机制集成

```go
// computeNodeHealthByLease 通过租约计算节点健康状态
func (nc *Controller) computeNodeHealthByLease(node *v1.Node) (*NodeHealthData, error) {
    // 获取节点租约对象
    lease, err := nc.leaseLister.Leases(v1.NamespaceNodeLease).Get(node.Name)
    if err != nil {
        return nil, err
    }
    
    nodeHealth := &NodeHealthData{
        status: node.Status.DeepCopy(),
    }
    
    // 使用租约的续约时间作为探测时间戳
    if lease.Spec.RenewTime != nil {
        nodeHealth.probeTimestamp = *lease.Spec.RenewTime
    } else {
        nodeHealth.probeTimestamp = lease.CreationTimestamp
    }
    
    // 计算节点状态条件
    now := nc.now()
    gracePeriod := nc.nodeMonitorGracePeriod
    if nc.now().Sub(node.CreationTimestamp.Time) < nc.nodeStartupGracePeriod {
        gracePeriod = nc.nodeStartupGracePeriod
    }
    
    var currentReadyCondition v1.NodeCondition
    if nodeHealth.probeTimestamp.Add(gracePeriod).After(now) {
        // 节点健康
        currentReadyCondition = v1.NodeCondition{
            Type:               v1.NodeReady,
            Status:             v1.ConditionTrue,
            Reason:             "NodeReady",
            Message:            "kubelet is ready.",
            LastHeartbeatTime:  nodeHealth.probeTimestamp,
            LastTransitionTime: now,
        }
    } else {
        // 节点不健康
        currentReadyCondition = v1.NodeCondition{
            Type:               v1.NodeReady,
            Status:             v1.ConditionUnknown,
            Reason:             "NodeStatusUnknown", 
            Message:            "kubelet stopped posting node status.",
            LastHeartbeatTime:  nodeHealth.probeTimestamp,
            LastTransitionTime: now,
        }
    }
    
    nodeHealth.status.Conditions = []v1.NodeCondition{currentReadyCondition}
    return nodeHealth, nil
}
```

---

## 节点污点和容忍度机制

上方的污点和容忍度机制图展示了完整的污点管理流程，包括：

1. **污点类型分类**：NoSchedule、PreferNoSchedule、NoExecute
2. **常见污点键值**：节点状态相关的标准污点
3. **Pod 容忍度配置**：不同类型的容忍度匹配规则
4. **驱逐行为决策**：根据容忍度决定Pod的驱逐策略

### 1. 污点添加逻辑

基于源码 `pkg/controller/nodelifecycle/scheduler/taint_manager.go`：

```go
// processTaintBaseEviction 处理基于污点的驱逐
func (tm *NoExecuteTaintManager) processTaintBaseEviction(podNamespacedName types.NamespacedName, th *throttlerargs.ThrottlerArguments) {
    if th.ActionType == throttlerargs.ActionTypeDelete {
        tm.taintEvictionQueue.AddWork(NewWorkArgs(podNamespacedName.Name, podNamespacedName.Namespace))
        return
    }
    
    pod, err := tm.podLister.Pods(podNamespacedName.Namespace).Get(podNamespacedName.Name)
    if err != nil {
        if apierrors.IsNotFound(err) {
            return
        }
        utilruntime.HandleError(fmt.Errorf("could not get pod %s/%s: %v", podNamespacedName.Namespace, podNamespacedName.Name, err))
        return
    }
    
    // 检查Pod是否仍在相同节点上
    if pod.Spec.NodeName != th.NodeName {
        return
    }
    
    // 计算容忍时间
    tolerationTime, hasDelayToleration := tm.getMinTolerationTime(pod)
    
    if !hasDelayToleration {
        // Pod无法容忍，立即驱逐
        tm.taintEvictionQueue.AddWork(NewWorkArgs(pod.Name, pod.Namespace))
        return
    }
    
    // 延迟驱逐
    durationUntilEviction := time.Until(tolerationTime)
    if durationUntilEviction < 0 {
        tm.taintEvictionQueue.AddWork(NewWorkArgs(pod.Name, pod.Namespace))
        return
    }
    
    tm.taintEvictionQueue.AddWorkWithDelay(NewWorkArgs(pod.Name, pod.Namespace), durationUntilEviction)
}
```

### 2. 容忍度匹配算法

```go
// getMinTolerationTime 获取Pod的最小容忍时间
func (tm *NoExecuteTaintManager) getMinTolerationTime(pod *v1.Pod) (time.Time, bool) {
    now := time.Now()
    tolerationTime := now
    hasDelayToleration := false
    
    node, err := tm.nodeLister.Get(pod.Spec.NodeName)
    if err != nil {
        return tolerationTime, false
    }
    
    for _, nodeTaint := range node.Spec.Taints {
        if nodeTaint.Effect != v1.TaintEffectNoExecute {
            continue
        }
        
        hasMatchingToleration := false
        tolerationSeconds := int64(-1)
        
        for _, podToleration := range pod.Spec.Tolerations {
            if podToleration.ToleratesTaint(&nodeTaint) {
                hasMatchingToleration = true
                if podToleration.TolerationSeconds != nil {
                    tolerationSeconds = *podToleration.TolerationSeconds
                }
                break
            }
        }
        
        if !hasMatchingToleration {
            return tolerationTime, false
        }
        
        if tolerationSeconds >= 0 {
            hasDelayToleration = true
            startTime := now
            if podTaintTime, exists := tm.taintedNodes[pod.Spec.NodeName][nodeTaint.ToString()]; exists {
                startTime = podTaintTime
            }
            
            currTolerationTime := startTime.Add(time.Duration(tolerationSeconds) * time.Second)
            if currTolerationTime.Before(tolerationTime) {
                tolerationTime = currTolerationTime
            }
        }
    }
    
    return tolerationTime, hasDelayToleration
}
```

---

## Zone 区域状态管理

### 1. 区域状态计算

基于源码分析，区域状态管理的核心逻辑：

```go
// handleDisruption 处理区域中断状态
func (nc *Controller) handleDisruption(zoneToNodeConditions map[string][]*v1.Node) {
    newZoneStates := make(map[string]ZoneState)
    
    for zone, nodes := range zoneToNodeConditions {
        unhealthy, newState := nc.computeZoneStateFunc(nodes)
        if currentState, found := nc.zoneStates[zone]; found {
            if newState != stateInitial && currentState != newState {
                klog.V(1).Infof("Zone %v state changed from %v to %v", zone, currentState, newState)
            }
        }
        
        newZoneStates[zone] = newState
        
        if _, had := nc.zonePodEvictor[zone]; !had {
            nc.zonePodEvictor[zone] = scheduler.CreateTaintEvictionQueue(nc.enterFullDisruptionFunc, nc.enterPartialDisruptionFunc)
        }
        
        // 根据区域状态调整驱逐速率
        switch newState {
        case stateFullDisruption:
            nc.zonePodEvictor[zone].SwapLimiter(0)
        case statePartialDisruption:
            nc.zonePodEvictor[zone].SwapLimiter(nc.secondaryEvictionLimiterQPS)
        case stateNormal:
            nc.zonePodEvictor[zone].SwapLimiter(nc.evictionLimiterQPS)
        }
    }
    
    nc.zoneStates = newZoneStates
}

// computeZoneState 计算区域状态
func (nc *Controller) computeZoneState(nodeReadyConditions []*v1.Node) (int, ZoneState) {
    readyNodes := 0
    notReadyNodes := 0
    
    for i := range nodeReadyConditions {
        if nc.isNodeHealthy(nodeReadyConditions[i]) {
            readyNodes++
        } else {
            notReadyNodes++
        }
    }
    
    switch {
    case readyNodes == 0 && notReadyNodes > 0:
        return notReadyNodes, stateFullDisruption
    case notReadyNodes > 2 && float32(notReadyNodes)/float32(notReadyNodes+readyNodes) >= nc.unhealthyThreshold:
        return notReadyNodes, statePartialDisruption
    default:
        return notReadyNodes, stateNormal
    }
}
```

### 2. 驱逐速率限制

```go
// ReducedQPSFunc 返回降低的QPS函数
func (nc *Controller) ReducedQPSFunc(nodeNum int) float32 {
    if int32(nodeNum) > nc.largeClusterThreshold {
        return nc.secondaryEvictionLimiterQPS
    }
    return nc.evictionLimiterQPS
}

// HealthyQPSFunc 返回健康状态的QPS函数  
func (nc *Controller) HealthyQPSFunc(nodeNum int) float32 {
    return nc.evictionLimiterQPS
}
```

---

## Pod 驱逐机制

### 1. Pod 驱逐决策流程

上方的序列图展示了完整的节点污点管理和Pod驱逐流程：

1. **节点状态检测**：Kubelet 报告节点不健康状态
2. **污点决策制定**：Node Controller 决定添加何种污点
3. **污点应用**：将污点应用到节点对象
4. **Pod容忍度检查**：检查节点上Pod的容忍度配置
5. **Pod驱逐执行**：驱逐无法容忍污点的Pod
6. **节点恢复**：节点恢复后移除污点

### 2. Pod 驱逐实现

```go
// evictPods 驱逐Pod的核心函数
func (tm *NoExecuteTaintManager) evictPods() {
    for {
        item, quit := tm.taintEvictionQueue.Get()
        if quit {
            break
        }
        workArgs := item.(*WorkArgs)
        func() {
            defer tm.taintEvictionQueue.Done(item)
            
            pod, err := tm.podLister.Pods(workArgs.NamespacedName.Namespace).Get(workArgs.NamespacedName.Name)
            if err != nil {
                if apierrors.IsNotFound(err) {
                    return
                }
                utilruntime.HandleError(fmt.Errorf("could not get pod %s/%s: %v", workArgs.NamespacedName.Namespace, workArgs.NamespacedName.Name, err))
                tm.taintEvictionQueue.AddRateLimited(item)
                return
            }
            
            // 检查Pod是否需要驱逐
            if !tm.processPodOnNode(pod) {
                return
            }
            
            // 执行Pod驱逐
            if err := tm.evictPod(pod); err != nil {
                utilruntime.HandleError(fmt.Errorf("error evicting pod %s/%s: %v", pod.Namespace, pod.Name, err))
                tm.taintEvictionQueue.AddRateLimited(item)
            }
        }()
    }
}

// evictPod 执行单个Pod的驱逐
func (tm *NoExecuteTaintManager) evictPod(pod *v1.Pod) error {
    deleteOptions := metav1.DeleteOptions{}
    if pod.Spec.GracefulDeletionGracePeriodSeconds != nil {
        gracePeriodSeconds := *pod.Spec.TerminationGracePeriodSeconds
        deleteOptions.GracePeriodSeconds = &gracePeriodSeconds
    }
    
    err := tm.client.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, deleteOptions)
    if err != nil && !apierrors.IsNotFound(err) {
        return err
    }
    
    tm.recorder.Eventf(pod, v1.EventTypeNormal, "TaintManagerEviction", "Evicted due to node %s", pod.Spec.NodeName)
    return nil
}
```

---

## Node Lease 机制

### 1. 节点租约更新

基于 Kubelet 的节点租约管理：

```go
// NodeLeaseController 节点租约控制器
type NodeLeaseController struct {
    client           clientset.Interface
    leaseClient      coordinationv1client.LeaseInterface
    holderIdentity   string
    leaseDuration    time.Duration
    renewInterval    time.Duration
    clock           clock.Clock
}

// Run 运行节点租约控制器
func (c *NodeLeaseController) Run(stopCh <-chan struct{}) {
    defer utilruntime.HandleCrash()
    klog.Infof("Starting NodeLeaseController")
    defer klog.Infof("Shutting down NodeLeaseController")

    // 立即更新一次租约
    if err := c.sync(); err != nil {
        klog.Errorf("unable to sync node lease: %v", err)
    }

    // 定期更新租约
    go wait.Until(func() {
        if err := c.sync(); err != nil {
            klog.Errorf("unable to sync node lease: %v", err)
        }
    }, c.renewInterval, stopCh)

    <-stopCh
}

// sync 同步节点租约
func (c *NodeLeaseController) sync() error {
    lease := &coordinationv1.Lease{
        ObjectMeta: metav1.ObjectMeta{
            Name:      c.holderIdentity,
            Namespace: v1.NamespaceNodeLease,
        },
        Spec: coordinationv1.LeaseSpec{
            HolderIdentity:       pointer.StringPtr(c.holderIdentity),
            LeaseDurationSeconds: pointer.Int32Ptr(int32(c.leaseDuration.Seconds())),
            RenewTime:           &metav1.MicroTime{Time: c.clock.Now()},
        },
    }

    // 尝试创建租约
    _, err := c.leaseClient.Create(context.TODO(), lease, metav1.CreateOptions{})
    if err == nil {
        return nil
    }
    if !apierrors.IsAlreadyExists(err) {
        return err
    }

    // 租约已存在，更新续约时间
    _, err = c.leaseClient.Patch(context.TODO(), c.holderIdentity, types.StrategicMergePatchType,
        []byte(fmt.Sprintf(`{"spec":{"renewTime":"%s"}}`, lease.Spec.RenewTime.Time.Format(time.RFC3339Nano))),
        metav1.PatchOptions{})
    
    return err
}
```

### 2. 租约状态检查

```go
// isNodeLeaseExpired 检查节点租约是否过期
func (nc *Controller) isNodeLeaseExpired(lease *coordinationv1.Lease) bool {
    if lease.Spec.RenewTime == nil {
        return true
    }
    
    // 计算租约过期时间
    expireTime := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
    return nc.now().After(expireTime)
}
```

---

## 性能优化与监控

### 1. 控制器性能配置

```yaml
# kube-controller-manager 配置优化
apiVersion: v1
kind: ConfigMap
metadata:
  name: node-controller-config
data:
  config.yaml: |
    # 节点监控配置
    nodeMonitorPeriod: "5s"                    # 节点监控周期
    nodeMonitorGracePeriod: "40s"              # 节点监控宽限期
    nodeStartupGracePeriod: "60s"              # 节点启动宽限期
    
    # Pod驱逐配置  
    podEvictionTimeout: "5m0s"                 # Pod驱逐超时
    evictionLimiterQPS: 0.1                    # 正常驱逐速率
    secondaryEvictionLimiterQPS: 0.01          # 降级驱逐速率
    
    # 大集群阈值
    largeClusterThreshold: 50                  # 大集群节点阈值
    unhealthyThreshold: 0.55                   # 不健康节点比例阈值
```

### 2. 监控指标

```go
// 关键性能指标
var (
    // 节点状态更新延迟
    nodeStatusUpdateDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "node_controller_node_status_update_duration_seconds",
            Help: "Duration of node status update operations",
        },
        []string{"result"},
    )
    
    // Pod驱逐操作计数
    podEvictionTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "node_controller_pod_eviction_total", 
            Help: "Total number of pod evictions",
        },
        []string{"zone", "reason"},
    )
    
    // 区域状态变化计数
    zoneStateTransitionTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "node_controller_zone_state_transition_total",
            Help: "Total number of zone state transitions", 
        },
        []string{"zone", "from_state", "to_state"},
    )
    
    // 不健康节点数量
    unhealthyNodesGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "node_controller_unhealthy_nodes",
            Help: "Number of unhealthy nodes per zone",
        },
        []string{"zone"},
    )
)
```

### 3. 告警规则配置

```yaml
# Prometheus告警规则
groups:
- name: node-controller
  rules:
  - alert: NodeControllerDown
    expr: up{job="kube-controller-manager"} == 0
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "Node Controller is down"
      description: "Node Controller has been down for more than 5 minutes"
      
  - alert: HighNodeEvictionRate
    expr: rate(node_controller_pod_eviction_total[5m]) > 1
    for: 2m
    labels:
      severity: warning
    annotations:
      summary: "High pod eviction rate detected" 
      description: "Pod eviction rate is {{ $value }} per second"
      
  - alert: ZonePartialDisruption
    expr: node_controller_zone_state{state="partial_disruption"} > 0
    for: 1m
    labels:
      severity: warning
    annotations:
      summary: "Zone {{ $labels.zone }} is in partial disruption"
      description: "Zone {{ $labels.zone }} has partial disruption for more than 1 minute"
      
  - alert: ZoneFullDisruption  
    expr: node_controller_zone_state{state="full_disruption"} > 0
    for: 30s
    labels:
      severity: critical
    annotations:
      summary: "Zone {{ $labels.zone }} is in full disruption"
      description: "Zone {{ $labels.zone }} has full disruption"
```

---

## 故障排除与调试

### 1. 常见问题诊断

#### 节点状态问题

```bash
# 检查节点状态
kubectl get nodes -o wide
kubectl describe node <node-name>

# 查看节点状态条件详情
kubectl get nodes -o custom-columns=NAME:.metadata.name,STATUS:.status.conditions[?(@.type==\"Ready\")].status,REASON:.status.conditions[?(@.type==\"Ready\")].reason

# 检查节点租约状态
kubectl get lease -n kube-node-lease
kubectl describe lease <node-name> -n kube-node-lease
```

#### 污点和容忍度问题

```bash
# 检查节点污点
kubectl describe node <node-name> | grep -A 5 "Taints:"

# 查看Pod容忍度配置
kubectl get pod <pod-name> -o yaml | grep -A 10 "tolerations:"

# 检查被驱逐的Pod
kubectl get events --all-namespaces | grep -i evict
kubectl get pods --all-namespaces | grep Evicted
```

#### 控制器日志分析

```bash
# 查看node-controller日志
kubectl logs -n kube-system <kube-controller-manager-pod> | grep -i "node.*controller"

# 查看关键事件
kubectl get events --sort-by=.metadata.creationTimestamp | grep -i node

# 检查控制器状态
kubectl get componentstatus
```

### 2. 调试脚本

```bash
#!/bin/bash
# node-controller-debug.sh - Node Controller调试脚本

echo "=== Node Controller Health Check ==="

echo "1. Node Status Overview:"
kubectl get nodes -o custom-columns=NAME:.metadata.name,STATUS:.status.conditions[?(@.type==\"Ready\")].status,REASON:.status.conditions[?(@.type==\"Ready\")].reason,LAST-HEARTBEAT:.status.conditions[?(@.type==\"Ready\")].lastHeartbeatTime

echo -e "\n2. Node Taints:"
for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
    echo "Node: $node"
    kubectl get node $node -o jsonpath='{.spec.taints}' | jq -r '.[]? | "  \(.key)=\(.value):\(.effect)"' 2>/dev/null || echo "  No taints"
    echo ""
done

echo -e "\n3. Node Leases Status:"
kubectl get leases -n kube-node-lease -o custom-columns=NAME:.metadata.name,HOLDER:.spec.holderIdentity,RENEW-TIME:.spec.renewTime

echo -e "\n4. Evicted Pods:"
kubectl get pods --all-namespaces | grep Evicted | wc -l
echo "Recent eviction events:"
kubectl get events --all-namespaces | grep -i evicted | tail -5

echo -e "\n5. Controller Manager Logs (last 20 lines):"
kubectl logs -n kube-system -l component=kube-controller-manager --tail=20 | grep -i "node.*controller"

echo -e "\n6. Zone Distribution:"
kubectl get nodes -o custom-columns=NAME:.metadata.name,ZONE:.metadata.labels.topology\\.kubernetes\\.io/zone,STATUS:.status.conditions[?(@.type==\"Ready\")].status | sort -k2
```

### 3. 故障恢复流程

#### 节点假死问题

```bash
# 1. 检查节点实际状态
ssh <node-ip> "systemctl status kubelet"
ssh <node-ip> "journalctl -u kubelet -f"

# 2. 重启kubelet服务
ssh <node-ip> "systemctl restart kubelet"

# 3. 检查网络连通性
ssh <node-ip> "ping -c 3 <api-server-ip>"

# 4. 手动移除污点（如需要）
kubectl taint nodes <node-name> node.kubernetes.io/not-ready:NoExecute-
kubectl taint nodes <node-name> node.kubernetes.io/unreachable:NoExecute-
```

#### 大量Pod被驱逐

```bash
# 1. 检查区域状态
kubectl get events | grep -i "zone.*disruption"

# 2. 临时调整驱逐速率
kubectl patch deployment kube-controller-manager -n kube-system -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-controller-manager","command":["kube-controller-manager","--eviction-limiter-qps=0.01"]}]}}}}'

# 3. 检查被驱逐Pod的PDB
kubectl get pdb --all-namespaces

# 4. 重新调度关键Pod
kubectl delete pod <evicted-pod-name> -n <namespace>
```

---

## 总结

### 🔑 **核心要点**

1. **节点生命周期管理**：Node Controller 是 Kubernetes 集群节点健康管理的核心组件，负责监控、维护和管理所有节点的生命周期

2. **智能污点管理**：基于节点状态自动添加和移除污点，配合 Pod 容忍度实现精细化的调度和驱逐控制

3. **区域感知保护**：通过区域状态感知和速率限制，防止大规模节点故障时的连锁反应

4. **多维度健康检测**：结合节点状态条件、租约机制和心跳监控，提供全面的节点健康状态评估

### 🏆 **最佳实践**

- **合理配置监控参数**：根据集群规模调整监控周期、宽限期和驱逐速率
- **完善监控告警**：建立全面的节点健康监控和及时的故障告警机制
- **优雅处理容忍度**：为应用Pod配置合适的污点容忍度以提高可用性
- **区域规划设计**：合理规划节点的区域分布以提高故障容错能力

### 🎯 **运维建议**

- **定期健康检查**：建立节点健康状态的定期巡检和维护机制
- **故障演练**：定期进行节点故障场景的应急响应演练
- **容量规划**：根据业务增长合理规划节点容量和区域分布
- **版本升级策略**：制定节点和 Kubernetes 版本的滚动升级策略

Node Controller 作为 Kubernetes 集群稳定性的重要保障，其正确配置和运维对于保证生产环境的高可用性至关重要。理解其工作原理和机制，有助于构建更加可靠和健壮的 Kubernetes 集群。
