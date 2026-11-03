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

// Organisation is its members and its projects. A member inspects and
// assesses every project of their organisation, and no other.
type Organisation struct {
	Members  map[string]Member  `json:"members,omitempty"`
	Projects map[string]Project `json:"projects"`
}

// Member is a person's token, by its SHA-256, so the file holds no token.
// It reads and assesses; it never syncs.
type Member struct {
	TokenSHA256 string `json:"tokenSha256"`
}

// Project is the project secret, by its SHA-256: every clone of the
// project syncs with it, and it does nothing else. Each project's secret
// is its own, so the hash names the project.
type Project struct {
	SecretSHA256 string `json:"secretSha256"`
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
		Projects json.RawMessage `json:"projects"`
	}
	if json.Unmarshal(data, &old) == nil && old.Projects != nil {
		return Config{}, fmt.Errorf("%s: projects now belong to an organisation, and read tokens are members' tokens: see deploy/server.example.json "+
			"(a project's tokenSha256 becomes its secretSha256)", path)
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
	projects := 0
	for _, o := range c.Organisations {
		projects += len(o.Projects)
	}
	if c.DB == "" || projects == 0 {
		return fmt.Errorf("set db and at least one organisation with a project")
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
		for _, p := range sortedKeys(o.Projects) {
			if !idPattern.MatchString(p) {
				return fmt.Errorf("project %q: ids are lowercase letters, digits and dashes", p)
			}
			if !sha256Hex.MatchString(o.Projects[p].SecretSHA256) {
				return fmt.Errorf("project %s: secretSha256 must be the secret's SHA-256 in lowercase hex (printf %%s \"$SECRET\" | shasum -a 256)", p)
			}
		}
	}
	_, err := newAccess(c.Organisations)
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

// access says who a bearer token is: a project, by its secret, or a
// member, by their token.
type access struct {
	// orgOf is each project's organisation.
	orgOf map[string]string
	// secrets and members are by the SHA-256 of the secret or token.
	secrets map[string]string
	members map[string]memberOf
	// projects are each organisation's projects, sorted.
	projects map[string][]string
}

type memberOf struct{ org, name string }

// newAccess indexes the organisations. A project belongs to one
// organisation, and no two credentials are the same, so each names one
// project or one member.
func newAccess(orgs map[string]Organisation) (access, error) {
	a := access{orgOf: map[string]string{}, secrets: map[string]string{}, members: map[string]memberOf{}, projects: map[string][]string{}}
	for _, org := range sortedKeys(orgs) {
		o := orgs[org]
		for _, name := range sortedKeys(o.Members) {
			h := o.Members[name].TokenSHA256
			if other, ok := a.members[h]; ok {
				return access{}, fmt.Errorf("members %s/%s and %s/%s have the same token: each member needs their own", other.org, other.name, org, name)
			}
			a.members[h] = memberOf{org: org, name: name}
		}
		for _, p := range sortedKeys(o.Projects) {
			if other, ok := a.orgOf[p]; ok {
				return access{}, fmt.Errorf("project %s is in both %s and %s: a project belongs to one organisation", p, other, org)
			}
			a.orgOf[p] = org
			a.projects[org] = append(a.projects[org], p)
			h := o.Projects[p].SecretSHA256
			if other, ok := a.secrets[h]; ok {
				return access{}, fmt.Errorf("projects %s and %s have the same secret: each project needs its own", other, p)
			}
			a.secrets[h] = p
		}
	}
	for h, p := range a.secrets {
		if _, ok := a.members[h]; ok {
			return access{}, fmt.Errorf("a member's token and project %s's secret are the same: they must differ", p)
		}
	}
	return a, nil
}

// credential is who a bearer token is; both empty for nobody.
type credential struct {
	project string // the project whose secret it is
	member  memberOf
}

// of looks the token up by its SHA-256: a map lookup on a hash leaks
// nothing usable about the token, so no constant-time compare is needed.
func (a access) of(token string) credential {
	if token == "" {
		return credential{}
	}
	sum := sha256.Sum256([]byte(token))
	h := hex.EncodeToString(sum[:])
	if p, ok := a.secrets[h]; ok {
		return credential{project: p}
	}
	return credential{member: a.members[h]}
}
