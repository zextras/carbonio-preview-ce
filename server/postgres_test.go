// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zextras/carbonio-preview-ce/v4/db"
)

func startPostgresDSN(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH — skipping DB integration tests")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker daemon not reachable (%v): %s", err, out)
	}

	const (
		pgUser  = "preview"
		pgPass  = "preview"
		pgDB    = "preview_video_test"
		pgPort  = "5432"
		imgName = "postgres:16-alpine"
	)

	out, err := exec.Command("docker", "run", "--rm", "-d",
		"-e", "POSTGRES_USER="+pgUser,
		"-e", "POSTGRES_PASSWORD="+pgPass,
		"-e", "POSTGRES_DB="+pgDB,
		"-P",
		imgName,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v — %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-fv", containerID).Run()
	})

	portOut, err := exec.Command(
		"docker", "inspect",
		"--format", fmt.Sprintf(`{{(index (index .NetworkSettings.Ports "%s/tcp") 0).HostPort}}`, pgPort),
		containerID,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect port: %v — %s", err, portOut)
	}
	hostPort := strings.TrimSpace(string(portOut))

	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%s/%s?sslmode=disable",
		pgUser, pgPass, hostPort, pgDB)
}

func startDBStore(t *testing.T) *db.Store {
	t.Helper()
	store, _ := startDBStoreWithDSN(t)
	return store
}

func startDBStoreWithDSN(t *testing.T) (*db.Store, string) {
	t.Helper()
	dsn := startPostgresDSN(t)

	ctx := context.Background()
	var store *db.Store
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, serr := db.New(ctx, dsn, db.PoolConfig{MaxConns: 5})
		if serr == nil {
			store = st
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if store == nil {
		t.Fatalf("postgres not ready after 30s (DSN: %s)", dsn)
	}
	t.Cleanup(store.Close)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store, dsn
}

func testUID(t *testing.T, suffix string) string {
	t.Helper()
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, t.Name())
	s := safe + "_" + suffix
	if len(s) > 60 {
		s = s[len(s)-60:]
	}
	return s
}
