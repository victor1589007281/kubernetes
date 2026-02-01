// Package main - IO CLI 客户端
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var (
	apiEndpoint string
	outputFormat string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "iocli",
		Short: "Node IO Manager CLI",
		Long:  "命令行工具用于与 Node IO Manager 交互",
	}

	// 全局标志
	rootCmd.PersistentFlags().StringVarP(&apiEndpoint, "endpoint", "e", "http://localhost:8080", "API 端点")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "输出格式 (table, json)")

	// 子命令
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(disksCmd())
	rootCmd.AddCommand(podsCmd())
	rootCmd.AddCommand(profileCmd())
	rootCmd.AddCommand(victimsCmd())
	rootCmd.AddCommand(scoresCmd())
	rootCmd.AddCommand(queueCmd())
	rootCmd.AddCommand(limitCmd())
	rootCmd.AddCommand(agentCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// statusCmd 状态命令
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看系统状态",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := httpGet("/api/v1/health")
			if err != nil {
				return err
			}
			fmt.Println("状态: 健康")
			fmt.Println(resp)
			return nil
		},
	}
}

// disksCmd 磁盘命令
func disksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disks",
		Short: "查看磁盘 IO 统计",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := httpGet("/api/v1/collect/disks")
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				fmt.Println(resp)
				return nil
			}

			var disks map[string]interface{}
			if err := json.Unmarshal([]byte(resp), &disks); err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "设备\t读IOPS\t写IOPS\t读带宽\t写带宽\t利用率\t队列深度")
			fmt.Fprintln(w, "----\t------\t------\t------\t------\t------\t--------")

			for device, data := range disks {
				d := data.(map[string]interface{})
				fmt.Fprintf(w, "%s\t%.0f\t%.0f\t%.2f MB/s\t%.2f MB/s\t%.1f%%\t%.2f\n",
					device,
					d["ReadIOPS"],
					d["WriteIOPS"],
					d["ReadBytesPerSec"].(float64)/1024/1024,
					d["WriteBytesPerSec"].(float64)/1024/1024,
					d["Utilization"],
					d["AvgQueueDepth"],
				)
			}
			w.Flush()
			return nil
		},
	}
}

// podsCmd Pod 命令
func podsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pods",
		Short: "查看 Pod IO 统计",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := httpGet("/api/v1/collect/pods")
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				fmt.Println(resp)
				return nil
			}

			var pods map[string]interface{}
			if err := json.Unmarshal([]byte(resp), &pods); err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "命名空间\tPod名称\tIOPS\t带宽\tIOPS占比\t限流")
			fmt.Fprintln(w, "--------\t-------\t----\t----\t--------\t----")

			for _, data := range pods {
				p := data.(map[string]interface{})
				throttled := "否"
				if p["IsThrottled"].(bool) {
					throttled = "是"
				}
				fmt.Fprintf(w, "%s\t%s\t%.0f\t%.2f MB/s\t%.1f%%\t%s\n",
					p["Namespace"],
					p["PodName"],
					p["TotalIOPS"],
					p["TotalBPS"].(float64)/1024/1024,
					p["IOPSPercent"],
					throttled,
				)
			}
			w.Flush()
			return nil
		},
	}
	return cmd
}

// profileCmd 画像命令
func profileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile [namespace/pod]",
		Short: "查看 Pod IO 画像",
		RunE: func(cmd *cobra.Command, args []string) error {
			var url string
			if len(args) > 0 {
				parts := strings.Split(args[0], "/")
				if len(parts) != 2 {
					return fmt.Errorf("格式应为: namespace/pod")
				}
				url = fmt.Sprintf("/api/v1/profile/pod/%s/%s", parts[0], parts[1])
			} else {
				url = "/api/v1/profile/pods"
			}

			resp, err := httpGet(url)
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				fmt.Println(resp)
				return nil
			}

			// 简化输出
			fmt.Println(resp)
			return nil
		},
	}
	return cmd
}

// victimsCmd 受害者命令
func victimsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "victims",
		Short: "查看受害者 Pod 列表",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := httpGet("/api/v1/analyze/victims")
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				fmt.Println(resp)
				return nil
			}

			var victims []interface{}
			if err := json.Unmarshal([]byte(resp), &victims); err != nil {
				return err
			}

			if len(victims) == 0 {
				fmt.Println("未检测到受害者 Pod")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "命名空间\tPod名称\t评分\t严重程度\t原因")
			fmt.Fprintln(w, "--------\t-------\t----\t--------\t----")

			for _, data := range victims {
				v := data.(map[string]interface{})
				reasons := v["Reasons"].([]interface{})
				reasonStrs := make([]string, 0)
				for _, r := range reasons {
					reason := r.(map[string]interface{})
					reasonStrs = append(reasonStrs, reason["Type"].(string))
				}

				fmt.Fprintf(w, "%s\t%s\t%.1f\t%s\t%s\n",
					v["Namespace"],
					v["PodName"],
					v["Score"],
					v["Severity"],
					strings.Join(reasonStrs, ", "),
				)
			}
			w.Flush()
			return nil
		},
	}
}

// scoresCmd 评分命令
func scoresCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scores",
		Short: "查看 Pod 操作评分",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := httpGet("/api/v1/scoring/pods")
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				fmt.Println(resp)
				return nil
			}

			var scores []interface{}
			if err := json.Unmarshal([]byte(resp), &scores); err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "命名空间\tPod名称\t最终评分\t业务\t历史\t效果\t影响\t推荐操作")
			fmt.Fprintln(w, "--------\t-------\t--------\t----\t----\t----\t----\t--------")

			for _, data := range scores {
				s := data.(map[string]interface{})
				fmt.Fprintf(w, "%s\t%s\t%.1f\t%.1f\t%.1f\t%.1f\t%.1f\t%s\n",
					s["Namespace"],
					s["PodName"],
					s["FinalScore"],
					s["BusinessScore"],
					s["HistoryScore"],
					s["EffectScore"],
					s["ImpactScore"],
					s["RecommendedAction"],
				)
			}
			w.Flush()
			return nil
		},
	}
}

// queueCmd 队列命令
func queueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "查看决策队列",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出队列项",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := httpGet("/api/v1/queue")
			if err != nil {
				return err
			}

			if outputFormat == "json" {
				fmt.Println(resp)
				return nil
			}

			var items []interface{}
			if err := json.Unmarshal([]byte(resp), &items); err != nil {
				return err
			}

			if len(items) == 0 {
				fmt.Println("队列为空")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\t状态\t优先级\tPod\t操作\t入队时间")
			fmt.Fprintln(w, "--\t----\t------\t---\t----\t--------")

			for _, data := range items {
				item := data.(map[string]interface{})
				score := item["PodScore"].(map[string]interface{})
				action := item["Action"].(map[string]interface{})

				enqueueTime, _ := time.Parse(time.RFC3339, item["EnqueuedAt"].(string))

				fmt.Fprintf(w, "%s\t%s\t%d\t%s/%s\t%s\t%s\n",
					item["ID"],
					item["Status"],
					int(item["Priority"].(float64)),
					score["Namespace"],
					score["PodName"],
					action["Type"],
					enqueueTime.Format("15:04:05"),
				)
			}
			w.Flush()
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "cancel [id]",
		Short: "取消队列项",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := httpPost(fmt.Sprintf("/api/v1/queue/%s/cancel", args[0]), nil)
			if err != nil {
				return err
			}
			fmt.Println("已取消")
			return nil
		},
	})

	return cmd
}

// limitCmd 限制命令
func limitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "limit",
		Short: "IO 限制操作",
	}

	var readIOPS, writeIOPS int64

	setCmd := &cobra.Command{
		Use:   "set [namespace/pod]",
		Short: "设置 IO 限制",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[0], "/")
			if len(parts) != 2 {
				return fmt.Errorf("格式应为: namespace/pod")
			}

			body := map[string]interface{}{
				"namespace": parts[0],
				"podName":   parts[1],
				"readIOPS":  readIOPS,
				"writeIOPS": writeIOPS,
			}

			_, err := httpPost("/api/v1/toolbox/limit-io", body)
			if err != nil {
				return err
			}
			fmt.Printf("已对 %s 设置 IO 限制: riops=%d wiops=%d\n", args[0], readIOPS, writeIOPS)
			return nil
		},
	}
	setCmd.Flags().Int64Var(&readIOPS, "riops", 1000, "读 IOPS 限制")
	setCmd.Flags().Int64Var(&writeIOPS, "wiops", 500, "写 IOPS 限制")
	cmd.AddCommand(setCmd)

	removeCmd := &cobra.Command{
		Use:   "remove [namespace/pod]",
		Short: "移除 IO 限制",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[0], "/")
			if len(parts) != 2 {
				return fmt.Errorf("格式应为: namespace/pod")
			}

			body := map[string]interface{}{
				"namespace": parts[0],
				"podName":   parts[1],
			}

			_, err := httpPost("/api/v1/toolbox/remove-limit", body)
			if err != nil {
				return err
			}
			fmt.Printf("已移除 %s 的 IO 限制\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(removeCmd)

	return cmd
}

// agentCmd AI Agent 命令
func agentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "AI Agent 操作",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "analyze [query]",
		Short: "请求 AI 分析",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			body := map[string]interface{}{
				"query": query,
			}

			resp, err := httpPost("/api/v1/agent/analyze", body)
			if err != nil {
				return err
			}
			fmt.Println(resp)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "sessions",
		Short: "列出 Agent 会话",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := httpGet("/api/v1/agent/sessions")
			if err != nil {
				return err
			}
			fmt.Println(resp)
			return nil
		},
	})

	return cmd
}

// HTTP 辅助函数

func httpGet(path string) (string, error) {
	resp, err := http.Get(apiEndpoint + path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

func httpPost(path string, data map[string]interface{}) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(apiEndpoint+path, "application/json", strings.NewReader(string(jsonData)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}
