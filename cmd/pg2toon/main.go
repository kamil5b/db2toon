package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/jackc/pgx/v5"
	"github.com/kamil5b/pgschema2toon/pkg/toon"
)

const extractQuery = `
SELECT COALESCE(jsonb_agg(table_info ORDER BY table_name), '[]'::jsonb)
FROM (
    SELECT
        t.relname AS table_name,
        jsonb_strip_nulls(jsonb_build_object(
            'table', t.relname,
            'comment', NULLIF(obj_description(t.oid, 'pg_class'), ''),
            'columns', (
                SELECT COALESCE(jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
                    'name', a.attname,
                    'type', format_type(a.atttypid, a.atttypmod),
                    'comment', NULLIF(col_description(t.oid, a.attnum), ''),
                    'nullable', NOT a.attnotnull,
                    'is_pk', EXISTS (
                        SELECT 1
                        FROM pg_index i
                        WHERE i.indrelid = t.oid
                          AND a.attnum = ANY(i.indkey)
                          AND i.indisprimary
                    ),
                    'default', pg_get_expr(ad.adbin, ad.adrelid),
                    'identity', NULLIF(a.attidentity, ''),
                    'generated', NULLIF(a.attgenerated, '')
                )) ORDER BY a.attnum), '[]'::jsonb)
                FROM pg_attribute a
                LEFT JOIN pg_attrdef ad
                  ON ad.adrelid = a.attrelid
                 AND ad.adnum = a.attnum
                WHERE a.attrelid = t.oid
                  AND a.attnum > 0
                  AND NOT a.attisdropped
            ),
            'constraints', (
                SELECT COALESCE(jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
                    'name', c.conname,
                    'def', pg_get_constraintdef(c.oid),
                    'local_columns', (
                        SELECT jsonb_agg(a.attname ORDER BY k.ordinality)
                        FROM unnest(c.conkey) WITH ORDINALITY AS k(attnum, ordinality)
                        JOIN pg_attribute a
                          ON a.attrelid = c.conrelid
                         AND a.attnum = k.attnum
                    ),
                    'referenced_schema', rn.nspname,
                    'referenced_table', rt.relname,
                    'referenced_columns', (
                        SELECT jsonb_agg(a.attname ORDER BY k.ordinality)
                        FROM unnest(c.confkey) WITH ORDINALITY AS k(attnum, ordinality)
                        JOIN pg_attribute a
                          ON a.attrelid = c.confrelid
                         AND a.attnum = k.attnum
                    ),
                    'on_update', CASE c.confupdtype
                        WHEN 'a' THEN 'NO ACTION'
                        WHEN 'r' THEN 'RESTRICT'
                        WHEN 'c' THEN 'CASCADE'
                        WHEN 'n' THEN 'SET NULL'
                        WHEN 'd' THEN 'SET DEFAULT'
                    END,
                    'on_delete', CASE c.confdeltype
                        WHEN 'a' THEN 'NO ACTION'
                        WHEN 'r' THEN 'RESTRICT'
                        WHEN 'c' THEN 'CASCADE'
                        WHEN 'n' THEN 'SET NULL'
                        WHEN 'd' THEN 'SET DEFAULT'
                    END
                )) ORDER BY c.conname), '[]'::jsonb)
                FROM pg_constraint c
                JOIN pg_class rt ON rt.oid = c.confrelid
                JOIN pg_namespace rn ON rn.oid = rt.relnamespace
                WHERE c.conrelid = t.oid
                  AND c.contype = 'f'
            ),
            'indexes', (
                SELECT COALESCE(jsonb_agg(jsonb_build_object(
                    'name', i.relname,
                    'def', pg_get_indexdef(i.oid),
                    'unique', x.indisunique,
                    'constraint_backed', EXISTS (
                        SELECT 1 FROM pg_constraint c WHERE c.conindid = x.indexrelid
                    )
                ) ORDER BY i.relname), '[]'::jsonb)
                FROM pg_index x
                JOIN pg_class i ON i.oid = x.indexrelid
                WHERE x.indrelid = t.oid
                  AND NOT x.indisprimary
            )
        )) AS table_info
    FROM pg_class t
    JOIN pg_namespace n ON n.oid = t.relnamespace
    WHERE t.relkind IN ('r', 'p')
      AND n.nspname = 'public'
) sub;`

func main() {
	dbURL := flag.String("db", "", "Postgres URL")
	output := flag.String("out", "", "Output File")
	flag.Parse()

	if *dbURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: pg2toon -db <url>")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "DB connection error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(context.Background())

	var jsonRaw []byte
	if err := conn.QueryRow(ctx, extractQuery).Scan(&jsonRaw); err != nil {
		fmt.Fprintf(os.Stderr, "SQL query failed: %v\n", err)
		os.Exit(1)
	}

	result, err := toon.ToToon(jsonRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Conversion failed: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		if err := os.WriteFile(*output, []byte(result), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Writing output failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Print(result)
}
