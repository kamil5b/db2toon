//go:build integration

package db2toon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	db2toon "github.com/kamil5b/db2toon"
	"github.com/kamil5b/db2toon/pkg/dbml"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// BenchmarkDBMLPipeline compares direct PostgreSQL-to-TOON conversion with
// db2dbml and the DBML-to-TOON round trip. Install @dbml/cli so its db2dbml
// executable is on PATH before running this integration benchmark.
func BenchmarkDBMLPipeline(b *testing.B) {
	db2dbml, err := exec.LookPath("db2dbml")
	if err != nil {
		b.Skip("db2dbml not found; install it with: npm install -g @dbml/cli")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("benchmark"), postgres.WithUsername("benchmark"),
		postgres.WithPassword("benchmark"), postgres.BasicWaitStrategies())
	if err != nil {
		b.Fatalf("start PostgreSQL container: %v", err)
	}
	b.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			b.Errorf("terminate PostgreSQL container: %v", err)
		}
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		b.Fatalf("get connection string: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		b.Fatalf("connect to PostgreSQL: %v", err)
	}
	if _, err := conn.Exec(ctx, benchmarkFixture); err != nil {
		b.Fatalf("create benchmark fixture: %v", err)
	}
	if err := conn.Close(ctx); err != nil {
		b.Fatalf("close fixture connection: %v", err)
	}

	direct := runDB2TOON(b, ctx, dsn)
	dbmlText := runDB2DBML(b, ctx, db2dbml, dsn)
	roundTrip := runDBML2TOON(b, dbmlText)
	directTokens := countTokens(b, direct)
	dbmlTokens := countTokens(b, dbmlText)
	roundTripTokens := countTokens(b, roundTrip)
	b.Logf("token counts: db2toon=%s db2dbml=%s dbml2toon=%s",
		directTokens, dbmlTokens, roundTripTokens)
	if direct != roundTrip {
		b.Logf("db2toon versus dbml2toon diff:\n%s", lineDiff(direct, roundTrip))
	} else {
		b.Log("db2toon and dbml2toon outputs are identical")
	}

	b.Run("db2toon", func(b *testing.B) {
		reportTokens(b, directTokens)
		for i := 0; i < b.N; i++ {
			_ = runDB2TOON(b, context.Background(), dsn)
		}
	})
	b.Run("db2dbml", func(b *testing.B) {
		reportTokens(b, dbmlTokens)
		for i := 0; i < b.N; i++ {
			_ = runDB2DBML(b, context.Background(), db2dbml, dsn)
		}
	})
	b.Run("dbml2toon", func(b *testing.B) {
		reportTokens(b, roundTripTokens)
		for i := 0; i < b.N; i++ {
			_ = runDBML2TOON(b, dbmlText)
		}
	})
}

func runDB2TOON(tb testing.TB, ctx context.Context, dsn string) string {
	tb.Helper()
	database, err := db2toon.Extract(ctx, db2toon.Request{Dialect: "postgres", DB: dsn, Options: db2toon.Options{Schemas: []string{"public"}}})
	if err != nil {
		tb.Fatalf("db2toon extract: %v", err)
	}
	var output bytes.Buffer
	if err := db2toon.Encode(&output, database); err != nil {
		tb.Fatalf("db2toon encode: %v", err)
	}
	return output.String()
}

func runDB2DBML(tb testing.TB, ctx context.Context, binary, dsn string) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "schema.dbml")
	command := exec.CommandContext(ctx, binary, "postgres", dsn, "-o", path)
	if output, err := command.CombinedOutput(); err != nil {
		tb.Fatalf("db2dbml: %v: %s", err, output)
	}
	output, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read db2dbml output: %v", err)
	}
	return string(output)
}

func runDBML2TOON(tb testing.TB, input string) string {
	tb.Helper()
	var output bytes.Buffer
	if err := dbml.Convert(&output, strings.NewReader(input)); err != nil {
		tb.Fatalf("dbml2toon: %v", err)
	}
	return output.String()
}

var benchmarkTokens = regexp.MustCompile(`[\pL\pN_]+|[^\s\pL\pN_]`)

type tokenCounts struct {
	Local     int `json:"local"`
	OpenAI    int `json:"openai"`
	Anthropic int `json:"anthropic"`
}

func (c tokenCounts) String() string {
	return fmt.Sprintf("{local:%d openai:%d anthropic:%d}", c.Local, c.OpenAI, c.Anthropic)
}

func countTokens(tb testing.TB, input string) tokenCounts {
	tb.Helper()
	counts := tokenCounts{Local: len(benchmarkTokens.FindAllString(input, -1))}
	command := exec.Command("node", "benchmarks/token-count.mjs")
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		tb.Fatalf("run offline OpenAI/Anthropic tokenizers: %v: %s\nrun npm install first", err, output)
	}
	if err := json.Unmarshal(output, &counts); err != nil {
		tb.Fatalf("decode tokenizer output %q: %v", output, err)
	}
	return counts
}

func reportTokens(b *testing.B, counts tokenCounts) {
	b.ReportMetric(float64(counts.Local), "local_tokens")
	b.ReportMetric(float64(counts.OpenAI), "openai_tokens")
	b.ReportMetric(float64(counts.Anthropic), "anthropic_tokens")
}

func lineDiff(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	var diff strings.Builder
	limit := len(wantLines)
	if len(gotLines) > limit {
		limit = len(gotLines)
	}
	changes := 0
	for i := 0; i < limit; i++ {
		var left, right string
		if i < len(wantLines) {
			left = wantLines[i]
		}
		if i < len(gotLines) {
			right = gotLines[i]
		}
		if left == right {
			continue
		}
		changes++
		fmt.Fprintf(&diff, "@@ line %d @@\n- %s\n+ %s\n", i+1, left, right)
	}
	return fmt.Sprintf("%d differing line positions\n%s", changes, diff.String())
}

const benchmarkFixture = `
CREATE TABLE organizations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug varchar(80) NOT NULL UNIQUE,
    name text NOT NULL,
    settings jsonb NOT NULL DEFAULT '{}'
);
COMMENT ON TABLE organizations IS 'Customer organizations and their settings';
COMMENT ON COLUMN organizations.slug IS 'Stable URL-safe organization identifier';

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email varchar(255) NOT NULL,
    display_name text,
    status varchar(20) NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT users_org_email_unique UNIQUE (organization_id, email)
);
COMMENT ON TABLE users IS 'Users belonging to an organization';
COMMENT ON COLUMN users.email IS 'Login and notification address';
CREATE INDEX users_created_at_idx ON users USING btree (created_at DESC);
CREATE INDEX users_active_email_idx ON users USING btree (lower(email)) WHERE status = 'active';

CREATE TABLE projects (
    organization_id bigint NOT NULL,
    id bigint GENERATED ALWAYS AS IDENTITY,
    owner_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    code varchar(32) NOT NULL,
    name text NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (organization_id, id),
    UNIQUE (organization_id, code),
    FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE
);
COMMENT ON TABLE projects IS 'Projects use a composite organization-scoped key';
CREATE INDEX projects_owner_idx ON projects(owner_id);

CREATE TABLE tasks (
    organization_id bigint NOT NULL,
    project_id bigint NOT NULL,
    id bigint GENERATED ALWAYS AS IDENTITY,
    assignee_id bigint REFERENCES users(id) ON DELETE SET NULL,
    parent_id bigint,
    title text NOT NULL,
    priority smallint NOT NULL DEFAULT 0,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, project_id, id),
    FOREIGN KEY (organization_id, project_id) REFERENCES projects(organization_id, id) ON DELETE CASCADE,
    CONSTRAINT tasks_priority_check CHECK (priority BETWEEN 0 AND 5)
);
COMMENT ON TABLE tasks IS 'Hierarchical work items with JSON metadata';
CREATE INDEX tasks_assignee_priority_idx ON tasks(assignee_id, priority DESC);
CREATE INDEX tasks_metadata_gin_idx ON tasks USING gin(metadata);

CREATE TABLE labels (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL,
    color char(7) NOT NULL,
    UNIQUE (organization_id, name)
);
CREATE TABLE task_labels (
    organization_id bigint NOT NULL,
    project_id bigint NOT NULL,
    task_id bigint NOT NULL,
    label_id bigint NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (organization_id, project_id, task_id, label_id),
    FOREIGN KEY (organization_id, project_id, task_id) REFERENCES tasks(organization_id, project_id, id) ON DELETE CASCADE
);

INSERT INTO organizations (slug, name, settings) VALUES
 ('acme', 'Acme Corporation', '{"timezone":"UTC"}'),
 ('globex', 'Globex Corporation', '{"timezone":"Europe/Paris"}');
INSERT INTO users (organization_id, email, display_name) VALUES
 (1, 'alice@acme.test', 'Alice'), (1, 'bob@acme.test', 'Bob'),
 (2, 'carol@globex.test', 'Carol');
INSERT INTO projects (organization_id, owner_id, code, name) VALUES
 (1, 1, 'PLATFORM', 'Platform'), (1, 2, 'WEBSITE', 'Website'),
 (2, 3, 'SALES', 'Sales Tools');
INSERT INTO tasks (organization_id, project_id, assignee_id, title, priority, metadata) VALUES
 (1, 1, 1, 'Design API', 5, '{"sprint":1}'),
 (1, 1, 2, 'Implement API', 4, '{"sprint":1}'),
 (1, 2, NULL, 'Refresh home page', 2, '{"sprint":2}'),
 (2, 3, 3, 'Import leads', 3, '{"source":"csv"}');
INSERT INTO labels (organization_id, name, color) VALUES
 (1, 'backend', '#112233'), (1, 'frontend', '#abcdef'), (2, 'sales', '#ff9900');
INSERT INTO task_labels VALUES (1, 1, 1, 1), (1, 1, 2, 1), (1, 2, 1, 2), (2, 3, 1, 3);
`
