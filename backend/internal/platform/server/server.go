package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"nestwall/backend/internal/apitokens"
	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/avatars"
	"nestwall/backend/internal/backup"
	"nestwall/backend/internal/badges"
	"nestwall/backend/internal/bonus"
	"nestwall/backend/internal/calendar"
	"nestwall/backend/internal/dashboard"
	"nestwall/backend/internal/diagnostics"
	"nestwall/backend/internal/email"
	"nestwall/backend/internal/export"
	"nestwall/backend/internal/family"
	"nestwall/backend/internal/health"
	"nestwall/backend/internal/integrations"
	"nestwall/backend/internal/notifications"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/config"
	"nestwall/backend/internal/platform/logger"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/sessions"
	"nestwall/backend/internal/tasks"
	"nestwall/backend/internal/wall"
	"nestwall/backend/internal/weather"
)

// requestIDKey is the context key for the request ID.
type requestIDKey struct{}

// Server represents the HTTP server.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	clock      clock.Clock
}

// New creates a new Server instance.
func New(cfg *config.Config, log *slog.Logger, clk clock.Clock, pool *sql.DB, outbox *email.Outbox) *Server {
	mux := http.NewServeMux()

	// Auth routes
	authHandlers := auth.NewHandlers(pool, auth.NewRateLimiter(clk), clk, log).WithEmailReset(outbox, cfg.App.URL)
	mux.HandleFunc("POST /api/v1/auth/login", authHandlers.Login)
	mux.HandleFunc("POST /api/v1/auth/logout", authHandlers.Logout)
	mux.HandleFunc("GET /api/v1/auth/me", authHandlers.Me)
	// The caller's own permission codes. Authenticated only: the handler returns the caller's grants, nothing else.
	mux.HandleFunc("GET /api/v1/auth/permissions", permissions.Authenticated(pool, clk)(permissions.Effective(pool)))
	// The change-password route is not wrapped by the permission middleware,
	// so a user with a temporary password can still replace it.
	mux.HandleFunc("POST /api/v1/auth/change-password", authHandlers.ChangePassword)
	// The caller's own theme choice. Like change-password, it is not behind the permission middleware.
	mux.HandleFunc("PUT /api/v1/auth/me/theme", authHandlers.SetTheme)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", authHandlers.ForgotPassword)
	mux.HandleFunc("POST /api/v1/auth/forgot-password/notify-parents", authHandlers.NotifyParents)
	mux.HandleFunc("POST /api/v1/auth/forgot-password/email", authHandlers.ForgotPasswordByEmail)
	// Public: the single-use token in the body is the only credential.
	mux.HandleFunc("POST /api/v1/auth/reset-password", authHandlers.CompletePasswordReset)

	// Setup routes
	setupHandlers := auth.NewSetupHandlers(pool, clk, log)
	mux.HandleFunc("GET /api/v1/setup/status", setupHandlers.Status)
	mux.HandleFunc("POST /api/v1/setup/parent", setupHandlers.Parent)

	// Family routes
	// Profile pictures are stored as files in avatars/ beside the database.
	avatarDir := filepath.Join(filepath.Dir(cfg.DB.Path), "avatars")
	familyHandlers := family.NewHandlers(pool, clk, log, avatarDir)
	permMiddleware := permissions.Middleware(pool, clk)

	// Any signed-in member can edit their own details. The handler only ever touches the caller's own account.
	mux.Handle("PUT /api/v1/me/profile", permissions.Authenticated(pool, clk)(familyHandlers.UpdateOwnProfile))
	mux.Handle("GET /api/v1/family/members", permMiddleware(permissions.PermMembersView, familyHandlers.ListMembers))
	mux.Handle("PUT /api/v1/family/members/{id}", permMiddleware(permissions.PermMembersManage, familyHandlers.UpdateMember))
	mux.Handle("DELETE /api/v1/family/members/{id}", permMiddleware(permissions.PermMembersManage, familyHandlers.RemoveMember))
	mux.Handle("POST /api/v1/family/members", permMiddleware(permissions.PermMembersManage, familyHandlers.CreateMember))
	mux.Handle("PUT /api/v1/family", permMiddleware(permissions.PermFamilyManage, familyHandlers.UpdateFamily))

	// Roles and permissions routes
	mux.Handle("GET /api/v1/permissions/catalogue", permMiddleware(permissions.PermRolesView, familyHandlers.PermissionCatalogue))
	mux.Handle("GET /api/v1/family/roles", permMiddleware(permissions.PermRolesView, familyHandlers.ListRoles))
	mux.Handle("POST /api/v1/family/roles", permMiddleware(permissions.PermRolesManage, familyHandlers.CreateRole))
	mux.Handle("PUT /api/v1/family/roles/{id}/permissions", permMiddleware(permissions.PermRolesManage, familyHandlers.UpdateRolePermissions))
	mux.Handle("POST /api/v1/family/roles/{id}/reset", permMiddleware(permissions.PermRolesManage, familyHandlers.ResetRolePermissions))
	mux.Handle("DELETE /api/v1/family/roles/{id}", permMiddleware(permissions.PermRolesManage, familyHandlers.DeleteRole))
	mux.Handle("GET /api/v1/family/members/{id}/permissions", permMiddleware(permissions.PermRolesView, familyHandlers.GetMemberPermissions))
	mux.Handle("PUT /api/v1/family/members/{id}/permissions", permMiddleware(permissions.PermRolesManage, familyHandlers.UpdateMemberPermissions))

	// Session routes
	sessionHandlers := sessions.NewHandlers(pool, clk, log)
	mux.Handle("GET /api/v1/auth/sessions", permMiddleware(permissions.PermSessionsManage, sessionHandlers.ListOwn))
	mux.Handle("DELETE /api/v1/auth/sessions/{id}", permMiddleware(permissions.PermSessionsManage, sessionHandlers.RevokeOwn))
	mux.Handle("POST /api/v1/auth/sessions/revoke-others", permMiddleware(permissions.PermSessionsManage, sessionHandlers.RevokeOthers))
	mux.Handle("GET /api/v1/family/members/{id}/sessions", permMiddleware(permissions.PermSessionsView, sessionHandlers.ListForMember))
	mux.Handle("POST /api/v1/family/members/{id}/sessions/revoke-all", permMiddleware(permissions.PermMembersManage, sessionHandlers.RevokeAllForMember))

	// Notification routes. Every signed-in member reads and clears their own notifications, so these
	// only require a session; the handlers scope each query to the caller. Only parents read and
	// change the family notification settings.
	notificationHandlers := notifications.NewHandlers(pool, clk, log, cfg.App.URL)
	mux.HandleFunc("GET /api/v1/notifications", permissions.Authenticated(pool, clk)(notificationHandlers.List))
	mux.HandleFunc("POST /api/v1/notifications/{id}/read", permissions.Authenticated(pool, clk)(notificationHandlers.MarkRead))
	mux.HandleFunc("DELETE /api/v1/notifications", permissions.Authenticated(pool, clk)(notificationHandlers.ClearAll))
	mux.HandleFunc("GET /api/v1/notifications/stream", permissions.Authenticated(pool, clk)(notificationHandlers.Stream))
	mux.Handle("GET /api/v1/notifications/settings", permMiddleware(permissions.PermSettingsView, notificationHandlers.GetSettings))
	mux.Handle("PUT /api/v1/notifications/settings", permMiddleware(permissions.PermSettingsManage, notificationHandlers.PutSettings))
	mux.Handle("POST /api/v1/notifications/settings/preview", permMiddleware(permissions.PermSettingsManage, notificationHandlers.PreviewTemplate))

	// Profile picture routes.
	avatarHandlers := avatars.NewHandlers(pool, avatarDir, log)
	mux.Handle("POST /api/v1/family/members/{id}/avatar", permMiddleware(permissions.PermProfileAvatarChange, avatarHandlers.Upload))
	mux.Handle("DELETE /api/v1/family/members/{id}/avatar", permMiddleware(permissions.PermProfileAvatarChange, avatarHandlers.Delete))
	// Any signed-in member may fetch a picture. The handler limits it to the same family and to the Wall setting.
	mux.HandleFunc("GET /api/v1/avatars/{id}", permissions.Authenticated(pool, clk)(avatarHandlers.Serve))
	mux.Handle("GET /api/v1/family/avatar-settings", permMiddleware(permissions.PermSettingsView, avatarHandlers.GetSettings))
	mux.Handle("PUT /api/v1/family/avatar-settings", permMiddleware(permissions.PermSettingsManage, avatarHandlers.UpdateSettings))
	wxService := weather.NewService(pool, weather.NewClient(weather.DefaultBaseURL, clk), log)
	weatherHandlers := weather.NewHandlers(pool, wxService, log)
	mux.Handle("GET /api/v1/weather", permissions.Authenticated(pool, clk)(weatherHandlers.Forecast))
	mux.Handle("GET /api/v1/family/weather-settings", permMiddleware(permissions.PermSettingsView, weatherHandlers.GetSettings))
	mux.Handle("PUT /api/v1/family/weather-settings", permMiddleware(permissions.PermSettingsManage, weatherHandlers.UpdateSettings))
	mux.Handle("PUT /api/v1/family/time-format", permMiddleware(permissions.PermSettingsManage, familyHandlers.UpdateTimeFormat))
	mux.Handle("PUT /api/v1/family/timezone", permMiddleware(permissions.PermSettingsManage, familyHandlers.UpdateTimezone))
	emailHandlers := email.NewHandlers(pool, clk, log, cfg.DB.Key)
	mux.Handle("GET /api/v1/settings/email", permMiddleware(permissions.PermSettingsView, emailHandlers.GetSettings))
	mux.Handle("PUT /api/v1/settings/email", permMiddleware(permissions.PermSettingsManage, emailHandlers.UpdateSettings))
	mux.Handle("POST /api/v1/settings/email/test", permMiddleware(permissions.PermSettingsManage, emailHandlers.SendTest))

	// API tokens (M7-07). Tokens are created and revoked with settings.manage; the owner's own permissions cap the scopes.
	apiTokenHandlers := apitokens.NewHandlers(pool, clk, log)
	mux.Handle("GET /api/v1/settings/api-tokens", permMiddleware(permissions.PermSettingsView, apiTokenHandlers.List))
	mux.Handle("POST /api/v1/settings/api-tokens", permMiddleware(permissions.PermSettingsManage, apiTokenHandlers.Create))
	mux.Handle("POST /api/v1/settings/api-tokens/{id}/revoke", permMiddleware(permissions.PermSettingsManage, apiTokenHandlers.Revoke))

	// Data export for reading (M7-09): CSV copies for parents. They are not backups and have no import.
	exportHandlers := export.NewHandlers(pool, log)
	mux.Handle("GET /api/v1/export/tasks.csv", permMiddleware(permissions.PermSettingsManage, exportHandlers.Tasks))
	mux.Handle("GET /api/v1/export/ledger.csv", permMiddleware(permissions.PermSettingsManage, exportHandlers.Ledger))
	mux.Handle("GET /api/v1/export/members.csv", permMiddleware(permissions.PermSettingsManage, exportHandlers.Members))

	// Scheduled and manual backups (M7-10). Each route needs its own permission, so a role with
	// settings.manage alone cannot restore or reset. A download also needs the parent's password.
	backupHandlers := backup.NewHandlers(pool, clk, log, cfg.Backup.Dir, cfg.DB.Key, cfg.DB.Path, avatarDir)
	mux.Handle("GET /api/v1/settings/backups", permMiddleware(permissions.PermBackupManage, backupHandlers.GetStatus))
	mux.Handle("PUT /api/v1/settings/backups/encryption", permMiddleware(permissions.PermSettingsManage, backupHandlers.UpdateEncryption))
	mux.Handle("PUT /api/v1/settings/backups/schedule", permMiddleware(permissions.PermSettingsManage, backupHandlers.UpdateSchedule))
	mux.Handle("POST /api/v1/settings/backups/run", permMiddleware(permissions.PermBackupManage, backupHandlers.RunNow))
	mux.Handle("POST /api/v1/settings/backups/{id}/download", permMiddleware(permissions.PermBackupManage, backupHandlers.Download))
	mux.Handle("POST /api/v1/settings/backups/{id}/restore", permMiddleware(permissions.PermBackupRestore, backupHandlers.Restore))
	mux.Handle("POST /api/v1/settings/backups/restore-upload", permMiddleware(permissions.PermBackupRestore, backupHandlers.RestoreUpload))
	// First-run restore (M7-11c). Public, but it refuses once any family exists.
	mux.HandleFunc("POST /api/v1/setup/restore", backupHandlers.SetupRestore)
	// Factory reset (M7-12). The preview only counts; the reset needs the password and the confirm word.
	mux.Handle("GET /api/v1/settings/reset/preview", permMiddleware(permissions.PermDataReset, backupHandlers.ResetPreview))
	mux.Handle("POST /api/v1/settings/reset", permMiddleware(permissions.PermDataReset, backupHandlers.FactoryReset))
	// Parent diagnostics (M8-05): version, migrations, backup, sync, email, wall, storage and 5xx counts.
	diagnosticsHandlers := diagnostics.NewHandlers(pool, cfg.DB.Path, log)
	mux.Handle("GET /api/v1/settings/diagnostics", permMiddleware(permissions.PermSettingsView, diagnosticsHandlers.Get))
	mux.Handle("GET /api/v1/settings/diagnostics/events", permMiddleware(permissions.PermSettingsView, diagnosticsHandlers.ListEvents))
	mux.Handle("DELETE /api/v1/settings/diagnostics/events", permMiddleware(permissions.PermSettingsManage, diagnosticsHandlers.ClearEvents))

	// Integration summary (M7-08) for external tools such as Home Assistant. A token needs both the
	// tasks:read and calendar:read scopes, because the summary includes task counts and events.
	integrationHandlers := integrations.NewHandlers(pool, clk, log)
	mux.Handle("GET /api/v1/integrations/summary", apitokens.Require(pool, clk, "tasks:read")(
		apitokens.Require(pool, clk, "calendar:read")(integrationHandlers.Summary)))

	// Task category and task settings routes (M3-02). Viewing needs settings.view;
	// changing needs settings.manage. Children have neither, so they get 403.
	taskHandlers := tasks.NewHandlers(pool, clk, log)
	// Points for approved tasks (M4-02). The participant posts in the same transaction as the
	// approval or reopen, so points cannot be lost if the points write fails.
	taskHandlers.Participate(rewards.NewTaskAwards(clk).Apply)
	// Badges for approved tasks (M4-09a). It runs after the points participant, so a badge that
	// depends on the new points sees them, and in the same transaction as the approval.
	taskHandlers.Participate(badges.NewAwards(clk).Apply)
	// Task notices for the kid (approved, sent back) and the parents (submitted for approval).
	taskHandlers.Participate(tasks.NewNotifier(clk).Apply)
	mux.Handle("GET /api/v1/family/task-categories", permMiddleware(permissions.PermSettingsView, taskHandlers.ListCategories))
	mux.Handle("POST /api/v1/family/task-categories", permMiddleware(permissions.PermSettingsManage, taskHandlers.CreateCategory))
	mux.Handle("PUT /api/v1/family/task-categories/{id}", permMiddleware(permissions.PermSettingsManage, taskHandlers.UpdateCategory))
	mux.Handle("DELETE /api/v1/family/task-categories/{id}", permMiddleware(permissions.PermSettingsManage, taskHandlers.DeleteCategory))
	mux.Handle("GET /api/v1/family/task-settings", permMiddleware(permissions.PermSettingsView, taskHandlers.GetSettings))
	mux.Handle("PUT /api/v1/family/task-settings", permMiddleware(permissions.PermSettingsManage, taskHandlers.UpdateSettings))

	// Task template routes (M3-03). Reading needs tasks.view; changes need tasks.manage.
	mux.Handle("GET /api/v1/task-templates", permMiddleware(permissions.PermTasksView, taskHandlers.ListTemplates))
	mux.Handle("POST /api/v1/task-templates", permMiddleware(permissions.PermTasksManage, taskHandlers.CreateTemplate))
	mux.Handle("GET /api/v1/task-templates/{id}", permMiddleware(permissions.PermTasksView, taskHandlers.GetTemplate))
	mux.Handle("PATCH /api/v1/task-templates/{id}", permMiddleware(permissions.PermTasksManage, taskHandlers.UpdateTemplate))
	mux.Handle("DELETE /api/v1/task-templates/{id}", permMiddleware(permissions.PermTasksManage, taskHandlers.DeleteTemplate))

	// One-off task routes (M3-04). Reads use the authenticated wrapper, because a child has
	// tasks.view at own level; the handler checks the scope itself (readScope).
	// Changes need tasks.manage, so children get 403.
	mux.HandleFunc("GET /api/v1/tasks", permissions.Authenticated(pool, clk)(taskHandlers.ListTasks))
	mux.HandleFunc("GET /api/v1/tasks/{id}", permissions.Authenticated(pool, clk)(taskHandlers.GetTask))
	mux.Handle("POST /api/v1/tasks", permMiddleware(permissions.PermTasksManage, taskHandlers.CreateTask))
	mux.Handle("POST /api/v1/task-definitions", permMiddleware(permissions.PermTasksManage, taskHandlers.CreateRepeat))
	mux.Handle("PATCH /api/v1/tasks/{id}", permMiddleware(permissions.PermTasksManage, taskHandlers.UpdateTask))
	mux.Handle("POST /api/v1/tasks/{id}/archive", permMiddleware(permissions.PermTasksManage, taskHandlers.ArchiveTask))
	mux.Handle("POST /api/v1/tasks/{id}/unarchive", permMiddleware(permissions.PermTasksManage, taskHandlers.UnarchiveTask))
	mux.Handle("POST /api/v1/tasks/{id}/remove-person", permMiddleware(permissions.PermTasksManage, taskHandlers.RemovePerson))
	mux.Handle("DELETE /api/v1/tasks/{id}", permMiddleware(permissions.PermTasksManage, taskHandlers.DeleteTask))

	// Completion and approval routes (M3-07). Completing and ticking a checklist need
	// tasks.complete, which children have. Approve and reject need tasks.approve. Skip and
	// reopen need tasks.manage.
	mux.Handle("POST /api/v1/tasks/{id}/complete", permMiddleware(permissions.PermTasksComplete, taskHandlers.Complete))
	mux.Handle("POST /api/v1/tasks/{id}/approve", permMiddleware(permissions.PermTasksApprove, taskHandlers.Approve))
	mux.Handle("POST /api/v1/tasks/{id}/reject", permMiddleware(permissions.PermTasksApprove, taskHandlers.Reject))
	mux.Handle("POST /api/v1/tasks/{id}/skip", permMiddleware(permissions.PermTasksManage, taskHandlers.Skip))
	mux.Handle("POST /api/v1/tasks/{id}/reopen", permMiddleware(permissions.PermTasksManage, taskHandlers.Reopen))
	mux.Handle("POST /api/v1/tasks/{id}/withdraw", permMiddleware(permissions.PermTasksComplete, taskHandlers.Withdraw))
	mux.Handle("PUT /api/v1/tasks/{id}/checklist/{item}", permMiddleware(permissions.PermTasksComplete, taskHandlers.TickChecklist))
	mux.Handle("POST /api/v1/tasks/{id}/checklist", permMiddleware(permissions.PermTasksManage, taskHandlers.AddChecklistItem))
	mux.Handle("PATCH /api/v1/tasks/{id}/checklist/{item}", permMiddleware(permissions.PermTasksManage, taskHandlers.RenameChecklistItem))
	mux.Handle("DELETE /api/v1/tasks/{id}/checklist/{item}", permMiddleware(permissions.PermTasksManage, taskHandlers.RemoveChecklistItem))

	// Parent dashboard route (M3-10). Parent-only, so children get 403 whatever their task scope.
	dashboardHandlers := dashboard.NewHandlers(pool, clk, log)
	mux.HandleFunc("GET /api/v1/dashboard/parent", permissions.RequireParent(pool, clk)(dashboardHandlers.Parent))
	mux.Handle("GET /api/v1/me/tasks", permMiddleware(permissions.PermTasksView, taskHandlers.MyTasks))
	mux.Handle("GET /api/v1/me/tasks/coming", permMiddleware(permissions.PermTasksView, taskHandlers.MyComingUp))
	mux.Handle("GET /api/v1/me/tasks/done", permMiddleware(permissions.PermTasksView, taskHandlers.MyDone))

	// Calendar event routes (M5-01). Changes need calendar.manage, so children get 403. Reads use
	// the authenticated wrapper, because a child has calendar.view at own level; the
	// handler checks the scope itself (readScope).
	calendarHandlers := calendar.NewHandlers(pool, clk, log, cfg.DB.Key)
	mux.Handle("POST /api/v1/calendar/events", permMiddleware(permissions.PermCalendarManage, calendarHandlers.CreateEvent))
	mux.HandleFunc("GET /api/v1/calendar/events/{id}", permissions.Authenticated(pool, clk)(calendarHandlers.GetEvent))
	mux.Handle("PUT /api/v1/calendar/events/{id}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.UpdateEvent))
	mux.Handle("DELETE /api/v1/calendar/events/{id}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.DeleteEvent))
	mux.Handle("PUT /api/v1/calendar/events/{id}/occurrences/{date}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.EditOccurrence))
	mux.Handle("DELETE /api/v1/calendar/events/{id}/occurrences/{date}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.CancelOccurrence))
	mux.HandleFunc("GET /api/v1/calendar/range", permissions.Authenticated(pool, clk)(calendarHandlers.Range))
	mux.Handle("POST /api/v1/calendar/imports", permMiddleware(permissions.PermCalendarManage, calendarHandlers.CreateImport))
	mux.Handle("GET /api/v1/calendar/imports", permMiddleware(permissions.PermCalendarManage, calendarHandlers.ListImports))
	mux.Handle("PATCH /api/v1/calendar/imports/{id}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.UpdateImport))
	mux.Handle("DELETE /api/v1/calendar/imports/{id}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.DeleteImport))
	mux.Handle("POST /api/v1/calendar/imports/{id}/sync", permMiddleware(permissions.PermCalendarManage, calendarHandlers.SyncImport))
	mux.Handle("GET /api/v1/calendar/sources", permMiddleware(permissions.PermCalendarManage, calendarHandlers.ListSources))
	mux.Handle("POST /api/v1/calendar/sources", permMiddleware(permissions.PermCalendarManage, calendarHandlers.CreateSource))
	mux.Handle("PATCH /api/v1/calendar/sources/{id}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.UpdateSource))
	mux.Handle("DELETE /api/v1/calendar/sources/{id}", permMiddleware(permissions.PermCalendarManage, calendarHandlers.DeleteSource))
	// The export scopes the events in the handler, like the calendar range, so a child gets their own events.
	mux.HandleFunc("GET /api/v1/calendar/export.ics", permissions.Authenticated(pool, clk)(calendarHandlers.ExportFamily))

	// Audit routes
	auditHandlers := audit.NewHandlers(pool, log)
	mux.Handle("GET /api/v1/audit/logs", permMiddleware(permissions.PermAuditView, auditHandlers.ListAuditLogs))

	// Points routes (M4-03). A bonus, penalty or adjustment needs an Idempotency-Key header.
	// Both routes need rewards.manage, so children get 403.
	rewardHandlers := rewards.NewHandlers(pool, clk, log)
	mux.Handle("POST /api/v1/family/members/{id}/points", permMiddleware(permissions.PermRewardsManage, rewardHandlers.PostPoints))
	mux.Handle("POST /api/v1/points/entries/{id}/reverse", permMiddleware(permissions.PermRewardsManage, rewardHandlers.ReversePenaltyEntry))

	// Catalogue. Children hold rewards.view (view level) and see only the rewards open to them;
	// the handlers filter. Writes need rewards.manage, so children get 403.
	mux.Handle("GET /api/v1/rewards", permMiddleware(permissions.PermRewardsView, rewardHandlers.ListRewards))
	mux.Handle("POST /api/v1/rewards", permMiddleware(permissions.PermRewardsManage, rewardHandlers.CreateReward))
	mux.Handle("GET /api/v1/rewards/{id}", permMiddleware(permissions.PermRewardsView, rewardHandlers.GetReward))
	mux.Handle("PATCH /api/v1/rewards/{id}", permMiddleware(permissions.PermRewardsManage, rewardHandlers.UpdateReward))
	mux.Handle("DELETE /api/v1/rewards/{id}", permMiddleware(permissions.PermRewardsManage, rewardHandlers.ArchiveReward))
	mux.Handle("GET /api/v1/rewards/settings", permMiddleware(permissions.PermRewardsManage, rewardHandlers.GetRewardSettings))
	mux.Handle("PUT /api/v1/rewards/settings", permMiddleware(permissions.PermRewardsManage, rewardHandlers.PutRewardSettings))

	// Redemptions (M4-05). Redeeming needs an Idempotency-Key header. A child can redeem and cancel
	// their own pending request. Approve and reject need rewards.approve, so children get 403.
	mux.Handle("POST /api/v1/rewards/{id}/redeem", permMiddleware(permissions.PermRewardsRedeem, rewardHandlers.RedeemReward))
	mux.Handle("POST /api/v1/redemptions/{id}/approve", permMiddleware(permissions.PermRewardsApprove, rewardHandlers.ApproveRedemption))
	mux.Handle("POST /api/v1/redemptions/{id}/reject", permMiddleware(permissions.PermRewardsApprove, rewardHandlers.RejectRedemption))
	mux.Handle("POST /api/v1/redemptions/{id}/cancel", permMiddleware(permissions.PermRewardsRedeem, rewardHandlers.CancelRedemption))

	// Read views for the points and rewards screens (M4-06). Children hold rewards.view, and the
	// handlers limit a child to their own rows.
	mux.Handle("GET /api/v1/me/points", permMiddleware(permissions.PermRewardsView, rewardHandlers.MyPoints))
	mux.Handle("GET /api/v1/points/entries", permMiddleware(permissions.PermRewardsView, rewardHandlers.ListEntries))
	mux.Handle("GET /api/v1/points/balances", permMiddleware(permissions.PermRewardsView, rewardHandlers.MemberBalances))
	mux.Handle("GET /api/v1/redemptions", permMiddleware(permissions.PermRewardsView, rewardHandlers.ListRedemptions))

	// Sharing (M4-08). A child with points.share sends points to a sibling; a parent cannot (403
	// children_only). Children hold rewards.view to read their own policy and daily use. Settings
	// need rewards.manage. Approving or declining a share needs rewards.approve, so children get 403.
	mux.Handle("GET /api/v1/points/share", permMiddleware(permissions.PermRewardsView, rewardHandlers.GetShare))
	mux.Handle("PUT /api/v1/points/share/settings", permMiddleware(permissions.PermRewardsManage, rewardHandlers.PutShareSettings))
	mux.Handle("POST /api/v1/points/share", permMiddleware(permissions.PermPointsShare, rewardHandlers.PostShare))
	mux.Handle("GET /api/v1/point-shares", permMiddleware(permissions.PermRewardsApprove, rewardHandlers.ListPendingShares))
	mux.Handle("POST /api/v1/point-shares/{id}/approve", permMiddleware(permissions.PermRewardsApprove, rewardHandlers.ApprovePendingShare))
	mux.Handle("POST /api/v1/point-shares/{id}/decline", permMiddleware(permissions.PermRewardsApprove, rewardHandlers.DeclinePendingShare))

	// Badges settings and manual awards (M4-09b). Reading the badge list needs settings.view (a
	// caregiver can read it, a child cannot). Changing switches, groups and custom badges needs
	// settings.manage. Awarding a custom badge by hand needs rewards.manage, so children get 403.
	badgeHandlers := badges.NewHandlers(pool, clk, log)
	mux.Handle("GET /api/v1/badges", permMiddleware(permissions.PermSettingsView, badgeHandlers.GetConfig))
	mux.Handle("PUT /api/v1/badges/settings", permMiddleware(permissions.PermSettingsManage, badgeHandlers.PutSettings))
	mux.Handle("PUT /api/v1/badges/groups/{group}", permMiddleware(permissions.PermSettingsManage, badgeHandlers.PutGroup))
	mux.Handle("POST /api/v1/badges/custom", permMiddleware(permissions.PermSettingsManage, badgeHandlers.CreateCustom))
	mux.Handle("PUT /api/v1/badges/custom/{id}", permMiddleware(permissions.PermSettingsManage, badgeHandlers.UpdateCustom))
	mux.Handle("POST /api/v1/badges/awards", permMiddleware(permissions.PermRewardsManage, badgeHandlers.PostAward))
	mux.Handle("GET /api/v1/me/badges", permMiddleware(permissions.PermRewardsView, badgeHandlers.MyBadges))

	// Bonus (open) tasks (M4-10). Publishing and deciding need bonus.manage, so children get 403.
	// Claiming, releasing and completing need bonus.grab; a parent passes that check and is refused in
	// the handler with children_only. The list is open to either permission, and the handler checks.
	bonusHandlers := bonus.NewHandlers(pool, clk, log)
	mux.Handle("GET /api/v1/bonus-tasks", permissions.Authenticated(pool, clk)(bonusHandlers.ListTasks))
	mux.Handle("POST /api/v1/bonus-tasks", permMiddleware(permissions.PermBonusManage, bonusHandlers.CreateTask))
	mux.Handle("POST /api/v1/bonus-tasks/{id}/claim", permMiddleware(permissions.PermBonusGrab, bonusHandlers.ClaimTask))
	mux.Handle("POST /api/v1/bonus-tasks/{id}/release", permMiddleware(permissions.PermBonusGrab, bonusHandlers.ReleaseTask))
	mux.Handle("POST /api/v1/bonus-tasks/{id}/complete", permMiddleware(permissions.PermBonusGrab, bonusHandlers.CompleteTask))
	mux.Handle("PATCH /api/v1/bonus-tasks/{id}", permMiddleware(permissions.PermBonusManage, bonusHandlers.EditTask))
	mux.Handle("POST /api/v1/bonus-tasks/{id}/cancel", permMiddleware(permissions.PermBonusManage, bonusHandlers.CancelTask))
	mux.Handle("POST /api/v1/bonus-claims/{id}/approve", permMiddleware(permissions.PermBonusManage, bonusHandlers.ApproveClaim))
	mux.Handle("POST /api/v1/bonus-claims/{id}/reject", permMiddleware(permissions.PermBonusManage, bonusHandlers.RejectClaim))
	mux.Handle("POST /api/v1/bonus-claims/{id}/unassign", permMiddleware(permissions.PermBonusManage, bonusHandlers.UnassignClaim))

	// Wall devices (M6-02). Managing devices is parent-only, so children get 403. Pairing is public: the tablet
	// has no session yet, so the one-time key and the per-IP limit are its protection.
	wallHandlers := wall.NewHandlers(pool, auth.NewRateLimiter(clk), clk, log, wxService, avatarDir)
	mux.Handle("POST /api/v1/wall/devices", permissions.RequireParent(pool, clk)(wallHandlers.CreateDevice))
	mux.Handle("GET /api/v1/wall/devices", permissions.RequireParent(pool, clk)(wallHandlers.ListDevices))
	mux.Handle("PATCH /api/v1/wall/devices/{id}", permissions.RequireParent(pool, clk)(wallHandlers.UpdateDevice))
	mux.HandleFunc("POST /api/v1/wall/pair", wallHandlers.Pair)
	mux.HandleFunc("GET /api/v1/wall/device", wallHandlers.RequireDevice(wallHandlers.GetDevice))
	mux.HandleFunc("GET /api/v1/wall/summary", wallHandlers.RequireDevice(wallHandlers.Summary))
	mux.HandleFunc("GET /api/v1/wall/events", wallHandlers.RequireDevice(wallHandlers.Events))
	mux.HandleFunc("GET /api/v1/wall/avatars/{id}", wallHandlers.RequireDevice(wallHandlers.ServeAvatar))
	mux.HandleFunc("POST /api/v1/wall/exit/verify", wallHandlers.RequireDevice(wallHandlers.VerifyExitCode))
	mux.HandleFunc("POST /api/v1/wall/unpair", wallHandlers.RequireDevice(wallHandlers.UnpairDevice))
	mux.Handle("POST /api/v1/wall/devices/{id}/revoke", permissions.RequireParent(pool, clk)(wallHandlers.RevokeDevice))

	// Health routes
	health.Routes(mux, clk, pool)

	// Middleware order matters: the CSRF token cookie must be issued (outer)
	// before CSRF validation (inner) runs on unsafe methods.
	handler := requestIDMiddleware(
		loggingMiddleware(
			auth.CSRFTokenMiddleware()(auth.CSRFMiddleware(mux)),
			log,
			clk,
			pool,
		),
	)

	return &Server{
		httpServer: &http.Server{
			Addr:              fmt.Sprintf(":%s", cfg.API.Port),
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		logger: log,
		clock:  clk,
	}
}

// Start runs the server and handles graceful shutdown.
func (s *Server) Start() error {
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	serverErrors := make(chan error, 1)

	go func() {
		s.logger.Info("starting server", slog.String("addr", s.httpServer.Addr))
		serverErrors <- s.httpServer.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		s.logger.Info("shutdown started", slog.String("signal", sig.String()))
		defer s.logger.Info("shutdown complete")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.httpServer.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}

// requestIDMiddleware adds a request ID to the request context and headers.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = generateRequestID()
		}

		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), requestIDKey{}, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// loggingMiddleware logs the incoming requests with structured logging and counts 5xx responses.
func loggingMiddleware(next http.Handler, log *slog.Logger, clk clock.Clock, db *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := clk.Now()

		reqID, _ := r.Context().Value(requestIDKey{}).(string)

		log.Info("request started",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			logger.RequestID(reqID),
		)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		if rec.status >= http.StatusInternalServerError {
			now := clk.Now()
			if err := diagnostics.RecordServerErrorEvent(r.Context(), db, now, r.URL.Path, rec.status); err != nil {
				log.Warn("failed to record diagnostics event", slog.String("error", err.Error()))
			}
		}
		log.Info("request completed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			logger.RequestID(reqID),
			slog.Int("status", rec.status),
			slog.Duration("duration", clk.Now().Sub(start)),
		)
	})
}

// statusRecorder keeps the response status so the logger can see it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Flush keeps streaming responses working through the wrapper.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func generateRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(bytes)
}
