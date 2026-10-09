// Package claudehooks reads Claude Code hook input and writes hook output for
// the `gf hook <event>` commands.
//
// Field names are pinned to https://code.claude.com/docs/en/hooks
// (checked against Claude Code 2.1.286):
//   - "Common input fields": session_id, cwd, hook_event_name, agent_id and
//     agent_type (the last two only inside a subagent or with --agent).
//   - "PreToolUse input": tool_name, tool_input.
//   - "Stop input" / "SubagentStop input": stop_hook_active.
//   - "UserPromptExpansion input": command_name, command_source.
//   - "SessionStart input": source.
//   - "JSON output": systemMessage (universal); decision + reason (Stop,
//     SubagentStop); hookSpecificOutput.hookEventName with
//     permissionDecision / permissionDecisionReason (PreToolUse) or
//     additionalContext (SessionStart).
package claudehooks

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Input is the JSON Claude Code sends a hook on stdin. Only the fields gofast
// reads are declared.
type Input struct {
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	AgentID        string          `json:"agent_id,omitempty"`
	AgentType      string          `json:"agent_type,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	StopHookActive bool            `json:"stop_hook_active,omitempty"`
	CommandName    string          `json:"command_name,omitempty"`
	CommandSource  string          `json:"command_source,omitempty"`
	Source         string          `json:"source,omitempty"`
}

// InSubagent reports whether the hook fired inside a subagent.
func (in Input) InSubagent() bool { return in.AgentID != "" }

// Output is the JSON a hook prints on stdout. The zero value prints nothing.
type Output struct {
	Decision           string              `json:"decision,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	SystemMessage      string              `json:"systemMessage,omitempty"`
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type HookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// Event names, as in hook_event_name.
const (
	EventPreToolUse          = "PreToolUse"
	EventSessionStart        = "SessionStart"
	EventUserPromptExpansion = "UserPromptExpansion"
	EventSubagentStart       = "SubagentStart"
	EventSubagentStop        = "SubagentStop"
	EventStop                = "Stop"
)

// Nothing lets the action proceed without saying anything.
func Nothing() Output { return Output{} }

// Deny refuses a PreToolUse tool call.
func Deny(reason string) Output {
	return Output{HookSpecificOutput: &HookSpecificOutput{
		HookEventName: EventPreToolUse, PermissionDecision: "deny", PermissionDecisionReason: reason,
	}}
}

// Block keeps Claude (Stop) or a subagent (SubagentStop) working.
func Block(reason string) Output { return Output{Decision: "block", Reason: reason} }

// Tell lets the action proceed and shows the owner a message.
func Tell(message string) Output { return Output{SystemMessage: message} }

// Context adds text to Claude's context at session start.
func Context(event, text string) Output {
	return Output{HookSpecificOutput: &HookSpecificOutput{HookEventName: event, AdditionalContext: text}}
}

func (o Output) isEmpty() bool { return o == Output{} }

// Env is what a hook command gets from the process.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Dir    string // the --dir flag; empty to find the repository from the input's cwd
	Getenv func(string) string
}

// Repo is a repository with gofast in it, as a hook handler sees it.
type Repo struct {
	Root   string
	Store  *eventlog.Store
	Logger *slog.Logger
	Getenv func(string) string
}

// Run reads the hook input, finds the repository and runs handle. Outside a
// git repository, or in one without .gofast/, it does nothing, so the plugin
// is harmless elsewhere. It never fails: problems are logged, and the output
// printed is whatever handle decided.
func Run(env Env, handle func(Repo, Input) Output) {
	var in Input
	if err := json.NewDecoder(env.Stdin).Decode(&in); err != nil {
		return // no input, no repository to log to
	}
	root := env.Dir
	if root == "" {
		found, err := eventlog.FindRoot(in.CWD)
		if err != nil {
			return
		}
		root = found
	}
	if info, err := os.Stat(eventlog.Dir(root)); err != nil || !info.IsDir() {
		return
	}
	logger := slog.New(slog.DiscardHandler)
	if f, err := os.OpenFile(eventlog.LogFile(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		defer f.Close()
		logger = slog.New(slog.NewTextHandler(f, nil)).With("hook", in.HookEventName, "session", in.SessionID)
	}
	store, err := eventlog.Open(root, eventlog.WithLogger(logger))
	if err != nil {
		logger.Error("hook could not open the event log", "error", err.Error())
		return
	}
	out := handle(Repo{Root: root, Store: store, Logger: logger, Getenv: env.Getenv}, in)
	if out.isEmpty() {
		return
	}
	if err := json.NewEncoder(env.Stdout).Encode(out); err != nil {
		logger.Error("hook could not write its output", "error", fmt.Sprint(err))
	}
}
