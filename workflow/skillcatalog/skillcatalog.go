// Package skillcatalog reads the gofast plugin's skill catalogs,
// skills/catalog.json: per stage, the owner's skills that stage may load
// only once the work has a diagnosis.
package skillcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// Path is the catalog, relative to the plugin's root.
const Path = "skills/catalog.json"

// PluginRootEnv is the variable Claude Code sets to the plugin's root for
// the plugin's hooks (https://code.claude.com/docs/en/plugins-reference,
// "Environment variables").
const PluginRootEnv = "CLAUDE_PLUGIN_ROOT"

// file is the catalog's JSON form.
type file struct {
	Stages map[string][]string `json:"stages"`
}

// Load reads the catalog of the plugin at root and lists what is wrong
// with it: unknown stage names, and skills with no directory under
// skills/. A missing catalog is an empty one. Problems never stop it: the
// rest of the catalog still holds.
func Load(root string) (catalog domain.SkillCatalog, problems []string, err error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path)))
	if errors.Is(err, os.ErrNotExist) {
		catalog, _ = domain.NewSkillCatalog(nil)
		return catalog, nil, nil
	}
	if err != nil {
		return domain.SkillCatalog{}, nil, fmt.Errorf("read skill catalog: %w", err)
	}
	var f file
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return domain.SkillCatalog{}, nil, fmt.Errorf("%s: not valid JSON for gf: %w", Path, err)
	}
	catalog, problems = domain.NewSkillCatalog(f.Stages)
	for _, stage := range catalog.Stages() {
		for _, skill := range catalog.Skills(stage) {
			if info, err := os.Stat(filepath.Join(root, "skills", skill)); err != nil || !info.IsDir() {
				problems = append(problems, fmt.Sprintf("stage %s lists skill %q, which has no directory skills/%s/", stage, skill, skill))
			}
		}
	}
	return catalog, problems, nil
}

// SkillFile is the SKILL.md a catalog skill's name points to, as a path
// ending in it: "skills/<name>/SKILL.md".
func SkillFile(skill string) string { return "skills/" + skill + "/SKILL.md" }
