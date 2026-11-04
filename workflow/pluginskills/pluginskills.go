// Package pluginskills reads and writes the skills of the plugin directory
// a session loaded, $CLAUDE_PLUGIN_ROOT/skills/<name>/, for the skill
// improvements gf applies to them (docs/adr/0004): each skill's record,
// .applied-improvements, the approved improvements the startup check
// fetched, and the skill-improvements stream of the repository's history.
//
// A record is a file the owner edits too: one line per improvement,
//
//	<improvementId> <outcome> <before> <after|-> [<session>] [# reason]
//
// with outcomes in-progress, applied, not-applied and declined. Blank
// lines and lines starting with # are kept as they are. A line gf can't
// read counts as recorded, and gf keeps it as written.
package pluginskills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/instructions"
	"github.com/jordiSalazarr/go-fast/workflow/skillcatalog"
)

// RecordFile is a skill's record, in its directory. A dotfile: it is not
// part of the skill's instruction version.
const RecordFile = ".applied-improvements"

// vendoredMarker is the file a vendored skill carries: its upstream source.
const vendoredMarker = "SOURCE.md"

// SkillsDir is where the plugin directory keeps its skills.
func SkillsDir(pluginRoot string) string { return filepath.Join(pluginRoot, "skills") }

// SkillDir is a skill's directory.
func SkillDir(pluginRoot, skill string) string { return filepath.Join(SkillsDir(pluginRoot), skill) }

// RecordPath is a skill's record.
func RecordPath(pluginRoot, skill string) string {
	return filepath.Join(SkillDir(pluginRoot, skill), RecordFile)
}

// Version is a skill's current instruction version.
func Version(pluginRoot, skill string) (domain.InstructionsVersion, error) {
	s, err := instructions.Skill(pluginRoot, skill)
	if err != nil {
		return domain.InstructionsVersion{}, err
	}
	return s.Version, nil
}

// Exists reports whether the plugin directory has the skill.
func Exists(pluginRoot, skill string) bool {
	if skill == "" || strings.ContainsAny(skill, `/\`) || strings.HasPrefix(skill, ".") {
		return false
	}
	info, err := os.Stat(filepath.Join(SkillDir(pluginRoot, skill), "SKILL.md"))
	return err == nil && info.Mode().IsRegular()
}

// Vendored reports whether a skill carries its upstream source, so gf
// never improves it.
func Vendored(pluginRoot, skill string) bool {
	_, err := os.Stat(filepath.Join(SkillDir(pluginRoot, skill), vendoredMarker))
	return err == nil
}

// Local is every skill of the plugin directory, with its record.
func Local(pluginRoot string) ([]domain.LocalSkill, error) {
	entries, err := os.ReadDir(SkillsDir(pluginRoot))
	if err != nil {
		return nil, fmt.Errorf("read the plugin's skills: %w", err)
	}
	var out []domain.LocalSkill
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !Exists(pluginRoot, name) {
			continue
		}
		record, err := ReadRecord(pluginRoot, name)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.LocalSkill{Name: name, Vendored: Vendored(pluginRoot, name), Record: record})
	}
	return out, nil
}

// ReadRecord is a skill's record; none without the file.
func ReadRecord(pluginRoot, skill string) ([]domain.RecordEntry, error) {
	data, err := os.ReadFile(RecordPath(pluginRoot, skill))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the record of skill %s: %w", skill, err)
	}
	var out []domain.RecordEntry
	for _, l := range parse(data) {
		if l.entry != nil {
			out = append(out, *l.entry)
		}
	}
	return out, nil
}

// line is one line of a record: an entry, or text gf keeps as it is.
type line struct {
	raw   string
	entry *domain.RecordEntry
}

var outcomes = map[string]domain.ImprovementOutcome{
	"in-progress": domain.OutcomeInProgress,
	"applied":     domain.OutcomeApplied,
	"not-applied": domain.OutcomeNotApplied,
	"declined":    domain.OutcomeDeclined,
}

func outcomeWord(o domain.ImprovementOutcome) string {
	for w, known := range outcomes {
		if known == o {
			return w
		}
	}
	return ""
}

func parse(data []byte) []line {
	text := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if text == "" {
		return nil
	}
	var out []line
	for _, raw := range strings.Split(text, "\n") {
		out = append(out, line{raw: raw, entry: parseEntry(raw)})
	}
	return out
}

// parseEntry reads a line; nil for a blank line or a comment. A line gf
// can't read is an entry of its first word, unreadable.
func parseEntry(raw string) *domain.RecordEntry {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}
	body, reason, _ := strings.Cut(trimmed, " #")
	fields := strings.Fields(body)
	unreadable := &domain.RecordEntry{Improvement: domain.SkillImprovementID(fields[0]), Outcome: domain.OutcomeUnreadable}
	if len(fields) < 4 {
		return unreadable
	}
	id, err := domain.ParseSkillImprovementID(fields[0])
	if err != nil {
		return unreadable
	}
	outcome, ok := outcomes[fields[1]]
	if !ok {
		return unreadable // an outcome this gf doesn't know: recorded
	}
	before, err := domain.ParseContentHash(fields[2])
	if err != nil {
		return unreadable
	}
	e := domain.RecordEntry{Improvement: id, Outcome: outcome, Before: before, Reason: strings.TrimSpace(reason)}
	if fields[3] != "-" {
		if e.After, err = domain.ParseContentHash(fields[3]); err != nil {
			return unreadable
		}
	}
	if len(fields) > 4 {
		e.Session = domain.SessionID(fields[4]) // later fields are a newer gf's: ignored
	}
	return &e
}

func formatEntry(e domain.RecordEntry) string {
	after := "-"
	if !e.After.IsZero() {
		after = e.After.String()
	}
	s := fmt.Sprintf("%s %s %s %s", e.Improvement, outcomeWord(e.Outcome), e.Before, after)
	if e.Session != "" {
		s += " " + string(e.Session)
	}
	if reason := strings.Join(strings.Fields(e.Reason), " "); reason != "" {
		s += " # " + reason
	}
	return s
}

// render is lines with their readable entries replaced by next's, in
// place, and next's other entries appended.
func render(lines []line, next []domain.RecordEntry) []byte {
	used := make([]bool, len(next))
	take := func(id domain.SkillImprovementID) (domain.RecordEntry, bool) {
		for i, e := range next {
			if !used[i] && e.Outcome != domain.OutcomeUnreadable && e.Improvement == id {
				used[i] = true
				return e, true
			}
		}
		return domain.RecordEntry{}, false
	}
	var b strings.Builder
	for _, l := range lines {
		switch {
		case l.entry == nil || l.entry.Outcome == domain.OutcomeUnreadable:
			b.WriteString(l.raw + "\n")
		default:
			if e, ok := take(l.entry.Improvement); ok {
				b.WriteString(formatEntry(e) + "\n")
			}
		}
	}
	for i, e := range next {
		if !used[i] && e.Outcome != domain.OutcomeUnreadable {
			b.WriteString(formatEntry(e) + "\n")
		}
	}
	return []byte(b.String())
}

// Change runs decide on the skill's state, under an exclusive lock on its
// record, and writes the record the events lead to before returning them.
// With create unset, a skill without a record is left alone.
func Change(pluginRoot, skill string, create bool, decide func(domain.PluginSkill) ([]domain.Event, error)) ([]domain.Event, error) {
	path := RecordPath(pluginRoot, skill)
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, os.ErrNotExist) && !create {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the record of skill %s: %w", skill, err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock the record of skill %s: %w", skill, err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the record of skill %s: %w", skill, err)
	}
	lines := parse(data)
	var record []domain.RecordEntry
	for _, l := range lines {
		if l.entry != nil {
			record = append(record, *l.entry)
		}
	}
	state := domain.PluginSkillOf(skill, record)
	events, err := decide(state)
	if err != nil || len(events) == 0 {
		return nil, err
	}
	for _, e := range events {
		state = domain.EvolvePluginSkill(state, e)
	}
	out := render(lines, domain.Entries(state))
	if err := f.Truncate(0); err != nil {
		return nil, fmt.Errorf("write the record of skill %s: %w", skill, err)
	}
	if _, err := f.WriteAt(out, 0); err != nil {
		return nil, fmt.Errorf("write the record of skill %s: %w", skill, err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("write the record of skill %s: %w", skill, err)
	}
	return events, nil
}

// FindRecorded is the skill whose record lists id, if any.
func FindRecorded(pluginRoot string, id domain.SkillImprovementID) (string, bool, error) {
	local, err := Local(pluginRoot)
	if err != nil {
		return "", false, err
	}
	for _, l := range local {
		for _, e := range l.Record {
			if e.Improvement == id {
				return l.Name, true, nil
			}
		}
	}
	return "", false, nil
}

// fetchedPath is where the startup check keeps what it fetched, for the
// improvement agent's `gf improvement start`: local, never committed.
func fetchedPath(repoRoot string) string {
	return filepath.Join(eventlog.Dir(repoRoot), eventlog.RuntimeDir, "skill-improvements.json")
}

// Fetched is what the startup check fetched, and for which plugin
// directory.
type Fetched struct {
	PluginRoot   string
	Improvements []domain.ApprovedImprovementFact
}

type fetchedFile struct {
	PluginRoot   string            `json:"pluginRoot"`
	FetchedAt    time.Time         `json:"fetchedAt"`
	Improvements []fetchedApproved `json:"improvements"`
}

type fetchedApproved struct {
	ID         string    `json:"id"`
	Skill      string    `json:"skill"`
	Text       string    `json:"text"`
	ApprovedAt time.Time `json:"approvedAt"`
}

// SaveFetched keeps what the startup check fetched.
func SaveFetched(repoRoot string, f Fetched, at time.Time) error {
	file := fetchedFile{PluginRoot: f.PluginRoot, FetchedAt: at.UTC(), Improvements: []fetchedApproved{}}
	for _, i := range f.Improvements {
		file.Improvements = append(file.Improvements, fetchedApproved{ID: string(i.ID), Skill: i.Skill, Text: i.Text, ApprovedAt: i.ApprovedAt.UTC()})
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	path := fetchedPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("keep the fetched skill improvements: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("keep the fetched skill improvements: %w", err)
	}
	return os.Rename(tmp, path)
}

// LoadFetched is what the last startup check fetched; nothing when none
// ran.
func LoadFetched(repoRoot string) (Fetched, error) {
	data, err := os.ReadFile(fetchedPath(repoRoot))
	if errors.Is(err, os.ErrNotExist) {
		return Fetched{}, nil
	}
	if err != nil {
		return Fetched{}, fmt.Errorf("read the fetched skill improvements: %w", err)
	}
	var file fetchedFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Fetched{}, fmt.Errorf("read the fetched skill improvements: %w", err)
	}
	out := Fetched{PluginRoot: file.PluginRoot}
	for _, i := range file.Improvements {
		id, err := domain.ParseSkillImprovementID(i.ID)
		if err != nil {
			continue
		}
		out.Improvements = append(out.Improvements, domain.ApprovedImprovementFact{ID: id, Skill: i.Skill, Text: i.Text, ApprovedAt: i.ApprovedAt})
	}
	sort.SliceStable(out.Improvements, func(a, b int) bool { return out.Improvements[a].ApprovedAt.Before(out.Improvements[b].ApprovedAt) })
	return out, nil
}

// PluginRoot is the plugin directory gf improves: $CLAUDE_PLUGIN_ROOT when
// set, or else the one the startup check fetched for.
func PluginRoot(repoRoot string, getenv func(string) string) (string, error) {
	if root := getenv(skillcatalog.PluginRootEnv); root != "" {
		return root, nil
	}
	f, err := LoadFetched(repoRoot)
	if err != nil {
		return "", err
	}
	if f.PluginRoot == "" {
		return "", errors.New("gf doesn't know which plugin directory this session loaded: $CLAUDE_PLUGIN_ROOT is not set and no startup check ran")
	}
	return f.PluginRoot, nil
}

// AppendToHistory appends events to the repository's skill-improvements
// stream.
func AppendToHistory(store *eventlog.Store, actor eventlog.Actor, events []domain.Event) error {
	if len(events) == 0 {
		return nil
	}
	return store.Exclusive(func(s *eventlog.Session) error {
		records, err := s.ReadAll()
		if err != nil {
			return err
		}
		history := eventlog.NewHistory(records)
		return s.Append(eventlog.SkillImprovementsStream, history.Version(eventlog.SkillImprovementsStream), actor, events...)
	})
}

// Error is a `gf improvement` command that can't run, said for the agent.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// SessionEnv holds the Claude Code session that applies improvements: the
// SessionStart hook exports it through CLAUDE_ENV_FILE.
const SessionEnv = "GOFAST_SESSION"

// Session is the session the `gf improvement` commands act for.
func Session(getenv func(string) string) (domain.SessionID, error) {
	s := strings.TrimSpace(getenv(SessionEnv))
	if s == "" {
		return "", &Error{Message: "gf doesn't know which session runs this: $" + SessionEnv + " is set by the SessionStart hook at startup. Run `gf improvement` from the session's improvement agent."}
	}
	return domain.SessionID(s), nil
}

// RecordInHistory opens the event log and appends events to its
// skill-improvements stream.
func RecordInHistory(openStore func() (*eventlog.Store, error), actor func() eventlog.Actor, events []domain.Event) error {
	if len(events) == 0 {
		return nil
	}
	store, err := openStore()
	if err != nil {
		return err
	}
	return AppendToHistory(store, actor(), events)
}
