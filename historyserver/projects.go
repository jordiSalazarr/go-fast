package historyserver

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jordiSalazarr/go-fast/workflow/remote/wire"
)

// secretPrefix marks a project secret, so a secret scanner, or a person,
// knows one when they see it.
const secretPrefix = "gfps_"

// newSecret is a project secret: 32 random bytes.
func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return secretPrefix + hex.EncodeToString(b)
}

// CreateProject makes project id in org, created by member, and returns
// its secret: the server keeps only its hash, so this is the secret's only
// showing. Ids are the server's, across organisations.
func (s *Store) CreateProject(ctx context.Context, org, id, member string) (wire.ProjectSecret, error) {
	if !idPattern.MatchString(id) {
		return wire.ProjectSecret{}, refuse(http.StatusBadRequest, wire.ProblemInvalid, "a project's id is lowercase letters, digits and dashes, such as \"wallets\", not %q", id)
	}
	secret := newSecret()
	now := s.stamp()
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects (id, organisation, secret_sha256, created_by, created_at, secret_by, secret_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, org, sha256Of(secret), member, now, member, now)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: projects.id") {
		return wire.ProjectSecret{}, refuse(http.StatusConflict, wire.ProblemTaken, "the server has a project %s already: pick another id", id)
	}
	if err != nil {
		return wire.ProjectSecret{}, err
	}
	return wire.ProjectSecret{Project: id, Secret: secret}, nil
}

// RotateSecret gives project a new secret, which replaces the old one at
// once: every clone that syncs it needs the new one.
func (s *Store) RotateSecret(ctx context.Context, project, member string) (wire.ProjectSecret, error) {
	secret := newSecret()
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET secret_sha256 = ?, secret_by = ?, secret_at = ? WHERE id = ?`, sha256Of(secret), member, s.stamp(), project)
	if err != nil {
		return wire.ProjectSecret{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return wire.ProjectSecret{}, refuse(http.StatusNotFound, wire.ProblemNotFound, "no project %s", project)
	}
	return wire.ProjectSecret{Project: project, Secret: secret}, nil
}

// projectOfSecret is the project whose secret has hash; ok is false for
// none.
func (s *Store) projectOfSecret(ctx context.Context, hash string) (project string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE secret_sha256 = ?`, hash).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return project, err == nil, err
}

// organisationOf is the organisation project belongs to; "" for no
// project.
func (s *Store) organisationOf(ctx context.Context, project string) (string, error) {
	var org string
	err := s.db.QueryRowContext(ctx, `SELECT organisation FROM projects WHERE id = ?`, project).Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return org, err
}

// Projects are org's projects, by id.
func (s *Store) Projects(ctx context.Context, org string) ([]wire.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_by, created_at, secret_by, secret_at FROM projects WHERE organisation = ? ORDER BY id`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wire.Project{}
	for rows.Next() {
		var p wire.Project
		var created, secret string
		if err := rows.Scan(&p.ID, &p.CreatedBy, &created, &p.SecretBy, &secret); err != nil {
			return nil, err
		}
		if p.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if p.SecretAt, err = time.Parse(time.RFC3339Nano, secret); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
