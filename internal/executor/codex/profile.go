package codex

import (
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
	RulesRelFile = ".codex/AGENTS.md"
	SkillsRelDir = ".codex/skills"
)

type Profile struct {
	log *slog.Logger
}

var _ executor.Profile = (*Profile)(nil)

func NewProfile(log *slog.Logger) *Profile {
	return &Profile{log: log}
}

func (a *Adapter) Profile() executor.Profile {
	if a == nil {
		return &Profile{}
	}
	return &Profile{log: a.log}
}

func (a *Adapter) Prepare(ctx context.Context, req executor.ProfileReq) (executor.ProfileReport, error) {
	return a.Profile().Prepare(ctx, req)
}

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
		p.log.Info("Profile.Prepare 开始", "harness", executor.HarnessCodex, "home", req.HomeDir, "isolated", req.Isolated)
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
		p.log.Info("Profile.Prepare 完成", "harness", executor.HarnessCodex, "prepared", true)
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
	if _, err := os.Stat(rulePath); err == nil {
		rep.Verified = true
	}
	return rep, nil
}
