package ingest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/store"
	"github.com/sellebeww/spillway/internal/testutil"
)

// The integration tests share one Postgres, one Redis and one Mosquitto, started on first use,
// so the unit tests (fake backend, goleak) run without Docker. Without Docker the integration
// tests are skipped with a message, never reported as passed.
type testEnv struct {
	pgTmpl    string
	redisAddr string
	mqttURL   string
	pool      *pgxpool.Pool
	st        *store.Store
	cipher    *auth.Cipher
	stops     []func()
}

var (
	envOnce sync.Once
	envVal  *testEnv
	envErr  error
)

func getEnv(t *testing.T) *testEnv {
	t.Helper()
	envOnce.Do(func() {
		ctx := context.Background()
		e := &testEnv{}
		var stop func()
		var err error
		if e.pgTmpl, stop, err = testutil.StartPostgres(ctx); err != nil {
			envErr = fmt.Errorf("postgres: %w", err)
			return
		}
		e.stops = append(e.stops, stop)
		if e.redisAddr, stop, err = testutil.StartRedis(ctx); err != nil {
			envErr = fmt.Errorf("redis: %w", err)
			e.close()
			return
		}
		e.stops = append(e.stops, stop)
		if e.mqttURL, stop, err = testutil.StartMosquitto(ctx); err != nil {
			envErr = fmt.Errorf("mosquitto: %w", err)
			e.close()
			return
		}
		e.stops = append(e.stops, stop)
		if e.pool, err = store.NewPool(ctx, fmt.Sprintf(e.pgTmpl, "main")); err == nil {
			err = store.Migrate(ctx, e.pool)
		}
		if err != nil {
			envErr = fmt.Errorf("migrate: %w", err)
			e.close()
			return
		}
		e.st = store.New(e.pool)
		e.cipher, _ = auth.NewCipher(1, bytes.Repeat([]byte{9}, 32), nil)
		envVal = e
	})
	if envErr != nil {
		t.Skipf("integration environment unavailable (Docker?): %v", envErr)
	}
	return envVal
}

func (e *testEnv) close() {
	if e.pool != nil {
		e.pool.Close()
	}
	for i := len(e.stops) - 1; i >= 0; i-- {
		e.stops[i]()
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if envVal != nil {
		envVal.close()
	}
	os.Exit(code)
}
