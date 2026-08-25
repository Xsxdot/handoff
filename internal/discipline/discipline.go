// discipline.go —— 纪律块的角色名词汇表与解析产物类型。
//
// 职责：
//   - Name* 常量：工作流节点点名的角色名（implement / review / …），
//     是跨包（ledger 模板、ledgerstep、cmd 测试）共用的词汇表
//   - Block：一次纪律组装的产物（正文 + 人可读来源标注）
//
// 边界：
//   - B229 起本包不再持有任何内置正文：builtin/*.md 六份、go:embed 声明与执行器
//     能力档位轴（subagent/single-context 分版）已随本地解析一并删除——一切正文
//     从账本取，派发期经 dispatch.go#ResolveDispatch 下发，执行机收文即用，
//     不再有「猜一份」的回退阶梯
//   - 不理解纪律内容、不校验语义；不负责注入进 prompt（交各 adapter）
package discipline

// Block 是一次纪律组装的产物。
//
// Source 是人可读的来源标注（「账本:review」/「临时正文」/「内置:平台不变量」），
// 供派发时回显给协调者：配置化把纪律块从 plan 文件里拿走之后，写 plan 的人
// 再也看不见它，回显是唯一的补偿（B126-A；来源清单随 B229 收窄为账本/临时正文/
// 平台层三种）。
type Block struct {
	Text   string // 纪律块正文；空表示不注入
	Source string // 人可读来源标注；Text 为空时同为空
}

// 纪律块角色名。名字是「这一轮执行者扮演什么角色」。
//
// B229 之前的 Tier（执行器能力档位）是另一条轴，已随内置分档删除——
// implement 的 subagent/single-context 分版不再存在。
const (
	NameImplement   = "implement"    // 实现角色
	NameReview      = "review"       // 审阅角色；只读，不写
	NameSpecDraft   = "spec-draft"   // 出 spec 角色；只出文档，不写代码
	NamePlanWriting = "plan-writing" // 写 plan 角色；只出计划，不写代码
	NameFinishing   = "finishing"    // 收尾合并角色；合并目标取自卡的有效基线
)
