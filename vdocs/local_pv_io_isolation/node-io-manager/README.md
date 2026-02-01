# Node IO Manager

节点级别的 IO 监控和管理服务，用于解决 Kubernetes 共享 Local PV 的 IO 过载和互相影响问题。

## 功能特性

### 📊 多维度 IO 数据统计
- 磁盘级别: IOPS、带宽、延迟、利用率、队列深度
- Pod 级别: IO 占比、限流状态
- 进程级别: D 状态进程、等待通道
- 系统级别: IO wait、阻塞进程数

### 🎯 IO 画像与受害者分析
- Pod IO 行为画像 (顺序/随机 IO、读写比例、突发性)
- Z-Score 异常检测
- 受害者识别和噪声邻居定位
- IO 相关性分析

### 📈 动态评分决策系统
- 业务重要性评分 (外部配置)
- 历史行为分析 (复犯概率)
- 操作效果模拟 (最小操作最大收益)
- 动态权重自适应调整

### ⏱ 决策队列与观察期
- 优先级排序的操作队列
- 分数动态衰减
- 观察期状态机
- 升级策略

### 🤖 AI Agent 系统
- Node Manager: 协调者角色
- IO Expert: 深度分析专家
- Workers: 诊断/修复/预测执行者
- ReAct 循环推理
- 多 LLM Provider 支持 (OpenAI, Claude, Ollama)

### 🧰 工具箱
- cgroup v2 IO 限制
- 节点调度控制 (cordon/uncordon)
- 告警集成

## 架构图

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Node IO Manager                              │
├─────────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌───────────┐  │
│  │  Collector  │  │   Profile   │  │  Analyzer   │  │ Predictor │  │
│  │ (磁盘/Pod/  │  │  Engine     │  │ (受害者/    │  │ (趋势预测)│  │
│  │  进程/系统) │  │ (IO画像)   │  │  噪声邻居)  │  │           │  │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └─────┬─────┘  │
│         │                │                │               │         │
│  ┌──────▼────────────────▼────────────────▼───────────────▼──────┐  │
│  │                     Scoring Engine                            │  │
│  │  (业务重要性 + 历史行为 + 操作效果 + 当前影响) → 动态加权评分 │  │
│  └──────────────────────────┬────────────────────────────────────┘  │
│                             │                                       │
│  ┌──────────────────────────▼────────────────────────────────────┐  │
│  │                   Decision Queue                              │  │
│  │  优先级排序 → 执行 → 观察期 → 成功/升级                       │  │
│  └──────────────────────────┬────────────────────────────────────┘  │
│                             │                                       │
│  ┌──────────────────────────▼────────────────────────────────────┐  │
│  │                    AI Agent System                            │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐           │  │
│  │  │NodeManager  │  │  IO Expert  │  │   Workers   │           │  │
│  │  │ (协调者)    │  │ (分析专家) │  │ (执行者)    │           │  │
│  │  └─────────────┘  └─────────────┘  └─────────────┘           │  │
│  └──────────────────────────┬────────────────────────────────────┘  │
│                             │                                       │
│  ┌──────────────────────────▼────────────────────────────────────┐  │
│  │                       Toolbox                                 │  │
│  │  IO 限制 (cgroup) │ 调度控制 │ 告警                           │  │
│  └───────────────────────────────────────────────────────────────┘  │
├─────────────────────────────────────────────────────────────────────┤
│  API Server (:8080)  │  Prometheus Metrics (:9100)                 │
└─────────────────────────────────────────────────────────────────────┘
```

## 快速开始

### 前提条件
- Kubernetes 1.24+
- cgroup v2
- 特权容器权限

### 部署

```bash
# 构建镜像
make docker

# 部署到 Kubernetes
make deploy

# 查看状态
kubectl get pods -n node-io-manager
```

### 配置

编辑 ConfigMap 配置:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: node-io-manager-config
  namespace: node-io-manager
data:
  config.yaml: |
    collector:
      intervalSeconds: 5
    
    scoring:
      weights:
        businessImportance: 0.3
        historyBehavior: 0.2
        actionEffect: 0.3
        currentImpact: 0.2
    
    agent:
      enabled: true
      provider: openai
```

### CLI 使用

```bash
# 查看磁盘状态
iocli disks

# 查看 Pod IO
iocli pods

# 查看受害者
iocli victims

# 查看评分
iocli scores

# 设置 IO 限制
iocli limit set production/my-pod --riops=1000 --wiops=500

# AI 分析
iocli agent analyze "分析当前 IO 问题"
```

## API 接口

### 数据采集
- `GET /api/v1/collect/disks` - 磁盘统计
- `GET /api/v1/collect/pods` - Pod 统计
- `GET /api/v1/collect/processes` - 进程统计

### IO 画像
- `GET /api/v1/profile/pods` - 所有 Pod 画像
- `GET /api/v1/profile/pod/:namespace/:name` - 指定 Pod 画像

### 分析
- `GET /api/v1/analyze/victims` - 受害者列表

### 评分
- `GET /api/v1/scoring/pods` - 所有 Pod 评分
- `GET /api/v1/scoring/weights` - 当前权重
- `PUT /api/v1/scoring/weights` - 更新权重

### 队列
- `GET /api/v1/queue` - 决策队列
- `POST /api/v1/queue/:id/cancel` - 取消
- `POST /api/v1/queue/:id/execute` - 立即执行

### 工具箱
- `POST /api/v1/toolbox/limit-io` - 设置 IO 限制
- `POST /api/v1/toolbox/remove-limit` - 移除限制
- `POST /api/v1/toolbox/cordon` - 标记节点不可调度
- `POST /api/v1/toolbox/uncordon` - 取消标记

### AI Agent
- `POST /api/v1/agent/analyze` - 请求分析
- `GET /api/v1/agent/sessions` - 会话列表

## Prometheus 指标

| 指标 | 说明 |
|------|------|
| `node_io_disk_iops` | 磁盘 IOPS |
| `node_io_disk_utilization_percent` | 磁盘利用率 |
| `node_io_pod_iops` | Pod IOPS |
| `node_io_pod_percent` | Pod IO 占比 |
| `node_io_d_state_count` | D 状态进程数 |
| `node_io_pod_victim_score` | 受害者评分 |
| `node_io_pod_operation_score` | 操作评分 |
| `node_io_queue_pending_count` | 队列待处理数 |

## 业务优先级配置

```yaml
namespaces:
  kube-system:
    priority: 100
    protectionLevel: critical
  production:
    priority: 90
    protectionLevel: high

labelRules:
  - selector:
      matchLabels:
        app.kubernetes.io/tier: database
    priority: 90
    protectionLevel: high

podOverrides:
  - namespace: production
    namePattern: "mysql-master-*"
    neverThrottle: true
```

## 目录结构

```
node-io-manager/
├── cmd/
│   ├── manager/        # 主服务入口
│   └── iocli/          # CLI 客户端
├── pkg/
│   ├── collector/      # 数据采集器
│   ├── metrics/        # Prometheus 指标
│   ├── api/            # REST API
│   ├── profile/        # IO 画像引擎
│   ├── analyzer/       # 受害者分析器
│   ├── predictor/      # 预测引擎
│   ├── scoring/        # 评分系统
│   ├── queue/          # 决策队列
│   ├── observation/    # 观察期管理
│   ├── agent/          # AI Agent
│   │   ├── core/       # 核心框架
│   │   ├── experts/    # 专家 Agent
│   │   └── workers/    # Worker Agent
│   ├── toolbox/        # 工具箱
│   └── config/         # 配置管理
├── deploy/             # Kubernetes 部署清单
├── Dockerfile
├── Makefile
└── README.md
```

## 开发

```bash
# 本地构建
make build-local

# 本地运行
make run

# 运行测试
make test

# 代码检查
make lint
```

## License

Apache License 2.0
