# Kubernetes Scheduler 调度器架构与原理深度解读

## 目录

1. [概述](#概述)
2. [调度器核心概念](#调度器核心概念)
3. [调度器整体架构图](#调度器整体架构图)
4. [调度流程详解](#调度流程详解)
5. [调度器框架插件系统](#调度器框架插件系统)
6. [内置插件深度分析](#内置插件深度分析)
7. [调度队列系统](#调度队列系统)
8. [调度器性能优化](#调度器性能优化)
9. [监控和指标](#监控和指标)
10. [故障排除与调试](#故障排除与调试)
11. [总结](#总结)

---

## 概述

Kubernetes Scheduler 是集群的核心组件之一，负责将 Pod 分配到合适的节点上运行。调度器通过一系列复杂的算法和策略，综合考虑资源需求、亲和性规则、污点容忍、拓扑约束等因素，为每个 Pod 选择最佳的运行节点。本文档基于 Kubernetes 源码深入分析调度器的架构设计、实现原理和最佳实践。

### 核心特性

- **框架化设计**：可插拔的框架架构，支持自定义调度插件
- **多阶段调度**：包含过滤、打分、绑定等多个阶段的调度流程
- **性能优化**：支持并行处理、节点采样、缓存优化等性能优化策略
- **可扩展性**：支持多调度器配置、自定义扩展器和调度策略

---

## 调度器核心概念

### 1. 调度器主要组件

基于源码 `pkg/scheduler/scheduler.go`：

```go
// Scheduler 主结构体
type Scheduler struct {
    // 调度器缓存，存储节点和 Pod 信息
    Cache internalcache.Cache

    // 外部扩展器，用于与第三方调度器集成
    Extenders []framework.Extender

    // 获取下一个待调度 Pod 的函数
    NextPod func() (*framework.QueuedPodInfo, error)

    // 调度失败处理函数
    FailureHandler FailureHandlerFn

    // 核心调度函数
    SchedulePod func(ctx context.Context, fwk framework.Framework, state *framework.CycleState, pod *v1.Pod) (ScheduleResult, error)

    // 停止信号
    StopEverything <-chan struct{}

    // 调度队列
    SchedulingQueue internalqueue.SchedulingQueue

    // 调度配置文件
    Profiles profile.Map

    // Kubernetes 客户端
    client clientset.Interface

    // 节点信息快照
    nodeInfoSnapshot *internalcache.Snapshot

    // 参与调度的节点百分比
    percentageOfNodesToScore int32

    // 下一个开始节点索引（负载均衡）
    nextStartNodeIndex int

    // 日志器
    logger klog.Logger
}
```

### 2. 调度结果结构

```go
// ScheduleResult 调度结果
type ScheduleResult struct {
    // 建议的主机节点
    SuggestedHost string
    // 评估的节点数量
    EvaluatedNodes int
    // 可行的节点数量
    FeasibleNodes int
    // 提名信息（抢占时使用）
    nominatingInfo *framework.NominatingInfo
}
```

### 3. 调度框架状态

```go
// CycleState 调度周期状态
type CycleState struct {
    // 存储插件状态的映射
    storage sync.Map
    // 是否记录插件指标
    recordPluginMetrics bool
}
```

---

## 调度器整体架构图

上方的架构图展示了 Kubernetes 调度器的完整架构，包括：

1. **主要组件**：Scheduler、Framework Runtime、Plugin Registry 等
2. **队列系统**：Active Queue、Backoff Queue、Unschedulable Pods
3. **插件生态**：内置插件、自定义插件、扩展器
4. **监控系统**：指标收集、Prometheus 集成

---

## 调度流程详解

### 1. 调度主循环

基于源码 `pkg/scheduler/schedule_one.go` 中的 `scheduleOne` 函数：

```go
func (sched *Scheduler) scheduleOne(ctx context.Context) {
    logger := klog.FromContext(ctx)
    
    // 1. 从调度队列获取下一个 Pod
    podInfo, err := sched.NextPod()
    if err != nil {
        logger.Error(err, "Error while retrieving next pod from scheduling queue")
        return
    }
    
    if podInfo == nil || podInfo.Pod == nil {
        return
    }

    pod := podInfo.Pod
    logger.V(4).Info("About to try and schedule pod", "pod", klog.KObj(pod))

    // 2. 获取对应的调度框架
    fwk, err := sched.frameworkForPod(pod)
    if err != nil {
        logger.Error(err, "Error occurred")
        return
    }
    
    // 3. 检查是否应该跳过此 Pod 的调度
    if sched.skipPodSchedule(ctx, fwk, pod) {
        return
    }

    logger.V(3).Info("Attempting to schedule pod", "pod", klog.KObj(pod))

    // 4. 初始化调度状态
    start := time.Now()
    state := framework.NewCycleState()
    state.SetRecordPluginMetrics(rand.Intn(100) < pluginMetricsSamplePercent)

    podsToActivate := framework.NewPodsToActivate()
    state.Write(framework.PodsToActivateKey, podsToActivate)

    // 5. 执行调度周期
    schedulingCycleCtx, cancel := context.WithCancel(ctx)
    defer cancel()

    scheduleResult, assumedPodInfo, status := sched.schedulingCycle(schedulingCycleCtx, state, fwk, podInfo, start, podsToActivate)
    if !status.IsSuccess() {
        sched.FailureHandler(schedulingCycleCtx, fwk, assumedPodInfo, status, scheduleResult.nominatingInfo, start)
        return
    }

    // 6. 异步执行绑定周期
    go func() {
        bindingCycleCtx, cancel := context.WithCancel(ctx)
        defer cancel()

        status := sched.bindingCycle(bindingCycleCtx, state, fwk, scheduleResult, assumedPodInfo, start, podsToActivate)
        if !status.IsSuccess() {
            sched.handleBindingCycleError(bindingCycleCtx, fwk, assumedPodInfo, status, scheduleResult.nominatingInfo, start)
        }
    }()
}
```

### 2. 调度周期详解

调度周期序列图展示了完整的调度流程，包括 PreFilter、Filter、PostFilter、PreScore、Score、NormalizeScore 等阶段。

#### 调度周期实现

```go
func (sched *Scheduler) schedulingCycle(
    ctx context.Context,
    state *framework.CycleState,
    fwk framework.Framework,
    podInfo *framework.QueuedPodInfo,
    start time.Time,
    podsToActivate *framework.PodsToActivate,
) (ScheduleResult, *framework.QueuedPodInfo, *framework.Status) {
    
    logger := klog.FromContext(ctx)
    pod := podInfo.Pod
    
    // 执行核心调度算法
    scheduleResult, err := sched.SchedulePod(ctx, fwk, state, pod)
    if err != nil {
        if err == ErrNoNodesAvailable {
            status := framework.NewStatus(framework.UnschedulableAndUnresolvable).WithError(err)
            return ScheduleResult{nominatingInfo: clearNominatedNode}, podInfo, status
        }

        // 处理适配错误
        fitError, ok := err.(*framework.FitError)
        if !ok {
            logger.Error(err, "Error selecting node for pod", "pod", klog.KObj(pod))
            return ScheduleResult{nominatingInfo: clearNominatedNode}, podInfo, framework.AsStatus(err)
        }

        // 如果没有合适的节点，尝试抢占
        if !fwk.HasPostFilterPlugins() {
            logger.V(3).Info("No PostFilter plugins are registered, so no preemption will be performed")
            return ScheduleResult{}, podInfo, framework.NewStatus(framework.Unschedulable).WithError(err)
        }

        // 运行 PostFilter 插件进行抢占
        result, status := fwk.RunPostFilterPlugins(ctx, state, pod, fitError.Diagnosis.NodeToStatusMap)
        msg := status.Message()
        fitError.Diagnosis.PostFilterMsg = msg
        
        if status.Code() == framework.Error {
            logger.Error(nil, "Status after running PostFilter plugins for pod", "pod", klog.KObj(pod), "status", msg)
        } else {
            logger.V(5).Info("Status after running PostFilter plugins for pod", "pod", klog.KObj(pod), "status", msg)
        }

        var nominatingInfo *framework.NominatingInfo
        if result != nil {
            nominatingInfo = result.NominatingInfo
        }
        return ScheduleResult{nominatingInfo: nominatingInfo}, podInfo, framework.NewStatus(status.Code()).WithError(err)
    }

    // 执行 Assume 操作
    assumedPodInfo := podInfo.DeepCopy()
    assumedPod := assumedPodInfo.Pod
    err = sched.assume(logger, assumedPod, scheduleResult.SuggestedHost)
    if err != nil {
        return ScheduleResult{nominatingInfo: clearNominatedNode}, assumedPodInfo, framework.AsStatus(err)
    }

    // 激活等待的 Pod
    if len(podsToActivate.Map) != 0 {
        sched.SchedulingQueue.Activate(logger, podsToActivate.Map)
        podsToActivate.Map = make(map[string]*v1.Pod)
    }

    return scheduleResult, assumedPodInfo, nil
}
```

### 3. 核心调度算法

```go
func (sched *Scheduler) schedulePod(ctx context.Context, fwk framework.Framework, state *framework.CycleState, pod *v1.Pod) (result ScheduleResult, err error) {
    trace := utiltrace.New("Scheduling", utiltrace.Field{Key: "namespace", Value: pod.Namespace}, utiltrace.Field{Key: "name", Value: pod.Name})
    defer trace.LogIfLong(100 * time.Millisecond)
    
    // 1. 更新节点信息快照
    if err := sched.Cache.UpdateSnapshot(klog.FromContext(ctx), sched.nodeInfoSnapshot); err != nil {
        return result, err
    }
    trace.Step("Snapshotting scheduler cache and node infos done")

    if sched.nodeInfoSnapshot.NumNodes() == 0 {
        return result, ErrNoNodesAvailable
    }

    // 2. 查找合适的节点
    feasibleNodes, diagnosis, err := sched.findNodesThatFitPod(ctx, fwk, state, pod)
    if err != nil {
        return result, err
    }
    trace.Step("Computing predicates done")

    // 3. 如果没有合适的节点，返回错误
    if len(feasibleNodes) == 0 {
        return result, &framework.FitError{
            Pod:         pod,
            NumAllNodes: sched.nodeInfoSnapshot.NumNodes(),
            Diagnosis:   diagnosis,
        }
    }

    // 4. 如果只有一个节点，直接返回
    if len(feasibleNodes) == 1 {
        return ScheduleResult{
            SuggestedHost:  feasibleNodes[0].Name,
            EvaluatedNodes: 1 + len(diagnosis.NodeToStatusMap),
            FeasibleNodes:  1,
        }, nil
    }

    // 5. 对节点进行打分
    priorityList, err := prioritizeNodes(ctx, sched.Extenders, fwk, state, pod, feasibleNodes)
    if err != nil {
        return result, err
    }

    // 6. 选择最高分的节点
    host, _, err := selectHost(priorityList, numberOfHighestScoredNodesToReport)
    trace.Step("Prioritizing done")

    return ScheduleResult{
        SuggestedHost:  host,
        EvaluatedNodes: len(feasibleNodes) + len(diagnosis.NodeToStatusMap),
        FeasibleNodes:  len(feasibleNodes),
    }, err
}
```

### 4. 节点过滤实现

```go
func (sched *Scheduler) findNodesThatFitPod(ctx context.Context, fwk framework.Framework, state *framework.CycleState, pod *v1.Pod) ([]*v1.Node, framework.Diagnosis, error) {
    logger := klog.FromContext(ctx)
    diagnosis := framework.Diagnosis{
        NodeToStatusMap: make(framework.NodeToStatusMap),
    }

    allNodes, err := sched.nodeInfoSnapshot.NodeInfos().List()
    if err != nil {
        return nil, diagnosis, err
    }
    
    // 1. 运行 PreFilter 插件
    preRes, s := fwk.RunPreFilterPlugins(ctx, state, pod)
    if !s.IsSuccess() {
        if !s.IsUnschedulable() {
            return nil, diagnosis, s.AsError()
        }
        
        // 所有节点都不符合 PreFilter 条件
        for _, n := range allNodes {
            diagnosis.NodeToStatusMap[n.Node().Name] = s
        }

        diagnosis.PreFilterMsg = s.Message()
        logger.V(5).Info("Status after running PreFilter plugins for pod", "pod", klog.KObj(pod), "status", s.Message())
        diagnosis.AddPluginStatus(s)
        return nil, diagnosis, nil
    }

    // 2. 检查提名节点
    if len(pod.Status.NominatedNodeName) > 0 {
        feasibleNodes, err := sched.evaluateNominatedNode(ctx, pod, fwk, state, diagnosis)
        if err != nil {
            logger.Error(err, "Evaluation failed on nominated node", "pod", klog.KObj(pod), "node", pod.Status.NominatedNodeName)
        }
        
        if len(feasibleNodes) != 0 {
            return feasibleNodes, diagnosis, nil
        }
    }

    // 3. 过滤所有节点
    nodes := allNodes
    feasibleNodes, err := sched.findNodesThatPassFilters(ctx, fwk, state, pod, &diagnosis, nodes)
    if err != nil {
        return nil, diagnosis, err
    }

    // 4. 运行外部扩展器过滤
    feasibleNodes, err = findNodesThatPassExtenders(ctx, sched.Extenders, pod, feasibleNodes, diagnosis.NodeToStatusMap)
    if err != nil {
        return nil, diagnosis, err
    }

    return feasibleNodes, diagnosis, nil
}
```

### 5. 绑定周期实现

```go
func (sched *Scheduler) bindingCycle(
    ctx context.Context,
    state *framework.CycleState,
    fwk framework.Framework,
    scheduleResult ScheduleResult,
    assumedPodInfo *framework.QueuedPodInfo,
    start time.Time,
    podsToActivate *framework.PodsToActivate) *framework.Status {
    
    logger := klog.FromContext(ctx)
    assumedPod := assumedPodInfo.Pod

    // 1. 运行 Permit 插件
    if status := fwk.WaitOnPermit(ctx, assumedPod); !status.IsSuccess() {
        if status.IsUnschedulable() {
            fitErr := &framework.FitError{
                NumAllNodes: 1,
                Pod:         assumedPodInfo.Pod,
                Diagnosis: framework.Diagnosis{
                    NodeToStatusMap:      framework.NodeToStatusMap{scheduleResult.SuggestedHost: status},
                    UnschedulablePlugins: sets.New(status.FailedPlugin()),
                },
            }
            return framework.NewStatus(status.Code()).WithError(fitErr)
        }
        return status
    }

    // 2. 运行 PreBind 插件
    if status := fwk.RunPreBindPlugins(ctx, state, assumedPod, scheduleResult.SuggestedHost); !status.IsSuccess() {
        return status
    }

    // 3. 运行 Bind 插件
    if status := sched.bind(ctx, fwk, assumedPod, scheduleResult.SuggestedHost, state); !status.IsSuccess() {
        return status
    }

    // 4. 记录成功绑定日志
    logger.V(2).Info("Successfully bound pod to node", "pod", klog.KObj(assumedPod), "node", scheduleResult.SuggestedHost, "evaluatedNodes", scheduleResult.EvaluatedNodes, "feasibleNodes", scheduleResult.FeasibleNodes)
    
    // 5. 记录指标
    metrics.PodScheduled(fwk.ProfileName(), metrics.SinceInSeconds(start))
    metrics.PodSchedulingAttempts.Observe(float64(assumedPodInfo.Attempts))

    // 6. 运行 PostBind 插件
    fwk.RunPostBindPlugins(ctx, state, assumedPod, scheduleResult.SuggestedHost)

    return nil
}
```

---

## 调度器框架插件系统

### 1. 插件扩展点架构

插件扩展点架构图展示了调度器框架的完整扩展点系统，包括：

- **队列阶段**：PreEnqueue、QueueSort
- **调度周期**：PreFilter、Filter、PostFilter、PreScore、Score、NormalizeScore
- **绑定周期**：Reserve、Permit、PreBind、Bind、PostBind、Unreserve

### 2. 框架接口定义

基于源码 `pkg/scheduler/framework/interface.go`：

```go
// Framework 调度框架接口
type Framework interface {
    Handle

    // PreEnqueue 插件
    PreEnqueuePlugins() []PreEnqueuePlugin
    
    // 队列扩展
    EnqueueExtensions() []EnqueueExtensions
    
    // 队列排序函数
    QueueSortFunc() LessFunc

    // 运行各阶段插件
    RunPreFilterPlugins(ctx context.Context, state *CycleState, pod *v1.Pod) (*PreFilterResult, *Status)
    RunPostFilterPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, filteredNodeStatusMap NodeToStatusMap) (*PostFilterResult, *Status)
    RunPreBindPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status
    RunPostBindPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string)
    RunReservePluginsReserve(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status
    RunReservePluginsUnreserve(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string)
    RunPermitPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status
    WaitOnPermit(ctx context.Context, pod *v1.Pod) *Status
    RunBindPlugins(ctx context.Context, state *CycleState, pod *v1.Pod, nodeName string) *Status

    // 查询函数
    HasFilterPlugins() bool
    HasPostFilterPlugins() bool
    HasScorePlugins() bool
    ListPlugins() *config.Plugins
    ProfileName() string
    PercentageOfNodesToScore() *int32
    SetPodNominator(nominator PodNominator)
}
```

### 3. 插件接口定义

```go
// 基础插件接口
type Plugin interface {
    Name() string
}

// PreFilter 插件接口
type PreFilterPlugin interface {
    Plugin
    PreFilter(ctx context.Context, state *CycleState, p *v1.Pod) (*PreFilterResult, *Status)
    PreFilterExtensions() PreFilterExtensions
}

// Filter 插件接口
type FilterPlugin interface {
    Plugin
    Filter(ctx context.Context, state *CycleState, pod *v1.Pod, nodeInfo *NodeInfo) *Status
}

// PostFilter 插件接口（抢占）
type PostFilterPlugin interface {
    Plugin
    PostFilter(ctx context.Context, state *CycleState, pod *v1.Pod, filteredNodeStatusMap NodeToStatusMap) (*PostFilterResult, *Status)
}

// Score 插件接口
type ScorePlugin interface {
    Plugin
    Score(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string) (int64, *Status)
    ScoreExtensions() ScoreExtensions
}

// Bind 插件接口
type BindPlugin interface {
    Plugin
    Bind(ctx context.Context, state *CycleState, p *v1.Pod, nodeName string) *Status
}
```

### 4. 框架运行时实现

基于源码 `pkg/scheduler/framework/runtime/framework.go`：

```go
// frameworkImpl 框架实现
type frameworkImpl struct {
    registry             Registry
    snapshotSharedLister framework.SharedLister
    waitingPods          *waitingPodsMap
    scorePluginWeight    map[string]int
    
    // 各阶段插件列表
    preEnqueuePlugins    []framework.PreEnqueuePlugin
    enqueueExtensions    []framework.EnqueueExtensions
    queueSortPlugins     []framework.QueueSortPlugin
    preFilterPlugins     []framework.PreFilterPlugin
    filterPlugins        []framework.FilterPlugin
    postFilterPlugins    []framework.PostFilterPlugin
    preScorePlugins      []framework.PreScorePlugin
    scorePlugins         []framework.ScorePlugin
    reservePlugins       []framework.ReservePlugin
    preBindPlugins       []framework.PreBindPlugin
    bindPlugins          []framework.BindPlugin
    postBindPlugins      []framework.PostBindPlugin
    permitPlugins        []framework.PermitPlugin

    // 客户端和配置
    clientSet       clientset.Interface
    kubeConfig      *restclient.Config
    eventRecorder   events.EventRecorder
    informerFactory informers.SharedInformerFactory
    logger          klog.Logger

    // 指标和配置
    metricsRecorder          *metrics.MetricAsyncRecorder
    profileName              string
    percentageOfNodesToScore *int32
    
    // 扩展器和提名器
    extenders []framework.Extender
    framework.PodNominator
    
    // 并行处理器
    parallelizer parallelize.Parallelizer
}
```

---

## 内置插件深度分析

### 1. NodeResourcesFit 插件

基于源码 `pkg/scheduler/framework/plugins/noderesources/fit.go`：

```go
// Fit 节点资源适配插件
type Fit struct {
    ignoredResources                sets.Set[string]
    ignoredResourceGroups           sets.Set[string]
    enableInPlacePodVerticalScaling bool
    enableSidecarContainers         bool
    handle                          framework.Handle
    resourceAllocationScorer
}

// PreFilter 预过滤实现
func (f *Fit) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod) (*framework.PreFilterResult, *framework.Status) {
    cycleState.Write(preFilterStateKey, computePodResourceRequest(pod))
    return nil, nil
}

// Filter 过滤实现
func (f *Fit) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
    if !f.enableSidecarContainers && hasRestartableInitContainer(pod) {
        return framework.NewStatus(framework.UnschedulableAndUnresolvable, "Pod has a restartable init container and the SidecarContainers feature is disabled")
    }

    s, err := getPreFilterState(cycleState)
    if err != nil {
        return framework.AsStatus(err)
    }

    // 检查资源是否充足
    insufficientResources := fitsRequest(s, nodeInfo, f.ignoredResources, f.ignoredResourceGroups)

    if len(insufficientResources) != 0 {
        failureReasons := make([]string, 0, len(insufficientResources))
        for i := range insufficientResources {
            failureReasons = append(failureReasons, insufficientResources[i].Reason)
        }
        return framework.NewStatus(framework.Unschedulable, failureReasons...)
    }
    return nil
}

// Score 打分实现
func (f *Fit) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
    nodeInfo, err := f.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
    if err != nil {
        return 0, framework.AsStatus(fmt.Errorf("getting node %q from Snapshot: %w", nodeName, err))
    }

    s, err := getPreScoreState(state)
    if err != nil {
        s = &preScoreState{
            podRequests: f.calculatePodResourceRequestList(pod, f.resources),
        }
    }

    return f.score(ctx, pod, nodeInfo, s.podRequests)
}
```

### 2. NodeAffinity 插件

基于源码 `pkg/scheduler/framework/plugins/nodeaffinity/node_affinity.go`：

```go
// NodeAffinity 节点亲和性插件
type NodeAffinity struct {
    handle              framework.Handle
    addedNodeSelector   *nodeaffinity.NodeSelector
    addedPrefSchedTerms *nodeaffinity.PreferredSchedulingTerms
}

// PreFilter 预过滤实现
func (pl *NodeAffinity) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod) (*framework.PreFilterResult, *framework.Status) {
    affinity := pod.Spec.Affinity
    noNodeAffinity := (affinity == nil || affinity.NodeAffinity == nil || affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil)
    
    if noNodeAffinity && pl.addedNodeSelector == nil && pod.Spec.NodeSelector == nil {
        return nil, framework.NewStatus(framework.Skip)
    }

    state := &preFilterState{requiredNodeSelectorAndAffinity: nodeaffinity.GetRequiredNodeAffinity(pod)}
    cycleState.Write(preFilterStateKey, state)

    if noNodeAffinity || len(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
        return nil, nil
    }

    // 检查是否有特定节点亲和性并返回
    terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
    var nodeNames sets.Set[string]
    
    for _, t := range terms {
        var termNodeNames sets.Set[string]
        for _, r := range t.MatchFields {
            if r.Key == metav1.ObjectNameField && r.Operator == v1.NodeSelectorOpIn {
                s := sets.New(r.Values...)
                if termNodeNames == nil {
                    termNodeNames = s
                } else {
                    termNodeNames = termNodeNames.Intersection(s)
                }
            }
        }
        if termNodeNames == nil {
            return nil, nil
        }
        nodeNames = nodeNames.Union(termNodeNames)
    }
    
    if nodeNames != nil && len(nodeNames) == 0 {
        return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, errReasonConflict)
    } else if len(nodeNames) > 0 {
        return &framework.PreFilterResult{NodeNames: nodeNames}, nil
    }
    
    return nil, nil
}

// Filter 过滤实现
func (pl *NodeAffinity) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
    node := nodeInfo.Node()

    if pl.addedNodeSelector != nil && !pl.addedNodeSelector.Match(node) {
        return framework.NewStatus(framework.UnschedulableAndUnresolvable, errReasonEnforced)
    }

    s, err := getPreFilterState(state)
    if err != nil {
        s = &preFilterState{requiredNodeSelectorAndAffinity: nodeaffinity.GetRequiredNodeAffinity(pod)}
    }

    match, _ := s.requiredNodeSelectorAndAffinity.Match(node)
    if !match {
        return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonPod)
    }

    return nil
}

// Score 打分实现
func (pl *NodeAffinity) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
    nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
    if err != nil {
        return 0, framework.AsStatus(fmt.Errorf("getting node %q from Snapshot: %w", nodeName, err))
    }

    node := nodeInfo.Node()
    var count int64
    
    if pl.addedPrefSchedTerms != nil {
        count += pl.addedPrefSchedTerms.Score(node)
    }

    s, err := getPreScoreState(state)
    if err != nil {
        preferredNodeAffinity, err := getPodPreferredNodeAffinity(pod)
        if err != nil {
            return 0, framework.AsStatus(err)
        }
        s = &preScoreState{preferredNodeAffinity: preferredNodeAffinity}
    }

    if s.preferredNodeAffinity != nil {
        count += s.preferredNodeAffinity.Score(node)
    }

    return count, nil
}
```

### 3. 插件注册机制

基于源码 `pkg/scheduler/framework/plugins/registry.go`：

```go
// NewInTreeRegistry 创建内置插件注册表
func NewInTreeRegistry() runtime.Registry {
    fts := plfeature.Features{
        EnableDynamicResourceAllocation:   feature.DefaultFeatureGate.Enabled(features.DynamicResourceAllocation),
        EnableReadWriteOncePod:           feature.DefaultFeatureGate.Enabled(features.ReadWriteOncePod),
        EnableVolumeCapacityPriority:     feature.DefaultFeatureGate.Enabled(features.VolumeCapacityPriority),
        EnableMinDomainsInPodTopologySpread: feature.DefaultFeatureGate.Enabled(features.MinDomainsInPodTopologySpread),
        EnableNodeInclusionPolicyInPodTopologySpread: feature.DefaultFeatureGate.Enabled(features.NodeInclusionPolicyInPodTopologySpread),
        EnableMatchLabelKeysInPodTopologySpread: feature.DefaultFeatureGate.Enabled(features.MatchLabelKeysInPodTopologySpread),
        EnablePodSchedulingReadiness:     feature.DefaultFeatureGate.Enabled(features.PodSchedulingReadiness),
        EnablePodDisruptionConditions:    feature.DefaultFeatureGate.Enabled(features.PodDisruptionConditions),
        EnableInPlacePodVerticalScaling:  feature.DefaultFeatureGate.Enabled(features.InPlacePodVerticalScaling),
        EnableSidecarContainers:          feature.DefaultFeatureGate.Enabled(features.SidecarContainers),
    }

    registry := runtime.Registry{
        // 动态资源管理
        dynamicresources.Name:                runtime.FactoryAdapter(fts, dynamicresources.New),
        // 镜像本地性
        imagelocality.Name:                   imagelocality.New,
        // 污点容忍
        tainttoleration.Name:                 tainttoleration.New,
        // 节点名称
        nodename.Name:                        nodename.New,
        // 节点端口
        nodeports.Name:                       nodeports.New,
        // 节点亲和性
        nodeaffinity.Name:                    nodeaffinity.New,
        // Pod 拓扑分布
        podtopologyspread.Name:               runtime.FactoryAdapter(fts, podtopologyspread.New),
        // 节点不可调度
        nodeunschedulable.Name:               nodeunschedulable.New,
        // 节点资源适配
        noderesources.Name:                   runtime.FactoryAdapter(fts, noderesources.NewFit),
        // 节点资源均衡分配
        noderesources.BalancedAllocationName: runtime.FactoryAdapter(fts, noderesources.NewBalancedAllocation),
        // 卷绑定
        volumebinding.Name:                   runtime.FactoryAdapter(fts, volumebinding.New),
        // 卷限制
        volumerestrictions.Name:              runtime.FactoryAdapter(fts, volumerestrictions.New),
        // 卷区域
        volumezone.Name:                      volumezone.New,
        // CSI 节点卷限制
        nodevolumelimits.CSIName:             runtime.FactoryAdapter(fts, nodevolumelimits.NewCSI),
        // EBS 卷限制
        nodevolumelimits.EBSName:             runtime.FactoryAdapter(fts, nodevolumelimits.NewEBS),
        // GCE PD 卷限制
        nodevolumelimits.GCEPDName:           runtime.FactoryAdapter(fts, nodevolumelimits.NewGCEPD),
        // Azure 磁盘限制
        nodevolumelimits.AzureDiskName:       runtime.FactoryAdapter(fts, nodevolumelimits.NewAzureDisk),
        // Cinder 卷限制
        nodevolumelimits.CinderName:          runtime.FactoryAdapter(fts, nodevolumelimits.NewCinder),
        // Inter-Pod 亲和性
        interpodaffinity.Name:                interpodaffinity.New,
        // 队列排序
        queuesort.Name:                       queuesort.New,
        // 默认绑定器
        defaultbinder.Name:                   defaultbinder.New,
        // 默认抢占
        defaultpreemption.Name:               runtime.FactoryAdapter(fts, defaultpreemption.New),
        // 调度门控
        schedulinggates.Name:                 runtime.FactoryAdapter(fts, schedulinggates.New),
    }

    return registry
}
```

---

## 调度队列系统

### 1. 调度队列架构

基于源码 `pkg/scheduler/internal/queue/scheduling_queue.go`：

```go
// SchedulingQueue 调度队列接口
type SchedulingQueue interface {
    framework.PodNominator
    
    // 基本队列操作
    Add(logger klog.Logger, pod *v1.Pod) error
    Activate(logger klog.Logger, pods map[string]*v1.Pod)
    AddUnschedulableIfNotPresent(logger klog.Logger, pod *framework.QueuedPodInfo, podSchedulingCycle int64) error
    SchedulingCycle() int64
    Pop() (*framework.QueuedPodInfo, error)
    Done(types.UID)
    Update(logger klog.Logger, oldPod, newPod *v1.Pod) error
    Delete(pod *v1.Pod) error
    
    // 事件处理
    MoveAllToActiveOrBackoffQueue(logger klog.Logger, event framework.ClusterEvent, oldObj, newObj interface{}, preCheck PreEnqueueCheck)
    AssignedPodAdded(logger klog.Logger, pod *v1.Pod)
    AssignedPodUpdated(logger klog.Logger, oldPod, newPod *v1.Pod)
    PendingPods() ([]*v1.Pod, string)
    
    // 生命周期管理
    Run(logger klog.Logger)
    Close()
}
```

### 2. 优先级队列实现

```go
// PriorityQueue 优先级队列实现
type PriorityQueue struct {
    // 停止信号
    stop  chan struct{}
    clock clock.Clock
    
    // 队列状态锁
    lock sync.RWMutex
    cond sync.Cond
    
    // 三个子队列
    activeQ                           *heap.Heap
    podBackoffQ                       *heap.Heap
    unschedulablePods                 *UnschedulablePods
    moveRequestCycle                  int64
    
    // 正在处理的 Pod
    inFlightPods   map[types.UID]*list.Element
    inFlightEvents *list.List
    
    // 配置参数
    podInitialBackoffDuration         time.Duration
    podMaxBackoffDuration             time.Duration
    podMaxInUnschedulablePodsDuration time.Duration
    
    // 插件相关
    preEnqueuePluginMap               map[string][]framework.PreEnqueuePlugin
    queueingHintMap                   QueueingHintMapPerProfile
    
    // 指标记录器
    metricsRecorder                   metrics.MetricAsyncRecorder
    pluginMetricsSamplePercent        int
    
    // Pod 提名器
    nominator                         framework.PodNominator
    
    // 调度周期
    schedulingCycle                   int64
    moveRequestCycle                  int64
    
    // 队列提示功能开关
    isSchedulingQueueHintEnabled      bool
}
```

### 3. Pop 操作实现

```go
// Pop 从活跃队列中弹出下一个 Pod
func (p *PriorityQueue) Pop() (*framework.QueuedPodInfo, error) {
    p.lock.Lock()
    defer p.lock.Unlock()
    
    // 等待队列中有 Pod 或队列关闭
    for p.activeQ.Len() == 0 {
        if p.closed {
            klog.V(2).InfoS("Scheduling queue is closed")
            return nil, nil
        }
        p.cond.Wait()
    }
    
    // 弹出优先级最高的 Pod
    obj, err := p.activeQ.Pop()
    if err != nil {
        return nil, err
    }
    
    pInfo := obj.(*framework.QueuedPodInfo)
    pInfo.Attempts++
    p.schedulingCycle++
    
    // 记录正在处理的 Pod
    if p.isSchedulingQueueHintEnabled {
        p.inFlightPods[pInfo.Pod.UID] = p.inFlightEvents.PushBack(pInfo.Pod)
    }

    // 更新指标
    for plugin := range pInfo.UnschedulablePlugins.Union(pInfo.PendingPlugins) {
        metrics.UnschedulableReason(plugin, pInfo.Pod.Spec.SchedulerName).Dec()
    }
    pInfo.UnschedulablePlugins.Clear()
    pInfo.PendingPlugins.Clear()

    return pInfo, nil
}
```

### 4. 队列事件处理

```go
// MoveAllToActiveOrBackoffQueue 将 Pod 从不可调度队列移动到活跃队列或退避队列
func (p *PriorityQueue) MoveAllToActiveOrBackoffQueue(logger klog.Logger, event framework.ClusterEvent, oldObj, newObj interface{}, preCheck PreEnqueueCheck) {
    p.lock.Lock()
    defer p.lock.Unlock()
    
    unschedulablePods := make([]*framework.QueuedPodInfo, 0, len(p.unschedulablePods.podInfoMap))
    for _, pInfo := range p.unschedulablePods.podInfoMap {
        if preCheck != nil && !preCheck(pInfo.Pod) {
            continue
        }
        
        // 检查是否应该移动此 Pod
        if p.isSchedulingQueueHintEnabled {
            if shouldMove := p.shouldMoveAllToActiveOrBackoffQueue(logger, pInfo, event, oldObj, newObj); !shouldMove {
                continue
            }
        }
        
        unschedulablePods = append(unschedulablePods, pInfo)
    }
    
    p.movePodsToActiveOrBackoffQueue(logger, unschedulablePods, event, oldObj, newObj)
}
```

---

## 调度器性能优化

### 1. 性能优化架构

性能优化架构图展示了调度器的多层次优化策略：

- **算法优化**：快照机制、并行处理、节点采样、缓存优化
- **队列优化**：优先级队列、退避策略、不可调度池、队列提示
- **插件优化**：PreFilter 优化、增量更新、条件跳过、插件缓存

### 2. 节点采样优化

```go
// numFeasibleNodesToFind 计算需要查找的可行节点数量
func (sched *Scheduler) numFeasibleNodesToFind(percentageOfNodesToScore int32, numAllNodes int32) (numNodes int32) {
    if percentageOfNodesToScore == 0 {
        percentageOfNodesToScore = 50
    }

    if percentageOfNodesToScore == 100 {
        return numAllNodes
    }

    adaptivePercentage := sched.adaptivePercentageOfNodesToScore(numAllNodes)
    if adaptivePercentage > percentageOfNodesToScore {
        percentageOfNodesToScore = adaptivePercentage
    }

    numNodes = numAllNodes * percentageOfNodesToScore / 100
    if numNodes < minFeasibleNodesToFind {
        return minFeasibleNodesToFind
    }

    return numNodes
}

// adaptivePercentageOfNodesToScore 自适应节点采样百分比
func (sched *Scheduler) adaptivePercentageOfNodesToScore(numAllNodes int32) int32 {
    if numAllNodes <= 50 {
        return 100
    }
    if numAllNodes <= 300 {
        return 50
    }
    if numAllNodes <= 1000 {
        return 20
    }
    return 5
}
```

### 3. 并行化处理

```go
// findNodesThatPassFilters 并行过滤节点
func (sched *Scheduler) findNodesThatPassFilters(
    ctx context.Context,
    fwk framework.Framework,
    state *framework.CycleState,
    pod *v1.Pod,
    diagnosis *framework.Diagnosis,
    nodes []*framework.NodeInfo) ([]*v1.Node, error) {
    
    numAllNodes := len(nodes)
    numNodesToFind := sched.numFeasibleNodesToFind(fwk.PercentageOfNodesToScore(), int32(numAllNodes))

    feasibleNodes := make([]*v1.Node, numNodesToFind)

    if !fwk.HasFilterPlugins() {
        for i := range feasibleNodes {
            feasibleNodes[i] = nodes[(sched.nextStartNodeIndex+i)%numAllNodes].Node()
        }
        return feasibleNodes, nil
    }

    errCh := parallelize.NewErrorChannel()
    var statusesLock sync.Mutex
    var feasibleNodesLen int32
    ctx, cancel := context.WithCancel(ctx)
    defer cancel()
    
    // 并行检查节点
    checkNode := func(i int) {
        nodeInfo := nodes[(sched.nextStartNodeIndex+i)%numAllNodes]
        status := fwk.RunFilterPluginsWithNominatedPods(ctx, state, pod, nodeInfo)
        
        if status.Code() == framework.Error {
            errCh.SendErrorWithCancel(status.AsError(), cancel)
            return
        }
        
        if status.IsSuccess() {
            length := atomic.AddInt32(&feasibleNodesLen, 1)
            if length > numNodesToFind {
                cancel()
                atomic.AddInt32(&feasibleNodesLen, -1)
            } else {
                feasibleNodes[length-1] = nodeInfo.Node()
            }
        } else {
            statusesLock.Lock()
            diagnosis.NodeToStatusMap[nodeInfo.Node().Name] = status
            diagnosis.AddPluginStatus(status)
            statusesLock.Unlock()
        }
    }

    beginCheckNode := time.Now()
    statusCode := framework.Success
    defer func() {
        // 更新下次开始节点索引以实现负载均衡
        sched.nextStartNodeIndex = (sched.nextStartNodeIndex + len(feasibleNodes)) % numAllNodes
        metrics.FrameworkExtensionPointDuration.WithLabelValues(metrics.Filter, statusCode.String(), fwk.ProfileName()).Observe(metrics.SinceInSeconds(beginCheckNode))
    }()

    // 并行执行节点检查
    fwk.Parallelizer().Until(ctx, numAllNodes, checkNode, metrics.Filter)
    
    if err := errCh.ReceiveError(); err != nil {
        statusCode = framework.Error
        return nil, err
    }

    feasibleNodes = feasibleNodes[:feasibleNodesLen]
    if len(feasibleNodes) == 0 {
        statusCode = framework.Unschedulable
    }
    
    return feasibleNodes, nil
}
```

### 4. 缓存优化

```go
// assume 假设 Pod 已经绑定到节点（乐观并发控制）
func (sched *Scheduler) assume(logger klog.Logger, assumed *v1.Pod, host string) error {
    // 乐观地假设绑定会成功，并在后台发送到 apiserver
    // 如果绑定失败，调度器会立即释放分配给假设 Pod 的资源
    assumed.Spec.NodeName = host

    if err := sched.Cache.AssumePod(logger, assumed); err != nil {
        logger.Error(err, "Scheduler cache AssumePod failed")
        return err
    }
    
    // 如果 "assumed" 是提名的 Pod，应从内部缓存中删除它
    if sched.SchedulingQueue != nil {
        sched.SchedulingQueue.DeleteNominatedPodIfExists(assumed)
    }

    return nil
}
```

---

## 监控和指标

### 1. 核心监控指标

基于源码 `pkg/scheduler/metrics/metrics.go`：

```go
// 调度尝试次数
var scheduleAttempts = metrics.NewCounterVec(
    &metrics.CounterOpts{
        Subsystem:      SchedulerSubsystem,
        Name:           "schedule_attempts_total",
        Help:           "Number of attempts to schedule pods, by the result",
        StabilityLevel: metrics.STABLE,
    }, []string{"result", "profile"})

// 调度延迟
var schedulingLatency = metrics.NewHistogramVec(
    &metrics.HistogramOpts{
        Subsystem:      SchedulerSubsystem,
        Name:           "scheduling_attempt_duration_seconds",
        Help:           "Scheduling attempt latency in seconds",
        Buckets:        metrics.ExponentialBuckets(0.001, 2, 15),
        StabilityLevel: metrics.STABLE,
    }, []string{"result", "profile"})

// 调度算法延迟
var SchedulingAlgorithmLatency = metrics.NewHistogram(
    &metrics.HistogramOpts{
        Subsystem:      SchedulerSubsystem,
        Name:           "scheduling_algorithm_duration_seconds",
        Help:           "Scheduling algorithm latency in seconds",
        Buckets:        metrics.ExponentialBuckets(0.001, 2, 15),
        StabilityLevel: metrics.ALPHA,
    },
)

// 待调度 Pod 数量
var pendingPods = metrics.NewGaugeVec(
    &metrics.GaugeOpts{
        Subsystem:      SchedulerSubsystem,
        Name:           "pending_pods",
        Help:           "Number of pending pods, by the queue type",
        StabilityLevel: metrics.STABLE,
    }, []string{"queue"})

// 框架扩展点延迟
var FrameworkExtensionPointDuration = metrics.NewHistogramVec(
    &metrics.HistogramOpts{
        Subsystem: SchedulerSubsystem,
        Name:      "framework_extension_point_duration_seconds",
        Help:      "Latency for running all plugins of a specific extension point",
        Buckets:   metrics.ExponentialBuckets(0.0001, 2, 12),
        StabilityLevel: metrics.STABLE,
    },
    []string{"extension_point", "status", "profile"})

// 插件执行延迟
var PluginExecutionDuration = metrics.NewHistogramVec(
    &metrics.HistogramOpts{
        Subsystem: SchedulerSubsystem,
        Name:      "plugin_execution_duration_seconds",
        Help:      "Duration for running a plugin at a specific extension point",
        Buckets:   metrics.ExponentialBuckets(0.00001, 1.5, 20),
        StabilityLevel: metrics.ALPHA,
    },
    []string{"plugin", "extension_point", "status"})
```

### 2. 指标记录实现

```go
// PodScheduled 记录成功调度指标
func PodScheduled(profile string, duration float64) {
    observeScheduleAttemptAndLatency(ScheduledResult, profile, duration)
}

// PodUnschedulable 记录不可调度指标
func PodUnschedulable(profile string, duration float64) {
    observeScheduleAttemptAndLatency(UnschedulableResult, profile, duration)
}

// PodScheduleError 记录调度错误指标
func PodScheduleError(profile string, duration float64) {
    observeScheduleAttemptAndLatency(ErrorResult, profile, duration)
}

func observeScheduleAttemptAndLatency(result, profile string, duration float64) {
    schedulingLatency.WithLabelValues(result, profile).Observe(duration)
    scheduleAttempts.WithLabelValues(result, profile).Inc()
}
```

### 3. 异步指标记录器

```go
// MetricAsyncRecorder 异步指标记录器
type MetricAsyncRecorder struct {
    bufferCh    chan *metric
    bufferSize  int
    interval    time.Duration
    stopCh      <-chan struct{}
    IsStoppedCh chan struct{}
}

// ObservePluginDurationAsync 异步记录插件执行时长
func (r *MetricAsyncRecorder) ObservePluginDurationAsync(extensionPoint, pluginName, status string, value float64) {
    newMetric := &metric{
        metric:      PluginExecutionDuration,
        labelValues: []string{pluginName, extensionPoint, status},
        value:       value,
    }
    select {
    case r.bufferCh <- newMetric:
    default:
        // 缓冲区满时丢弃指标
    }
}

// run 定期刷新缓冲的指标到 Prometheus
func (r *MetricAsyncRecorder) run() {
    for {
        select {
        case <-r.stopCh:
            close(r.IsStoppedCh)
            return
        default:
        }
        r.FlushMetrics()
        time.Sleep(r.interval)
    }
}
```

### 4. 监控配置示例

```yaml
# ServiceMonitor 配置
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kube-scheduler
  namespace: kube-system
spec:
  selector:
    matchLabels:
      component: kube-scheduler
  endpoints:
  - port: https-metrics
    scheme: https
    path: /metrics
    interval: 30s
    tlsConfig:
      caFile: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
      serverName: kube-scheduler
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token

---
# Grafana Dashboard 配置
apiVersion: v1
kind: ConfigMap
metadata:
  name: scheduler-dashboard
data:
  dashboard.json: |
    {
      "dashboard": {
        "title": "Kubernetes Scheduler",
        "panels": [
          {
            "title": "Scheduling Latency",
            "type": "graph",
            "targets": [
              {
                "expr": "histogram_quantile(0.99, sum(rate(scheduler_scheduling_attempt_duration_seconds_bucket[5m])) by (le))",
                "legendFormat": "99th percentile"
              },
              {
                "expr": "histogram_quantile(0.95, sum(rate(scheduler_scheduling_attempt_duration_seconds_bucket[5m])) by (le))",
                "legendFormat": "95th percentile"
              }
            ]
          },
          {
            "title": "Pending Pods",
            "type": "graph",
            "targets": [
              {
                "expr": "scheduler_pending_pods",
                "legendFormat": "{{queue}} queue"
              }
            ]
          }
        ]
      }
    }
```

---

## 故障排除与调试

### 1. 常见调度问题诊断

#### Pod 一直处于 Pending 状态

```bash
# 1. 检查 Pod 状态和事件
kubectl describe pod <pod-name> -n <namespace>

# 2. 检查节点资源
kubectl describe nodes

# 3. 检查调度器日志
kubectl logs -n kube-system kube-scheduler-<node-name>

# 4. 检查调度器指标
curl -k https://<scheduler-endpoint>:10259/metrics | grep scheduler_
```

#### 调度延迟过高

```bash
# 1. 检查调度延迟指标
curl -k https://<scheduler-endpoint>:10259/metrics | grep scheduling_attempt_duration_seconds

# 2. 检查插件执行时间
curl -k https://<scheduler-endpoint>:10259/metrics | grep plugin_execution_duration_seconds

# 3. 检查节点数量和采样配置
kubectl logs -n kube-system kube-scheduler-<node-name> | grep "percentageOfNodesToScore"
```

### 2. 调试工具和技巧

#### 调度器性能分析

```go
// 启用性能分析
import _ "net/http/pprof"

func main() {
    // 启动 pprof 服务器
    go func() {
        log.Println(http.ListenAndServe("localhost:6060", nil))
    }()
    
    // 调度器主逻辑
    // ...
}
```

```bash
# 获取 CPU 性能分析
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# 获取内存分析
go tool pprof http://localhost:6060/debug/pprof/heap

# 获取协程分析
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

#### 调度模拟器

```go
// 调度模拟器示例
package main

import (
    "context"
    "fmt"
    "time"
    
    v1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    clientset "k8s.io/client-go/kubernetes"
    "k8s.io/kubernetes/pkg/scheduler"
)

func simulateScheduling(client clientset.Interface, sched *scheduler.Scheduler) {
    // 创建测试 Pod
    pod := &v1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-pod",
            Namespace: "default",
        },
        Spec: v1.PodSpec{
            Containers: []v1.Container{
                {
                    Name:  "test-container",
                    Image: "nginx",
                    Resources: v1.ResourceRequirements{
                        Requests: v1.ResourceList{
                            v1.ResourceCPU:    resource.MustParse("100m"),
                            v1.ResourceMemory: resource.MustParse("128Mi"),
                        },
                    },
                },
            },
        },
    }

    start := time.Now()
    
    // 执行调度
    ctx := context.Background()
    fwk, _ := sched.Profiles["default-scheduler"]
    state := framework.NewCycleState()
    
    result, err := sched.SchedulePod(ctx, fwk, state, pod)
    if err != nil {
        fmt.Printf("Scheduling failed: %v\n", err)
        return
    }
    
    duration := time.Since(start)
    fmt.Printf("Pod scheduled to node %s in %v\n", result.SuggestedHost, duration)
    fmt.Printf("Evaluated %d nodes, %d were feasible\n", result.EvaluatedNodes, result.FeasibleNodes)
}
```

### 3. 常见配置错误

#### 错误的节点选择器

```yaml
# 错误：节点选择器匹配不到任何节点
apiVersion: v1
kind: Pod
spec:
  nodeSelector:
    disk: "ssd"     # 如果没有节点有这个标签，Pod 将无法调度
  containers:
  - name: app
    image: nginx
```

#### 资源请求过高

```yaml
# 错误：资源请求超过集群容量
apiVersion: v1
kind: Pod
spec:
  containers:
  - name: app
    image: nginx
    resources:
      requests:
        memory: "100Gi"  # 超过集群总内存
        cpu: "50"        # 超过集群总 CPU
```

#### 亲和性规则冲突

```yaml
# 错误：亲和性和反亲和性规则冲突
apiVersion: v1
kind: Pod
spec:
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
        - matchExpressions:
          - key: zone
            operator: In
            values: ["us-west-1a"]
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchLabels:
            app: myapp
        topologyKey: "failure-domain.beta.kubernetes.io/zone"
```

---

## 总结

### 🔑 **核心要点**

1. **框架化设计**：Kubernetes 调度器采用可插拔的框架架构，支持灵活的扩展和定制

2. **多阶段调度**：包含 PreFilter、Filter、PostFilter、PreScore、Score、NormalizeScore 等完整的调度流程

3. **性能优化**：通过节点采样、并行处理、缓存优化等策略实现高性能调度

4. **可观测性**：提供丰富的监控指标和调试工具，支持性能分析和故障排除

### 🏆 **最佳实践**

- **合理配置资源请求**：避免资源请求过高或过低导致的调度问题
- **优化调度策略**：根据集群规模和负载特点调整调度参数
- **监控调度性能**：建立完善的调度器监控和告警机制
- **定期性能调优**：根据监控数据和性能分析结果持续优化

### 🎯 **发展趋势**

- **智能调度**：基于机器学习的智能调度算法
- **多集群调度**：跨集群的统一调度和资源管理
- **边缘调度**：支持边缘计算场景的调度优化
- **实时调度**：支持实时工作负载的低延迟调度

Kubernetes 调度器作为集群资源管理的核心组件，其设计理念和实现方式为云原生应用的高效运行提供了强有力的支撑。通过深入理解调度器的架构和原理，可以更好地优化集群性能，提升应用的运行效率。
