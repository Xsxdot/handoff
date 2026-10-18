// session_resident.go —— `handoff session sit/stand`：会话房间驻场者的登记与请下（B435 R，D5）。
//
// 职责：CLI 半边——解析 --as（驻场身份，Q5 甲必填）与 --desktop-session（O2 门铃信的收件
// 前提，C-406/C-409），桌面端地址的 cli_session 从当前会话出示（Q9 甲：复用 currentSeatIdentity，
// 只认 claude 物种）、machine 写本机 hostname（N3：与 scheduling.IsLocalMachine 同一取值来源），
// 然后经 collab.Service.SitResident/StandResident 唯一入口写账本。stdout：sit 一行
// proto.SessionResident JSON（C-406 两种键集），stand 一行 {"ok":true}（join/leave 先例）。
// 边界：不做匹配判定、不写信、不碰卡席位；记法校验在 collab（本文件不重复）。桌面端形状与
// 信箱键校验在本文件自写，不引用 cmd/card_seat_desktop.go（B435-X 整文件删除）。
// B435 O1：`sit --project <p>` 先经 collab.Service.EnsureProjectRoom 取或建项目房间，再走同一 SitResident
// 入口；会话号位置参数与 --project 互斥、必有其一（O1 plan Q4 甲），不绑定既有会话。
// 失败一律非 0 且 ≠124（cobra 返回错误 → 退出 1），stderr 给可行动的下一条命令。
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

var (
	sessionSitAs             string
	sessionSitDesktopSession string
	sessionSitProject        string
	sessionStandAs           string
)

var sessionSitCmd = &cobra.Command{
	Use:   "sit (<session> | --project <project>)",
	Short: "登记为会话房间驻场者（一房间至多一名）：--as 驻场身份必填；--project 坐进项目房间（不存在则创建）；--desktop-session 登记桌面端地址",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project := strings.TrimSpace(sessionSitProject)
		byProject := cmd.Flags().Changed("project")
		// O1 plan Q4 甲：位置参数与 --project 互斥、必有其一；判定在开账本之前（被拒零写入，项目房间也不建）。
		switch {
		case byProject && len(args) == 1:
			return fmt.Errorf("会话号与 --project 只能给一个：坐进项目房间用 handoff session sit --project %s --as <身份>", project)
		case !byProject && len(args) == 0:
			return fmt.Errorf("缺会话：handoff session sit <session> --as <身份>，或坐进项目房间 handoff session sit --project <项目> --as <身份>")
		}
		session := ""
		if len(args) == 1 {
			session = strings.TrimSpace(args[0])
		}
		identity := strings.TrimSpace(sessionSitAs)
		if identity == "" {
			return fmt.Errorf("--as 必填：驻场身份用统一记法 user:<名字> 或 agent:<名字>（agent 身份可用 session send --agent 回房间）")
		}
		resident := proto.SessionResident{Session: session, Identity: identity}
		if cmd.Flags().Changed("desktop-session") {
			cliSession, machine, err := residentDesktopAddress(sessionSitDesktopSession)
			if err != nil {
				slog.Default().Warn("CLI 驻场登记被拒：桌面端地址不可用", "session", session, "identity", identity, "cause", err)
				return err
			}
			resident.DesktopSession = sessionSitDesktopSession
			resident.CLISession = cliSession
			resident.Machine = machine
		}
		slog.Default().Info("CLI 驻场登记入口", "session", session, "project", project, "identity", identity,
			"has_desktop", resident.DesktopSession != "")
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		if byProject {
			// 先确保项目房间（不存在则创建，与 project room 同一入口），再走 R 的 SitResident 同一入口。
			room, created, err := svc.EnsureProjectRoom(project, ledgerActor())
			if err != nil {
				slog.Default().Warn("CLI 驻场登记失败：确保项目房间", "project", project, "identity", identity, "cause", err)
				return fmt.Errorf("session sit --project %s: %w", project, err)
			}
			if created {
				fmt.Fprintf(cmd.ErrOrStderr(), "已创建项目房间 %s（项目 %s）\n", room.ID, project)
			}
			session = room.ID
			resident.Session = session
		}
		got, err := svc.SitResident(resident)
		if err != nil {
			slog.Default().Warn("CLI 驻场登记失败", "session", session, "identity", identity, "cause", err)
			if errors.Is(err, collab.ErrResidentOccupied) {
				return fmt.Errorf("会话 %s 已有驻场者%s，一房间至多一名；要换人先请下：handoff session stand %s --as %s",
					session, residentNameHint(st, session), session, residentIdentityOr(st, session, "<现驻场者>"))
			}
			return fmt.Errorf("session sit %s: %w", session, err)
		}
		slog.Default().Info("CLI 驻场者已登记", "session", got.Session, "identity", got.Identity,
			"has_desktop", got.DesktopSession != "")
		return json.NewEncoder(cmd.OutOrStdout()).Encode(got)
	},
}

var sessionStandCmd = &cobra.Command{
	Use:   "stand <session>",
	Short: "请下会话房间驻场者：--as 须等于现驻场者（卡席位不受影响）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		session := strings.TrimSpace(args[0])
		identity := strings.TrimSpace(sessionStandAs)
		if identity == "" {
			return fmt.Errorf("--as 必填：须等于该会话的现驻场者（handoff session detail %s 可查）", session)
		}
		slog.Default().Info("CLI 请下驻场者入口", "session", session, "identity", identity)
		svc, st, err := openRoomService()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := svc.StandResident(session, identity); err != nil {
			slog.Default().Warn("CLI 请下驻场者失败", "session", session, "identity", identity, "cause", err)
			if errors.Is(err, collab.ErrResidentNotSeated) {
				if current := residentIdentityOr(st, session, ""); current != "" {
					return fmt.Errorf("%s 不是会话 %s 的驻场者（现驻场者 %s）；请用 handoff session stand %s --as %s",
						identity, session, current, session, current)
				}
				return fmt.Errorf("会话 %s 当前无驻场者，无需请下", session)
			}
			return fmt.Errorf("session stand %s: %w", session, err)
		}
		slog.Default().Info("CLI 驻场者已请下", "session", session, "identity", identity)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]bool{"ok": true})
	},
}

// residentDesktopAddress 为 --desktop-session 补齐信箱键与机器名（Q9 甲、N3）。
// 参数：desktopSession 为 --desktop-session 原值（不裁剪）。返回：cliSession = 当前 Claude Code
// 会话 id（信箱文件名键，C-404），machine = os.Hostname()。错误（均在任何写入之前）：桌面端
// 会话 id 形状非法；当前会话无法出示或不是 claude 物种（桌面端信箱只有 Claude Code 的 mod 取）；
// 会话 id 不能作信箱文件名；取不到 hostname。
func residentDesktopAddress(desktopSession string) (cliSession, machine string, err error) {
	if err := proto.ValidateDesktopSessionID(desktopSession); err != nil {
		return "", "", fmt.Errorf("--desktop-session 形状非法: %w", err)
	}
	identity, err := currentSeatIdentity("", "")
	if err != nil {
		return "", "", fmt.Errorf("--desktop-session 需要在该桌面端的 Claude Code 会话里执行（取信箱键）: %w", err)
	}
	cli, sid, err := proto.ParseSeatIdentity(identity)
	if err != nil {
		return "", "", fmt.Errorf("解析当前会话身份: %w", err)
	}
	if cli != "claude" {
		return "", "", fmt.Errorf("--desktop-session 只认 Claude Code 会话（当前会话物种 %s）；请在桌面端的 Claude Code 会话里执行，或去掉 --desktop-session 只靠 session wait 收件", cli)
	}
	if err := residentMailboxKeyOK(sid); err != nil {
		return "", "", err
	}
	host, err := os.Hostname()
	if err != nil {
		return "", "", fmt.Errorf("取本机 hostname 作驻场者桌面端机器名失败: %w", err)
	}
	return sid, host, nil
}

// residentMailboxKeyOK 校验 CLI 会话 id 能否原样作信箱文件名前缀（C-404：`<CLISession>.<纳秒>.md`）：
// 不得为空、含路径分隔符 / \ 或席位分隔符 #、含空白、以 . 开头。
func residentMailboxKeyOK(cliSession string) error {
	if cliSession == "" || strings.ContainsAny(cliSession, "/\\#") ||
		strings.IndexFunc(cliSession, unicode.IsSpace) >= 0 || strings.HasPrefix(cliSession, ".") {
		return fmt.Errorf("当前 Claude Code 会话 id 不可作信箱文件名（不得含 / \\ # 或空白，不得以 . 开头），不能登记桌面端地址")
	}
	return nil
}

// residentIdentityOr 读会话现驻场者身份，供 stderr 提示用；读不到（无驻场者或读失败）返回 fallback。
// 只服务提示文案：判定已由写面完成，这里的读失败不改变命令结果。
func residentIdentityOr(st *ledger.Store, session, fallback string) string {
	r, ok, err := st.SessionResident(session)
	if err != nil {
		slog.Default().Warn("CLI 读现驻场者（提示用）失败", "session", session, "cause", err)
		return fallback
	}
	if !ok {
		return fallback
	}
	return r.Identity
}

// residentNameHint 把现驻场者身份拼成提示片段「（现驻场者 X）」；读不到返回空串。
func residentNameHint(st *ledger.Store, session string) string {
	if current := residentIdentityOr(st, session, ""); current != "" {
		return "（现驻场者 " + current + "）"
	}
	return ""
}

func init() {
	sessionSitCmd.Flags().StringVar(&sessionSitAs, "as", "", "驻场身份（统一记法 user:<名字>/agent:<名字>，必填）")
	sessionSitCmd.Flags().StringVar(&sessionSitDesktopSession, "desktop-session", "",
		"本机 Claude 桌面端会话 id（local_…）；须在该会话的 Claude Code 里执行，用于门铃信")
	sessionSitCmd.Flags().StringVar(&sessionSitProject, "project", "",
		"坐进该项目的项目房间（不存在则创建；与会话号位置参数互斥）")
	sessionStandCmd.Flags().StringVar(&sessionStandAs, "as", "", "要请下的驻场身份（须等于现驻场者，必填）")
	sessionCmd.AddCommand(sessionSitCmd, sessionStandCmd)
}
