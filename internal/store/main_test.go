package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sellebeww/spillway/internal/auth"
)

var (
	testPool    *pgxpool.Pool
	testBaseURL string // postgres URL template with %s for the database name
	skipReason  string
)

// TestMain starts one TimescaleDB+PostGIS container for the whole package. If Docker is not
// available the integration tests are skipped (and reported as skipped, never as passed).
// SPILLWAY_TEST_DATABASE_URL (a throwaway database, URL template with %s) can replace the container.
func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	ctx := context.Background()
	base := os.Getenv("SPILLWAY_TEST_DATABASE_URL")
	if base == "" {
		var stop func()
		var err error
		base, stop, err = startPostgres(ctx)
		if err != nil {
			skipReason = "docker unavailable: " + err.Error()
			fmt.Fprintln(os.Stderr, "SKIPPING integration tests:", skipReason)
			return m.Run()
		}
		defer stop()
	}
	testBaseURL = base
	pool, err := NewPool(ctx, fmt.Sprintf(base, "main"))
	if err == nil {
		err = Migrate(ctx, pool)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL: prepare test database:", err)
		return 1
	}
	testPool = pool
	defer pool.Close()
	return m.Run()
}

func startPostgres(ctx context.Context) (urlTemplate string, stop func(), err error) {
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "timescale/timescaledb-ha:pg17",
			ExposedPorts: []string{"5432/tcp"},
			Env:          map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "main"},
			WaitingFor:   wait.ForListeningPort("5432/tcp").WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	}
	c, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		return "", nil, err
	}
	stop = func() { _ = testcontainers.TerminateContainer(c) }
	host, err := c.Host(ctx)
	if err != nil {
		stop()
		return "", nil, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		stop()
		return "", nil, err
	}
	tmpl := fmt.Sprintf("postgres://postgres:test@%s:%s/%%s?sslmode=disable", host, port.Port())

	// The port can open before the final server is ready: poll until a real query works.
	deadline := time.Now().Add(90 * time.Second)
	for {
		p, perr := NewPool(ctx, fmt.Sprintf(tmpl, "main"))
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

func requireDB(t *testing.T) *Store {
	t.Helper()
	if testPool == nil {
		t.Skip(skipReason)
	}
	return New(testPool)
}

func testCipher(t *testing.T) *auth.Cipher {
	t.Helper()
	c, err := auth.NewCipher(1, bytes.Repeat([]byte{7}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// newOrg creates an isolated tenant for one test.
func newOrg(t *testing.T, s *Store) *OrgStore {
	t.Helper()
	org, err := s.CreateOrg(context.Background(), "t-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "Test Org")
	if err != nil {
		t.Fatal(err)
	}
	return s.ForOrg(org.ID)
}

type fixture struct {
	Site   Site
	Device Device
	Secret string
}

// newFixture creates a site and a device in o.
func newFixture(t *testing.T, o *OrgStore) fixture {
	t.Helper()
	ctx := context.Background()
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	site, err := o.CreateSite(ctx, SiteInput{Slug: "s-" + tag, Name: "Site " + tag, Lat: -6.2, Lon: 106.8})
	if err != nil {
		t.Fatal(err)
	}
	dev, secret, err := o.RegisterDevice(ctx, testCipher(t), DeviceInput{SiteID: site.ID, Key: "d-" + tag, Name: "Device " + tag})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{Site: site, Device: dev, Secret: secret}
}

func countRows(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
