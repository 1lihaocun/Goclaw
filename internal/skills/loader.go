package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultMaxEntries       = 32
	defaultMaxInline        = 4
	defaultMaxBytesPerSkill = 4000
	defaultMaxPromptBytes   = 16000
)

type LoaderConfig struct {
	Enabled          bool
	Root             string
	MaxEntries       int
	MaxInline        int
	MaxBytesPerSkill int
	MaxPromptBytes   int
}

type Loader struct {
	EnabledFlag      bool
	Root             string
	MaxEntries       int
	MaxInline        int
	MaxBytesPerSkill int
	MaxPromptBytes   int
}

type Skill struct {
	Name        string
	Description string
	WhenToUse   string
	Path        string
	Content     string
	Always      bool
}

type Snapshot struct {
	Root         string
	AllSkills    []Skill
	InlineSkills []Skill
}

type frontmatter struct {
	Name        string
	Description string
	WhenToUse   string
	Enabled     bool
	Always      bool
}

func NewLoader(cfg LoaderConfig) *Loader {
	return &Loader{
		EnabledFlag:      cfg.Enabled,
		Root:             strings.TrimSpace(cfg.Root),
		MaxEntries:       normalizePositive(cfg.MaxEntries, defaultMaxEntries),
		MaxInline:        normalizeNonNegative(cfg.MaxInline, defaultMaxInline),
		MaxBytesPerSkill: normalizePositive(cfg.MaxBytesPerSkill, defaultMaxBytesPerSkill),
		MaxPromptBytes:   normalizePositive(cfg.MaxPromptBytes, defaultMaxPromptBytes),
	}
}

func (l *Loader) Enabled() bool {
	return l != nil && l.EnabledFlag && strings.TrimSpace(l.Root) != ""
}

func (l *Loader) LoadPromptSnapshot(_ context.Context) (Snapshot, error) {
	if !l.Enabled() {
		return Snapshot{}, nil
	}

	entries, err := os.ReadDir(l.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{Root: l.Root}, nil
		}
		return Snapshot{}, fmt.Errorf("skills: read root %s: %w", l.Root, err)
	}

	dirNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.IsDir() || isDirSymlink(l.Root, entry.Name()) {
			dirNames = append(dirNames, entry.Name())
		}
	}
	sort.Strings(dirNames)
	if l.MaxEntries > 0 && len(dirNames) > l.MaxEntries {
		dirNames = dirNames[:l.MaxEntries]
	}

	allSkills := make([]Skill, 0, len(dirNames))
	for _, dirName := range dirNames {
		skill, ok, err := l.loadSkill(dirName)
		if err != nil {
			return Snapshot{}, err
		}
		if !ok {
			continue
		}
		allSkills = append(allSkills, skill)
	}

	sort.SliceStable(allSkills, func(i, j int) bool {
		if allSkills[i].Always != allSkills[j].Always {
			return allSkills[i].Always
		}
		return strings.ToLower(allSkills[i].Name) < strings.ToLower(allSkills[j].Name)
	})

	return Snapshot{
		Root:         l.Root,
		AllSkills:    allSkills,
		InlineSkills: selectInlineSkills(allSkills, l.MaxInline, l.MaxPromptBytes),
	}, nil
}

func (l *Loader) loadSkill(dirName string) (Skill, bool, error) {
	filePath := filepath.Join(l.Root, dirName, "SKILL.md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, false, nil
		}
		return Skill{}, false, fmt.Errorf("skills: read %s: %w", filePath, err)
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return Skill{}, false, nil
	}

	meta, body := parseFrontmatter(raw)
	if !meta.Enabled {
		return Skill{}, false, nil
	}
	body = truncateContent(trimSkillBody(body, meta.Name), l.MaxBytesPerSkill)
	name := firstNonEmpty(meta.Name, extractHeading(body), dirName)
	description := firstNonEmpty(meta.Description, firstParagraph(body))

	return Skill{
		Name:        name,
		Description: description,
		WhenToUse:   meta.WhenToUse,
		Path:        l.relativePath(filePath),
		Content:     strings.TrimSpace(body),
		Always:      meta.Always,
	}, true, nil
}

func (l *Loader) relativePath(path string) string {
	base := l.Root
	if strings.EqualFold(filepath.Base(base), "skills") {
		base = filepath.Dir(base)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func selectInlineSkills(skills []Skill, maxInline int, maxPromptBytes int) []Skill {
	if maxInline <= 0 || maxPromptBytes <= 0 || len(skills) == 0 {
		return nil
	}

	candidates := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		// Default to summary-only prompt injection. Only "always" skills remain
		// eligible for inline body injection until we add explicit per-skill overrides.
		if !skill.Always || strings.TrimSpace(skill.Content) == "" {
			continue
		}
		candidates = append(candidates, skill)
	}
	if len(candidates) == 0 {
		return nil
	}

	inline := make([]Skill, 0, min(maxInline, len(candidates)))
	usedBytes := 0
	for _, skill := range candidates {
		size := approximatePromptSize(skill)
		if len(inline) > 0 && usedBytes+size > maxPromptBytes {
			continue
		}
		if size > maxPromptBytes {
			continue
		}
		inline = append(inline, skill)
		usedBytes += size
		if len(inline) >= maxInline {
			break
		}
	}
	return inline
}

func approximatePromptSize(skill Skill) int {
	return len([]rune(skill.Name)) +
		len([]rune(skill.Description)) +
		len([]rune(skill.WhenToUse)) +
		len([]rune(skill.Path)) +
		len([]rune(skill.Content))
}

func parseFrontmatter(content string) (frontmatter, string) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---\n") {
		return frontmatter{Enabled: true}, trimmed
	}
	rest := strings.TrimPrefix(trimmed, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return frontmatter{Enabled: true}, trimmed
	}
	meta := frontmatter{Enabled: true}
	for _, line := range strings.Split(rest[:end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = trimFrontmatterValue(value)
		switch key {
		case "name":
			meta.Name = value
		case "description":
			meta.Description = value
		case "when_to_use", "when-to-use":
			meta.WhenToUse = value
		case "enabled":
			meta.Enabled = parseBool(value, true)
		case "always":
			meta.Always = parseBool(value, false)
		}
	}
	return meta, strings.TrimSpace(rest[end+len("\n---\n"):])
}

func trimFrontmatterValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func trimSkillBody(body string, preferredName string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return ""
	}
	firstLine := strings.TrimSpace(lines[0])
	if strings.HasPrefix(firstLine, "#") {
		title := strings.TrimSpace(strings.TrimLeft(firstLine, "#"))
		if preferredName == "" || strings.EqualFold(title, preferredName) {
			return strings.TrimSpace(strings.Join(lines[1:], "\n"))
		}
	}
	return strings.TrimSpace(body)
}

func extractHeading(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return ""
	}
	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "#") {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(firstLine, "#"))
}

func firstParagraph(body string) string {
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if strings.HasPrefix(block, "#") {
			continue
		}
		return truncateContent(block, 280)
	}
	return ""
}

func truncateContent(content string, limit int) string {
	content = strings.TrimSpace(content)
	if limit <= 0 {
		return content
	}
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return strings.TrimSpace(string(runes[:limit])) + "\n...[truncated]"
}

func isDirSymlink(root, name string) bool {
	info, err := os.Stat(filepath.Join(root, name))
	if err != nil {
		return false
	}
	return info.IsDir()
}

func normalizePositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func normalizeNonNegative(value, fallback int) int {
	if value >= 0 {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
