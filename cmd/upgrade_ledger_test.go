// B462 S3：换版前账本可读 → 换版后必须仍可读。远端经 upgrade.RemoteOne，本机经 localUpgrade
// 的「等上线 + 探针」补段；两者用 internal/upgrade 的同一判据。
package cmd

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestUpgradeRemoteLedgerNotMountedAfterUpgradeFails(t *testing.T) {
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0",
		ledger: []ledgerReply{{readable: true}, {readable: false}}}
	withStubs(t, &fakeFetcher{sum: "abc"}, peer)

	out, err := runUpgrade(t, "--now")
	if err == nil {
		t.Fatal("换版后账本未挂载应让命令退出非零")
	}
	if !strings.Contains(out, "devbox   失败   新版已上线但账本未挂载") || !strings.Contains(out, "日志") {
		t.Fatalf("应报「新版已上线但账本未挂载」并给日志处置，得:\n%s", out)
	}
	if peer.ledgerCalls != 2 {
		t.Fatalf("换版前后各探一次，得 %d", peer.ledgerCalls)
	}
}

func TestUpgradeCheckDoesNotProbeLedger(t *testing.T) {
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0",
		ledger: []ledgerReply{{readable: true}}}
	withStubs(t, &fakeFetcher{}, peer)
	if _, err := runUpgrade(t); err != nil {
		t.Fatal(err)
	}
	if peer.ledgerCalls != 0 {
		t.Fatalf("只读巡检不探账本，得 %d 次", peer.ledgerCalls)
	}
}

// localRow 按 renderCheckRow/process 的排版拼本机那一行的前缀（名字列 %-8s 按 rune 计宽）。
func localRow(rest string) string { return fmt.Sprintf("%-8s %s", "本机", rest) }

func localLedgerMachine(ledger ...ledgerReply) map[string]*fakeMachine {
	return map[string]*fakeMachine{"__本机": {
		platform: runtime.GOOS + "/" + runtime.GOARCH, version: "v0.1.0", managed: true, ledger: ledger,
	}}
}

func TestUpgradeLocalLedgerNotMountedAfterRestartFails(t *testing.T) {
	machines := localLedgerMachine(ledgerReply{readable: true}, ledgerReply{readable: false})
	out, err := runUpgradeNowErr(t, machines)
	if err == nil || !strings.Contains(out, localRow("失败   新版已上线但账本未挂载")) {
		t.Fatalf("本机换版后账本未挂载应失败，err=%v 输出:\n%s", err, out)
	}
	if m := machines["__本机"]; m.waitCalls != 1 || m.ledgerCalls != 2 {
		t.Fatalf("换版前可读时本机应等新版上线再探：wait=%d probes=%d", m.waitCalls, m.ledgerCalls)
	}
}

func TestUpgradeLocalLedgerReadableAfterRestartSucceeds(t *testing.T) {
	machines := localLedgerMachine(ledgerReply{readable: true})
	out, err := runUpgradeNowErr(t, machines)
	if err != nil || !strings.Contains(out, localRow("成功   已换到 v0.1.1")) {
		t.Fatalf("前后均可读应成功，err=%v 输出:\n%s", err, out)
	}
	if machines["__本机"].waitCalls != 1 {
		t.Fatal("换版前可读时本机应等新版上线")
	}
}

func TestUpgradeLocalWaitFailureAfterRestartFails(t *testing.T) {
	machines := localLedgerMachine(ledgerReply{readable: true})
	machines["__本机"].waitErr = errors.New("agentd 未就绪（账本未挂载：自登记失败，第 2 次重试）")
	out, err := runUpgradeNowErr(t, machines)
	if err == nil || !strings.Contains(out, localRow("失败")) || !strings.Contains(out, "自登记失败，第 2 次重试") {
		t.Fatalf("本机等上线失败应判失败并带原文，err=%v 输出:\n%s", err, out)
	}
	if machines["__本机"].ledgerCalls != 1 {
		t.Fatalf("等上线失败不得再探账本，得 %d 次", machines["__本机"].ledgerCalls)
	}
}

func TestUpgradeLocalLedgerUnknownBeforeKeepsOldBehavior(t *testing.T) {
	machines := localLedgerMachine() // 探针脚本为空 → 对端过旧 → 未知
	out, err := runUpgradeNowErr(t, machines)
	if err != nil || !strings.Contains(out, localRow("成功   已换到 v0.1.1，已触发 agentd 重启")) {
		t.Fatalf("换版前未知应保持今天的行为，err=%v 输出:\n%s", err, out)
	}
	if machines["__本机"].waitCalls != 0 {
		t.Fatal("换版前未知时本机不等待（与 B462 之前一致）")
	}
}
