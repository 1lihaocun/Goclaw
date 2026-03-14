package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Summary struct {
	Enabled bool           `json:"enabled"`
	Root    string         `json:"root,omitempty"`
	Total   int            `json:"total"`
	Inline  int            `json:"inline"`
	Skills  []SkillSummary `json:"skills"`
}

type CheckSummary struct {
	Enabled    bool           `json:"enabled"`
	Root       string         `json:"root,omitempty"`
	RootExists bool           `json:"root_exists"`
	Total      int            `json:"total"`
	Active     int            `json:"active"`
	Inline     int            `json:"inline"`
	Disabled   int            `json:"disabled"`
	Skipped    int            `json:"skipped"`
	Skills     []SkillSummary `json:"skills"`
}

type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	WhenToUse   string `json:"when_to_use,omitempty"`
	Path        string `json:"path,omitempty"`
	Enabled     bool   `json:"enabled"`
	Always      bool   `json:"always,omitempty"`
	Inline      bool   `json:"inline,omitempty"`
	Status      string `json:"status,omitempty"`
}

func Inspect(ctx context.Context, cfg LoaderConfig) (Summary, error) {
	report, err := inspectState(ctx, cfg)
	if err != nil {
		return Summary{}, err
	}
	enabledSkills := make([]SkillSummary, 0, report.Active)
	for _, skill := range report.Skills {
		if !skill.Enabled {
			continue
		}
		enabledSkills = append(enabledSkills, skill)
	}
	return Summary{
		Enabled: report.Enabled,
		Root:    report.Root,
		Total:   len(enabledSkills),
		Inline:  report.Inline,
		Skills:  enabledSkills,
	}, nil
}

func Check(ctx context.Context, cfg LoaderConfig) (CheckSummary, error) {
	return inspectState(ctx, cfg)
}

func Info(ctx context.Context, cfg LoaderConfig, name string) (SkillSummary, error) {
	report, err := inspectState(ctx, cfg)
	if err != nil {
		return SkillSummary{}, err
	}
	needle := strings.TrimSpace(strings.ToLower(name))
	if needle == "" {
		return SkillSummary{}, errors.New("missing skill name")
	}
	for _, skill := range report.Skills {
		if strings.TrimSpace(strings.ToLower(skill.Name)) == needle {
			return skill, nil
		}
	}
	return SkillSummary{}, fmt.Errorf("skill %q not found", name)
}

func inspectState(ctx context.Context, cfg LoaderConfig) (CheckSummary, error) {
	loader := NewLoader(cfg)
	report := CheckSummary{
		Enabled: loader.Enabled(),
		Root:    loader.Root,
		Skills:  []SkillSummary{},
	}

	root := strings.TrimSpace(loader.Root)
	if root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			report.RootExists = true
		} else if err != nil && !os.IsNotExist(err) {
			return CheckSummary{}, fmt.Errorf("skills: stat root %s: %w", root, err)
		}
	}
	if !loader.Enabled() || !report.RootExists {
		return report, nil
	}

	snapshot, err := loader.LoadPromptSnapshot(ctx)
	if err != nil {
		return CheckSummary{}, err
	}

	activeByPath := make(map[string]Skill, len(snapshot.AllSkills))
	for _, skill := range snapshot.AllSkills {
		activeByPath[skill.Path] = skill
	}
	inlineSet := make(map[string]struct{}, len(snapshot.InlineSkills))
	for _, skill := range snapshot.InlineSkills {
		inlineSet[skill.Path] = struct{}{}
	}

	entries, err := os.ReadDir(loader.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return CheckSummary{}, fmt.Errorf("skills: read root %s: %w", loader.Root, err)
	}

	dirNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.IsDir() || isDirSymlink(loader.Root, entry.Name()) {
			dirNames = append(dirNames, entry.Name())
		}
	}
	sort.Strings(dirNames)
	if loader.MaxEntries > 0 && len(dirNames) > loader.MaxEntries {
		dirNames = dirNames[:loader.MaxEntries]
	}

	skills := make([]SkillSummary, 0, len(dirNames))
	for _, dirName := range dirNames {
		skillView, ok, err := inspectDir(loader, dirName, activeByPath, inlineSet)
		if err != nil {
			return CheckSummary{}, err
		}
		if !ok {
			continue
		}
		skills = append(skills, skillView)
		switch skillView.Status {
		case "disabled":
			report.Disabled++
		case "skipped":
			report.Skipped++
		default:
			report.Active++
			if skillView.Inline {
				report.Inline++
			}
		}
	}

	report.Total = len(skills)
	report.Root = snapshot.Root
	report.Skills = skills
	return report, nil
}

func inspectDir(
	loader *Loader,
	dirName string,
	activeByPath map[string]Skill,
	inlineSet map[string]struct{},
) (SkillSummary, bool, error) {
	filePath := filepath.Join(loader.Root, dirName, "SKILL.md")
	relativePath := loader.relativePath(filePath)

	if skill, ok := activeByPath[relativePath]; ok {
		_, inline := inlineSet[relativePath]
		return SkillSummary{
			Name:        skill.Name,
			Description: skill.Description,
			WhenToUse:   skill.WhenToUse,
			Path:        skill.Path,
			Enabled:     true,
			Always:      skill.Always,
			Inline:      inline,
			Status:      "enabled",
		}, true, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return SkillSummary{
				Name:    dirName,
				Path:    relativePath,
				Enabled: false,
				Status:  "skipped",
			}, true, nil
		}
		return SkillSummary{}, false, fmt.Errorf("skills: read %s: %w", filePath, err)
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return SkillSummary{
			Name:    dirName,
			Path:    relativePath,
			Enabled: false,
			Status:  "skipped",
		}, true, nil
	}

	meta, body := parseFrontmatter(raw)
	body = trimSkillBody(body, meta.Name)
	name := firstNonEmpty(meta.Name, extractHeading(body), dirName)
	description := firstNonEmpty(meta.Description, firstParagraph(body))
	status := "disabled"
	if meta.Enabled {
		status = "skipped"
	}
	return SkillSummary{
		Name:        name,
		Description: description,
		WhenToUse:   meta.WhenToUse,
		Path:        relativePath,
		Enabled:     false,
		Always:      meta.Always,
		Status:      status,
	}, true, nil
}

func ResolveRoot(workspaceRoot, configuredRoot string) string {
	if root := firstNonEmpty(configuredRoot); root != "" {
		return root
	}
	if workspace := firstNonEmpty(workspaceRoot); workspace != "" {
		return filepath.Join(workspace, "skills")
	}
	return ""
}
