# Kubernetes Secret 架构与原理深度解读

## 目录

1. [概述](#概述)
2. [Secret 核心概念](#secret-核心概念)
3. [Secret 整体架构图](#secret-整体架构图)
4. [Secret 数据结构与源码分析](#secret-数据结构与源码分析)
5. [Secret 管理器架构](#secret-管理器架构)
6. [Secret 环境变量注入机制](#secret-环境变量注入机制)
7. [Secret 卷挂载机制](#secret-卷挂载机制)
8. [Secret 验证与策略](#secret-验证与策略)
9. [Secret 类型系统](#secret-类型系统)
10. [Secret 时序交互分析](#secret-时序交互分析)
11. [Secret 使用样例](#secret-使用样例)
12. [Secret 最佳实践](#secret-最佳实践)
13. [总结](#总结)

---

## 概述

Secret 是 Kubernetes 中用于存储和管理敏感信息（如密码、OAuth 令牌、SSH 密钥、TLS 证书等）的 API 对象。它提供了一种安全的方式来处理敏感数据，避免将机密信息硬编码到容器镜像或 Pod 规范中。本文档基于 Kubernetes 源码深入解读 Secret 的架构设计、工作原理和实现机制。

### 核心特性

- **数据加密**：支持 base64 编码和 etcd 静态加密
- **类型系统**：提供多种预定义的 Secret 类型
- **灵活使用**：可作为环境变量、命令行参数或卷文件使用
- **权限控制**：基于 RBAC 的细粒度访问控制
- **不可变模式**：支持只读 Secret，防止意外修改
- **自动挂载**：ServiceAccount Token 的自动注入机制

---

## Secret 核心概念

### 1. 基本概念关系

- **Secret**：存储敏感数据的 Kubernetes 对象
- **Data**：包含 base64 编码的敏感数据字段
- **StringData**：便于输入的纯文本数据字段（仅写入时使用）
- **Type**：Secret 类型，定义数据的用途和格式
- **Immutable**：不可变标志，确保数据不被修改

### 2. 核心数据结构

基于源码 `staging/src/k8s.io/api/core/v1/types.go`：

```go
// Secret holds secret data of a certain type. The total bytes of the values in
// the Data field must be less than MaxSecretSize bytes.
type Secret struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    // Immutable, if set to true, ensures that data stored in the Secret cannot
    // be updated (only object metadata can be modified).
    Immutable *bool `json:"immutable,omitempty"`
    
    // Data contains the secret data. Each key must consist of alphanumeric
    // characters, '-', '_' or '.'. The serialized form of the secret data is a
    // base64 encoded string, representing the arbitrary (possibly non-string)
    // data value here.
    Data map[string][]byte `json:"data,omitempty"`
    
    // stringData allows specifying non-binary secret data in string form.
    // It is provided as a write-only input field for convenience.
    // All keys and values are merged into the data field on write, overwriting any existing values.
    // The stringData field is never output when reading from the API.
    StringData map[string]string `json:"stringData,omitempty"`
    
    // Used to facilitate programmatic handling of secret data.
    Type SecretType `json:"type,omitempty"`
}

const MaxSecretSize = 1 * 1024 * 1024  // 1MB
```

---

## Secret 整体架构图

```mermaid
graph TB
    subgraph API_Layer["API 层"]
        API["API Server<br/>• REST API 接口<br/>• 验证和准入控制<br/>• 存储到 etcd"]
        VALIDATION["验证策略<br/>• 键名格式验证<br/>• 数据大小限制<br/>• 类型特定验证"]
        STRATEGY["Secret 策略<br/>• 创建/更新逻辑<br/>• 不可变性控制<br/>• 字段预处理"]
    end
    
    subgraph Storage_Layer["存储层"]
        ETCD["etcd 存储<br/>• 持久化存储<br/>• 静态加密支持<br/>• 分布式一致性"]
        ENCRYPTION["加密存储<br/>• 静态加密配置<br/>• 密钥轮换<br/>• 加密提供者"]
    end
    
    subgraph Node_Layer["节点层 - Kubelet"]
        SECRET_MGR["Secret Manager<br/>• 三种管理器实现<br/>• 缓存和监听机制"]
        VOLUME_PLUGIN["卷插件<br/>• secret 卷插件<br/>• 内存文件系统<br/>• 原子更新机制"]
        ENV_INJECTION["环境变量注入<br/>• envFrom.secretRef<br/>• env.valueFrom<br/>• base64 解码处理"]
    end
    
    subgraph ServiceAccount_Layer["ServiceAccount 层"]
        SA_CONTROLLER["ServiceAccount Controller<br/>• Token 自动创建<br/>• Secret 生命周期管理<br/>• 自动挂载逻辑"]
        TOKEN_PROJECTOR["Token Projector<br/>• 投影卷支持<br/>• Token 刷新<br/>• 过期时间管理"]
    end
    
    subgraph App_Layer["应用层"]
        POD["Pod 使用方式<br/>• 环境变量<br/>• 卷挂载<br/>• 投影卷"]
        APP["应用程序<br/>• 读取敏感数据<br/>• TLS 证书加载<br/>• 认证信息获取"]
    end
    
    API --> ETCD
    API --> VALIDATION
    VALIDATION --> STRATEGY
    STRATEGY --> ENCRYPTION
    ETCD --> SECRET_MGR
    SECRET_MGR --> VOLUME_PLUGIN
    SECRET_MGR --> ENV_INJECTION
    SA_CONTROLLER --> SECRET_MGR
    TOKEN_PROJECTOR --> VOLUME_PLUGIN
    VOLUME_PLUGIN --> POD
    ENV_INJECTION --> POD
    POD --> APP
    
    classDef apiStyle fill:#e6f3ff,stroke:#0066cc,stroke-width:2px
    classDef storageStyle fill:#fff2e6,stroke:#cc6600,stroke-width:2px
    classDef nodeStyle fill:#e6ffe6,stroke:#009900,stroke-width:2px
    classDef saStyle fill:#ffe6f2,stroke:#cc0066,stroke-width:2px
    classDef appStyle fill:#f0f0f0,stroke:#666666,stroke-width:2px
    
    class API,VALIDATION,STRATEGY apiStyle
    class ETCD,ENCRYPTION storageStyle
    class SECRET_MGR,VOLUME_PLUGIN,ENV_INJECTION nodeStyle
    class SA_CONTROLLER,TOKEN_PROJECTOR saStyle
    class POD,APP appStyle
```

---

## Secret 数据结构与源码分析

### 1. Secret 类型定义

基于 `pkg/apis/core/types.go`：

```go
// SecretType defines the types of secrets
type SecretType string

// These are the valid values for SecretType
const (
    // SecretTypeOpaque is the default; arbitrary user-defined data
    SecretTypeOpaque SecretType = "Opaque"

    // SecretTypeServiceAccountToken contains a token that identifies a service account to the API
    SecretTypeServiceAccountToken SecretType = "kubernetes.io/service-account-token"

    // SecretTypeDockercfg contains a dockercfg file that follows the same format rules as ~/.dockercfg
    SecretTypeDockercfg SecretType = "kubernetes.io/dockercfg"

    // SecretTypeDockerConfigJSON contains a dockercfg file that follows the same format rules as ~/.docker/config.json
    SecretTypeDockerConfigJSON SecretType = "kubernetes.io/dockerconfigjson"

    // SecretTypeBasicAuth contains data needed for basic authentication.
    SecretTypeBasicAuth SecretType = "kubernetes.io/basic-auth"

    // SecretTypeSSHAuth contains data needed for SSH authentication.
    SecretTypeSSHAuth SecretType = "kubernetes.io/ssh-auth"

    // SecretTypeTLS contains information about a TLS client or server secret.
    SecretTypeTLS SecretType = "kubernetes.io/tls"

    // SecretTypeBootstrapToken is used during the automated bootstrap process
    SecretTypeBootstrapToken SecretType = "bootstrap.kubernetes.io/token"
)
```

### 2. Secret 策略实现

基于 `pkg/registry/core/secret/strategy.go`：

```go
// strategy implements behavior for Secret objects
type strategy struct {
    runtime.ObjectTyper
    names.NameGenerator
}

// Strategy is the default logic that applies when creating and updating Secret
// objects via the REST API.
var Strategy = strategy{legacyscheme.Scheme, names.SimpleNameGenerator}

func (strategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
    secret := obj.(*api.Secret)
    dropDisabledFields(secret, nil)
}

func (strategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
    return validation.ValidateSecret(obj.(*api.Secret))
}

func (strategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
    newSecret := obj.(*api.Secret)
    oldSecret := old.(*api.Secret)
    
    // Preserve the type if it's not specified in the update
    if len(newSecret.Type) == 0 {
        newSecret.Type = oldSecret.Type
    }
    
    dropDisabledFields(newSecret, oldSecret)
}

func (strategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
    return validation.ValidateSecretUpdate(obj.(*api.Secret), old.(*api.Secret))
}
```

---

## Secret 管理器架构

### 1. Secret 管理器接口

基于 `pkg/kubelet/secret/secret_manager.go`：

```go
// Manager manages Kubernetes secrets. This includes retrieving
// secrets or registering/unregistering them via Pods.
type Manager interface {
    // Get secret by secret namespace and name.
    GetSecret(namespace, name string) (*v1.Secret, error)
    
    // WARNING: Register/UnregisterPod functions should be efficient,
    // i.e. should not block on network operations.
    
    // RegisterPod registers all secrets from a given pod.
    RegisterPod(pod *v1.Pod)
    
    // UnregisterPod unregisters secrets from a given pod that are not
    // used by any other registered pod.
    UnregisterPod(pod *v1.Pod)
}
```

### 2. 三种管理器实现

#### 2.1 Simple Secret Manager（直接调用）

```go
// simpleSecretManager implements SecretManager interfaces with
// simple operations to apiserver.
type simpleSecretManager struct {
    kubeClient clientset.Interface
}

func NewSimpleSecretManager(kubeClient clientset.Interface) Manager {
    return &simpleSecretManager{kubeClient: kubeClient}
}

func (s *simpleSecretManager) GetSecret(namespace, name string) (*v1.Secret, error) {
    return s.kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}
```

#### 2.2 Caching Secret Manager（基于缓存）

```go
// NewCachingSecretManager creates a manager that keeps a cache of all secrets
// necessary for registered pods.
func NewCachingSecretManager(kubeClient clientset.Interface, getTTL manager.GetObjectTTLFunc) Manager {
    getSecret := func(namespace, name string, opts metav1.GetOptions) (runtime.Object, error) {
        return kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), name, opts)
    }
    secretStore := manager.NewObjectStore(getSecret, clock.RealClock{}, getTTL, defaultTTL)
    return &secretManager{
        manager: manager.NewCacheBasedManager(secretStore, getSecretNames),
    }
}
```

#### 2.3 Watching Secret Manager（基于监听）

```go
// NewWatchingSecretManager creates a manager that keeps a cache of all secrets
// necessary for registered pods.
func NewWatchingSecretManager(kubeClient clientset.Interface, resyncInterval time.Duration) Manager {
    listSecret := func(namespace string, opts metav1.ListOptions) (runtime.Object, error) {
        return kubeClient.CoreV1().Secrets(namespace).List(context.TODO(), opts)
    }
    watchSecret := func(namespace string, opts metav1.ListOptions) (watch.Interface, error) {
        return kubeClient.CoreV1().Secrets(namespace).Watch(context.TODO(), opts)
    }
    
    // 检查不可变性
    isImmutable := func(object runtime.Object) bool {
        if secret, ok := object.(*v1.Secret); ok {
            return secret.Immutable != nil && *secret.Immutable
        }
        return false
    }
    
    return &secretManager{
        manager: manager.NewWatchBasedManager(listSecret, watchSecret, newSecret, isImmutable, gr, resyncInterval, getSecretNames),
    }
}
```

### 3. 管理器选择策略

基于 `pkg/kubelet/kubelet.go`：

```go
switch kubeCfg.ConfigMapAndSecretChangeDetectionStrategy {
case kubeletconfiginternal.WatchChangeDetectionStrategy:
    secretManager = secret.NewWatchingSecretManager(klet.kubeClient, klet.resyncInterval)
case kubeletconfiginternal.TTLCacheChangeDetectionStrategy:
    secretManager = secret.NewCachingSecretManager(
        klet.kubeClient, manager.GetObjectTTLFromNodeFunc(klet.GetNode))
case kubeletconfiginternal.GetChangeDetectionStrategy:
    secretManager = secret.NewSimpleSecretManager(klet.kubeClient)
}
```

### 4. 管理器架构对比

```mermaid
graph TB
    subgraph Simple_Manager["Simple Manager"]
        SIMPLE["SimpleSecretManager<br/>• 直接 API 调用<br/>• 无缓存机制<br/>• 简单可靠"]
    end
    
    subgraph Caching_Manager["Caching Manager"] 
        CACHING["CachingSecretManager<br/>• TTL 缓存机制<br/>• 按需获取<br/>• 内存优化"]
        OBJECT_STORE["ObjectStore<br/>• 对象缓存<br/>• TTL 管理<br/>• 过期清理"]
    end
    
    subgraph Watching_Manager["Watching Manager"]
        WATCHING["WatchingSecretManager<br/>• 基于 Watch API<br/>• 实时同步<br/>• 事件驱动"]
        WATCH_STORE["WatchBasedManager<br/>• List/Watch 机制<br/>• 本地缓存<br/>• 断线重连"]
    end
    
    subgraph API_Client["API Client"]
        KUBE_CLIENT["Kubernetes Client<br/>• Secret API 调用<br/>• Get/List/Watch 操作<br/>• 网络通信"]
    end
    
    SIMPLE --> KUBE_CLIENT
    CACHING --> OBJECT_STORE
    OBJECT_STORE --> KUBE_CLIENT
    WATCHING --> WATCH_STORE
    WATCH_STORE --> KUBE_CLIENT
    
    classDef simpleStyle fill:#ffe6e6,stroke:#cc0000,stroke-width:2px
    classDef cacheStyle fill:#fff2e6,stroke:#cc6600,stroke-width:2px
    classDef watchStyle fill:#e6ffe6,stroke:#009900,stroke-width:2px
    classDef clientStyle fill:#e6f3ff,stroke:#0066cc,stroke-width:2px
    
    class SIMPLE simpleStyle
    class CACHING,OBJECT_STORE cacheStyle
    class WATCHING,WATCH_STORE watchStyle
    class KUBE_CLIENT clientStyle
```

---

## Secret 环境变量注入机制

### 1. 环境变量注入核心原理

环境变量注入是 Secret 使用的重要方式，通过 Kubelet 在容器启动前将 Secret 数据解码并注入到容器的环境变量中。

#### 1.1 注入时机和流程

基于 `pkg/kubelet/kubelet_pods.go` 中的源码分析：

```go
// Make the environment variables for a pod in the given namespace.
func (kl *Kubelet) makeEnvironmentVariables(pod *v1.Pod, container *v1.Container, podIP string, podIPs []string) ([]kubecontainer.EnvVar, error) {
    var (
        secrets = make(map[string]*v1.Secret)  // Secret 缓存
        tmpEnv  = make(map[string]string)      // 临时环境变量映射
    )
    
    // 步骤1：处理 envFrom 批量导入
    for _, envFrom := range container.EnvFrom {
        switch {
        case envFrom.SecretRef != nil:
            s := envFrom.SecretRef
            name := s.Name
            secret, ok := secrets[name]
            
            // 从 SecretManager 获取 Secret
            if !ok {
                optional := s.Optional != nil && *s.Optional
                secret, err = kl.secretManager.GetSecret(pod.Namespace, name)
                if err != nil {
                    if errors.IsNotFound(err) && optional {
                        continue  // 可选的 Secret 不存在时跳过
                    }
                    return result, err
                }
                secrets[name] = secret
            }
            
            // 批量注入所有键值对
            invalidKeys := []string{}
            for k, v := range secret.Data {
                if len(envFrom.Prefix) > 0 {
                    k = envFrom.Prefix + k  // 添加前缀
                }
                
                // 验证环境变量名有效性
                if errMsgs := utilvalidation.IsEnvVarName(k); len(errMsgs) != 0 {
                    invalidKeys = append(invalidKeys, k)
                    continue
                }
                
                // 关键：将 byte[] 转换为 string
                tmpEnv[k] = string(v)
            }
            
            // 记录无效的环境变量名
            if len(invalidKeys) > 0 {
                kl.recorder.Eventf(pod, v1.EventTypeWarning, 
                    "InvalidEnvironmentVariableNames", 
                    "Keys [%s] from the EnvFrom secret %s/%s were skipped", 
                    strings.Join(invalidKeys, ", "), pod.Namespace, name)
            }
        }
    }
    
    // 步骤2：处理 env 单个变量引用
    for _, envVar := range container.Env {
        if envVar.ValueFrom != nil {
            switch {
            case envVar.ValueFrom.SecretKeyRef != nil:
                s := envVar.ValueFrom.SecretKeyRef
                name := s.Name
                key := s.Key
                optional := s.Optional != nil && *s.Optional
                
                secret, ok := secrets[name]
                if !ok {
                    secret, err = kl.secretManager.GetSecret(pod.Namespace, name)
                    if err != nil {
                        if errors.IsNotFound(err) && optional {
                            continue
                        }
                        return result, err
                    }
                    secrets[name] = secret
                }
                
                runtimeValBytes, ok := secret.Data[key]
                if !ok {
                    if optional {
                        continue
                    }
                    return result, fmt.Errorf("couldn't find key %v in Secret %v/%v", 
                        key, pod.Namespace, name)
                }
                
                // 关键：解码 base64 数据为字符串
                runtimeVal := string(runtimeValBytes)
                tmpEnv[envVar.Name] = runtimeVal
            }
        }
    }
    
    // 步骤3：转换为容器环境变量格式
    for k, v := range tmpEnv {
        result = append(result, kubecontainer.EnvVar{Name: k, Value: v})
    }
    
    return result, nil
}
```

### 2. 数据解码处理

Secret 数据在存储时是 base64 编码的，在注入环境变量时需要解码：

```go
// Secret.Data 中的数据是 []byte 类型（base64 解码后）
// 在环境变量注入时直接转换为 string
tmpEnv[k] = string(secret.Data[key])
```

### 3. 环境变量注入流程图

```mermaid
sequenceDiagram
    participant Pod as Pod Spec
    participant Kubelet as Kubelet
    participant SecretMgr as Secret Manager
    participant API as API Server
    participant Container as Container Runtime
    
    Note over Pod,Container: Secret 环境变量注入流程
    
    Pod->>Kubelet: Pod 启动请求
    Kubelet->>Kubelet: 解析 envFrom 和 env 配置
    
    loop 处理每个 Secret 引用
        Kubelet->>SecretMgr: GetSecret(namespace, name)
        SecretMgr->>API: 获取 Secret 数据
        API->>SecretMgr: 返回 base64 编码的数据
        SecretMgr->>Kubelet: 提供 Secret 数据
    end
    
    Kubelet->>Kubelet: base64 解码数据
    Kubelet->>Kubelet: 验证环境变量名
    Kubelet->>Kubelet: 应用前缀和优先级规则
    Kubelet->>Kubelet: 构建最终环境变量列表
    
    Kubelet->>Container: 创建容器（包含解码后的环境变量）
    Container->>Container: 敏感环境变量注入完成
    
    Note over Pod,Container: 环境变量在容器生命周期内不变
```

---

## Secret 卷挂载机制

### 1. Secret 卷插件实现

基于 `pkg/volume/secret/secret.go`：

```go
// secretPlugin implements the VolumePlugin interface.
type secretPlugin struct {
    host      volume.VolumeHost
    getSecret func(namespace, name string) (*v1.Secret, error)
}

const (
    secretPluginName = "kubernetes.io/secret"
)

func (plugin *secretPlugin) Init(host volume.VolumeHost) error {
    plugin.host = host
    plugin.getSecret = host.GetSecretFunc()
    return nil
}

func (plugin *secretPlugin) NewMounter(spec *volume.Spec, pod *v1.Pod, opts volume.VolumeOptions) (volume.Mounter, error) {
    return &secretVolumeMounter{
        secretVolume: &secretVolume{
            spec.Name(),
            pod.UID,
            plugin,
            plugin.host.GetMounter(plugin.GetPluginName()),
            volume.NewCachedMetrics(volume.NewMetricsDu(getPath(pod.UID, spec.Name(), plugin.host))),
        },
        source:    *spec.Volume.Secret,
        pod:       *pod,
        opts:      &opts,
        getSecret: plugin.getSecret,
    }, nil
}
```

### 2. Secret 卷挂载器实现

```go
// secretVolumeMounter handles retrieving secrets from the API server
// and placing them into the volume on the host.
type secretVolumeMounter struct {
    *secretVolume
    
    source    v1.SecretVolumeSource
    pod       v1.Pod
    opts      *volume.VolumeOptions
    getSecret func(namespace, name string) (*v1.Secret, error)
}

func (b *secretVolumeMounter) SetUpAt(dir string, mounterArgs volume.MounterArgs) error {
    klog.V(3).Infof("Setting up volume %v for pod %v at %v", b.volName, b.pod.UID, dir)
    
    // Wrap EmptyDir, let it do the setup (使用内存文件系统)
    wrapped, err := b.plugin.host.NewWrapperMounter(b.volName, wrappedVolumeSpec(), &b.pod, *b.opts)
    if err != nil {
        return err
    }
    
    optional := b.source.Optional != nil && *b.source.Optional
    secret, err := b.getSecret(b.pod.Namespace, b.source.SecretName)
    if err != nil {
        if !(errors.IsNotFound(err) && optional) {
            return err
        }
        // 创建空的 Secret 对象用于可选情况
        secret = &v1.Secret{
            ObjectMeta: metav1.ObjectMeta{
                Namespace: b.pod.Namespace,
                Name:      b.source.SecretName,
            },
        }
    }
    
    // 构建文件负载
    payload, err := MakePayload(b.source.Items, secret, b.source.DefaultMode, optional)
    if err != nil {
        return err
    }
    
    // 使用原子写入器写入文件
    writer, err := volumeutil.NewAtomicWriter(dir, writerContext)
    if err != nil {
        return err
    }
    
    // 设置权限和所有者
    setPerms := func(_ string) error {
        return volume.SetVolumeOwnership(b, dir, mounterArgs.FsGroup, nil, volumeutil.FSGroupCompleteHook(b.plugin, nil))
    }
    
    err = writer.Write(payload, setPerms)
    if err != nil {
        return err
    }
    
    return nil
}
```

### 3. Secret 数据处理

```go
// MakePayload function is exported so that it can be called from the projection volume driver
func MakePayload(mappings []v1.KeyToPath, secret *v1.Secret, defaultMode *int32, optional bool) (map[string]volumeutil.FileProjection, error) {
    payload := make(map[string]volumeutil.FileProjection, len(secret.Data))
    
    if len(mappings) == 0 {
        // 直接映射所有 Secret 数据
        for name, data := range secret.Data {
            payload[name] = volumeutil.FileProjection{
                Data: data,  // data 已经是解码后的 []byte
                Mode: *defaultMode,
            }
        }
    } else {
        // 根据指定的键路径映射
        for _, ktp := range mappings {
            content, ok := secret.Data[ktp.Key]
            if !ok {
                if optional {
                    continue
                }
                return nil, fmt.Errorf("references non-existent secret key: %s", ktp.Key)
            }
            
            fileProjection := volumeutil.FileProjection{
                Data: content,
                Mode: *defaultMode,
            }
            if ktp.Mode != nil {
                fileProjection.Mode = *ktp.Mode
            }
            payload[ktp.Path] = fileProjection
        }
    }
    
    return payload, nil
}
```

### 4. 内存文件系统特性

Secret 卷使用内存文件系统（tmpfs）来存储敏感数据：

```go
func wrappedVolumeSpec() volume.Spec {
    return volume.Spec{
        Volume: &v1.Volume{
            VolumeSource: v1.VolumeSource{
                EmptyDir: &v1.EmptyDirVolumeSource{
                    Medium: v1.StorageMediumMemory  // 使用内存存储
                }
            }
        },
    }
}
```

### 5. Secret 卷特性

```go
func (sv *secretVolume) GetAttributes() volume.Attributes {
    return volume.Attributes{
        ReadOnly:       true,   // Secret 卷是只读的
        Managed:        true,   // 由 Kubernetes 管理
        SELinuxRelabel: true,   // 支持 SELinux 重新标记
    }
}
```

---

## Secret 验证与策略

### 1. Secret 验证规则

基于 `pkg/apis/core/validation/validation.go`：

```go
// ValidateSecret tests if required fields in the Secret are set.
func ValidateSecret(secret *core.Secret) field.ErrorList {
    allErrs := ValidateObjectMeta(&secret.ObjectMeta, true, ValidateSecretName, field.NewPath("metadata"))
    
    dataPath := field.NewPath("data")
    totalSize := 0
    
    // 验证 Data 字段
    for key, value := range secret.Data {
        // 使用与 ConfigMap 相同的键名验证规则
        for _, msg := range validation.IsConfigMapKey(key) {
            allErrs = append(allErrs, field.Invalid(dataPath.Key(key), key, msg))
        }
        totalSize += len(value)
    }
    
    // 检查总大小限制 (1MB)
    if totalSize > core.MaxSecretSize {
        allErrs = append(allErrs, field.TooLong(dataPath, "", core.MaxSecretSize))
    }
    
    // 验证特定类型的 Secret
    switch secret.Type {
    case core.SecretTypeServiceAccountToken:
        // ServiceAccount Token 必须包含服务账号名称注解
        if value := secret.Annotations[core.ServiceAccountNameKey]; len(value) == 0 {
            allErrs = append(allErrs, field.Required(
                field.NewPath("metadata", "annotations").Key(core.ServiceAccountNameKey), ""))
        }
    
    case core.SecretTypeDockercfg:
        // Docker 配置必须是有效的 JSON
        dockercfgBytes, exists := secret.Data[core.DockerConfigKey]
        if !exists {
            allErrs = append(allErrs, field.Required(dataPath.Key(core.DockerConfigKey), ""))
            break
        }
        if err := json.Unmarshal(dockercfgBytes, &map[string]interface{}{}); err != nil {
            allErrs = append(allErrs, field.Invalid(
                dataPath.Key(core.DockerConfigKey), "<secret contents redacted>", err.Error()))
        }
    
    case core.SecretTypeDockerConfigJSON:
        // Docker Config JSON 验证
        dockerConfigJSONBytes, exists := secret.Data[core.DockerConfigJSONKey]
        if !exists {
            allErrs = append(allErrs, field.Required(dataPath.Key(core.DockerConfigJSONKey), ""))
            break
        }
        if err := json.Unmarshal(dockerConfigJSONBytes, &map[string]interface{}{}); err != nil {
            allErrs = append(allErrs, field.Invalid(
                dataPath.Key(core.DockerConfigJSONKey), "<secret contents redacted>", err.Error()))
        }
    
    case core.SecretTypeBasicAuth:
        // 基本认证至少需要用户名或密码之一
        if len(secret.Data[core.BasicAuthUsernameKey]) == 0 && 
           len(secret.Data[core.BasicAuthPasswordKey]) == 0 {
            allErrs = append(allErrs, field.Required(
                dataPath, "must contain at least username or password"))
        }
    
    case core.SecretTypeSSHAuth:
        // SSH 认证必须包含私钥
        if len(secret.Data[core.SSHAuthPrivateKey]) == 0 {
            allErrs = append(allErrs, field.Required(dataPath.Key(core.SSHAuthPrivateKey), ""))
        }
    
    case core.SecretTypeTLS:
        // TLS 必须包含证书和私钥
        if len(secret.Data[core.TLSCertKey]) == 0 {
            allErrs = append(allErrs, field.Required(dataPath.Key(core.TLSCertKey), ""))
        }
        if len(secret.Data[core.TLSPrivateKeyKey]) == 0 {
            allErrs = append(allErrs, field.Required(dataPath.Key(core.TLSPrivateKeyKey), ""))
        }
        
        // 验证 TLS 证书和私钥的有效性
        if len(secret.Data[core.TLSCertKey]) > 0 && len(secret.Data[core.TLSPrivateKeyKey]) > 0 {
            if _, err := tls.X509KeyPair(secret.Data[core.TLSCertKey], secret.Data[core.TLSPrivateKeyKey]); err != nil {
                allErrs = append(allErrs, field.Invalid(dataPath, "<secret contents redacted>", err.Error()))
            }
        }
    }
    
    return allErrs
}
```

### 2. Secret 更新验证

```go
// ValidateSecretUpdate tests if required fields in the Secret are set during an update.
func ValidateSecretUpdate(newSecret, oldSecret *core.Secret) field.ErrorList {
    allErrs := ValidateObjectMetaUpdate(&newSecret.ObjectMeta, &oldSecret.ObjectMeta, field.NewPath("metadata"))
    
    // 验证不可变性
    if oldSecret.Immutable != nil && *oldSecret.Immutable {
        // 不能将不可变的 Secret 改为可变
        if newSecret.Immutable == nil || !*newSecret.Immutable {
            allErrs = append(allErrs, field.Forbidden(field.NewPath("immutable"), 
                "field is immutable when `immutable` is set"))
        }
        
        // Data 字段不能修改
        if !reflect.DeepEqual(newSecret.Data, oldSecret.Data) {
            allErrs = append(allErrs, field.Forbidden(field.NewPath("data"), 
                "field is immutable when `immutable` is set"))
        }
        
        // Type 字段不能修改
        if newSecret.Type != oldSecret.Type {
            allErrs = append(allErrs, field.Forbidden(field.NewPath("type"), 
                "field is immutable when `immutable` is set"))
        }
    }
    
    allErrs = append(allErrs, ValidateSecret(newSecret)...)
    return allErrs
}
```

---

## Secret 类型系统

### 1. 预定义 Secret 类型

Kubernetes 提供了多种预定义的 Secret 类型，每种类型都有特定的数据格式和验证规则：

#### 1.1 Opaque（默认类型）

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: mysecret
type: Opaque  # 默认类型，可以省略
data:
  username: YWRtaW4=    # base64 编码的 "admin"
  password: MWYyZDFlMmU2N2Rm  # base64 编码的密码
```

#### 1.2 Service Account Token

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: sa-token-secret
  annotations:
    kubernetes.io/service-account.name: my-service-account  # 必需
    kubernetes.io/service-account.uid: "12345-67890"       # 可选
type: kubernetes.io/service-account-token
data:
  token: ZXlKaGJHY2lPaU...      # JWT token
  ca.crt: LS0tLS1CRUdJTi...     # CA 证书
  namespace: bXluYW1lc3BhY2U=   # 命名空间
```

#### 1.3 Docker Config

```yaml
# ~/.dockercfg 格式
apiVersion: v1
kind: Secret
metadata:
  name: dockercfg-secret
type: kubernetes.io/dockercfg
data:
  .dockercfg: eyJodHRwczovL2luZGV4LmRvY2tlci5pby92MS8iOnsidXNlcm5hbWUiOiJ0ZXN0IiwicGFzc3dvcmQiOiJ0ZXN0IiwiYXV0aCI6ImRHVnpkRHAwWlhOMCJ9fQ==

---
# ~/.docker/config.json 格式
apiVersion: v1
kind: Secret
metadata:
  name: docker-config-secret
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: eyJhdXRocyI6eyJodHRwczovL2luZGV4LmRvY2tlci5pby92MS8iOnsidXNlcm5hbWUiOiJ0ZXN0IiwicGFzc3dvcmQiOiJ0ZXN0IiwiYXV0aCI6ImRHVnpkRHAwWlhOMCJ9fX0=
```

#### 1.4 Basic Auth

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: basic-auth-secret
type: kubernetes.io/basic-auth
data:
  username: YWRtaW4=  # base64 编码的用户名
  password: cGFzc3dvcmQ=  # base64 编码的密码（可选，但至少需要username或password之一）
```

#### 1.5 SSH Auth

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ssh-auth-secret
type: kubernetes.io/ssh-auth
data:
  ssh-privatekey: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQ==  # SSH 私钥（必需）
```

#### 1.6 TLS

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: tls-secret
type: kubernetes.io/tls
data:
  tls.crt: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t  # TLS 证书（必需）
  tls.key: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQ==  # TLS 私钥（必需）
```

### 2. 类型系统架构

```mermaid
graph TB
    subgraph Secret_Types["Secret 类型系统"]
        OPAQUE["Opaque<br/>• 默认类型<br/>• 任意数据<br/>• 无特殊验证"]
        
        SA_TOKEN["ServiceAccount Token<br/>• JWT token<br/>• CA 证书<br/>• 自动创建"]
        
        DOCKER_CFG["Docker Config<br/>• .dockercfg<br/>• .dockerconfigjson<br/>• 镜像拉取认证"]
        
        BASIC_AUTH["Basic Auth<br/>• username<br/>• password<br/>• HTTP 基本认证"]
        
        SSH_AUTH["SSH Auth<br/>• ssh-privatekey<br/>• Git 仓库认证<br/>• SSH 连接"]
        
        TLS["TLS<br/>• tls.crt<br/>• tls.key<br/>• HTTPS 证书"]
        
        BOOTSTRAP["Bootstrap Token<br/>• token-id<br/>• token-secret<br/>• 集群引导"]
    end
    
    subgraph Validation["验证规则"]
        KEY_VALIDATION["键名验证<br/>• DNS 子域名格式<br/>• 字符限制<br/>• 长度检查"]
        
        SIZE_VALIDATION["大小验证<br/>• 总大小 ≤ 1MB<br/>• 单个值限制<br/>• 内存保护"]
        
        TYPE_VALIDATION["类型验证<br/>• 必需字段检查<br/>• 格式验证<br/>• 内容有效性"]
    end
    
    subgraph Usage["使用方式"]
        ENV_VAR["环境变量<br/>• base64 解码<br/>• 字符串转换<br/>• 前缀支持"]
        
        VOLUME_MOUNT["卷挂载<br/>• 内存文件系统<br/>• 文件权限<br/>• 原子更新"]
        
        IMAGE_PULL["镜像拉取<br/>• Docker 认证<br/>• 自动注入<br/>• 多仓库支持"]
    end
    
    OPAQUE --> KEY_VALIDATION
    SA_TOKEN --> TYPE_VALIDATION
    DOCKER_CFG --> TYPE_VALIDATION
    BASIC_AUTH --> TYPE_VALIDATION
    SSH_AUTH --> TYPE_VALIDATION
    TLS --> TYPE_VALIDATION
    
    KEY_VALIDATION --> ENV_VAR
    SIZE_VALIDATION --> VOLUME_MOUNT
    TYPE_VALIDATION --> IMAGE_PULL
    
    classDef typeStyle fill:#e6f3ff,stroke:#0066cc,stroke-width:2px
    classDef validationStyle fill:#fff2e6,stroke:#cc6600,stroke-width:2px
    classDef usageStyle fill:#e6ffe6,stroke:#009900,stroke-width:2px
    
    class OPAQUE,SA_TOKEN,DOCKER_CFG,BASIC_AUTH,SSH_AUTH,TLS,BOOTSTRAP typeStyle
    class KEY_VALIDATION,SIZE_VALIDATION,TYPE_VALIDATION validationStyle
    class ENV_VAR,VOLUME_MOUNT,IMAGE_PULL usageStyle
```

---

## Secret 时序交互分析

### 1. Secret 创建和存储流程

```mermaid
sequenceDiagram
    participant User as User/Controller
    participant API as API Server
    participant Validation as Validator
    participant Etcd as etcd
    participant Encryption as Encryption Provider
    
    Note over User,Encryption: Secret 创建和存储流程
    
    User->>API: 创建 Secret 请求
    API->>Validation: 执行准入控制
    Validation->>Validation: 验证 Secret 格式
    Validation->>Validation: 检查类型特定规则
    Validation->>Validation: 验证数据大小限制
    
    alt 验证成功
        Validation->>API: 验证通过
        API->>Encryption: 加密敏感数据
        Encryption->>Etcd: 存储加密的 Secret
        Etcd->>API: 存储确认
        API->>User: 返回创建成功
    else 验证失败
        Validation->>API: 返回验证错误
        API->>User: 返回错误响应
    end
```

### 2. Secret 获取和缓存流程

```mermaid
sequenceDiagram
    participant Pod as Pod Startup
    participant Kubelet as Kubelet
    participant SecretMgr as Secret Manager
    participant Cache as Local Cache
    participant API as API Server
    participant Etcd as etcd
    
    Note over Pod,Etcd: Secret 获取和缓存流程
    
    Pod->>Kubelet: Pod 启动请求
    Kubelet->>SecretMgr: RegisterPod(pod)
    SecretMgr->>SecretMgr: 解析 Pod 中的 Secret 引用
    
    loop 处理每个 Secret
        SecretMgr->>Cache: 检查本地缓存
        
        alt 缓存命中且未过期
            Cache->>SecretMgr: 返回缓存的 Secret
        else 缓存未命中或已过期
            SecretMgr->>API: GetSecret(namespace, name)
            API->>Etcd: 查询 Secret 数据
            Etcd->>API: 返回加密的 Secret
            API->>API: 解密 Secret 数据
            API->>SecretMgr: 返回解密的 Secret
            SecretMgr->>Cache: 更新本地缓存
        end
        
        SecretMgr->>Kubelet: 提供 Secret 数据
    end
    
    Kubelet->>Kubelet: 处理环境变量注入
    Kubelet->>Kubelet: 设置卷挂载
    Kubelet->>Pod: 启动容器
```

### 3. Secret 热更新流程

```mermaid
sequenceDiagram
    participant User as User
    participant API as API Server
    participant Etcd as etcd
    participant Watch as Watch Mechanism
    participant SecretMgr as Secret Manager
    participant VolumePlugin as Volume Plugin
    participant AtomicWriter as Atomic Writer
    participant App as Application
    
    Note over User,App: Secret 热更新流程
    
    User->>API: 更新 Secret
    API->>Etcd: 存储新的 Secret 数据
    Etcd->>Watch: 触发 Watch 事件
    Watch->>SecretMgr: Secret 更新通知
    SecretMgr->>SecretMgr: 更新本地缓存
    
    alt Secret 被卷挂载使用
        SecretMgr->>VolumePlugin: 触发卷更新
        VolumePlugin->>AtomicWriter: 写入新的 Secret 数据
        
        par 原子更新过程
            AtomicWriter->>AtomicWriter: 创建新时间戳目录
            AtomicWriter->>AtomicWriter: 写入新文件内容
            AtomicWriter->>AtomicWriter: 原子性切换符号链接
            AtomicWriter->>AtomicWriter: 清理旧时间戳目录
        end
        
        VolumePlugin->>App: 文件内容已更新
        
        Note over App: 应用程序可通过文件监听感知变化
    else Secret 被环境变量使用
        Note over SecretMgr,App: 环境变量不支持热更新需要重启 Pod
    end
```

### 4. ServiceAccount Token 自动管理流程

```mermaid
sequenceDiagram
    participant SA as ServiceAccount
    participant SAController as SA Controller
    participant TokenController as Token Controller
    participant API as API Server
    participant Pod as Pod
    participant Kubelet as Kubelet
    participant TokenProjector as Token Projector
    
    Note over SA,TokenProjector: ServiceAccount Token 自动管理流程
    
    SA->>SAController: ServiceAccount 创建
    SAController->>API: 创建对应的 Secret
    API->>TokenController: Secret 创建事件
    TokenController->>TokenController: 生成 JWT token
    TokenController->>API: 更新 Secret 数据
    
    Pod->>Kubelet: 使用 ServiceAccount 的 Pod 启动
    Kubelet->>Kubelet: 检查自动挂载设置
    
    alt 传统 Secret 挂载
        Kubelet->>API: 获取 ServiceAccount Secret
        Kubelet->>Kubelet: 挂载 token 到默认路径
    else 投影卷挂载
        Kubelet->>TokenProjector: 请求投影 token
        TokenProjector->>API: 请求短期 token
        API->>TokenProjector: 返回带过期时间的 token
        TokenProjector->>Kubelet: 提供 token 数据
        
        loop Token 刷新
            TokenProjector->>TokenProjector: 检查 token 过期时间
            TokenProjector->>API: 刷新即将过期的 token
            TokenProjector->>TokenProjector: 更新文件中的 token
        end
    end
    
    Pod->>Pod: 应用程序可使用 token 访问 API
```

### 5. Secret 生命周期管理

```mermaid
stateDiagram-v2
    [*] --> Creating: 用户创建 Secret
    Creating --> Validating: API 接收请求
    Validating --> Storing: 验证通过
    Validating --> Failed: 验证失败
    Failed --> [*]: 返回错误
    
    Storing --> Active: 存储到 etcd
    Active --> Cached: 被 Pod 引用
    Cached --> InUse: 注入到容器
    
    InUse --> Updating: Secret 内容更新
    Updating --> Active: 更新完成
    
    Active --> Immutable: 设置不可变
    Immutable --> Immutable: 只能修改 metadata
    
    InUse --> Unused: Pod 删除
    Unused --> Active: 清理缓存
    
    Active --> Deleting: 用户删除
    Deleting --> [*]: 从 etcd 删除
    
    note right of Cached
        Secret Manager 缓存
        TTL 过期机制
        Watch 事件更新
    end note
    
    note right of InUse
        环境变量注入
        卷文件挂载
        热更新支持
    end note
    
    note right of Immutable
        immutable: true
        防止意外修改
        提高缓存效率
    end note
```

---

## Secret 使用样例

### 1. 基本使用样例

#### 1.1 创建和使用 Opaque Secret

```bash
# 方法1：从命令行创建
kubectl create secret generic my-secret \
  --from-literal=username=admin \
  --from-literal=password=secretpassword

# 方法2：从文件创建
echo -n "admin" > username.txt
echo -n "secretpassword" > password.txt
kubectl create secret generic my-secret \
  --from-file=username.txt \
  --from-file=password.txt

# 方法3：使用 YAML 文件
apiVersion: v1
kind: Secret
metadata:
  name: my-secret
type: Opaque
stringData:  # 使用 stringData 自动编码
  username: admin
  password: secretpassword
```

#### 1.2 环境变量使用方式

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: secret-env-pod
spec:
  containers:
  - name: app
    image: nginx
    env:
    # 单个环境变量引用
    - name: DB_USERNAME
      valueFrom:
        secretKeyRef:
          name: my-secret
          key: username
          optional: false
    - name: DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: my-secret
          key: password
          optional: false
    
    # 批量环境变量导入
    envFrom:
    - secretRef:
        name: my-secret
        optional: false
    - prefix: "DB_"  # 添加前缀
      secretRef:
        name: my-secret
        optional: true
```

#### 1.3 卷挂载使用方式

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: secret-volume-pod
spec:
  containers:
  - name: app
    image: nginx
    volumeMounts:
    - name: secret-volume
      mountPath: /etc/secrets
      readOnly: true
  
  volumes:
  - name: secret-volume
    secret:
      secretName: my-secret
      defaultMode: 0400  # 只读权限
      items:  # 可选：指定特定的键和文件名
      - key: username
        path: db-username
        mode: 0400
      - key: password
        path: db-password
        mode: 0400
```

### 2. TLS Secret 使用样例

#### 2.1 创建 TLS Secret

```bash
# 生成私钥和证书
openssl genrsa -out tls.key 2048
openssl req -new -x509 -key tls.key -out tls.crt -days 365 -subj "/CN=example.com"

# 创建 TLS Secret
kubectl create secret tls tls-secret \
  --cert=tls.crt \
  --key=tls.key

# 或使用 YAML
apiVersion: v1
kind: Secret
metadata:
  name: tls-secret
type: kubernetes.io/tls
data:
  tls.crt: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t...  # base64 编码的证书
  tls.key: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQ==  # base64 编码的私钥
```

#### 2.2 在 Ingress 中使用 TLS Secret

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: tls-ingress
spec:
  tls:
  - hosts:
    - example.com
    secretName: tls-secret  # 引用 TLS Secret
  rules:
  - host: example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: web-service
            port:
              number: 80
```

### 3. Docker Registry Secret 使用样例

#### 3.1 创建 Docker Registry Secret

```bash
# 方法1：使用 kubectl 命令
kubectl create secret docker-registry registry-secret \
  --docker-server=https://index.docker.io/v1/ \
  --docker-username=myuser \
  --docker-password=mypassword \
  --docker-email=user@example.com

# 方法2：使用现有的 Docker 配置
kubectl create secret generic registry-secret \
  --from-file=.dockerconfigjson=$HOME/.docker/config.json \
  --type=kubernetes.io/dockerconfigjson
```

#### 3.2 在 Pod 中使用 Registry Secret

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: private-registry-pod
spec:
  imagePullSecrets:  # 全局镜像拉取认证
  - name: registry-secret
  
  containers:
  - name: app
    image: myregistry.com/myapp:latest  # 私有镜像
    
  # 或者在 ServiceAccount 中配置
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-service-account
imagePullSecrets:
- name: registry-secret

---
apiVersion: v1
kind: Pod
metadata:
  name: sa-registry-pod
spec:
  serviceAccountName: my-service-account  # 使用配置了认证的 SA
  containers:
  - name: app
    image: myregistry.com/myapp:latest
```

### 4. SSH Auth Secret 使用样例

#### 4.1 创建 SSH Secret

```bash
# 生成 SSH 密钥对
ssh-keygen -t rsa -b 4096 -f ssh-key -N ""

# 创建 SSH Secret
kubectl create secret generic ssh-secret \
  --from-file=ssh-privatekey=ssh-key \
  --type=kubernetes.io/ssh-auth

# 或使用 YAML
apiVersion: v1
kind: Secret
metadata:
  name: ssh-secret
type: kubernetes.io/ssh-auth
data:
  ssh-privatekey: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQ==  # SSH 私钥
```

#### 4.2 在 Pod 中使用 SSH Secret

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: git-clone-pod
spec:
  containers:
  - name: git-clone
    image: alpine/git
    command:
    - sh
    - -c
    - |
      # 设置 SSH 配置
      mkdir -p ~/.ssh
      cp /etc/ssh/ssh-privatekey ~/.ssh/id_rsa
      chmod 600 ~/.ssh/id_rsa
      ssh-keyscan github.com >> ~/.ssh/known_hosts
      
      # 克隆私有仓库
      git clone git@github.com:myuser/private-repo.git /workspace
    
    volumeMounts:
    - name: ssh-volume
      mountPath: /etc/ssh
      readOnly: true
  
  volumes:
  - name: ssh-volume
    secret:
      secretName: ssh-secret
      defaultMode: 0600  # SSH 私钥需要严格的权限
```

### 5. ServiceAccount Token 使用样例

#### 5.1 自动挂载的 ServiceAccount Token

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-service-account
  namespace: default
automountServiceAccountToken: true  # 默认为 true

---
apiVersion: v1
kind: Pod
metadata:
  name: sa-token-pod
spec:
  serviceAccountName: my-service-account
  containers:
  - name: app
    image: nginx
    # Token 自动挂载到 /var/run/secrets/kubernetes.io/serviceaccount/
    # 包含以下文件：
    # - token: JWT token
    # - ca.crt: CA 证书  
    # - namespace: 命名空间
```

#### 5.2 投影卷方式使用 Token

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: projected-token-pod
spec:
  serviceAccountName: my-service-account
  containers:
  - name: app
    image: nginx
    volumeMounts:
    - name: projected-token
      mountPath: /var/run/secrets/tokens
      readOnly: true
  
  volumes:
  - name: projected-token
    projected:
      sources:
      - serviceAccountToken:
          path: token
          expirationSeconds: 3600  # 1小时过期
          audience: "https://kubernetes.default.svc.cluster.local"
      - configMap:
          name: ca-configmap
          items:
          - key: ca.crt
            path: ca.crt
      - downwardAPI:
          items:
          - path: namespace
            fieldRef:
              fieldPath: metadata.namespace
```

### 6. 不可变 Secret 使用样例

```yaml
# 创建不可变 Secret
apiVersion: v1
kind: Secret
metadata:
  name: immutable-secret
type: Opaque
immutable: true  # 设置为不可变
data:
  config: Y29uZmlndXJhdGlvbiBkYXRh  # 配置数据

---
apiVersion: v1
kind: Pod
metadata:
  name: immutable-secret-pod
spec:
  containers:
  - name: app
    image: nginx
    env:
    - name: CONFIG_DATA
      valueFrom:
        secretKeyRef:
          name: immutable-secret
          key: config
```

### 7. 多 Secret 组合使用样例

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: multi-secret-pod
spec:
  containers:
  - name: app
    image: myapp:latest
    
    # 环境变量方式使用多个 Secret
    env:
    - name: DB_HOST
      value: "database.example.com"
    - name: DB_USERNAME
      valueFrom:
        secretKeyRef:
          name: db-credentials
          key: username
    - name: DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: db-credentials
          key: password
    - name: API_KEY
      valueFrom:
        secretKeyRef:
          name: external-api-secret
          key: api-key
    
    # 批量导入 Secret
    envFrom:
    - secretRef:
        name: app-config-secret
        optional: true
    - prefix: "CACHE_"
      secretRef:
        name: cache-config-secret
        optional: true
    
    # 卷挂载方式使用多个 Secret
    volumeMounts:
    - name: tls-certs
      mountPath: /etc/tls
      readOnly: true
    - name: ssh-keys
      mountPath: /etc/ssh
      readOnly: true
    - name: app-secrets
      mountPath: /etc/secrets
      readOnly: true
  
  # 镜像拉取认证
  imagePullSecrets:
  - name: registry-secret
  
  volumes:
  # TLS 证书
  - name: tls-certs
    secret:
      secretName: tls-secret
      defaultMode: 0444
  
  # SSH 密钥
  - name: ssh-keys
    secret:
      secretName: ssh-secret
      defaultMode: 0600
      items:
      - key: ssh-privatekey
        path: id_rsa
        mode: 0600
  
  # 应用 Secret
  - name: app-secrets
    secret:
      secretName: app-secret
      items:
      - key: config.json
        path: app-config.json
      - key: license.key
        path: license
```

---

## Secret 最佳实践

### 1. 安全实践

#### 1.1 权限控制

```yaml
# RBAC 配置示例
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: production
  name: secret-reader
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list"]
  resourceNames: ["app-secret", "db-secret"]  # 限制特定 Secret

---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: production
  name: secret-admin
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["*"]  # 完整权限

---
# 应用程序只能读取自己的 Secret
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: myapp
  name: myapp-secret-access
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get"]
  resourceNames: ["myapp-secrets"]
```

#### 1.2 静态加密配置

```yaml
# API Server 配置文件
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
- resources:
  - secrets
  providers:
  - aescbc:
      keys:
      - name: key1
        secret: <32字节base64编码密钥>
  - identity: {}  # 回退到未加密
```

#### 1.3 网络策略限制

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-secret-access
  namespace: production
spec:
  podSelector:
    matchLabels:
      role: secret-consumer
  policyTypes:
  - Ingress
  - Egress
  ingress: []  # 拒绝所有入站
  egress:
  - to:
    - namespaceSelector:
        matchLabels:
          name: kube-system
    ports:
    - protocol: TCP
      port: 443  # 只允许访问 API Server
```

### 2. 性能优化

#### 2.1 Secret 管理器选择

```yaml
# Kubelet 配置
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
configMapAndSecretChangeDetectionStrategy: Watch  # 推荐用于生产环境

# 替代选择：
# - Watch: 基于事件，实时性好，适合生产环境
# - TTLCache: 基于缓存，减少 API 调用，适合大规模集群  
# - Get: 直接调用，简单可靠，适合测试环境
```

#### 2.2 不可变 Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: static-config
type: Opaque
immutable: true  # 不可变 Secret 有更好的缓存效率
data:
  static-data: c3RhdGljIGNvbmZpZ3VyYXRpb24=
```

### 3. 运维实践

#### 3.1 监控和告警

```yaml
# Prometheus 规则示例
groups:
- name: secret-monitoring
  rules:
  - alert: SecretTooLarge
    expr: kube_secret_info * on(secret, namespace) group_left(bytes) kube_secret_size > 900000
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Secret {{ $labels.secret }} in namespace {{ $labels.namespace }} is approaching size limit"
      
  - alert: SecretAccessFailure
    expr: increase(apiserver_audit_total{verb="get",objectRef_resource="secrets",objectRef_namespace!="kube-system"}[5m]) > 100
    for: 2m
    labels:
      severity: critical
    annotations:
      summary: "High number of Secret access failures detected"
```

#### 3.2 备份和恢复

```bash
#!/bin/bash
# Secret 备份脚本

NAMESPACE=${1:-default}
BACKUP_DIR="./secret-backup-$(date +%Y%m%d-%H%M%S)"

mkdir -p "$BACKUP_DIR"

echo "Backing up secrets in namespace: $NAMESPACE"

# 获取所有 Secret（排除 ServiceAccount token）
kubectl get secrets -n "$NAMESPACE" -o json | \
  jq '.items[] | select(.type != "kubernetes.io/service-account-token")' | \
  jq -s '{"apiVersion": "v1", "kind": "List", "items": .}' > "$BACKUP_DIR/secrets.json"

# 加密备份文件
gpg --symmetric --cipher-algo AES256 "$BACKUP_DIR/secrets.json"
rm "$BACKUP_DIR/secrets.json"

echo "Backup completed: $BACKUP_DIR/secrets.json.gpg"
```

#### 3.3 轮换策略

```yaml
# CronJob 实现定期轮换
apiVersion: batch/v1
kind: CronJob
metadata:
  name: secret-rotator
  namespace: production
spec:
  schedule: "0 2 * * 0"  # 每周日凌晨2点
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: secret-rotator
          containers:
          - name: rotator
            image: secret-rotator:latest
            env:
            - name: SECRET_NAME
              value: "db-credentials"
            - name: NAMESPACE
              value: "production"
            command:
            - /bin/sh
            - -c
            - |
              # 生成新密码
              NEW_PASSWORD=$(openssl rand -base64 32)
              
              # 更新外部系统密码
              update_external_system_password "$NEW_PASSWORD"
              
              # 更新 Secret
              kubectl patch secret "$SECRET_NAME" -n "$NAMESPACE" \
                --patch="{\"data\":{\"password\":\"$(echo -n $NEW_PASSWORD | base64)\"}}"
              
              # 重启相关应用
              kubectl rollout restart deployment/myapp -n "$NAMESPACE"
              
          restartPolicy: OnFailure
```

### 4. 开发实践

#### 4.1 本地开发环境

```yaml
# 开发环境 Secret 模板
apiVersion: v1
kind: Secret
metadata:
  name: dev-secrets
  namespace: development
type: Opaque
stringData:
  database-url: "postgresql://user:pass@localhost:5432/devdb"
  redis-url: "redis://localhost:6379"
  debug-mode: "true"
  log-level: "debug"
```

#### 4.2 CI/CD 集成

```yaml
# GitHub Actions 示例
name: Deploy with Secrets
on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v2
    
    - name: Setup kubectl
      uses: azure/setup-kubectl@v1
    
    - name: Create Secret
      env:
        DB_PASSWORD: ${{ secrets.DB_PASSWORD }}
        API_KEY: ${{ secrets.API_KEY }}
      run: |
        kubectl create secret generic app-secrets \
          --from-literal=db-password="$DB_PASSWORD" \
          --from-literal=api-key="$API_KEY" \
          --dry-run=client -o yaml | kubectl apply -f -
    
    - name: Deploy Application
      run: kubectl apply -f k8s/
```

#### 4.3 多环境管理

```bash
# 环境特定的 Secret 管理
#!/bin/bash

ENVIRONMENT=${1:-dev}

case $ENVIRONMENT in
  dev)
    kubectl apply -f secrets/dev-secrets.yaml
    ;;
  staging)
    kubectl apply -f secrets/staging-secrets.yaml
    ;;
  production)
    # 生产环境使用外部密钥管理系统
    ./scripts/sync-from-vault.sh
    ;;
  *)
    echo "Unknown environment: $ENVIRONMENT"
    exit 1
    ;;
esac
```

---

## 总结

Secret 作为 Kubernetes 中敏感信息管理的核心组件，提供了完整而安全的敏感数据处理能力：

### 🎯 **核心价值**

1. **安全存储**：Base64 编码、etcd 静态加密、内存文件系统
2. **类型系统**：多种预定义类型，满足不同场景需求
3. **灵活使用**：环境变量、卷挂载、镜像拉取认证等多种方式
4. **权限控制**：基于 RBAC 的细粒度访问控制

### 🏗️ **架构优势**

1. **分层设计**：API 层、存储层、节点层、应用层的清晰分工
2. **管理器模式**：三种管理器实现适应不同场景需求
3. **原子更新**：基于符号链接的零中断热更新机制
4. **缓存优化**：TTL 缓存和 Watch 机制减少 API 调用

### 🔒 **安全特性**

1. **静态加密**：支持多种加密提供者
2. **传输安全**：TLS 加密的 API 通信
3. **访问控制**：RBAC 权限管理
4. **不可变模式**：防止意外修改，提高缓存效率

### 🚀 **高级功能**

1. **自动管理**：ServiceAccount Token 的自动创建和轮换
2. **投影卷**：支持 token 过期和自动刷新
3. **热更新**：卷挂载方式支持配置热更新
4. **类型验证**：特定类型的格式和内容验证

### 🎯 **应用场景**

- **数据库认证**：数据库连接密码和证书
- **API 访问**：外部服务的 API 密钥和 token
- **TLS 证书**：HTTPS 和服务间通信证书
- **容器镜像**：私有镜像仓库的认证信息
- **SSH 访问**：Git 仓库和远程系统访问密钥

Secret 通过其完善的架构设计和丰富的安全特性，为 Kubernetes 应用提供了企业级的敏感信息管理解决方案。其多层次的安全保护、灵活的使用方式和强大的管理功能，使得应用程序能够在云原生环境中安全地处理各种敏感数据，是现代容器化应用不可或缺的基础设施组件。
