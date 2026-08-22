# B159 ledger

## 实验记录

### 改前基线

命令：

```text
go test ./internal/agentd/ -run TestApproverApprovesPermissionWithoutWaking -count=20
```

原文：

```text
ok  	github.com/Xsxdot/handoff/internal/agentd	3.804s
```

### 变异实验：markDelivered 延迟 200ms，修改前

临时在 `Manager.markDelivered` 开头插入 `time.Sleep(200 * time.Millisecond)`，未提交。

命令：

```text
go test ./internal/agentd/ -run TestApproverApprovesPermissionWithoutWaking -count=20
```

原文：

```text
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.14s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:d8d03e87-eb41-4809-905b-f57ddeb1fb9b:perm-1 TaskID:d8d03e87-eb41-4809-905b-f57ddeb1fb9b Kind:gate Request:[123 34 107 105 110 100 34 58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223f024e0 CreatedAt:2026-08-22 16:15:20.821244979 +0000 UTC AnsweredAt:2026-08-22 16:15:20.823636656 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.20s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:072b3ace-36af-43b5-8160-ba7fd3cd07fa:perm-1 TaskID:072b3ace-36af-43b5-8160-ba7fd3cd07fa Kind:gate Request:[123 34 107 105 110 100 34 58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223e06c00 CreatedAt:2026-08-22 16:15:21.003851992 +0000 UTC AnsweredAt:2026-08-22 16:15:21.007557891 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.21s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:4a186971-ba14-45fe-9640-641322511b7c:perm-1 TaskID:4a186971-ba14-45fe-9640-641322511b7c Kind:gate Request:[123 34 107 105 110 100 34 58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223baa708 CreatedAt:2026-08-22 16:15:21.224295 +0000 UTC AnsweredAt:2026-08-22 16:15:21.226156126 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.18s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:29f12288-51b0-4743-aeb6-31573cd52d3b:perm-1 TaskID:29f12288-51b0-4743-aeb6-31573cd52d3b Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223e06708 CreatedAt:2026-08-22 16:15:21.401719694 +0000 UTC AnsweredAt:2026-08-22 16:15:21.403673048 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.18s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:22bcb1c4-01cc-490e-93a1-4cd2ef779691:perm-1 TaskID:22bcb1c4-01cc-490e-93a1-4cd2ef779691 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223dfe330 CreatedAt:2026-08-22 16:15:21.590038051 +0000 UTC AnsweredAt:2026-08-22 16:15:21.592380592 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.26s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:ceca0e16-41b5-4620-b436-e6b438fd27fd:perm-1 TaskID:ceca0e16-41b5-4620-b436-e6b438fd27fd Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223e06720 CreatedAt:2026-08-22 16:15:21.838534 +0000 UTC AnsweredAt:2026-08-22 16:15:21.842001051 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.25s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:d2db1a3a-e0c5-4289-a552-5bc47f13e69d:perm-1 TaskID:d2db1a3a-e0c5-4289-a552-5bc47f13e69d Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223dfe8b8 CreatedAt:2026-08-22 16:15:22.087919837 +0000 UTC AnsweredAt:2026-08-22 16:15:22.090597085 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.15s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:1251390b-4f1b-496b-906c-6e1cad97933e:perm-1 TaskID:1251390b-4f1b-496b-906c-6e1cad97933e Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223e07998 CreatedAt:2026-08-22 16:15:22.249601958 +0000 UTC AnsweredAt:2026-08-22 16:15:22.251870305 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.20s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:2f75854f-7241-401b-ab0d-23c9d8408bb0:perm-1 TaskID:2f75854f-7241-401b-ab0d-23c9d8408bb0 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223dfe720 CreatedAt:2026-08-22 16:15:22.43595351 +0000 UTC AnsweredAt:2026-08-22 16:15:22.438958503 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.19s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:420c1b37-b8a1-4c23-852e-1ecc0f7ca7e2:perm-1 TaskID:420c1b37-b8a1-4c23-852e-1ecc0f7ca7e2 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223d1a750 CreatedAt:2026-08-22 16:15:22.638813039 +0000 UTC AnsweredAt:2026-08-22 16:15:22.640623656 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.24s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:a82dd903-64fe-441d-b4d5-6d09516f1f22:perm-1 TaskID:a82dd903-64fe-441d-b4d5-6d09516f1f22 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab22413abe8 CreatedAt:2026-08-22 16:15:22.870378542 +0000 UTC AnsweredAt:2026-08-22 16:15:22.873436734 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.17s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:eb5a7550-3a56-4436-8a5a-e02e4104c687:perm-1 TaskID:eb5a7550-3a56-4436-8a5a-e02e4104c687 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34:58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223baaaf8 CreatedAt:2026-08-22 16:15:23.05379807 +0000 UTC AnsweredAt:2026-08-22 16:15:23.055639726 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.21s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:6612215d-a0a9-4962-9724-68964aa36c1e:perm-1 TaskID:6612215d-a0a9-4962-9724-68964aa36c1e Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223dfede0 CreatedAt:2026-08-22 16:15:23.260422334 +0000 UTC AnsweredAt:2026-08-22 16:15:23.263186145 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.21s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:3143fec7-66dd-411e-bce0-205905bdd20d:perm-1 TaskID:3143fec7-66dd-411e-bce0-205905bdd20d Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223ae0450 CreatedAt:2026-08-22 16:15:23.462608614 +0000 UTC AnsweredAt:2026-08-22 16:15:23.465452604 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.15s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:5e2535a7-a83d-412a-be8b-ed642c399cda:perm-1 TaskID:5e2535a7-a83d-412a-be8b-ed642c399cda Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223baa750 CreatedAt:2026-08-22 16:15:23.619649519 +0000 UTC AnsweredAt:2026-08-22 16:15:23.621770861 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.25s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:f5df1040-3c7d-4967-8bd7-82f9434744b0:perm-1 TaskID:f5df1040-3c7d-4967-8bd7-82f9434744b0 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab22413ba88 CreatedAt:2026-08-22 16:15:23.858286929 +0000 UTC AnsweredAt:2026-08-22 16:15:23.861433707 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.22s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:831a9b07-8cd4-4cb1-96b3-08e5d2c2b013:perm-1 TaskID:831a9b07-8cd4-4cb1-96b3-08e5d2c2b013 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34 58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223d1a7f8 CreatedAt:2026-08-22 16:15:24.08416621 +0000 UTC AnsweredAt:2026-08-22 16:15:24.086588004 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.12s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:7b13003e-26be-4cf7-9229-05468a2317ee:perm-1 TaskID:7b13003e-26be-4cf7-9229-05468a2317ee Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34:58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223ae0b70 CreatedAt:2026-08-22 16:15:24.21729425 +0000 UTC AnsweredAt:2026-08-22 16:15:24.218561792 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.16s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:45a1b3e5-5588-4aea-a2f9-a0c54f790b01:perm-1 TaskID:45a1b3e5-5588-4aea-a2f9-a0c54f790b01 Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34:58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223e8a138 CreatedAt:2026-08-22 16:15:24.373908629 +0000 UTC AnsweredAt:2026-08-22 16:15:24.376355087 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
--- FAIL: TestApproverApprovesPermissionWithoutWaking (0.17s)
    approver_test.go:279: approve 应自动应答精确 allow 并标记送达: &{ID:392a2c93-8efb-45a5-887e-42412eaed82e:perm-1 TaskID:392a2c93-8efb-45a5-887e-42412eaed82e Kind:gate Request:[123 34 107 105 110 100 34:58 34 103 97 116 101 34 44 34 112 101 114 109 105 115 115 105 111 110 34:58 34 114 117 110 32 116 101 115 116 115 34 125] Answer:0xab223e8b470 CreatedAt:2026-08-22 16:15:24.550699738 +0000 UTC AnsweredAt:2026-08-22 16:15:24.552824138 +0000 UTC DeliveredAt:<nil> Fingerprint:c7b8e61142837b8ee5c2846f5c05c420dcbf72fff1b8d30dc20afcc518e8b4f5}
FAIL
FAIL	github.com/Xsxdot/handoff/internal/agentd	3.879s
```

### 变异实验：markDelivered 延迟 200ms，修改后

同样临时插入延迟；送达门已加入，实验 sleep 未提交。

命令：

```text
go test ./internal/agentd/ -run TestApproverApprovesPermissionWithoutWaking -count=20
```

原文：

```text
ok  	github.com/Xsxdot/handoff/internal/agentd	7.422s
```

## 裁决与提交记录

- Task 1 双裁决：spec 符合（送达门取 `DeliveredAt`、保留 `waitTaskState`、断言不改、无生产改动）；代码质量通过（轮询有超时、错误可诊断、helper 与既有测试 helper 同处）。commit 范围：`408cd912..HEAD`（Task 1 提交）。
