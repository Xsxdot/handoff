// b393_provider_config_test.go —— B393 R3.3：隔离 HOME 缺 opencode provider/model
// 定义会让 resume 失败后的重建直接 ProviderModelNotFoundError，退化链断在起点。
//
// 职责：锁 coordinator_home 供给把主 HOME 的 opencode provider 配置投影进隔离 HOME
// （缺失才写、不覆盖已有）。
// 边界：只经供给缝与投影纯函数；不启动真 opencode、不碰运行态。
//
// 内部锁声明（plan T3.2 已登记）：TestB393PrepareProjectsProviderConfig 的入口是
// 未导出纯函数 projectCoordinatorProviderConfig——从声明缝 NewCoordinatorPrepareHome
// 构造不出这条断言（Prepare 需活配置 + Profile 供给 + 规则装载三依赖），故另加缝级
// 断言 TestB393PrepareViaSupplierProjectsProviderConfig（走 supplier.Prepare 返回值）。
package orchestration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// TestB393PrepareProjectsProviderConfig 锁 R3.3：投影必须把主 HOME 的 opencode
// provider 定义复制进隔离 HOME，否则重建/首拉的回合拿不到模型。
//
// 红：今天投影函数不存在（编译红）→ 实现后目标文件存在且逐字节相等。
// 绿：T3.1 后目标文件存在且内容与主 HOME 逐字节相等；已有文件不被覆盖。
func TestB393PrepareProjectsProviderConfig(t *testing.T) {
	mainHome := t.TempDir()
	targetHome := t.TempDir()
	srcDir := filepath.Join(mainHome, ".config", "opencode")
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"plugin":["x"],"provider":{"commandcode":{}}}`)
	if err := os.WriteFile(filepath.Join(srcDir, "opencode.jsonc"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projectCoordinatorProviderConfig(mainHome, targetHome); err != nil {
		t.Fatalf("投影: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"))
	if err != nil {
		t.Fatalf("隔离 HOME 缺 provider 配置（R3.3 红）: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("provider 配置逐字节不等：\n got=%s\nwant=%s", got, body)
	}

	// 已有**真实 provider 定义**不被覆盖（隔离侧可能比主 HOME 更精确）。
	keep := []byte(`{"provider":{"isolated":{"models":{}}}}`)
	if err := os.WriteFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"), keep, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projectCoordinatorProviderConfig(mainHome, targetHome); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"))
	if string(again) != string(keep) {
		t.Fatalf("已有隔离配置被覆盖")
	}

	// 语义为空的裸壳（真机生产隔离 HOME 就是这种，2026-09-22 取证）必须被投影覆盖，
	// 否则重建继续 ProviderModelNotFoundError——「文件存在即跳过」正是本卡要修的漏。
	stub := []byte(`{"$schema":"https://opencode.ai/config.json"}`)
	if err := os.WriteFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"), stub, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projectCoordinatorProviderConfig(mainHome, targetHome); err != nil {
		t.Fatal(err)
	}
	over, _ := os.ReadFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"))
	if string(over) != string(body) {
		t.Fatalf("裸壳未被投影覆盖（重建仍会缺模型定义）：\n got=%s\nwant=%s", over, body)
	}
}

// TestB393PrepareViaSupplierProjectsProviderConfig 是缝级断言：走 supplier.Prepare
// 真实供给，隔离 HOME 里必须出现主 HOME 的 provider 配置（缺失才写）。
func TestB393PrepareViaSupplierProjectsProviderConfig(t *testing.T) {
	mainHome := t.TempDir()
	provDir := filepath.Join(mainHome, ".config", "opencode")
	if err := os.MkdirAll(provDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"provider":{"commandcode":{}}}`)
	if err := os.WriteFile(filepath.Join(provDir, "opencode.jsonc"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	targetDir := t.TempDir()
	supplier := coordinatorHomeSupplier{
		currentConfig: func() *config.Config {
			return &config.Config{Token: "agentd-live", DataDir: t.TempDir()}
		},
		userHomeDir:    func() (string, error) { return mainHome, nil },
		expandHomeDir:  func(string) (string, error) { return targetDir, nil },
		credentialPath: toolchain.CredRelPathFor,
		profileFor: func(cli string) (executor.Profile, error) {
			return opencode.New(nil).Profile(), nil
		},
		loadRules: testLoadRules,
	}
	if _, err := supplier.Prepare(keysclient.SessionSpec{CLI: "opencode", HomeDir: targetDir}); err != nil {
		t.Fatalf("supplier.Prepare: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetDir, ".config", "opencode", "opencode.jsonc"))
	if err != nil {
		t.Fatalf("供给缝未投影 provider 配置（R3.3）: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("provider 配置逐字节不等：\n got=%s\nwant=%s", got, body)
	}
}
