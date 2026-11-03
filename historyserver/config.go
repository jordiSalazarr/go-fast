package historyserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
)

// Config is the server's configuration file.
type Config struct {
	// Listen is the address to listen on, e.g. ":8080". Put TLS in front.
	// Empty, gofast-server listens on $PORT, or on :8080.
	Listen string `json:"listen,omitempty"`
	// DB is the SQLite database's path.
	DB string `json:"db"`
	// Organisations are the groups of people who share the server, by id.
	Organisations map[string]Organisation `json:"organisations"`
}

// Organisation is its members. A member inspects and assesses every
// project of their organisation, and creates its projects in the viewer;
// the projects live in the database (docs/adr/0003).
type Organisation struct {
	Members map[string]Member `json:"members"`
}

// Member is a person's token, by its SHA-256, so the file holds no token.
// It reads and assesses; it never syncs.
type Member struct {
	TokenSHA256 string `json:"tokenSha256"`
}

var (
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// idPattern is a project's, an organisation's or a member's id, as it appears
	// in paths and in the assessments.
	idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// LoadConfig reads the configuration at path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(data, path)
}

// ParseConfig reads a configuration; source names it in errors.
func ParseConfig(data []byte, source string) (Config, error) {
	path := source
	var old struct {
		Projects      json.RawMessage                       `json:"projects"`
		Organisations map[string]map[string]json.RawMessage `json:"organisations"`
	}
	if json.Unmarshal(data, &old) == nil {
		if old.Projects != nil {
			return Config{}, fmt.Errorf("%s: projects are created in the viewer now, and read tokens are members' tokens: list each organisation's members, as in deploy/server.example.json", path)
		}
		for _, org := range sortedKeys(old.Organisations) {
			if _, ok := old.Organisations[org]["projects"]; ok {
				return Config{}, fmt.Errorf("%s: organisation %s lists projects: members create them in the viewer now, which makes each one's secret. Remove \"projects\" and create them there", path, org)
			}
		}
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func (c Config) validate() error {
	members := 0
	for _, o := range c.Organisations {
		members += len(o.Members)
	}
	if c.DB == "" || members == 0 {
		return fmt.Errorf("set db and at least one organisation with a member")
	}
	for _, org := range sortedKeys(c.Organisations) {
		o := c.Organisations[org]
		if !idPattern.MatchString(org) {
			return fmt.Errorf("organisation %q: ids are lowercase letters, digits and dashes", org)
		}
		for _, name := range sortedKeys(o.Members) {
			if !idPattern.MatchString(name) {
				return fmt.Errorf("member %s/%q: ids are lowercase letters, digits and dashes", org, name)
			}
			if !sha256Hex.MatchString(o.Members[name].TokenSHA256) {
				return fmt.Errorf("member %s/%s: tokenSha256 must be the token's SHA-256 in lowercase hex (printf %%s \"$TOKEN\" | shasum -a 256)", org, name)
			}
		}
	}
	_, err := newMembers(c.Organisations)
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// members are the organisations' members, by the SHA-256 of their token.
type members map[string]memberOf

type memberOf struct{ org, name string }

// newMembers indexes the organisations' members. No two have the same
// token, so a token names one member.
func newMembers(orgs map[string]Organisation) (members, error) {
	out := members{}
	for _, org := range sortedKeys(orgs) {
		for _, name := range sortedKeys(orgs[org].Members) {
			h := orgs[org].Members[name].TokenSHA256
			if other, ok := out[h]; ok {
				return nil, fmt.Errorf("members %s/%s and %s/%s have the same token: each member needs their own", other.org, other.name, org, name)
			}
			out[h] = memberOf{org: org, name: name}
		}
	}
	return out, nil
}

// sha256Of is a credential's SHA-256, as the configuration and the
// database hold it. Looking a credential up by its hash leaks nothing
// usable about it, so no constant-time compare is needed.
func sha256Of(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:])
}
