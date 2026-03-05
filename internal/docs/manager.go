package docs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DocInfo holds parsed metadata for a single doc file.
type DocInfo struct {
	Number int
	Title  string
	Status string
	Path   string
}

// Manager creates and lists ADR/PRD/Plan documents in a project's docs/ directory.
type Manager struct {
	root string // absolute path to project root
}

// New returns a Manager scoped to projectRoot.
func New(projectRoot string) *Manager {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		abs = projectRoot
	}
	return &Manager{root: abs}
}

// dir returns the absolute path for a given docs sub-directory.
func (m *Manager) dir(sub string) string {
	return filepath.Join(m.root, "docs", sub)
}

// ensureDir creates the directory if it does not exist.
func ensureDir(p string) error {
	return os.MkdirAll(p, 0o755)
}

// slug converts a title to a safe-for-filenames lowercase hyphenated string.
func slug(title string) string {
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s := strings.ToLower(strings.TrimSpace(title))
	return strings.Trim(re.ReplaceAllString(s, "-"), "-")
}

// nextNumber returns the next sequential number in dir (based on existing files).
func nextNumber(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	max := 0
	re := regexp.MustCompile(`^[A-Za-z]+-(\d+)-`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if n > max {
			max = n
		}
	}
	return max + 1, nil
}

// renderTemplate replaces {{DATE}}, {{TITLE}}, {{NUMBER}} in a template string.
func renderTemplate(tmpl, title string, number int, extra map[string]string) string {
	date := time.Now().Format("2006-01-02")
	out := strings.ReplaceAll(tmpl, "{{DATE}}", date)
	out = strings.ReplaceAll(out, "{{TITLE}}", title)
	out = strings.ReplaceAll(out, "{{NUMBER}}", fmt.Sprintf("%03d", number))
	for k, v := range extra {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// NewADR creates a new ADR file. Returns the created file path.
func (m *Manager) NewADR(title string) (string, error) {
	d := m.dir("adr")
	if err := ensureDir(d); err != nil {
		return "", err
	}
	n, err := nextNumber(d)
	if err != nil {
		return "", err
	}
	content := renderTemplate(adrTemplate, title, n, nil)
	fname := fmt.Sprintf("ADR-%03d-%s.md", n, slug(title))
	fpath := filepath.Join(d, fname)
	return fpath, os.WriteFile(fpath, []byte(content), 0o644)
}

// NewPRD creates a new PRD file. Returns the created file path.
func (m *Manager) NewPRD(title string) (string, error) {
	d := m.dir("prd")
	if err := ensureDir(d); err != nil {
		return "", err
	}
	n, err := nextNumber(d)
	if err != nil {
		return "", err
	}
	content := renderTemplate(prdTemplate, title, n, nil)
	fname := fmt.Sprintf("PRD-%03d-%s.md", n, slug(title))
	fpath := filepath.Join(d, fname)
	return fpath, os.WriteFile(fpath, []byte(content), 0o644)
}

// NewPlan creates a new Plan file. prdRef is the PRD id (e.g. "PRD-001").
func (m *Manager) NewPlan(title, prdRef string) (string, error) {
	d := m.dir("plans")
	if err := ensureDir(d); err != nil {
		return "", err
	}
	n, err := nextNumber(d)
	if err != nil {
		return "", err
	}
	extra := map[string]string{
		"FASE_1_TITULO": "Implementação inicial",
		"FASE_2_TITULO": "Validação e ajustes",
	}
	content := renderTemplate(planTemplate, title, n, extra)
	// Replace PRD-000 placeholder with actual reference.
	if prdRef != "" {
		content = strings.ReplaceAll(content, "PRD-000", prdRef)
	}
	fname := fmt.Sprintf("Plan-%03d-%s.md", n, slug(title))
	fpath := filepath.Join(d, fname)
	return fpath, os.WriteFile(fpath, []byte(content), 0o644)
}

// listDocs reads all .md files in dir and parses their YAML frontmatter.
func listDocs(dir string) ([]DocInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	re := regexp.MustCompile(`^[A-Za-z]+-(\d+)-`)
	var docs []DocInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		fpath := filepath.Join(dir, e.Name())
		info, err := parseFrontmatter(fpath)
		if err != nil {
			info = DocInfo{Path: fpath, Title: e.Name()}
		}
		info.Path = fpath
		m := re.FindStringSubmatch(e.Name())
		if m != nil {
			info.Number, _ = strconv.Atoi(m[1])
		}
		docs = append(docs, info)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Number < docs[j].Number })
	return docs, nil
}

// parseFrontmatter extracts title and status from YAML frontmatter.
func parseFrontmatter(fpath string) (DocInfo, error) {
	f, err := os.Open(fpath)
	if err != nil {
		return DocInfo{}, err
	}
	defer f.Close()

	var info DocInfo
	scanner := bufio.NewScanner(f)
	inFrontmatter := false
	lineNo := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		if lineNo == 1 && line == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter && line == "---" {
			break
		}
		if inFrontmatter {
			if strings.HasPrefix(line, "title:") {
				info.Title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
				info.Title = strings.Trim(info.Title, `"'`)
			} else if strings.HasPrefix(line, "status:") {
				info.Status = strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			}
		}
	}
	return info, nil
}

// ListADRs returns all ADRs in docs/adr/.
func (m *Manager) ListADRs() ([]DocInfo, error) { return listDocs(m.dir("adr")) }

// ListPRDs returns all PRDs in docs/prd/.
func (m *Manager) ListPRDs() ([]DocInfo, error) { return listDocs(m.dir("prd")) }

// ListPlans returns all plans in docs/plans/.
func (m *Manager) ListPlans() ([]DocInfo, error) { return listDocs(m.dir("plans")) }

// SupersedeADR marks ADR-N as superseded and creates a new ADR explaining the change.
// Returns the path of the new ADR.
func (m *Manager) SupersedeADR(number int, newTitle string) (string, error) {
	d := m.dir("adr")
	// Find the old ADR file.
	oldRef := fmt.Sprintf("ADR-%03d", number)
	entries, err := os.ReadDir(d)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", d, err)
	}
	var oldPath string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), oldRef) && strings.HasSuffix(e.Name(), ".md") {
			oldPath = filepath.Join(d, e.Name())
			break
		}
	}
	if oldPath == "" {
		return "", fmt.Errorf("%s not found in %s", oldRef, d)
	}

	// Update status in old ADR.
	if err := setFrontmatterField(oldPath, "status", "superseded"); err != nil {
		return "", fmt.Errorf("failed to update %s: %w", oldPath, err)
	}

	// Create new ADR with supersedes reference.
	newN, err := nextNumber(d)
	if err != nil {
		return "", err
	}
	content := renderTemplate(adrTemplate, newTitle, newN, nil)
	// Inject supersedes field into frontmatter.
	content = strings.Replace(content,
		"title: \""+newTitle+"\"",
		"title: \""+newTitle+"\"\nsupersedes: \""+oldRef+"\"",
		1)
	fname := fmt.Sprintf("ADR-%03d-%s.md", newN, slug(newTitle))
	fpath := filepath.Join(d, fname)
	return fpath, os.WriteFile(fpath, []byte(content), 0o644)
}

// setFrontmatterField updates a single YAML field in the frontmatter of a file.
func setFrontmatterField(fpath, field, value string) error {
	data, err := os.ReadFile(fpath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	prefix := field + ":"
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = fmt.Sprintf("%s: %s", field, value)
			break
		}
	}
	return os.WriteFile(fpath, []byte(strings.Join(lines, "\n")), 0o644)
}
