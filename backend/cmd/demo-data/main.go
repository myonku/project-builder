// Local-only additive visual fixtures. Never accepts a production database.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/myonku/project-builder/internal/platform"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dsn := flag.String("dsn", "postgres://project_builder_test:project_builder_test_password@127.0.0.1:15432/project_builder_test?sslmode=disable", "isolated local test database")
	flag.Parse()
	cfg, err := pgx.ParseConfig(*dsn)
	if err != nil {
		return err
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 15432 || cfg.Database != "project_builder_test" {
		return fmt.Errorf("demo-data only accepts 127.0.0.1:15432/project_builder_test")
	}
	ctx := context.Background()
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close(ctx)
	var version int
	var dirty bool
	if err = db.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil || version != 12 || dirty {
		return fmt.Errorf("apply migration 12 first: version=%d dirty=%v error=%v", version, dirty, err)
	}
	var existing string
	err = db.QueryRow(ctx, `SELECT id FROM groups WHERE description='worklane-p04-demo-v2' LIMIT 1`).Scan(&existing)
	if err == nil {
		fmt.Println("演示数据已存在，保留原样。工作组：", existing)
		return nil
	}
	if err != pgx.ErrNoRows {
		return err
	}
	hash, err := platform.NewPasswordHasher().Hash("WorklaneDemo2026!")
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) error { _, e := tx.Exec(ctx, sql, args...); return e }
	users := []string{}
	names := []string{"demo_lead", "demo_chen", "demo_lin", "demo_wang"}
	display := []string{"演示·周宁", "演示·陈晨", "演示·林悦", "演示·王越"}
	for i, name := range names {
		if err = exec(`INSERT INTO users(id,username,email,display_name,password_hash,timezone,reminder_enabled) VALUES(gen_random_uuid(),$1,$2,$3,$4,'Asia/Shanghai',false) ON CONFLICT(username) DO NOTHING`, name, name+"@demo.test", display[i], hash); err != nil {
			return err
		}
		var id string
		if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE username=$1`, name).Scan(&id); err != nil {
			return err
		}
		users = append(users, id)
	}
	// Existing administrators can browse the same projects without changing credentials.
	admins, err := tx.Query(ctx, `SELECT id::text FROM users WHERE system_role='admin' AND status='active'`)
	if err != nil {
		return err
	}
	for admins.Next() {
		var id string
		if err = admins.Scan(&id); err != nil {
			admins.Close()
			return err
		}
		users = append(users, id)
	}
	admins.Close()
	groups := []string{}
	for _, name := range []string{"【演示】产品研发组", "【演示】协作支持组"} {
		var id string
		if err = tx.QueryRow(ctx, `INSERT INTO groups(id,name,description,owner_id) VALUES(gen_random_uuid(),$1,'worklane-p04-demo-v2',$2) RETURNING id`, name, users[0]).Scan(&id); err != nil {
			return err
		}
		groups = append(groups, id)
		for i, user := range users {
			role := "member"
			if i == 0 {
				role = "manager"
			}
			if err = exec(`INSERT INTO group_members(id,group_id,user_id,role) VALUES(gen_random_uuid(),$1,$2,$3)`, id, user, role); err != nil {
				return err
			}
		}
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	date := func(n int) string { return today.AddDate(0, 0, n).Format("2006-01-02") }
	taskIDs := []string{}
	projects := []string{}
	titles := []string{"【演示】客户门户迭代", "【演示】移动端体验优化", "【演示】内部知识库建设", "【演示】已归档交付样例"}
	actions := []string{"需求评审", "接口联调", "视觉规范整理", "自动化回归", "文档交付", "灰度验收", "埋点核查", "权限校验", "上线准备", "性能优化", "交互走查", "里程碑验收"}
	for p, title := range titles {
		var id string
		visibility := "group_editable"
		if p == 2 {
			visibility = "group_readonly"
		}
		if err = tx.QueryRow(ctx, `INSERT INTO projects(id,name,description,created_by,group_id,visibility,start_date,end_date,color) VALUES(gen_random_uuid(),$1,'仅用于本地页面演示，非真实业务记录',$2,$3,$4,$5,$6,$7) RETURNING id`, title, users[0], groups[p%2], visibility, date(-90), date(90), []string{"#1976d2", "#388e3c", "#7b1fa2", "#f57c00"}[p]).Scan(&id); err != nil {
			return err
		}
		projects = append(projects, id)
		for i, user := range users {
			role := "member"
			if i == 0 {
				role = "owner"
			}
			if err = exec(`INSERT INTO project_members(id,project_id,user_id,role) VALUES(gen_random_uuid(),$1,$2,$3)`, id, user, role); err != nil {
				return err
			}
		}
		var parent string
		if err = tx.QueryRow(ctx, `INSERT INTO tasks(id,project_id,created_by,updated_by,name,start_date,end_date,is_shared) VALUES(gen_random_uuid(),$1,$2,$2,'【演示】阶段交付汇总',$3,$4,true) RETURNING id`, id, users[0], date(-90), date(90)).Scan(&parent); err != nil {
			return err
		}
		for i, action := range actions {
			status := []string{"completed", "in_progress", "not_started", "in_progress", "completed", "cancelled"}[i%6]
			progress := 0
			if status == "completed" {
				progress = 100
			} else if status == "in_progress" {
				progress = 25 + i*5
			}
			due := []int{-5, -2, 0, 1, 3, 5, 7, 9, 12, 18, 25, 35}[i]
			var task string
			owner := users[(i+p)%4]
			if err = tx.QueryRow(ctx, `INSERT INTO tasks(id,project_id,parent_id,created_by,updated_by,name,status,progress,priority,start_date,end_date,is_shared,is_milestone) VALUES(gen_random_uuid(),$1,$2,$3,$3,$4,$5,$6,$7,$8,$9,true,$10) RETURNING id`, id, parent, owner, "【演示】"+action, status, progress, i%4, date(-20), date(due), i == 11 || i == 5).Scan(&task); err != nil {
				return err
			}
			taskIDs = append(taskIDs, task)
			if err = exec(`INSERT INTO task_members(id,task_id,user_id,role) VALUES(gen_random_uuid(),$1,$2,'owner')`, task, owner); err != nil {
				return err
			}
			if i%3 == 0 {
				if err = exec(`INSERT INTO task_members(id,task_id,user_id,role) VALUES(gen_random_uuid(),$1,$2,'member')`, task, users[(i+p+1)%4]); err != nil {
					return err
				}
			}
			if len(users) > 4 && i%2 == 0 {
				if err = exec(`INSERT INTO task_members(id,task_id,user_id,role) VALUES(gen_random_uuid(),$1,$2,'member')`, task, users[4]); err != nil {
					return err
				}
			}
		}
		if p == 3 {
			if err = exec(`UPDATE projects SET archived_at=clock_timestamp(),status='archived' WHERE id=$1`, id); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Explicit synthetic history only for newly created demo tasks; global coverage is preserved.
	history, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer history.Rollback(ctx)
	var coverage time.Time
	if err = history.QueryRow(ctx, `SELECT started_at FROM insights_coverage`).Scan(&coverage); err != nil {
		return err
	}
	start := time.Now().Add(-36 * time.Hour)
	if start.Before(coverage) {
		start = coverage.Add(time.Second)
	}
	end := time.Now().Add(-time.Second)
	if start.After(end) {
		start = end
	}
	for i, id := range taskIDs {
		var snapshot []byte
		if err = history.QueryRow(ctx, `SELECT snapshot FROM insight_states WHERE task_id=$1`, id).Scan(&snapshot); err != nil {
			return err
		}
		var state map[string]any
		if err = json.Unmarshal(snapshot, &state); err != nil {
			return err
		}
		initial := map[string]any{}
		for k, v := range state {
			initial[k] = v
		}
		initial["status"] = "not_started"
		initial["progress"] = 0
		raw, _ := json.Marshal(initial)
		at := start.Add(time.Duration(i) * end.Sub(start) / time.Duration(len(taskIDs)+2))
		if _, err = history.Exec(ctx, `UPDATE insight_events SET occurred_at=$2,after_state=$3,actor_id=$4 WHERE task_id=$1 AND before_state IS NULL`, id, at, raw, users[i%4]); err != nil {
			return err
		}
		if _, err = history.Exec(ctx, `INSERT INTO insight_events(task_id,project_id,transaction_id,occurred_at,actor_id,before_state,after_state) VALUES($1,$2,txid_current(),$3,$4,$5,$6)`, id, state["project_id"], at.Add(time.Second), users[i%4], raw, snapshot); err != nil {
			return err
		}
	}
	if err = history.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("已追加演示数据：4账号、%d工作组、%d项目、48叶子任务；未清理现有数据。\n", len(groups), len(projects))
	fmt.Println("账号：demo_lead / demo_chen / demo_lin / demo_wang；初始密码：WorklaneDemo2026!")
	fmt.Println("工作组：", groups, "；项目：", projects)
	return nil
}
