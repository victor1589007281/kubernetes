# Kubernetes 容器生命周期 Hook 机制深度解析

## 目录
1. [架构概览](#架构概览)
2. [Hook 类型详解](#hook-类型详解)
3. [实现原理](#实现原理)
4. [源码分析](#源码分析)
5. [使用场景](#使用场景)
6. [最佳实践](#最佳实践)
7. [故障排查](#故障排查)

---

## 架构概览

### Hook 在 Pod 生命周期中的位置

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                        **容器生命周期 Hook 架构图**                                       │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  Pod 创建                                                                               │
│      │                                                                                  │
│      ▼                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                              Init Containers                                     │   │
│  │   (按顺序执行，每个必须成功完成才能继续)                                          │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│      │                                                                                  │
│      ▼                                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                           Main Containers 启动                                   │   │
│  │                                                                                  │   │
│  │   ┌───────────────┐    ┌──────────────────────────┐    ┌─────────────────────┐  │   │
│  │   │   容器进程    │    │      **PostStart Hook**  │    │    探针开始运行     │  │   │
│  │   │   启动        │───▶│  (与容器进程并行执行)    │───▶│  (等待 Hook 完成后) │  │   │
│  │   │               │    │                          │    │                     │  │   │
│  │   └───────────────┘    └──────────────────────────┘    └─────────────────────┘  │   │
│  │                                                                                  │   │
│  │   ──────────────────────── 容器运行期间 ────────────────────────────────────    │   │
│  │                                                                                  │   │
│  │   ┌─────────────────────┐                                                        │   │
│  │   │  Liveness Probe     │  持续运行，检测容器是否存活                            │   │
│  │   │  Readiness Probe    │  持续运行，检测容器是否就绪                            │   │
│  │   │  Startup Probe      │  启动时运行，检测应用是否启动完成                      │   │
│  │   └─────────────────────┘                                                        │   │
│  │                                                                                  │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│      │                                                                                  │
│      ▼ (收到终止信号)                                                                   │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                           容器终止流程                                           │   │
│  │                                                                                  │   │
│  │   ┌──────────────────────────┐    ┌───────────────┐    ┌─────────────────────┐  │   │
│  │   │    **PreStop Hook**      │    │   SIGTERM     │    │  SIGKILL            │  │   │
│  │   │  (在发送 SIGTERM 之前)   │───▶│   信号发送    │───▶│  (gracePeriod后)    │  │   │
│  │   │                          │    │               │    │                     │  │   │
│  │   └──────────────────────────┘    └───────────────┘    └─────────────────────┘  │   │
│  │                                                                                  │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│      │                                                                                  │
│      ▼                                                                                  │
│  Pod 已删除                                                                             │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### Hook 与 Probe 的区别

| 特性 | Hook (PostStart/PreStop) | Probe (Liveness/Readiness/Startup) |
|:-----|:------------------------|:-----------------------------------|
| **执行时机** | 特定时间点（启动后/停止前） | 周期性执行 |
| **执行次数** | 一次 | 多次（周期性） |
| **目的** | 初始化/清理 | 健康检查 |
| **失败影响** | 容器终止 (PostStart) 或记录事件 (PreStop) | 重启容器或从 Service 移除 |
| **阻塞行为** | PostStart 阻塞探针开始 | 不阻塞其他操作 |

---

## Hook 类型详解

### 1. PostStart Hook

**执行时机**: 容器创建后立即执行，与容器 ENTRYPOINT **并行**执行

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: poststart-demo
spec:
  containers:
  - name: app
    image: nginx
    lifecycle:
      postStart:
        exec:
          command: ["/bin/sh", "-c", "echo 'Container started' > /tmp/started"]
```

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **PostStart Hook 特性**                                         │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **执行顺序**:                                                                          │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │  时间线 ────────────────────────────────────────────────────────────────────▶    │  │
│  │                                                                                   │  │
│  │  容器进程 (ENTRYPOINT)  ████████████████████████████████████████████████████    │  │
│  │                         ↑ 启动                                                    │  │
│  │                                                                                   │  │
│  │  PostStart Hook         ██████████████████                                       │  │
│  │                         ↑ 立即开始      ↑ 完成                                    │  │
│  │                                         │                                         │  │
│  │  Probe 开始             ─────────────────█████████████████████████████████████   │  │
│  │                                         ↑ Hook 完成后才开始                       │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  **关键特性**:                                                                          │
│  1. 与容器进程**并行**执行，不是在容器进程启动完成后                                   │
│  2. 阻塞 kubelet 开始 Probe 检测                                                       │
│  3. 如果 Hook 执行失败，容器会被终止                                                   │
│  4. 不保证在容器 ENTRYPOINT 之前执行（并行关系）                                       │
│  5. Hook 超时等于 `TerminationGracePeriodSeconds`                                      │
│                                                                                         │
│  **失败处理**:                                                                          │
│  - Hook 执行失败 → 容器被杀死                                                          │
│  - 生成 FailedPostStartHook 事件                                                       │
│  - Pod 可能进入 CrashLoopBackOff                                                       │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 2. PreStop Hook

**执行时机**: 容器收到终止请求后，在发送 SIGTERM **之前**执行

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: prestop-demo
spec:
  terminationGracePeriodSeconds: 60
  containers:
  - name: app
    image: nginx
    lifecycle:
      preStop:
        exec:
          command: ["/bin/sh", "-c", "nginx -s quit; while killall -0 nginx; do sleep 1; done"]
```

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **PreStop Hook 特性**                                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **执行顺序**:                                                                          │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │  时间线 ────────────────────────────────────────────────────────────────────▶    │  │
│  │                                                                                   │  │
│  │  删除请求  ↓                                                                      │  │
│  │            │                                                                      │  │
│  │  PreStop   ████████████████████                                                  │  │
│  │            ↑ 开始              ↑ 完成/超时                                        │  │
│  │                                │                                                  │  │
│  │  SIGTERM   ────────────────────█                                                 │  │
│  │                                ↑ PreStop 完成后发送                               │  │
│  │                                │                                                  │  │
│  │  等待期    ────────────────────████████████████                                  │  │
│  │                                                ↑ gracePeriod 到期                 │  │
│  │                                                │                                  │  │
│  │  SIGKILL   ────────────────────────────────────█                                 │  │
│  │                                                ↑ 强制终止                         │  │
│  │                                                                                   │  │
│  │  ◄────────────── terminationGracePeriodSeconds ────────────────────────────────▶│  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  **关键特性**:                                                                          │
│  1. 在 SIGTERM **之前**执行                                                            │
│  2. 执行时间从 gracePeriod 中扣除                                                      │
│  3. 即使 Hook 执行失败，SIGTERM 仍然会发送                                             │
│  4. Hook 执行时间过长会导致留给进程优雅退出的时间减少                                   │
│  5. 最小 gracePeriod 保证为 2 秒 (minimumGracePeriodInSeconds)                        │
│                                                                                         │
│  **超时处理**:                                                                          │
│  - Hook 超时 → 立即发送 SIGTERM                                                        │
│  - 生成 FailedPreStopHook 事件                                                         │
│  - 不会阻止容器终止流程                                                                │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 3. Hook Handler 类型

Hook 支持三种执行方式：

#### 3.1 Exec Handler

```yaml
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 30"]
```

#### 3.2 HTTPGet Handler

```yaml
lifecycle:
  postStart:
    httpGet:
      path: /healthz
      port: 8080
      host: localhost   # 可选，默认是 Pod IP
      scheme: HTTP      # HTTP 或 HTTPS
      httpHeaders:      # 可选
      - name: X-Custom-Header
        value: CustomValue
```

#### 3.3 Sleep Handler (Kubernetes 1.29+)

```yaml
lifecycle:
  preStop:
    sleep:
      seconds: 10
```

---

## 实现原理

### Hook 执行架构

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Hook 执行架构**                                               │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  kubelet                                                                                │
│  ├── kubeGenericRuntimeManager                                                         │
│  │   │                                                                                  │
│  │   ├── [容器启动] startContainer()                                                   │
│  │   │   └── runner.Run(containerID, pod, container, handler.PostStart)               │
│  │   │       │                                                                          │
│  │   │       └── [HandlerRunner 接口]                                                  │
│  │   │                                                                                  │
│  │   └── [容器终止] killContainer()                                                    │
│  │       └── executePreStopHook()                                                      │
│  │           └── runner.Run(containerID, pod, container, handler.PreStop)             │
│  │               │                                                                      │
│  │               └── [HandlerRunner 接口]                                              │
│  │                                                                                      │
│  └── lifecycle.HandlerRunner (pkg/kubelet/lifecycle/handlers.go)                       │
│      │                                                                                  │
│      ├── [Exec Handler]                                                                │
│      │   └── commandRunner.RunInContainer(ctx, containerID, command, timeout)          │
│      │       └── CRI: ExecSync                                                         │
│      │           └── containerd: execSync() → runc exec                               │
│      │                                                                                  │
│      ├── [HTTPGet Handler]                                                             │
│      │   └── httpDoer.Do(request)                                                      │
│      │       └── HTTP GET 请求到容器内服务                                             │
│      │                                                                                  │
│      └── [Sleep Handler]                                                               │
│          └── time.Sleep(duration)                                                      │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 源码分析

### 1. Hook 定义 (API)

**文件**: `staging/src/k8s.io/api/core/v1/types.go`

```go
// Lifecycle describes actions that the management system should take in response
// to container lifecycle events.
type Lifecycle struct {
    // PostStart is called immediately after a container is created.
    PostStart *LifecycleHandler `json:"postStart,omitempty"`
    // PreStop is called immediately before a container is terminated.
    PreStop *LifecycleHandler `json:"preStop,omitempty"`
}

// LifecycleHandler defines a specific action that should be taken in a lifecycle hook.
type LifecycleHandler struct {
    // Exec specifies the action to take.
    Exec *ExecAction `json:"exec,omitempty"`
    // HTTPGet specifies the http request to perform.
    HTTPGet *HTTPGetAction `json:"httpGet,omitempty"`
    // Sleep represents the duration that the container should sleep before being terminated.
    Sleep *SleepAction `json:"sleep,omitempty"`
}
```

### 2. PostStart Hook 执行

**文件**: `pkg/kubelet/kuberuntime/kuberuntime_container.go`

```go
// 行500-530: startContainer() 中调用 PostStart Hook
func (m *kubeGenericRuntimeManager) startContainer(...) (...) {
    // ...容器创建代码...
    
    // Step 4: execute the post start hook.
    if container.Lifecycle != nil && container.Lifecycle.PostStart != nil {
        kubeContainerID := kubecontainer.ContainerID{
            Type: m.runtimeName,
            ID:   containerID,
        }
        msg, handlerErr := m.runner.Run(ctx, kubeContainerID, pod, container, container.Lifecycle.PostStart)
        if handlerErr != nil {
            klog.ErrorS(handlerErr, "Failed to execute PostStartHook", ...)
            // 记录事件
            m.recordContainerEvent(pod, container, containerID, v1.EventTypeWarning, 
                events.FailedPostStartHook, msg)
            // PostStart 失败会导致容器被杀死
            if err := m.killContainer(ctx, pod, containerID, container.Name, 
                "FailedPostStartHook", reasonFailedPostStartHook, nil); err != nil {
                klog.ErrorS(err, "Failed to kill container", ...)
            }
            return msg, ErrPostStartHook
        }
    }
    return "", nil
}
```

### 3. PreStop Hook 执行

**文件**: `pkg/kubelet/kuberuntime/kuberuntime_container.go`

#### 3.1 killContainer 中的 PreStop 调用

```go
// 文件: pkg/kubelet/kuberuntime/kuberuntime_container.go:706-756
func (m *kubeGenericRuntimeManager) killContainer(ctx context.Context, pod *v1.Pod, 
    containerID kubecontainer.ContainerID, containerName string, message string, 
    reason containerKillReason, gracePeriodOverride *int64) error {
    
    // 1. 获取容器 Spec
    containerSpec = kubecontainer.GetContainerSpec(pod, containerName)
    
    // 2. 计算 gracePeriod
    gracePeriod := setTerminationGracePeriod(pod, containerSpec, containerName, containerID, reason)
    
    // 3. 记录事件
    m.recordContainerEvent(pod, containerSpec, containerID.ID, v1.EventTypeNormal, 
        events.KillingContainer, message)
    
    // 4. 执行 PreStop Hook（如果配置了且有足够时间）
    if containerSpec.Lifecycle != nil && containerSpec.Lifecycle.PreStop != nil && gracePeriod > 0 {
        // ⚠️ 关键：PreStop 消耗的时间从 gracePeriod 中扣除
        gracePeriod = gracePeriod - m.executePreStopHook(ctx, pod, containerID, containerSpec, gracePeriod)
    }
    
    // 5. 保证最小优雅期（2秒）
    if gracePeriod < minimumGracePeriodInSeconds {
        gracePeriod = minimumGracePeriodInSeconds  // 最小 2 秒
    }
    
    // 6. 调用 CRI StopContainer
    err := m.runtimeService.StopContainer(ctx, containerID.ID, gracePeriod)
    return err
}
```

#### 3.2 executePreStopHook 实现

```go
// 文件: pkg/kubelet/kuberuntime/kuberuntime_container.go:627-653
func (m *kubeGenericRuntimeManager) executePreStopHook(ctx context.Context, 
    pod *v1.Pod, containerID kubecontainer.ContainerID, 
    containerSpec *v1.Container, gracePeriod int64) int64 {
    
    klog.V(3).InfoS("Running preStop hook", "pod", klog.KObj(pod), 
        "containerName", containerSpec.Name, "containerID", containerID.String())

    start := metav1.Now()
    done := make(chan struct{})
    
    // 异步执行 Hook（防止阻塞）
    go func() {
        defer close(done)
        defer utilruntime.HandleCrash()
        // 调用 HandlerRunner 执行 Hook
        if _, err := m.runner.Run(ctx, containerID, pod, containerSpec, 
            containerSpec.Lifecycle.PreStop); err != nil {
            klog.ErrorS(err, "PreStop hook failed", ...)
            // 记录事件，但不阻止容器终止
            m.recordContainerEvent(pod, containerSpec, containerID.ID, 
                v1.EventTypeWarning, events.FailedPreStopHook, "PreStopHook failed")
        }
    }()

    // 等待 Hook 完成或超时
    select {
    case <-time.After(time.Duration(gracePeriod) * time.Second):
        // 超时：Hook 未在 gracePeriod 内完成
        klog.V(2).InfoS("PreStop hook not completed in grace period", ...)
    case <-done:
        // 正常完成
        klog.V(3).InfoS("PreStop hook completed", ...)
    }

    // 返回 Hook 消耗的时间（秒），用于从 gracePeriod 中扣除
    return int64(metav1.Now().Sub(start.Time).Seconds())
}
```

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                      **PreStop Hook 执行时序**                                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  时间线 ────────────────────────────────────────────────────────────────────▶           │
│                                                                                         │
│  terminationGracePeriodSeconds = 30s                                                   │
│                                                                                         │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │                                                                                   │  │
│  │  删除请求  ↓                                                                      │  │
│  │            │                                                                      │  │
│  │  PreStop   ██████████████████ (假设执行了15秒)                                   │  │
│  │            ↑ start           ↑ done                                              │  │
│  │            │                 │                                                    │  │
│  │  剩余      │◄───15秒───────▶│◄─────15秒(30-15)──────▶│                           │  │
│  │  gracePeriod                │                        │                           │  │
│  │                             │                        │                           │  │
│  │  SIGTERM                    █                        │                           │  │
│  │                             ↑ PreStop完成后立即发送   │                           │  │
│  │                                                      │                           │  │
│  │  等待容器退出               │◄───gracePeriod(15秒)──▶│                           │  │
│  │                                                      │                           │  │
│  │  SIGKILL                    ────────────────────────█                            │  │
│  │                                                     ↑ 如果容器仍未退出            │  │
│  │                                                                                   │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  **关键点**:                                                                            │
│  1. PreStop 消耗的时间从总 gracePeriod 中扣除                                          │
│  2. 最小保证 2 秒给 SIGTERM（minimumGracePeriodInSeconds）                             │
│  3. 如果 PreStop 超时，仍会继续终止流程                                                │
│  4. PreStop 失败只记录事件，不阻止终止                                                 │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 4. Hook Runner 实现

**文件**: `pkg/kubelet/lifecycle/handlers.go`

```go
// 行71-104: Run() - Hook 执行主逻辑
func (hr *handlerRunner) Run(ctx context.Context, containerID kubecontainer.ContainerID, 
    pod *v1.Pod, container *v1.Container, handler *v1.LifecycleHandler) (string, error) {
    
    switch {
    case handler.Exec != nil:
        // Exec Handler: 在容器内执行命令
        output, err := hr.commandRunner.RunInContainer(ctx, containerID, 
            handler.Exec.Command, 0)
        if err != nil {
            msg = fmt.Sprintf("Exec lifecycle hook (%v) for Container %q in Pod %q failed - error: %v", 
                handler.Exec.Command, container.Name, format.Pod(pod), err)
        }
        return msg, err
        
    case handler.HTTPGet != nil:
        // HTTPGet Handler: 发送 HTTP 请求
        err := hr.runHTTPHandler(ctx, pod, container, handler, hr.eventRecorder)
        if err != nil {
            msg = fmt.Sprintf("HTTP lifecycle hook (%s) for Container %q in Pod %q failed", 
                handler.HTTPGet.Path, container.Name, format.Pod(pod))
        }
        return msg, err
        
    case handler.Sleep != nil:
        // Sleep Handler: 简单等待
        err := hr.runSleepHandler(ctx, handler.Sleep.Seconds)
        return msg, err
        
    default:
        return "invalid handler", fmt.Errorf("invalid handler: %v", handler)
    }
}
```

### 5. Exec Handler - CRI 调用链（完整版）

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                      **Exec Handler 完整调用链**                                         │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  [Kubelet 层]                                                                           │
│  handlerRunner.Run()                                                                    │
│  │   pkg/kubelet/lifecycle/handlers.go:71-104                                          │
│  │                                                                                      │
│  └── hr.commandRunner.RunInContainer(ctx, containerID, handler.Exec.Command, 0)        │
│      │   pkg/kubelet/lifecycle/handlers.go:76                                          │
│      │                                                                                  │
│      └── kubeGenericRuntimeManager.RunInContainer()                                    │
│          │   pkg/kubelet/kuberuntime/kuberuntime_container.go:1182-1188               │
│          │                                                                              │
│          └── m.runtimeService.ExecSync(ctx, id.ID, cmd, timeout)                       │
│              │   pkg/kubelet/kuberuntime/kuberuntime_container.go:1184                 │
│              │                                                                          │
│  [CRI Remote 层]                                                                        │
│              └── remoteRuntimeService.ExecSync()                                       │
│                  │   pkg/kubelet/cri/remote/remote_runtime.go:469-516                  │
│                  │                                                                      │
│                  └── r.runtimeClient.ExecSync(ctx, req)  // gRPC 调用                  │
│                      │   pkg/kubelet/cri/remote/remote_runtime.go:494                  │
│                      │                                                                  │
│  [containerd CRI Server 层]                                                            │
│                      └── criService.ExecSync()                                         │
│                          │   containerd/internal/cri/server/container_execsync.go:73-100
│                          │                                                              │
│                          └── c.execInContainer(ctx, r.GetContainerId(), execOptions{})│
│                              │   container_execsync.go:87-92                           │
│                              │                                                          │
│                              └── c.execInternal(ctx, cntr.Container, id, opts)        │
│                                  │   container_execsync.go:274-291                     │
│                                  │                                                      │
│  [containerd Client/Task 层]                                                           │
│                                  ├── container.Task(ctx, nil)                         │
│                                  │   └── 获取容器的 Task 对象                          │
│                                  │                                                      │
│                                  ├── task.Exec(ctx, execID, pspec, ioCreator)         │
│                                  │   │   container_execsync.go:165-188                 │
│                                  │   │   containerd/client/task.go:115                 │
│                                  │   │                                                  │
│                                  │   └── 创建 exec 进程配置                            │
│                                  │       ├── 生成唯一 execID                           │
│                                  │       ├── 设置进程 spec (args, env, tty)           │
│                                  │       └── 创建 IO (Fifo/Streaming)                 │
│                                  │                                                      │
│                                  ├── process.Wait(ctx)                                 │
│                                  │   └── 等待进程退出通道                              │
│                                  │                                                      │
│                                  ├── process.Start(ctx)                                │
│                                  │   │   container_execsync.go:204                     │
│                                  │   └── 启动 exec 进程                                │
│                                  │                                                      │
│                                  └── select { case exitRes := <-exitCh }              │
│                                      │   container_execsync.go:232-258                 │
│                                      │                                                  │
│                                      ├── [超时处理]                                    │
│                                      │   └── process.Kill(ctx, SIGKILL)               │
│                                      │                                                  │
│                                      └── [正常退出]                                    │
│                                          └── 返回 exitCode, stdout, stderr            │
│                                                                                         │
│  [containerd-shim-runc-v2 层]                                                          │
│  TaskService.Exec()                                                                    │
│  │   containerd/cmd/containerd-shim-runc-v2/task/service.go                           │
│  │                                                                                      │
│  └── container.NewExec()                                                               │
│      │   containerd/cmd/containerd-shim-runc-v2/runc/container.go                     │
│      │                                                                                  │
│      └── runc.Exec(ctx, id, spec, opts)                                               │
│          │   containerd/vendor/github.com/containerd/go-runc/runc.go                  │
│          │                                                                              │
│          └── [系统调用] exec.Command("runc", "exec", ...)                             │
│              │                                                                          │
│              └── runc exec <container-id> <command>                                   │
│                  │                                                                      │
│                  └── [进入容器 Namespace]                                             │
│                      ├── setns(PID namespace)                                         │
│                      ├── setns(Network namespace)                                     │
│                      ├── setns(Mount namespace)                                       │
│                      ├── setns(UTS namespace)                                         │
│                      ├── setns(IPC namespace)                                         │
│                      ├── 加入容器 cgroup                                              │
│                      └── execve(command)  // 执行用户命令                             │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 5.1 关键源码解析

**Kubelet RunInContainer 实现**:

```go
// 文件: pkg/kubelet/kuberuntime/kuberuntime_container.go:1182-1188
func (m *kubeGenericRuntimeManager) RunInContainer(ctx context.Context, 
    id kubecontainer.ContainerID, cmd []string, timeout time.Duration) ([]byte, error) {
    // 调用 CRI ExecSync 接口
    stdout, stderr, err := m.runtimeService.ExecSync(ctx, id.ID, cmd, timeout)
    // 合并 stdout 和 stderr
    return append(stdout, stderr...), err
}
```

**containerd execInternal 核心逻辑**:

```go
// 文件: containerd/internal/cri/server/container_execsync.go:115-259
func (c *criService) execInternal(ctx context.Context, container containerd.Container, 
    id string, opts execOptions) (*uint32, error) {
    
    // 1. 获取容器 spec 和 task
    spec, err := container.Spec(ctx)
    task, err := container.Task(ctx, nil)
    
    // 2. 准备 exec 进程配置
    pspec := spec.Process
    pspec.Args = opts.cmd
    pspec.Terminal = opts.tty
    
    // 3. 创建 exec 进程
    execID := util.GenerateID()
    process, err := task.Exec(ctx, execID, pspec, ioCreator)
    
    // 4. 等待并启动进程
    exitCh, err := process.Wait(ctx)
    err = process.Start(ctx)
    
    // 5. 处理超时和退出
    select {
    case <-execCtx.Done():  // 超时
        process.Kill(ctx, syscall.SIGKILL)
        return nil, fmt.Errorf("timeout exceeded")
    case exitRes := <-exitCh:  // 正常退出
        code, _, err := exitRes.Result()
        return &code, nil
    }
}
```

### 6. HTTPGet Handler 实现

**文件**: `pkg/kubelet/lifecycle/handlers.go:143-210`

```go
func (hr *handlerRunner) runHTTPHandler(ctx context.Context, pod *v1.Pod, 
    container *v1.Container, handler *v1.LifecycleHandler, 
    eventRecorder record.EventRecorder) error {
    
    host := handler.HTTPGet.Host
    podIP := host
    if len(host) == 0 {
        // 如果没有指定 host，使用 Pod IP
        status, err := hr.containerManager.GetPodStatus(ctx, pod.UID, pod.Name, pod.Namespace)
        if err != nil {
            klog.ErrorS(err, "Unable to get pod info, event handlers may be invalid.", ...)
            return err
        }
        if len(status.IPs) == 0 {
            return fmt.Errorf("failed to find networking container: %v", status)
        }
        host = status.IPs[0]
        podIP = host
    }

    // 使用新的一致性 HTTP 请求构建（如果启用特性门）
    if utilfeature.DefaultFeatureGate.Enabled(features.ConsistentHTTPGetHandlers) {
        req, err := httpprobe.NewRequestForHTTPGetAction(handler.HTTPGet, container, podIP, "lifecycle")
        if err != nil {
            return err
        }
        resp, err := hr.httpDoer.Do(req)
        discardHTTPRespBody(resp)

        // HTTPS 降级到 HTTP 的回退逻辑
        if isHTTPResponseError(err) {
            req := req.Clone(context.Background())
            req.URL.Scheme = "http"
            req.Header.Del("Authorization")
            resp, httpErr := hr.httpDoer.Do(req)
            if httpErr == nil {
                metrics.LifecycleHandlerHTTPFallbacks.Inc()
                eventRecorder.Event(pod, v1.EventTypeWarning, "LifecycleHTTPFallback", ...)
                err = nil
            }
            discardHTTPRespBody(resp)
        }
        return err
    }

    // 旧的代码路径
    port, err := resolvePort(handler.HTTPGet.Port, container)
    url := fmt.Sprintf("http://%s/%s", net.JoinHostPort(host, strconv.Itoa(port)), handler.HTTPGet.Path)
    req, err := http.NewRequest(http.MethodGet, url, nil)
    resp, err := hr.httpDoer.Do(req)
    discardHTTPRespBody(resp)
    return err
}
```

### 6.1 Sleep Handler 实现

**文件**: `pkg/kubelet/lifecycle/handlers.go:129-141`

```go
func (hr *handlerRunner) runSleepHandler(ctx context.Context, seconds int64) error {
    // 需要启用 PodLifecycleSleepAction 特性门
    if !utilfeature.DefaultFeatureGate.Enabled(features.PodLifecycleSleepAction) {
        return nil  // 特性未启用时静默返回
    }
    c := time.After(time.Duration(seconds) * time.Second)
    select {
    case <-ctx.Done():
        // 容器在 sleep 完成前被终止
        return fmt.Errorf("container terminated before sleep hook finished")
    case <-c:
        return nil  // sleep 完成
    }
}
```

### 7. Kubelet 内部生命周期 Hook

除了用户可配置的 `PostStart`/`PreStop` Hook，Kubelet 还有一套内部容器生命周期钩子用于资源管理。

**文件**: `pkg/kubelet/cm/internal_container_lifecycle.go`

```go
// InternalContainerLifecycle 接口 - 用于 CPU/Memory/Topology Manager
type InternalContainerLifecycle interface {
    // 在容器创建前调用 - 设置 CRI 容器配置
    PreCreateContainer(pod *v1.Pod, container *v1.Container, 
        containerConfig *runtimeapi.ContainerConfig) error
    
    // 在容器启动前调用 - 分配 CPU/Memory 资源
    PreStartContainer(pod *v1.Pod, container *v1.Container, containerID string) error
    
    // 在容器停止后调用 - 释放资源
    PostStopContainer(containerID string) error
}

// 实现
type internalContainerLifecycleImpl struct {
    cpuManager      cpumanager.Manager      // CPU 管理器
    memoryManager   memorymanager.Manager   // 内存管理器
    topologyManager topologymanager.Manager // NUMA 拓扑管理器
}

func (i *internalContainerLifecycleImpl) PreStartContainer(pod *v1.Pod, 
    container *v1.Container, containerID string) error {
    // 将容器添加到各个资源管理器
    if i.cpuManager != nil {
        i.cpuManager.AddContainer(pod, container, containerID)
    }
    if i.memoryManager != nil {
        i.memoryManager.AddContainer(pod, container, containerID)
    }
    i.topologyManager.AddContainer(pod, container, containerID)
    return nil
}

func (i *internalContainerLifecycleImpl) PostStopContainer(containerID string) error {
    // 从拓扑管理器中移除容器
    return i.topologyManager.RemoveContainer(containerID)
}
```

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│              **用户 Hook vs 内部 Hook 对比**                                              │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  ┌────────────────────────────┐     ┌────────────────────────────┐                      │
│  │     用户定义 Hook          │     │     内部生命周期 Hook      │                      │
│  │  (PostStart/PreStop)       │     │  (InternalContainerLifecycle)                    │
│  ├────────────────────────────┤     ├────────────────────────────┤                      │
│  │  在 YAML 中配置            │     │  Kubelet 自动处理          │                      │
│  │  容器内执行 (Exec/HTTP)    │     │  在 Kubelet 进程内执行     │                      │
│  │  应用级初始化/清理         │     │  资源级管理 (CPU/Memory)   │                      │
│  │  可能失败并影响容器        │     │  通常不会失败              │                      │
│  └────────────────────────────┘     └────────────────────────────┘                      │
│                                                                                         │
│  **执行顺序**:                                                                          │
│                                                                                         │
│  容器创建前:  PreCreateContainer() → 设置容器 CRI 配置                                  │
│       ↓                                                                                 │
│  容器启动前:  PreStartContainer() → 分配 CPU/Memory 到容器                              │
│       ↓                                                                                 │
│  容器启动后:  [用户 PostStart Hook] → 执行用户定义的初始化逻辑                          │
│       ↓                                                                                 │
│  容器运行中:  ...                                                                       │
│       ↓                                                                                 │
│  容器终止前:  [用户 PreStop Hook] → 执行用户定义的清理逻辑                              │
│       ↓                                                                                 │
│  容器停止后:  PostStopContainer() → 释放 CPU/Memory 资源                               │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 使用场景

### 1. PostStart 使用场景

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **PostStart Hook 适用场景**                                     │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. 注册服务**                                                                        │
│  - 向服务注册中心（如 Consul、Eureka）注册服务                                          │
│  - 通知负载均衡器添加节点                                                               │
│                                                                                         │
│  **2. 初始化配置**                                                                      │
│  - 从配置中心拉取配置                                                                   │
│  - 初始化数据库连接池                                                                   │
│  - 预热缓存                                                                             │
│                                                                                         │
│  **3. 发送通知**                                                                        │
│  - 通知监控系统容器已启动                                                               │
│  - 发送 Slack/邮件通知                                                                  │
│                                                                                         │
│  **4. 文件系统初始化**                                                                  │
│  - 创建必要的目录结构                                                                   │
│  - 设置文件权限                                                                         │
│  - 拷贝初始化文件                                                                       │
│                                                                                         │
│  **注意事项**:                                                                          │
│  - PostStart 与 ENTRYPOINT 并行执行，不保证顺序                                        │
│  - 如果需要保证顺序，考虑在 ENTRYPOINT 脚本中处理                                      │
│  - PostStart 失败会导致容器终止                                                        │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 2. PreStop 使用场景

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **PreStop Hook 适用场景**                                       │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. 优雅关闭 Web 服务器**                                                             │
│  lifecycle:                                                                             │
│    preStop:                                                                             │
│      exec:                                                                              │
│        command:                                                                         │
│        - /bin/sh                                                                        │
│        - -c                                                                             │
│        - |                                                                              │
│          # 告诉 nginx 优雅关闭                                                          │
│          nginx -s quit                                                                  │
│          # 等待所有请求处理完成                                                         │
│          while killall -0 nginx 2>/dev/null; do                                        │
│            sleep 1                                                                      │
│          done                                                                           │
│                                                                                         │
│  **2. 服务注销**                                                                        │
│  - 从服务注册中心注销                                                                   │
│  - 通知负载均衡器移除节点                                                               │
│  - 确保不再接收新请求                                                                   │
│                                                                                         │
│  **3. 数据持久化**                                                                      │
│  - 刷新内存数据到磁盘                                                                   │
│  - 完成正在进行的事务                                                                   │
│  - 写入 checkpoint                                                                      │
│                                                                                         │
│  **4. 清理资源**                                                                        │
│  - 释放分布式锁                                                                         │
│  - 关闭长连接                                                                           │
│  - 清理临时文件                                                                         │
│                                                                                         │
│  **5. 等待流量排空 (Connection Draining)**                                              │
│  lifecycle:                                                                             │
│    preStop:                                                                             │
│      sleep:                                                                             │
│        seconds: 15  # 等待 Service endpoint 更新传播                                   │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 3. 经典配置示例

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-app
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      terminationGracePeriodSeconds: 60  # 总优雅期
      containers:
      - name: app
        image: nginx:1.21
        ports:
        - containerPort: 80
        
        lifecycle:
          postStart:
            exec:
              command:
              - /bin/sh
              - -c
              - |
                # 等待应用就绪
                until curl -s http://localhost/health > /dev/null; do
                  sleep 1
                done
                # 注册到服务发现
                curl -X POST http://consul:8500/v1/agent/service/register \
                  -d '{"Name":"web","Port":80}'
                  
          preStop:
            exec:
              command:
              - /bin/sh
              - -c
              - |
                # 从服务发现注销
                curl -X PUT http://consul:8500/v1/agent/service/deregister/web
                # 等待连接排空
                sleep 10
                # 优雅关闭 nginx
                nginx -s quit
                # 等待 nginx 完全停止
                while killall -0 nginx 2>/dev/null; do sleep 1; done
                
        # 探针配置
        readinessProbe:
          httpGet:
            path: /health
            port: 80
          initialDelaySeconds: 5
          periodSeconds: 5
          
        livenessProbe:
          httpGet:
            path: /health
            port: 80
          initialDelaySeconds: 15
          periodSeconds: 10
```

---

## 最佳实践

### 1. 时间规划

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **优雅终止时间规划**                                            │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  terminationGracePeriodSeconds = PreStop时间 + SIGTERM处理时间 + 安全余量               │
│                                                                                         │
│  示例计算:                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────────────────┐   │
│  │                                                                                  │   │
│  │  PreStop Hook:                    15 秒 (服务注销 + 连接排空)                    │   │
│  │  SIGTERM 处理:                    20 秒 (处理正在进行的请求)                     │   │
│  │  安全余量:                        5 秒                                          │   │
│  │  ─────────────────────────────────────────────────────────                      │   │
│  │  terminationGracePeriodSeconds:  40 秒                                          │   │
│  │                                                                                  │   │
│  └─────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                         │
│  **注意**:                                                                              │
│  - 最小 gracePeriod 为 2 秒（硬编码）                                                  │
│  - PreStop 执行时间会从 gracePeriod 中扣除                                             │
│  - 不要让 PreStop 占用过多时间                                                         │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 2. PreStop 最佳实践

```yaml
# ✅ 好的做法
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 5"]  # 简短的等待
      
# ❌ 不好的做法
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 300"]  # 太长！会占用所有 gracePeriod
```

### 3. 错误处理

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Hook 错误处理建议**                                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **PostStart 错误处理**:                                                                │
│  - PostStart 失败会导致容器被杀死                                                      │
│  - 应该编写幂等的初始化逻辑                                                            │
│  - 使用重试机制处理临时失败                                                            │
│  - 设置合理的超时                                                                      │
│                                                                                         │
│  示例:                                                                                  │
│  command:                                                                               │
│  - /bin/sh                                                                              │
│  - -c                                                                                   │
│  - |                                                                                    │
│    for i in $(seq 1 5); do                                                             │
│      if curl -s http://config-server/init; then                                        │
│        exit 0                                                                           │
│      fi                                                                                 │
│      sleep 2                                                                            │
│    done                                                                                 │
│    exit 1  # 重试失败                                                                   │
│                                                                                         │
│  **PreStop 错误处理**:                                                                  │
│  - PreStop 失败不会阻止容器终止                                                        │
│  - 但会生成告警事件                                                                    │
│  - 应该处理所有可能的错误                                                              │
│  - 设置合理的超时防止阻塞                                                              │
│                                                                                         │
│  示例:                                                                                  │
│  command:                                                                               │
│  - /bin/sh                                                                              │
│  - -c                                                                                   │
│  - |                                                                                    │
│    # 即使注销失败也继续执行                                                             │
│    curl -X PUT http://consul/deregister || true                                        │
│    # 等待连接排空                                                                       │
│    sleep 10                                                                             │
│    # 优雅关闭                                                                           │
│    kill -QUIT 1 || true                                                                │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 故障排查

### 1. 查看 Hook 相关事件

```bash
# 查看 Pod 事件
kubectl describe pod <pod-name> | grep -A 5 "Events:"

# 常见 Hook 相关事件:
# - FailedPostStartHook: PostStart 执行失败
# - FailedPreStopHook: PreStop 执行失败
```

### 2. 查看 kubelet 日志

```bash
# 查看 kubelet 日志中的 Hook 信息
journalctl -u kubelet | grep -E "preStop|postStart|hook" -i

# 或者
journalctl -u kubelet | grep "lifecycle"
```

### 3. 常见问题

| 问题 | 原因 | 解决方案 |
|:-----|:-----|:---------|
| PostStart 超时 | Hook 执行时间过长 | 优化脚本或增加 gracePeriod |
| PreStop 未执行 | 容器已崩溃 | 确保容器健康时才删除 |
| Pod 卡在 Terminating | PreStop 阻塞 | 检查 Hook 脚本，添加超时 |
| HTTP Hook 失败 | 端口/路径错误 | 检查容器内服务是否可达 |
| Exec Hook 失败 | 命令不存在 | 确保镜像包含所需命令 |

### 4. 调试技巧

```bash
# 手动执行 Hook 命令测试
kubectl exec <pod-name> -- /bin/sh -c "你的hook命令"

# 查看容器内文件系统
kubectl exec <pod-name> -- ls -la /path/to/script

# 检查网络连通性（HTTPGet Hook）
kubectl exec <pod-name> -- curl -v http://localhost:8080/health
```

---

## Hook 与 Pod 删除流程的关系

### PreStop Hook 在删除流程中的位置

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                      **Pod 删除流程与 PreStop Hook**                                     │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  kubectl delete pod ───────────────────────────────────────────────────────────────▶   │
│                                                                                         │
│  1. API Server                                                                         │
│     └── 设置 pod.DeletionTimestamp                                                     │
│         └── 开始 terminationGracePeriodSeconds 倒计时                                  │
│                                                                                         │
│  2. Kubelet 收到删除事件                                                               │
│     └── podWorker.UpdatePod()                                                          │
│         └── SyncTerminatingPod()                                                       │
│             │                                                                          │
│             ├── probeManager.StopLivenessAndStartup()  // 停止 liveness/startup 探针  │
│             │                                                                          │
│             └── KillPod()                                                              │
│                 └── killPodWithSyncResult()                                            │
│                     │                                                                  │
│                     ├── [并行] killContainersWithSyncResult()                         │
│                     │   │                                                              │
│                     │   └── [每个容器] killContainer()                                │
│                     │       │                                                          │
│                     │       ├── **执行 PreStop Hook** ─────────────────────────┐       │
│                     │       │   └── executePreStopHook()                       │       │
│                     │       │       └── runner.Run(Exec/HTTP/Sleep)            │       │
│                     │       │                                                  │       │
│                     │       └── runtimeService.StopContainer()  ◄─────────────┘       │
│                     │           ├── SIGTERM                                           │
│                     │           └── [等待 gracePeriod 后] SIGKILL                     │
│                     │                                                                  │
│                     └── StopPodSandbox()                                              │
│                                                                                         │
│  3. SyncTerminatedPod()                                                                │
│     └── 清理资源、卸载卷、删除 Pod                                                     │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### Hook 与探针的关系

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                      **Hook 与探针的交互**                                               │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **启动阶段**:                                                                          │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │                                                                                   │  │
│  │  容器创建 → PostStart Hook 开始 → PostStart 完成 → 探针开始                       │  │
│  │                                                    │                              │  │
│  │                                                    ├── Startup Probe (如果配置)   │  │
│  │                                                    ├── Liveness Probe             │  │
│  │                                                    └── Readiness Probe            │  │
│  │                                                                                   │  │
│  │  ⚠️ 关键: PostStart Hook 阻塞所有探针的启动                                       │  │
│  │     如果 PostStart 执行很长时间，探针不会开始检测                                 │  │
│  │                                                                                   │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  **终止阶段**:                                                                          │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │                                                                                   │  │
│  │  删除请求 → 停止 Liveness/Startup 探针 → PreStop Hook → SIGTERM                  │  │
│  │                    │                                                              │  │
│  │                    │   Readiness 探针继续运行 ────────────┐                       │  │
│  │                    │   (允许服务发现尽快移除 endpoint)    │                       │  │
│  │                    │                                      │                       │  │
│  │                    └──────────────────────────────────────┴─▶ 容器终止后停止     │  │
│  │                                                                                   │  │
│  │  **为什么 Readiness 探针最后停止?**                                               │  │
│  │  - 允许 Service/Endpoint Controller 尽快感知 Pod 不可用                          │  │
│  │  - 配合 PreStop Hook 实现流量排空                                                │  │
│  │  - 确保新请求不会路由到正在终止的 Pod                                            │  │
│  │                                                                                   │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 优雅终止最佳实践配置

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: graceful-shutdown-demo
spec:
  terminationGracePeriodSeconds: 60  # 总优雅期
  containers:
  - name: app
    image: nginx:1.21
    
    # Readiness 探针 - 用于流量排空
    readinessProbe:
      httpGet:
        path: /health
        port: 8080
      periodSeconds: 5
      
    lifecycle:
      preStop:
        exec:
          command:
          - /bin/sh
          - -c
          - |
            # 1. 标记应用不再接收新请求（可选）
            touch /tmp/shutdown
            
            # 2. 等待 Service endpoint 更新传播（关键！）
            #    给 kube-proxy/Ingress Controller 时间更新规则
            sleep 10
            
            # 3. 等待现有请求处理完成
            while [ $(netstat -an | grep ESTABLISHED | wc -l) -gt 0 ]; do
              sleep 1
            done
            
            # 4. 可选：通知外部系统
            # curl -X POST http://monitoring/shutdown-event
            
    # 应用应该正确处理 SIGTERM 信号
    # 大多数 Web 框架都支持优雅关闭
```

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                      **优雅终止时间分配示例**                                            │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  terminationGracePeriodSeconds = 60s                                                   │
│                                                                                         │
│  ┌────────────────────────────────────────────────────────────────────────────────┐    │
│  │    PreStop Hook                      SIGTERM 处理           安全余量           │    │
│  │  ├─────────────────────────────────┼──────────────────────┼────────────────┤   │    │
│  │  │                                 │                      │                │   │    │
│  │  │  sleep 10s (endpoint 传播)      │  应用优雅关闭        │  缓冲          │   │    │
│  │  │  + 请求排空 (~10s)              │  (~30s)              │  (~10s)        │   │    │
│  │  │                                 │                      │                │   │    │
│  │  ├─────────────────────────────────┼──────────────────────┼────────────────┤   │    │
│  │  │            ~20s                 │         ~30s         │     ~10s       │   │    │
│  │  │                                 │                      │                │   │    │
│  └────────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 总结

### Hook 设计原则

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Hook 设计原则总结**                                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. 快速执行**                                                                        │
│  - Hook 应该尽可能快地完成                                                              │
│  - 避免长时间阻塞操作                                                                   │
│  - 如果需要异步操作，考虑发送信号而不是等待完成                                         │
│                                                                                         │
│  **2. 幂等性**                                                                          │
│  - Hook 可能被重复执行（Pod 重启）                                                      │
│  - 确保多次执行不会产生副作用                                                           │
│                                                                                         │
│  **3. 错误处理**                                                                        │
│  - PostStart 失败会终止容器                                                            │
│  - PreStop 失败不阻止终止，但应该处理                                                  │
│  - 使用 `|| true` 忽略非关键错误                                                       │
│                                                                                         │
│  **4. 时间规划**                                                                        │
│  - 合理设置 terminationGracePeriodSeconds                                              │
│  - PreStop 时间从 gracePeriod 中扣除                                                   │
│  - 最小保证 2 秒给 SIGTERM                                                             │
│                                                                                         │
│  **5. 监控和告警**                                                                      │
│  - 监控 Hook 执行时间                                                                  │
│  - 配置 FailedPostStartHook/FailedPreStopHook 事件告警                                │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

*本文档基于 Kubernetes 源码分析，详细介绍了 Hook 机制的架构、实现和最佳实践。*
