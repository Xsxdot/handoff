# iOS 前台恢复绑定增量

基线 bf1976f1；仅扩展现有壳→绑定→连接核方向。生产者为 mobile/bind，消费者为 iOS LiveConnectCore；Android 原有消费不受新增函数影响。恢复的是原本由连接核拥有的网络资源，认证权威仍在 agentd，原生壳没有新的凭据或 HTTP 能力。

实际钉版 gobind / XCFramework 生成面：

```go
func Resume(machine string) error
```

```java
public static native void resume(String machine) throws Exception;
```

```objc
FOUNDATION_EXPORT BOOL BindResume(NSString* _Nullable machine, NSError* _Nullable* _Nullable error);
```

Swift 经 NSErrorPointer 检查 BOOL，不以返回 false 冒充成功。生产消费为 ConnectCore.resume → LiveConnectCore.resume → BindResume；同一个 liveCore 拥有原有会话槽。

1. 仅允许已经配对、原在线且当前活动的机器恢复；已关闭的连接核拒绝恢复。
2. 成功后 loopback origin 与原文档完全相同，不导航、不换端口。
3. 恢复重建 HTTP/relay 客户端和监听 socket，不领 ticket、不清空或重新签发 cookie。
4. 恢复后的反代仍不注入凭据；无 cookie 请求继续受到 agentd 门禁约束。
5. 新客户端构造失败时保留旧资源；重新监听失败时关闭新资源并返回 error，旧 origin/会话仍保留供下一次恢复尝试。
6. Swift 在后台队列执行恢复，完成后回主队列，仅当机器和 origin 仍匹配才向原文档的 document 发 handoff-foreground。错误或机器切换不发旧页面恢复事件。
7. Web 对该事件丢弃旧轮询请求、重拉当前数据；真实 401 已进入终止态时不因此重新认证。

可执行形状在 export_surface_test.go、shell_api_golden_test.go 钉住；行为覆盖见 internal/mobilecore/resume_test.go、mobile/bind/realcore_test.go、CookieBridgeTests 和 usePoll.test.ts。真机安装与恢复验收记录在同目录 ios-settings-reconnect 台账，不以生成成功代替真机通过。
