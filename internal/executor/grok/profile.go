// profile.go —— Grok HOME/配置 Profile 能力（B233.2）。
//
// 职责：把隔离 HOME 的规则、技能与任务 overlay 写入 Grok 原生落点。
// 边界：不复制会话数据库或整棵 HOME；EngineOK 只由真机引擎探证决定。
package grok

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/executor"
)

const (
	RulesRelFile = ".grok/AGENTS.md"
	SkillsRelDir = ".grok/skills"
)

type Profile struct {
	log *slog.Logger
}

var _ executor.Profile = (*Profile)(nil)

func NewProfile(log *slog.Logger) *Profile {
	return &Profile{log: log}
}

// Profile 返回绑定当前 Adapter 日志器的 Profile 能力实现。
func (a *Adapter) Profile() executor.Profile {
	if a == nil {
		return &Profile{}
	}
	return &Profile{log: a.log}
}

// Prepare 将规则、技能与任务 overlay 写入 Grok 原生落点。
func (a *Adapter) Prepare(ctx context.Context, req executor.ProfileReq) (executor.ProfileReport, error) {
	return a.Profile().Prepare(ctx, req)
}

// Verify 校验 Grok 规则落点；引擎健康仍需真机探证。
func (a *Adapter) Verify(ctx context.Context, req executor.ProfileReq) (executor.ProfileReport, error) {
	return a.Profile().Verify(ctx, req)
}

func (p *Profile) Inspect(ctx context.Context, req executor.ProfileReq) (executor.ProfileReport, error) {
	rep := executor.ProfileReport{
		HomeDir:  req.HomeDir,
		Isolated: req.Isolated,
	}
	if req.HomeDir == "" {
		return rep, nil
	}
	rulePath := filepath.Join(req.HomeDir, RulesRelFile)
	if _, err := os.Stat(rulePath); errors.Is(err, os.ErrNotExist) {
		rep.Missing = append(rep.Missing, RulesRelFile)
	}
	skillsPath := filepath.Join(req.HomeDir, SkillsRelDir)
	if _, err := os.Stat(skillsPath); errors.Is(err, os.ErrNotExist) {
		rep.Missing = append(rep.Missing, SkillsRelDir)
	}
	return rep, nil
}

func (p *Profile) Prepare(ctx context.Context, req executor.ProfileReq) (executor.ProfileReport, error) {
	rep := executor.ProfileReport{
		HomeDir:  req.HomeDir,
		Isolated: req.Isolated,
	}
	if req.HomeDir == "" {
		return rep, errors.New("Profile.Prepare 缺少 HomeDir")
	}
	if p != nil && p.log != nil {
		p.log.Info("Profile.Prepare 开始", "harness", executor.HarnessGrok, "home", req.HomeDir, "isolated", req.Isolated)
	}
	nativeRoot := filepath.Join(req.HomeDir, filepath.Dir(RulesRelFile))
	if err := os.MkdirAll(nativeRoot, 0700); err != nil {
		return rep, fmt.Errorf("创建原生配置目录 %q: %w", nativeRoot, err)
	}

	for _, r := range req.Rules {
		if strings.Contains(r.Name, "..") {
			return rep, fmt.Errorf("规则文件名含非法字符: %q", r.Name)
		}
		var target string
		if strings.HasPrefix(filepath.Clean(r.Name), filepath.Dir(RulesRelFile)) {
			target = filepath.Join(req.HomeDir, filepath.Clean(r.Name))
		} else {
			target = filepath.Join(nativeRoot, filepath.Clean(r.Name))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return rep, fmt.Errorf("创建规则父目录 %q: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(r.Content), 0600); err != nil {
			return rep, fmt.Errorf("写入规则文件 %q: %w", target, err)
		}
	}

	if len(req.Skills) > 0 {
		skillsRoot := filepath.Join(req.HomeDir, SkillsRelDir)
		for _, s := range req.Skills {
			if strings.Contains(s.Name, "..") {
				return rep, fmt.Errorf("skill 文件名含非法字符: %q", s.Name)
			}
			target := filepath.Join(skillsRoot, filepath.Clean(s.Name))
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return rep, fmt.Errorf("创建 skill 父目录 %q: %w", filepath.Dir(target), err)
			}
			if err := os.WriteFile(target, []byte(s.Content), 0600); err != nil {
				return rep, fmt.Errorf("写入 skill 文件 %q: %w", target, err)
			}
		}
	}

	if len(req.TaskOverlay) > 0 {
		overlayRoot := filepath.Join(req.HomeDir, ".handoff", "task-overlay")
		for _, o := range req.TaskOverlay {
			if strings.Contains(o.Name, "..") {
				return rep, fmt.Errorf("overlay 文件名含非法字符: %q", o.Name)
			}
			target := filepath.Join(overlayRoot, filepath.Clean(o.Name))
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return rep, fmt.Errorf("创建 overlay 父目录 %q: %w", filepath.Dir(target), err)
			}
			if err := os.WriteFile(target, []byte(o.Content), 0600); err != nil {
				return rep, fmt.Errorf("写入 overlay 文件 %q: %w", target, err)
			}
		}
	}

	rep.Prepared = true
	if p != nil && p.log != nil {
		p.log.Info("Profile.Prepare 完成", "harness", executor.HarnessGrok, "prepared", true)
	}
	return rep, nil
}

func (p *Profile) Verify(ctx context.Context, req executor.ProfileReq) (executor.ProfileReport, error) {
	rep := executor.ProfileReport{
		HomeDir:  req.HomeDir,
		Isolated: req.Isolated,
		EngineOK: false,
	}
	if req.HomeDir == "" {
		return rep, nil
	}
	rulePath := filepath.Join(req.HomeDir, RulesRelFile)
	globalOK := false
	if len(req.Rules) == 0 {
		_, err := os.Stat(rulePath)
		globalOK = err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			if p != nil && p.log != nil {
				p.log.Error("Profile.Verify 读取原生规则失败", "harness", executor.HarnessGrok,
					"home", req.HomeDir, "path", rulePath, "cause", err)
			}
			return rep, fmt.Errorf("校验原生规则 %q: %w", rulePath, err)
		}
	} else {
		expected := req.Rules[0].Content
		for _, rule := range req.Rules {
			if filepath.Base(filepath.Clean(rule.Name)) == filepath.Base(RulesRelFile) {
				expected = rule.Content
			}
		}
		got, readErr := os.ReadFile(rulePath)
		if readErr != nil {
			rep.Missing = append(rep.Missing, RulesRelFile)
		} else if !bytes.Equal(got, []byte(expected)) {
			rep.Notes = append(rep.Notes, fmt.Sprintf("规则内容不符: %s", RulesRelFile))
		} else {
			globalOK = true
		}
	}
	overlayOK := true
	for _, overlay := range req.TaskOverlay {
		if strings.Contains(overlay.Name, "..") {
			overlayOK = false
			rep.Notes = append(rep.Notes, fmt.Sprintf("overlay 文件名含非法字符: %s", overlay.Name))
			continue
		}
		rel := filepath.Join(".handoff", "task-overlay", filepath.Clean(overlay.Name))
		got, readErr := os.ReadFile(filepath.Join(req.HomeDir, rel))
		if readErr != nil {
			overlayOK = false
			rep.Missing = append(rep.Missing, rel)
			continue
		}
		if !bytes.Equal(got, []byte(overlay.Content)) {
			overlayOK = false
			rep.Notes = append(rep.Notes, fmt.Sprintf("overlay 内容不符: %s", rel))
		}
	}
	rep.Verified = globalOK && overlayOK
	if p != nil && p.log != nil {
		p.log.Info("Profile.Verify 完成", "harness", executor.HarnessGrok,
			"home", req.HomeDir, "verified", rep.Verified, "engine_ok", rep.EngineOK,
			"overlay_count", len(req.TaskOverlay))
	}
	return rep, nil
}
