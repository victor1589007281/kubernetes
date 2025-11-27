# Logical Memory 超售系统使用指南

## 项目结构

```
vdocs/design/logical-resource/
├── README.md                    # 设计文档
├── USAGE.md                     # 使用指南（本文件）
├── go.mod                       # Go 模块定义
├── cmd/
│   └── main.go                  # 主程序入口
└── pkg/
    ├── types/
    │   └── types.go             # 公共类型定义
    ├── predictor/
    │   ├── predictor.go         # 内存预测器
    │   ├── holt_winters.go      # Holt-Winters 算法
    │   ├── trend.go             # 趋势分析
    │   └── predictor_test.go    # 单元测试
    ├── oversell/
    │   ├── manager.go           # 超售管理器
    │   ├── calculator.go        # 比例计算器
    │   ├── cgroup.go            # cgroup 操作
    │   └── manager_test.go      # 单元测试
    └── monitor/
        ├── monitor.go           # 监控器
        ├── alerter.go           # 告警器
        ├── adjuster.go          # 自动调整器
        └── monitor_test.go      # 单元测试
```

## 快速开始

### 1. 编译

```bash
cd vdocs/design/logical-resource
go mod tidy
go build -o logical-memory-oversell ./cmd/main.go
```

### 2. 运行

```bash
# 使用默认配置运行
./logical-memory-oversell

# 使用自定义配置文件运行
./logical-memory-oversell -config /path/to/config.json
```

### 3. 运行测试

```bash
# 运行所有测试
go test ./...

# 运行带覆盖率的测试
go test -cover ./...

# 运行特定包的测试
go test ./pkg/predictor/...
go test ./pkg/oversell/...
go test ./pkg/monitor/...

# 运行基准测试
go test -bench=. ./...
```

## 配置文件

创建 `config.json`：

```json
{
  "log_level": "info",
  "http_port": 8080,
  "metrics_port": 9090,
  "predictor": {
    "history_days": 14,
    "forecast_days": 3,
    "alpha": 0.3,
    "beta": 0.1,
    "gamma": 0.2,
    "seasonal_period": 24
  },
  "oversell": {
    "max_ratio": 2.0,
    "safety_factor": 0.85,
    "min_ratio": 1.0,
    "adjustment_step": 0.05
  },
  "monitor": {
    "collect_interval": "10s",
    "alert_thresholds": {
      "info": 0.60,
      "warning": 0.75,
      "critical": 0.85,
      "emergency": 0.95
    },
    "adjustment_cooldown": "5m",
    "enable_auto_adjust": true,
    "enable_eviction": false
  },
  "webhook_urls": [
    "http://your-alerting-system/webhook"
  ]
}
```

## API 接口

### 健康检查

```bash
# 健康状态
curl http://localhost:8080/health

# 就绪状态
curl http://localhost:8080/ready
```

### 状态查询

```bash
# 获取系统状态
curl http://localhost:8080/api/v1/status

# 获取超售状态
curl http://localhost:8080/api/v1/oversell

# 获取预测结果
curl http://localhost:8080/api/v1/predictions

# 获取告警历史
curl http://localhost:8080/api/v1/alerts

# 获取 Pod 列表
curl http://localhost:8080/api/v1/pods
```

### 数据输入

```bash
# 添加内存数据点
curl -X POST http://localhost:8080/api/v1/datapoint \
  -H "Content-Type: application/json" \
  -d '{
    "actual_usage_bytes": 21474836480,
    "buffer_pool_bytes": 68719476736,
    "pod_name": "mysql-0",
    "namespace": "database"
  }'
```

### 超售比例调整

```bash
# 设置超售比例
curl -X PUT http://localhost:8080/api/v1/oversell \
  -H "Content-Type: application/json" \
  -d '{"ratio": 1.5}'
```

### Pod 管理

```bash
# 添加 Pod 到超售管理
curl -X POST http://localhost:8080/api/v1/pods \
  -H "Content-Type: application/json" \
  -d '{
    "pod_name": "mysql-0",
    "namespace": "database",
    "memory_limit_bytes": 68719476736,
    "memory_request_bytes": 34359738368,
    "buffer_pool_bytes": 51539607552,
    "priority": 100
  }'
```

## Prometheus 指标

访问 `http://localhost:9090/metrics` 获取以下指标：

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `logical_memory_usage_ratio` | Gauge | 当前内存使用率 |
| `logical_memory_oversell_ratio` | Gauge | 当前超售比例 |
| `logical_memory_bytes` | Gauge | 逻辑内存总量（字节） |
| `logical_memory_prediction_accuracy` | Gauge | 预测准确度 (0-1) |

## 告警级别

| 级别 | 触发条件 | 自动动作 |
|------|---------|---------|
| **INFO** | 内存使用 > 60% | 记录日志 |
| **WARNING** | 内存使用 > 75% | 逐步降低超售比例 |
| **CRITICAL** | 内存使用 > 85% | 立即降低至 1.0 |
| **EMERGENCY** | 内存使用 > 95% | 禁用超售，触发驱逐 |

## 集成示例

### 与 Kubernetes 集成

1. 部署为 DaemonSet 在每个节点运行
2. 配置 RBAC 权限以访问 Pod 信息
3. 将 cgroup 路径挂载到容器中

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: logical-memory-oversell
spec:
  selector:
    matchLabels:
      app: logical-memory-oversell
  template:
    metadata:
      labels:
        app: logical-memory-oversell
    spec:
      containers:
      - name: oversell
        image: your-registry/logical-memory-oversell:latest
        volumeMounts:
        - name: cgroup
          mountPath: /sys/fs/cgroup
        - name: proc
          mountPath: /proc
          readOnly: true
        ports:
        - containerPort: 8080
        - containerPort: 9090
      volumes:
      - name: cgroup
        hostPath:
          path: /sys/fs/cgroup
      - name: proc
        hostPath:
          path: /proc
```

### 与 Prometheus 集成

```yaml
# prometheus-servicemonitor.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: logical-memory-oversell
spec:
  selector:
    matchLabels:
      app: logical-memory-oversell
  endpoints:
  - port: metrics
    interval: 15s
```

## 算法说明

### Holt-Winters 三次指数平滑

用于捕获时间序列的三个成分：
- **Level (L)**: 数据的基础水平
- **Trend (T)**: 趋势变化
- **Seasonal (S)**: 周期性模式

```
Level:    L_t = α(Y_t / S_{t-s}) + (1-α)(L_{t-1} + T_{t-1})
Trend:    T_t = β(L_t - L_{t-1}) + (1-β)T_{t-1}
Season:   S_t = γ(Y_t / L_t) + (1-γ)S_{t-s}
Forecast: F_{t+h} = L_t + h*T_t + S_{t+h-s}
```

### 超售比例计算

```
oversell_ratio = total_buffer_pool / predicted_max_usage
safe_ratio = oversell_ratio × safety_factor
final_ratio = min(safe_ratio, max_ratio)
```

## 注意事项

1. **cgroup 权限**: 运行时需要对 cgroup 文件系统有读写权限
2. **内存监控精度**: 依赖 `/proc/meminfo` 的准确性
3. **预测数据量**: 需要至少 48 小时的历史数据才能进行预测
4. **Buffer Pool 变更**: 变更后需要 72 小时的适应期
5. **安全边界**: 建议保持 15% 的安全边界（safety_factor=0.85）

## 故障排除

### 预测失败
- 检查是否有足够的历史数据（至少 2 个 seasonal period）
- 确认数据点格式正确

### 超售比例未调整
- 检查 adjustment_cooldown 设置
- 确认 enable_auto_adjust 为 true
- 查看日志确认是否触发了告警

### cgroup 操作失败
- 确认 cgroup v2 是否启用
- 检查文件系统权限
- 验证 cgroup 路径正确

