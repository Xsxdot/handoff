// discipline.go —— 控制台配置执行纪律的线格式（B157）。
//
// 职责：GET /api/discipline 与 PUT /api/discipline/mapping 的请求/响应结构。
// 边界：
//   - 文件正文的读写复用 FileRead / FileWriteReq / FileWriteResp / FileConflictResp，
//     不另造一套——那与工作树在线编辑是同一件事的同一形状
//   - 不含任何密钥字段：纪律块是纯文本指令
package proto

// DisciplineResp 是 GET /api/discipline 的响应：一次给全配置面要用的四样东西。
//
// 为什么一次给全：纪律分区要文件列表 + 内置全文，开发机详情要 executor 档位 +
// 可选文件名，同一份数据喂两处界面，不做两套接口。用户文件的**正文不在这里**
// （按需单读），内置全文只有两份、几 KB，随列表带走。
type DisciplineResp struct {
	Dir      string              `json:"dir"`      // <DataDir>/discipline 绝对路径，界面照原样显示
	Builtins []DisciplineBuiltin `json:"builtins"` // 内置两版全文，随二进制走，只读
	Files    []DisciplineFile    `json:"files"`    // 该机纪律块目录下的文件（不含正文）
	Bindings []DisciplineBinding `json:"bindings"` // 该机每个 executor 的当前档位
}

// DisciplineBuiltin 是一份内置纪律块。Tier 取 "subagent" / "single-context"。
type DisciplineBuiltin struct {
	Tier    string `json:"tier"`
	Content string `json:"content"`
}

// DisciplineFile 是纪律块目录下的一个文件。Size 是磁盘真实大小。
type DisciplineFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// DisciplineBinding 是一个 executor 的当前档位。
//
// Mode 三值：
//   - "default"：配置里没有这个键，用内置默认（DefaultTier 指出是哪版）
//   - "file"：用 File 指定的文件
//   - "off"：显式关闭注入
//
// DefaultTier 恒有值：Mode 为 default 时界面要显示「内置默认（single-context）」；
// 其余两档它是「改回默认会变成什么」的预告，同样要显示。
type DisciplineBinding struct {
	Executor    string `json:"executor"`
	Mode        string `json:"mode"`
	File        string `json:"file,omitempty"`
	DefaultTier string `json:"default_tier"`
}

// 纪律档位取值。与 config 的三档语义一一对应（键不存在 / 值为文件名 / 值为空串）。
const (
	DisciplineModeDefault = "default"
	DisciplineModeFile    = "file"
	DisciplineModeOff     = "off"
)

// DisciplineMappingReq 是 PUT /api/discipline/mapping 的请求体：**整段替换**。
//
// 为什么整段替换而不是逐项 patch：界面就是整块保存，整段替换让「界面所见 =
// 落盘所得」无需推理；逐项 patch 还要额外定义「没出现的键是保持还是删除」。
// 这条成立的前提是 GET 返回的 Bindings 是全集（注册的 adapter ∪ 配置里的键），
// 若日后有只送部分键的写入方，本语义必须重新审视。
type DisciplineMappingReq struct {
	Bindings []DisciplineBinding `json:"bindings"`
}
