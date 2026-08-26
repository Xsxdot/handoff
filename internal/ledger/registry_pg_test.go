// registry 双方言冒烟回归（B156.3 契约 §3.1 四原语 + §9 测试义务④，卡 B156.3.7）。
//
// 职责：把 registry 四原语的冻结语义（CAS 版本、全表单调 Seq、ErrNotFound、
// 入参校验、body 字节搬运边界）在同一组判据下分别压过 PG 与 SQLite 两个存储
// 方言——某条语句只在一侧方言可用时，另一腿必翻红。
//
// 边界：只测 internal/ledger 存储层；编制域语义（准入、队列排序）归 K2 的
// scheduling 层测试，门面 DTO 映射已由竖切（internal/keystone/slice_test.go）
// 锁定，此处不再重复。
//
// 为什么两条腿共用一个文件：拆解为本卡圈定的有界文件集只允许新增一个测试
// 文件；两腿判据体是同一个函数，分文件只会复制粘贴。
//
// 已知方言不对称（刻意不锁进断言）：PG 的 JSONB 归一化空白与键序，SQLite
// TEXT 逐字节透传——序列化边界因此断言 JSON 语义恒等（assertSameJSON）而非
// 字节恒等；消费方（schedclient.Record → 编制域）按语义读 body，字节形态不是
// 任何契约的一部分。
package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

// registrySmokeKind 是冒烟专用的 kind——刻意取冻结五 kind（carrier/squad/
// sched_running/ignition_queue/launch_queue）之外的值：Store 层对 kind 只要求
// 非空（契约拍板记录④「账本不理解 body」，白名单执法在编制域
// scheduling.go Enqueue）。用外来 kind 让清理段的作用域与生产数据零交集，
// LEDGER_TEST_PG_DSN 即使指向带真数据的开发库也不会误伤或撞行。
const registrySmokeKind = "registry-smoke-test"

// TestPGRegistryCASAndSeq 是真机半边：设 LEDGER_TEST_PG_DSN 后真跑判据体；
// 未设时 newPGStore 显式 skip 并输出原因（skip 是绿的，但必须让人看见为什么绿；
// 真机清单第 4 条要求至少一次非 skip 落证）。
func TestPGRegistryCASAndSeq(t *testing.T) {
	s := newPGStore(t)
	assertRegistrySemantics(t, s)
}

// TestSQLiteRegistryCASAndSeq 是机内默认绿的半边：与 PG 腿共用判据体，
// 无条件执行——它锁「SQLite 行为不被后续改动破坏」，并与 PG 腿构成同判据对偶。
func TestSQLiteRegistryCASAndSeq(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("打开临时 SQLite 账本: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	assertRegistrySemantics(t, s)
}

// TestSQLiteRegistryPutLostUpdateGuard 锁 Put 更新路径的最后一道防线：UPDATE
// 带 version 谓词却匹配 0 行（SELECT 与 UPDATE 之间行被并发改走）时必须报
// ErrCASConflict 而非静默假成功。判据体②的冲突都开火在更早的版本比对上，
// 到不了这个分支——审阅轮（B156.3.7）指出该守卫此前无机内防线，本测试补上。
//
// 机内确定性构造法：BEFORE UPDATE 触发器在更新落格前把该行删掉——WHERE 已按
// SELECT 看到的版本匹配，触发器删行使实际改动归零，RowsAffected==0 分支被真实
// 踩中（探针实测：守卫开火报「并发修改」，回滚后行原样保留）。这等价于并发
// 事务在窗口期删走行的结局，只是把竞态窗口换成引擎保证的确定时点。PG 方言无法
// 移植本构造（同表 BEFORE 触发器删自身行的行为不同型），PG 侧同型守卫的真机
// 验证归真机清单第 4 条。
func TestSQLiteRegistryPutLostUpdateGuard(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("打开临时 SQLite 账本: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	v, err := s.RegistryPut(registrySmokeKind, "lost-1", 0, []byte(`{"n":1}`), "smoke")
	if err != nil || v != 1 {
		t.Fatalf("播种 lost-1: v=%d err=%v", v, err)
	}

	const trig = `zz_putguard_lost_update`
	if _, err := s.db.Exec(`CREATE TRIGGER ` + trig + ` BEFORE UPDATE ON registry
		FOR EACH ROW WHEN NEW.version = OLD.version + 1
		BEGIN DELETE FROM registry WHERE kind = OLD.kind AND id = OLD.id AND version = OLD.version; END`); err != nil {
		t.Fatalf("建丢失更新触发器: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + trig); err != nil {
			t.Errorf("拆触发器: %v", err)
		}
	})

	if _, err := s.RegistryPut(registrySmokeKind, "lost-1", 1, []byte(`{"n":2}`), "smoke"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("RowsAffected==0 守卫应报 ErrCASConflict，得 %v", err)
	}

	// 冲突不写入：mutate 整体回滚连触发器的 DELETE 一并撤销——行必须原样还在，
	// 版本与 body 都不动。若守卫被摘除，Put 会假成功且行真的被改写。
	back, err := s.RegistryGet(registrySmokeKind, "lost-1")
	if err != nil {
		t.Fatalf("冲突后读回: %v", err)
	}
	if back.Version != 1 {
		t.Fatalf("冲突后版本应仍为 1，得 %d", back.Version)
	}
	assertSameJSON(t, "lost-1 body 不变", []byte(`{"n":1}`), back.Body)
}

// assertRegistrySemantics 双方言共用的判据体。断言编号①–⑨与 plan §5 的
// 验收映射表一一对应；seq 断言一律用相对比较（共享库上绝对前值不定）。
func assertRegistrySemantics(t *testing.T, s *Store) {
	t.Helper()
	cleanRegistrySmoke(t, s)
	t.Cleanup(func() { cleanRegistrySmoke(t, s) })

	// ① 新建：expectVersion=0 且行不存在 → 版本从 1 起
	v, err := s.RegistryPut(registrySmokeKind, "ent-1", 0, []byte(`{"n":1}`), "smoke")
	if err != nil {
		t.Fatalf("新建 ent-1: %v", err)
	}
	if v != 1 {
		t.Fatalf("新建后版本应为 1，得 %d", v)
	}

	// ② CAS 冲突两个方向：对已存在行重放 expect=0；用过期/未来版本号更新
	if _, err := s.RegistryPut(registrySmokeKind, "ent-1", 0, []byte(`{"n":9}`), "smoke"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("已存在行 expect=0 应 ErrCASConflict，得 %v", err)
	}
	cur, err := s.RegistryGet(registrySmokeKind, "ent-1")
	if err != nil {
		t.Fatalf("读回 ent-1: %v", err)
	}
	if _, err := s.RegistryPut(registrySmokeKind, "ent-1", cur.Version+1, []byte(`{"n":8}`), "smoke"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("未来版本应 ErrCASConflict，得 %v", err)
	}

	// ③ Get 读回元字段 + 序列化边界（JSON 语义恒等，理由见文件头不对称注记）
	if cur.Kind != registrySmokeKind || cur.ID != "ent-1" || cur.Version != 1 {
		t.Fatalf("元字段不符: %+v", cur)
	}
	assertSameJSON(t, "ent-1 body", []byte(`{"n":1}`), cur.Body)

	// ④ Seq 全表单调：更新后严格递增；跨实体新建也严格递增
	v2, err := s.RegistryPut(registrySmokeKind, "ent-1", cur.Version, []byte(`{"n":2}`), "smoke")
	if err != nil || v2 != cur.Version+1 {
		t.Fatalf("按当前版本更新: v=%d err=%v", v2, err)
	}
	after, err := s.RegistryGet(registrySmokeKind, "ent-1")
	if err != nil {
		t.Fatalf("更新后读回: %v", err)
	}
	if after.Seq <= cur.Seq {
		t.Fatalf("更新后 seq 应严格递增: %d -> %d", cur.Seq, after.Seq)
	}
	if _, err := s.RegistryPut(registrySmokeKind, "ent-2", 0, []byte(`{"n":3}`), "smoke"); err != nil {
		t.Fatalf("新建 ent-2: %v", err)
	}
	e2, err := s.RegistryGet(registrySmokeKind, "ent-2")
	if err != nil {
		t.Fatalf("读回 ent-2: %v", err)
	}
	if e2.Seq <= after.Seq {
		t.Fatalf("跨实体 seq 应严格递增: %d -> %d", after.Seq, e2.Seq)
	}

	// ⑤ List：只含本 kind、按 seq 升序、body 语义恒等
	rows, err := s.RegistryList(registrySmokeKind)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应恰 2 行，得 %d", len(rows))
	}
	if rows[0].ID != "ent-1" || rows[1].ID != "ent-2" || rows[0].Seq >= rows[1].Seq {
		t.Fatalf("List 应按 seq 升序: %+v", rows)
	}
	assertSameJSON(t, "list[0] body", []byte(`{"n":2}`), rows[0].Body)

	// ⑥ ErrNotFound 缺失语义（errors.Is 可判定——包装错误不得吞哨兵）
	if _, err := s.RegistryGet(registrySmokeKind, "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("缺失读应 ErrNotFound，得 %v", err)
	}

	// ⑦ body 多形态语义恒等：嵌套对象/数组/unicode/null/数字精度各取一样本
	samples := []string{
		`{"name":"载体甲","nested":{"k":[1,2,{"deep":true}]}}`,
		`[1,"二",3.5,null]`,
		`{"emoji":"🚀","empty":"","zero":0,"big":9007199254740993}`,
		`true`,
	}
	for i, sample := range samples {
		id := fmt.Sprintf("shape-%d", i)
		if _, err := s.RegistryPut(registrySmokeKind, id, 0, []byte(sample), "smoke"); err != nil {
			t.Fatalf("写入样本 %s: %v", sample, err)
		}
		back, err := s.RegistryGet(registrySmokeKind, id)
		if err != nil {
			t.Fatalf("读回样本 %s: %v", sample, err)
		}
		assertSameJSON(t, id, []byte(sample), back.Body)
	}

	// ⑧ 入参校验错误分支：空 kind / 空 id / 空 body 在写路径拒绝
	if _, err := s.RegistryPut("", "x", 0, []byte(`{}`), "smoke"); err == nil {
		t.Fatal("空 kind 应被拒")
	}
	if _, err := s.RegistryPut(registrySmokeKind, "", 0, []byte(`{}`), "smoke"); err == nil {
		t.Fatal("空 id 应被拒")
	}
	if _, err := s.RegistryPut(registrySmokeKind, "x", 0, nil, "smoke"); err == nil {
		t.Fatal("空 body 应被拒")
	}

	// ⑨ Delete 的 CAS 三分支：错版本冲突 → 对版本成功 → 重复删除冲突 → 删后读缺失
	if err := s.RegistryDelete(registrySmokeKind, "ent-2", e2.Version+7, "smoke"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("错版本删除应 ErrCASConflict，得 %v", err)
	}
	if err := s.RegistryDelete(registrySmokeKind, "ent-2", e2.Version, "smoke"); err != nil {
		t.Fatalf("对版本删除: %v", err)
	}
	if err := s.RegistryDelete(registrySmokeKind, "ent-2", e2.Version, "smoke"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("重复删除应 ErrCASConflict，得 %v", err)
	}
	if _, err := s.RegistryGet(registrySmokeKind, "ent-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后读应 ErrNotFound，得 %v", err)
	}
}

// assertSameJSON 断言两段字节是同一份 JSON 文档。语义比较而非字节比较：
// PG JSONB 归一化空白与键序（文件头注记），字节比较在 PG 腿必然假红；
// 数字统一 unmarshal 为 float64 后比较——JSON 数文本就没有整浮之分，语义层面无损。
func assertSameJSON(t *testing.T, label string, want, have []byte) {
	t.Helper()
	var w, h any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("%s: 判据自身不是合法 JSON: %v", label, err)
	}
	if err := json.Unmarshal(have, &h); err != nil {
		t.Fatalf("%s: 存储层返回的不是合法 JSON: %v", label, err)
	}
	if !reflect.DeepEqual(w, h) {
		t.Fatalf("%s: JSON 语义不等: want=%s have=%s", label, want, have)
	}
}

// cleanRegistrySmoke 清掉本冒烟自己 kind 的行——杀伤半径以 kind 为界，不碰
// 同库其它数据（store_pg_test.go 清理段的同款纪律）。测试体前后各清一次：
// 前清防上次异常残留让「expect=0 新建」假冲突，后清防残留扩散。
// 清场失败直接 Fatal 而非像 mirror_pg_test.go:19 那样忽略：我们的行有唯一的
// kind 边界，DELETE 失败意味着库本身不可用，继续跑只会产出不可信结果。
func cleanRegistrySmoke(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(s.q(`DELETE FROM registry WHERE kind = ?`), registrySmokeKind); err != nil {
		t.Fatalf("清场 registry kind=%s: %v", registrySmokeKind, err)
	}
}
