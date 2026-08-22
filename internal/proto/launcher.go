// launcher.go —— 工作台自定义启动项的线格式（2026-08-22 需求 B 契约）。
//
// 职责：定义 Launcher 与 GET/PUT /api/launchers 的请求/响应形状。
//
// 边界：
//   - 纯类型定义：不读写文件、不做校验（校验在 internal/launcher）
//   - **不承载 env 的值**：Launcher 只记「用哪份 env 文件」，变量值从不上线格式
//     ——与 env.go 的凭据边界同源
//
// 为什么与 EnvBinding 分成两套而不是复用：EnvBinding 描述「某个 executor 用哪份
// 文件」，有 file/off 两档；启动项的 env 是「可选的一份文件」，没有档位这个概念。
// 复用会造出一个永远只有一个取值的枚举。
package proto

// Launcher 是一条自定义启动项：给终端预置一份 env 文件和/或一条启动命令。
//
// 不变式（服务端保证，客户端不得自行放宽）：**EnvFile 与 Command 至少一个非空**。
// 两者都空的启动项与「新终端」完全等价，存在本身就是一次误操作。
type Launcher struct {
	// Name 是启动项的身份，**机器内唯一**，非空。
	//
	// 刻意没有单独的 id 字段：启动项没有任何跨对象引用（点一下就开一个终端
	// tab，tab 不持有它的引用），id 唯一的用处是前端列表键，而 Name 唯一已经
	// 够用。先例是 EnvBinding 以 Executor 名为键。加一个只服务于框架细节的
	// 字段，代价是它长期被误当成稳定引用，滋生「改名不影响引用」的错觉——
	// 而改名就是换一个启动项。
	Name string `json:"name"`
	// EnvFile 是该机 <DataDir>/env 下的**纯文件名**；空 = 不注入。
	EnvFile string `json:"env_file,omitempty"`
	// Command 是启动后送进终端的命令原文；空 = 只开终端。
	//
	// 它会出现在本接口的响应里（用户要在界面上编辑它），但**绝不进日志**——
	// 命令里带 API key 是常见写法。
	Command string `json:"command,omitempty"`
	// EnvMissing 报告 EnvFile 在本机已不存在。
	//
	// **不带 omitempty**：false 是一个明确结论（「引用是好的」），缺键会让前端
	// 分不清它和「这版服务端还不认识这个字段」（与 PtySession.Foreground 同款）。
	//
	// 只在 GET 时由服务端算出；PUT 时忽略客户端送来的值。
	EnvMissing bool `json:"env_missing"`
}

// LaunchersResp 是 GET /api/launchers[?machine=] 的响应。
type LaunchersResp struct {
	Launchers []Launcher `json:"launchers"`
}

// LaunchersReq 是 PUT /api/launchers[?machine=] 的请求体：**整段替换**。
//
// 为什么整段替换而不是逐项 patch：界面就是整块保存，整段替换让「界面所见 =
// 落盘所得」无需推理。与 EnvMappingReq 同款语义。
type LaunchersReq struct {
	Launchers []Launcher `json:"launchers"`
}
