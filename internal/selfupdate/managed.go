// Package selfupdate 提供「能不能安全换版 / 要不要提示更新」的判据。
//
// 职责：
//   - IsManaged：判断当前进程是不是被进程管理器（systemd / launchd）拉起的，
//     换版接口（POST /api/update）的闸二用它做硬拒绝判据
//   - CLI 侧版本检查提示（clicheck.go）：每条命令跑完后提示有没有新版本
//
// 边界：
//   - 不做下载、不做 rename、不做换版编排：那是 internal/release 与
//     handoff upgrade 命令的职责
//   - 不 import internal/agentd（会成环）
package selfupdate

// IsManaged 判断当前进程是不是被进程管理器（systemd / launchd）拉起的。
//
// 参数：
//   - getenv: 取环境变量的函数（测试注入用；生产传 os.Getenv）
//
// 返回：
//   - true 表示托管。**判不出来一律返回 false（fail-closed）**
//
// 注意：
//   - 这是「非托管则拒绝自动更新」这条防线的判据，也是整个自动更新里最
//     重要的一个判断。如果 agentd 不是被管理器拉起的，换完版 exit(0) 之后
//     没人拉起，机器上就此没有 handoff 在跑，而且没有任何信号告诉任何人
//   - **绝不能用 PPID**。手工 nohup / `zsh -c … &` 起的进程被 init 收养后
//     PPID 同样是 1，拿 PPID==1 当判据会把所有裸进程误判成托管，
//     正好把这条防线打穿
//   - XPC_SERVICE_NAME 的 `!= "0"` 是必要的额外防御：从 Finder / Terminal.app
//     启动的进程会继承 XPC_SERVICE_NAME=0（launchd 给非 XPC 服务的占位值），
//     只判「非空」会把桌面上手动跑的 agentd 误判成托管
//   - 判据取值来自 spec §7.1 的 P1 真机实测：launchd 托管时该变量等于 job
//     Label，ssh / tmux / 裸进程三种形态全为空
func IsManaged(getenv func(string) string) bool {
	// systemd：为每个 unit 调用注入唯一 id
	if getenv("INVOCATION_ID") != "" {
		return true
	}
	// launchd：注入 job Label
	if v := getenv("XPC_SERVICE_NAME"); v != "" && v != "0" {
		return true
	}
	return false
}
