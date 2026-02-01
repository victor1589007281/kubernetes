# Node IO Manager 实现计划

> **状态: ✅ 全部完成**
> 更新时间: 2026-02-01

## 项目结构

```
node-io-manager/
├── cmd/
│   ├── manager/main.go      # DaemonSet 主进程 ✅
│   └── iocli/main.go        # CLI 客户端 ✅
├── pkg/
│   ├── collector/           # 采集器 ✅
│   │   ├── types.go
│   │   ├── collector.go
│   │   ├── disk.go
│   │   ├── process.go
│   │   ├── cgroup.go
│   │   └── ebpf.go          # eBPF 采集器 ✅
│   ├── profile/             # IO画像 ✅
│   │   ├── engine.go
│   │   └── engine_test.go
│   ├── analyzer/            # 受害者分析 ✅
│   │   ├── victim.go
│   │   └── victim_test.go
│   ├── predictor/           # 预测引擎 ✅
│   │   └── predictor.go
│   ├── scoring/             # 评分系统 ✅
│   │   ├── engine.go
│   │   ├── engine_test.go
│   │   ├── weights.go
│   │   ├── history.go
│   │   ├── simulator.go
│   │   └── business.go
│   ├── queue/               # 决策队列 ✅
│   │   └── manager.go
│   ├── observation/         # 观察期管理 ✅
│   │   └── manager.go
│   ├── toolbox/             # 工具箱 ✅
│   │   └── toolbox.go
│   ├── agent/               # AI Agent ✅
│   │   ├── core/
│   │   │   ├── agent.go
│   │   │   ├── loop.go
│   │   │   ├── provider.go
│   │   │   ├── tools.go
│   │   │   └── experts.go
│   │   ├── experts/
│   │   │   ├── node_manager.go
│   │   │   └── io_expert.go
│   │   ├── workers/
│   │   │   └── worker.go
│   │   └── provider/        # LLM Providers ✅
│   │       ├── provider.go
│   │       ├── openai.go
│   │       ├── claude.go
│   │       ├── gemini.go
│   │       ├── china_qwen.go     # 阿里云通义千问
│   │       ├── china_ernie.go    # 百度文心一言
│   │       ├── china_zhipu.go    # 智谱清言
│   │       ├── china_moonshot.go # 月之暗面
│   │       ├── china_deepseek.go # 深度求索
│   │       ├── china_others.go
│   │       └── foreign_others.go
│   ├── api/                 # REST API ✅
│   │   └── server.go
│   ├── metrics/             # Prometheus metrics ✅
│   │   └── prometheus.go
│   └── config/              # 配置管理 ✅
│       ├── config.go
│       └── provider_config.go
├── config/
│   └── provider-example.yaml # LLM Provider 配置示例 ✅
├── deploy/
│   ├── daemonset.yaml        # 基础部署清单 ✅
│   ├── business-priority.yaml
│   └── grafana-dashboard.json # Grafana Dashboard ✅
├── helm/                      # Helm Chart ✅
│   └── node-io-manager/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
│           ├── _helpers.tpl
│           ├── daemonset.yaml
│           ├── configmap.yaml
│           ├── serviceaccount.yaml
│           ├── clusterrole.yaml
│           ├── clusterrolebinding.yaml
│           ├── service.yaml
│           ├── servicemonitor.yaml
│           ├── prometheusrule.yaml
│           ├── grafana-dashboard-configmap.yaml
│           └── NOTES.txt
├── Dockerfile ✅
├── Makefile ✅
├── README.md ✅
└── go.mod ✅
```

## 里程碑与任务拆分

### Phase 1 基础框架 ✅

- [x] 架构骨架与依赖管理
- [x] 采集器 v1（磁盘、进程、cgroup）
- [x] Prometheus metrics 基础输出
- [x] REST API 基础版本

### Phase 2 分析引擎 ✅

- [x] IO画像基础实现
- [x] 受害者分析（统计学方法）
- [x] 异常检测（Z-Score/分位数）

### Phase 3 评分系统 ✅

- [x] 动态权重引擎
- [x] 业务重要性配置解析
- [x] 历史行为模型
- [x] 操作效果模拟器

### Phase 4 队列与观察期 ✅

- [x] 决策队列调度
- [x] 观察期状态机
- [x] 动态调整策略

### Phase 5 AI Agent ✅

- [x] Node Manager Agent
- [x] IO Expert Agent
- [x] Worker Agents
- [x] 多 LLM Provider 适配
  - [x] OpenAI (GPT-4, GPT-3.5)
  - [x] Anthropic Claude
  - [x] Google Gemini
  - [x] 阿里云通义千问 (Qwen)
  - [x] 百度文心一言 (Ernie)
  - [x] 智谱清言 (Zhipu)
  - [x] 月之暗面 (Moonshot/Kimi)
  - [x] 深度求索 (DeepSeek)

### Phase 6 工具箱与集成 ✅

- [x] IO 限制（cgroup v2）
- [x] 调度控制（cordon）
- [x] DaemonSet 部署清单
- [x] Grafana Dashboard
- [x] eBPF 采集器
- [x] Helm Chart

## 测试计划

| 测试类型 | 覆盖范围 | 状态 |
|:---------|:---------|:-----|
| 单元测试 | 评分与分析模块 | ✅ 已创建测试框架 |
| 集成测试 | 采集与 API | ⏳ 待实施 |
| 压力测试 | 高 IO 场景 | ⏳ 待实施 |
| 回归测试 | 自动处置 | ⏳ 待实施 |

## LLM Provider 支持列表

### 国际大模型

| Provider | 模型 | 状态 |
|:---------|:-----|:-----|
| OpenAI | GPT-4, GPT-4-turbo, GPT-3.5-turbo | ✅ |
| Anthropic | Claude 3 Opus, Sonnet, Haiku | ✅ |
| Google | Gemini 1.5 Pro, Flash | ✅ |

### 国内大模型

| Provider | 模型 | 状态 |
|:---------|:-----|:-----|
| 阿里云 | 通义千问 Qwen-max, Qwen-plus | ✅ |
| 百度 | 文心一言 ERNIE-4.0, ERNIE-3.5 | ✅ |
| 智谱 | GLM-4, GLM-3-turbo | ✅ |
| 月之暗面 | Moonshot-v1, Kimi | ✅ |
| 深度求索 | DeepSeek-V2, DeepSeek-Coder | ✅ |

## 输出物

- `node_io_manager_design.md` ✅
- `node_io_manager_scoring_system.md` ✅
- `node_io_manager_impl_plan.md` ✅
- `node-io-manager/` 完整项目代码 ✅
