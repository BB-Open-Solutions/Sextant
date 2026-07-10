package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
)

// Tokens exposes the token store on the pool.
func (s *Store) Tokens() *TokenStore { return &TokenStore{s} }

// TokenStore implements ports.TokenStore.
type TokenStore struct{ s *Store }

// Put implements ports.TokenStore.
func (t *TokenStore) Put(ctx context.Context, tok token.Token) error {
	groups, err := json.Marshal(tok.Groups)
	if err != nil {
		return err
	}
	_, err = t.s.pool.Exec(ctx, `
		INSERT INTO api_tokens (id, name, kind, subject, groups, ceiling, hash, created, expires, last_used)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, kind=EXCLUDED.kind, subject=EXCLUDED.subject,
			groups=EXCLUDED.groups, ceiling=EXCLUDED.ceiling, hash=EXCLUDED.hash,
			expires=EXCLUDED.expires`,
		tok.ID, tok.Name, string(tok.Kind), tok.Subject, groups, tok.Ceiling,
		tok.Hash, tok.Created, tok.Expires, tok.LastUsed)
	return err
}

func scanToken(row pgx.Row) (token.Token, error) {
	var tok token.Token
	var kind string
	var groups []byte
	err := row.Scan(&tok.ID, &tok.Name, &kind, &tok.Subject, &groups,
		&tok.Ceiling, &tok.Hash, &tok.Created, &tok.Expires, &tok.LastUsed)
	if err != nil {
		return token.Token{}, err
	}
	tok.Kind = token.Kind(kind)
	if len(groups) > 0 {
		_ = json.Unmarshal(groups, &tok.Groups)
	}
	return tok, nil
}

// Get implements ports.TokenStore.
func (t *TokenStore) Get(ctx context.Context, id string) (token.Token, bool, error) {
	tok, err := scanToken(t.s.pool.QueryRow(ctx, `
		SELECT id, name, kind, subject, groups, ceiling, hash, created, expires, last_used
		FROM api_tokens WHERE id = $1`, id))
	if err == pgx.ErrNoRows {
		return token.Token{}, false, nil
	}
	if err != nil {
		return token.Token{}, false, err
	}
	return tok, true, nil
}

// ListBySubject implements ports.TokenStore.
func (t *TokenStore) ListBySubject(ctx context.Context, subject string) ([]token.Token, error) {
	rows, err := t.s.pool.Query(ctx, `
		SELECT id, name, kind, subject, groups, ceiling, hash, created, expires, last_used
		FROM api_tokens WHERE subject = $1 ORDER BY created DESC`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []token.Token
	for rows.Next() {
		tok, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

// Delete implements ports.TokenStore.
func (t *TokenStore) Delete(ctx context.Context, id string) error {
	_, err := t.s.pool.Exec(ctx, "DELETE FROM api_tokens WHERE id = $1", id)
	return err
}

// TouchLastUsed implements ports.TokenStore.
func (t *TokenStore) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	_, err := t.s.pool.Exec(ctx, "UPDATE api_tokens SET last_used = $2 WHERE id = $1", id, at)
	return err
}
