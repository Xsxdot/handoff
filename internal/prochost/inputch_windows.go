//go:build windows

// inputch_windows.go —— Windows 输入通道：命名管道服务端 + 中继 + 匿名管道。
//
// 职责：提供 createInputChannel / waitInputReader / writeInputChannel /
// openInputChannel 四个原语的 Windows 实现。
//
// 边界：
//   - 只搬字节，不解析内容、不按帧缓冲：claude 的 stream-json 是逐行 JSON，
//     原样抄即可
//   - 不判断执行者死活：那是存活锁的事
//
// 为什么是「匿名管道 + 中继」而不是「命名管道直接当 stdin」：
// unix 侧 shim 以 O_RDWR 打开 FIFO，图的是**永不 EOF**——agentd 每次投递开
// 写端、写完就关，子进程 stdin 不受影响。Windows 命名管道没有这个性质：
// 客户端一断开，服务端侧即 broken pipe。若把服务端句柄直接给子进程当 stdin，
// 它会在第一条指令投递完成的瞬间看到 EOF，claude 的 stream-json 输入模式当场
// 结束——症状是「执行者起来了、第一条指令也执行了，然后再也不响应」。
// 因此：匿名管道的写端由 shim 全程持有（这才是 O_RDWR 的等价物），命名管道
// 只做 agentd → shim 的搬运。
//
// 为什么服务端必须是 shim 而不是 agentd：agentd 当服务端时，agentd 重启会
// 关闭管道、杀死执行者 stdin，而「执行者活过 agentd 重启」是 B36 的招牌属性，
// B37 已在 Windows 真机上验证过。
package prochost

import (
	"fmt"
	"io"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pipeBufSize 是命名管道两侧的内核缓冲区大小。投递的是单条指令（量级 KB），
// 4KB 够用；不够时 WriteFile 会阻塞等中继读走，不丢数据。
const pipeBufSize = 4096

// pipePollInterval 是 waitInputReader 的轮询间隔，与 unix 侧保持一致。
const pipePollInterval = 20 * time.Millisecond

// createInputChannel 在 Windows 上是 no-op。
//
// **返回 nil 表示「无事可做」，不表示「已验证通道可用」。** 命名管道服务端
// 必须由 shim 创建（见文件头「为什么服务端必须是 shim」），agentd 侧没有
// 任何可做的准备工作。就绪判定全部由 waitInputReader 承担：服务端没建起来时
// 它必然超时，而超时路径已有的处置（StartProc 自行 Kill 回收 shim）不变。
func createInputChannel(path string) error {
	log().Debug("Windows 输入通道无需预建，等待责任在 waitInputReader",
		"path", path, "pipe", pipeNameFor(path))
	return nil
}

// waitInputReader 轮询等待 shim 把命名管道服务端建起来。
//
// 参数：path 为通道路径；timeout 为等待上限
//
// 返回：等待耗时与错误。
//
// 注意：
//   - 探测方式是以客户端身份 CreateFile 管道名。ERROR_FILE_NOT_FOUND 表示
//     服务端还没建，继续等；成功或 ERROR_PIPE_BUSY 都表示已建好
//   - **探测成功后立即关闭句柄**。中继会把这次探测看成一次「连上又断开的
//     客户端」——这是无害的：中继是循环受理的，读到 EOF 后回到下一轮
//   - 其它错误立即返回，不重试：重试一个权限错误没有意义
func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	name := pipeNameFor(path)
	deadline := time.Now().Add(timeout)
	start := time.Now()
	for {
		h, err := dialPipe(name)
		if err == nil {
			_ = windows.CloseHandle(h)
			return time.Since(start), nil
		}
		if err == windows.ERROR_PIPE_BUSY {
			// 服务端在、只是实例都忙着：对「是否就绪」这个问题而言就是就绪
			return time.Since(start), nil
		}
		if err != windows.ERROR_FILE_NOT_FOUND {
			return time.Since(start), fmt.Errorf("探测管道 %s: %w", name, err)
		}
		if time.Now().After(deadline) {
			return time.Since(start), fmt.Errorf("管道 %s（通道 %s）在 %s 内未出现服务端",
				name, path, timeout)
		}
		time.Sleep(pipePollInterval)
	}
}

// writeInputChannel 以客户端身份连上管道投递一段字节（见 WriteInputChannel 的文档）。
//
// 注意：打不开即「读端不在」，这条语义与 unix 侧的 ENXIO 对齐，是调用方判定
// 「执行者已不在」的唯一依据，不得改成重试等待。
func writeInputChannel(path string, data []byte) error {
	name := pipeNameFor(path)
	h, err := dialPipeWaitBusy(name, pipeBusyBudget)
	if err != nil {
		return fmt.Errorf("连接管道 %s（通道 %s，读端可能已不在）: %w", name, path, err)
	}
	defer windows.CloseHandle(h)
	var written uint32
	for off := 0; off < len(data); {
		if err := windows.WriteFile(h, data[off:], &written, nil); err != nil {
			return fmt.Errorf("写管道 %s（通道 %s）: %w", name, path, err)
		}
		if written == 0 {
			return fmt.Errorf("写管道 %s 返回 0 字节，放弃", name)
		}
		off += int(written)
	}
	return nil
}

// openInputChannel 为子进程准备 stdin：建匿名管道 + 命名管道服务端 + 中继。
//
// 返回：
//   - r: 匿名管道读端，交给 cmd.Stdin
//   - cleanup: 子进程退出后调用，停中继、关服务端与写端
//   - error: 非 nil 时 shim 必须放弃拉起执行者
//
// 注意：**写端 w 不在这里关闭**——shim 全程持有它，子进程才永不见 EOF。
// 它由 cleanup 关闭，而 cleanup 在子进程退出后才被调用。
func openInputChannel(path string) (io.ReadCloser, func(), error) {
	name := pipeNameFor(path)
	srv, err := createPipeServer(name)
	if err != nil {
		log().Error("创建命名管道服务端失败", "pipe", name, "path", path, "cause", err)
		return nil, nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		_ = windows.CloseHandle(srv)
		return nil, nil, fmt.Errorf("创建匿名管道: %w", err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		relayPipe(srv, w, name, stop)
	}()
	log().Info("Windows 输入通道已就位", "pipe", name, "path", path)
	cleanup := func() {
		close(stop)
		// 承重，三步缺一不可，顺序也不能换：
		//
		//  1. CancelIoEx：管道是同步（无 FILE_FLAG_OVERLAPPED）创建的，中继
		//     阻塞在 ConnectNamedPipe 里，而 stop 只在循环顶部被查——close(stop)
		//     唤不醒它。同时 CloseHandle 对「有同步 I/O 挂起」的 handle 会**等**
		//     那个 I/O 完成，于是关的人等 I/O、I/O 等客户端，互等到死。
		//     实测：Windows CI 上 TestWindowsFirstInstanceRejectsSquatting 卡满
		//     10 分钟，把整个包拖成 timeout panic（run 32149311654）。生产里的
		//     形态是「没有执行者连上来时，关闭任务输入通道永久挂起」。
		//  2. 等 done：中继真正退出前不能关 handle——它在出错路径上还会调
		//     DisconnectNamedPipe，对已关闭的 handle 调等于 use-after-close。
		//  3. 这时才 CloseHandle。
		_ = windows.CancelIoEx(srv, nil)
		<-done
		_ = windows.CloseHandle(srv)
		_ = w.Close()
		_ = r.Close()
	}
	return r, cleanup, nil
}

// createPipeServer 建立命名管道服务端。
//
// 两条安全要求，都不是可选项：
//   - FILE_FLAG_FIRST_PIPE_INSTANCE：命名管道位于**全局命名空间**，不加它的话
//     任何本机进程都能抢先创建同名管道，之后 agentd 连上去的是它的实例——
//     这条通道直接就是执行者的 stdin，被搭上去等于能给模型下任意指令。
//     加上之后，抢占表现为这里创建失败，是可见故障而非静默劫持
//   - 显式安全描述符只授权当前用户与 SYSTEM：默认 ACL 会放行同机其它用户
func createPipeServer(name string) (windows.Handle, error) {
	sa, err := pipeSecurityAttributes()
	if err != nil {
		return windows.InvalidHandle, err
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("管道名 %s 转 UTF16: %w", name, err)
	}
	h, err := windows.CreateNamedPipe(namePtr,
		windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1, // nMaxInstances：中继循环复用同一个实例，一个就够
		0, pipeBufSize, 0, sa)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("创建命名管道 %s（名字可能已被占用）: %w", name, err)
	}
	return h, nil
}

// pipeSecurityAttributes 构造只授权当前用户与 SYSTEM 的安全属性。
func pipeSecurityAttributes() (*windows.SecurityAttributes, error) {
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("取当前用户 SID: %w", err)
	}
	// D:P = 不继承父对象 ACL；GA = 全部权限；SY = LocalSystem
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("构造安全描述符 %s: %w", sddl, err)
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

// dialPipe 以客户端身份打开管道（写方向）。
func dialPipe(name string) (windows.Handle, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(namePtr, windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, 0, 0)
}

// pipeBusyBudget 是 ERROR_PIPE_BUSY 的重试预算，见 dialPipeWaitBusy。
//
// 取 5s 而不是更短：中继绕回下一次 ConnectNamedPipe 通常是微秒级，5s 已经
// 宽裕到只有「中继真的卡死」才会耗尽——而那种情况下失败正是我们要的。
const pipeBusyBudget = 5 * time.Second

// dialPipeWaitBusy 连接管道，遇到 ERROR_PIPE_BUSY 时在预算内重试。
//
// 参数：name 为管道全名；budget 为 busy 重试的总时长上限
//
// 返回：连上的句柄；预算耗尽或遇到其它错误时返回错误。
//
// 为什么必须有这一层（B128 真机验收实证）：Windows 命名管道的客户端协议要求
// 撞到 ERROR_PIPE_BUSY 时等一个空闲实例再重试（标准做法是 WaitNamedPipe，
// 但本项目所用的 x/sys/windows v0.47.0 未包装它，故用有界重试实现同一语义）。
// 缺了它，首回合投递会直接失败——因为 waitInputReader 的就绪探测本身就是一次
// CreateFile+Close，它会消耗掉中继的一个受理周期，紧接着的投递恰好落在中继
// 绕回 ConnectNamedPipe 之前的窗口里。真机报文是：
// "投递首回合 prompt: 连接管道 …（读端可能已不在）: All pipe instances are busy."
//
// 注意：**只对 ERROR_PIPE_BUSY 重试**。ERROR_FILE_NOT_FOUND 表示服务端根本
// 不在，必须立刻失败——那是调用方判定「执行者已不在」的承重语义，用重试把它
// 拖成超时会让一个死任务看起来像在忙。
func dialPipeWaitBusy(name string, budget time.Duration) (windows.Handle, error) {
	deadline := time.Now().Add(budget)
	for {
		h, err := dialPipe(name)
		if err == nil {
			return h, nil
		}
		if err != windows.ERROR_PIPE_BUSY {
			return windows.InvalidHandle, err
		}
		if time.Now().After(deadline) {
			return windows.InvalidHandle, fmt.Errorf(
				"管道 %s 的实例在 %s 内始终忙（中继可能卡住）: %w", name, budget, err)
		}
		time.Sleep(pipePollInterval)
	}
}

// relayPipe 循环受理客户端并把字节抄进匿名管道写端。
//
// 每次 agentd 投递就是一次客户端连接：连上 → 写一帧 → 断开。断开在服务端侧
// 表现为 ReadFile 返回 ERROR_BROKEN_PIPE，那是**正常结束**不是故障。
//
// 失败取舍（有意为之，不是缺陷）：受理或读取出错时打日志并重建连接继续；
// 只有 stop 被关闭才退出。**任何情况下都不杀子进程**——回合中间的产出不该
// 被丢弃。中继彻底失效后，后续 writeInputChannel 会连不上管道，走既有的
// 「读端不在」路径；真实原因去 shim.log 找。
func relayPipe(srv windows.Handle, w *os.File, name string, stop <-chan struct{}) {
	buf := make([]byte, pipeBufSize)
	for {
		select {
		case <-stop:
			log().Info("输入通道中继退出", "pipe", name)
			return
		default:
		}
		err := windows.ConnectNamedPipe(srv, nil)
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			// ERROR_OPERATION_ABORTED 是 cleanup 调 CancelIoEx 的结果，属正常
			// 收尾而非故障——按故障走会打一条误导的 Error，还会白等一个轮询间隔。
			if err == windows.ERROR_OPERATION_ABORTED {
				log().Info("输入通道中继被取消，退出", "pipe", name)
				return
			}
			log().Error("受理输入通道客户端失败", "pipe", name, "cause", err)
			_ = windows.DisconnectNamedPipe(srv)
			// 退避也要能被 stop 打断：否则 cleanup 最坏要多等一个轮询间隔
			select {
			case <-stop:
				log().Info("输入通道中继退出", "pipe", name)
				return
			case <-time.After(pipePollInterval):
			}
			continue
		}
		for {
			var n uint32
			rerr := windows.ReadFile(srv, buf, &n, nil)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					log().Error("写匿名管道失败，执行者可能已退出",
						"pipe", name, "cause", werr)
					break
				}
			}
			if rerr != nil {
				// ERROR_BROKEN_PIPE = 客户端投递完毕正常断开，不是错误
				if rerr != windows.ERROR_BROKEN_PIPE {
					log().Warn("读输入通道出错", "pipe", name, "cause", rerr)
				}
				break
			}
		}
		_ = windows.DisconnectNamedPipe(srv)
	}
}
