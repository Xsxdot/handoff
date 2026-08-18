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
	h, err := dialPipe(name)
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
	go relayPipe(srv, w, name, stop)
	log().Info("Windows 输入通道已就位", "pipe", name, "path", path)
	cleanup := func() {
		close(stop)
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
			log().Error("受理输入通道客户端失败", "pipe", name, "cause", err)
			_ = windows.DisconnectNamedPipe(srv)
			time.Sleep(pipePollInterval)
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
