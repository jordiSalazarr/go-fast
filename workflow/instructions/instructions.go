// Package instructions reads the versions of the gofast plugin's skills and
// agent definitions, and keeps a copy of each version in the repository,
// under .gofast/instructions/<version>/, so the event log's versions can
// always be read back.
//
// A version is the SHA-256 of a manifest: one line per file, sorted by
// path, as sha256sum prints it ("<sha256>  <path>\n"). In a kept copy,
//
//	find . -type f | sed 's|^\./||' | LC_ALL=C sort | xargs sha256sum | sha256sum
//
// prints the directory's own name.
package instructions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Snapshot is one version of a skill or an agent definition: its files,
// by slash-separated path relative to the skill's directory (an agent's
// one file by its name).
type Snapshot struct {
	Version domain.InstructionsVersion
	Files   map[string][]byte
}

// Of versions files.
func Of(files map[string][]byte) Snapshot {
	return Snapshot{Version: domain.HashOf(Manifest(files)), Files: files}
}

// Manifest lists files as sha256sum does, sorted by path.
func Manifest(files map[string][]byte) []byte {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b bytes.Buffer
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), p)
	}
	return b.Bytes()
}

// Skill reads the plugin's skill name, in skills/<name>/ under the plugin's
// root. Files and directories whose name starts with "." (.DS_Store, editor
// files) are not part of it.
func Skill(pluginRoot, name string) (Snapshot, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return Snapshot{}, fmt.Errorf("skill %q: not a skill name", name)
	}
	dir := filepath.Join(pluginRoot, "skills", name)
	files, err := readDir(dir)
	if err != nil {
		return Snapshot{}, fmt.Errorf("skill %s: %w", name, err)
	}
	if len(files) == 0 {
		return Snapshot{}, fmt.Errorf("skill %s: %s has no files: %w", name, dir, fs.ErrNotExist)
	}
	return Of(files), nil
}

func readDir(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if abs != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, abs)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = content
		return nil
	})
	return files, err
}

// Agent is a version of one of the plugin's agent definitions, and the
// skills its frontmatter preloads, as written (gofast:<skill>).
type Agent struct {
	Snapshot
	Preloads []string
}

// AgentDefinition reads the plugin's agent name, agents/<name>.md under the
// plugin's root.
func AgentDefinition(pluginRoot, name string) (Agent, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return Agent{}, fmt.Errorf("agent %q: not an agent name", name)
	}
	file := name + ".md"
	content, err := os.ReadFile(filepath.Join(pluginRoot, "agents", file))
	if err != nil {
		return Agent{}, fmt.Errorf("agent %s: %w", name, err)
	}
	preloads, err := frontmatterSkills(content)
	if err != nil {
		return Agent{}, fmt.Errorf("agent %s: %w", name, err)
	}
	return Agent{Snapshot: Of(map[string][]byte{file: content}), Preloads: preloads}, nil
}

// frontmatterSkills is the skills field of a Markdown file's YAML
// frontmatter (https://code.claude.com/docs/en/sub-agents, "Supported
// frontmatter fields").
func frontmatterSkills(content []byte) ([]string, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return nil, nil
	}
	front, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return nil, errors.New("frontmatter has no closing ---")
	}
	var fields struct {
		Skills []string `yaml:"skills"`
	}
	if err := yaml.Unmarshal([]byte(front), &fields); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	return fields.Skills, nil
}

// PluginVersion is the version the plugin's manifest declares; empty when
// it has none or gf can't read it.
func PluginVersion(pluginRoot string) string {
	data, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Version
}

// GfVersion is gf's build: its module version, and the commit it was built
// from when Go recorded it ("+dirty" when built with uncommitted changes).
// Empty when the binary has no build information.
func GfVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return buildVersion(info)
}

func buildVersion(info *debug.BuildInfo) string {
	version := info.Main.Version
	var revision string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision != "" && (version == "" || version == "(devel)") {
		version = revision[:min(12, len(revision))]
		if dirty {
			version += "+dirty"
		}
	}
	if version == "(devel)" {
		return ""
	}
	return version
}

// Path is where the kept versions live, relative to the repository's root.
func Path() string { return eventlog.InstructionsPath() }

// Keep copies a version into the repository, unless it is already there.
// The copy appears whole or not at all.
func Keep(root string, s Snapshot) error {
	if s.Version.IsZero() {
		return errors.New("keep instructions: no version")
	}
	base := filepath.Join(root, filepath.FromSlash(Path()))
	dir := filepath.Join(base, s.Version.String())
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("keep instructions: %w", err)
	}
	tmp, err := os.MkdirTemp(base, ".tmp-")
	if err != nil {
		return fmt.Errorf("keep instructions: %w", err)
	}
	defer os.RemoveAll(tmp)
	for p, content := range s.Files {
		abs := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("keep instructions: %w", err)
		}
		if err := os.WriteFile(abs, content, 0o644); err != nil {
			return fmt.Errorf("keep instructions: %w", err)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return nil // another hook kept it first
		}
		return fmt.Errorf("keep instructions: %w", err)
	}
	return nil
}

// Read reads a kept version back; ok is false when it isn't kept.
func Read(root string, version domain.InstructionsVersion) (s Snapshot, ok bool, err error) {
	dir := filepath.Join(root, filepath.FromSlash(Path()), version.String())
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	files, err := readDir(dir)
	if err != nil {
		return Snapshot{}, false, err
	}
	return Of(files), true, nil
}

// Verify lists the kept versions whose files no longer match their name:
// a copy changed after gf kept it. kept is how many there are.
func Verify(root string) (kept int, problems []string, err error) {
	base := filepath.Join(root, filepath.FromSlash(Path()))
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		version, parseErr := domain.ParseContentHash(e.Name())
		if parseErr != nil || !e.IsDir() {
			problems = append(problems, fmt.Sprintf("%s/%s is not a kept version", Path(), e.Name()))
			continue
		}
		kept++
		s, _, err := Read(root, version)
		if err != nil {
			return 0, nil, err
		}
		if s.Version != version {
			problems = append(problems, fmt.Sprintf("%s/%s changed after gf kept it (its files hash to %s)", Path(), e.Name(), s.Version.Short()))
		}
	}
	return kept, problems, nil
}
