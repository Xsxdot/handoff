// env.go —— 控制台配置 env 文件的线格式（B158）。
//
// 职责：GET /api/env、GET /api/env/file/keys、PUT /api/env/mapping 的请求/响应结构。
//
// 边界：
//   - 文件正文的读写复用 FileRead / FileWriteReq / FileWriteResp / FileConflictResp，
//     不另造一套——那与工作树在线编辑是同一件事的同一形状
//   - **本文件里没有任何字段承载 env 的值**：默认视图只交出 key 名与值长度，
//     全文只走 FileRead（且只在用户点「编辑正文」时）。这条是 spec §7 的凭据边界
//   - 与 DisciplineResp 同构，少了 Builtins 一节——env 没有内置默认
package proto

// EnvResp 是 GET /api/env 的响应：一次给全配置面要用的三样东西。
//
// 为什么一次给全：Env 分区要文件列表，开发机详情要 executor 档位 + 可选文件名，
// 同一份数据喂两处界面，不做两套接口。文件正文与变量清单都**不在这里**（按需单读）。
type EnvResp struct {
	Dir      string       `json:"dir"`      // <DataDir>/env 绝对路径，界面照原样显示
	Files    []EnvFile    `json:"files"`    // 该机 env 目录下的文件（不含正文）
	Bindings []EnvBinding `json:"bindings"` // 该机每个 executor 的当前档位
}

// EnvFile 是 env 目录下的一个文件。Size 是磁盘真实大小。
type EnvFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// EnvBinding 是一个 executor 的当前档位。**只有两档**：
//
//   - "off"：配置里**没有这个键** → 启动时不注入任何环境变量
//   - "file"：用 File 指定的文件
//
// 注意与 DisciplineBinding 的**错位**：discipline 的「键不存在」是「用内置默认」、
// 「空串」才是关闭；env 没有内置默认，「键不存在」就是唯一的关闭表达。落盘时
// **绝不写空串**——空串会让 Resolver 走到「读 <dir>/」这种无意义路径。
type EnvBinding struct {
	Executor string `json:"executor"`
	Mode     string `json:"mode"`
	File     string `json:"file,omitempty"`
}

// env 档位取值。与 config 的两档语义一一对应（键不存在 / 值为文件名）。
const (
	EnvModeFile = "file"
	EnvModeOff  = "off"
)

// EnvKey 是解析出的一个变量。**永不含值**——这是本设计的凭据边界所在。
//
// ValueBytes 是值的字节长度，口径是**展开后**（Parse 的产物）：它让「这个变量
// 是不是空的」可判断，而不泄露内容。注意展开用 lookup=nil，所以引用了外部变量
// 的值在这里会显示为更短甚至 0——这不是 bug，是刻意不查 agentd 自己的环境，
// 否则同一个文件在不同机器上会显示出不同的长度，既误导又多泄露一层信息。
//
// Duplicate 为真表示该键在文件里出现过多次（Resolver 的既有行为是 WARN 不拒，
// 界面照此标注、不拦保存）。
//
// **刻意没有「是否单引号字面量」这一项**：Parse 只回 Key/Value，不暴露引号风格，
// 要标它就得在 handler 里重扫一遍原始行、再造一套与 Parse 可能漂移的解析。
type EnvKey struct {
	Key        string `json:"key"`
	ValueBytes int    `json:"value_bytes"`
	Duplicate  bool   `json:"duplicate,omitempty"`
}

// EnvKeysResp 是 GET /api/env/file/keys 的响应。
type EnvKeysResp struct {
	Keys []EnvKey `json:"keys"`
}

// EnvMappingReq 是 PUT /api/env/mapping 的请求体：**整段替换**。
//
// 为什么整段替换而不是逐项 patch：界面就是整块保存，整段替换让「界面所见 =
// 落盘所得」无需推理。这条成立的前提是 GET 返回的 Bindings 是全集（注册的
// adapter ∪ 配置里的键），若日后有只送部分键的写入方，本语义必须重新审视。
type EnvMappingReq struct {
	Bindings []EnvBinding `json:"bindings"`
}
