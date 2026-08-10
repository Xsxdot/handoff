// 本文件实现 handoff status 子命令：一条命令回答「这个 agentd 能不能用、是什么」。
//
// 职责：
//   - 调 client.Status 取服务端聚合结果，渲染人读文本（默认）或 JSON（--json）
//   - 把老 agentd 的 404 直译成一条**成功的**诊断结论
//   - 退出码只回答「能不能用」：0=可达且鉴权通过，1=够不着
//
// 边界：
//   - 不做探活：判据在各 adapter 里，服务端已经做完，本层只渲染
//   - 不因两边版本不一致而阻断：handoff 没有兼容矩阵，revision 不同不等于
//     不兼容，并列报出交给人判
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// statusJSONOut 对应 --json。
var statusJSONOut bool

// statusJSON 是 --json 的线格式。
//
// reachable 与退出码同源（都回答「能不能用」），脚本读哪个都行；
// degraded=true 表示对端是老 agentd，此时 agentd 字段为 null。
type statusJSON struct {
	Reachable bool              `json:"reachable"`
	Degraded  bool              `json:"degraded"`
	CLI       proto.BuildInfo   `json:"cli"`
	Agentd    *proto.StatusResp `json:"agentd"`
}

// statusCmd 查询 agentd 的可用性与身份。
//
// 使用方式：handoff status [--target <名字>] [--json]
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看 agentd 是否可用及其版本/数据目录/任务概况",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		cliVer, _ := buildinfo.Read()
		out := cmd.OutOrStdout()

		st, err := client.New(addr, token).Status(cmd.Context())
		switch {
		case errors.Is(err, client.ErrStatusUnsupported):
			// 老 agentd：能收到 404 已经证明了 TCP 通、HTTP 正常、Bearer 过，
			// 这是一条成功的诊断，不是失败
			if statusJSONOut {
				return json.NewEncoder(out).Encode(statusJSON{
					Reachable: true, Degraded: true, CLI: cliVer})
			}
			renderDegraded(out, addr)
			return nil
		case err != nil:
			return err
		}

		if statusJSONOut {
			return json.NewEncoder(out).Encode(statusJSON{
				Reachable: true, CLI: cliVer, Agentd: st})
		}
		renderStatus(out, addr, cliVer, st)
		return nil
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSONOut, "json", false, "以 JSON 输出（reachable 与退出码同源）")
	rootCmd.AddCommand(statusCmd)
}

// renderDegraded 渲染老 agentd 的降级结论。
//
// 措辞刻意不写成失败：在版本错配这个场景里「远端过旧」正是要的答案。
// 也不写「该端点自 xxx 版本引入」——CLI 无从知道对端版本，编一个引入点就是编造。
func renderDegraded(w io.Writer, addr string) {
	fmt.Fprintf(w, "agentd   %s   可用（版本过旧）\n", addr)
	fmt.Fprintln(w, "已确认   TCP 可达 · HTTP 正常 · Bearer 鉴权通过")
	fmt.Fprintln(w, "限制     该 agentd 不支持 /api/status，详情不可得")
	fmt.Fprintln(w, "处置     升级远端 agentd 后重试")
}

// renderStatus 渲染完整状态。
func renderStatus(w io.Writer, addr string, cli proto.BuildInfo, st *proto.StatusResp) {
	fmt.Fprintf(w, "agentd   %s   可用\n", addr)
	fmt.Fprintf(w, "版本     %s\n", describeBuild(st.Version))
	fmt.Fprintf(w, "本地     %s\n", compareBuild(cli, st.Version))
	fmt.Fprintf(w, "数据     %s   已运行 %s\n", st.DataDir, humanUptime(st.StartedAt))
	fmt.Fprintf(w, "执行者   %s\n", strings.Join(markDefault(st.Executors, st.DefaultExecutor), "  "))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "任务     %s\n", renderCounts(st.TaskCounts))
	if len(st.Active) == 0 {
		return
	}
	fmt.Fprintln(w, "活跃")
	for _, a := range st.Active {
		fmt.Fprintf(w, "  %s  %s  %s  %s  %s\n",
			short8(a.ID), a.Name, a.State, a.Executor, liveText(a))
	}
}

// describeBuild 把一个构建标识渲染成一行。
//
// Revision 为空表示不是 go build 产物（go run / 测试二进制），如实说明而不是留空。
func describeBuild(b proto.BuildInfo) string {
	if b.Revision == "" {
		return fmt.Sprintf("未知（非 go build 产物）  %s", b.Go)
	}
	s := fmt.Sprintf("%s  %s  %s", short12(b.Revision), b.Time, b.Go)
	if b.Modified {
		// 带未提交改动意味着这个二进制对不上任何一个提交，排障时是关键信息
		s += "  带未提交改动"
	}
	return s
}

// compareBuild 渲染「本地」行：两边 revision 的对照结论。
//
// 不一致**不阻断**：handoff 没有兼容矩阵，revision 不同不等于不兼容，
// 该不该继续交给人判。
func compareBuild(cli, agentd proto.BuildInfo) string {
	if cli.Revision == "" {
		return "本地版本未知（非 go build 产物）"
	}
	s := short12(cli.Revision)
	if cli.Modified {
		s += "  带未提交改动"
	}
	if agentd.Revision == "" {
		return s + "  （对端版本未知，无从对照）"
	}
	if cli.Revision == agentd.Revision {
		return s + "  一致"
	}
	return s + "  与对端不一致（不一定不兼容，请自行判断）"
}

// humanUptime 把启动时刻换算成「已运行多久」。
//
// 零值时刻（老响应或字段缺失）返回「未知」，不显示一个荒谬的天数。
func humanUptime(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "未知"
	}
	d := time.Since(startedAt)
	if d < 0 {
		// 两机时钟不同步时会为负，如实说明而不是显示负数
		return "未知（对端时钟与本机不同步）"
	}
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// markDefault 给缺省执行者加标注。
func markDefault(names []string, def string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == def {
			out = append(out, n+"(缺省)")
			continue
		}
		out = append(out, n)
	}
	return out
}

// renderCounts 渲染任务计数，**只列非零的状态**。
//
// why（只列非零）：六个状态里常年有四个是 0，全列会把真正的结论淹掉。
// JSON 侧不做这个省略——人看的要短，机器读的要齐。
func renderCounts(counts map[string]int) string {
	order := []string{"pending", "running", "waiting_answer", "waiting_review", "completed", "failed"}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, n))
		}
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, " · ")
}

// liveText 把存活结论渲染成一句人话。
func liveText(a proto.ActiveTask) string {
	switch a.Live {
	case proto.LiveAlive:
		return "executor 存活"
	case proto.LiveDead:
		return fmt.Sprintf("executor 已不在（%s）", a.Note)
	default:
		return fmt.Sprintf("存活性未知（%s）", a.Note)
	}
}

// short8 取 id 前 8 位用于展示（与 tmux 会话命名 handoff-<id8> 一致，便于人肉对照）。
//
// 注意：只用于展示。任何拿去当参数的地方都必须用完整 UUID——store.GetTask 是
// 精确匹配，不做前缀查找。
func short8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// short12 取 revision 前 12 位（git 惯用短 hash 长度）。
func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
