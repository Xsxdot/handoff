// retain.go —— managed 工作树留存/回收判据（B233.4）。
//
// 职责：把「取消留存、验收前不删、归档或显式清理才回收」收成纯函数，
// 供应用在调用 RecycleManaged 之前询问。
//
// 边界：不执行 git、不删目录。生产路径的 Stop/Done/reclaim 接线归实现节点。
package workspace

// MayRecycle 决定一次触发能不能回收这棵树。
//
// 规则（全是 keep，除非下面某一条命中 recycle）：
//   - 非 managed（用户树/原地）永远 keep
//   - 手工树永远 keep
//   - waiting_review 永远 keep
//   - TriggerStop 永远 keep（取消留存现场）
//   - TriggerCompensate 且 managed：recycle（executor 尚未接管，无待验收现场）
//   - TriggerDone 或 TriggerExplicit，且终态 managed：recycle
func MayRecycle(in RecycleInput) Decision {
	if !in.Managed || in.Manual {
		return RetainKeep
	}
	if in.State == "waiting_review" {
		return RetainKeep
	}
	if in.Trigger == TriggerStop {
		return RetainKeep
	}
	if in.Trigger == TriggerCompensate {
		return RetainRecycle
	}
	if (in.Trigger == TriggerDone || in.Trigger == TriggerExplicit) && in.Terminal {
		return RetainRecycle
	}
	return RetainKeep
}
