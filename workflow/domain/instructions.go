package domain

import "fmt"

// The instructions a stage visit works from are the plugin's skills and
// agent definitions. Both change as the owner refines them, so gf records
// which version of each a visit started with: its InstructionsVersion, the
// content hash of its manifest. A skill's manifest lists every file of its
// directory and an agent's its one file, so any change to them is a new
// version.

// SkillLoad says how a skill reached a stage visit. The zero value is a
// load recorded before gf told them apart; those were all invoked.
type SkillLoad struct{ name string }

var (
	// SkillInvoked: the agent loaded the skill with the Skill tool.
	SkillInvoked = SkillLoad{name: "invoked"}
	// SkillPreloaded: the agent's definition preloaded the skill when the
	// agent started.
	SkillPreloaded = SkillLoad{name: "preloaded"}
)

// ParseSkillLoad restores a recorded load; empty is the zero value.
func ParseSkillLoad(s string) (SkillLoad, error) {
	switch s {
	case "":
		return SkillLoad{}, nil
	case SkillInvoked.name:
		return SkillInvoked, nil
	case SkillPreloaded.name:
		return SkillPreloaded, nil
	}
	return SkillLoad{}, fmt.Errorf("skill load %q (available: invoked, preloaded): %w", s, ErrInvalidInput)
}

// Name is the load as recorded: empty for the zero value.
func (l SkillLoad) Name() string { return l.name }

func (l SkillLoad) String() string {
	if l.name == "" {
		return SkillInvoked.name
	}
	return l.name
}

// InstructionsVersion is the content hash of a skill's or an agent's
// manifest. The zero value is a version gf could not read: another
// plugin's skill, or a plugin gf could not find.
type InstructionsVersion = ContentHash

// AgentStarted observes a stage agent starting on a stage visit, with the
// versions of what instructs it: its definition (Version), the plugin's
// declared version and gf's build. The skills it preloads are recorded as
// SkillLoaded events next to it. Like SkillLoaded, it is no aggregate's
// event and lives on the work's observations stream.
type AgentStarted struct {
	WorkID        WorkID
	Stage         Stage
	Visit         Visit
	Agent         string // as Claude Code names it, e.g. "gofast:specify"
	Version       InstructionsVersion
	PluginVersion string // plugin.json's version; empty when unknown
	GfVersion     string // gf's build; empty when unknown
}

func (AgentStarted) isEvent() {}

// IsZero reports whether the hash is unknown.
func (h ContentHash) IsZero() bool { return h.isZero() }
