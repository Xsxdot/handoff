// managed.go —— 判断当前进程是不是被进程管理器拉起的。
//
// 职责：
//   - IsManaged：systemd / launchd 托管判据，fail-closed
//
// 边界：
//   - 只读环境变量，不看进程树、不读 /proc、不执行任何命令
//   - **绝不用 PPID**：理由见 IsManaged 的注释，这是整条防线最容易被打穿的地方
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
