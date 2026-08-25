// Package schedclient 是编制域的出站 client 缝：本域需要的持久化能力接口
// 定义在使用方这一侧（架构法第九条「接口归使用方」），具体实现由组装点绑定
// （internal/agentd/server.go 的 SetupAutomation），组装点之外不得 new 他方
// 具体类型（架构法第八条）。
//
// 接口刻意字节化：Put/Get 只收发序列化好的 JSON——编制域不 import 账本类型，
// 账本的实体形状演进不牵动本缝（B156.3 契约拍板记录①）。测试缝：in-memory
// 假实现供编制域在不起账本的情况下独立测试。
package schedclient

import "errors"

// ErrCASConflict 表示写入/删除时的期望版本与当前版本不符。组装点的适配器
// 负责把底层（账本）的同义错误翻译成本哨兵。
var ErrCASConflict = errors.New("schedclient: 注册表版本冲突")

// ErrNotFound 表示读取的实体不存在。组装点的适配器负责翻译底层同义错误。
var ErrNotFound = errors.New("schedclient: 注册表实体不存在")

// Record 是注册表里一条版本化实体：Body 是调用方序列化好的 JSON，
// Seq 是全局单调写入序（出队 FIFO 依据）。
type Record struct {
	ID      string
	Version int
	Seq     int64
	Body    []byte
}

// Registry 是载体/小队/点火队列/并发计数的持久化缝。expectVersion 必须等于
// 当前版本，0 表示必须不存在（新建）；冲突返回 ErrCASConflict。
type Registry interface {
	Put(kind, id string, expectVersion int, body []byte, actor string) (version int, err error)
	Get(kind, id string) (Record, error)
	List(kind string) ([]Record, error)
	Delete(kind, id string, expectVersion int, actor string) error
}
