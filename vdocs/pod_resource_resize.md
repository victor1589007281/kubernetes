# Kubernetes Pod CPU/Memory 资源调整流程深度分析

## 目录
1. [架构概览](#架构概览)
2. [完整调用链分析](#完整调用链分析)
3. [队列与并发模型](#队列与并发模型)
4. [瓶颈分析](#瓶颈分析)
5. [大规模场景优化建议](#大规模场景优化建议)
6. [源码参考](#源码参考)

---

## 架构概览

### 资源调整数据流

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **Pod CPU/Memory 资源调整架构图**                                      │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  用户/控制器                                                                            │
│  ├── kubectl patch pod                                                                 │
│  ├── VPA (Vertical Pod Autoscaler)                                                     │
│  └── 自定义控制器                                                                       │
│      │                                                                                  │
│      ▼ PATCH requests.cpu / requests.memory / limits.cpu / limits.memory               │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                          **API Server**                                          │   │
│  │  ┌───────────────────────────────────────────────────────────────────────────┐  │   │
│  │  │  Pod Strategy: 验证资源调整请求                                            │  │   │
│  │  │  - InPlacePodVerticalScaling 特性门控                                       │  │   │
│  │  │  - 设置 pod.Status.Resize = "Proposed"                                     │  │   │
│  │  │  📁 pkg/registry/core/pod/strategy.go:102                                  │  │   │
│  │  └───────────────────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│      │                                                                                  │
│      ▼ Watch 事件                                                                       │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                          **Kubelet**                                             │   │
│  │                                                                                  │   │
│  │   ┌─────────────────────────────────────────────────────────────────────────┐   │   │
│  │   │                    **Pod Workers (每 Pod 一个 Goroutine)**               │   │   │
│  │   │                                                                          │   │   │
│  │   │  syncLoop → HandlePodUpdates → podWorkers.UpdatePod()                   │   │   │
│  │   │      │                                                                   │   │   │
│  │   │      ▼                                                                   │   │   │
│  │   │  podWorkerLoop (goroutine per pod)                                      │   │   │
│  │   │      │                                                                   │   │   │
│  │   │      ├── handlePodResourcesResize() - 检测资源变更                       │   │   │
│  │   │      │   └── canResizePod() - 评估是否可以调整                           │   │   │
│  │   │      │                                                                   │   │   │
│  │   │      └── SyncPod()                                                       │   │   │
│  │   │          └── doPodResizeAction()                                         │   │   │
│  │   │              ├── SetPodCgroupConfig() - 调整 Pod 级 cgroup               │   │   │
│  │   │              └── updatePodContainerResources()                           │   │   │
│  │   │                  └── [每个容器] updateContainerResources()               │   │   │
│  │   │                      └── CRI: UpdateContainerResources                   │   │   │
│  │   │                                                                          │   │   │
│  │   └─────────────────────────────────────────────────────────────────────────┘   │   │
│  │                                                                                  │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│      │                                                                                  │
│      ▼ gRPC                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                          **containerd**                                          │   │
│  │                                                                                  │   │
│  │  UpdateContainerResources()                                                     │   │
│  │  ├── 获取容器锁 (container.Status.UpdateSync)                                   │   │
│  │  ├── 更新 OCI Spec                                                              │   │
│  │  └── task.Update() → containerd-shim → runc update                             │   │
│  │                                                                                  │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│      │                                                                                  │
│      ▼                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                          **Linux Kernel (cgroups)**                              │   │
│  │                                                                                  │   │
│  │  cgroups v1:                              cgroups v2:                           │   │
│  │  /sys/fs/cgroup/cpu/kubepods/...         /sys/fs/cgroup/kubepods.slice/...     │   │
│  │  ├── cpu.cfs_period_us                   ├── cpu.max                            │   │
│  │  ├── cpu.cfs_quota_us                    ├── memory.max                         │   │
│  │  ├── cpu.shares                          └── memory.high                        │   │
│  │  └── memory.limit_in_bytes                                                      │   │
│  │                                                                                  │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 特性门控

| 特性 | 说明 | 默认状态 |
|:-----|:-----|:---------|
| `InPlacePodVerticalScaling` | 允许原地调整 Pod 资源 | Beta (K8s 1.27+) |

---

## 完整调用链分析

### 阶段一：API Server 处理

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **API Server 资源变更处理**                                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  kubectl patch pod <name> -p '{"spec":{"containers":[{"name":"app","resources":{...}}]}'
│  │                                                                                      │
│  ▼                                                                                      │
│  REST Handler: PATCH /api/v1/namespaces/{ns}/pods/{name}                               │
│  │   vendor/k8s.io/apiserver/pkg/endpoints/handlers/patch.go                           │
│  │                                                                                      │
│  └── podStrategy.PrepareForUpdate()                                                    │
│      │   pkg/registry/core/pod/strategy.go:102                                         │
│      │                                                                                  │
│      └── [如果 InPlacePodVerticalScaling 启用]                                         │
│          │                                                                              │
│          ├── 验证 ResizePolicy 合法性                                                   │
│          │   └── NotRequired / RestartContainer                                        │
│          │                                                                              │
│          ├── 对比 Spec.Resources vs Status.AllocatedResources                          │
│          │                                                                              │
│          └── 设置 pod.Status.Resize = "Proposed"                                       │
│              │                                                                          │
│              └── ┌────────────────────────────────────────────────────────────────────┐│
│                  │  Pod Resize Status 状态机                                          ││
│                  │                                                                    ││
│                  │  Proposed → InProgress → (成功) → 清空                             ││
│                  │         ↘                                                          ││
│                  │           → Infeasible (资源不足)                                  ││
│                  │           → Deferred   (暂缓调整)                                  ││
│                  │                                                                    ││
│                  └────────────────────────────────────────────────────────────────────┘│
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 阶段二：Kubelet 检测与调度

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **Kubelet 资源调整检测**                                              │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  syncLoop() - pkg/kubelet/kubelet.go:2330                                              │
│  │   [主循环，监听多个 channel]                                                         │
│  │                                                                                      │
│  └── syncLoopIteration() - kubelet.go:2404                                             │
│      │                                                                                  │
│      └── case u := <-configCh:  // 监听 Pod 配置变更                                   │
│          │                                                                              │
│          └── handler.HandlePodUpdates(pods) - kubelet.go:2617                          │
│              │                                                                          │
│              └── [遍历每个变更的 Pod]                                                   │
│                  │                                                                      │
│                  ├── kl.podManager.UpdatePod(pod) - 更新本地缓存                       │
│                  │                                                                      │
│                  └── kl.podWorkers.UpdatePod(UpdatePodOptions{                         │
│                      │   Pod:        pod,                                              │
│                      │   UpdateType: kubetypes.SyncPodUpdate,                          │
│                      │   StartTime:  start,                                            │
│                      │}) - pod_workers.go:737                                          │
│                      │                                                                  │
│                      └── [发送信号到对应 Pod 的 worker goroutine]                      │
│                          │                                                              │
│                          └── status.pendingUpdate = &options                           │
│                              podUpdates <- struct{}{}  // 非阻塞发送                   │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 阶段三：Pod Worker 处理

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **Pod Worker 资源调整处理**                                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  podWorkerLoop(podUID, podUpdates) - pod_workers.go:1213                               │
│  │   [每个 Pod 一个独立 goroutine]                                                      │
│  │                                                                                      │
│  └── for range podUpdates { ... }                                                      │
│      │                                                                                  │
│      ├── p.startPodSync(podUID) - 获取待处理更新                                       │
│      │                                                                                  │
│      └── p.podSyncer.SyncPod(ctx, update.Options, ...) - 执行同步                      │
│          │                                                                              │
│          └── kubelet.SyncPod() - kubelet.go:1701                                       │
│              │                                                                          │
│              ├── [资源调整检测]                                                         │
│              │   handlePodResourcesResize(pod) - kubelet.go:2804                       │
│              │   │                                                                      │
│              │   ├── 检查 pod.Status.Phase == Running                                  │
│              │   │                                                                      │
│              │   ├── 对比 container.Resources.Requests vs containerStatus.AllocatedResources
│              │   │                                                                      │
│              │   └── canResizePod(pod) - 评估节点资源是否足够                          │
│              │       │                                                                  │
│              │       ├── 计算当前节点可分配资源                                        │
│              │       ├── 计算调整后需要的资源                                          │
│              │       │                                                                  │
│              │       └── [返回]                                                         │
│              │           fit = true  → 可以调整                                        │
│              │           fit = false → 设置 Resize = "Infeasible"                      │
│              │                                                                          │
│              └── containerRuntime.SyncPod(ctx, pod, podStatus, ...)                    │
│                  │   pkg/kubelet/kuberuntime/kuberuntime_manager.go:1139               │
│                  │                                                                      │
│                  └── [Step 7: 资源调整]                                                │
│                      doPodResizeAction() - kuberuntime_manager.go:665                  │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 阶段四：Runtime Manager 执行调整

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **Runtime Manager 资源调整执行**                                      │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  doPodResizeAction(pod, podStatus, podContainerChanges, result)                        │
│  │   pkg/kubelet/kuberuntime/kuberuntime_manager.go:665                                │
│  │                                                                                      │
│  ├── [1. 计算 Pod 级资源配置]                                                          │
│  │   podResources := cm.ResourceConfigForPod(pod, cpuCFSQuota, cpuCFSQuotaPeriod, ...)│
│  │   │   pkg/kubelet/cm/helpers_linux.go:122                                           │
│  │   │                                                                                  │
│  │   └── 返回 ResourceConfig{                                                          │
│  │           CPUPeriod: 100000 (默认 100ms)                                            │
│  │           CPUQuota:  计算的配额                                                      │
│  │           CPUShares: 计算的权重                                                      │
│  │           Memory:    内存限制                                                        │
│  │       }                                                                              │
│  │                                                                                      │
│  ├── [2. 调整 Pod 级 cgroup - 如果资源增加，先调整 Pod 再调整容器]                     │
│  │   pcm.SetPodCgroupConfig(pod, resourceName, podResources)                           │
│  │   │   pkg/kubelet/cm/pod_container_manager_linux.go                                 │
│  │   │                                                                                  │
│  │   └── 写入 cgroup 文件:                                                             │
│  │       ├── cpu.cfs_quota_us / cpu.max                                                │
│  │       ├── cpu.shares                                                                │
│  │       └── memory.limit_in_bytes / memory.max                                        │
│  │                                                                                      │
│  ├── [3. 调整容器资源]                                                                 │
│  │   updatePodContainerResources(pod, resourceName, containersToUpdate)                │
│  │   │   kuberuntime_manager.go:773                                                    │
│  │   │                                                                                  │
│  │   └── [遍历每个需要更新的容器]                                                      │
│  │       │                                                                              │
│  │       └── updateContainerResources(pod, container, containerID)                     │
│  │           │   kuberuntime_container.go:361                                          │
│  │           │                                                                          │
│  │           ├── 构建 LinuxContainerResources:                                         │
│  │           │   {                                                                      │
│  │           │       CpuPeriod:          100000,                                       │
│  │           │       CpuQuota:           cpuLimit * period / 1000,                     │
│  │           │       CpuShares:          cpuRequest * 1024 / 1000,                     │
│  │           │       MemoryLimitInBytes: memoryLimit,                                  │
│  │           │   }                                                                      │
│  │           │                                                                          │
│  │           └── m.runtimeService.UpdateContainerResources(ctx, containerID, resources)
│  │               │   pkg/kubelet/cri/remote/remote_runtime.go:451                      │
│  │               │                                                                      │
│  │               └── [gRPC 调用 containerd]                                            │
│  │                                                                                      │
│  └── [4. 调整 Pod 级 cgroup - 如果资源减少，先调整容器再调整 Pod]                      │
│      pcm.SetPodCgroupConfig(pod, resourceName, podResources)                           │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 阶段五：containerd 处理

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **containerd UpdateContainerResources**                               │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  criService.UpdateContainerResources(ctx, request)                                     │
│  │   containerd/internal/cri/server/container_update_resources.go:38                   │
│  │                                                                                      │
│  ├── [1. 获取容器和沙箱信息]                                                           │
│  │   container, _ := c.containerStore.Get(r.GetContainerId())                          │
│  │   sandbox, _ := c.sandboxStore.Get(container.SandboxID)                             │
│  │                                                                                      │
│  ├── [2. NRI 插件处理 (可选)]                                                          │
│  │   c.nri.BlockPluginSync().Unblock()                                                 │
│  │   updated, _ := c.nri.UpdateContainerResources(ctx, &sandbox, &container, resources)│
│  │                                                                                      │
│  ├── [3. 事务性更新容器状态]                                                           │
│  │   container.Status.UpdateSync(func(status) (newStatus, error) {                     │
│  │   │   // 获取容器级锁，确保原子操作                                                  │
│  │   │                                                                                  │
│  │   │   return c.updateContainerResources(ctx, container, request, status)            │
│  │   │})                                                                                │
│  │   │                                                                                  │
│  │   └── updateContainerResources() - container_update_resources.go:78                 │
│  │       │                                                                              │
│  │       ├── [检查容器状态]                                                             │
│  │       │   if status.Removing { return error }                                       │
│  │       │                                                                              │
│  │       ├── [获取并更新 OCI Spec]                                                     │
│  │       │   oldSpec, _ := cntr.Container.Spec(ctx)                                    │
│  │       │   newSpec, _ := updateOCIResource(ctx, oldSpec, request, config)            │
│  │       │   │   container_update_resources_linux.go                                   │
│  │       │   │                                                                          │
│  │       │   └── 更新 spec.Linux.Resources:                                            │
│  │       │       {                                                                      │
│  │       │           CPU: { Quota, Period, Shares },                                   │
│  │       │           Memory: { Limit, Swap },                                          │
│  │       │       }                                                                      │
│  │       │                                                                              │
│  │       ├── [持久化 OCI Spec]                                                         │
│  │       │   updateContainerSpec(ctx, cntr.Container, newSpec)                         │
│  │       │   │   container_update_resources.go:146                                     │
│  │       │   │                                                                          │
│  │       │   └── cntr.Update(ctx, func(c *containers.Container) {                      │
│  │       │           c.Spec = typeurl.MarshalAny(spec)  // 序列化并存储                │
│  │       │       })                                                                     │
│  │       │                                                                              │
│  │       └── [如果容器正在运行，更新 Task]                                             │
│  │           if status.State() == CONTAINER_RUNNING {                                  │
│  │               task, _ := cntr.Container.Task(ctx, nil)                              │
│  │               task.Update(ctx, containerd.WithResources(getResources(newSpec)))     │
│  │           }                                                                          │
│  │                                                                                      │
│  └── [4. NRI 后置通知]                                                                 │
│      c.nri.PostUpdateContainerResources(ctx, &sandbox, &container)                     │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 阶段六：Task Update 到 runc

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **containerd Task Update → runc update**                              │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  task.Update(ctx, containerd.WithResources(resources))                                 │
│  │   containerd/client/task.go                                                         │
│  │                                                                                      │
│  └── t.client.TaskService().Update(ctx, &UpdateTaskRequest{                           │
│          ContainerID: t.id,                                                            │
│          Resources:   resources,  // *anypb.Any                                        │
│      })                                                                                 │
│      │                                                                                  │
│      ▼ gRPC to containerd daemon                                                       │
│  TasksService.Update()                                                                 │
│  │   containerd/plugins/services/tasks/service.go                                      │
│  │                                                                                      │
│  └── l.Update(ctx, r)                                                                  │
│      │   containerd/plugins/services/tasks/local.go                                    │
│      │                                                                                  │
│      └── t.Process.Update(ctx, resources)                                              │
│          │   // t.Process 是 shim 客户端                                               │
│          │                                                                              │
│          ▼ ttrpc to containerd-shim-runc-v2                                            │
│  TaskService.Update()                                                                  │
│  │   containerd/cmd/containerd-shim-runc-v2/task/service.go                            │
│  │                                                                                      │
│  └── s.update(ctx, r)                                                                  │
│      │                                                                                  │
│      └── container.Update(ctx, resources)                                              │
│          │   containerd/cmd/containerd-shim-runc-v2/runc/container.go                  │
│          │                                                                              │
│          └── c.Runtime.Update(ctx, c.ID, resources)                                    │
│              │   containerd/vendor/github.com/containerd/go-runc/runc.go               │
│              │                                                                          │
│              └── [执行 runc update 命令]                                               │
│                  │                                                                      │
│                  ├── buf := getBuf()                                                   │
│                  ├── json.NewEncoder(buf).Encode(resources)  // 序列化资源配置         │
│                  │                                                                      │
│                  └── exec.Command("runc", "update", "--resources=-", containerID)      │
│                      │   // 通过 stdin 传递 JSON 配置                                  │
│                      │                                                                  │
│                      └── [runc 写入 cgroup 文件]                                       │
│                          │                                                              │
│                          ├── cgroups v1:                                               │
│                          │   /sys/fs/cgroup/cpu/.../cpu.cfs_quota_us                   │
│                          │   /sys/fs/cgroup/cpu/.../cpu.shares                         │
│                          │   /sys/fs/cgroup/memory/.../memory.limit_in_bytes           │
│                          │                                                              │
│                          └── cgroups v2:                                               │
│                              /sys/fs/cgroup/.../cpu.max                                │
│                              /sys/fs/cgroup/.../memory.max                             │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 队列与并发模型

### Pod Workers 并发模型

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **Pod Workers 并发处理模型**                                          │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  type podWorkers struct {                                                              │
│      podLock sync.Mutex                        // 全局锁，保护状态 map                 │
│                                                                                         │
│      podUpdates map[types.UID]chan struct{}   // 每 Pod 一个更新通道                   │
│      │                                                                                  │
│      │   ┌─────────────────────────────────────────────────────────────────────────┐   │
│      │   │  Pod-A 的 Worker Goroutine                                              │   │
│      │   │  podWorkerLoop(uid-A, podUpdates[uid-A])                               │   │
│      │   │      for range podUpdates[uid-A] { SyncPod(...) }                      │   │
│      │   └─────────────────────────────────────────────────────────────────────────┘   │
│      │   ┌─────────────────────────────────────────────────────────────────────────┐   │
│      │   │  Pod-B 的 Worker Goroutine                                              │   │
│      │   │  podWorkerLoop(uid-B, podUpdates[uid-B])                               │   │
│      │   │      for range podUpdates[uid-B] { SyncPod(...) }                      │   │
│      │   └─────────────────────────────────────────────────────────────────────────┘   │
│      │   ... (每个 Pod 一个独立 goroutine)                                             │
│      │                                                                                  │
│      podSyncStatuses map[types.UID]*podSyncStatus  // 每 Pod 同步状态                  │
│                                                                                         │
│      workQueue queue.WorkQueue                 // 重试队列，用于失败后退避              │
│      │                                                                                  │
│      │   type basicWorkQueue struct {                                                  │
│      │       clock clock.Clock                                                          │
│      │       lock  sync.Mutex                                                          │
│      │       queue map[types.UID]time.Time    // UID → 下次可执行时间                  │
│      │   }                                                                              │
│      │                                                                                  │
│      │   Enqueue(uid, delay) - 添加到延迟队列                                          │
│      │   GetWork() []UID     - 获取所有到期的工作项                                    │
│      │                                                                                  │
│      backOffPeriod time.Duration = 10s         // 失败后退避时间                       │
│      resyncInterval time.Duration = 1s         // 重新同步间隔                         │
│  }                                                                                      │
│                                                                                         │
│  **并发特性**:                                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  ✅ 不同 Pod 的更新是**并行**的 (每个 Pod 独立 goroutine)                        │   │
│  │  ⚠️ 同一 Pod 的更新是**串行**的 (通过 channel 排队)                              │   │
│  │  ⚠️ 单个 Pod 内的容器资源更新是**串行**的 (按 Memory → CPU 顺序)                 │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### CRI 请求队列

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **CRI 请求处理**                                                      │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  Kubelet → containerd (gRPC)                                                           │
│  │                                                                                      │
│  ├── 无显式队列，依赖 gRPC 并发处理能力                                                │
│  │                                                                                      │
│  └── containerd 内部：                                                                 │
│      │                                                                                  │
│      ├── 每个容器有独立的状态锁 (container.Status.UpdateSync)                          │
│      │   └── 同一容器的并发请求会被串行化                                              │
│      │                                                                                  │
│      └── 不同容器的请求可以并行处理                                                    │
│                                                                                         │
│  **超时设置**:                                                                          │
│  ┌────────────────────────────────────────────────────────────────────────────────┐    │
│  │  pkg/kubelet/cri/remote/remote_runtime.go                                      │    │
│  │                                                                                 │    │
│  │  remoteRuntimeService.timeout = 2 * time.Minute  // 默认 CRI 请求超时         │    │
│  │                                                                                 │    │
│  │  UpdateContainerResources:                                                     │    │
│  │      ctx, cancel := context.WithTimeout(ctx, r.timeout)                        │    │
│  │      defer cancel()                                                             │    │
│  └────────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 容器内更新顺序

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **容器资源更新顺序**                                                  │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  doPodResizeAction() 执行顺序:                                                         │
│  │                                                                                      │
│  ├── [1] Memory 资源更新 (优先)                                                        │
│  │   │                                                                                  │
│  │   ├── 如果 newMemLimit > currentMemLimit:                                           │
│  │   │   ├── (1) 扩大 Pod cgroup Memory Limit                                         │
│  │   │   └── (2) 依次扩大每个容器 Memory Limit                                        │
│  │   │                                                                                  │
│  │   └── 如果 newMemLimit < currentMemLimit:                                           │
│  │       ├── (1) 依次缩小每个容器 Memory Limit                                        │
│  │       └── (2) 缩小 Pod cgroup Memory Limit                                         │
│  │                                                                                      │
│  └── [2] CPU 资源更新 (其次)                                                           │
│      │                                                                                  │
│      ├── 如果 newCPULimit > currentCPULimit:                                           │
│      │   ├── (1) 扩大 Pod cgroup CPU Quota/Shares                                     │
│      │   └── (2) 依次扩大每个容器 CPU Quota/Shares                                    │
│      │                                                                                  │
│      └── 如果 newCPULimit < currentCPULimit:                                           │
│          ├── (1) 依次缩小每个容器 CPU Quota/Shares                                    │
│          └── (2) 缩小 Pod cgroup CPU Quota/Shares                                     │
│                                                                                         │
│  **为什么这个顺序?**                                                                    │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  资源增加时：先扩大 Pod 限制，再扩大容器限制                                      │   │
│  │             → 确保容器不会因父 cgroup 限制而被 OOM                                │   │
│  │                                                                                   │   │
│  │  资源减少时：先缩小容器限制，再缩小 Pod 限制                                      │   │
│  │             → 确保子 cgroup 不会超过父 cgroup 限制                                │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 瓶颈分析

### 瓶颈点识别

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **大规模资源调整瓶颈分析**                                            │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  假设场景: 1000 个 Pod 同时需要调整 CPU/Memory 资源                                    │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  **瓶颈 1: API Server 处理能力**                                                 │   │
│  │                                                                                   │   │
│  │  位置: API Server 接收 PATCH 请求                                                │   │
│  │  限制因素:                                                                        │   │
│  │    - API Server 的请求处理能力 (QPS 限制)                                        │   │
│  │    - etcd 写入性能 (每个 Pod 更新需要写入 etcd)                                  │   │
│  │    - 请求队列深度                                                                │   │
│  │                                                                                   │   │
│  │  量化:                                                                            │   │
│  │    - 默认 API Server maxRequestsInFlight = 400                                   │   │
│  │    - 默认 mutatingMaxRequestsInFlight = 200                                      │   │
│  │    - etcd 写入延迟 ~10ms                                                         │   │
│  │    - 理论 QPS: ~100-200 个 Pod 更新/秒                                           │   │
│  │                                                                                   │   │
│  │  潜在问题: ⚠️ 大量并发更新可能导致请求被限流或超时                                │   │
│  │                                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  **瓶颈 2: Kubelet Watch 事件处理**                                               │   │
│  │                                                                                   │   │
│  │  位置: Kubelet 的 Pod informer                                                   │   │
│  │  限制因素:                                                                        │   │
│  │    - Watch 事件缓冲区大小                                                        │   │
│  │    - reflector 的事件处理速度                                                    │   │
│  │    - syncLoop 的循环周期                                                         │   │
│  │                                                                                   │   │
│  │  量化:                                                                            │   │
│  │    - 默认 watch 缓冲区: 1000 个事件                                              │   │
│  │    - syncLoop 周期: 1 秒                                                         │   │
│  │    - 单次 HandlePodUpdates 可处理多个 Pod                                        │   │
│  │                                                                                   │   │
│  │  潜在问题: ⚠️ 大量事件可能导致事件丢失或延迟处理                                  │   │
│  │                                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  **瓶颈 3: Pod Worker Goroutine 数量**                                            │   │
│  │                                                                                   │   │
│  │  位置: podWorkers                                                                │   │
│  │  限制因素:                                                                        │   │
│  │    - 每个 Pod 一个 goroutine                                                     │   │
│  │    - Go 调度器的 goroutine 调度开销                                              │   │
│  │    - 内存消耗 (~2-8KB per goroutine)                                             │   │
│  │                                                                                   │   │
│  │  量化:                                                                            │   │
│  │    - 1000 个 Pod = 1000 个 goroutine                                             │   │
│  │    - 内存开销: ~8MB (可接受)                                                     │   │
│  │    - 并发调度: 依赖 GOMAXPROCS (通常等于 CPU 核数)                               │   │
│  │                                                                                   │   │
│  │  潜在问题: ✅ 通常不是瓶颈，Go 调度器可以处理数千 goroutine                       │   │
│  │                                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  **瓶颈 4: CRI gRPC 调用** ⭐ 主要瓶颈                                            │   │
│  │                                                                                   │   │
│  │  位置: Kubelet → containerd UpdateContainerResources                             │   │
│  │  限制因素:                                                                        │   │
│  │    - gRPC 连接复用                                                               │   │
│  │    - containerd 处理能力                                                         │   │
│  │    - 每个请求的处理时间                                                          │   │
│  │                                                                                   │   │
│  │  量化:                                                                            │   │
│  │    - 默认超时: 2 分钟                                                            │   │
│  │    - 单个 UpdateContainerResources 耗时: ~10-50ms                               │   │
│  │    - containerd 默认 gRPC 并发: 无限制 (依赖系统资源)                            │   │
│  │                                                                                   │   │
│  │  **计算**:                                                                        │   │
│  │    假设每个容器资源更新需要 30ms                                                 │   │
│  │    1000 个 Pod × 2 个容器/Pod = 2000 次 CRI 调用                                 │   │
│  │    串行处理: 2000 × 30ms = 60 秒                                                 │   │
│  │    并行处理 (假设 100 并发): 2000 / 100 × 30ms = 600ms                           │   │
│  │                                                                                   │   │
│  │  潜在问题: ⚠️ 大量并发 CRI 调用可能导致 containerd 负载过高                      │   │
│  │                                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  **瓶颈 5: containerd 容器锁** ⭐ 关键瓶颈                                        │   │
│  │                                                                                   │   │
│  │  位置: container.Status.UpdateSync()                                             │   │
│  │  限制因素:                                                                        │   │
│  │    - 每个容器有独立的状态锁                                                      │   │
│  │    - 同一容器的并发请求被串行化                                                  │   │
│  │                                                                                   │   │
│  │  潜在问题: ✅ 不同容器可以并行，同一容器需要排队                                  │   │
│  │                                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  **瓶颈 6: runc update 系统调用** ⭐ 底层瓶颈                                     │   │
│  │                                                                                   │   │
│  │  位置: runc update --resources=- <container-id>                                  │   │
│  │  限制因素:                                                                        │   │
│  │    - 进程 fork/exec 开销                                                         │   │
│  │    - cgroup 文件系统写入                                                         │   │
│  │    - 内核 cgroup 锁竞争                                                          │   │
│  │                                                                                   │   │
│  │  量化:                                                                            │   │
│  │    - 单次 runc update: ~5-20ms                                                   │   │
│  │    - cgroup 文件写入: ~1-5ms                                                     │   │
│  │    - 内核处理: ~1ms                                                              │   │
│  │                                                                                   │   │
│  │  潜在问题: ⚠️ 大量并发 runc 进程可能导致系统负载升高                              │   │
│  │                                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │  **瓶颈 7: cgroup 文件系统写入**                                                  │   │
│  │                                                                                   │   │
│  │  位置: /sys/fs/cgroup/...                                                        │   │
│  │  限制因素:                                                                        │   │
│  │    - cgroup 是伪文件系统，写入是同步的                                           │   │
│  │    - 内核 cgroup 子系统锁                                                        │   │
│  │    - cgroups v1 vs v2 性能差异                                                   │   │
│  │                                                                                   │   │
│  │  量化:                                                                            │   │
│  │    - cgroups v1: 多个子系统文件，可能有锁竞争                                    │   │
│  │    - cgroups v2: 统一层次结构，锁竞争较少                                        │   │
│  │    - 单次写入: ~100μs - 1ms                                                      │   │
│  │                                                                                   │   │
│  │  潜在问题: ⚠️ 极端并发下可能有内核锁等待                                          │   │
│  │                                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 瓶颈量化分析

| 瓶颈点 | 单次延迟 | 并发能力 | 1000 Pod 预估耗时 | 风险等级 |
|:-------|:---------|:---------|:------------------|:---------|
| API Server | 10-50ms | 100-200 QPS | 5-10秒 | 🟡 中 |
| Kubelet Watch | ~1ms | 批量处理 | <1秒 | 🟢 低 |
| Pod Workers | ~0.1ms | 1000+ 并发 | <1秒 | 🟢 低 |
| CRI gRPC | 10-50ms | 100-500 并发 | 2-20秒 | 🟡 中 |
| containerd 处理 | 10-30ms | 容器级并发 | 10-30秒 | 🟡 中 |
| runc update | 5-20ms | 进程级 | 10-40秒 | 🟡 中 |
| cgroup 写入 | 0.1-1ms | 内核限制 | <1秒 | 🟢 低 |

**总体预估**: 1000 个 Pod 的资源调整，在理想并发条件下约需 **30-60 秒**。

---

## 大规模场景优化建议

### 1. API Server 层面

```yaml
# kube-apiserver 配置优化
apiVersion: v1
kind: Pod
metadata:
  name: kube-apiserver
spec:
  containers:
  - name: kube-apiserver
    command:
    - kube-apiserver
    # 增加并发请求处理能力
    - --max-requests-inflight=800
    - --max-mutating-requests-inflight=400
    # 启用优先级和公平调度
    - --enable-priority-and-fairness=true
```

### 2. Kubelet 层面

```yaml
# kubelet 配置优化
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
# 减少同步周期
syncFrequency: "500ms"
# 增加事件突发处理能力
eventRecordQPS: 50
eventBurst: 100
```

### 3. 批量更新策略

```go
// 推荐：分批更新，避免雪崩
func batchResizePods(pods []*v1.Pod, batchSize int, interval time.Duration) error {
    for i := 0; i < len(pods); i += batchSize {
        end := i + batchSize
        if end > len(pods) {
            end = len(pods)
        }
        
        batch := pods[i:end]
        for _, pod := range batch {
            go updatePodResources(pod)
        }
        
        time.Sleep(interval)  // 批次间隔
    }
    return nil
}

// 示例：每批 50 个 Pod，间隔 1 秒
batchResizePods(pods, 50, 1*time.Second)
```

### 4. VPA 配置优化

```yaml
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: my-vpa
spec:
  targetRef:
    apiVersion: "apps/v1"
    kind: Deployment
    name: my-app
  updatePolicy:
    updateMode: "Auto"
    # 控制更新频率
    minReplicas: 2
  resourcePolicy:
    containerPolicies:
    - containerName: "*"
      # 设置调整阈值，避免频繁小幅调整
      minAllowed:
        cpu: 100m
        memory: 128Mi
      maxAllowed:
        cpu: 4
        memory: 8Gi
```

### 5. 监控指标

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **关键监控指标**                                                      │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **Kubelet 指标**:                                                                      │
│  - kubelet_pod_worker_duration_seconds       # Pod Worker 处理时间                      │
│  - kubelet_cgroup_manager_duration_seconds   # cgroup 操作时间                          │
│  - kubelet_runtime_operations_duration_seconds{operation="update_container"}            │
│                                                                                         │
│  **containerd 指标**:                                                                   │
│  - containerd_task_update_duration_seconds   # Task Update 耗时                        │
│                                                                                         │
│  **系统指标**:                                                                          │
│  - node_cgroup_write_latency_seconds         # cgroup 写入延迟 (自定义)                 │
│  - process_open_fds                          # 打开的文件描述符数                        │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 源码参考

### Kubernetes

| 文件 | 行号 | 函数/结构 |
|:-----|:-----|:---------|
| `pkg/kubelet/pod_workers.go` | 551 | `type podWorkers struct` |
| `pkg/kubelet/pod_workers.go` | 737 | `UpdatePod()` |
| `pkg/kubelet/pod_workers.go` | 1213 | `podWorkerLoop()` |
| `pkg/kubelet/kubelet.go` | 2617 | `HandlePodUpdates()` |
| `pkg/kubelet/kubelet.go` | 2804 | `handlePodResourcesResize()` |
| `pkg/kubelet/kuberuntime/kuberuntime_manager.go` | 665 | `doPodResizeAction()` |
| `pkg/kubelet/kuberuntime/kuberuntime_manager.go` | 773 | `updatePodContainerResources()` |
| `pkg/kubelet/kuberuntime/kuberuntime_container.go` | 361 | `updateContainerResources()` |
| `pkg/kubelet/cri/remote/remote_runtime.go` | 451 | `UpdateContainerResources()` |
| `pkg/kubelet/util/queue/work_queue.go` | 29 | `type WorkQueue interface` |

### containerd

| 文件 | 行号 | 函数/结构 |
|:-----|:-----|:---------|
| `internal/cri/server/container_update_resources.go` | 38 | `UpdateContainerResources()` |
| `internal/cri/server/container_update_resources.go` | 78 | `updateContainerResources()` |
| `internal/cri/server/container_update_resources_linux.go` | 31 | `updateOCIResource()` |
| `client/task.go` | 620 | `task.Update()` |
| `vendor/github.com/containerd/go-runc/runc.go` | 692 | `runc.Update()` |

---

## 总结

### 关键结论

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **Pod 资源调整关键结论**                                              │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  1. **并发模型**:                                                                       │
│     - 不同 Pod 的资源调整是并行的 (每 Pod 一个 goroutine)                               │
│     - 同一 Pod 的多次调整是串行的 (channel 排队)                                       │
│     - 同一 Pod 内的容器资源调整是串行的 (Memory 优先于 CPU)                            │
│                                                                                         │
│  2. **主要瓶颈**:                                                                       │
│     - CRI gRPC 调用延迟 (~10-50ms per container)                                       │
│     - runc update 进程开销 (~5-20ms per container)                                     │
│     - API Server 写入吞吐量 (100-200 QPS)                                              │
│                                                                                         │
│  3. **大规模场景**:                                                                     │
│     - 1000 个 Pod 调整预计需要 30-60 秒                                                │
│     - 建议分批处理，每批 50-100 个 Pod                                                 │
│     - 批次间隔 1-2 秒，避免系统过载                                                    │
│                                                                                         │
│  4. **优化方向**:                                                                       │
│     - 使用 cgroups v2 减少内核锁竞争                                                   │
│     - 调整 API Server 并发限制                                                         │
│     - VPA 配置合理的更新阈值                                                           │
│     - 监控关键延迟指标                                                                 │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

*本文档基于 Kubernetes 和 containerd 源码分析，详细介绍了 Pod CPU/Memory 资源调整的完整流程、队列机制和瓶颈分析。*
