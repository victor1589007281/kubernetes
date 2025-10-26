# Kubernetes NetworkPolicy 网络策略深度解读

## 目录

1. [概述](#概述)
2. [NetworkPolicy 核心概念](#networkpolicy-核心概念)
3. [NetworkPolicy 整体架构](#networkpolicy-整体架构)
4. [网络策略规则定义](#网络策略规则定义)
5. [流量控制机制](#流量控制机制)
6. [CNI 插件集成](#cni-插件集成)
7. [实际应用场景](#实际应用场景)
8. [最佳实践与排查](#最佳实践与排查)
9. [总结](#总结)

---

## 概述

NetworkPolicy 是 Kubernetes 中用于定义网络安全策略的资源对象，它通过标签选择器指定作用的 Pod 集合，并定义允许的入站（Ingress）和出站（Egress）流量规则。NetworkPolicy 提供了命名空间级别的网络隔离能力，是构建安全的多租户 Kubernetes 集群的重要组件。本文档基于 Kubernetes 源码深入解读 NetworkPolicy 的架构设计、工作原理和实现机制。

### 核心特性

- **网络隔离**：基于标签选择器的 Pod 网络隔离
- **双向流量控制**：支持 Ingress 和 Egress 规则定义
- **细粒度控制**：支持端口、协议、IP 段等多维度控制
- **命名空间集成**：与 Kubernetes RBAC 和命名空间无缝集成
- **CNI 插件实现**：依赖网络插件具体实现策略执行

---

## NetworkPolicy 核心概念

### 1. NetworkPolicy 资源结构

基于源码 `pkg/apis/networking/types.go`：

```go
type NetworkPolicySpec struct {
    // Pod 选择器 - 指定应用此策略的 Pod
    PodSelector metav1.LabelSelector

    // 入站规则列表
    Ingress []NetworkPolicyIngressRule

    // 出站规则列表  
    Egress []NetworkPolicyEgressRule

    // 策略类型：["Ingress"], ["Egress"], 或 ["Ingress", "Egress"]
    PolicyTypes []PolicyType
}

type NetworkPolicyIngressRule struct {
    // 端口规则
    Ports []NetworkPolicyPort
    
    // 允许的来源
    From []NetworkPolicyPeer
}

type NetworkPolicyEgressRule struct {
    // 端口规则
    Ports []NetworkPolicyPort
    
    // 允许的目标
    To []NetworkPolicyPeer
}
```

### 2. NetworkPolicy 核心架构图

```mermaid
graph TB
    subgraph "**NetworkPolicy 架构全景**"
        
        subgraph "**策略定义层**"
            
            NP_RESOURCE[**NetworkPolicy 资源**<br/>• Pod 选择器<br/>• Ingress 规则<br/>• Egress 规则<br/>• 策略类型]
            
            RULE_TYPES[**规则组件**<br/>• **NetworkPolicyPeer**: 对等体<br/>• **NetworkPolicyPort**: 端口<br/>• **IPBlock**: IP段<br/>• **LabelSelector**: 标签选择器]
        end
        
        subgraph "**Kubernetes 控制平面**"
            
            API_SERVER[**API Server**<br/>• NetworkPolicy API<br/>• 资源验证<br/>• etcd 存储<br/>• 变更通知]
            
            CONTROLLER[**NetworkPolicy Controller**<br/>• 策略监听<br/>• 规则解析<br/>• 事件分发<br/>• 状态同步]
        end
        
        subgraph "**网络插件层**"
            
            CNI_PLUGINS[**CNI 插件**<br/>• **Calico**: iptables/BPF<br/>• **Cilium**: eBPF<br/>• **Weave**: 用户空间<br/>• **Flannel**: 不支持]
            
            POLICY_ENGINE[**策略引擎**<br/>• 规则转换<br/>• 流量过滤<br/>• 连接跟踪<br/>• 策略执行]
        end
        
        subgraph "**数据平面**"
            
            NETWORK_STACK[**网络栈**<br/>• iptables 规则<br/>• eBPF 程序<br/>• 内核网络过滤<br/>• 流量转发]
            
            POD_NETWORK[**Pod 网络**<br/>• 虚拟网卡<br/>• 网络命名空间<br/>• 路由表<br/>• 防火墙规则]
        end
        
        subgraph "**流量流向**"
            
            INGRESS_TRAFFIC[**入站流量**<br/>• 外部→Pod<br/>• Pod→Pod<br/>• Service→Pod<br/>• 规则匹配]
            
            EGRESS_TRAFFIC[**出站流量**<br/>• Pod→外部<br/>• Pod→Pod<br/>• Pod→Service<br/>• 规则匹配]
        end
    end
    
    NP_RESOURCE --> API_SERVER
    RULE_TYPES --> NP_RESOURCE
    API_SERVER --> CONTROLLER
    
    CONTROLLER --> CNI_PLUGINS
    CNI_PLUGINS --> POLICY_ENGINE
    POLICY_ENGINE --> NETWORK_STACK
    
    NETWORK_STACK --> POD_NETWORK
    POD_NETWORK --> INGRESS_TRAFFIC
    POD_NETWORK --> EGRESS_TRAFFIC
    
    style NP_RESOURCE fill:#90EE90,stroke:#006400,stroke-width:2px
    style CNI_PLUGINS fill:#87CEEB,stroke:#4682B4,stroke-width:2px
    style POLICY_ENGINE fill:#DDA0DD,stroke:#8B008B,stroke-width:2px
    style POD_NETWORK fill:#98FB98,stroke:#006400,stroke-width:2px
```

---

## NetworkPolicy 整体架构

### 1. 网络策略处理流程

```mermaid
sequenceDiagram
    participant ADMIN as **管理员**
    participant API as **API Server**
    participant CONTROLLER as **NetworkPolicy Controller**
    participant CNI as **CNI 插件**
    participant DATAPLANE as **数据平面**
    participant POD as **Pod**
    
    Note over ADMIN,POD: **NetworkPolicy 策略生效流程**
    
    ADMIN->>API: **1. 创建 NetworkPolicy**
    Note right of ADMIN: **定义网络策略规则**<br/>**podSelector, ingress, egress**
    
    API->>API: **2. 策略验证**
    Note right of API: **• 语法验证**<br/>**• 字段完整性检查**<br/>**• 标签选择器验证**
    
    API->>CONTROLLER: **3. 策略变更通知**
    Note right of API: **Watch 机制触发**<br/>**NetworkPolicy 事件**
    
    CONTROLLER->>CONTROLLER: **4. 策略解析**
    Note right of CONTROLLER: **• 解析 Pod 选择器**<br/>**• 计算影响的 Pod**<br/>**• 构建规则映射**
    
    CONTROLLER->>CNI: **5. 策略下发**
    Note right of CONTROLLER: **• 策略规则**<br/>**• Pod 标识信息**<br/>**• 更新指令**
    
    CNI->>CNI: **6. 规则转换**
    Note right of CNI: **• 转换为具体实现**<br/>**• iptables 规则**<br/>**• eBPF 程序等**
    
    CNI->>DATAPLANE: **7. 规则应用**
    Note right of CNI: **网络规则生效**
    
    DATAPLANE->>POD: **8. 流量过滤生效**
    Note right of DATAPLANE: **基于规则过滤流量**
    
    Note over ADMIN,POD: **流量匹配和执行阶段**
    
    POD->>DATAPLANE: **9. 网络流量**
    Note right of POD: **入站/出站网络请求**
    
    DATAPLANE->>DATAPLANE: **10. 策略匹配**
    Note right of DATAPLANE: **• 检查源/目标**<br/>**• 检查端口/协议**<br/>**• 应用策略规则**
    
    alt **流量符合策略**
        DATAPLANE->>POD: **11a. 允许流量**
        Note right of DATAPLANE: **流量正常通过**
    else **流量违反策略**
        DATAPLANE->>POD: **11b. 拒绝流量**
        Note right of DATAPLANE: **丢包或拒绝连接**
    end
```

---

## 网络策略规则定义

### 1. 策略类型与规则

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: example-policy
  namespace: default
spec:
  # 选择应用此策略的 Pod
  podSelector:
    matchLabels:
      app: web
  
  # 策略类型
  policyTypes:
  - Ingress
  - Egress
  
  # 入站规则
  ingress:
  - from:
    # 允许来自特定命名空间的 Pod
    - namespaceSelector:
        matchLabels:
          name: frontend
    # 允许来自特定 Pod
    - podSelector:
        matchLabels:
          app: api
    # 允许来自特定 IP 段
    - ipBlock:
        cidr: 10.0.0.0/16
        except:
        - 10.0.1.0/24
    ports:
    - protocol: TCP
      port: 80
    - protocol: TCP
      port: 443
  
  # 出站规则
  egress:
  - to:
    - podSelector:
        matchLabels:
          app: database
    ports:
    - protocol: TCP
      port: 3306
```

### 2. 规则匹配逻辑图

```mermaid
graph TB
    subgraph "**NetworkPolicy 规则匹配逻辑**"
        
        subgraph "**Pod 选择器匹配**"
            
            POD_SELECTOR[**podSelector**<br/>• **空选择器**: 匹配所有 Pod<br/>• **标签匹配**: 精确匹配<br/>• **命名空间范围**: 仅当前命名空间<br/>• **组合条件**: AND 逻辑]
        end
        
        subgraph "**Ingress 规则匹配**"
            
            INGRESS_RULES[**入站规则处理**<br/>• **无规则**: 拒绝所有入站<br/>• **空 from**: 允许所有来源<br/>• **多个 from**: OR 逻辑<br/>• **端口+来源**: AND 逻辑]
            
            FROM_TYPES[**from 类型**<br/>• **podSelector**: 相同命名空间 Pod<br/>• **namespaceSelector**: 跨命名空间选择<br/>• **ipBlock**: IP 地址段<br/>• **组合使用**: 灵活控制]
        end
        
        subgraph "**Egress 规则匹配**"
            
            EGRESS_RULES[**出站规则处理**<br/>• **无规则**: 拒绝所有出站<br/>• **空 to**: 允许所有目标<br/>• **多个 to**: OR 逻辑<br/>• **端口+目标**: AND 逻辑]
            
            TO_TYPES[**to 类型**<br/>• **podSelector**: 相同命名空间 Pod<br/>• **namespaceSelector**: 跨命名空间选择<br/>• **ipBlock**: IP 地址段<br/>• **组合使用**: 灵活控制]
        end
        
        subgraph "**端口规则匹配**"
            
            PORT_RULES[**端口匹配规则**<br/>• **协议**: TCP/UDP/SCTP<br/>• **端口号**: 具体端口<br/>• **端口名**: 命名端口<br/>• **端口范围**: port-endPort]
        end
        
        subgraph DefaultBehavior ["**默认行为**"]
            
            DEFAULT_BEHAVIOR[**默认策略行为**<br/>• **无 NetworkPolicy**: 允许所有流量<br/>• **有 NetworkPolicy**: 默认拒绝<br/>• **多策略叠加**: 规则累加生效<br/>• **策略冲突**: 宽松优先]
        end
    end
    
    POD_SELECTOR --> INGRESS_RULES
    POD_SELECTOR --> EGRESS_RULES
    
    INGRESS_RULES --> FROM_TYPES
    EGRESS_RULES --> TO_TYPES
    
    FROM_TYPES --> PORT_RULES
    TO_TYPES --> PORT_RULES
    
    PORT_RULES --> DEFAULT_BEHAVIOR
    
    style POD_SELECTOR fill:#90EE90,stroke:#006400,stroke-width:2px
    style INGRESS_RULES fill:#87CEEB,stroke:#4682B4,stroke-width:2px
    style EGRESS_RULES fill:#DDA0DD,stroke:#8B008B,stroke-width:2px
    style PORT_RULES fill:#98FB98,stroke:#006400,stroke-width:2px
```

---

## 流量控制机制

### 1. 流量匹配决策树

```mermaid
flowchart TD
    START([**网络流量到达**]) --> POLICY_CHECK{**是否有 NetworkPolicy？**}
    
    POLICY_CHECK -->|无策略| ALLOW_ALL[**✅ 允许所有流量**<br/>默认开放策略]
    POLICY_CHECK -->|有策略| DIRECTION{**流量方向？**}
    
    DIRECTION -->|入站| INGRESS_POLICY{**有 Ingress 策略？**}
    DIRECTION -->|出站| EGRESS_POLICY{**有 Egress 策略？**}
    
    INGRESS_POLICY -->|无| ALLOW_INGRESS[**✅ 允许入站**<br/>无限制]
    INGRESS_POLICY -->|有| INGRESS_RULES[**检查 Ingress 规则**]
    
    EGRESS_POLICY -->|无| ALLOW_EGRESS[**✅ 允许出站**<br/>无限制]
    EGRESS_POLICY -->|有| EGRESS_RULES[**检查 Egress 规则**]
    
    INGRESS_RULES --> INGRESS_FROM{**来源匹配？**}
    EGRESS_RULES --> EGRESS_TO{**目标匹配？**}
    
    INGRESS_FROM -->|匹配| INGRESS_PORT{**端口匹配？**}
    INGRESS_FROM -->|不匹配| CHECK_OTHER_INGRESS{**其他规则？**}
    
    EGRESS_TO -->|匹配| EGRESS_PORT{**端口匹配？**}
    EGRESS_TO -->|不匹配| CHECK_OTHER_EGRESS{**其他规则？**}
    
    INGRESS_PORT -->|匹配| ALLOW_INGRESS
    INGRESS_PORT -->|不匹配| CHECK_OTHER_INGRESS
    
    EGRESS_PORT -->|匹配| ALLOW_EGRESS
    EGRESS_PORT -->|不匹配| CHECK_OTHER_EGRESS
    
    CHECK_OTHER_INGRESS -->|有| INGRESS_FROM
    CHECK_OTHER_INGRESS -->|无| DENY_INGRESS[**❌ 拒绝入站**<br/>无匹配规则]
    
    CHECK_OTHER_EGRESS -->|有| EGRESS_TO
    CHECK_OTHER_EGRESS -->|无| DENY_EGRESS[**❌ 拒绝出站**<br/>无匹配规则]
    
    ALLOW_ALL --> END([**流量处理完成**])
    ALLOW_INGRESS --> END
    ALLOW_EGRESS --> END
    DENY_INGRESS --> END
    DENY_EGRESS --> END
    
    style START fill:#e6f3ff,stroke:#0066cc,stroke-width:2px
    style ALLOW_ALL fill:#90EE90,stroke:#006400,stroke-width:3px
    style ALLOW_INGRESS fill:#98FB98,stroke:#006400,stroke-width:2px
    style ALLOW_EGRESS fill:#98FB98,stroke:#006400,stroke-width:2px
    style DENY_INGRESS fill:#FFB6C1,stroke:#DC143C,stroke-width:2px
    style DENY_EGRESS fill:#FFB6C1,stroke:#DC143C,stroke-width:2px
```

### 2. 策略组合效果

```mermaid
graph TB
    subgraph "**NetworkPolicy 策略组合效果**"
        
        subgraph "**单一策略效果**"
            
            SINGLE_POLICY[**单个 NetworkPolicy**<br/>• **仅 Ingress**: 限制入站，出站开放<br/>• **仅 Egress**: 限制出站，入站开放<br/>• **双向限制**: 同时限制入站出站<br/>• **空规则**: 完全隔离]
        end
        
        subgraph "**多策略叠加**"
            
            MULTIPLE_POLICIES[**多个 NetworkPolicy**<br/>• **规则合并**: 取所有策略的并集<br/>• **累加生效**: 更宽松的访问<br/>• **命名空间隔离**: 每个命名空间独立<br/>• **优先级**: 无优先级概念]
        end
        
        subgraph "**策略冲突处理**"
            
            CONFLICT_RESOLUTION[**冲突解决策略**<br/>• **允许优先**: 有允许规则就放行<br/>• **宽松合并**: 取最宽松的规则<br/>• **独立评估**: 每个策略独立评估<br/>• **OR 逻辑**: 任一策略匹配即可]
        end
        
        subgraph "**特殊情况处理**"
            
            SPECIAL_CASES[**特殊场景**<br/>• **空 podSelector**: 匹配所有 Pod<br/>• **空规则列表**: 拒绝所有流量<br/>• **空 from/to**: 允许所有来源/目标<br/>• **本地节点流量**: 总是允许]
        end
        
        subgraph "**实际效果示例**"
            
            EXAMPLE_EFFECTS[**组合效果示例**<br/>**策略A**: 允许端口80<br/>**策略B**: 允许端口443<br/>**实际效果**: 允许80和443<br/>**结果**: 规则累加生效]
        end
    end
    
    SINGLE_POLICY --> MULTIPLE_POLICIES
    MULTIPLE_POLICIES --> CONFLICT_RESOLUTION
    CONFLICT_RESOLUTION --> SPECIAL_CASES
    SPECIAL_CASES --> EXAMPLE_EFFECTS
    
    style SINGLE_POLICY fill:#90EE90,stroke:#006400,stroke-width:2px
    style MULTIPLE_POLICIES fill:#87CEEB,stroke:#4682B4,stroke-width:2px
    style CONFLICT_RESOLUTION fill:#DDA0DD,stroke:#8B008B,stroke-width:2px
    style EXAMPLE_EFFECTS fill:#98FB98,stroke:#006400,stroke-width:2px
```

---

## CNI 插件集成

### 1. 主流 CNI 插件对 NetworkPolicy 的支持

```mermaid
graph TB
    subgraph "**CNI 插件 NetworkPolicy 支持对比**"
        
        subgraph "**Calico**"
            
            CALICO[**Calico 实现**<br/>• **技术栈**: iptables/eBPF<br/>• **性能**: 高性能<br/>• **功能**: 完整支持<br/>• **扩展**: 支持 GlobalNetworkPolicy]
            
            CALICO_FEATURES[**Calico 特性**<br/>• L3 网络架构<br/>• BGP 路由协议<br/>• 细粒度安全策略<br/>• 高可扩展性]
        end
        
        subgraph "**Cilium**"
            
            CILIUM[**Cilium 实现**<br/>• **技术栈**: eBPF<br/>• **性能**: 超高性能<br/>• **功能**: 完整支持+扩展<br/>• **特色**: L7 策略支持]
            
            CILIUM_FEATURES[**Cilium 特性**<br/>• eBPF 内核程序<br/>• L3/L4/L7 策略<br/>• 观测性强<br/>• 云原生优化]
        end
        
        subgraph "**Weave Net**"
            
            WEAVE[**Weave 实现**<br/>• **技术栈**: 用户空间代理<br/>• **性能**: 中等性能<br/>• **功能**: 基础支持<br/>• **特色**: 易于部署]
            
            WEAVE_FEATURES[**Weave 特性**<br/>• 自动发现<br/>• 加密通信<br/>• 简单配置<br/>• 跨云支持]
        end
        
        subgraph "**不支持插件**"
            
            UNSUPPORTED[**不支持的插件**<br/>• **Flannel**: 基础覆盖网络<br/>• **host-local**: 本地网络<br/>• **bridge**: 简单桥接<br/>• **macvlan**: MAC 地址虚拟化]
        end
        
        subgraph "**实现对比**"
            
            COMPARISON[**技术对比**<br/>**iptables**: 成熟稳定，规则数量限制<br/>**eBPF**: 高性能，复杂度高<br/>**用户空间**: 功能灵活，性能较低<br/>**内核集成**: 效率最高，开发复杂]
        end
    end
    
    CALICO --> CALICO_FEATURES
    CILIUM --> CILIUM_FEATURES  
    WEAVE --> WEAVE_FEATURES
    
    CALICO_FEATURES --> COMPARISON
    CILIUM_FEATURES --> COMPARISON
    WEAVE_FEATURES --> COMPARISON
    UNSUPPORTED --> COMPARISON
    
    style CALICO fill:#90EE90,stroke:#006400,stroke-width:2px
    style CILIUM fill:#87CEEB,stroke:#4682B4,stroke-width:2px
    style WEAVE fill:#DDA0DD,stroke:#8B008B,stroke-width:2px
    style UNSUPPORTED fill:#FFB6C1,stroke:#DC143C,stroke-width:2px
```

---

## 实际应用场景

### 1. 典型使用场景配置

#### **场景一：微服务隔离**

```yaml
# 前端服务只能访问API服务
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: frontend-netpol
  namespace: production
spec:
  podSelector:
    matchLabels:
      tier: frontend
  policyTypes:
  - Egress
  egress:
  # 只允许访问API服务
  - to:
    - podSelector:
        matchLabels:
          tier: api
    ports:
    - protocol: TCP
      port: 8080
  # 允许DNS查询
  - to: []
    ports:
    - protocol: UDP
      port: 53
```

#### **场景二：数据库安全访问**

```yaml
# 数据库只允许来自API层的访问
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: database-netpol
  namespace: production
spec:
  podSelector:
    matchLabels:
      tier: database
  policyTypes:
  - Ingress
  ingress:
  # 只允许API层访问
  - from:
    - podSelector:
        matchLabels:
          tier: api
    ports:
    - protocol: TCP
      port: 3306
  # 允许来自监控命名空间的访问
  - from:
    - namespaceSelector:
        matchLabels:
          name: monitoring
    ports:
    - protocol: TCP
      port: 9104
```

#### **场景三：命名空间隔离**

```yaml
# 拒绝所有跨命名空间流量
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-cross-namespace
  namespace: production
spec:
  podSelector: {}  # 匹配所有Pod
  policyTypes:
  - Ingress
  - Egress
  ingress:
  # 只允许同命名空间内的流量
  - from:
    - podSelector: {}
  egress:
  # 只允许访问同命名空间内的Pod
  - to:
    - podSelector: {}
  # 允许DNS查询
  - to:
    - namespaceSelector:
        matchLabels:
          name: kube-system
    ports:
    - protocol: UDP
      port: 53
```

### 2. 策略设计最佳实践

```mermaid
graph TB
    subgraph "**NetworkPolicy 最佳实践指南**"
        
        subgraph "**设计原则**"
            
            DESIGN_PRINCIPLES[**策略设计原则**<br/>• **最小权限原则**: 只开放必需的访问<br/>• **分层防护**: 多层安全策略<br/>• **明确拒绝**: 显式拒绝不需要的流量<br/>• **渐进实施**: 逐步收紧策略]
        end
        
        subgraph "**标签策略**"
            
            LABEL_STRATEGY[**标签使用策略**<br/>• **一致性命名**: 统一标签规范<br/>• **层级标签**: tier, app, version<br/>• **环境标签**: prod, staging, dev<br/>• **功能标签**: frontend, api, database]
        end
        
        subgraph "**测试验证**"
            
            TESTING[**策略测试方法**<br/>• **连通性测试**: 验证允许的连接<br/>• **隔离测试**: 验证拒绝的连接<br/>• **端口测试**: 验证端口级访问<br/>• **回归测试**: 确保策略变更安全]
        end
        
        subgraph "**监控告警**"
            
            MONITORING[**监控和告警**<br/>• **策略违规**: 监控被拒绝的连接<br/>• **异常流量**: 检测异常网络行为<br/>• **策略覆盖**: 确保策略完整覆盖<br/>• **性能影响**: 监控策略对性能的影响]
        end
        
        subgraph "**运维管理**"
            
            OPERATIONS[**运维实践**<br/>• **版本控制**: 策略配置版本管理<br/>• **变更管理**: 策略变更流程<br/>• **文档维护**: 策略文档同步更新<br/>• **应急预案**: 策略故障处理流程]
        end
        
        subgraph "**性能优化**"
            
            PERFORMANCE[**性能优化建议**<br/>• **规则简化**: 避免过于复杂的规则<br/>• **选择器优化**: 高效的标签选择器<br/>• **策略合并**: 合并相似的策略<br/>• **插件选择**: 选择高性能CNI插件]
        end
    end
    
    DESIGN_PRINCIPLES --> LABEL_STRATEGY
    LABEL_STRATEGY --> TESTING
    TESTING --> MONITORING
    MONITORING --> OPERATIONS
    OPERATIONS --> PERFORMANCE
    
    style DESIGN_PRINCIPLES fill:#90EE90,stroke:#006400,stroke-width:2px
    style LABEL_STRATEGY fill:#87CEEB,stroke:#4682B4,stroke-width:2px
    style TESTING fill:#DDA0DD,stroke:#8B008B,stroke-width:2px
    style MONITORING fill:#98FB98,stroke:#006400,stroke-width:2px
    style PERFORMANCE fill:#FFE4B5,stroke:#FF8C00,stroke-width:2px
```

---

## 最佳实践与排查

### 1. 常见问题排查流程

```mermaid
flowchart TD
    START([**网络连接问题**]) --> CNI_SUPPORT{**CNI插件支持NetworkPolicy？**}
    
    CNI_SUPPORT -->|不支持| CNI_UPGRADE[**❌ 升级CNI插件**<br/>Calico/Cilium/Weave]
    CNI_SUPPORT -->|支持| POLICY_EXISTS{**是否有NetworkPolicy？**}
    
    POLICY_EXISTS -->|无策略| ALLOW_ALL[**✅ 允许所有流量**<br/>默认开放]
    POLICY_EXISTS -->|有策略| POD_SELECTED{**Pod被策略选中？**}
    
    POD_SELECTED -->|未选中| ALLOW_ALL
    POD_SELECTED -->|选中| RULE_TYPE{**检查规则类型**}
    
    RULE_TYPE --> INGRESS_CHECK{**Ingress规则检查**}
    RULE_TYPE --> EGRESS_CHECK{**Egress规则检查**}
    
    INGRESS_CHECK --> FROM_MATCH{**from匹配？**}
    FROM_MATCH -->|不匹配| CHECK_LABELS[**🔍 检查标签选择器**<br/>Pod/Namespace标签]
    FROM_MATCH -->|匹配| PORT_MATCH_IN{**端口匹配？**}
    
    PORT_MATCH_IN -->|不匹配| CHECK_PORTS[**🔍 检查端口配置**<br/>协议和端口号]
    PORT_MATCH_IN -->|匹配| ALLOW_TRAFFIC[**✅ 允许流量**]
    
    EGRESS_CHECK --> TO_MATCH{**to匹配？**}
    TO_MATCH -->|不匹配| CHECK_LABELS
    TO_MATCH -->|匹配| PORT_MATCH_EG{**端口匹配？**}
    
    PORT_MATCH_EG -->|不匹配| CHECK_PORTS
    PORT_MATCH_EG -->|匹配| ALLOW_TRAFFIC
    
    CHECK_LABELS --> DNS_CHECK{**DNS解析正常？**}
    CHECK_PORTS --> DNS_CHECK
    
    DNS_CHECK -->|异常| FIX_DNS[**🔧 修复DNS策略**<br/>允许kube-system访问]
    DNS_CHECK -->|正常| PLUGIN_LOGS[**🔍 查看CNI插件日志**]
    
    PLUGIN_LOGS --> POLICY_DEBUG[**🔍 策略调试**<br/>kubectl describe networkpolicy]
    POLICY_DEBUG --> ALLOW_TRAFFIC
    
    FIX_DNS --> ALLOW_TRAFFIC
    CNI_UPGRADE --> ALLOW_TRAFFIC
    
    ALLOW_TRAFFIC --> END([**问题解决**])
    
    style START fill:#e6f3ff,stroke:#0066cc,stroke-width:2px
    style ALLOW_TRAFFIC fill:#90EE90,stroke:#006400,stroke-width:3px
    style CNI_UPGRADE fill:#FFB6C1,stroke:#DC143C,stroke-width:2px
    style CHECK_LABELS fill:#FFFFE0,stroke:#DAA520,stroke-width:2px
    style CHECK_PORTS fill:#FFFFE0,stroke:#DAA520,stroke-width:2px
```

### 2. 调试和验证工具

#### **连通性测试脚本**

```bash
#!/bin/bash
# NetworkPolicy 连通性测试

# 测试函数
test_connection() {
    local from_pod=$1
    local to_pod=$2  
    local port=$3
    local expected=$4
    
    echo "测试 $from_pod -> $to_pod:$port"
    
    # 执行连接测试
    result=$(kubectl exec $from_pod -- nc -z -w 3 $to_pod $port 2>&1)
    
    if [[ $? -eq 0 ]]; then
        echo "✅ 连接成功"
        [[ "$expected" == "allow" ]] && echo "  符合预期" || echo "  ⚠️  意外成功"
    else
        echo "❌ 连接失败"
        [[ "$expected" == "deny" ]] && echo "  符合预期" || echo "  ⚠️  意外失败"
    fi
}

# 测试场景
test_connection "frontend-pod" "api-service" "8080" "allow"
test_connection "frontend-pod" "database-service" "3306" "deny"
test_connection "api-pod" "database-service" "3306" "allow"
```

#### **策略验证命令**

```bash
# 查看 NetworkPolicy 详情
kubectl describe networkpolicy <policy-name> -n <namespace>

# 查看 Pod 标签
kubectl get pods --show-labels -n <namespace>

# 查看命名空间标签  
kubectl get namespaces --show-labels

# 检查 CNI 插件状态
kubectl get pods -n kube-system | grep -E "(calico|cilium|weave)"

# 查看 NetworkPolicy 事件
kubectl get events -n <namespace> | grep NetworkPolicy
```

---

## 总结

NetworkPolicy 是 Kubernetes 中实现网络安全隔离的重要机制，它通过声明式的规则定义，为集群提供了细粒度的网络访问控制能力。NetworkPolicy 的成功实施需要 CNI 插件的支持，并且需要合理的策略设计和持续的监控维护。

### 核心价值

1. **安全隔离**：提供命名空间和 Pod 级别的网络隔离
2. **细粒度控制**：支持基于标签、端口、协议的精确控制
3. **声明式管理**：通过 YAML 配置实现策略即代码
4. **多租户支持**：为多租户集群提供网络安全边界
5. **合规支持**：满足企业安全合规要求

### 技术特点

- **标签驱动**：基于 Kubernetes 标签系统的灵活选择机制
- **双向控制**：同时支持入站和出站流量控制
- **策略叠加**：支持多个策略的组合生效
- **CNI 集成**：与主流 CNI 插件深度集成
- **性能优化**：通过内核级别的流量过滤保证高性能

NetworkPolicy 的正确使用能够显著提升 Kubernetes 集群的安全性，但需要注意选择支持的 CNI 插件，合理设计策略规则，并建立完善的测试和监控体系。在微服务架构和多租户场景中，NetworkPolicy 是不可或缺的安全组件。

