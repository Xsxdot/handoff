# B462 台账

## spec
- 2026-10-22 熟练工 code-investigate 只读调查 upgrade 链路（基线 085ff40b，与 d905c3ab 同码），报告 /var/folders/.../handoff-artifacts-b2045ea4-1768282917/report.md：无本地构建入口；最小扩展点在 releaseFetcher 实现与 RemoteOne push 分支；Install 自检 version 首行==tag；managed 由 INVOCATION_ID/XPC_SERVICE_NAME 判定。
- 主会话读 scripts/build-deploy.sh：已注入 releaseVersion=<短号>[+dirtyN]，只出裸二进制。
- 主会话读 cmd/agentd.go setupLedger：openLedgerWithRetry 只包住 open；SelfRegister/ImportLocalTargets 出错即不挂载、照常 Ready——too many clients 需重启的根因。
- `handoff upgrade --check`（生产）：三台 e53b10ce83b9 均判「需要升级」（vs v0.5.0），status.update={managed:true,pull:true}。
