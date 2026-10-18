// approver.go —— handoff approver 命令族（B437）：全队共享审批链的查看与设置。
//
// 职责：
//   - show：打印当前共享审批链与版本号；未设时明说并附本地 approver 概要
//   - set -f <链文件>：校验（形状 + 设备表机器名）后 CAS 写入共享账本
//   - clear：CAS 删除共享链（各机下次裁决回落本地链）
//
// 边界：
//   - 共享链只存共享账本（ledger.dsn）；无 DSN 时 set/clear 拒绝、show 报「未设」
//     ——写本机 SQLite 的「共享链」全队不可见，属静默失效
//   - 直连账本库（与 ledgercli 同款，有意不经本机 agentd HTTP）
//   - 不校验执行器在对端是否可用（装机级事实，裁决时判）
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/spf13/cobra"
)

var (
	approverJSON      bool
	approverChainFile string
)

var approverCmd = &cobra.Command{
	Use:   "approver",
	Short: "查看/设置全队共享审批链（B437）",
	Args:  cobra.NoArgs,
}

var approverShowCmd = &cobra.Command{
	Use:   "show",
	Short: "显示当前共享审批链与版本号",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runApproverShow(cmd)
	},
}

var approverSetCmd = &cobra.Command{
	Use:   "set -f <链文件>",
	Short: "设置全队共享审批链（CAS 写入共享账本）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runApproverSet(cmd, approverChainFile)
	},
}

var approverClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "清除共享审批链（各机下次裁决回落本地链）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runApproverClear(cmd)
	},
}

func init() {
	approverShowCmd.Flags().BoolVar(&approverJSON, "json", false, "输出单行 JSON")
	approverSetCmd.Flags().StringVarP(&approverChainFile, "file", "f", "", "审批链 YAML 文件（必填）")
	approverCmd.AddCommand(approverShowCmd, approverSetCmd, approverClearCmd)
	rootCmd.AddCommand(approverCmd)
}

// requireSharedLedger 校验本机配置了共享账本 DSN；无则拒绝并给可行动指引。
func requireSharedLedger() (*config.Config, error) {
	cfg := loadCLIConfig()
	if cfg.Ledger.DSN == "" {
		return nil, errors.New("共享审批链需要 config.yaml 的 ledger.dsn（当前为空）；" +
			"写本机 SQLite 的「共享链」全队不可见，已拒绝")
	}
	return cfg, nil
}

func runApproverSet(cmd *cobra.Command, file string) error {
	if strings.TrimSpace(file) == "" {
		return errors.New("必须用 -f 指定审批链文件")
	}
	if _, err := requireSharedLedger(); err != nil {
		return err
	}
	st, err := openLedger()
	if err != nil {
		return err
	}
	defer st.Close()
	return approverSetWithStore(cmd, st, file, ledgerActor())
}

// approverSetWithStore 是 set 的核心（与 DSN 门/开库分离，便于对真实 SQLite 库测试）。
func approverSetWithStore(cmd *cobra.Command, st *ledger.Store, file, actor string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("读取审批链文件: %w", err)
	}
	chain, err := orchestration.DecodeApproverChainFile(data)
	if err != nil {
		return err
	}
	names, err := deviceNames(st)
	if err != nil {
		return err
	}
	if err := orchestration.ValidateApproverChain(chain, names); err != nil {
		return err
	}
	body, err := orchestration.EncodeApproverChain(chain)
	if err != nil {
		return err
	}
	facade := ledgerapi.New(st)
	expect := 0
	if entry, gerr := facade.Get(orchestration.ApproverChainKind, orchestration.ApproverChainID); gerr == nil {
		expect = entry.Version
	} else if !errors.Is(gerr, ledger.ErrNotFound) {
		return fmt.Errorf("读取当前共享审批链: %w", gerr)
	}
	version, err := facade.Put(orchestration.ApproverChainKind, orchestration.ApproverChainID, expect, body, actor)
	if err != nil {
		return mapApproverWriteError(expect, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "已设置共享审批链：%d 环，版本 %d\n", len(chain.Rings), version)
	return nil
}

func runApproverShow(cmd *cobra.Command) error {
	cfg := loadCLIConfig()
	if cfg.Ledger.DSN == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "未设共享链（本机无 ledger.dsn）")
		printLocalApprover(cmd, cfg.Approver)
		return nil
	}
	st, err := openLedger()
	if err != nil {
		return err
	}
	defer st.Close()
	return approverShowWithStore(cmd, st, cfg.Approver)
}

// approverShowWithStore 是 show 的核心。
func approverShowWithStore(cmd *cobra.Command, st *ledger.Store, local config.ApproverConfig) error {
	facade := ledgerapi.New(st)
	entry, err := facade.Get(orchestration.ApproverChainKind, orchestration.ApproverChainID)
	if errors.Is(err, ledger.ErrNotFound) {
		fmt.Fprintln(cmd.OutOrStdout(), "未设共享链")
		printLocalApprover(cmd, local)
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取共享审批链: %w", err)
	}
	chain, err := orchestration.DecodeApproverChain(entry.Body)
	if err != nil {
		return fmt.Errorf("解码共享审批链: %w", err)
	}
	if approverJSON {
		out := map[string]any{"version": entry.Version, "chain": chain}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "共享审批链（版本 %d，单环时限 %s）\n",
		entry.Version, orchestration.EffectiveApproverChainTimeout(chain))
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\t机器\t执行器\t模型")
	for i, ring := range chain.Rings {
		model := ring.Model
		if model == "" {
			model = "（默认）"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", i+1, ring.Machine, ring.Executor, model)
	}
	return tw.Flush()
}

func runApproverClear(cmd *cobra.Command) error {
	if _, err := requireSharedLedger(); err != nil {
		return err
	}
	st, err := openLedger()
	if err != nil {
		return err
	}
	defer st.Close()
	return approverClearWithStore(cmd, st, ledgerActor())
}

// approverClearWithStore 是 clear 的核心。
func approverClearWithStore(cmd *cobra.Command, st *ledger.Store, actor string) error {
	facade := ledgerapi.New(st)
	entry, err := facade.Get(orchestration.ApproverChainKind, orchestration.ApproverChainID)
	if errors.Is(err, ledger.ErrNotFound) {
		fmt.Fprintln(cmd.OutOrStdout(), "共享链本就未设，无需清除")
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取共享审批链: %w", err)
	}
	if err := facade.Delete(orchestration.ApproverChainKind, orchestration.ApproverChainID, entry.Version, actor); err != nil {
		if errors.Is(err, ledger.ErrCASConflict) {
			return fmt.Errorf("共享审批链版本冲突（本机版本 %d），请重读后再清: %w", entry.Version, err)
		}
		return fmt.Errorf("清除共享审批链: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "已清除共享审批链，各机下次裁决回落本地链")
	return nil
}

// mapApproverWriteError 把共享链写入错误翻成可行动文案（CAS 冲突提示重读）。
func mapApproverWriteError(expect int, err error) error {
	if errors.Is(err, ledger.ErrCASConflict) {
		return fmt.Errorf("共享审批链版本冲突（本机期望版本 %d），请重新读取后再改: %w", expect, err)
	}
	return fmt.Errorf("写入共享审批链: %w", err)
}

// deviceNames 返回设备表现有机器名（升序），供链校验注入。
func deviceNames(st *ledger.Store) ([]string, error) {
	devices, err := st.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("读设备表: %w", err)
	}
	names := make([]string, 0, len(devices))
	for _, d := range devices {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names, nil
}

// printLocalApprover 打印本地 approver 概要（可观测，不拒读）。
func printLocalApprover(cmd *cobra.Command, cfg config.ApproverConfig) {
	if len(cfg.Executor) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "本机也未配本地审批链（approver.executor 为空）")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "本机本地审批链：executor=%s model=%q timeout=%s\n",
		strings.Join(cfg.Executor, ","), cfg.Model, cfg.Timeout)
}
