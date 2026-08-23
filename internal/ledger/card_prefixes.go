// 任务卡项目到号段前缀的分配与显式管理。前缀映射与 cards 同库，
// 由 CreateCard 的事务内路径维护；本文件不负责历史卡迁号或从卡号反推项目。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var cardPrefixPat = regexp.MustCompile(`^[A-Z]{1,4}$`)

// cardPrefixTx 返回项目已有前缀；首次建卡时按项目名首个 ASCII 字母分配。
//
// 参数：tx 当前建卡事务；project 卡的自由字符串项目名。
// 返回：已存在或新分配的前缀；前缀撞车、项目无 ASCII 字母或数据库失败时报错。
//
// 注意：调用方必须处于 mutate 的写事务内。这样前缀占用与卡行插入同进同退，
// 并发首建不会出现两个项目同时看见同一个空闲前缀。
func (s *Store) cardPrefixTx(tx *sql.Tx, project string) (string, error) {
	var prefix string
	err := tx.QueryRow(s.q(`SELECT prefix FROM card_prefixes WHERE project = ?`), project).Scan(&prefix)
	if err == nil {
		return prefix, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("读项目 %q 的卡号前缀: %w", project, err)
	}

	candidate := firstASCIILetter(project)
	if candidate == "" {
		log().Warn("建卡被拒：项目名没有 ASCII 字母", "project", project)
		return "", fmt.Errorf("项目 %q 没有 ASCII 字母，无法自动分配前缀；请先执行 `handoff card prefix %s <前缀>`", project, project)
	}
	var owner string
	err = tx.QueryRow(s.q(`SELECT project FROM card_prefixes WHERE prefix = ?`), candidate).Scan(&owner)
	if err == nil {
		log().Warn("建卡被拒：自动前缀已占用", "project", project, "prefix", candidate, "owner", owner)
		return "", fmt.Errorf("项目 %q 的自动前缀 %s 已被项目 %q 占用，请先执行 `handoff card prefix %s <前缀>`",
			project, candidate, owner, project)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("查卡号前缀 %s 的占用方: %w", candidate, err)
	}
	if _, err := tx.Exec(s.q(`INSERT INTO card_prefixes (project, prefix) VALUES (?, ?)`), project, candidate); err != nil {
		return "", fmt.Errorf("分配项目 %q 的卡号前缀 %s: %w", project, candidate, err)
	}
	log().Info("自动分配卡号前缀", "project", project, "prefix", candidate)
	return candidate, nil
}

// firstASCIILetter 取 project 中第一个 ASCII 字母并转成大写。
// 非 ASCII 字母不参与推导，刻意让中文名/纯数字名走显式配置，避免
// 不透明的拼音或随机回退破坏「看到前缀即可定位项目」的约定。
func firstASCIILetter(project string) string {
	for i := 0; i < len(project); i++ {
		c := project[i]
		if c >= 'a' && c <= 'z' {
			return string(c - ('a' - 'A'))
		}
		if c >= 'A' && c <= 'Z' {
			return string(c)
		}
	}
	return ""
}

// SetCardPrefix 为项目设置或修改卡号前缀。
//
// 参数：project 自由字符串项目名；prefix 为 1~4 个大写 ASCII 字母。
// 返回：映射落库成功返回 nil；格式非法、前缀占用或项目已有卡时报错。
//
// 注意：已有卡的项目禁止修改，即使新前缀与旧前缀相同也只允许在无卡时
// 幂等重放。前缀一旦随卡号发出就不能迁移，避免已发出的卡号变成孤儿。
func (s *Store) SetCardPrefix(project, prefix string) error {
	log().Info("开始设置卡号前缀", "project", project, "prefix", prefix)
	if strings.TrimSpace(project) == "" {
		log().Warn("设置卡号前缀被拒：项目名为空", "prefix", prefix)
		return fmt.Errorf("项目名不能为空")
	}
	if !cardPrefixPat.MatchString(prefix) {
		log().Warn("设置卡号前缀被拒：前缀格式非法", "project", project, "prefix", prefix)
		return fmt.Errorf("卡号前缀 %q 非法，必须是 1~4 个大写 ASCII 字母", prefix)
	}
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var cardCount int
		if err := tx.QueryRow(s.q(`SELECT COUNT(*) FROM cards WHERE project = ?`), project).Scan(&cardCount); err != nil {
			return fmt.Errorf("检查项目 %q 是否已有卡: %w", project, err)
		}
		if cardCount > 0 {
			log().Warn("设置卡号前缀被拒：项目已有卡", "project", project, "prefix", prefix, "cards", cardCount)
			return fmt.Errorf("项目 %q 已有卡，不能修改卡号前缀", project)
		}

		var owner string
		err := tx.QueryRow(s.q(`SELECT project FROM card_prefixes WHERE prefix = ?`), prefix).Scan(&owner)
		if err == nil && owner != project {
			log().Warn("设置卡号前缀被拒：前缀已占用", "project", project, "prefix", prefix, "owner", owner)
			return fmt.Errorf("卡号前缀 %s 已被项目 %q 占用", prefix, owner)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("查卡号前缀 %s 的占用方: %w", prefix, err)
		}

		var existing string
		err = tx.QueryRow(s.q(`SELECT prefix FROM card_prefixes WHERE project = ?`), project).Scan(&existing)
		switch {
		case err == nil:
			if existing == prefix {
				log().Info("卡号前缀设置幂等完成", "project", project, "prefix", prefix)
				return nil
			}
			if _, err := tx.Exec(s.q(`UPDATE card_prefixes SET prefix = ? WHERE project = ?`), prefix, project); err != nil {
				return fmt.Errorf("更新项目 %q 的卡号前缀: %w", project, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(s.q(`INSERT INTO card_prefixes (project, prefix) VALUES (?, ?)`), project, prefix); err != nil {
				return fmt.Errorf("写入项目 %q 的卡号前缀: %w", project, err)
			}
		default:
			return fmt.Errorf("读项目 %q 的卡号前缀: %w", project, err)
		}
		log().Info("卡号前缀设置完成", "project", project, "prefix", prefix)
		return nil
	})
	if err != nil {
		log().Error("设置卡号前缀失败", "project", project, "prefix", prefix, "cause", err)
	}
	return err
}
