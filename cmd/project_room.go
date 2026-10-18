// project_room.go —— `handoff project room <project>`：显示项目房间，不存在则创建（B435 O1，spec D6、R-K1 (a)；
// O1 plan Q5 甲 / Q11 甲 / Q15 甲）。
//
// 职责：CLI 半边——经 collab.Service.EnsureProjectRoom（项目房间创建的唯一入口，群主 = 系统发信身份）取或建
// 项目房间，再以 collab.Service.SessionDetail 的摘要头输出。stdout：恰一行 proto.SessionSummary JSON（零新
// wire 形状；驻场者身份 = members 中 kind="resident" 那一行的 identity，其他 agent 据此显式 @组织者）。
// stderr（人话）：新建时一行「已创建」并回显项目名（Q15 甲：便于发现手误）；无驻场者时给坐下命令；归档时
// 说明只读。边界：不坐下、不拉卡（项目房间不挂卡，Q12 甲）、不做名字校验以外的判定。
// 失败一律非 0 且 ≠124（cobra 返回错误 → 退出 1）。
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/proto"
)

var projectRoomCmd = &cobra.Command{
	Use:   "room <project>",
	Short: "显示项目房间（不存在则创建）：一行会话摘要 JSON，members 里 kind=resident 的即组织者身份",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project := strings.TrimSpace(args[0])
		slog.Default().Info("CLI 项目房间入口", "project", project)
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		room, created, err := svc.EnsureProjectRoom(project, ledgerActor())
		if err != nil {
			slog.Default().Warn("CLI 项目房间失败", "project", project, "cause", err)
			return fmt.Errorf("project room %s: %w", project, err)
		}
		detail, err := svc.SessionDetail(room.ID)
		if err != nil {
			slog.Default().Warn("CLI 项目房间读摘要失败", "project", project, "session", room.ID, "cause", err)
			return fmt.Errorf("读项目房间 %s 摘要: %w", room.ID, err)
		}
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(detail.Summary); err != nil {
			return err
		}
		stderr := cmd.ErrOrStderr()
		if created {
			fmt.Fprintf(stderr, "已创建项目房间 %s（项目 %s）\n", room.ID, project)
		}
		if detail.Summary.Archived {
			fmt.Fprintf(stderr, "项目房间 %s 已归档（只读）：系统通知不再写入，无法在此坐下\n", room.ID)
		}
		resident := ""
		for _, m := range detail.Summary.Members {
			if m.Kind == proto.SessionMemberResident {
				resident = m.Identity
			}
		}
		if resident == "" && !detail.Summary.Archived {
			fmt.Fprintf(stderr, "项目房间 %s 无组织者（驻场者）；坐下：handoff session sit --project %s --as <user:名字|agent:名字>\n",
				room.ID, project)
		}
		slog.Default().Info("CLI 项目房间完成", "project", project, "session", room.ID, "created", created,
			"resident", resident, "archived", detail.Summary.Archived)
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectRoomCmd)
}
