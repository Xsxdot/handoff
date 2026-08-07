// handoff 程序入口：把命令执行交给 cmd 包。
package main

import "github.com/xushixin/handoff/cmd"

func main() {
	cmd.Execute()
}
