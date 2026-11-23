package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
)

func TestSSHMigrationPreservesExistingCredential(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	schema := "test_migration_" + strings.ReplaceAll(cryptobox.ID(), "-", "")
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, "CREATE TABLE schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_initial.sql", "002_vpn.sql"} {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", name, fmt.Sprintf("%x", sha256.Sum256(body))); err != nil {
			t.Fatal(err)
		}
	}
	db := &Store{pool}
	u, err := db.UpsertUser(ctx, "test", "legacy", "legacy", "admin")
	if err != nil {
		t.Fatal(err)
	}
	id := cryptobox.ID()
	box, _ := cryptobox.New(bytes.Repeat([]byte{1}, 32))
	encrypted := box.Seal([]byte("legacy credential fixture"), "server:"+id)
	if _, err = pool.Exec(ctx, `INSERT INTO servers(id,name,hostname,ssh_port,ssh_user,host_key_fingerprint,encrypted_private_key,created_by) VALUES($1,'Legacy','192.0.2.10',22,'root','test',$2,$3)`, id, encrypted, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	servers, err := db.Servers(ctx)
	if err != nil || len(servers) != 1 || servers[0].SignatureMode != "off" || servers[0].SignatureRevision != 1 {
		t.Fatal("migration changed existing AWG signature", err)
	}
	target, err := db.Target(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if target.AuthMethod != "key" || target.SSHKeyID != "" || !bytes.Equal(target.EncryptedKey, encrypted) {
		t.Fatal("legacy credential changed")
	}
	plain, err := box.Open(target.EncryptedKey, "server:"+id)
	if err != nil || string(plain) != "legacy credential fixture" {
		t.Fatal("legacy credential no longer decrypts")
	}
}
