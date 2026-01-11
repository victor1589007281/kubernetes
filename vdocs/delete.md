# Kubernetes Pod 删除流程深度分析

## 目录
1. [整体架构图](#整体架构图)
2. [时序图](#时序图)
3. [核心函数调用链](#核心函数调用链)
4. [各组件详细流程分析](#各组件详细流程分析)
5. [操作系统原理](#操作系统原理)
6. [常见问题分析](#常见问题分析)
7. [如何复用 Pod 的关联资源](#如何复用-pod-的关联资源)

---

## 整体架构图

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                           **Kubernetes Pod 删除流程架构图**                               │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  ┌─────────────┐                                                                        │
│  │  **kubectl** │ ───────────────── DELETE /api/v1/namespaces/{ns}/pods/{name} ────────▶│
│  └─────────────┘                                                                        │
│         │                                                                               │
│         ▼                                                                               │
│  ┌──────────────────────────────────────────────────────────────────────┐               │
│  │                        **kube-apiserver**                            │               │
│  │  ┌─────────────────┐    ┌────────────────┐    ┌─────────────────┐   │               │
│  │  │ Authentication  │───▶│ Authorization  │───▶│ Admission       │   │               │
│  │  └─────────────────┘    └────────────────┘    │ Controllers     │   │               │
│  │                                                └────────┬────────┘   │               │
│  │                                                         │            │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │                   **Pod Registry**                            │  │               │
│  │  │  1. CheckGracefulDelete() - 计算 gracePeriod                  │  │               │
│  │  │  2. 设置 DeletionTimestamp                                    │  │               │
│  │  │  3. 设置 DeletionGracePeriodSeconds                           │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  └─────────────────────────────────────────────────────────────────────┘               │
│         │                                                                               │
│         ▼                                                                               │
│  ┌─────────────┐                                                                        │
│  │   **etcd**  │ ◄──── Pod.DeletionTimestamp != nil (软删除)                            │
│  └─────────────┘                                                                        │
│         │                                                                               │
│         │ Watch 机制                                                                    │
│         ▼                                                                               │
│  ┌──────────────────────────────────────────────────────────────────────┐               │
│  │                          **kubelet**                                 │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │                    **Pod Workers**                            │  │               │
│  │  │  1. 检测 DeletionTimestamp                                    │  │               │
│  │  │  2. 状态转换: SyncPod → TerminatingPod → TerminatedPod        │  │               │
│  │  │  3. 计算有效 gracePeriod                                      │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  │         │                                                            │               │
│  │         ▼                                                            │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │               **SyncTerminatingPod**                          │  │               │
│  │  │  1. 停止健康检查 (probeManager.StopLivenessAndStartup)        │  │               │
│  │  │  2. killPod() - 杀死所有容器                                  │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  │         │                                                            │               │
│  │         ▼                                                            │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │              **kubeGenericRuntimeManager**                    │  │               │
│  │  │  ┌─────────────────────────────────────────────────────────┐ │  │               │
│  │  │  │ killContainersWithSyncResult() - 并行杀死所有容器       │ │  │               │
│  │  │  │   └─▶ killContainer()                                   │ │  │               │
│  │  │  │         ├─▶ executePreStopHook() - 执行 PreStop 钩子    │ │  │               │
│  │  │  │         └─▶ runtimeService.StopContainer()              │ │  │               │
│  │  │  └─────────────────────────────────────────────────────────┘ │  │               │
│  │  │  ┌─────────────────────────────────────────────────────────┐ │  │               │
│  │  │  │ StopPodSandbox() - 停止 Pod 沙箱                        │ │  │               │
│  │  │  └─────────────────────────────────────────────────────────┘ │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  │         │                                                            │               │
│  │         ▼                                                            │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │               **SyncTerminatedPod**                           │  │               │
│  │  │  1. volumeManager.WaitForUnmount() - 等待卷卸载               │  │               │
│  │  │  2. 等待卷路径清理                                            │  │               │
│  │  │  3. secretManager.UnregisterPod()                             │  │               │
│  │  │  4. configMapManager.UnregisterPod()                          │  │               │
│  │  │  5. pcm.Destroy() - 删除 cgroups                              │  │               │
│  │  │  6. statusManager.TerminatePod() - 标记终止状态               │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  │         │                                                            │               │
│  │         ▼                                                            │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │                 **Status Manager**                            │  │               │
│  │  │  1. syncPod() - 同步状态到 API Server                         │  │               │
│  │  │  2. canBeDeleted() - 检查是否可以删除                         │  │               │
│  │  │  3. 发送最终 DELETE 请求 (GracePeriodSeconds=0)               │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  └──────────────────────────────────────────────────────────────────────┘               │
│         │                                                                               │
│         │ CRI (Container Runtime Interface)                                             │
│         ▼                                                                               │
│  ┌──────────────────────────────────────────────────────────────────────┐               │
│  │                    **Container Runtime**                             │               │
│  │                   (containerd / CRI-O)                               │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │ StopContainer:                                                │  │               │
│  │  │   1. 发送 SIGTERM 信号                                        │  │               │
│  │  │   2. 等待 gracePeriod                                         │  │               │
│  │  │   3. 超时后发送 SIGKILL 信号                                  │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  │  ┌───────────────────────────────────────────────────────────────┐  │               │
│  │  │ RemoveContainer (GC阶段):                                     │  │               │
│  │  │   1. 清理容器文件系统                                         │  │               │
│  │  │   2. 清理容器日志                                             │  │               │
│  │  └───────────────────────────────────────────────────────────────┘  │               │
│  └──────────────────────────────────────────────────────────────────────┘               │
│         │                                                                               │
│         ▼                                                                               │
│  ┌──────────────────────────────────────────────────────────────────────┐               │
│  │                    **Linux Kernel**                                  │               │
│  │  ┌─────────────────────────────────────────────────────────────────┐│               │
│  │  │ 进程终止:                                                       ││               │
│  │  │   SIGTERM → 进程处理信号 → 正常退出                            ││               │
│  │  │   SIGKILL → 内核强制终止 (D状态进程例外)                       ││               │
│  │  ├─────────────────────────────────────────────────────────────────┤│               │
│  │  │ Cgroups 清理:                                                   ││               │
│  │  │   - 释放 CPU/Memory 限制                                       ││               │
│  │  │   - 终止 cgroup 内所有进程                                     ││               │
│  │  ├─────────────────────────────────────────────────────────────────┤│               │
│  │  │ Namespace 清理:                                                 ││               │
│  │  │   - PID namespace                                              ││               │
│  │  │   - Network namespace                                          ││               │
│  │  │   - Mount namespace                                            ││               │
│  │  └─────────────────────────────────────────────────────────────────┘│               │
│  └──────────────────────────────────────────────────────────────────────┘               │
│         │                                                                               │
│         ▼                                                                               │
│  ┌─────────────┐                                                                        │
│  │   **etcd**  │ ◄──── 最终删除 Pod 对象 (硬删除)                                       │
│  └─────────────┘                                                                        │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 时序图

```
┌────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                **Pod 删除时序图**                                                   │
├────────────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                                    │
│  User      kubectl    API Server     etcd       kubelet      RuntimeManager   ContainerRuntime     │
│   │           │           │           │            │               │                │              │
│   │──delete───▶           │           │            │               │                │              │
│   │           │──DELETE───▶           │            │               │                │              │
│   │           │           │           │            │               │                │              │
│   │           │    ┌──────┴──────┐    │            │               │                │              │
│   │           │    │CheckGraceful│    │            │               │                │              │
│   │           │    │   Delete    │    │            │               │                │              │
│   │           │    └──────┬──────┘    │            │               │                │              │
│   │           │           │           │            │               │                │              │
│   │           │    ┌──────┴──────┐    │            │               │                │              │
│   │           │    │设置删除时间戳│    │            │               │                │              │
│   │           │    │DeletionTime │    │            │               │                │              │
│   │           │    └──────┬──────┘    │            │               │                │              │
│   │           │           │──PUT──────▶            │               │                │              │
│   │           │           │           │            │               │                │              │
│   │◀──200 OK──│◀──────────│           │            │               │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │──Watch────▶│               │                │              │
│   │           │           │           │    事件     │               │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │     ┌──────┴──────┐        │                │              │
│   │           │           │           │     │ UpdatePod   │        │                │              │
│   │           │           │           │     │检测删除时间戳│        │                │              │
│   │           │           │           │     └──────┬──────┘        │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │     ┌──────┴──────┐        │                │              │
│   │           │           │           │     │状态转换:    │        │                │              │
│   │           │           │           │     │SyncPod →    │        │                │              │
│   │           │           │           │     │TerminatingPod        │                │              │
│   │           │           │           │     └──────┬──────┘        │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │     ┌──────┴──────┐        │                │              │
│   │           │           │           │     │SyncTerminating       │                │              │
│   │           │           │           │     │    Pod      │        │                │              │
│   │           │           │           │     └──────┬──────┘        │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │──KillPod─────▶│                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │        ┌──────┴──────┐         │              │
│   │           │           │           │            │        │killContainers         │              │
│   │           │           │           │            │        │WithSyncResult         │              │
│   │           │           │           │            │        └──────┬──────┘         │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │               │──PreStop Hook──▶              │
│   │           │           │           │            │               │◀───完成────────│              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │               │──StopContainer─▶              │
│   │           │           │           │            │               │    (SIGTERM)   │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │               │ ...等待gracePeriod...         │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │               │──StopContainer─▶              │
│   │           │           │           │            │               │    (SIGKILL)   │              │
│   │           │           │           │            │               │◀──容器退出────│              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │               │──StopPodSandbox▶              │
│   │           │           │           │            │               │◀───完成────────│              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │◀──────────────│                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │     ┌──────┴──────┐        │                │              │
│   │           │           │           │     │SyncTerminated        │                │              │
│   │           │           │           │     │    Pod      │        │                │              │
│   │           │           │           │     └──────┬──────┘        │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │     ┌──────┴──────┐        │                │              │
│   │           │           │           │     │等待Volume   │        │                │              │
│   │           │           │           │     │  Unmount    │        │                │              │
│   │           │           │           │     └──────┬──────┘        │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │     ┌──────┴──────┐        │                │              │
│   │           │           │           │     │清理Cgroups  │        │                │              │
│   │           │           │           │     └──────┬──────┘        │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │     ┌──────┴──────┐        │                │              │
│   │           │           │           │     │StatusManager│        │                │              │
│   │           │           │           │     │TerminatePod │        │                │              │
│   │           │           │           │     └──────┬──────┘        │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │──PATCH status─▶               │              │
│   │           │           │◀──────────│────────────│               │                │              │
│   │           │           │           │            │               │                │              │
│   │           │           │           │            │──DELETE───────▶               │              │
│   │           │           │           │            │ (gracePeriod=0)│               │              │
│   │           │           │──────────▶│◀───────────│               │                │              │
│   │           │           │  最终删除  │            │               │                │              │
│   │           │           │           │            │               │                │              │
│                                                                                                    │
└────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 核心函数调用链（按 tech.md 格式）

### Pod 删除完整函数调用链

```
kubectl delete pod <name> - staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go:143
├── NewCmdDelete() - staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go:143
│   └── 构造 DeleteOptions
│       └── ┌──────────────────┬─────────────────────────────────┬──────────────────────────────┐
│           │  字段             │  默认值                         │  说明                        │
│           ├──────────────────┼─────────────────────────────────┼──────────────────────────────┤
│           │  GracePeriod     │  -1 (使用Pod默认值)             │  优雅删除周期                │
│           ├──────────────────┼─────────────────────────────────┼──────────────────────────────┤
│           │  ForceDeletion   │  false                          │  --force 标志                │
│           ├──────────────────┼─────────────────────────────────┼──────────────────────────────┤
│           │  CascadingStrategy│ Background                     │  级联删除策略                │
│           ├──────────────────┼─────────────────────────────────┼──────────────────────────────┤
│           │  WaitForDeletion │  true                           │  是否等待删除完成            │
│           └──────────────────┴─────────────────────────────────┴──────────────────────────────┘
├── RunDelete() - staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go:317
│   └── DeleteResult() - staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go:364
│       └── DynamicClient.Resource().Delete()
│           └── 发送 HTTP DELETE /api/v1/namespaces/{ns}/pods/{name}
│
├── [API Server 处理阶段] - vendor/k8s.io/apiserver/pkg/registry/generic/registry/store.go:1050
│   └── Store.Delete(ctx, name, deleteValidation, options)
│       │
│       ├── 1. 获取对象 - store.go:1057
│       │   └── e.Storage.Get(ctx, key, storage.GetOptions{}, obj)
│       │       └── 从 etcd 获取当前 Pod 对象
│       │
│       ├── 2. 验证前置条件 - store.go:1065-1068
│       │   ├── options.Preconditions.UID - 验证 UID 是否匹配
│       │   └── options.Preconditions.ResourceVersion - 验证版本是否匹配
│       │
│       ├── 3. rest.BeforeDelete() - vendor/k8s.io/apiserver/pkg/registry/rest/delete.go:75
│       │   │   参数: (strategy, ctx, obj, options) → (graceful, gracefulPending, err)
│       │   │
│       │   ├── 3.1 验证 DeleteOptions - delete.go:80
│       │   │   └── validation.ValidateDeleteOptions(options)
│       │   │
│       │   ├── 3.2 检查是否已经在删除中 - delete.go:108
│       │   │   └── if objectMeta.GetDeletionTimestamp() != nil
│       │   │       ├── [已在删除中] 检查是否可以缩短 gracePeriod
│       │   │       │   └── return (graceful=false, gracefulPending=true, nil)
│       │   │       └── [DeletionGracePeriodSeconds==0] 立即删除
│       │   │           └── return (graceful=false, gracefulPending=false, nil)
│       │   │
│       │   ├── 3.3 CheckGracefulDelete() - pkg/registry/core/pod/strategy.go:165
│       │   │   │   (Pod 特有的优雅删除策略)
│       │   │   │
│       │   │   └── 计算 gracePeriod
│       │   │       └── ┌──────────────────┬─────────────────────────────────────────────────────────┐
│       │   │           │  条件             │  gracePeriod 值                                        │
│       │   │           ├──────────────────┼─────────────────────────────────────────────────────────┤
│       │   │           │  用户指定         │  options.GracePeriodSeconds                            │
│       │   │           ├──────────────────┼─────────────────────────────────────────────────────────┤
│       │   │           │  使用Pod默认      │  pod.Spec.TerminationGracePeriodSeconds (默认30秒)     │
│       │   │           ├──────────────────┼─────────────────────────────────────────────────────────┤
│       │   │           │  Pod未调度        │  0 (立即删除)                                          │
│       │   │           ├──────────────────┼─────────────────────────────────────────────────────────┤
│       │   │           │  Pod已终止        │  0 (立即删除)                                          │
│       │   │           └──────────────────┴─────────────────────────────────────────────────────────┘
│       │   │
│       │   ├── 3.4 设置删除时间戳 - delete.go:150-152
│       │   │   ├── objectMeta.SetDeletionTimestamp(&now)
│       │   │   │   └── **Pod 状态变化**: DeletionTimestamp 从 nil 变为当前时间+gracePeriod
│       │   │   └── objectMeta.SetDeletionGracePeriodSeconds(options.GracePeriodSeconds)
│       │   │
│       │   └── 3.5 增加 Generation - delete.go:157-159
│       │       └── if objectMeta.GetGeneration() > 0:
│       │           objectMeta.SetGeneration(objectMeta.GetGeneration() + 1)
│       │
│       ├── 4. 检查 Finalizers - store.go:1084
│       │   │   pendingFinalizers := len(accessor.GetFinalizers()) != 0
│       │   │-  updateForGracefulDeletionAndFinalizers() //更新设置deleteImmediately，默认true
│       │   └── **Finalizer 处理逻辑详解**:
│       │       │
│       │       ├── **什么是 Finalizer?**
│       │       │   ├── Finalizer 是 Kubernetes 的保护机制，确保资源删除前完成必要的清理
│       │       │   ├── 存储在 metadata.finalizers[] 字段中
│       │       │   └── 只有当 finalizers 为空时，对象才能被真正删除
│       │       │
│       │       ├── **有 Finalizer 时的 Pod 状态**:
│       │       │   │
│       │       │   │  ┌─────────────────────────────────────────────────────────────────────┐
│       │       │   │  │ **有 Finalizer 时 Pod 状态变化**                                   │
│       │       │   │  ├─────────────────────────────────────────────────────────────────────┤
│       │       │   │  │  字段                     │  删除前          │  删除后(有Finalizer)│
│       │       │   │  ├─────────────────────────────────────────────────────────────────────┤
│       │       │   │  │  metadata.deletionTimestamp │  nil             │  当前时间          │
│       │       │   │  │  metadata.deletionGracePeriodSeconds │  nil   │  gracePeriod值     │
│       │       │   │  │  metadata.finalizers[]     │  ["xxx/yyy"]     │  ["xxx/yyy"] (不变) │
│       │       │   │  │  status.phase              │  Running         │  Running (不变!)   │
│       │       │   │  │  **Pod 是否存在于 etcd**  │  是              │  **是** (未删除!)   │
│       │       │   │  │  **kubectl get pods 可见** │  是              │  **是** (Terminating)│
│       │       │   │  └─────────────────────────────────────────────────────────────────────┘
│       │       │   │
│       │       │   │  **关键理解**: 有 Finalizer 时:
│       │       │   │  ├── 1. Pod 对象仍然存在于 etcd 中！
│       │       │   │  ├── 2. Pod.Status.Phase 仍然是 Running（API Server 不改变 Phase）
│       │       │   │  ├── 3. kubectl get pods 显示状态为 "Terminating"
│       │       │   │  │      └── 这是 kubectl 根据 DeletionTimestamp!=nil 显示的
│       │       │   │  │      └── 不是真正的 Pod Phase
│       │       │   │  ├── 4. kubelet 仍然会收到 Watch 事件并处理删除
│       │       │   │  └── 5. 只有 Finalizer 全部移除后，Pod 才从 etcd 删除
│       │       │   │
│       │       │   ├── [有 Finalizer] 如 kubernetes.io/pvc-protection
│       │       │   │   ├── Pod 不会立即从 etcd 删除
│       │       │   │   ├── 只设置 DeletionTimestamp
│       │       │   │   ├── 等待 Controller 移除 Finalizer
│       │       │   │   │   └── PVC Protection Controller 检查 Pod 是否还在使用 PVC
│       │       │   │   │       └── pkg/controller/volume/pvcprotection/pvc_protection_controller.go:164
│       │       │   │   │           └── protectionutil.IsDeletionCandidate(pvc, volumeutil.PVCProtectionFinalizer)
│       │       │   │   │               ├── [PVC 正在被使用] 保留 Finalizer
│       │       │   │   │               └── [PVC 不再被使用] removeFinalizer() - 行200
│       │       │   │   └── Finalizer 全部移除后，再次触发删除
│       │       │   │
│       │       │   └── **Finalizer 移除后的处理流程**:
│       │       │       ├── 1. Controller Patch 移除 finalizer
│       │       │       ├── 2. API Server 检测到 finalizers 为空
│       │       │       ├── 3. 如果 DeletionTimestamp != nil 且 gracePeriod 已过
│       │       │       │      └── 触发最终删除，从 etcd 移除对象
│       │       │       └── 4. 或者等待 kubelet 通过 statusManager 发起最终删除
│       │       │
│       │       └── [无 Finalizer]
│       │           └── deleteImmediately = true (可以立即删除，但仍需等待优雅删除)
│       │
│       ├── 5. **级联删除策略处理** - store.go:887-923
│       │   │   deletionFinalizersForGarbageCollection(ctx, e, accessor, options)
│       │   │
│       │   │   **级联删除策略 (PropagationPolicy)**:
│       │   │   │
│       │   │   │  ┌─────────────────────────────────────────────────────────────────────────────┐
│       │   │   │  │ **kubectl delete --cascade 参数说明**                                       │
│       │   │   │  ├───────────────┬──────────────────────────────────────────────────────────────┤
│       │   │   │  │  策略          │  行为                                                       │
│       │   │   │  ├───────────────┼──────────────────────────────────────────────────────────────┤
│       │   │   │  │  background   │  **默认值**。立即删除owner，GC异步删除dependents              │
│       │   │   │  │  (默认)       │  Owner先从etcd删除，GarbageCollector后台清理子资源          │
│       │   │   │  │               │  不阻塞删除操作，子资源稍后被删除                           │
│       │   │   │  ├───────────────┼──────────────────────────────────────────────────────────────┤
│       │   │   │  │  foreground   │  先删除dependents，再删除owner                              │
│       │   │   │  │               │  在owner上设置 FinalizerDeleteDependents                    │
│       │   │   │  │               │  GC先删所有 BlockOwnerDeletion=true 的子资源                │
│       │   │   │  │               │  子资源全部删除后，GC移除finalizer，owner才被删除          │
│       │   │   │  │               │  **阻塞删除操作直到子资源全部删除**                         │
│       │   │   │  ├───────────────┼──────────────────────────────────────────────────────────────┤
│       │   │   │  │  orphan       │  只删除owner，保留dependents                                │
│       │   │   │  │               │  子资源的 ownerReferences 被清除                            │
│       │   │   │  │               │  子资源变成"孤儿"独立存在                                   │
│       │   │   │  └───────────────┴──────────────────────────────────────────────────────────────┘
│       │   │   │
│       │   │   ├── shouldOrphanDependents() - store.go:803-844
│       │   │   │   └── 检查是否应该孤立子资源
│       │   │   │       ├── options.PropagationPolicy == DeletePropagationOrphan → true
│       │   │   │       └── 或 accessor 有 FinalizerOrphanDependents
│       │   │   │
│       │   │   ├── shouldDeleteDependents() - store.go:846-885
│       │   │   │   └── 检查是否应该前台删除子资源
│       │   │   │       ├── options.PropagationPolicy == DeletePropagationForeground → true
│       │   │   │       └── 或 accessor 有 FinalizerDeleteDependents
│       │   │   │
│       │   │   └── 设置相应的 Finalizer
│       │   │       ├── [orphan 模式] 添加 metav1.FinalizerOrphanDependents
│       │   │       └── [foreground 模式] 添加 metav1.FinalizerDeleteDependents
│       │   │
│       │   │   **注意**: 对于 Pod 本身，级联删除主要影响其子资源（如 ReplicaSet 删除 Pod）
│       │   │   Pod 作为叶子节点，通常没有子资源需要级联删除
│       │   │
│       │   └── 源码: staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go:116
│       │       └── CascadingStrategy metav1.DeletionPropagation
│       │
│       ├── 6. updateForGracefulDeletionAndFinalizers() - store.go:1094
│       │   │   处理优雅删除和 Finalizers 的组合情况
│       │   │
│       │   └── Storage.GuaranteedUpdate() - 更新到 etcd
│       │       └── **etcd 操作**: 更新 Pod 对象
│       │           ├── 设置 metadata.deletionTimestamp
│       │           ├── 设置 metadata.deletionGracePeriodSeconds
│       │           └── 触发 Watch 事件通知 kubelet
│       │
│       └── 7. [如果 deleteImmediately=true] 最终删除 - store.go:1126
│           └── e.Storage.Delete(ctx, key, out, &preconditions, ...)
│               └── **etcd 操作**: DELETE /registry/pods/{namespace}/{name}
│
├── [Kubelet 处理阶段]
│   └── syncLoop() - pkg/kubelet/kubelet.go:2330
│       └── syncLoopIteration() - pkg/kubelet/kubelet.go:2404
│           └── case kubetypes.DELETE: - pkg/kubelet/kubelet.go:2432
│               └── handler.HandlePodUpdates() - pkg/kubelet/kubelet.go:2435
│                   └── UpdatePod() - pkg/kubelet/pod_workers.go:737
│                       ├── 检测 pod.DeletionTimestamp != nil - pkg/kubelet/pod_workers.go:857
│                       │   └── status.deleted = true
│                       │   └── status.terminatingAt = now
│                       │   └── becameTerminating = true
│                       ├── calculateEffectiveGracePeriod() - pkg/kubelet/pod_workers.go:980
│                       │   └── ┌──────────────────┬──────────────────────────────────────────────┐
│                       │       │  优先级           │  来源                                        │
│                       │       ├──────────────────┼──────────────────────────────────────────────┤
│                       │       │  1 (最高)         │  DeletionGracePeriodSeconds (API Server设置) │
│                       │       ├──────────────────┼──────────────────────────────────────────────┤
│                       │       │  2               │  KillPodOptions.Override (eviction等)        │
│                       │       ├──────────────────┼──────────────────────────────────────────────┤
│                       │       │  3               │  Pod.Spec.TerminationGracePeriodSeconds      │
│                       │       ├──────────────────┼──────────────────────────────────────────────┤
│                       │       │  最小值           │  1秒                                         │
│                       │       └──────────────────┴──────────────────────────────────────────────┘
│                       └── 触发 podWorkerLoop goroutine
│                           └── podWorkerLoop() - pkg/kubelet/pod_workers.go:1213
│                               └── SyncTerminatingPod() - pkg/kubelet/kubelet.go:2003
│
├── [SyncTerminatingPod 阶段] - pkg/kubelet/kubelet.go:2003
│   │
│   │   **Pod 状态何时变为 "Terminating"?**
│   │   │
│   │   │   ┌─────────────────────────────────────────────────────────────────────────────────────┐
│   │   │   │ **重要澄清**: Kubernetes 没有官方的 "Terminating" Phase！                           │
│   │   │   ├─────────────────────────────────────────────────────────────────────────────────────┤
│   │   │   │                                                                                     │
│   │   │   │  1. **API 层面**: Pod.Status.Phase 始终是 Pending/Running/Succeeded/Failed/Unknown  │
│   │   │   │                   **从不会显示 "Terminating"**                                      │
│   │   │   │                                                                                     │
│   │   │   │  2. **kubectl 显示 "Terminating"**:                                                 │
│   │   │   │     └── kubectl 检测到 DeletionTimestamp != nil 时显示 "Terminating"               │
│   │   │   │     └── 这是 **客户端显示逻辑**，不是真正的 Pod Phase                               │
│   │   │   │                                                                                     │
│   │   │   │  3. **Kubelet 内部状态机** (podSyncStatus):                                         │
│   │   │   │     └── 这是 kubelet 内部用于跟踪 Pod 生命周期的状态                               │
│   │   │   │     └── 定义在 pkg/kubelet/pod_workers.go:322                                      │
│   │   │   │                                                                                     │
│   │   │   └─────────────────────────────────────────────────────────────────────────────────────┘
│   │   │
│   │   │   **Kubelet 内部状态转换点**:
│   │   │   │
│   │   │   ├── 1. acknowledgeTerminating() - pkg/kubelet/pod_workers.go:1343
│   │   │   │   │   在 podWorkerLoop 中调用
│   │   │   │   │
│   │   │   │   └── if !status.terminatingAt.IsZero() && !status.startedTerminating:
│   │   │   │       └── status.startedTerminating = true  // 行1355
│   │   │   │           └── **这是内部状态转换为 "正在终止" 的关键点**
│   │   │   │
│   │   │   ├── 2. completeSync() - pkg/kubelet/pod_workers.go:1364
│   │   │   │   │   当 syncPod 正常完成时调用（如 Pod 达到终态）
│   │   │   │   │
│   │   │   │   └── status.terminatingAt = p.clock.Now()  // 行1382
│   │   │   │       └── status.startedTerminating = true  // 行1386
│   │   │   │
│   │   │   └── 3. IsTerminationStarted() 方法 - 行407
│   │   │       └── return s.startedTerminating
│   │   │           └── 其他组件通过此方法判断是否已开始终止
│   │   │
│   │   │   **podSyncStatus 状态机变化** - pkg/kubelet/pod_workers.go:322
│   │   │       ├── startedTerminating = true (行1355/1386)
│   │   │       ├── terminatingAt = now (行1382)
│   │   │       └── deleted = true (如果 DeletionTimestamp != nil)
│   │
│   ├── 1. 停止健康检查 - probeManager.StopLivenessAndStartup(pod)
│   │   │   pkg/kubelet/prober/prober_manager.go:227
│   │   │   调用点: pkg/kubelet/kubelet.go:2028
│   │   │
│   │   ├── 1.1 遍历所有容器，停止 liveness 和 startup 探针
│   │   │   │
│   │   │   └── for _, c := range pod.Spec.Containers:  // 行232
│   │   │       │   key := probeKey{podUID: pod.UID, containerName: c.Name}
│   │   │       │
│   │   │       └── for probeType := range [liveness, startup]:  // 行234
│   │   │           │   key.probeType = probeType
│   │   │           │
│   │   │           └── if worker, ok := m.workers[key]; ok:  // 行236
│   │   │               └── worker.stop() - pkg/kubelet/prober/worker.go:189
│   │   │
│   │   ├── 1.2 worker.stop() 实现详解 - worker.go:189-193
│   │   │   │
│   │   │   │   func (w *worker) stop() {
│   │   │   │       select {
│   │   │   │       case w.stopCh <- struct{}{}:  // 非阻塞发送停止信号
│   │   │   │       default:  // 如果已经有信号在通道中，不阻塞
│   │   │   │       }
│   │   │   │   }
│   │   │   │
│   │   │   └── **stopCh 是带缓冲的通道**: make(chan struct{}, 1) - worker.go:92
│   │   │       └── 保证 stop() 是非阻塞的
│   │   │
│   │   ├── 1.3 探针 worker 如何消费停止信号 - worker.go:145-185
│   │   │   │
│   │   │   │   func (w *worker) run() {
│   │   │   │       probeTicker := time.NewTicker(probeTickerPeriod)  // 行157
│   │   │   │       defer func() {
│   │   │   │           probeTicker.Stop()                            // 停止定时器
│   │   │   │           w.resultsManager.Remove(w.containerID)        // 清理结果缓存 - 行163
│   │   │   │           w.probeManager.removeWorker(...)              // 从管理器移除 - 行166
│   │   │   │           ProberResults.Delete(...)                     // 清理 metrics - 行167-171
│   │   │   │       }()
│   │   │   │
│   │   │   │       probeLoop:
│   │   │   │       for w.doProbe(ctx) {                              // 探测循环 - 行175
│   │   │   │           select {
│   │   │   │           case <-w.stopCh:                              // **接收停止信号** - 行178
│   │   │   │               break probeLoop                           // 跳出探测循环
│   │   │   │           case <-probeTicker.C:                         // 定时探测
│   │   │   │           case <-w.manualTriggerCh:                     // 手动触发
│   │   │   │           }
│   │   │   │       }
│   │   │   │   }
│   │   │   │
│   │   │   └── **停止后的清理工作** (defer 块):
│   │   │       ├── resultsManager.Remove() - 从结果缓存中移除此容器的探测结果
│   │   │       ├── probeManager.removeWorker() - 从 workers map 中移除
│   │   │       └── 清理 Prometheus metrics
│   │   │
│   │   ├── 1.4 探针结果的影响
│   │   │   │
│   │   │   │   ┌─────────────────────────────────────────────────────────────────────────────┐
│   │   │   │   │ **探针类型与停止时机的设计考量**                                            │
│   │   │   │   ├──────────────────┬────────────────────────────────────────────────────────────┤
│   │   │   │   │  探针类型        │  作用与停止时机                                           │
│   │   │   │   ├──────────────────┼────────────────────────────────────────────────────────────┤
│   │   │   │   │  **liveness**    │  检测容器是否需要重启                                     │
│   │   │   │   │                  │  **早停止**: 终止时不需要重启逻辑，防止干扰               │
│   │   │   │   │                  │  如果不停止，可能触发不必要的容器重启                     │
│   │   │   │   ├──────────────────┼────────────────────────────────────────────────────────────┤
│   │   │   │   │  **startup**     │  检测容器是否已启动完成                                   │
│   │   │   │   │                  │  **早停止**: 终止时不再关心启动状态                       │
│   │   │   │   │                  │  没有意义继续检测即将被杀死的容器启动状态                 │
│   │   │   │   ├──────────────────┼────────────────────────────────────────────────────────────┤
│   │   │   │   │  **readiness**   │  检测容器是否能接收流量                                   │
│   │   │   │   │                  │  **晚停止**: 保持 NotReady 状态直到容器真正停止           │
│   │   │   │   │                  │  确保 Service 及时将流量从此 Pod 移除                     │
│   │   │   │   └──────────────────┴────────────────────────────────────────────────────────────┘
│   │   │   │
│   │   │   └── **为什么 readiness 探针要最后停止？**
│   │   │       │
│   │   │       ├── **原因1: 流量控制**
│   │   │       │   ├── readiness 探针结果直接影响 Service Endpoints
│   │   │       │   ├── 如果过早停止，Pod 可能被错误地认为是 Ready
│   │   │       │   └── 导致流量继续路由到正在终止的 Pod
│   │   │       │
│   │   │       ├── **原因2: 优雅关闭**
│   │   │       │   ├── readiness 探针在 Pod 删除期间应该返回 NotReady
│   │   │       │   ├── 这样 Endpoints Controller 会从 Service 中移除此 Pod
│   │   │       │   └── 新请求不会路由到正在终止的 Pod
│   │   │       │
│   │   │       ├── **原因3: 源码设计**
│   │   │       │   ├── StopLivenessAndStartup() 只停止 liveness 和 startup - 行234
│   │   │       │   ├── RemovePod() 停止所有探针包括 readiness - 行250
│   │   │       │   └── RemovePod() 在 killPod() 成功后才调用
│   │   │       │
│   │   │       └── **源码证据**:
│   │   │           ├── StopLivenessAndStartup - prober_manager.go:227
│   │   │           │   └── for probeType := range [liveness, startup]  // 只有两种
│   │   │           └── RemovePod - prober_manager.go:243
│   │   │               └── for probeType := range [readiness, liveness, startup]  // 三种全部
│   │   │
│   │   └── **注意**: readiness 探针此时不停止，在 RemovePod 时才停止
│   │
│   ├── 2. killPod() - pkg/kubelet/kubelet_pods.go:866
│   │   │
│   │   ├── 2.1 containerRuntime.KillPod() - pkg/kubelet/kuberuntime/kuberuntime_manager.go:1360
│   │   │   │
│   │   │   └── killPodWithSyncResult() - kuberuntime_manager.go:1367
│   │   │       │
│   │   │       ├── 2.1.1 killContainersWithSyncResult() - kuberuntime_container.go:762
│   │   │       │   │   **并行停止所有容器**
│   │   │       │   │
│   │   │       │   └── for container := range runningPod.Containers (并行 goroutine)
│   │   │       │       │
│   │   │       │       └── killContainer() - kuberuntime_container.go:706
│   │   │       │           │
│   │   │       │           ├── setTerminationGracePeriod() - 行1247
│   │   │       │           │   └── 计算容器的 gracePeriod
│   │   │       │           │
│   │   │       │           ├── recordContainerEvent("Killing")
│   │   │       │           │   └── **容器状态变化**: ContainerState = Running → Terminating
│   │   │       │           │
│   │   │       │           ├── [如果有PreStop Hook] executePreStopHook() - 行627
│   │   │       │           │   │
│   │   │       │           │   └── ┌──────────────────┬─────────────────────────────────────────────┐
│   │   │       │           │       │  行为             │  说明                                       │
│   │   │       │           │       ├──────────────────┼─────────────────────────────────────────────┤
│   │   │       │           │       │  异步执行         │  go m.runner.Run(ctx, containerID, ...)    │
│   │   │       │           │       ├──────────────────┼─────────────────────────────────────────────┤
│   │   │       │           │       │  超时处理         │  select case <-time.After(gracePeriod)     │
│   │   │       │           │       ├──────────────────┼─────────────────────────────────────────────┤
│   │   │       │           │       │  返回值           │  消耗的时间(秒)，从gracePeriod中扣除        │
│   │   │       │           │       └──────────────────┴─────────────────────────────────────────────┘
│   │   │       │           │
│   │   │       │           ├── 确保最小 gracePeriod >= 2秒 (minimumGracePeriodInSeconds)
│   │   │       │           │
│   │   │       │           └── runtimeService.StopContainer() - pkg/kubelet/cri/remote/remote_runtime.go:352
│   │   │       │               │   gRPC 调用 containerd
│   │   │       │               │
│   │   │       │               └── **[containerd 完整调用链]**
│   │   │       │                   │
│   │   │       │                   │   仓库路径: /Users/huaquan.liang/Documents/GitHub/containerd
│   │   │       │                   │
│   │   │       │                   ├── 1. CRI Server 入口 - internal/cri/server/container_stop.go:39
│   │   │       │                   │   │   StopContainer(ctx, r *runtime.StopContainerRequest)
│   │   │       │                   │   │
│   │   │       │                   │   └── c.stopContainer(ctx, container, timeout) - 行58
│   │   │       │                   │
│   │   │       │                   ├── 2. stopContainer() 实现 - container_stop.go:83
│   │   │       │                   │   │
│   │   │       │                   │   ├── 2.1 检查容器状态 - 行91-97
│   │   │       │                   │   │   └── state := container.Status.Get().State()
│   │   │       │                   │   │       └── 必须是 CONTAINER_RUNNING 或 CONTAINER_UNKNOWN
│   │   │       │                   │   │
│   │   │       │                   │   ├── 2.2 获取 containerd task - 行99
│   │   │       │                   │   │   └── task, err := container.Container.Task(ctx, nil)
│   │   │       │                   │   │       └── client/container.go - Task() 方法
│   │   │       │                   │   │
│   │   │       │                   │   ├── 2.3 [timeout > 0] 发送停止信号 - 行137-197
│   │   │       │                   │   │   │
│   │   │       │                   │   │   ├── 解析停止信号 - 行162
│   │   │       │                   │   │   │   └── sig, err := signal.ParseSignal(stopSignal)
│   │   │       │                   │   │   │       └── 默认 "SIGTERM"，或镜像的 StopSignal
│   │   │       │                   │   │   │
│   │   │       │                   │   │   └── **task.Kill(ctx, sig)** - 行177
│   │   │       │                   │   │       │
│   │   │       │                   │   │       └── [进入 containerd client 层]
│   │   │       │                   │   │
│   │   │       │                   │   ├── 2.4 等待容器停止 - 行186
│   │   │       │                   │   │   └── c.waitContainerStop(sigTermCtx, container)
│   │   │       │                   │   │
│   │   │       │                   │   └── 2.5 [超时] 发送 SIGKILL - 行199-208
│   │   │       │                   │       └── task.Kill(ctx, syscall.SIGKILL)
│   │   │       │                   │
│   │   │       │                   ├── 3. containerd Client 层 - client/task.go:263
│   │   │       │                   │   │   func (t *task) Kill(ctx, s syscall.Signal, opts...)
│   │   │       │                   │   │
│   │   │       │                   │   └── t.client.TaskService().Kill(ctx, &tasks.KillRequest{...})
│   │   │       │                   │       │   行280-284
│   │   │       │                   │       │   gRPC 调用 containerd daemon 的 Task Service
│   │   │       │                   │       │
│   │   │       │                   │       └── [进入 containerd daemon Task Service]
│   │   │       │                   │
│   │   │       │                   ├── 4. containerd Task Service 层
│   │   │       │                   │   │
│   │   │       │                   │   ├── 4.1 gRPC Service 入口 - plugins/services/tasks/service.go:94
│   │   │       │                   │   │   │   func (s *service) Kill(ctx, r *api.KillRequest)
│   │   │       │                   │   │   │
│   │   │       │                   │   │   └── return s.local.Kill(ctx, r)
│   │   │       │                   │   │
│   │   │       │                   │   └── 4.2 Local Service - plugins/services/tasks/local.go:452
│   │   │       │                   │       │   func (l *local) Kill(ctx, r *api.KillRequest)
│   │   │       │                   │       │
│   │   │       │                   │       ├── t, err := l.getTask(ctx, r.ContainerID) - 行453
│   │   │       │                   │       │   └── 获取 runtime.Task 接口
│   │   │       │                   │       │
│   │   │       │                   │       └── p.Kill(ctx, r.Signal, r.All) - 行463
│   │   │       │                   │           └── 调用 runtime.Process.Kill()
│   │   │       │                   │               └── [进入 containerd-shim]
│   │   │       │                   │
│   │   │       │                   ├── 5. containerd-shim-runc-v2 层
│   │   │       │                   │   │
│   │   │       │                   │   ├── 5.1 Shim Task Service - cmd/containerd-shim-runc-v2/task/service.go:491
│   │   │       │                   │   │   │   func (s *service) Kill(ctx, r *taskAPI.KillRequest)
│   │   │       │                   │   │   │
│   │   │       │                   │   │   ├── container, err := s.getContainer(r.ID) - 行492
│   │   │       │                   │   │   │
│   │   │       │                   │   │   └── container.Kill(ctx, r) - 行496
│   │   │       │                   │   │
│   │   │       │                   │   ├── 5.2 Container.Kill - cmd/containerd-shim-runc-v2/runc/container.go:418
│   │   │       │                   │   │   │   func (c *Container) Kill(ctx, r *task.KillRequest)
│   │   │       │                   │   │   │
│   │   │       │                   │   │   ├── p, err := c.Process(r.ExecID) - 行419
│   │   │       │                   │   │   │   └── 获取 init 进程或 exec 进程
│   │   │       │                   │   │   │
│   │   │       │                   │   │   └── p.Kill(ctx, r.Signal, r.All) - 行423
│   │   │       │                   │   │
│   │   │       │                   │   └── 5.3 Init.Kill - cmd/containerd-shim-runc-v2/process/init.go:348
│   │   │       │                   │       │   func (p *Init) Kill(ctx, signal uint32, all bool)
│   │   │       │                   │       │
│   │   │       │                   │       └── p.initState.Kill(ctx, signal, all) - 行352
│   │   │       │                   │           │   通过状态机调用
│   │   │       │                   │           │
│   │   │       │                   │           └── [runningState.Kill] - process/init_state.go:272
│   │   │       │                   │               └── s.p.kill(ctx, sig, all)
│   │   │       │                   │
│   │   │       │                   ├── 6. Init.kill - process/init.go:355
│   │   │       │                   │   │   func (p *Init) kill(ctx, signal uint32, all bool)
│   │   │       │                   │   │
│   │   │       │                   │   └── p.runtime.Kill(ctx, p.id, int(signal), &runc.KillOpts{All: all})
│   │   │       │                   │       │   行356-358
│   │   │       │                   │       │
│   │   │       │                   │       └── [进入 go-runc 库]
│   │   │       │                   │
│   │   │       │                   ├── 7. go-runc Kill - vendor/github.com/containerd/go-runc/runc.go:392
│   │   │       │                   │   │   func (r *Runc) Kill(ctx, id string, sig int, opts *KillOpts)
│   │   │       │                   │   │
│   │   │       │                   │   └── r.runOrError(r.command(ctx, append(args, id, strconv.Itoa(sig))...))
│   │   │       │                   │       │   行399
│   │   │       │                   │       │
│   │   │       │                   │       └── **执行命令**: runc kill <container-id> <signal-number>
│   │   │       │                   │
│   │   │       │                   └── 8. runc → Linux 内核
│   │   │       │                       │
│   │   │       │                       ├── runc 解析参数并调用 kill() 系统调用
│   │   │       │                       │   └── kill(container_pid, signal)
│   │   │       │                       │
│   │   │       │                       └── **Linux 内核处理**:
│   │   │       │                           ├── SIGTERM (15): 可被进程捕获和处理
│   │   │       │                           │   └── 进程可以优雅关闭
│   │   │       │                           └── SIGKILL (9): 不可被捕获
│   │   │       │                               └── 内核强制终止 (D状态进程除外)
│   │   │       │
│   │   │       │                   ┌─────────────────────────────────────────────────────────────────┐
│   │   │       │                   │ **containerd StopContainer 完整调用链总结**                     │
│   │   │       │                   ├─────────────────────────────────────────────────────────────────┤
│   │   │       │                   │ kubelet                                                         │
│   │   │       │                   │   └─ CRI gRPC ─▶ containerd (CRI Server)                       │
│   │   │       │                   │                     └─ containerd client                        │
│   │   │       │                   │                           └─ gRPC ─▶ Task Service               │
│   │   │       │                   │                                        └─ ttrpc ─▶ shim         │
│   │   │       │                   │                                                    └─ go-runc   │
│   │   │       │                   │                                                         └─ runc │
│   │   │       │                   │                                                            └─kill│
│   │   │       │                   └─────────────────────────────────────────────────────────────────┘
│   │   │       │
│   │   │       └── 2.1.2 StopPodSandbox() - kuberuntime_manager.go:1378
│   │   │           │   停止 Pod 沙箱 (pause 容器)
│   │   │           │
│   │   │           └── runtimeService.StopPodSandbox() - remote_runtime.go:213
│   │   │               │   gRPC 调用 containerd
│   │   │               │
│   │   │               └── [containerd 处理] internal/cri/server/sandbox_stop.go:35
│   │   │                   │   StopPodSandbox(ctx, r *runtime.StopPodSandboxRequest)
│   │   │                   │
│   │   │                   └── stopPodSandbox() - sandbox_stop.go:60
│   │   │                       │
│   │   │                       │   **[stopPodSandbox 完整调用链展开]**
│   │   │                       │
│   │   │                       ├── 1. 强制停止所有容器 - 行67-80
│   │   │                       │   │   **关键**: timeout=0 表示立即 SIGKILL，不等待优雅退出
│   │   │                       │   │
│   │   │                       │   └── for _, container := range c.containerStore.List():
│   │   │                       │       └── if container.SandboxID == id:
│   │   │                       │           │
│   │   │                       │           └── c.stopContainer(ctx, container, 0) - 行77
│   │   │                       │               │   container_stop.go:83
│   │   │                       │               │
│   │   │                       │               ├── **timeout=0 分支处理** - 行127-133
│   │   │                       │               │   │   跳过 SIGTERM，直接发送 SIGKILL
│   │   │                       │               │   │
│   │   │                       │               │   └── task.Kill(ctx, syscall.SIGKILL, containerd.WithKillAll)
│   │   │                       │               │       │   直接发送 SIGKILL 信号
│   │   │                       │               │       │
│   │   │                       │               │       └── [调用链同上 StopContainer 中的 task.Kill]
│   │   │                       │               │           └── ... → go-runc → runc kill → kill(pid, SIGKILL)
│   │   │                       │               │
│   │   │                       │               └── waitContainerStop(ctx, container) - 行141
│   │   │                       │                   └── 等待容器停止事件
│   │   │                       │
│   │   │                       ├── 2. 停止沙箱容器 (pause container) - 行83-93
│   │   │                       │   │   只有 State=Ready 或 Unknown 时才调用
│   │   │                       │   │
│   │   │                       │   └── c.sandboxService.StopSandbox(ctx, sandbox.Sandboxer, id)
│   │   │                       │       │   internal/cri/server/sandbox_service.go:113
│   │   │                       │       │
│   │   │                       │       └── ctrl.Stop(ctx, sandboxID, opts...) - 行118
│   │   │                       │           │   调用 Sandbox Controller
│   │   │                       │           │
│   │   │                       │           ├── [podsandbox Controller] - internal/cri/server/podsandbox/sandbox_stop.go
│   │   │                       │           │   │
│   │   │                       │           │   └── func (c *Controller) Stop(ctx, sandboxID, opts...)
│   │   │                       │           │       │
│   │   │                       │           │       ├── sandbox := c.store.Get(sandboxID)
│   │   │                       │           │       ├── task, _ := sandbox.Container.Task(ctx, nil)
│   │   │                       │           │       └── task.Kill(ctx, syscall.SIGKILL, containerd.WithKillAll)
│   │   │                       │           │           └── 杀死 pause 容器
│   │   │                       │           │
│   │   │                       │           └── [controllerLocal] - plugins/sandbox/controller.go:215
│   │   │                       │               │
│   │   │                       │               └── svc.StopSandbox(ctx, req) - 行233
│   │   │                       │                   │   通过 ttrpc 调用 shim
│   │   │                       │                   │
│   │   │                       │                   └── [shim] ShutdownSandbox 处理
│   │   │                       │                       └── 停止 pause 容器进程
│   │   │                       │
│   │   │                       ├── 3. NRI 通知 - 行101-104
│   │   │                       │   │   通知 NRI 插件沙箱停止
│   │   │                       │   │
│   │   │                       │   └── c.nri.StopPodSandbox(ctx, &sandbox)
│   │   │                       │       │   internal/cri/nri/nri_api_linux.go:97
│   │   │                       │       │
│   │   │                       │       └── a.nri.StopPodSandbox(ctx, pod) - 行103
│   │   │                       │           │   internal/nri/nri.go:192
│   │   │                       │           │
│   │   │                       │           └── l.nri.StopPodSandbox(ctx, request) - 行203
│   │   │                       │               └── 通知所有注册的 NRI 插件
│   │   │                       │
│   │   │                       └── 4. 清理网络 - 行107-130
│   │   │                           │   **Linux Namespace 操作**
│   │   │                           │
│   │   │                           ├── 4.1 检查 NetNS 状态 - 行112-116
│   │   │                           │   └── if closed, err := sandbox.NetNS.Closed(); closed:
│   │   │                           │       └── sandbox.NetNSPath = ""  // 使用空路径
│   │   │                           │
│   │   │                           ├── 4.2 teardownPodNetwork() - 行118
│   │   │                           │   │   sandbox_stop.go:152
│   │   │                           │   │
│   │   │                           │   ├── netPlugin := c.getNetworkPlugin(sandbox.RuntimeHandler)
│   │   │                           │   │   └── 获取 CNI 插件实例
│   │   │                           │   │
│   │   │                           │   └── netPlugin.Remove(ctx, id, path, opts...) - 行170
│   │   │                           │       │   调用 CNI 删除网络
│   │   │                           │       │
│   │   │                           │       └── **CNI 操作详情**:
│   │   │                           │           ├── 执行 CNI DEL 命令
│   │   │                           │           │   └── /opt/cni/bin/<plugin> DEL
│   │   │                           │           │
│   │   │                           │           ├── 删除 veth pair
│   │   │                           │           │   └── ip link del <veth-name>
│   │   │                           │           │
│   │   │                           │           ├── 清理 iptables 规则
│   │   │                           │           │   └── iptables -D FORWARD/INPUT/OUTPUT ...
│   │   │                           │           │
│   │   │                           │           └── 释放 IP 地址
│   │   │                           │               └── 从 IPAM 释放分配的 IP
│   │   │                           │
│   │   │                           └── 4.3 sandbox.NetNS.Remove() - 行122
│   │   │                               │   **Linux 内核**: 删除 Network Namespace
│   │   │                               │
│   │   │                               └── **Linux 操作详情**:
│   │   │                                   ├── umount("/var/run/netns/<sandbox-id>")
│   │   │                                   │   └── 卸载 bind mount
│   │   │                                   │
│   │   │                                   └── unlink("/var/run/netns/<sandbox-id>")
│   │   │                                       └── 删除 netns 文件
│   │   │                                           └── 触发内核回收 Network Namespace
│   │   │
│   │   └── 2.2 UpdateQOSCgroups() - pkg/kubelet/kubelet_pods.go:871
│   │       │   **QOS 资源更新**
│   │       │
│   │       └── kl.containerManager.UpdateQOSCgroups()
│   │           └── pkg/kubelet/cm/container_manager_linux.go:547
│   │               └── cm.qosContainerManager.UpdateCgroups()
│   │                   └── 更新 QoS cgroup 层次结构
│   │                       ├── 重新计算 Guaranteed/Burstable/BestEffort 的资源限制
│   │                       └── **Linux Cgroups 操作**:
│   │                           └── 更新 /sys/fs/cgroup/kubepods/<qos-class>/ 下的资源限制
│   │
│   └── 3. 移除所有探针 - probeManager.RemovePod(pod)
│       │   pkg/kubelet/prober/prober_manager.go:243
│       │
│       └── 遍历所有容器，停止所有类型探针
│           └── for probeType := range [readiness, liveness, startup]:
│               └── if worker, ok := m.workers[key]; ok:
│                   └── worker.stop()
│                       └── 清理 goroutine 资源
│
├── [SyncTerminatedPod 阶段] - pkg/kubelet/kubelet.go:2140
│   │   **podSyncStatus 状态机变化**:
│   │       ├── terminatedAt = now (行363)
│   │       └── finished = true (行391) - 在 syncTerminatedPod 成功后
│   │
│   ├── 1. 生成最终 Pod 状态 - kubelet.go:2153
│   │   │
│   │   └── apiPodStatus := kl.generateAPIPodStatus(pod, podStatus, true)
│   │       │
│   │       └── **Pod 状态变化**:
│   │           ├── pod.Status.Phase = Succeeded 或 Failed
│   │           │   └── 根据容器退出码决定:
│   │           │       ├── 所有容器正常退出 (exitCode=0) → Succeeded
│   │           │       └── 任何容器异常退出 → Failed
│   │           │
│   │           └── containerStatuses[*].State = Terminated
│   │               ├── ExitCode: 容器退出码
│   │               ├── Reason: "Completed" 或 "Error"
│   │               ├── FinishedAt: 容器停止时间
│   │               └── StartedAt: 容器启动时间
│   │
│   ├── 2. 更新 Pod 状态到 StatusManager - kubelet.go:2155
│   │   │
│   │   └── kl.statusManager.SetPodStatus(pod, apiPodStatus)
│   │       └── 将状态缓存到 StatusManager，稍后同步到 API Server
│   │
│   ├── 3. 等待卷卸载 - kubelet.go:2159
│   │   │   **PVC/Volume 处理逻辑（含 CSI 接口调用）**
│   │   │
│   │   └── kl.volumeManager.WaitForUnmount(ctx, pod)
│   │       │   pkg/kubelet/volumemanager/volume_manager.go:446
│   │       │
│   │       │   **[wait.PollUntilContextTimeout 详解]**
│   │       │
│   │       └── wait.PollUntilContextTimeout(ctx, 10s, 2m, true, conditionFunc)
│   │           │   vendor/k8s.io/apimachinery/pkg/util/wait/poll.go
│   │           │
│   │           │   参数说明:
│   │           │   ├── interval = 10秒 (podAttachAndMountRetryInterval)
│   │           │   ├── timeout = 2分钟 (podAttachAndMountTimeout)
│   │           │   └── immediate = true (立即执行第一次检查)
│   │           │
│   │           ├── 轮询机制 - poll.go
│   │           │   │
│   │           │   └── 每 10 秒执行一次 conditionFunc
│   │           │       │
│   │           │       ├── 如果返回 (true, nil) → 卷已卸载，函数返回
│   │           │       ├── 如果返回 (false, nil) → 继续等待
│   │           │       ├── 如果返回 (_, error) → 返回错误
│   │           │       └── 如果超过 2 分钟 → 返回 context.DeadlineExceeded
│   │           │
│   │           └── vm.verifyVolumesUnmountedFunc(uniquePodName) - 行525
│   │               │   pkg/kubelet/volumemanager/volume_manager.go:525
│   │               │
│   │               └── func() (done bool, err error) {
│   │                   │   // 检查是否有错误需要报告
│   │                   │   if errs := vm.desiredStateOfWorld.PopPodErrors(podName); len(errs) > 0:
│   │                   │       return true, errors.New(strings.Join(errs, "; "))
│   │                   │
│   │                   │   // 检查是否还有挂载的卷
│   │                   │   mountedVolumes := vm.actualStateOfWorld.GetMountedVolumesForPod(podName)
│   │                   │   return len(mountedVolumes) == 0, nil
│   │                   │}
│   │                   │
│   │                   └── **卷卸载由 reconciler 异步执行**
│   │                       │   (不在此等待函数中直接卸载)
│   │                       │
│   │                       └── [实际卸载流程 - 由 VolumeManager Reconciler 触发]
│   │                           │
│   │                           └── 见下方 "卷卸载异步流程"
│   │
│   │   ┌─────────────────────────────────────────────────────────────────────────────────────┐
│   │   │ **卷卸载异步流程 (VolumeManager Reconciler)**                                       │
│   │   │   pkg/kubelet/volumemanager/reconciler/reconciler_common.go                        │
│   │   │                                                                                     │
│   │   │ reconcile() 周期调用 (默认100ms) → unmountVolumes() - 行160                        │
│   │   │   │                                                                                 │
│   │   │   └── for mountedVolume := range rc.actualStateOfWorld.GetAllMountedVolumes():     │
│   │   │       │   if !rc.desiredStateOfWorld.PodExistsInVolume(...):                       │
│   │   │       │       // Pod 已删除，需要卸载此卷                                           │
│   │   │       │                                                                             │
│   │   │       └── rc.operationExecutor.UnmountVolume() - 行166                             │
│   │   │           │   pkg/volume/util/operationexecutor/operation_executor.go:969          │
│   │   │           │                                                                         │
│   │   │           └── GenerateUnmountVolumeFunc() - operation_generator.go:830             │
│   │   │               │                                                                     │
│   │   │               ├── 1. 清理 SubPath 挂载 - 行852                                     │
│   │   │               │   └── subpather.CleanSubPaths(podDir, volumeName)                  │
│   │   │               │                                                                     │
│   │   │               ├── 2. **执行 TearDown** - 行858                                     │
│   │   │               │   │   volumeUnmounter.TearDown()                                   │
│   │   │               │   │                                                                 │
│   │   │               │   │   **[CSI 卷卸载流程]**                                         │
│   │   │               │   │   (如果是 CSI 卷，如 PVC 绑定的 CSI StorageClass)              │
│   │   │               │   │                                                                 │
│   │   │               │   ├── [CSI Plugin] pkg/volume/csi/csi_mounter.go                   │
│   │   │               │   │   │                                                             │
│   │   │               │   │   └── csiMountMgr.TearDownAt(dir string) - 行454               │
│   │   │               │   │       │                                                         │
│   │   │               │   │       ├── 1. 调用 CSI NodeUnpublishVolume                      │
│   │   │               │   │       │   │   csi.NodeUnpublishVolume(ctx, req) - 行509       │
│   │   │               │   │       │   │                                                     │
│   │   │               │   │       │   └── **gRPC 调用 CSI Driver**                         │
│   │   │               │   │       │       ├── 目标: CSI Node Plugin (DaemonSet)            │
│   │   │               │   │       │       ├── 方法: NodeUnpublishVolume                    │
│   │   │               │   │       │       └── 作用: 从 Pod 目录卸载卷                      │
│   │   │               │   │       │                                                         │
│   │   │               │   │       └── 2. 清理挂载目录 - 行527                              │
│   │   │               │   │           └── os.RemoveAll(dir)                                │
│   │   │               │   │                                                                 │
│   │   │               │   └── [普通卷 - NFS/HostPath 等]                                   │
│   │   │               │       │   pkg/volume/<plugin>/xxx_mounter.go                       │
│   │   │               │       │                                                             │
│   │   │               │       └── mounter.TearDown()                                       │
│   │   │               │           └── **Linux syscall**:                                   │
│   │   │               │               └── syscall.Unmount(path, flags)                     │
│   │   │               │                   └── 内核卸载文件系统                              │
│   │   │               │                                                                     │
│   │   │               └── 3. 更新 actualStateOfWorld - 行892                               │
│   │   │                   └── actualStateOfWorld.MarkVolumeAsUnmounted(...)                │
│   │   │                       └── 标记卷为已卸载，verifyVolumesUnmountedFunc 检查通过      │
│   │   │                                                                                     │
│   │   └─────────────────────────────────────────────────────────────────────────────────────┘
│   │
│   │   **[卷 Finalizer 处理逻辑详解]**
│   │   │
│   │   ├── [有 PVC 卷 - 理解 Finalizer 的作用]
│   │   │   │
│   │   │   │   **重要澄清**: PVC 的 Finalizer 不阻塞 Pod 删除！
│   │   │   │
│   │   │   │   ┌──────────────────────────────────────────────────────────────────────────┐
│   │   │   │   │ PVC Finalizer (kubernetes.io/pvc-protection) 的作用:                     │
│   │   │   │   │                                                                          │
│   │   │   │   │ 1. 保护 PVC 本身不被误删除（当 Pod 正在使用时）                          │
│   │   │   │   │ 2. 不影响 Pod 的删除流程                                                 │
│   │   │   │   │ 3. Pod 删除后，如果 PVC 也被删除了，Controller 才会移除 Finalizer        │
│   │   │   │   └──────────────────────────────────────────────────────────────────────────┘
│   │   │   │
│   │   │   └── **PVC Finalizer 移除时机** (非 Pod 删除路径):
│   │   │       │
│   │   │       └── PVC Protection Controller - 行147-185
│   │   │           │   pkg/controller/volume/pvcprotection/pvc_protection_controller.go
│   │   │           │
│   │   │           ├── 触发条件: PVC 有 DeletionTimestamp 且不再被任何 Pod 使用
│   │   │           │
│   │   │           └── processPVC() 移除 Finalizer 流程:
│   │   │               ├── IsDeletionCandidate(pvc) - 行164
│   │   │               │   └── pvc.DeletionTimestamp != nil && has(Finalizer)
│   │   │               │
│   │   │               ├── isBeingUsed(ctx, pvc) - 行167
│   │   │               │   └── 检查是否有 Pod 引用此 PVC
│   │   │               │
│   │   │               └── [不再被使用] removeFinalizer() - 行172
│   │   │                   └── 移除 "kubernetes.io/pvc-protection"
│   │   │                       └── PVC 可以被真正删除
│   │   │
│   │   ├── [CSI 卷特殊处理]
│   │   │   │
│   │   │   │   **CSI 卷有两个阶段: Publish 和 Stage**
│   │   │   │
│   │   │   └── 完整卸载需要两次调用:
│   │   │       │
│   │   │       ├── 1. NodeUnpublishVolume (Pod 级别)
│   │   │       │   └── 从 /var/lib/kubelet/pods/<uid>/volumes/... 卸载
│   │   │       │
│   │   │       └── 2. NodeUnstageVolume (Node 级别，可选)
│   │   │           └── 从 /var/lib/kubelet/plugins/kubernetes.io/csi/... 卸载
│   │   │               └── 由 AttachDetach Controller 触发
│   │   │
│   │   └── [无 PVC 卷 - EmptyDir/HostPath 等]
│   │       └── 直接执行 TearDown，不涉及 Finalizer
│   │
│   ├── 4. 等待卷路径清理 - kubelet.go:2164-2176
│   │   │
│   │   └── wait.PollUntilContextCancel(ctx, 100ms, ...)
│   │       │
│   │       └── func() (bool, error) {
│   │           │   volumesExist := kl.podVolumesExist(pod.UID)
│   │           │   return !volumesExist, nil
│   │           │}
│   │           │
│   │           └── 检查 /var/lib/kubelet/pods/<pod-uid>/volumes/ 目录是否为空
│   │               └── **Linux 文件系统操作**:
│   │                   └── os.ReadDir() 检查目录内容
│   │
│   ├── 5. 取消注册 Secret - kubelet.go:2181
│   │   │
│   │   └── kl.secretManager.UnregisterPod(pod)
│   │       └── 从 SecretManager 缓存中移除 Pod 引用的 Secret
│   │
│   ├── 6. 取消注册 ConfigMap - kubelet.go:2184
│   │   │
│   │   └── kl.configMapManager.UnregisterPod(pod)
│   │       └── 从 ConfigMapManager 缓存中移除 Pod 引用的 ConfigMap
│   │
│   ├── 7. 删除 Pod Cgroups - kubelet.go:2192-2198
│   │   │   **Linux Cgroups 操作**
│   │   │
│   │   └── if kl.cgroupsPerQOS:
│   │       │
│   │       ├── pcm := kl.containerManager.NewPodContainerManager()
│   │       │
│   │       ├── name, _ := pcm.GetPodContainerName(pod)
│   │       │   └── 获取 cgroup 路径: /kubepods/<qos>/<pod-uid>
│   │       │
│   │       └── pcm.Destroy(name) - kubelet.go:2195
│   │           │   pkg/kubelet/cm/pod_container_manager_linux.go:197
│   │           │
│   │           └── **Linux Cgroups 操作**:
│   │               ├── 终止 cgroup 内所有进程
│   │               │   └── cgroup v2: echo 1 > /sys/fs/cgroup/.../cgroup.kill
│   │               │   └── cgroup v1: 遍历 cgroup.procs 发送 SIGKILL
│   │               │
│   │               ├── 删除子 cgroup
│   │               │   └── rmdir /sys/fs/cgroup/.../pod<uid>/<container-id>/
│   │               │
│   │               └── 删除 Pod cgroup
│   │                   └── rmdir /sys/fs/cgroup/.../pod<uid>/
│   │
│   ├── 8. 释放 User Namespace - kubelet.go:2201
│   │   │
│   │   └── kl.usernsManager.Release(pod.UID)
│   │       └── **Linux User Namespace 操作**:
│   │           └── 释放为 Pod 分配的 UID/GID 映射范围
│   │
│   └── 9. 标记 Pod 终止完成 - kubelet.go:2204
│       │
│       └── kl.statusManager.TerminatePod(pod)
│           │   pkg/kubelet/status/status_manager.go:428
│           │
│           └── **Pod 状态最终确定**:
│               ├── 将所有容器状态标记为 Terminated
│               ├── 设置 Pod Phase 为终态 (Succeeded/Failed)
│               └── podIsFinished = true
│                   └── 触发 canBeDeleted() 检查
│
└── [Status Manager 最终删除阶段] - pkg/kubelet/status/status_manager.go:841
    └── syncPod() - pkg/kubelet/status/status_manager.go:841
        ├── kubeClient.CoreV1().Pods(ns).Get() - 获取最新Pod状态
        ├── mergePodStatus() - 合并状态
        ├── statusutil.PatchPodStatus() - 更新状态到API Server
        └── canBeDeleted() - pkg/kubelet/status/status_manager.go:931
            └── 检查条件:
                ├── pod.DeletionTimestamp != nil
                ├── podutil.IsPodPhaseTerminal(pod.Status.Phase)
                └── podIsFinished == true
            └── [如果可删除] kubeClient.CoreV1().Pods(ns).Delete() - pkg/kubelet/status/status_manager.go:907
                └── DeleteOptions{GracePeriodSeconds: 0} - 最终从etcd删除

├── [Container GC 阶段 - 异步清理] - pkg/kubelet/kubelet.go:1408-1426
│   │   **注意**: 这是异步清理阶段，不在 Pod 删除的同步路径中！
│   │   GC 每分钟执行一次 (ContainerGCPeriod = 1 minute)
│   │
│   └── StartGarbageCollection() - kubelet.go:1409
│       └── wait.Until(kl.containerGC.GarbageCollect, ContainerGCPeriod, ...)
│           │   每 1 分钟触发一次 GC
│           │
│           └── GarbageCollect() - pkg/kubelet/kuberuntime/kuberuntime_gc.go:407
│               │
│               ├── 1. evictContainers() - 清理死亡容器 - 行412
│               │   │   pkg/kubelet/kuberuntime/kuberuntime_gc.go:227
│               │   │
│               │   ├── evictableContainers() - 获取可清理的容器 - 行229
│               │   │   └── 条件: 非运行中 && 创建时间 > gcPolicy.MinAge
│               │   │
│               │   ├── [已删除 Pod 的容器] 立即全部清理 - 行234-241
│               │   │   └── ShouldPodContentBeRemoved(uid) 检查
│               │   │       └── pkg/kubelet/pod_workers.go:689
│               │   │           └── status.IsEvicted() || (status.IsDeleted() && status.IsTerminated())
│               │   │
│               │   └── removeOldestN() - 执行容器删除 - 行238
│               │       │   pkg/kubelet/kuberuntime/kuberuntime_gc.go:129
│               │       │
│               │       └── for container := range containersToRemove:
│               │           │
│               │           ├── [unknown 状态容器] 先尝试停止 - 行136-148
│               │           │   └── killContainer(ctx, nil, id, name, message, ...) - 行144
│               │           │
│               │           └── **removeContainer()** - 行149
│               │               │   pkg/kubelet/kuberuntime/kuberuntime_container.go:1197
│               │               │
│               │               ├── PostStopContainer() - 内部生命周期钩子 - 行1200
│               │               │
│               │               ├── removeContainerLog() - 删除容器日志 - 行1206
│               │               │   ├── logManager.Clean(ctx, containerID) - 清理轮转日志
│               │               │   └── osInterface.Remove(legacySymlink) - 删除符号链接
│               │               │
│               │               └── **runtimeService.RemoveContainer()** - 行1210
│               │                   │   pkg/kubelet/cri/remote/remote_runtime.go:376
│               │                   │   gRPC 调用 containerd
│               │                   │
│               │                   └── **[containerd RemoveContainer 完整调用链]**
│               │                       │
│               │                       │   仓库路径: /Users/huaquan.liang/Documents/GitHub/containerd
│               │                       │
│               │                       ├── 1. CRI Server 入口 - internal/cri/server/container_remove.go:34
│               │                       │   │   func (c *criService) RemoveContainer(ctx, r *runtime.RemoveContainerRequest)
│               │                       │   │
│               │                       │   ├── 1.1 获取容器对象 - 行38
│               │                       │   │   └── container, err := c.containerStore.Get(ctrID)
│               │                       │   │       └── 从 CRI 容器缓存中获取
│               │                       │   │
│               │                       │   ├── 1.2 获取容器信息 - 行52
│               │                       │   │   └── i, err := container.Container.Info(ctx)
│               │                       │   │
│               │                       │   ├── 1.3 [如果容器还在运行] 强制停止 - 行67-74
│               │                       │   │   │   **这就是为什么 Remove 时可能再次 kill！**
│               │                       │   │   │
│               │                       │   │   └── if state == CONTAINER_RUNNING || state == CONTAINER_UNKNOWN:
│               │                       │   │       └── log.L.Infof("Forcibly stopping container %q", id)
│               │                       │   │           └── c.stopContainer(ctx, container, 0)
│               │                       │   │               │   timeout=0 表示直接 SIGKILL
│               │                       │   │               │
│               │                       │   │               └── [调用上面的 StopContainer 流程]
│               │                       │   │
│               │                       │   ├── 1.4 设置 Removing 状态 - 行79
│               │                       │   │   └── setContainerRemoving(container)
│               │                       │   │       │   container_remove.go:148
│               │                       │   │       │
│               │                       │   │       └── container.Status.Update(func(status) {
│               │                       │   │           │   status.Removing = true
│               │                       │   │           │   return status, nil
│               │                       │   │           })
│               │                       │   │           └── 防止并发 start/remove 操作
│               │                       │   │
│               │                       │   ├── 1.5 NRI 通知 - 行91-99
│               │                       │   │   │
│               │                       │   │   └── c.nri.RemoveContainer(ctx, &sandbox, &container)
│               │                       │   │       │   internal/cri/nri/nri_api_linux.go:246
│               │                       │   │       │
│               │                       │   │       │   **[NRI RemoveContainer 调用链展开]**
│               │                       │   │       │
│               │                       │   │       ├── 1. CRI-NRI API 层 - nri_api_linux.go:246
│               │                       │   │       │   │   func (a *API) RemoveContainer(ctx, criPod, criCtr)
│               │                       │   │       │   │
│               │                       │   │       │   ├── if a.IsDisabled(): return nil
│               │                       │   │       │   │   └── NRI 可能被禁用
│               │                       │   │       │   │
│               │                       │   │       │   ├── pod := a.nriPodSandbox(criPod)
│               │                       │   │       │   │   └── 转换为 NRI Pod 格式
│               │                       │   │       │   │
│               │                       │   │       │   ├── ctr := a.nriContainer(criCtr, nil)
│               │                       │   │       │   │   └── 转换为 NRI Container 格式
│               │                       │   │       │   │
│               │                       │   │       │   └── a.nri.RemoveContainer(ctx, pod, ctr)
│               │                       │   │       │       └── 调用 NRI 核心实现
│               │                       │   │       │
│               │                       │   │       ├── 2. NRI 核心层 - internal/nri/nri.go:427
│               │                       │   │       │   │   func (l *local) RemoveContainer(ctx, pod, ctr)
│               │                       │   │       │   │
│               │                       │   │       │   ├── l.stopContainer(ctx, pod, ctr) - 行428
│               │                       │   │       │   │   └── 先停止容器（NRI 层面）
│               │                       │   │       │   │
│               │                       │   │       │   ├── 构建请求 - 行430-433
│               │                       │   │       │   │   └── request := &nri.RemoveContainerRequest{
│               │                       │   │       │   │           Pod:       podSandboxToNRI(pod),
│               │                       │   │       │   │           Container: containerToNRI(ctr),
│               │                       │   │       │   │       }
│               │                       │   │       │   │
│               │                       │   │       │   └── l.nri.RemoveContainer(ctx, request) - 行434
│               │                       │   │       │       │   通知所有 NRI 插件
│               │                       │   │       │       │
│               │                       │   │       │       └── **NRI 插件可执行的操作**:
│               │                       │   │       │           ├── 清理 GPU/NPU 设备分配
│               │                       │   │       │           ├── 释放网络带宽预留
│               │                       │   │       │           ├── 更新监控/日志配置
│               │                       │   │       │           └── 其他自定义清理逻辑
│               │                       │   │       │
│               │                       │   │       └── 3. 更新状态 - 行435
│               │                       │   │           └── l.setState(request.Container.Id, Removed)
│               │                       │   │               └── 标记容器为已移除状态
│               │                       │   │
│               │                       │   ├── 1.6 **删除 containerd 容器对象** - 行107
│               │                       │   │   │   container.Container.Delete(ctx, containerd.WithSnapshotCleanup)
│               │                       │   │   │
│               │                       │   │   └── [containerd client 层] client/container.go
│               │                       │   │       │   func (c *container) Delete(ctx, opts...)
│               │                       │   │       │
│               │                       │   │       ├── 清理快照 - WithSnapshotCleanup 选项
│               │                       │   │       │   └── c.client.SnapshotService(info.Snapshotter).Remove(ctx, info.SnapshotKey)
│               │                       │   │       │       └── 删除容器的 overlay 文件系统快照
│               │                       │   │       │
│               │                       │   │       └── 调用 Container Service 删除
│               │                       │   │           └── c.client.ContainerService().Delete(ctx, c.id)
│               │                       │   │               │
│               │                       │   │               └── [containerd daemon] plugins/services/containers/service.go
│               │                       │   │                   └── s.local.Delete(ctx, r)
│               │                       │   │                       └── 从 metadata store 中删除容器记录
│               │                       │   │
│               │                       │   ├── 1.7 删除 CRI 容器 checkpoint - 行115
│               │                       │   │   └── container.Delete()
│               │                       │   │       │   internal/cri/store/container/container.go
│               │                       │   │       │
│               │                       │   │       └── 删除容器状态 checkpoint 文件
│               │                       │   │           └── os.RemoveAll(c.volatileCheckpointPath)
│               │                       │   │
│               │                       │   ├── 1.8 清理容器根目录 - 行119-128
│               │                       │   │   │
│               │                       │   │   ├── ensureRemoveAll(ctx, containerRootDir) - 行120
│               │                       │   │   │   │   containerRootDir = c.getContainerRootDir(id)
│               │                       │   │   │   │
│               │                       │   │   │   └── 删除: /var/lib/containerd/io.containerd.grpc.v1.cri/containers/<id>
│               │                       │   │   │       └── 包含容器配置、日志等
│               │                       │   │   │
│               │                       │   │   └── ensureRemoveAll(ctx, volatileContainerRootDir) - 行125
│               │                       │   │       │   volatileContainerRootDir = c.getVolatileContainerRootDir(id)
│               │                       │   │       │
│               │                       │   │       └── 删除: /run/containerd/io.containerd.grpc.v1.cri/containers/<id>
│               │                       │   │           └── 包含运行时临时文件
│               │                       │   │
│               │                       │   ├── 1.9 从 CRI store 中删除 - 行130
│               │                       │   │   └── c.containerStore.Delete(id)
│               │                       │   │
│               │                       │   ├── 1.10 释放容器名称 - 行132
│               │                       │   │   └── c.containerNameIndex.ReleaseByKey(id)
│               │                       │   │
│               │                       │   └── 1.11 发送删除事件 - 行134
│               │                       │       └── c.generateAndSendContainerEvent(ctx, id, sandboxID, CONTAINER_DELETED_EVENT)
│               │                       │
│               │                       └── 2. **Linux 文件系统清理总结**
│               │                           │
│               │                           ├── overlay 快照: /var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/<id>/
│               │                           ├── 容器元数据: /var/lib/containerd/io.containerd.grpc.v1.cri/containers/<id>/
│               │                           ├── 运行时状态: /run/containerd/io.containerd.grpc.v1.cri/containers/<id>/
│               │                           └── 容器日志由 kubelet 的 removeContainerLog() 清理
│               │
│               ├── 2. evictSandboxes() - 清理死亡沙箱 - 行417
│               │   │   pkg/kubelet/kuberuntime/kuberuntime_gc.go:279
│               │   │
│               │   └── removeOldestNSandboxes() - 行160
│               │       │
│               │       └── for sandbox := range sandboxesToRemove:
│               │           │
│               │           └── removeSandbox(ctx, sandboxID) - 行174
│               │               │
│               │               ├── **StopPodSandbox** (安全起见再次停止) - 行179
│               │               │   │   **为什么这里还要再 stop 一次？**
│               │               │   │   见下方详细解释
│               │               │   │
│               │               │   └── cgc.client.StopPodSandbox(ctx, sandboxID)
│               │               │
│               │               └── **RemovePodSandbox** - 行183
│               │                   │   pkg/kubelet/cri/remote/remote_runtime.go:233
│               │                   │   gRPC 调用 containerd
│               │                   │
│               │                   └── **[containerd RemovePodSandbox 完整调用链]**
│               │                       │
│               │                       │   仓库路径: /Users/huaquan.liang/Documents/GitHub/containerd
│               │                       │
│               │                       ├── 1. CRI Server 入口 - internal/cri/server/sandbox_remove.go:34
│               │                       │   │   func (c *criService) RemovePodSandbox(ctx, r *runtime.RemovePodSandboxRequest)
│               │                       │   │
│               │                       │   ├── 1.1 获取沙箱对象 - 行42
│               │                       │   │   └── sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
│               │                       │   │
│               │                       │   ├── 1.2 检查是否有容器存在 - 行52-60
│               │                       │   │   └── 如果还有容器，返回错误
│               │                       │   │       └── "sandbox contains containers"
│               │                       │   │
│               │                       │   └── 1.3 removePodSandbox() - 行65
│               │                       │
│               │                       ├── 2. removePodSandbox() 实现 - sandbox_remove.go:70
│               │                       │   │
│               │                       │   ├── 2.1 NRI 通知 - 行102
│               │                       │   │   │
│               │                       │   │   └── c.nri.RemovePodSandbox(ctx, &sandbox)
│               │                       │   │       │   internal/cri/nri/nri_api_linux.go:108
│               │                       │   │       │
│               │                       │   │       │   **[NRI RemovePodSandbox 调用链展开]**
│               │                       │   │       │
│               │                       │   │       ├── 1. CRI-NRI API 层 - nri_api_linux.go:108
│               │                       │   │       │   │   func (a *API) RemovePodSandbox(ctx, criPod)
│               │                       │   │       │   │
│               │                       │   │       │   ├── if a.IsDisabled(): return nil
│               │                       │   │       │   │
│               │                       │   │       │   ├── pod := a.nriPodSandbox(criPod)
│               │                       │   │       │   │   └── 转换为 NRI Pod 格式
│               │                       │   │       │   │
│               │                       │   │       │   └── a.nri.RemovePodSandbox(ctx, pod) - 行115
│               │                       │   │       │
│               │                       │   │       ├── 2. NRI 核心层 - internal/nri/nri.go:208
│               │                       │   │       │   │   func (l *local) RemovePodSandbox(ctx, pod)
│               │                       │   │       │   │
│               │                       │   │       │   ├── needsRemoval 检查 - 行216
│               │                       │   │       │   │   └── 检查 Pod 是否需要移除通知
│               │                       │   │       │   │
│               │                       │   │       │   ├── 构建请求 - 行220-222
│               │                       │   │       │   │   └── request := &nri.RemovePodSandboxRequest{
│               │                       │   │       │   │           Pod: podSandboxToNRI(pod),
│               │                       │   │       │   │       }
│               │                       │   │       │   │
│               │                       │   │       │   └── l.nri.RemovePodSandbox(ctx, request) - 行224
│               │                       │   │       │       │   通知所有 NRI 插件
│               │                       │   │       │       │
│               │                       │   │       │       └── **NRI 插件可执行的操作**:
│               │                       │   │       │           ├── 清理 Pod 级别的资源预留
│               │                       │   │       │           ├── 释放共享内存/IPC 资源
│               │                       │   │       │           ├── 更新节点资源统计
│               │                       │   │       │           └── 其他 Pod 清理逻辑
│               │                       │   │       │
│               │                       │   │       └── 3. 更新状态 - 行225
│               │                       │   │           └── l.setState(pod.GetID(), Removed)
│               │                       │   │
│               │                       │   ├── 2.2 调用 Sandboxer Shutdown - 行95
│               │                       │   │   │
│               │                       │   │   └── c.sandboxService.ShutdownSandbox(ctx, sandbox.Sandboxer, id)
│               │                       │   │       │   internal/cri/server/sandbox_service.go:105
│               │                       │   │       │
│               │                       │   │       │   **[sandboxService.ShutdownSandbox 调用链展开]**
│               │                       │   │       │
│               │                       │   │       ├── 1. CRI Sandbox Service 层 - sandbox_service.go:105
│               │                       │   │       │   │   func (c *criSandboxService) ShutdownSandbox(ctx, sandboxer, sandboxID)
│               │                       │   │       │   │
│               │                       │   │       │   └── ctrl.Shutdown(ctx, sandboxID) - 行110
│               │                       │   │       │       └── 调用 Sandbox Controller
│               │                       │   │       │
│               │                       │   │       ├── 2. [podsandbox Controller] Shutdown 实现
│               │                       │   │       │   │   internal/cri/server/podsandbox/sandbox_delete.go:31
│               │                       │   │       │   │   func (c *Controller) Shutdown(ctx, sandboxID)
│               │                       │   │       │   │
│               │                       │   │       │   ├── 2.1 获取沙箱对象 - 行32
│               │                       │   │       │   │   └── sandbox := c.store.Get(sandboxID)
│               │                       │   │       │   │
│               │                       │   │       │   ├── 2.2 清理沙箱根目录 - 行39-48
│               │                       │   │       │   │   ├── ensureRemoveAll(ctx, sandboxRootDir) - 行41
│               │                       │   │       │   │   │   └── /var/lib/containerd/.../sandboxes/<id>
│               │                       │   │       │   │   │
│               │                       │   │       │   │   └── ensureRemoveAll(ctx, volatileSandboxRootDir) - 行45
│               │                       │   │       │   │       └── /run/containerd/.../sandboxes/<id>
│               │                       │   │       │   │
│               │                       │   │       │   ├── 2.3 清理沙箱容器 Task - 行52
│               │                       │   │       │   │   │
│               │                       │   │       │   │   └── c.cleanupSandboxTask(ctx, sandbox.Container) - 行52
│               │                       │   │       │   │       │   sandbox_delete.go:69
│               │                       │   │       │   │       │
│               │                       │   │       │   │       ├── task, err := sbCntr.Task(ctx, nil) - 行70
│               │                       │   │       │   │       │
│               │                       │   │       │   │       ├── task.Delete(ctx, containerd.WithProcessKill) - 行76
│               │                       │   │       │   │       │   └── 删除 Task 并杀死所有进程
│               │                       │   │       │   │       │
│               │                       │   │       │   │       └── [防止 shim 泄露] - 行116-124
│               │                       │   │       │   │           └── c.client.TaskService().Delete(ctx, ...)
│               │                       │   │       │   │               └── 确保清理 task-service 中的记录
│               │                       │   │       │   │
│               │                       │   │       │   ├── 2.4 删除沙箱容器 - 行56
│               │                       │   │       │   │   └── sandbox.Container.Delete(ctx, containerd.WithSnapshotCleanup)
│               │                       │   │       │   │       └── 删除 pause 容器及其快照
│               │                       │   │       │   │
│               │                       │   │       │   └── 2.5 从 store 中移除 - 行64
│               │                       │   │       │       └── c.store.Remove(sandboxID)
│               │                       │   │       │
│               │                       │   │       └── 3. [controllerLocal] Shutdown 实现
│               │                       │   │           │   plugins/sandbox/controller.go:243
│               │                       │   │           │   func (c *controllerLocal) Shutdown(ctx, sandboxID)
│               │                       │   │           │
│               │                       │   │           ├── 3.1 获取 shim 服务 - 行244
│               │                       │   │           │   └── svc, err := c.getSandbox(ctx, sandboxID)
│               │                       │   │           │
│               │                       │   │           ├── 3.2 调用 shim Shutdown - 行249
│               │                       │   │           │   └── svc.ShutdownSandbox(ctx, &runtimeAPI.ShutdownSandboxRequest{...})
│               │                       │   │           │       └── 通过 ttrpc 调用 shim
│               │                       │   │           │
│               │                       │   │           └── 3.3 删除 shim - 行254
│               │                       │   │               └── c.shims.Delete(ctx, sandboxID)
│               │                       │   │                   └── 清理 shim 进程
│               │                       │   │
│               │                       │   ├── 2.3 清理沙箱根目录 - 行89
│               │                       │   │   └── ensureRemoveAll(ctx, c.getSandboxRootDir(id))
│               │                       │   │       └── /var/lib/containerd/io.containerd.grpc.v1.cri/sandboxes/<id>/
│               │                       │   │
│               │                       │   ├── 2.4 清理运行时目录 - 行94
│               │                       │   │   └── ensureRemoveAll(ctx, c.getVolatileSandboxRootDir(id))
│               │                       │   │       └── /run/containerd/io.containerd.grpc.v1.cri/sandboxes/<id>/
│               │                       │   │
│               │                       │   ├── 2.5 从 store 中删除 - 行100
│               │                       │   │   └── c.sandboxStore.Delete(id)
│               │                       │   │
│               │                       │   └── 2.6 释放沙箱名称 - 行101
│               │                       │       └── c.sandboxNameIndex.ReleaseByKey(id)
│               │                       │
│               │                       └── 3. **Linux 文件系统清理总结**
│               │                           ├── 沙箱配置: /var/lib/containerd/io.containerd.grpc.v1.cri/sandboxes/<id>/
│               │                           ├── 沙箱运行时: /run/containerd/io.containerd.grpc.v1.cri/sandboxes/<id>/
│               │                           └── 网络命名空间: 已在 StopPodSandbox 中清理
│               │
│               └── 3. evictPodLogsDirectories() - 清理 Pod 日志目录 - 行422
│                   └── 删除 /var/log/pods/<namespace>_<name>_<uid>/
│
│   ┌─────────────────────────────────────────────────────────────────────────────────────────┐
│   │ **为什么 StopPodSandbox 中还要再次 kill -9？**                                          │
│   ├─────────────────────────────────────────────────────────────────────────────────────────┤
│   │                                                                                         │
│   │  **问题背景**:                                                                          │
│   │  在正常 Pod 删除流程中，killContainersWithSyncResult() 已经停止了所有用户容器。        │
│   │  那为什么 StopPodSandbox 还要再次执行 stopContainer(ctx, container, 0)？               │
│   │                                                                                         │
│   │  **原因1: 防御性编程 - 处理异常情况**                                                   │
│   │  ├── 源码注释 (sandbox_stop.go:65-67):                                                 │
│   │  │   "Stop all containers inside the sandbox. This terminates the container forcibly,  │
│   │  │    and container may still be created, so production should not rely on this..."    │
│   │  ├── 可能有新容器在 stop 过程中被创建（虽然罕见）                                      │
│   │  └── 确保沙箱内所有容器都被停止，即使之前的 stop 失败或漏掉                           │
│   │                                                                                         │
│   │  **原因2: timeout=0 表示直接 SIGKILL**                                                  │
│   │  ├── c.stopContainer(ctx, container, 0) 中 timeout=0                                   │
│   │  ├── 这意味着不等待 gracePeriod，直接发送 SIGKILL                                      │
│   │  └── 这是"强制停止"，确保容器一定会终止（除非 D 状态）                                 │
│   │                                                                                         │
│   │  **原因3: GC 阶段的额外保护**                                                           │
│   │  ├── 源码: kuberuntime_gc.go:176-180                                                   │
│   │  │   "In normal cases, kubelet should've already called StopPodSandbox before         │
│   │  │    GC kicks in. To guard against the rare cases where this is not true,            │
│   │  │    try stopping the sandbox before removing it."                                    │
│   │  └── GC 不确定之前的 stop 是否成功，所以再次调用作为保险                              │
│   │                                                                                         │
│   │  **原因4: RemoveContainer 中的强制停止**                                                │
│   │  ├── 源码: container_remove.go:67-74                                                   │
│   │  │   if state == CONTAINER_RUNNING || state == CONTAINER_UNKNOWN:                      │
│   │  │       log.L.Infof("Forcibly stopping container %q", id)                             │
│   │  │       c.stopContainer(ctx, container, 0)                                            │
│   │  └── containerd 在 Remove 前检查容器状态，如果还在运行则强制停止                       │
│   │                                                                                         │
│   │  **总结**: 这是多层防御机制:                                                            │
│   │  1. killPod() - 正常优雅停止（SIGTERM → SIGKILL）                                      │
│   │  2. StopPodSandbox() - 确保沙箱内所有容器停止（timeout=0, 直接 SIGKILL）               │
│   │  3. GC removeSandbox() - 删除前再次确认停止                                            │
│   │  4. RemoveContainer() - containerd 层面的最终检查                                      │
│   │                                                                                         │
│   └─────────────────────────────────────────────────────────────────────────────────────────┘
```

### CRI 接口列表（供 containerd 源码查阅）

Pod 删除流程中使用的 CRI RuntimeService 接口:

```
┌─────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                               **CRI RuntimeService 接口列表**                                        │
├──────────────────────────────┬──────────────────────────────────────────────┬───────────────────────┤
│  接口名称                     │  源码位置 (kubelet)                          │  containerd 对应位置  │
├──────────────────────────────┼──────────────────────────────────────────────┼───────────────────────┤
│  StopContainer()             │  pkg/kubelet/cri/remote/remote_runtime.go:352│  pkg/cri/server/      │
│                              │  发送 SIGTERM，超时后 SIGKILL                │  container_stop.go    │
├──────────────────────────────┼──────────────────────────────────────────────┼───────────────────────┤
│  RemoveContainer()           │  pkg/kubelet/cri/remote/remote_runtime.go:376│  pkg/cri/server/      │
│                              │  删除容器及其日志                            │  container_remove.go  │
├──────────────────────────────┼──────────────────────────────────────────────┼───────────────────────┤
│  StopPodSandbox()            │  pkg/kubelet/cri/remote/remote_runtime.go:213│  pkg/cri/server/      │
│                              │  停止Pod沙箱，清理网络                       │  sandbox_stop.go      │
├──────────────────────────────┼──────────────────────────────────────────────┼───────────────────────┤
│  RemovePodSandbox()          │  pkg/kubelet/cri/remote/remote_runtime.go:233│  pkg/cri/server/      │
│                              │  删除Pod沙箱                                 │  sandbox_remove.go    │
├──────────────────────────────┼──────────────────────────────────────────────┼───────────────────────┤
│  PodSandboxStatus()          │  pkg/kubelet/cri/remote/remote_runtime.go:251│  pkg/cri/server/      │
│                              │  获取Pod沙箱状态                             │  sandbox_status.go    │
├──────────────────────────────┼──────────────────────────────────────────────┼───────────────────────┤
│  ListContainers()            │  pkg/kubelet/cri/remote/remote_runtime.go:394│  pkg/cri/server/      │
│                              │  列出Pod中的所有容器                         │  container_list.go    │
└──────────────────────────────┴──────────────────────────────────────────────┴───────────────────────┘
```

**gRPC 接口定义位置**: `staging/src/k8s.io/cri-api/pkg/apis/runtime/v1/api.proto`

**containerd CRI 实现**: `github.com/containerd/containerd/pkg/cri/server/`

```go
// StopContainer gRPC 请求结构 (api.proto)
message StopContainerRequest {
    string container_id = 1;  // 容器ID
    int64 timeout = 2;        // gracePeriod (秒)
}

// containerd 处理流程 (container_stop.go)
func (c *criService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) {
    // 1. 发送 SIGTERM
    task.Kill(ctx, syscall.SIGTERM)
    
    // 2. 等待 timeout 秒
    select {
    case <-time.After(timeout):
        // 3. 超时，发送 SIGKILL
        task.Kill(ctx, syscall.SIGKILL)
    case <-task.Wait(ctx):
        // 进程已退出
    }
}
```

---

## 各组件详细流程分析

### 阶段1：kubectl 发起删除请求

| 步骤 | 组件 | 操作 | 源码位置 |
|:---:|:----:|:-----|:---------|
| **1** | kubectl | 解析命令行参数，构建 DeleteOptions | `staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go` |
| **2** | kubectl | 发送 DELETE 请求到 API Server | `staging/src/k8s.io/kubectl/pkg/cmd/delete/delete.go` |

### 阶段2：API Server 处理删除请求

| 步骤 | 组件 | 操作 | 源码位置 |
|:---:|:----:|:-----|:---------|
| **1** | API Server | 认证、授权、准入控制 | `staging/src/k8s.io/apiserver/` |
| **2** | Pod Strategy | CheckGracefulDelete() 计算 gracePeriod | `pkg/registry/core/pod/strategy.go:165` |
| **3** | Generic Registry | 设置 DeletionTimestamp | `vendor/k8s.io/apiserver/pkg/registry/generic/registry/store.go:1050` |
| **4** | etcd | 持久化 Pod 对象（带 DeletionTimestamp）| etcd 存储 |

### 阶段3：Kubelet 检测并处理删除

| 步骤 | 组件 | 操作 | 源码位置 |
|:---:|:----:|:-----|:---------|
| **1** | Kubelet Watch | 检测到 Pod 的 DeletionTimestamp 变化 | `pkg/kubelet/config/` |
| **2** | Pod Workers | UpdatePod() 状态转换为 TerminatingPod | `pkg/kubelet/pod_workers.go:737` |
| **3** | Pod Workers | calculateEffectiveGracePeriod() | `pkg/kubelet/pod_workers.go:980` |
| **4** | Kubelet | SyncTerminatingPod() 开始终止流程 | `pkg/kubelet/kubelet.go:2003` |

### 阶段4：容器终止

| 步骤 | 组件 | 操作 | 源码位置 |
|:---:|:----:|:-----|:---------|
| **1** | Probe Manager | StopLivenessAndStartup() 停止健康检查 | `pkg/kubelet/prober/prober_manager.go` |
| **2** | Runtime Manager | KillPod() → killPodWithSyncResult() | `pkg/kubelet/kuberuntime/kuberuntime_manager.go:1360` |
| **3** | Runtime Manager | killContainersWithSyncResult() 并行杀死容器 | `pkg/kubelet/kuberuntime/kuberuntime_container.go:762` |
| **4** | Runtime Manager | executePreStopHook() 执行 PreStop 钩子 | `pkg/kubelet/kuberuntime/kuberuntime_container.go:627` |
| **5** | CRI Remote | StopContainer(SIGTERM → SIGKILL) | `pkg/kubelet/cri/remote/remote_runtime.go:352` |
| **6** | Runtime Manager | StopPodSandbox() 停止沙箱 | `pkg/kubelet/kuberuntime/kuberuntime_manager.go:1378` |

### 阶段5：资源清理

| 步骤 | 组件 | 操作 | 源码位置 |
|:---:|:----:|:-----|:---------|
| **1** | Kubelet | SyncTerminatedPod() | `pkg/kubelet/kubelet.go:2140` |
| **2** | Volume Manager | WaitForUnmount() 等待卷卸载 | `pkg/kubelet/volumemanager/volume_manager.go:446` |
| **3** | Kubelet | 等待卷路径清理 | `pkg/kubelet/kubelet.go:2167` |
| **4** | Secret Manager | UnregisterPod() | `pkg/kubelet/secret/` |
| **5** | ConfigMap Manager | UnregisterPod() | `pkg/kubelet/configmap/` |
| **6** | Pod Container Manager | Destroy() 删除 cgroups | `pkg/kubelet/cm/` |

### 阶段6：最终删除

| 步骤 | 组件 | 操作 | 源码位置 |
|:---:|:----:|:-----|:---------|
| **1** | Status Manager | TerminatePod() 标记终止状态 | `pkg/kubelet/status/status_manager.go:428` |
| **2** | Status Manager | syncPod() 同步状态到 API Server | `pkg/kubelet/status/status_manager.go:841` |
| **3** | Status Manager | canBeDeleted() 检查是否可删除 | `pkg/kubelet/status/status_manager.go:931` |
| **4** | Status Manager | Delete(gracePeriod=0) 最终删除 | `pkg/kubelet/status/status_manager.go:907` |
| **5** | API Server | 从 etcd 中删除 Pod 对象 | etcd |

---

## 操作系统原理

### 1. 进程信号处理

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              **Linux 信号处理机制**                                      │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  Container Runtime 发送 SIGTERM (信号15)                                               │
│  │                                                                                      │
│  ├── 容器主进程(PID 1)收到信号                                                         │
│  │   ├── 进程可以捕获并处理该信号                                                      │
│  │   ├── 执行优雅关闭逻辑（关闭连接、保存状态等）                                       │
│  │   └── 调用 exit() 正常退出                                                          │
│  │                                                                                      │
│  └── 等待 gracePeriod 秒                                                               │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  如果超时后进程仍在运行，发送 SIGKILL (信号9)                                          │
│  │                                                                                      │
│  ├── SIGKILL 不能被捕获、阻塞或忽略                                                    │
│  │                                                                                      │
│  ├── 内核直接终止进程:                                                                 │
│  │   1. 关闭所有打开的文件描述符                                                       │
│  │   2. 释放进程占用的内存                                                             │
│  │   3. 通知父进程 (通过 SIGCHLD)                                                      │
│  │   4. 清理进程表项                                                                   │
│  │                                                                                      │
│  └── **例外情况: D状态（不可中断睡眠）进程**                                           │
│      └── 即使 SIGKILL 也无法终止，必须等待 I/O 操作完成                               │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 2. Cgroups 资源隔离与清理

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              **Cgroups 清理流程**                                        │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  Pod Cgroup 层次结构:                                                                  │
│  /sys/fs/cgroup/                                                                       │
│  └── kubepods/                                                                         │
│      └── <qos-class>/  (burstable, besteffort, guaranteed)                             │
│          └── pod<pod-uid>/                                                             │
│              ├── <container-id-1>/                                                     │
│              └── <container-id-2>/                                                     │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  清理步骤:                                                                              │
│  │                                                                                      │
│  ├── 1. 终止 cgroup 内所有进程:                                                        │
│  │   echo 1 > /sys/fs/cgroup/.../pod<uid>/cgroup.kill  (cgroup v2)                    │
│  │   或者遍历 cgroup.procs 发送信号 (cgroup v1)                                        │
│  │                                                                                      │
│  ├── 2. 等待进程退出                                                                   │
│  │                                                                                      │
│  ├── 3. 移除子 cgroup                                                                  │
│  │   rmdir /sys/fs/cgroup/.../pod<uid>/<container-id>/                                 │
│  │                                                                                      │
│  └── 4. 移除 Pod cgroup                                                                │
│      rmdir /sys/fs/cgroup/.../pod<uid>/                                                │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **注意**: 如果 cgroup 内还有进程（如 D状态进程），rmdir 会失败                        │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 3. Namespace 清理

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              **Namespace 清理流程**                                      │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  Container 使用的 Namespace:                                                           │
│  │                                                                                      │
│  ├── **PID Namespace**: 进程隔离                                                       │
│  │   └── 容器 PID 1 退出后，namespace 自动清理                                         │
│  │                                                                                      │
│  ├── **Network Namespace**: 网络隔离                                                   │
│  │   └── Pod Sandbox 删除时清理 (StopPodSandbox)                                       │
│  │   └── 包括: veth pair、iptables 规则、路由表等                                      │
│  │                                                                                      │
│  ├── **Mount Namespace**: 文件系统隔离                                                 │
│  │   └── 容器停止后，挂载点自动 unmount                                                │
│  │   └── **如果有进程占用挂载点，unmount 会阻塞**                                      │
│  │                                                                                      │
│  ├── **UTS Namespace**: 主机名隔离                                                     │
│  │   └── 容器退出后自动清理                                                            │
│  │                                                                                      │
│  ├── **IPC Namespace**: 进程间通信隔离                                                 │
│  │   └── 容器退出后自动清理                                                            │
│  │                                                                                      │
│  └── **User Namespace** (可选): 用户隔离                                               │
│      └── 容器退出后自动清理                                                            │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 常见问题分析

### 问题1：D状态进程/线程能否被删除？

#### 答案：**不能直接删除，但 Pod 删除流程会继续进行**

#### 1.1 D状态基础分析

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **D状态进程（不可中断睡眠）分析**                                │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  D状态（TASK_UNINTERRUPTIBLE）的含义:                                                  │
│  │                                                                                      │
│  ├── 进程正在等待某些不可中断的 I/O 操作完成                                           │
│  │   - 磁盘 I/O (读写阻塞)                                                             │
│  │   - NFS/网络文件系统操作                                                            │
│  │   - 某些驱动程序操作                                                                │
│  │                                                                                      │
│  ├── **SIGKILL 对 D状态进程无效**                                                      │
│  │   - 内核不会将 SIGKILL 传递给 D状态进程                                             │
│  │   - 必须等待 I/O 操作完成后，进程才会检查信号并退出                                 │
│  │                                                                                      │
│  └── 常见触发 D状态的场景:                                                             │
│      - NFS 服务器无响应                                                                │
│      - 磁盘 I/O 错误（坏道、控制器故障）                                               │
│      - 存储驱动 bug                                                                    │
│      - 内核 bug                                                                        │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  对 Kubernetes Pod 删除的影响:                                                         │
│  │                                                                                      │
│  ├── 1. **CRI StopContainer 调用会卡住**                                               │
│  │   └── 发送 SIGTERM/SIGKILL 后，容器运行时等待进程退出                               │
│  │   └── D状态进程不响应信号，导致 StopContainer 超时                                  │
│  │                                                                                      │
│  ├── 2. **超时后 kubelet 行为**                                                        │
│  │   └── StopContainer 返回错误                                                        │
│  │   └── killPodWithSyncResult 返回失败                                                │
│  │   └── SyncTerminatingPod 返回错误                                                   │
│  │   └── Pod 保持在 Terminating 状态                                                   │
│  │                                                                                      │
│  ├── 3. **Pod 删除会卡在 Terminating 状态**                                            │
│  │   └── 因为 SyncTerminatingPod 无法成功完成                                          │
│  │   └── SyncTerminatedPod 不会被调用                                                  │
│  │   └── StatusManager 不会发送最终删除请求                                            │
│  │                                                                                      │
│  └── 4. **解决方案**                                                                   │
│      ├── 等待 D状态恢复（修复底层 I/O 问题）                                           │
│      ├── 强制删除: kubectl delete pod --force --grace-period=0                         │
│      │   └── 仅从 etcd 删除对象，不等待 kubelet 确认                                   │
│      └── 重启节点（最后手段）                                                          │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 1.2 D状态线程 vs D状态进程 - 关键区别分析

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **D状态线程与D状态进程的区别（深度剖析）**                             │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **Linux 线程模型基础**:                                                               │
│  │                                                                                      │
│  ├── Linux 使用 1:1 线程模型 (NPTL - Native POSIX Thread Library)                      │
│  ├── 每个用户空间线程对应一个内核调度实体 (task_struct)                                │
│  ├── 线程和进程在内核层面都是 task_struct，通过 clone() 系统调用创建                   │
│  └── **关键**: 信号处理是针对整个进程（线程组），而不是单个线程                        │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **场景对比分析**:                                                                     │
│  │                                                                                      │
│  │  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │  │                    **场景1: 主线程(PID 1)处于D状态**                        │    │
│  │  ├─────────────────────────────────────────────────────────────────────────────┤    │
│  │  │                                                                             │    │
│  │  │  进程结构:                                                                  │    │
│  │  │  ┌──────────────────────────────────────┐                                   │    │
│  │  │  │  Process (TGID=1)                    │                                   │    │
│  │  │  │  ├── Main Thread (TID=1) [D状态]    │ ← SIGKILL 目标                    │    │
│  │  │  │  ├── Worker Thread (TID=2) [S状态]  │                                   │    │
│  │  │  │  └── Worker Thread (TID=3) [R状态]  │                                   │    │
│  │  │  └──────────────────────────────────────┘                                   │    │
│  │  │                                                                             │    │
│  │  │  信号处理流程:                                                              │    │
│  │  │  1. SIGKILL 发送给进程 (TGID=1)                                            │    │
│  │  │  2. 内核选择一个非D状态线程处理致命信号                                     │    │
│  │  │  3. 该线程执行 do_group_exit()                                              │    │
│  │  │  4. 所有线程被标记为 TASK_KILLED                                           │    │
│  │  │  5. **D状态线程仍然不会被中断**，需等待 I/O 完成                           │    │
│  │  │  6. 进程变成僵尸状态，直到所有线程都退出                                    │    │
│  │  │                                                                             │    │
│  │  │  **结果**: 容器无法完全终止，Pod 卡在 Terminating                          │    │
│  │  └─────────────────────────────────────────────────────────────────────────────┘    │
│  │                                                                                      │
│  │  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │  │                    **场景2: 工作线程处于D状态**                             │    │
│  │  ├─────────────────────────────────────────────────────────────────────────────┤    │
│  │  │                                                                             │    │
│  │  │  进程结构:                                                                  │    │
│  │  │  ┌──────────────────────────────────────┐                                   │    │
│  │  │  │  Process (TGID=1)                    │                                   │    │
│  │  │  │  ├── Main Thread (TID=1) [S状态]    │                                   │    │
│  │  │  │  ├── Worker Thread (TID=2) [D状态]  │ ← 某个工作线程卡住                │    │
│  │  │  │  └── Worker Thread (TID=3) [R状态]  │                                   │    │
│  │  │  └──────────────────────────────────────┘                                   │    │
│  │  │                                                                             │    │
│  │  │  信号处理流程:                                                              │    │
│  │  │  1. SIGKILL 发送给进程 (TGID=1)                                            │    │
│  │  │  2. 主线程或其他可中断线程可以处理信号                                      │    │
│  │  │  3. 进程开始退出流程                                                        │    │
│  │  │  4. **D状态的工作线程仍然阻塞**                                            │    │
│  │  │  5. 进程无法完全退出，变成僵尸状态                                          │    │
│  │  │                                                                             │    │
│  │  │  **结果**: 同样会导致容器无法完全终止，Pod 卡在 Terminating                │    │
│  │  └─────────────────────────────────────────────────────────────────────────────┘    │
│  │                                                                                      │
│  └── **关键结论**: D状态线程和D状态进程的效果是**完全相同的**                         │
│      └── 只要有任何一个线程处于D状态，整个进程就无法完全终止                          │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **内核层面的解释**:                                                                   │
│  │                                                                                      │
│  │  // 内核信号处理 (kernel/signal.c)                                                  │
│  │  static void complete_signal(int sig, struct task_struct *p, ...)                   │
│  │  {                                                                                   │
│  │      // 对于致命信号，尝试找一个可以处理信号的线程                                  │
│  │      if (sig_fatal(p, sig)) {                                                       │
│  │          signal->flags = SIGNAL_GROUP_EXIT;                                         │
│  │          // 遍历所有线程，唤醒可唤醒的线程                                          │
│  │          for_each_thread(p, t) {                                                    │
│  │              if (t->state == TASK_UNINTERRUPTIBLE)                                  │
│  │                  continue;  // 跳过D状态线程，无法唤醒！                            │
│  │              signal_wake_up(t, 1);                                                  │
│  │          }                                                                           │
│  │      }                                                                               │
│  │  }                                                                                   │
│  │                                                                                      │
│  └── D状态线程在 I/O 完成前会一直阻塞，这是内核设计的保护机制                         │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **对 Pod 删除的影响对比表**:                                                          │
│                                                                                         │
│  ┌──────────────────┬─────────────────────┬─────────────────────┬─────────────────────┐│
│  │  场景            │  SIGTERM 效果       │  SIGKILL 效果       │  Pod 状态           ││
│  ├──────────────────┼─────────────────────┼─────────────────────┼─────────────────────┤│
│  │  主线程D状态     │  无效               │  无效(D不可中断)    │  卡在 Terminating   ││
│  ├──────────────────┼─────────────────────┼─────────────────────┼─────────────────────┤│
│  │  工作线程D状态   │  主线程可能响应     │  进程开始退出但卡住 │  卡在 Terminating   ││
│  ├──────────────────┼─────────────────────┼─────────────────────┼─────────────────────┤│
│  │  所有线程D状态   │  无效               │  无效               │  卡在 Terminating   ││
│  └──────────────────┴─────────────────────┴─────────────────────┴─────────────────────┘│
│                                                                                         │
│  **结论**: 无论是主线程还是工作线程处于D状态，都会导致Pod删除卡住                      │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 1.3 如何诊断D状态线程问题

```bash
# 查看进程中的D状态线程
ps -eLo pid,tid,stat,wchan:32,comm | grep -E "^[0-9]+.*D"

# 查看特定容器进程的所有线程状态
# 首先找到容器的 PID
crictl inspect <container_id> | grep pid

# 然后查看该进程的所有线程
ls /proc/<pid>/task/

# 查看每个线程的状态
cat /proc/<pid>/task/<tid>/status | grep State

# 查看D状态线程的等待位置（wchan）
cat /proc/<pid>/task/<tid>/wchan
# 常见输出: nfs_wait_bit_killable, blkdev_get_block, io_schedule 等
```

**相关源码分析**:

CRI StopContainer 超时处理 (`pkg/kubelet/cri/remote/remote_runtime.go`):
```go
func (r *remoteRuntimeService) StopContainer(ctx context.Context, containerID string, timeout int64) error {
    // 使用 timeout + 默认超时(2分钟) 作为总超时时间
    t := r.timeout + time.Duration(timeout)*time.Second
    ctx, cancel := context.WithTimeout(ctx, t)
    defer cancel()
    
    // 如果容器进程是D状态，这个调用可能会超时
    if _, err := r.runtimeClient.StopContainer(ctx, &runtimeapi.StopContainerRequest{
        ContainerId: containerID,
        Timeout:     timeout,
    }); err != nil {
        klog.ErrorS(err, "StopContainer from runtime service failed", "containerID", containerID)
        return err  // 返回错误，导致Pod停留在Terminating状态
    }
    return nil
}
```

---

### 问题2：删除请求已触发，但 kubelet 过一段时间才执行删除动作

#### 答案：**这是正常的设计行为，kubelet 使用异步工作队列处理 Pod 更新**

#### 2.1 Kubelet 删除延迟的根本原因分析

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                     **Kubelet 删除延迟机制深度剖析**                                     │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **延迟来源1: API Server → Kubelet 的 Watch 延迟**                                     │
│  │                                                                                      │
│  │  ┌─────────────────────────────────────────────────────────────────────┐            │
│  │  │  kubectl delete pod → API Server 设置 DeletionTimestamp           │            │
│  │  │              │                                                      │            │
│  │  │              ▼ (Watch 事件传播)                                     │            │
│  │  │  kubelet 的 Reflector 收到更新事件                                 │            │
│  │  │              │                                                      │            │
│  │  │  正常情况: 几十毫秒到几百毫秒                                      │            │
│  │  │  异常情况: 如果网络延迟或 API Server 负载高，可能达到秒级          │            │
│  │  └─────────────────────────────────────────────────────────────────────┘            │
│  │                                                                                      │
│  │  源码: pkg/kubelet/config/apiserver.go                                              │
│  │  - 使用 ListWatch 机制监听 Pod 变化                                                 │
│  │  - 默认 resync 周期为 0 (依赖 Watch 事件)                                           │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **延迟来源2: syncLoop 的处理间隔**                                                    │
│  │                                                                                      │
│  │  pkg/kubelet/kubelet.go:2330-2370                                                   │
│  │  func (kl *Kubelet) syncLoop(ctx, updates, handler) {                               │
│  │      // syncTicker 每 1 秒触发一次                                                  │
│  │      syncTicker := time.NewTicker(time.Second)                                      │
│  │                                                                                      │
│  │      // housekeepingTicker 每 2 秒触发一次 (housekeepingPeriod)                     │
│  │      housekeepingTicker := time.NewTicker(housekeepingPeriod)                       │
│  │                                                                                      │
│  │      for {                                                                           │
│  │          // 如果运行时有错误，使用指数退避，最长 5 秒                               │
│  │          if err := kl.runtimeState.runtimeErrors(); err != nil {                    │
│  │              time.Sleep(duration)  // 100ms 到 5s 的退避                            │
│  │              duration = min(max, factor*duration)                                   │
│  │              continue                                                                │
│  │          }                                                                           │
│  │          kl.syncLoopIteration(...)                                                  │
│  │      }                                                                               │
│  │  }                                                                                   │
│  │                                                                                      │
│  │  **关键**: 如果 containerRuntime 报错，kubelet 会进入退避模式                       │
│  │  磁盘满可能导致运行时错误 → 触发退避 → 延迟处理删除                                │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **延迟来源3: Pod Workers 工作队列**                                                   │
│  │                                                                                      │
│  │  pkg/kubelet/pod_workers.go:1493                                                    │
│  │  // 处理完一个 Pod 后，重新入队等待下次 resync                                      │
│  │  p.workQueue.Enqueue(podUID,                                                        │
│  │      wait.Jitter(p.resyncInterval, workerResyncIntervalJitterFactor))               │
│  │                                                                                      │
│  │  resyncInterval = SyncFrequency = 默认 1 分钟                                       │
│  │  workerResyncIntervalJitterFactor = 0.5                                             │
│  │                                                                                      │
│  │  这意味着: 如果一个 Pod 刚刚被同步过，下次主动同步可能在 30s-90s 后                │
│  │                                                                                      │
│  │  **但是**: 删除事件会通过 configCh 直接触发，不需要等待 resync                     │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **延迟来源4: 磁盘满导致的 Runtime 错误**                                              │
│  │                                                                                      │
│  │  当 Core 文件过多导致磁盘满时:                                                      │
│  │                                                                                      │
│  │  1. containerd/CRI-O 可能无法正常工作                                               │
│  │     └── 无法写入容器状态文件                                                        │
│  │     └── 无法创建/更新 bundle                                                        │
│  │                                                                                      │
│  │  2. kubelet 调用 CRI 接口可能失败                                                   │
│  │     └── GetPodStatus() 失败                                                         │
│  │     └── StopContainer() 可能失败                                                    │
│  │                                                                                      │
│  │  3. 触发 syncLoop 退避机制                                                          │
│  │     ┌───────────────────────────────────────────────────────────────────┐           │
│  │     │  第1次失败: 等待 100ms                                            │           │
│  │     │  第2次失败: 等待 200ms                                            │           │
│  │     │  第3次失败: 等待 400ms                                            │           │
│  │     │  第4次失败: 等待 800ms                                            │           │
│  │     │  第5次失败: 等待 1.6s                                             │           │
│  │     │  第6次失败: 等待 3.2s                                             │           │
│  │     │  第7次+:   等待 5s (达到最大值)                                   │           │
│  │     └───────────────────────────────────────────────────────────────────┘           │
│  │                                                                                      │
│  │  4. 这就是为什么删除"过一段时间"才执行                                             │
│  │                                                                                      │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 2.2 详细时序图: 磁盘满场景下的删除延迟

```
┌────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                        **磁盘满场景下 Pod 删除延迟时序图**                                          │
├────────────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                                    │
│  时间轴    kubectl     API Server    Kubelet syncLoop    Pod Worker    Container Runtime          │
│    │          │            │               │                 │               │                    │
│  T=0       delete ─────────▶              │                 │               │                    │
│    │          │     设置DeletionTimestamp │                 │               │                    │
│    │          │◀──────200 OK─────         │                 │               │                    │
│    │          │            │               │                 │               │                    │
│  T=50ms      │      Watch事件 ────────────▶                 │               │                    │
│    │          │            │               │                 │               │                    │
│  T=60ms      │            │        runtimeErrors()          │               │                    │
│    │          │            │        发现运行时错误 ◀─────────│───────────────│ 磁盘满错误        │
│    │          │            │               │                 │               │                    │
│  T=60ms      │            │        ┌──────┴──────┐          │               │                    │
│    │          │            │        │ 进入退避    │          │               │                    │
│    │          │            │        │ sleep 100ms │          │               │                    │
│    │          │            │        └──────┬──────┘          │               │                    │
│    │          │            │               │                 │               │                    │
│  T=160ms     │            │        runtimeErrors()          │               │                    │
│    │          │            │        仍然有错误               │               │                    │
│    │          │            │        sleep 200ms             │               │                    │
│    │          │            │               │                 │               │                    │
│  T=360ms     │            │        runtimeErrors()          │               │                    │
│    │          │            │        仍然有错误               │               │                    │
│    │          │            │        sleep 400ms             │               │                    │
│    │          │            │               │                 │               │                    │
│    ...      ...          ...             ...               ...             ...                  │
│    │          │            │               │                 │               │                    │
│  T=N秒       │            │        runtimeErrors()          │               │                    │
│    │          │            │        错误恢复(磁盘空间释放)   │               │                    │
│    │          │            │        ┌──────┴──────┐          │               │                    │
│    │          │            │        │syncLoopIter │          │               │                    │
│    │          │            │        └──────┬──────┘          │               │                    │
│    │          │            │               │                 │               │                    │
│  T=N+x秒     │            │    处理configCh中的DELETE事件   │               │                    │
│    │          │            │        ┌──────┴──────┐          │               │                    │
│    │          │            │        │HandlePodUpdates        │               │                    │
│    │          │            │        └──────┬──────┘          │               │                    │
│    │          │            │               │────UpdatePod───▶│               │                    │
│    │          │            │               │                 │──SyncTerminating──▶               │
│    │          │            │               │                 │               │                    │
│                                                                                                    │
└────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 2.3 关键源码位置

```
pkg/kubelet/kubelet.go:2354-2360 - syncLoop 退避逻辑
├── runtimeState.runtimeErrors() 检查运行时状态
├── 如果有错误，进入指数退避 (100ms → 5s)
└── 这是删除延迟的主要原因之一

pkg/kubelet/kubelet.go:2432-2435 - DELETE 事件处理
├── case kubetypes.DELETE:
│   klog.V(2).InfoS("SyncLoop DELETE", "source", u.Source, "pods", klog.KObjSlice(u.Pods))
│   // DELETE 被当作 UPDATE 处理，因为需要优雅删除
│   handler.HandlePodUpdates(u.Pods)

pkg/kubelet/pod_workers.go:600 - newPodWorkers() 初始化
├── resyncInterval: kubeCfg.SyncFrequency.Duration (默认 1 分钟)
└── 影响 Pod 定期重新同步的间隔
```

---

### 问题3：Core 文件导致磁盘满对 Pod 删除的影响

#### 答案：**可能会导致延迟，但通常不会完全卡住删除**

#### 详细分析：

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **磁盘空间不足对 Pod 删除的影响**                                │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  Core 文件导致磁盘空间不足可能影响的操作:                                               │
│  │                                                                                      │
│  ├── 1. **容器日志删除** (可能受影响)                                                  │
│  │   └── removeContainerLog() 需要写入新文件或更新元数据                               │
│  │   └── 如果磁盘满，可能失败                                                          │
│  │                                                                                      │
│  ├── 2. **容器文件系统清理** (可能受影响)                                              │
│  │   └── overlay 文件系统操作可能需要一些空间                                          │
│  │   └── 但通常删除操作本身不需要额外空间                                              │
│  │                                                                                      │
│  ├── 3. **Volume 卸载** (通常不受影响)                                                 │
│  │   └── unmount 操作不需要写入磁盘空间                                                │
│  │                                                                                      │
│  ├── 4. **Cgroups 删除** (不受影响)                                                    │
│  │   └── cgroups 是内存文件系统                                                        │
│  │                                                                                      │
│  └── 5. **API Server 状态更新** (通常不受影响)                                         │
│      └── 网络操作，不依赖本地磁盘                                                      │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  更可能影响 Pod 删除的磁盘空间问题:                                                    │
│  │                                                                                      │
│  ├── 1. **kubelet 无法写入日志**                                                       │
│  │   └── kubelet 可能无法记录删除进度                                                  │
│  │   └── 但不会直接阻止删除                                                            │
│  │                                                                                      │
│  ├── 2. **容器运行时问题**                                                             │
│  │   └── containerd/CRI-O 可能无法写入状态文件                                         │
│  │   └── 可能导致运行时不稳定                                                          │
│  │   └── **触发 syncLoop 退避机制，导致删除延迟**                                      │
│  │                                                                                      │
│  └── 3. **Eviction 触发**                                                              │
│      └── 如果触发了 nodefs.available 或 imagefs.available 阈值                        │
│      └── kubelet 会优先驱逐 Pod 而不是删除                                             │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **结论**: Core 文件导致磁盘满**通常不会直接卡住 Pod 删除**，但可能:                   │
│  │                                                                                      │
│  ├── 1. 导致容器日志清理失败，Pod 容器无法被 GC 完全清理                               │
│  ├── 2. 导致 kubelet 或容器运行时不稳定                                                │
│  ├── 3. **触发 syncLoop 退避，导致删除延迟（这就是你遇到的场景）**                     │
│  └── 4. 触发 Eviction，改变 Pod 终止优先级                                             │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

**相关源码分析**:

容器日志删除 (`pkg/kubelet/kuberuntime/kuberuntime_container.go`):
```go
func (m *kubeGenericRuntimeManager) removeContainer(ctx context.Context, containerID string) error {
    // 先删除容器日志
    // TODO: Separate log and container lifecycle management.
    if err := m.removeContainerLog(ctx, containerID); err != nil {
        return err  // 如果日志删除失败，容器删除也会失败
    }
    // 再删除容器
    return m.runtimeService.RemoveContainer(ctx, containerID)
}
```

syncLoop 退避逻辑 (`pkg/kubelet/kubelet.go`):
```go
func (kl *Kubelet) syncLoop(ctx context.Context, updates <-chan kubetypes.PodUpdate, handler SyncHandler) {
    const (
        base   = 100 * time.Millisecond
        max    = 5 * time.Second
        factor = 2
    )
    duration := base
    
    for {
        if err := kl.runtimeState.runtimeErrors(); err != nil {
            klog.ErrorS(err, "Skipping pod synchronization")
            // 指数退避
            time.Sleep(duration)
            duration = time.Duration(math.Min(float64(max), factor*float64(duration)))
            continue  // 跳过本次同步，不处理删除事件
        }
        // reset backoff if we have a success
        duration = base
        
        kl.syncLoopIteration(ctx, updates, handler, ...)
    }
}
```

Eviction Manager 磁盘检查 (`pkg/kubelet/eviction/eviction_manager.go`):
```go
// 磁盘压力阈值
signalToReclaimFunc[evictionapi.SignalNodeFsAvailable] = nodeReclaimFuncs{
    containerGC.DeleteAllUnusedContainers, 
    imageGC.DeleteUnusedImages,
}
```

---

### 问题3：其他可能导致 Pod 删除卡住的原因

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Pod 删除可能卡住的其他原因**                                  │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. Finalizers 未移除**                                                              │
│  ├── Pod 有 finalizers 字段                                                            │
│  ├── Controller 负责处理后移除 finalizer                                               │
│  ├── 如果 Controller 不工作，finalizer 永远不会被移除                                  │
│  └── 解决: kubectl patch pod <name> -p '{"metadata":{"finalizers":null}}'              │
│                                                                                         │
│  源码: pkg/controller/job/job_controller.go:1151                                       │
│  func canRemoveFinalizer(logger klog.Logger, jobCtx *syncJobCtx, pod *v1.Pod, ...) bool│
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **2. PreStop Hook 执行时间过长**                                                      │
│  ├── PreStop Hook 消耗了大部分 gracePeriod                                             │
│  ├── 如果 Hook 执行时间 >= gracePeriod，实际留给 SIGTERM 的时间很少                    │
│  ├── 最小 gracePeriod 为 2 秒                                                          │
│  └── 解决: 优化 PreStop Hook 或增加 terminationGracePeriodSeconds                      │
│                                                                                         │
│  源码: pkg/kubelet/kuberuntime/kuberuntime_container.go:627                            │
│  func (m *kubeGenericRuntimeManager) executePreStopHook(ctx, pod, containerID, ...) {  │
│      select {                                                                          │
│      case <-time.After(time.Duration(gracePeriod) * time.Second):                      │
│          klog.V(2).Info("PreStop hook not completed in grace period")                  │
│      case <-done:                                                                      │
│      }                                                                                  │
│  }                                                                                      │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **3. Volume 卸载失败**                                                                │
│  ├── NFS/iSCSI 等网络存储无响应                                                        │
│  ├── 本地卷被进程占用 (lsof)                                                           │
│  ├── 存储驱动 bug                                                                      │
│  └── 解决: 检查存储状态，强制 unmount                                                  │
│                                                                                         │
│  源码: pkg/kubelet/volumemanager/volume_manager.go:446                                 │
│  func (vm *volumeManager) WaitForUnmount(ctx context.Context, pod *v1.Pod) error {     │
│      err := wait.PollUntilContextTimeout(ctx,                                          │
│          podAttachAndMountRetryInterval,                                               │
│          podAttachAndMountTimeout,  // 默认2分钟                                        │
│          true,                                                                          │
│          vm.verifyVolumesUnmountedFunc(uniquePodName))                                 │
│      // 如果超时，返回错误                                                              │
│  }                                                                                      │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **4. Container Runtime 无响应**                                                       │
│  ├── containerd/CRI-O 进程卡死                                                         │
│  ├── 容器运行时资源耗尽                                                                │
│  ├── Docker shim 问题 (旧版本)                                                         │
│  └── 解决: 重启容器运行时，检查运行时日志                                              │
│                                                                                         │
│  源码: pkg/kubelet/cri/remote/remote_runtime.go:352                                    │
│  // 默认超时时间加上 gracePeriod                                                        │
│  t := r.timeout + time.Duration(timeout)*time.Second                                   │
│  ctx, cancel := context.WithTimeout(ctx, t)                                            │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **5. API Server 不可达**                                                              │
│  ├── 网络分区                                                                          │
│  ├── API Server 过载                                                                   │
│  ├── 认证/授权问题                                                                     │
│  └── 影响: Status Manager 无法更新 Pod 状态和发送最终删除请求                          │
│                                                                                         │
│  源码: pkg/kubelet/status/status_manager.go:907                                        │
│  err = m.kubeClient.CoreV1().Pods(pod.Namespace).Delete(...)                          │
│  if err != nil {                                                                       │
│      klog.InfoS("Failed to delete status for pod", "pod", klog.KObj(pod), "err", err) │
│      return  // 删除失败，会重试                                                        │
│  }                                                                                      │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **6. PVC Protection 阻止删除**                                                        │
│  ├── 如果 Pod 使用的 PVC 有 finalizer                                                  │
│  ├── PVC 不会被删除直到 Pod 停止使用                                                   │
│  ├── 如果 PVC Controller 有问题，可能影响 Pod 清理                                     │
│  └── 解决: 检查 PVC 状态，必要时移除 finalizer                                         │
│                                                                                         │
│  源码: pkg/controller/volume/pvcprotection/pvc_protection_controller.go:164            │
│  if protectionutil.IsDeletionCandidate(pvc, volumeutil.PVCProtectionFinalizer) {       │
│      isUsed, err := c.isBeingUsed(ctx, pvc)                                            │
│      if !isUsed {                                                                      │
│          return c.removeFinalizer(ctx, pvc)                                            │
│      }                                                                                  │
│  }                                                                                      │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **7. Node 压力导致 kubelet 降级**                                                     │
│  ├── 内存/CPU 压力                                                                     │
│  ├── kubelet OOM                                                                       │
│  ├── 系统负载过高                                                                      │
│  └── 解决: 检查节点资源状态，可能需要驱逐其他工作负载                                  │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **8. Zombie 进程问题**                                                                │
│  ├── 容器 init 进程未正确回收子进程                                                    │
│  ├── 大量 zombie 进程可能影响进程表                                                    │
│  └── 解决: 使用 tini 或类似的 init 进程                                                │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 关键问题深度分析

### 问题4：PVC/Finalizer 保护机制详解

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **PVC Finalizer 保护机制详解**                                   │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. Finalizer 的作用**                                                                │
│  │                                                                                      │
│  │  Finalizer 是 Kubernetes 的一种保护机制，防止资源被意外删除                          │
│  │  常见的 Finalizer:                                                                   │
│  │  ├── kubernetes.io/pvc-protection - PVC 保护                                        │
│  │  ├── kubernetes.io/pv-protection - PV 保护                                          │
│  │  └── batch.kubernetes.io/job-tracking - Job 跟踪                                    │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **2. Pod 删除时 PVC 的处理流程**                                                       │
│  │                                                                                      │
│  │  [有 PVC 挂载的 Pod]                                                                │
│  │  │                                                                                   │
│  │  ├── 阶段1: API Server 设置 DeletionTimestamp                                       │
│  │  │   └── Pod 进入 Terminating 状态                                                  │
│  │  │   └── **PVC 不受影响，保持 Bound 状态**                                          │
│  │  │                                                                                   │
│  │  ├── 阶段2: kubelet SyncTerminatingPod                                              │
│  │  │   └── 停止容器，但此时卷仍然挂载                                                 │
│  │  │                                                                                   │
│  │  ├── 阶段3: kubelet SyncTerminatedPod                                               │
│  │  │   └── volumeManager.WaitForUnmount(pod)                                          │
│  │  │       └── 等待卷卸载完成                                                         │
│  │  │       └── **此时 PVC 仍然存在，只是不再被此 Pod 使用**                           │
│  │  │                                                                                   │
│  │  └── 阶段4: Pod 从 etcd 删除                                                        │
│  │      └── PVC Protection Controller 检测到 Pod 不再使用 PVC                          │
│  │          └── pkg/controller/volume/pvcprotection/pvc_protection_controller.go:164   │
│  │              └── isBeingUsed(ctx, pvc) 返回 false                                   │
│  │                  └── **如果 PVC 有 DeletionTimestamp，此时可以移除 Finalizer**      │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **3. 如果 Pod 有 Finalizer**                                                          │
│  │                                                                                      │
│  │  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  │  步骤                    │  行为                                               │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  1. kubectl delete pod   │  API Server 设置 DeletionTimestamp                 │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  2. 检查 Finalizers     │  len(accessor.GetFinalizers()) != 0               │   │
│  │  │                          │  → pendingFinalizers = true                        │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  3. 更新到 etcd          │  Pod 保留，只更新 DeletionTimestamp                │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  4. kubelet 正常处理    │  停止容器、清理资源                                 │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  5. Controller 移除      │  处理完成后 Patch 移除 Finalizer                   │   │
│  │  │     Finalizer            │                                                     │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  6. 最终删除             │  statusManager 检测 canBeDeleted() = true          │   │
│  │  │                          │  发送 DELETE 请求，从 etcd 删除                     │   │
│  │  └──────────────────────────────────────────────────────────────────────────────┘   │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **4. 如果 Pod 没有 Finalizer (常见情况)**                                             │
│  │                                                                                      │
│  │  ┌──────────────────────────────────────────────────────────────────────────────┐   │
│  │  │  步骤                    │  行为                                               │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  1. kubectl delete pod   │  API Server 设置 DeletionTimestamp                 │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  2. 检查 Finalizers     │  len(accessor.GetFinalizers()) == 0               │   │
│  │  │                          │  → pendingFinalizers = false                       │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  3. 设置 graceful=true   │  如果 gracePeriod > 0，需要优雅删除               │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  4. 更新到 etcd          │  updateForGracefulDeletionAndFinalizers()          │   │
│  │  │                          │  设置 DeletionTimestamp，等待 kubelet              │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  5. kubelet 清理完成    │  statusManager.TerminatePod() 设置 podIsFinished   │   │
│  │  ├──────────────────────────────────────────────────────────────────────────────┤   │
│  │  │  6. 最终删除             │  canBeDeleted() = true，立即从 etcd 删除           │   │
│  │  └──────────────────────────────────────────────────────────────────────────────┘   │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **源码位置**:                                                                          │
│  ├── Finalizer 检查: vendor/k8s.io/apiserver/pkg/registry/generic/registry/store.go:1084│
│  ├── PVC Protection: pkg/controller/volume/pvcprotection/pvc_protection_controller.go  │
│  └── canBeDeleted: pkg/kubelet/status/status_manager.go:931                            │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

### 问题5：Pod 状态变化全流程

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Pod 删除过程中的状态变化**                                     │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **时间线**                                                                              │
│  │                                                                                      │
│  T0: kubectl delete pod                                                                 │
│  │   ├── Pod.Status.Phase = Running (未变)                                             │
│  │   ├── Pod.DeletionTimestamp = nil → 当前时间+gracePeriod                            │
│  │   ├── Pod.DeletionGracePeriodSeconds = nil → gracePeriod值                          │
│  │   └── Pod.Generation += 1                                                           │
│  │                                                                                      │
│  T1: kubelet 收到 Watch 事件                                                            │
│  │   ├── Pod.Status.Phase = Running (未变，API Server状态)                             │
│  │   └── kubelet 内部状态:                                                             │
│  │       ├── podSyncStatus.deleted = true                                              │
│  │       ├── podSyncStatus.terminatingAt = now                                         │
│  │       └── podSyncStatus.WorkType() = TerminatingPod                                 │
│  │                                                                                      │
│  T2: SyncTerminatingPod 开始                                                            │
│  │   ├── Pod.Status.Phase = Running (未变)                                             │
│  │   └── 容器状态:                                                                     │
│  │       └── ContainerStatus.State = Running → Terminating (内部)                      │
│  │                                                                                      │
│  T3: 容器收到 SIGTERM                                                                   │
│  │   ├── Pod.Status.Phase = Running (未变)                                             │
│  │   └── 容器状态:                                                                     │
│  │       └── 容器进程收到信号，开始优雅关闭                                            │
│  │                                                                                      │
│  T4: 容器退出 (或 SIGKILL 后强制退出)                                                   │
│  │   └── 容器状态:                                                                     │
│  │       ├── ContainerStatus.State.Terminated.ExitCode = 0/非0                         │
│  │       ├── ContainerStatus.State.Terminated.Reason = "Completed"/"Error"             │
│  │       └── ContainerStatus.State.Terminated.FinishedAt = now                         │
│  │                                                                                      │
│  T5: SyncTerminatingPod 完成                                                            │
│  │   └── kubelet 内部状态:                                                             │
│  │       ├── podSyncStatus.startedTerminating = true                                   │
│  │       └── podSyncStatus.WorkType() = TerminatedPod                                  │
│  │                                                                                      │
│  T6: SyncTerminatedPod 开始                                                             │
│  │   └── kubelet 内部状态:                                                             │
│  │       └── podSyncStatus.terminatedAt = now                                          │
│  │                                                                                      │
│  T7: generateAPIPodStatus()                                                             │
│  │   └── Pod.Status.Phase = Running → **Succeeded/Failed**                             │
│  │       ├── 所有容器 ExitCode=0 → Succeeded                                           │
│  │       └── 任何容器 ExitCode!=0 → Failed                                             │
│  │                                                                                      │
│  T8: statusManager.TerminatePod()                                                       │
│  │   └── kubelet 内部状态:                                                             │
│  │       └── podIsFinished = true                                                      │
│  │                                                                                      │
│  T9: syncPod() → canBeDeleted() = true                                                  │
│  │   ├── 条件满足:                                                                     │
│  │   │   ├── pod.DeletionTimestamp != nil ✓                                            │
│  │   │   ├── podutil.IsPodPhaseTerminal(pod.Status.Phase) ✓                            │
│  │   │   └── podIsFinished == true ✓                                                   │
│  │   └── 发送最终 DELETE 请求 (GracePeriodSeconds=0)                                   │
│  │                                                                                      │
│  T10: Pod 从 etcd 删除                                                                  │
│      └── Pod 对象不再存在                                                              │
│                                                                                         │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **状态变化图**:                                                                        │
│                                                                                         │
│  Pod.Status.Phase:                                                                      │
│  ┌─────────┐    ┌─────────────┐    ┌───────────────────┐                               │
│  │ Running │───▶│ (Terminating)│───▶│ Succeeded/Failed │                               │
│  └─────────┘    │ (内部状态)   │    └───────────────────┘                               │
│                 └─────────────┘                                                         │
│                                                                                         │
│  podSyncStatus.WorkType():                                                              │
│  ┌─────────┐    ┌───────────────┐    ┌──────────────┐                                  │
│  │ SyncPod │───▶│TerminatingPod │───▶│TerminatedPod │                                  │
│  └─────────┘    └───────────────┘    └──────────────┘                                  │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

### 问题6：Linux 系统调用汇总

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Pod 删除涉及的 Linux 系统调用**                                │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. 信号操作 (Signal)**                                                               │
│  │                                                                                      │
│  │  ┌─────────────┬────────────────────────────────────────────────────────────────┐   │
│  │  │  调用点      │  系统调用                                                       │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  发送SIGTERM │  kill(pid, SIGTERM) 或 kill(pid, 自定义信号)                   │   │
│  │  │             │  通过: runc kill <container-id> 15                             │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  发送SIGKILL │  kill(pid, SIGKILL)                                            │   │
│  │  │             │  通过: runc kill <container-id> 9                              │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  等待退出    │  waitpid(pid, &status, 0)                                      │   │
│  │  │             │  containerd 通过 task.Wait() 监听                              │   │
│  │  └─────────────┴────────────────────────────────────────────────────────────────┘   │
│  │                                                                                      │
│  │  **源码路径**:                                                                       │
│  │  └── containerd/vendor/github.com/containerd/go-runc/runc.go:392                    │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **2. Cgroups 操作**                                                                    │
│  │                                                                                      │
│  │  ┌─────────────┬────────────────────────────────────────────────────────────────┐   │
│  │  │  操作        │  文件系统操作                                                   │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  cgroup v2  │                                                                │   │
│  │  │  终止进程    │  echo 1 > /sys/fs/cgroup/.../cgroup.kill                      │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  cgroup v1  │                                                                │   │
│  │  │  终止进程    │  读取 cgroup.procs，逐个 kill                                  │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  删除cgroup │  rmdir /sys/fs/cgroup/.../pod<uid>/                            │   │
│  │  │             │  (必须先终止所有进程)                                           │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  更新QOS    │  写入 /sys/fs/cgroup/kubepods/<qos>/                           │   │
│  │  │             │  更新 cpu.weight, memory.max 等                                │   │
│  │  └─────────────┴────────────────────────────────────────────────────────────────┘   │
│  │                                                                                      │
│  │  **源码路径**:                                                                       │
│  │  └── pkg/kubelet/cm/pod_container_manager_linux.go                                  │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **3. Namespace 操作**                                                                  │
│  │                                                                                      │
│  │  ┌─────────────┬────────────────────────────────────────────────────────────────┐   │
│  │  │  操作        │  系统调用/文件操作                                              │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  PID NS     │  进程退出后自动清理                                            │   │
│  │  │             │  (容器 PID 1 退出，namespace 销毁)                             │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  Net NS     │  unlink("/var/run/netns/<sandbox-id>")                         │   │
│  │  │  删除       │  sandbox.NetNS.Remove()                                        │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  Mount NS   │  umount() 卸载所有挂载点                                       │   │
│  │  │             │  容器退出后自动清理                                            │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  User NS    │  释放 UID/GID 映射范围                                         │   │
│  │  │             │  kl.usernsManager.Release(pod.UID)                             │   │
│  │  └─────────────┴────────────────────────────────────────────────────────────────┘   │
│  │                                                                                      │
│  │  **源码路径**:                                                                       │
│  │  └── containerd/internal/cri/server/sandbox_stop.go:122                             │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **4. 文件系统操作**                                                                    │
│  │                                                                                      │
│  │  ┌─────────────┬────────────────────────────────────────────────────────────────┐   │
│  │  │  操作        │  系统调用                                                       │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  卸载卷     │  umount("/var/lib/kubelet/pods/<uid>/volumes/...")             │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  删除目录    │  rmdir(), unlink()                                             │   │
│  │  │             │  清理 /var/lib/kubelet/pods/<uid>/                             │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  overlay    │  umount overlay 文件系统                                       │   │
│  │  │  清理       │  删除 lower/upper/work/merged 目录                             │   │
│  │  └─────────────┴────────────────────────────────────────────────────────────────┘   │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **5. 网络操作**                                                                        │
│  │                                                                                      │
│  │  ┌─────────────┬────────────────────────────────────────────────────────────────┐   │
│  │  │  操作        │  内核操作                                                       │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  CNI 清理   │  netPlugin.Remove() 调用 CNI 插件                              │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  veth 删除  │  ip link del veth<xxx>                                         │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  iptables   │  清理 DNAT/SNAT 规则                                           │   │
│  │  ├─────────────┼────────────────────────────────────────────────────────────────┤   │
│  │  │  IP 释放    │  IPAM 释放 Pod IP 地址                                         │   │
│  │  └─────────────┴────────────────────────────────────────────────────────────────┘   │
│  │                                                                                      │
│  │  **源码路径**:                                                                       │
│  │  └── containerd/internal/cri/server/sandbox_stop.go:153                             │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

### 问题7：QOS 资源更新时机

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **QOS Cgroups 更新时机详解**                                    │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **UpdateQOSCgroups() 调用时机**:                                                       │
│  │                                                                                      │
│  │  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │  │  时机                │  源码位置                          │  说明              │    │
│  │  ├─────────────────────────────────────────────────────────────────────────────┤    │
│  │  │  1. Pod 删除后       │  pkg/kubelet/kubelet_pods.go:871   │  killPod() 成功后 │    │
│  │  ├─────────────────────────────────────────────────────────────────────────────┤    │
│  │  │  2. Pod 创建前       │  pkg/kubelet/kubelet.go:1887       │  EnsureExists之前 │    │
│  │  ├─────────────────────────────────────────────────────────────────────────────┤    │
│  │  │  3. 周期性更新       │  pkg/kubelet/kubelet.go            │  housekeeping     │    │
│  │  └─────────────────────────────────────────────────────────────────────────────┘    │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **在 Pod 删除流程中的具体调用**:                                                       │
│  │                                                                                      │
│  │  SyncTerminatingPod 阶段:                                                           │
│  │  │                                                                                   │
│  │  │  killPod() - pkg/kubelet/kubelet_pods.go:866                                     │
│  │  │  │                                                                                │
│  │  │  ├── 1. kl.containerRuntime.KillPod(...)                                         │
│  │  │  │   └── 停止所有容器                                                            │
│  │  │  │                                                                                │
│  │  │  └── 2. kl.containerManager.UpdateQOSCgroups() - 行871                           │
│  │  │      │                                                                            │
│  │  │      └── 更新 QOS cgroup 配置                                                    │
│  │  │          │                                                                        │
│  │  │          └── cm.qosContainerManager.UpdateCgroups()                              │
│  │  │              │   pkg/kubelet/cm/container_manager_linux.go:547                   │
│  │  │              │                                                                    │
│  │  │              └── 重新计算各 QOS 类别的资源配额                                   │
│  │  │                  │                                                                │
│  │  │                  ├── Guaranteed: 保持资源限制                                    │
│  │  │                  ├── Burstable: 重新分配 CPU shares                              │
│  │  │                  └── BestEffort: 更新可用资源                                    │
│  │  │                                                                                   │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **更新内容**:                                                                          │
│  │                                                                                      │
│  │  /sys/fs/cgroup/kubepods/                                                           │
│  │  ├── burstable/                                                                     │
│  │  │   ├── cpu.weight          ← 重新计算 CPU 权重                                   │
│  │  │   ├── memory.max          ← 重新计算内存限制                                    │
│  │  │   └── memory.min          ← 重新计算内存保证                                    │
│  │  ├── besteffort/                                                                    │
│  │  │   └── ... (类似)                                                                 │
│  │  └── pod<uid>/               ← 已删除 Pod 的 cgroup 将被移除                       │
│  │                                                                                      │
│  ──────────────────────────────────────────────────────────────────────────────────────│
│                                                                                         │
│  **注意事项**:                                                                          │
│  │                                                                                      │
│  │  1. UpdateQOSCgroups() 的错误不会阻止 Pod 删除                                      │
│  │     └── 只记录日志: klog.V(2).InfoS("Failed to update QoS cgroups...", "err", err) │
│  │                                                                                      │
│  │  2. 真正删除 Pod cgroup 在 SyncTerminatedPod 阶段                                   │
│  │     └── pcm.Destroy(name) - pkg/kubelet/kubelet.go:2195                             │
│  │                                                                                      │
│  │  3. QOS 更新是节点级别的操作                                                        │
│  │     └── 影响该节点上所有 Pod 的资源分配                                             │
│  │                                                                                      │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

**相关源码**:

```go
// pkg/kubelet/kubelet_pods.go:866
func (kl *Kubelet) killPod(ctx context.Context, pod *v1.Pod, p kubecontainer.Pod, gracePeriodOverride *int64) error {
    // 停止所有容器
    if err := kl.containerRuntime.KillPod(ctx, pod, p, gracePeriodOverride); err != nil {
        return err
    }
    // **更新 QOS cgroups**
    if err := kl.containerManager.UpdateQOSCgroups(); err != nil {
        klog.V(2).InfoS("Failed to update QoS cgroups while killing pod", "err", err)
    }
    return nil
}

// pkg/kubelet/cm/container_manager_linux.go:547
func (cm *containerManagerImpl) UpdateQOSCgroups() error {
    return cm.qosContainerManager.UpdateCgroups()
}
```

---

## 如何复用 Pod 的关联资源

当删除 Pod 后，如果希望保留并复用其关联的资源（如 PVC、ConfigMap、Secret），需要了解这些资源的生命周期和配置方式。

### 资源类型与生命周期

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Pod 关联资源的生命周期对比**                                    │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  ┌────────────────┬────────────────────┬───────────────────────────────────────────────┐│
│  │  资源类型       │  默认行为           │  与 Pod 的关系                                ││
│  ├────────────────┼────────────────────┼───────────────────────────────────────────────┤│
│  │  **PVC**       │  Pod 删除后保留    │  独立生命周期，需手动或通过 PV 策略删除       ││
│  ├────────────────┼────────────────────┼───────────────────────────────────────────────┤│
│  │  **ConfigMap** │  Pod 删除后保留    │  独立生命周期，需手动删除                     ││
│  ├────────────────┼────────────────────┼───────────────────────────────────────────────┤│
│  │  **Secret**    │  Pod 删除后保留    │  独立生命周期，需手动删除                     ││
│  ├────────────────┼────────────────────┼───────────────────────────────────────────────┤│
│  │  **emptyDir**  │  Pod 删除时清除    │  与 Pod 生命周期绑定                          ││
│  ├────────────────┼────────────────────┼───────────────────────────────────────────────┤│
│  │  **hostPath**  │  Pod 删除后保留    │  数据在宿主机上，与 Pod 无关                  ││
│  └────────────────┴────────────────────┴───────────────────────────────────────────────┘│
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 1. PVC (PersistentVolumeClaim) 复用

#### 1.1 默认情况 - PVC 天然支持复用

```yaml
# PVC 定义 - 独立于 Pod 创建
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-pvc
  namespace: default
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
---
# Pod 1 - 使用 PVC
apiVersion: v1
kind: Pod
metadata:
  name: pod-1
spec:
  containers:
  - name: app
    image: nginx
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: my-pvc  # 引用已存在的 PVC

# Pod 1 删除后，PVC 仍然存在
# 新的 Pod 2 可以继续使用同一个 PVC
```

#### 1.2 PV 回收策略 (persistentVolumeReclaimPolicy)

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **PV 回收策略详解**                                              │
│                          staging/src/k8s.io/api/core/v1/types.go:374-388                │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  ┌────────────────┬────────────────────────────────────────────────────────────────────┐│
│  │  策略          │  行为                                                               ││
│  ├────────────────┼────────────────────────────────────────────────────────────────────┤│
│  │  **Retain**    │  **默认值** (手动创建的 PV)                                         ││
│  │                │  PVC 删除后，PV 变为 Released 状态                                  ││
│  │                │  数据保留，需要管理员手动处理才能重新绑定                           ││
│  │                │  **最安全，推荐用于重要数据**                                       ││
│  ├────────────────┼────────────────────────────────────────────────────────────────────┤│
│  │  **Delete**    │  **默认值** (动态创建的 PV)                                         ││
│  │                │  PVC 删除后，PV 和底层存储一起被删除                                ││
│  │                │  **数据会丢失！**                                                   ││
│  ├────────────────┼────────────────────────────────────────────────────────────────────┤│
│  │  **Recycle**   │  **已废弃**                                                         ││
│  │                │  执行 rm -rf /thevolume/* 清理数据后重新可用                        ││
│  │                │  不推荐使用，推荐用动态 Provisioning 替代                           ││
│  └────────────────┴────────────────────────────────────────────────────────────────────┘│
│                                                                                         │
│  **如何设置 PV 回收策略**:                                                               │
│  ```yaml                                                                                │
│  apiVersion: v1                                                                         │
│  kind: PersistentVolume                                                                 │
│  metadata:                                                                              │
│    name: my-pv                                                                          │
│  spec:                                                                                  │
│    capacity:                                                                            │
│      storage: 10Gi                                                                      │
│    persistentVolumeReclaimPolicy: Retain  # 设置保留策略                                │
│    ...                                                                                  │
│  ```                                                                                    │
│                                                                                         │
│  **StorageClass 中设置默认回收策略**:                                                    │
│  ```yaml                                                                                │
│  apiVersion: storage.k8s.io/v1                                                          │
│  kind: StorageClass                                                                     │
│  metadata:                                                                              │
│    name: my-storage-class                                                               │
│  provisioner: kubernetes.io/aws-ebs                                                     │
│  reclaimPolicy: Retain  # 动态创建的 PV 也使用 Retain 策略                              │
│  ```                                                                                    │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

#### 1.3 避免 PVC 被意外删除 - Finalizer 保护

```yaml
# PVC 默认带有 Finalizer 保护
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-pvc
  finalizers:
  - kubernetes.io/pvc-protection  # 默认添加，防止 PVC 在被 Pod 使用时被删除
spec:
  ...
```

**工作原理**:
- PVC 被 Pod 使用时，即使执行 `kubectl delete pvc`，PVC 也不会立即删除
- PVC 进入 "Terminating" 状态，但保留直到所有使用它的 Pod 被删除
- Pod 删除后，PVC Controller 自动移除 Finalizer，PVC 才真正删除

### 2. ConfigMap 复用

#### 2.1 独立创建 ConfigMap

```yaml
# ConfigMap 独立于 Pod 创建，天然支持复用
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
data:
  config.yaml: |
    key1: value1
    key2: value2
---
# 多个 Pod 可以共享同一个 ConfigMap
apiVersion: v1
kind: Pod
metadata:
  name: pod-1
spec:
  containers:
  - name: app
    image: nginx
    volumeMounts:
    - name: config
      mountPath: /etc/config
  volumes:
  - name: config
    configMap:
      name: app-config  # 引用 ConfigMap
```

#### 2.2 避免 ConfigMap 随 Pod 删除

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **ConfigMap 最佳实践**                                          │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. 独立管理 ConfigMap**                                                               │
│  - 不要在 Pod spec 中使用 configMapGenerator (Kustomize)                                │
│  - 单独维护 ConfigMap 的 YAML 文件                                                       │
│  - ConfigMap 和 Pod 分开部署                                                            │
│                                                                                         │
│  **2. 使用 Deployment/StatefulSet 而不是裸 Pod**                                         │
│  - 控制器管理的 Pod 删除时不会删除关联的 ConfigMap                                       │
│  - Pod 重建后自动使用相同的 ConfigMap                                                    │
│                                                                                         │
│  **3. 注意 ownerReferences**                                                             │
│  - 如果 ConfigMap 的 ownerReferences 指向某个对象                                        │
│  - 该对象删除时（使用级联删除），ConfigMap 也会被删除                                    │
│  - **解决**: 不要设置 ownerReferences，或使用 orphan 级联策略                            │
│                                                                                         │
│  **4. 使用 immutable ConfigMap (Kubernetes 1.21+)**                                      │
│  ```yaml                                                                                │
│  apiVersion: v1                                                                         │
│  kind: ConfigMap                                                                        │
│  metadata:                                                                              │
│    name: app-config-v1                                                                  │
│  immutable: true  # 防止意外修改                                                        │
│  data:                                                                                  │
│    ...                                                                                  │
│  ```                                                                                    │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 3. Secret 复用

#### 3.1 独立创建 Secret

```yaml
# Secret 独立于 Pod 创建
apiVersion: v1
kind: Secret
metadata:
  name: app-secret
type: Opaque
data:
  password: cGFzc3dvcmQ=  # base64 编码
---
# Pod 引用 Secret
apiVersion: v1
kind: Pod
metadata:
  name: pod-1
spec:
  containers:
  - name: app
    image: nginx
    env:
    - name: DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: app-secret
          key: password
```

#### 3.2 Secret 的特殊注意事项

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **Secret 管理最佳实践**                                          │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. 与 ConfigMap 相同的复用原则**                                                       │
│  - 独立创建和管理 Secret                                                                │
│  - 不设置 ownerReferences                                                               │
│  - 可以被多个 Pod 共享                                                                  │
│                                                                                         │
│  **2. ServiceAccount Token Secret**                                                      │
│  - 类型: kubernetes.io/service-account-token                                            │
│  - **注意**: 这类 Secret 有 ownerReferences 指向 ServiceAccount                         │
│  - ServiceAccount 删除时，关联的 Token Secret 也会被删除                                │
│                                                                                         │
│  **3. immutable Secret (Kubernetes 1.21+)**                                              │
│  ```yaml                                                                                │
│  apiVersion: v1                                                                         │
│  kind: Secret                                                                           │
│  metadata:                                                                              │
│    name: app-secret                                                                     │
│  immutable: true  # 防止意外修改，提高安全性                                            │
│  data:                                                                                  │
│    ...                                                                                  │
│  ```                                                                                    │
│                                                                                         │
│  **4. 外部 Secret 管理**                                                                 │
│  - 使用 External Secrets Operator                                                       │
│  - 从 HashiCorp Vault、AWS Secrets Manager 等同步                                       │
│  - 即使 Secret 被删除也能自动重建                                                       │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 4. 级联删除与资源保留

#### 4.1 使用 --cascade=orphan 保留子资源

```bash
# 删除 Deployment 但保留 Pod（不推荐，仅用于特殊场景）
kubectl delete deployment my-deploy --cascade=orphan

# Pod 会变成"孤儿"，不再有 ownerReferences
# 需要手动管理这些 Pod
```

#### 4.2 ownerReferences 与资源保留

```yaml
# 如果不想让资源随 owner 一起删除，不要设置 ownerReferences
# 或者设置 blockOwnerDeletion: false
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  ownerReferences:
  - apiVersion: apps/v1
    kind: Deployment
    name: my-deploy
    uid: xxx
    blockOwnerDeletion: false  # 不阻止 owner 删除
    # 注意：即使设置为 false，background 级联删除仍会删除此资源
```

### 5. 完整示例：可复用资源的部署

```yaml
# 1. 首先创建需要复用的资源
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: shared-pvc
  # 不设置 ownerReferences，确保独立生命周期
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
  storageClassName: standard  # 确保 StorageClass 的 reclaimPolicy 是 Retain
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: shared-config
  # 不设置 ownerReferences
data:
  app.conf: |
    setting1=value1
---
apiVersion: v1
kind: Secret
metadata:
  name: shared-secret
  # 不设置 ownerReferences
type: Opaque
data:
  api-key: YXBpLWtleS12YWx1ZQ==
---
# 2. Pod/Deployment 使用这些资源
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
      - name: app
        image: nginx
        volumeMounts:
        - name: data
          mountPath: /data
        - name: config
          mountPath: /etc/config
        env:
        - name: API_KEY
          valueFrom:
            secretKeyRef:
              name: shared-secret
              key: api-key
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: shared-pvc
      - name: config
        configMap:
          name: shared-config

# 删除 Deployment 后，PVC、ConfigMap、Secret 都会保留
# 新的 Deployment 可以继续使用这些资源
```

### 6. 关键总结

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **资源复用关键点总结**                                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. PVC**                                                                              │
│  ├── 默认独立于 Pod，天然支持复用                                                        │
│  ├── 设置 StorageClass.reclaimPolicy: Retain 确保 PV 数据保留                           │
│  └── PVC Protection Finalizer 防止使用中被删除                                          │
│                                                                                         │
│  **2. ConfigMap / Secret**                                                               │
│  ├── 默认独立于 Pod，天然支持复用                                                        │
│  ├── 不要设置 ownerReferences（除非你理解级联删除的影响）                               │
│  └── 考虑使用 immutable: true 防止意外修改                                              │
│                                                                                         │
│  **3. 要避免的做法**                                                                     │
│  ├── 不要在资源上设置指向 Pod/Deployment 的 ownerReferences                             │
│  ├── 不要使用 ephemeral volume 存储重要数据                                             │
│  └── 注意 Helm 的 --cascade 行为                                                         │
│                                                                                         │
│  **4. 推荐做法**                                                                         │
│  ├── 资源分层管理：基础资源（PVC、ConfigMap、Secret）与工作负载分开部署                 │
│  ├── 使用 GitOps 独立管理持久资源                                                       │
│  └── 定期备份重要的 ConfigMap 和 Secret                                                 │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 9. kubectl get pods 状态计算规则

### 9.1 状态显示字段说明

当执行 `kubectl get pods` 时，显示的 STATUS 列是根据多个 Pod 状态字段计算得出的：

```
NAME              READY   STATUS      RESTARTS   AGE
nginx-pod         1/1     Running     0          10m
finished-pod      0/1     Completed   0          5m
error-pod         0/1     Error       1          2m
deleting-pod      1/1     Terminating 0          1m
```

### 9.2 状态计算核心源码

**源码位置**: `pkg/printers/internalversion/printers.go:804-937`

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                      **printPod() 状态计算逻辑流程图**                                    │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  输入: pod *api.Pod                                                                     │
│  输出: reason (显示在 STATUS 列的字符串)                                                │
│                                                                                         │
│  1. **初始值**: reason = pod.Status.Phase                                              │
│     │   可能的值: Pending, Running, Succeeded, Failed, Unknown                         │
│     │                                                                                   │
│     └── if pod.Status.Reason != "":                                                    │
│             reason = pod.Status.Reason                                                 │
│                                                                                         │
│  2. **检查 SchedulingGated 条件** - 行817-822                                          │
│     │                                                                                   │
│     └── for condition := range pod.Status.Conditions:                                  │
│             if condition.Type == PodScheduled && condition.Reason == SchedulingGated:  │
│                 reason = "SchedulingGated"                                             │
│                                                                                         │
│  3. **处理 Init 容器状态** - 行843-891                                                  │
│     │                                                                                   │
│     └── for i, container := range pod.Status.InitContainerStatuses:                    │
│             │                                                                           │
│             ├── [Terminated 且 ExitCode=0] → 继续下一个                                │
│             │                                                                           │
│             ├── [Terminated 且 ExitCode!=0]                                            │
│             │       if Reason == "": reason = "Init:Signal:X" 或 "Init:ExitCode:X"    │
│             │       else: reason = "Init:" + Reason                                    │
│             │       initializing = true; break                                         │
│             │                                                                           │
│             ├── [Waiting 且 Reason 有值]                                               │
│             │       reason = "Init:" + Reason                                          │
│             │       initializing = true; break                                         │
│             │                                                                           │
│             └── [其他情况]                                                              │
│                     reason = "Init:i/n" (如 "Init:0/2")                                │
│                     initializing = true; break                                         │
│                                                                                         │
│  4. **处理主容器状态** - 行893-931 (如果 Init 完成)                                    │
│     │                                                                                   │
│     └── for container := range pod.Status.ContainerStatuses (从后向前):                │
│             │                                                                           │
│             ├── [Waiting 且 Reason 有值]                                               │
│             │       reason = Reason (如 "CrashLoopBackOff", "ImagePullBackOff")       │
│             │                                                                           │
│             ├── [Terminated 且 Reason 有值]                                            │
│             │       reason = Reason (如 "Error", "OOMKilled", "Completed")            │
│             │                                                                           │
│             ├── [Terminated 且 Reason 为空]                                            │
│             │       reason = "Signal:X" 或 "ExitCode:X"                               │
│             │                                                                           │
│             └── [Ready 且 Running]                                                      │
│                     hasRunning = true                                                   │
│                                                                                         │
│  5. **特殊情况处理** - 行923-937                                                        │
│     │                                                                                   │
│     ├── if reason == "Completed" && hasRunning:                                        │
│     │       if hasPodReadyCondition: reason = "Running"                               │
│     │       else: reason = "NotReady"                                                 │
│     │                                                                                   │
│     ├── if DeletionTimestamp != nil && Reason == "NodeUnreachable":                   │
│     │       reason = "Unknown"                                                         │
│     │                                                                                   │
│     └── **if DeletionTimestamp != nil: reason = "Terminating"** ← 关键！             │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 9.3 pod.Status.Phase vs 显示的 STATUS

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                      **pod.Status.Phase 与显示 STATUS 的关系**                           │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  pod.Status.Phase 是 API 对象的字段，只有 5 个可能值：                                 │
│  ┌──────────────┬─────────────────────────────────────────────────────────────────────┐│
│  │  Phase        │  含义                                                              ││
│  ├──────────────┼─────────────────────────────────────────────────────────────────────┤│
│  │  Pending     │  Pod 已被 API Server 接受，但容器尚未创建                          ││
│  ├──────────────┼─────────────────────────────────────────────────────────────────────┤│
│  │  Running     │  Pod 已绑定到节点，至少一个容器正在运行                            ││
│  ├──────────────┼─────────────────────────────────────────────────────────────────────┤│
│  │  Succeeded   │  所有容器成功终止，不会重启                                        ││
│  ├──────────────┼─────────────────────────────────────────────────────────────────────┤│
│  │  Failed      │  所有容器终止，至少一个失败                                        ││
│  ├──────────────┼─────────────────────────────────────────────────────────────────────┤│
│  │  Unknown     │  无法获取 Pod 状态（通常是与节点通信问题）                         ││
│  └──────────────┴─────────────────────────────────────────────────────────────────────┘│
│                                                                                         │
│  **但 kubectl get pods 显示的 STATUS 可以有更多值！**                                  │
│                                                                                         │
│  这是因为 printPod() 函数会根据：                                                      │
│  - pod.Status.Reason                                                                   │
│  - pod.Status.Conditions                                                               │
│  - pod.Status.ContainerStatuses                                                        │
│  - pod.Status.InitContainerStatuses                                                    │
│  - pod.DeletionTimestamp                                                               │
│                                                                                         │
│  来计算最终显示的状态字符串                                                            │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 9.4 常见 STATUS 值及其含义

| STATUS | 来源 | 含义 |
|:-------|:-----|:-----|
| **Pending** | Phase | Pod 等待调度或镜像拉取 |
| **Running** | Phase/计算 | 至少一个容器运行中 |
| **Succeeded** | Phase | 所有容器成功完成 |
| **Failed** | Phase | 至少一个容器失败 |
| **Unknown** | Phase/计算 | 节点不可达或删除中的 Pod |
| **Terminating** | 计算 | `DeletionTimestamp != nil` |
| **ContainerCreating** | ContainerStatus.Waiting.Reason | 容器正在创建 |
| **CrashLoopBackOff** | ContainerStatus.Waiting.Reason | 容器反复崩溃 |
| **ImagePullBackOff** | ContainerStatus.Waiting.Reason | 镜像拉取失败重试中 |
| **ErrImagePull** | ContainerStatus.Waiting.Reason | 镜像拉取出错 |
| **Error** | ContainerStatus.Terminated.Reason | 容器以错误退出 |
| **Completed** | ContainerStatus.Terminated.Reason | 容器正常完成 |
| **OOMKilled** | ContainerStatus.Terminated.Reason | 内存超限被杀 |
| **Init:0/2** | 计算 | Init 容器进度 |
| **Init:Error** | 计算 | Init 容器失败 |
| **PodInitializing** | ContainerStatus.Waiting.Reason | 等待 Init 容器 |
| **SchedulingGated** | Condition.Reason | 调度门控阻止 |
| **NotReady** | 计算 | 运行但未就绪 |

### 9.5 pod.Status.Conditions 详解

Pod Conditions 是更细粒度的状态指示器：

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                           **Pod Conditions 类型**                                        │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  ┌─────────────────────┬────────────────────────────────────────────────────────────┐  │
│  │  Condition Type     │  含义                                                      │  │
│  ├─────────────────────┼────────────────────────────────────────────────────────────┤  │
│  │  **PodScheduled**   │  Pod 是否已被调度到节点                                    │  │
│  │                     │  - True: 已调度                                            │  │
│  │                     │  - False + SchedulingGated: 被门控阻止调度                 │  │
│  │                     │  - False + Unschedulable: 无法调度                         │  │
│  ├─────────────────────┼────────────────────────────────────────────────────────────┤  │
│  │  **Initialized**    │  所有 Init 容器是否成功完成                               │  │
│  │                     │  - True: Init 完成                                         │  │
│  │                     │  - False: Init 进行中或失败                               │  │
│  ├─────────────────────┼────────────────────────────────────────────────────────────┤  │
│  │  **ContainersReady**│  所有容器是否就绪                                          │  │
│  │                     │  - True: 所有容器就绪                                      │  │
│  │                     │  - False: 至少一个容器未就绪                              │  │
│  ├─────────────────────┼────────────────────────────────────────────────────────────┤  │
│  │  **Ready**          │  Pod 是否可以接收流量                                      │  │
│  │                     │  - True: Pod 就绪                                          │  │
│  │                     │  - False: Pod 未就绪                                       │  │
│  │                     │  - 这是 Service 选择 Pod 的依据                            │  │
│  ├─────────────────────┼────────────────────────────────────────────────────────────┤  │
│  │  **DisruptionTarget**│ (v1.26+) Pod 是否是中断目标                              │  │
│  │                     │  - True: Pod 将被中断（如 Eviction、Preemption）          │  │
│  └─────────────────────┴────────────────────────────────────────────────────────────┘  │
│                                                                                         │
│  **源码定义**: staging/src/k8s.io/api/core/v1/types.go                                 │
│                                                                                         │
│  type PodConditionType string                                                           │
│  const (                                                                               │
│      PodScheduled       PodConditionType = "PodScheduled"                              │
│      PodReady           PodConditionType = "Ready"                                     │
│      PodInitialized     PodConditionType = "Initialized"                               │
│      ContainersReady    PodConditionType = "ContainersReady"                           │
│      DisruptionTarget   PodConditionType = "DisruptionTarget"                          │
│  )                                                                                     │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 9.6 Condition 结构体

```go
// staging/src/k8s.io/api/core/v1/types.go
type PodCondition struct {
    Type               PodConditionType   // 条件类型
    Status             ConditionStatus    // True, False, Unknown
    LastProbeTime      metav1.Time        // 最后探测时间
    LastTransitionTime metav1.Time        // 最后状态变化时间
    Reason             string             // 状态原因（机器可读）
    Message            string             // 状态消息（人类可读）
}
```

### 9.7 Condition 与 STATUS 显示的关联

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                    **Condition 如何影响 kubectl get pods 输出**                          │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  **1. Ready Condition → READY 列**                                                      │
│  │                                                                                      │
│  │  kubectl get pods 的 READY 列显示 "就绪容器数/总容器数"                             │
│  │  计算方式：遍历 ContainerStatuses，统计 Ready=true 的容器                          │
│  │                                                                                      │
│  │  示例: 1/2 表示 2 个容器中有 1 个就绪                                               │
│  │                                                                                      │
│  │  源码: printers.go:868 (readyContainers++)                                          │
│  │                                                                                      │
│  └── Ready Condition 值:                                                               │
│      - True: 所有 ContainersReady + readinessGates 通过                               │
│      - False: 至少一个容器未就绪或 readinessGates 未通过                              │
│                                                                                         │
│  **2. PodScheduled Condition → STATUS 列**                                              │
│  │                                                                                      │
│  │  源码: printers.go:817-822                                                          │
│  │  if condition.Type == PodScheduled && condition.Reason == SchedulingGated:          │
│  │      reason = "SchedulingGated"                                                     │
│  │                                                                                      │
│  └── 当 PodScheduled 的 Reason 是 SchedulingGated 时，STATUS 显示 "SchedulingGated"   │
│                                                                                         │
│  **3. Initialized Condition → 内部计算**                                                │
│  │                                                                                      │
│  │  源码: printers.go:893 (isPodInitializedConditionTrue)                              │
│  │  用于判断是否需要处理 Init 容器状态                                                 │
│  │                                                                                      │
│  └── 如果 Initialized=False，STATUS 可能显示 "Init:X/Y" 或 "Init:Error"               │
│                                                                                         │
│  **4. kubectl describe pod 显示所有 Conditions**                                       │
│  │                                                                                      │
│  │  Conditions:                                                                        │
│  │    Type              Status                                                         │
│  │    Initialized       True                                                           │
│  │    Ready             False                                                          │
│  │    ContainersReady   False                                                          │
│  │    PodScheduled      True                                                           │
│  │                                                                                      │
│  └── describe 命令会显示所有 Conditions 的详细信息                                     │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 9.8 Terminating 状态的特殊处理

```go
// pkg/printers/internalversion/printers.go:933-937
// 关键代码：Terminating 状态的判断

if pod.DeletionTimestamp != nil && pod.Status.Reason == node.NodeUnreachablePodReason {
    reason = "Unknown"
} else if pod.DeletionTimestamp != nil {
    reason = "Terminating"  // ← 这就是为什么删除中的 Pod 显示 Terminating
}
```

**重要说明**:
- `Terminating` **不是** `pod.Status.Phase` 的合法值
- 它是 `kubectl` 客户端在显示时根据 `DeletionTimestamp` 计算出来的
- API Server 中的 Pod 对象的 Phase 仍然是原来的值（如 Running）
- 只有当 `DeletionTimestamp != nil` 时，kubectl 才会显示 `Terminating`

### 9.9 状态计算优先级

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                          **STATUS 计算优先级（从高到低）**                               │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                         │
│  1. [最高优先级] DeletionTimestamp 存在                                                 │
│     └── "Terminating" 或 "Unknown" (如果节点不可达)                                    │
│                                                                                         │
│  2. [次高] Init 容器未完成                                                              │
│     └── "Init:X/Y", "Init:Error", "Init:CrashLoopBackOff" 等                           │
│                                                                                         │
│  3. [中等] 主容器状态                                                                   │
│     ├── Waiting 状态的 Reason (如 CrashLoopBackOff, ImagePullBackOff)                  │
│     └── Terminated 状态的 Reason (如 Error, OOMKilled, Completed)                      │
│                                                                                         │
│  4. [较低] SchedulingGated 条件                                                         │
│     └── PodScheduled Condition 的 Reason 为 SchedulingGated                            │
│                                                                                         │
│  5. [最低] Pod Phase                                                                    │
│     └── 默认使用 pod.Status.Phase 或 pod.Status.Reason                                 │
│                                                                                         │
│  **源码逻辑**:                                                                          │
│  - 状态计算是从后向前扫描容器 (行897: for i := len(pod.Status.ContainerStatuses) - 1)  │
│  - 后面的状态会覆盖前面的（因此最后一个有问题的容器的状态会显示）                       │
│  - 但 DeletionTimestamp 的检查在最后，所以它总是能覆盖其他状态                          │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 9.10 实用命令

```bash
# 查看完整的 Pod 状态信息
kubectl get pod <name> -o yaml | grep -A 50 "status:"

# 只看 Phase
kubectl get pod <name> -o jsonpath='{.status.phase}'

# 只看 Conditions
kubectl get pod <name> -o jsonpath='{.status.conditions}' | jq

# 查看容器状态
kubectl get pod <name> -o jsonpath='{.status.containerStatuses}' | jq

# 查看 DeletionTimestamp（判断是否在删除中）
kubectl get pod <name> -o jsonpath='{.metadata.deletionTimestamp}'

# 查看 Finalizers
kubectl get pod <name> -o jsonpath='{.metadata.finalizers}'

# 详细状态描述
kubectl describe pod <name>
```

---

## 总结

### Pod 删除的关键检查点

| 检查点 | 可能卡住的原因 | 解决方案 |
|:------|:--------------|:---------|
| **API Server** | 连接问题、认证失败 | 检查网络、认证配置 |
| **Finalizers** | Controller 未移除 | 手动 patch 移除 |
| **PreStop Hook** | 执行时间过长 | 优化 Hook 或增加 gracePeriod |
| **容器停止** | D状态进程、信号处理问题 | 等待或强制删除 |
| **Volume 卸载** | 网络存储问题、进程占用 | 检查存储、强制 unmount |
| **Container Runtime** | 运行时卡死 | 重启运行时 |
| **磁盘空间** | 日志清理失败 | 清理磁盘空间 |

### 推荐的故障排查步骤

1. **检查 Pod 状态**: `kubectl describe pod <name>`
2. **检查 Events**: 查看是否有错误事件
3. **检查 Finalizers**: `kubectl get pod <name> -o jsonpath='{.metadata.finalizers}'`
4. **检查 kubelet 日志**: `journalctl -u kubelet`
5. **检查容器运行时**: `crictl ps`, `crictl logs`
6. **检查节点状态**: `kubectl describe node <node>`
7. **检查存储**: `df -h`, `mount`, `lsof`
8. **检查进程状态**: `ps aux | grep D` (查找 D状态进程)

---

*本文档基于 Kubernetes 源码分析，涵盖了 Pod 删除流程的完整调用链和可能遇到的问题。*

