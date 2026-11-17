// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package testutil starts throwaway infrastructure containers for integration tests.
// It must only be imported from _test.go files.
package testutil

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sellebeww/spillway/internal/store"
)

func start(ctx context.Context, req testcontainers.ContainerRequest) (testcontainers.Container, string, string, func(), error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		return nil, "", "", nil, err
	}
	stop := func() { _ = testcontainers.TerminateContainer(c) }
	host, err := c.Host(ctx)
	if err != nil {
		stop()
		return nil, "", "", nil, err
	}
	port, err := c.MappedPort(ctx, req.ExposedPorts[0])
	if err != nil {
		stop()
		return nil, "", "", nil, err
	}
	return c, host, port.Port(), stop, nil
}

// StartPostgres runs TimescaleDB+PostGIS and returns a URL template with %s for the database name.
func StartPostgres(ctx context.Context) (urlTemplate string, stop func(), err error) {
	_, host, port, stop, err := start(ctx, testcontainers.ContainerRequest{
		Image: "timescale/timescaledb-ha:pg17", ExposedPorts: []string{"5432/tcp"},
		Env:        map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "main"},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(3 * time.Minute),
	})
	if err != nil {
		return "", nil, err
	}
	tmpl := fmt.Sprintf("postgres://postgres:test@%s:%s/%%s?sslmode=disable", host, port)
	deadline := time.Now().Add(90 * time.Second)
	for {
		p, perr := pgxpool.New(ctx, fmt.Sprintf(tmpl, "main"))
		if perr == nil {
			perr = p.Ping(ctx)
			p.Close()
		}
		if perr == nil {
			return tmpl, stop, nil
		}
		if time.Now().After(deadline) {
			stop()
			return "", nil, fmt.Errorf("database did not become ready: %w", perr)
		}
		time.Sleep(time.Second)
	}
}

// StartRedis runs Redis and returns host:port.
func StartRedis(ctx context.Context) (addr string, stop func(), err error) {
	_, host, port, stop, err := start(ctx, testcontainers.ContainerRequest{
		Image: "redis:7-alpine", ExposedPorts: []string{"6379/tcp"},
		WaitingFor: wait.ForListeningPort("6379/tcp").WithStartupTimeout(time.Minute),
	})
	if err != nil {
		return "", nil, err
	}
	return host + ":" + port, stop, nil
}

// mosquittoConf lets clients connect anonymously and keeps generous per-client queues so
// persistent-session tests do not hit the broker's default limit of 1000 queued messages.
const mosquittoConf = `listener 1883
allow_anonymous true
persistence false
max_queued_messages 200000
max_inflight_messages 512
`

// StartMosquitto runs Eclipse Mosquitto 2 and returns tcp://host:port.
func StartMosquitto(ctx context.Context) (brokerURL string, stop func(), err error) {
	_, host, port, stop, err := start(ctx, testcontainers.ContainerRequest{
		Image: "eclipse-mosquitto:2", ExposedPorts: []string{"1883/tcp"},
		Files:      []testcontainers.ContainerFile{{Reader: strings.NewReader(mosquittoConf), ContainerFilePath: "/mosquitto/config/mosquitto.conf", FileMode: 0o644}},
		WaitingFor: wait.ForListeningPort("1883/tcp").WithStartupTimeout(time.Minute),
	})
	if err != nil {
		return "", nil, err
	}
	return "tcp://" + host + ":" + port, stop, nil
}

// DB is a migrated database shared by all tests of one test binary.
type DB struct {
	Store *store.Store
	Pool  *pgxpool.Pool
}

var (
	dbOnce sync.Once
	dbVal  *DB
	dbErr  error
	dbStop func()
)

// SharedDB starts one TimescaleDB container per test binary on first use and migrates it. Tests
// are skipped (never passed) when Docker is unavailable. Call Cleanup from TestMain.
func SharedDB(t *testing.T) *DB {
	t.Helper()
	dbOnce.Do(func() {
		ctx := context.Background()
		tmpl, stop, err := StartPostgres(ctx)
		if err != nil {
			dbErr = err
			return
		}
		dbStop = stop
		pool, err := store.NewPool(ctx, fmt.Sprintf(tmpl, "main"))
		if err == nil {
			err = store.Migrate(ctx, pool)
		}
		if err != nil {
			dbErr = err
			return
		}
		dbVal = &DB{Store: store.New(pool), Pool: pool}
	})
	if dbErr != nil {
		t.Skipf("database container unavailable (Docker?): %v", dbErr)
	}
	return dbVal
}

// Cleanup stops the shared container.
func Cleanup() {
	if dbVal != nil {
		dbVal.Pool.Close()
	}
	if dbStop != nil {
		dbStop()
	}
}
