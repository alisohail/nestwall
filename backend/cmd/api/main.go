// Command api runs the NestWall API server.
//
// Usage:
//
//	api                                 start the server
//	api healthcheck                     probe the local readiness endpoint and exit 0 when healthy
//	api reset-password --user <user-id> set a temporary password for a locked-out account
//	api seed-demo                       create the Rivera demo family in an empty database
//	api restore <file>                  replace the data with a backup; the API must be stopped
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/backup"
	"nestwall/backend/internal/calendar"
	"nestwall/backend/internal/demo"
	"nestwall/backend/internal/email"
	"nestwall/backend/internal/notifications"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/config"
	"nestwall/backend/internal/platform/db"
	"nestwall/backend/internal/platform/logger"
	"nestwall/backend/internal/platform/server"
	"nestwall/backend/internal/push"
	"nestwall/backend/internal/scheduler"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		os.Exit(runResetPassword(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "seed-demo" {
		os.Exit(runSeedDemo(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "restore" {
		os.Exit(runRestore(os.Args[2:]))
	}

	log := logger.New()

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	clk := clock.RealClock{}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := db.Open(ctx, cfg.DB.Path, cfg.DB.Key)
	if err != nil {
		log.Error("failed to open database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer conn.Close()

	// The scheduler runs in this process. It is stopped and awaited before the database closes,
	// so an in-flight run finishes or rolls back.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	// Linked calendars are refreshed on the same tick as the schedule.
	links := calendar.NewHandlers(conn, clk, log, cfg.DB.Key)
	worker := scheduler.New(conn, clk, log, scheduler.DefaultInterval).WithLinkSync(links)
	go func() {
		defer close(workerDone)
		worker.Run(workerCtx)
	}()
	// Email has its own short loop, so a reset link is not held up by the minute-long schedule tick.
	outbox := email.NewOutbox(conn, clk, log, cfg.DB.Key)
	outboxDone := make(chan struct{})
	go func() {
		defer close(outboxDone)
		outbox.Run(workerCtx, email.DefaultPollInterval)
	}()
	// Pending notices go out as email on their own loop. Each family's digest interval is set in its settings.
	digester := notifications.NewDigester(conn, clk, log, outbox, cfg.App.URL)
	digestDone := make(chan struct{})
	go func() {
		defer close(digestDone)
		digester.Run(workerCtx)
	}()
	// Pending notices go to subscribed phones and browsers on their own short loop.
	pushSender := push.NewSender(conn, clk, log, cfg.App.URL)
	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		pushSender.Run(workerCtx, push.DefaultPollInterval)
	}()
	// Event reminders are checked every ReminderPollInterval. Each one is sent once, as an in-app notice.
	reminders := calendar.NewReminders(conn, clk, log)
	remindersDone := make(chan struct{})
	go func() {
		defer close(remindersDone)
		reminders.Run(workerCtx, calendar.ReminderPollInterval)
	}()
	// Archives written before the .zip extension are renamed once. A failure is logged and does not stop the API.
	if err := backup.RenameLegacyArchives(ctx, conn, cfg.Backup.Dir, log); err != nil {
		log.Error("backup archive rename failed", slog.String("error", err.Error()))
	}
	// The daily backup. A run in progress finishes before the database closes.
	backups := backup.NewScheduler(conn, clk, log, cfg.Backup.Dir, cfg.DB.Key,
		filepath.Join(filepath.Dir(cfg.DB.Path), "avatars"))
	backupDone := make(chan struct{})
	go func() {
		defer close(backupDone)
		backups.Run(workerCtx, backup.DefaultPollInterval)
	}()

	srv := server.New(cfg, log, clk, conn, outbox)
	err = srv.Start()
	stopWorker()
	<-workerDone
	<-outboxDone
	<-digestDone
	<-pushDone
	<-remindersDone
	<-backupDone
	if err != nil {
		log.Error("failed to run server", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// runHealthcheck calls the readiness endpoint on the local API port.
// It returns the process exit code.
func runHealthcheck() int {
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/v1/health/ready")
	if err != nil {
		fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		fmt.Fprintf(os.Stderr, "health check failed with status %d: %s\n", resp.StatusCode, body)
		return 1
	}

	fmt.Println("health check passed")
	return 0
}

// runResetPassword sets a temporary password for an account. It works on the
// database directly, so it runs while the API is stopped. It returns the exit code.
func runResetPassword(args []string) int {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	userID := fs.String("user", "", "user ID of the account to reset")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *userID == "" {
		fmt.Fprintln(os.Stderr, "usage: api reset-password --user <user-id>")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := db.Open(ctx, cfg.DB.Path, cfg.DB.Key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		return 1
	}
	defer conn.Close()

	temporary, err := auth.ResetPasswordByLoginID(ctx, conn, clock.RealClock{}, *userID)
	if err == auth.ErrUnknownUser {
		fmt.Fprintln(os.Stderr, "no account has that user ID")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset failed: %v\n", err)
		return 1
	}

	fmt.Printf("temporary password: %s\n", temporary)
	fmt.Println("The user must choose a new password at the next sign-in.")
	return 0
}

// runRestore replaces the database and avatars with a backup. The API must be stopped, because it
// keeps the old database open. The current data is saved first as a safety backup. It returns the exit code.
func runRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: api restore <file>")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		return 1
	}

	res, err := backup.Restore(context.Background(), backup.RestoreOptions{
		Archive:    fs.Arg(0),
		DBPath:     cfg.DB.Path,
		DBKey:      cfg.DB.Key,
		BackupDir:  cfg.Backup.Dir,
		AvatarsDir: filepath.Join(filepath.Dir(cfg.DB.Path), "avatars"),
		Passphrase: os.Getenv("NESTWALL_RESTORE_PASSPHRASE"),
	})
	if errors.Is(err, backup.ErrPassphraseRequired) {
		fmt.Fprintln(os.Stderr, "this backup is encrypted: set NESTWALL_RESTORE_PASSPHRASE to its archive passphrase")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore failed: %v\n", err)
		return 1
	}

	if res.SafetyBackup != "" {
		fmt.Printf("current data saved first as %s\n", res.SafetyBackup)
	}
	fmt.Printf("restored %s: %d users, %d family members, %d ledger entries\n",
		fs.Arg(0), res.Counts["users"], res.Counts["family_members"], res.Counts["points_ledger"])
	fmt.Println("Start the API again to use the restored data.")
	return 0
}

// runSeedDemo creates the Rivera demo family in an empty database. It works on the
// database directly, so it runs while the API is stopped. Running it again changes
// nothing. It returns the exit code.
func runSeedDemo(args []string) int {
	fs := flag.NewFlagSet("seed-demo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := db.Open(ctx, cfg.DB.Path, cfg.DB.Key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open database: %v\n", err)
		return 1
	}
	defer conn.Close()

	password := os.Getenv("NESTWALL_DEMO_PASSWORD")
	result, err := demo.SeedWithPassword(ctx, conn, clock.RealClock{}, password)
	if errors.Is(err, demo.ErrFamilyExists) {
		fmt.Fprintln(os.Stderr, "this database already has a family; the demo was not added")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed failed: %v\n", err)
		return 1
	}

	if !result.Created {
		fmt.Println("the " + demo.FamilyName + " already exists; nothing was changed")
		return 0
	}
	fmt.Printf("created the %s\n", demo.FamilyName)
	if password != "" {
		fmt.Println("demo password set from NESTWALL_DEMO_PASSWORD for every account")
		return 0
	}
	fmt.Printf("demo password for every account (sam, jordan, ava, noah, mia, nana): %s\n", result.Password)
	fmt.Println("The password is shown only once.")
	return 0
}
