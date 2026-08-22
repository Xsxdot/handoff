# B162 实现计划：WS 截断诊断的等待改成确定性信号

> 2026-08-22。协调者写。卡：B162（低）。

## 事实基线（协调者在 985f37135 上查证）

`internal/agentd/ws_regression_round2_test.go:311`：

```go
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(env.logged(), "补发窗口截断且缺口未由实时流补齐") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
```

用挂钟等一行日志。服务端在**写出事件之后**才跑截断诊断
（`internal/agentd/server.go:2082` 起的那段），客户端收到 fresh 的时刻早于诊断
落盘——3 秒这个常数就是负载敏感的判据。全量并发跑时命中过一次
（`--- FAIL: TestWSTruncationWarnsOnRealGap (30.03s)`，整包 117s），单跑必绿。

同一形态在同文件 `:361` 还有一处（`TestWSTruncationGapCountedPerTask`），
**两处一起改**。

**不要走「把 3s 换成 wsDeadline(t, 3*time.Second)」这条路**：同文件 :285 的注释
已经明写「若仍偶发翻红，按 WS 用例分包处理，**不要继续调倍数**」——加倍数是把
同一个赌注押得更大，不是消掉它。

## 设计决定

1. **给诊断加一个测试专用钩子**，让用例等一个「诊断确实跑完了」的确定性信号，
   而不是等日志文本出现。生产路径 nil 即不调用，零开销、零行为变化。
2. **钩子放在诊断分支之后、覆盖三条分支**（错误 / 告警 / 已补齐）：只在告警分支
   调用会让「诊断跑了但判成已补齐」这种情形永远等到超时，把一个明确失败退化成
   一次超时——排查成本高得多。钩子传出判定结果，用例自己断言拿到的是哪一种。
3. **日志断言保留**：钩子解决的是「什么时候可以断言」，不是「断言什么」。
   告警文案仍是用户可见契约，那条 `strings.Contains` 一字不动。

## Task 1：诊断钩子

`internal/agentd/server.go`：`Server` 结构体加字段

```go
	// onTruncationDiagnosed 是**测试专用**的诊断完成钩子：截断诊断跑完后带着
	// 判定结果调用一次。生产上恒为 nil。
	//
	// why 存在：诊断在事件写出之后才跑，用例拿不到「诊断完成」这个时刻，
	// 只能拿挂钟猜（曾经是 3 秒），机器越忙越容易假红（B162）。
	onTruncationDiagnosed func(verdict string)
```

在诊断的三条分支各自结束处调用（`verdict` 取 `"error"` / `"warned"` /
`"covered"`），用一个小助手包一下避免 nil 判断散落：

```go
func (s *Server) noteTruncationDiagnosed(verdict string) {
	if s.onTruncationDiagnosed != nil {
		s.onTruncationDiagnosed(verdict)
	}
}
```

## Task 2：两条用例改等信号

`ws_regression_round2_test.go`：`newWSTestEnv` 里给 env 挂一个带缓冲的
`chan string`（缓冲 ≥4，避免多次诊断把服务端 goroutine 堵住），设进
`env.srv.onTruncationDiagnosed`。两条用例把挂钟轮询换成：

```go
	select {
	case verdict := <-env.truncationDiagnosed:
		if verdict != "warned" {
			t.Fatalf("截断诊断跑完了但判定是 %q，期望 warned；日志尾部：%s", verdict, tailStr(env.logged(), 600))
		}
	case <-ctx.Done():
		t.Fatalf("等截断诊断完成超时；日志尾部：%s", tailStr(env.logged(), 600))
	}
	if !strings.Contains(env.logged(), "补发窗口截断且缺口未由实时流补齐") {
		// 既有断言原样保留：钩子管「何时能断言」，告警文案仍是用户可见契约
		t.Errorf(...)   // 原文一字不改
	}
```

`ctx` 用两条用例里已有的那个（`wsDeadline(t, 10*time.Second)`），不新造常数。

## 测试映射与变异（必须真跑）

1. `go test ./internal/agentd/ -run 'TestWSTruncation' -count=5` 全绿。
2. **变异（本卡的真判据）**：在 `server.go` 诊断块**之前**临时插
   `time.Sleep(2 * time.Second)`（本地实验，**不提交**）：
   - 修改前：两条用例必须**红**（3 秒挂钟被 2 秒延迟吃掉大半，负载模拟成立）
   - 修改后：必须**绿**（等的是信号不是挂钟）
   两次原文抄进 ledger。修改前没红就说明延迟插错了位置，停下来报协调者。
3. 回归：`go test ./internal/agentd/ -count=1` 全绿。

## 测试范围

- `go test ./internal/agentd/`
- `go build ./...`、`go vet ./...`、`gofmt -l .` 无输出

## 不属于本次

- 不改 `wsDeadline`，不调任何超时倍数。
- 不改诊断的判定逻辑与告警文案。
- 不做「WS 用例分包」（那是另一条路，本卡用确定性信号解决）。
