// Package webui 持有 Web 控制台的静态资源，并按构建标签在两种形态间切换。
//
// 职责：
//   - 对外提供唯一入口 FS()，返回控制台静态资源的根文件系统。
//   - 通过 Embedded() 报告当前二进制是否嵌入了真实构建产物。
//
// 边界：
//   - 不做 HTTP 伺服、不判断路由、不设缓存头——那些是 internal/agentd 的事。
//   - 不负责生成产物：产物由 `npm run build` 产出到 web/dist/，再由 release
//     流水线拷进本包的 dist/ 目录。本包只负责嵌入。
//
// 为什么要两份实现：go:embed 指向不存在的目录是**编译期错误**。若无条件
// embed，任何没有先跑前端构建的机器上 `go build ./...` 与 `go test ./...`
// 都会整片失败。而把产物或占位文件提交进仓库会让 `npm run build` 之后工作区
// 变脏，与 handoff 自己「dispatch 要求工作区干净」的硬约束冲突。故用构建标签
// embedweb 分开：默认不嵌，release 才嵌。
package webui
