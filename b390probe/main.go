// 连接槽排障：看清 handoff 库的连接分布；APPLY=1 时终止空闲连接（本应用自己的）。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("B390_DSN"))
	if err != nil {
		fmt.Println("connect err:", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `select state, count(*), coalesce(max(now() - state_change)::text,'-')
		from pg_stat_activity where datname = current_database() group by state order by 2 desc`)
	if err != nil {
		fmt.Println("stat err:", err)
		os.Exit(1)
	}
	fmt.Println("=== 连接状态分布（state | 数量 | 最久时长）===")
	for rows.Next() {
		var st, age string
		var n int
		if err := rows.Scan(&st, &n, &age); err == nil {
			fmt.Printf("  %-18s %-5d %s\n", st, n, age)
		}
	}
	rows.Close()

	var maxConn string
	_ = conn.QueryRow(ctx, `show max_connections`).Scan(&maxConn)
	fmt.Println("max_connections =", maxConn)

	rows2, err := conn.Query(ctx, `select coalesce(application_name,'(空)'), count(*)
		from pg_stat_activity where datname = current_database() group by 1 order by 2 desc limit 8`)
	if err == nil {
		fmt.Println("=== 按 application_name 分布 ===")
		for rows2.Next() {
			var app string
			var n int
			if err := rows2.Scan(&app, &n); err == nil {
				fmt.Printf("  %-28s %d\n", app, n)
			}
		}
		rows2.Close()
	}

	if os.Getenv("APPLY") != "1" {
		fmt.Println("（只读；APPLY=1 终止空闲连接）")
		return
	}
	tag, err := conn.Exec(ctx, `select pg_terminate_backend(pid) from pg_stat_activity
		where datname = current_database() and state = 'idle'
		  and pid <> pg_backend_pid()
		  and coalesce(application_name,'') <> 'psql'`)
	if err != nil {
		fmt.Println("terminate err:", err)
		os.Exit(1)
	}
	fmt.Println("已终止空闲连接数:", tag.RowsAffected())
}
