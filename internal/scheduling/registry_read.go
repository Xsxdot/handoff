// registry_read.go —— 编制域只读投影：登记行（带版本）与队列清队序快照
// （B156.3 K3，gateway GET /api/squads、GET /api/queue 的数据源）。
//
// 职责：把 repo.List 的原始记录解码成实体并携带 registry 版本；队列位次语义
// 全部委托既有 position()——它是 Enqueue 返回值与清队次序的现有唯一权威，
// 本文件不写第二份排序比较逻辑，只按 position() 返回的整数排位。
//
// 边界：
//   - 不碰清队侧符号（PopReady/Enqueue/position 本体等归 K5 的递减链）；
//   - 损坏行与 position() 同判跳过：解码失败的行既不参与排位也不出现在快照
//     （诚实差异声明：清队侧 PopReady 对损坏行是整队报错——读面选择跳过是因为
//     位次权威 position() 本身跳过，两处语义由本声明钉住，漂移时先看这里）；
//   - 写入面（PutCarrier/PutSquad）仍在 scheduling.go；
//   - 代价声明：每行一次 position() 全队重算（O(N²) 读）——控制面看板量级
//     可接受；队列规模长大时由持清队侧的卡提供批量序，不在本卡自造第二份排序。
package scheduling

import (
	"encoding/json"
	"fmt"
	"sort"
)

// SquadRow 是小队登记行：实体 + registry 版本（CAS 编辑回路 GET 取版的读半边）。
type SquadRow struct {
	Version int
	Squad   Squad
}

// CarrierRow 是载体登记行，语义同 SquadRow。
type CarrierRow struct {
	Version int
	Carrier Carrier
}

// QueuedRequest 是队列快照行：队列元数据 + 入队时刻的请求快照（拍板记录②：
// 出队前不重读卡，快照即入队时刻定格）。
type QueuedRequest struct {
	Kind     string
	ID       string
	Req      IgnitionRequest
	Seq      int64
	Position int
}

// SquadRows 列出全部小队（repo.List 写入序稳定），每行带 registry 版本。
func (s *Service) SquadRows() ([]SquadRow, error) {
	recs, err := s.repo.List(kindSquad)
	if err != nil {
		return nil, err
	}
	out := make([]SquadRow, 0, len(recs))
	for _, rec := range recs {
		var q Squad
		if err := json.Unmarshal(rec.Body, &q); err != nil {
			return nil, fmt.Errorf("squad/%s 解码失败: %w", rec.ID, err)
		}
		out = append(out, SquadRow{Version: rec.Version, Squad: q})
	}
	return out, nil
}

// CarrierRows 列出全部载体（repo.List 写入序稳定），每行带 registry 版本。
func (s *Service) CarrierRows() ([]CarrierRow, error) {
	recs, err := s.repo.List(kindCarrier)
	if err != nil {
		return nil, err
	}
	out := make([]CarrierRow, 0, len(recs))
	for _, rec := range recs {
		var c Carrier
		if err := json.Unmarshal(rec.Body, &c); err != nil {
			return nil, fmt.Errorf("carrier/%s 解码失败: %w", rec.ID, err)
		}
		out = append(out, CarrierRow{Version: rec.Version, Carrier: c})
	}
	return out, nil
}

// QueueSnapshot 按 QueueKinds 的清队顺序返回两个队列的全部排队请求与全局位次。
// Position 从 1 起连续计数：launch_queue 整队在前（QueueKinds 是法定清队顺序），
// 队内位次 = position() 原样取用。只读不出队——PopReady 才删除头部。
func (s *Service) QueueSnapshot() ([]QueuedRequest, error) {
	out := make([]QueuedRequest, 0)
	offset := 0 // 前序队列已占用的全局位次数
	for _, kind := range QueueKinds {
		recs, err := s.repo.List(kind)
		if err != nil {
			return nil, err
		}
		type snapRow struct {
			id  string
			req IgnitionRequest
			seq int64
			pos int
		}
		rows := make([]snapRow, 0, len(recs))
		for _, rec := range recs {
			var e queuedEntry
			if err := json.Unmarshal(rec.Body, &e); err != nil {
				continue // 与 position() 同判：损坏行不参与排位（文件头差异声明）
			}
			rows = append(rows, snapRow{id: rec.ID, req: e.Req, seq: e.Seq})
		}
		for i := range rows {
			rows[i].pos = s.position(kind, rows[i].id)
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].pos < rows[j].pos })
		for _, r := range rows {
			out = append(out, QueuedRequest{Kind: kind, ID: r.id,
				Req: r.req, Seq: r.seq, Position: offset + r.pos})
		}
		offset += len(rows)
	}
	return out, nil
}
