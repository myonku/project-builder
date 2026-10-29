// reminders is a non-sending operational tool. Configuration matches cmd/api;
// no Sender is constructed and no operation opens an SMTP connection.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/myonku/project-builder/internal/app/reminders"
	"github.com/myonku/project-builder/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: reminders inspect | initialize [--dry-run=true] [--allow-overdue] [--dispatchers-stopped] | resolve --attempt ID --actor ID --outcome accepted|not_accepted --evidence TEXT [--sent-at RFC3339] --dispatchers-stopped | export")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dry := flags.Bool("dry-run", true, "rollback project initialization")
	allow := flags.Bool("allow-overdue", false, "explicitly release initial overdue backlog")
	stopped := flags.Bool("dispatchers-stopped", false, "operator confirms all previous dispatch processes terminated")
	token := flags.String("attempt", "", "unknown attempt ID")
	actor := flags.String("actor", "", "active administrator ID")
	outcome := flags.String("outcome", "", "accepted or not_accepted")
	evidence := flags.String("evidence", "", "verification evidence")
	sent := flags.String("sent-at", "", "actual SMTP acceptance time in RFC3339")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	cfg, err := config.Load(os.Getenv("APP_CONFIG_FILE"), os.Getenv("APP_ENV_FILE"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.Database.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := reminders.NewRepository(pool)
	now := time.Now().UTC()
	switch args[0] {
	case "initialize":
		if !*dry && !*stopped {
			return fmt.Errorf("apply requires --dispatchers-stopped; default is dry-run")
		}
		n, err := repo.Initialize(ctx, now, *allow, *dry)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"dry_run": *dry, "allow_overdue": *allow, "pending_plans": n})
	case "resolve":
		var at *time.Time
		if *sent != "" {
			parsed, err := time.Parse(time.RFC3339, *sent)
			if err != nil {
				return err
			}
			at = &parsed
		}
		if err = repo.ResolveUnknown(ctx, *token, *actor, *outcome, *evidence, at, *stopped, now); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"attempt": *token, "resolved": true, "outcome": *outcome})
	case "inspect":
		rows, err := pool.Query(ctx, `SELECT status,count(*),count(*) FILTER(WHERE scheduled_for<=now()) FROM reminders GROUP BY status ORDER BY status`)
		if err != nil {
			return err
		}
		defer rows.Close()
		result := []map[string]any{}
		for rows.Next() {
			var status string
			var n, due int
			if err = rows.Scan(&status, &n, &due); err != nil {
				return err
			}
			result = append(result, map[string]any{"status": status, "count": n, "due": due})
		}
		if err = rows.Err(); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "export":
		// Contains recipients/content; redirect to a protected operator-owned file.
		rows, err := pool.Query(ctx, `SELECT row_to_json(a) FROM reminder_delivery_attempts a ORDER BY authorized_at,id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw json.RawMessage
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			if err = json.NewEncoder(os.Stdout).Encode(raw); err != nil {
				return err
			}
		}
		return rows.Err()
	default:
		return fmt.Errorf("unknown operation %q", args[0])
	}
}
