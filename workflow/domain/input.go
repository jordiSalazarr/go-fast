package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// InputKind is what a work input is: the owner's written spec of a feature,
// or the owner's event model.
type InputKind struct{ name string }

var (
	// InputSpec is a markdown file describing the feature, in any structure.
	InputSpec = InputKind{name: "spec"}
	// InputEventModel is the owner's event model, in whatever form it
	// exists: JSON, markdown, an exported image or PDF of a board.
	InputEventModel = InputKind{name: "event-model"}
)

func ParseInputKind(s string) (InputKind, error) {
	for _, k := range []InputKind{InputSpec, InputEventModel} {
		if k.name == s {
			return k, nil
		}
	}
	return InputKind{}, fmt.Errorf("input kind %q (available: event-model, spec): %w", s, ErrInvalidInput)
}

func (k InputKind) String() string { return k.name }
func (k InputKind) isZero() bool   { return k.name == "" }

// ContentHash is the SHA-256 of a file's content, in lowercase hex.
type ContentHash struct{ hex string }

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// HashOf is the content hash of content.
func HashOf(content []byte) ContentHash {
	sum := sha256.Sum256(content)
	return ContentHash{hex: hex.EncodeToString(sum[:])}
}

// ParseContentHash restores a recorded hash.
func ParseContentHash(s string) (ContentHash, error) {
	if !sha256Hex.MatchString(s) {
		return ContentHash{}, fmt.Errorf("content hash %q: %w", s, ErrInvalidInput)
	}
	return ContentHash{hex: s}, nil
}

func (h ContentHash) String() string { return h.hex }
func (h ContentHash) isZero() bool   { return h.hex == "" }

// WorkInput is a file the owner started the work from: a committed file of
// the repository and its content's hash at start, so every stage reads the
// same thing and a later change shows.
type WorkInput struct {
	kind InputKind
	path RepoPath
	hash ContentHash
}

func NewWorkInput(kind InputKind, path RepoPath, hash ContentHash) (WorkInput, error) {
	if err := requirePresent(required{"input kind", kind}, required{"input path", path}, required{"input hash", hash}); err != nil {
		return WorkInput{}, fmt.Errorf("work input: %w", err)
	}
	return WorkInput{kind: kind, path: path, hash: hash}, nil
}

func (i WorkInput) Kind() InputKind   { return i.kind }
func (i WorkInput) Path() RepoPath    { return i.path }
func (i WorkInput) Hash() ContentHash { return i.hash }

// ChangedSince reports whether content differs from the input's content at
// start.
func (i WorkInput) ChangedSince(content []byte) bool { return HashOf(content) != i.hash }

// checkInputs rejects inputs a work can't start with: two of one kind, or
// an event model on a path without event modelling.
func checkInputs(path Path, inputs []WorkInput) error {
	var kinds []InputKind
	for _, in := range inputs {
		if err := requirePresent(required{"input kind", in.kind}, required{"input path", in.path}, required{"input hash", in.hash}); err != nil {
			return fmt.Errorf("work input: %w", err)
		}
		if slices.Contains(kinds, in.kind) {
			return fmt.Errorf("two %s inputs: %w", in.kind, ErrInvalidInput)
		}
		kinds = append(kinds, in.kind)
		if in.kind == InputEventModel && !path.Includes(StageEventModelling) {
			return fmt.Errorf("%s work, path %s: %w", path.workType, path.version, ErrEventModelNotOnPath)
		}
	}
	return nil
}

// ParseRepoPath restores a recorded RepoPath: clean, relative and
// slash-separated, inside the repository.
func ParseRepoPath(s string) (RepoPath, error) {
	if s == "" || s == "." || strings.HasPrefix(s, "/") || path.Clean(s) != s || s == ".." || strings.HasPrefix(s, "../") {
		return RepoPath{}, fmt.Errorf("repo path %q: %w", s, ErrInvalidRepoPath)
	}
	return RepoPath{value: s}, nil
}
