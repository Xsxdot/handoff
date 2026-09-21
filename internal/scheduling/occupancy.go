// occupancy.go —— 编制域占用键编码（B233.5）。
//
// 职责：冻结两级运行计数在 registry kind `sched_running` 下的 id 字面值。
// 载体物理位与小队成员政策位必须用同一套键，否则准入与释放会对不上。
//
// 边界：纯函数，无 I/O、不改计数。生产路径 acquire/Release 接线归实现节点。
package scheduling

// OccupancyCarrierKey 是载体物理位计数 id。与 acquire 现状拼接 `carrier/<name>` 同形。
func OccupancyCarrierKey(carrier string) string {
	if carrier == "" {
		return ""
	}
	return "carrier/" + carrier
}

// OccupancyMemberKey 是小队成员政策位计数 id。与 acquire 现状拼接
// `squad/<squad>/<carrier>` 同形。小队或载体名为空时返回空串——空小队不得
// 写出 `squad//<carrier>`（那会把载体直派算成一个不存在的成员键）。
func OccupancyMemberKey(squad, carrier string) string {
	if squad == "" || carrier == "" {
		return ""
	}
	return "squad/" + squad + "/" + carrier
}

// OccupancyKeys 返回一次占用要动的计数 id。载体直派（小队名为空）只有载体键。
func OccupancyKeys(squad, carrier string) (memberKey, carrierKey string) {
	return OccupancyMemberKey(squad, carrier), OccupancyCarrierKey(carrier)
}
