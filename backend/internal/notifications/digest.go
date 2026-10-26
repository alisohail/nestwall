package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"nestwall/backend/internal/email"
	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
)

// Digester sends the email copies of notices that are waiting in the outbox queue.
type Digester struct {
	db     *sql.DB
	clock  clock.Clock
	log    *slog.Logger
	outbox *email.Outbox
	appURL string
}

// NewDigester returns a digester that sends through the given outbox.
func NewDigester(db *sql.DB, clk clock.Clock, log *slog.Logger, outbox *email.Outbox, appURL string) *Digester {
	return &Digester{db: db, clock: clk, log: log, outbox: outbox, appURL: strings.TrimRight(appURL, "/")}
}

// pollInterval is how often the digester checks for due email. Each family's own digest interval
// decides whether its notices go out on a given check.
const pollInterval = time.Minute

// Run checks for due email every minute until ctx is cancelled.
func (d *Digester) Run(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := d.SendDue(ctx); err != nil && ctx.Err() == nil {
				d.log.Error("notification digest failed", slog.String("error", err.Error()))
			}
		}
	}
}

// pendingNotice is one notice waiting for email, with what the email needs.
type pendingNotice struct {
	id, familyID, recipientID, body, familyName, to, template string
	digest                                                    bool
	digestMinutes                                             int
	digestLastSent                                            sql.NullInt64
}

// due reports whether the notice may be emailed now. A notice with digest off is always due. A
// digest is due once its family's interval has passed since the last digest was sent.
func (p pendingNotice) due(now time.Time) bool {
	if !p.digest || !p.digestLastSent.Valid {
		return true
	}
	return now.Unix()-p.digestLastSent.Int64 >= int64(p.digestMinutes)*60
}

// SendDue enqueues the due pending notices as email and clears their pending flag. With digest on,
// a parent gets one email listing their pending notices, at most once per the family's interval.
// With digest off, each notice is its own email. It returns the number of emails queued.
func (d *Digester) SendDue(ctx context.Context) (int, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT n.id, n.family_id, n.recipient_id, n.body, f.name, u.email,
		       s.email_digest, s.email_template, s.digest_minutes, s.digest_last_sent_at
		  FROM notifications n
		  JOIN families f ON f.id = n.family_id
		  JOIN notification_settings s ON s.family_id = n.family_id AND s.email = 1
		  JOIN family_members m ON m.id = n.recipient_id AND m.status = 'active'
		  JOIN users u ON u.id = m.user_id
		 WHERE n.email_pending = 1 AND u.email IS NOT NULL AND u.email <> ''
		 ORDER BY n.created_at, n.id`)
	if err != nil {
		return 0, fmt.Errorf("load pending notices: %w", err)
	}
	now := d.clock.Now()
	var pending []pendingNotice
	for rows.Next() {
		var p pendingNotice
		var digest int
		if err := rows.Scan(&p.id, &p.familyID, &p.recipientID, &p.body, &p.familyName, &p.to,
			&digest, &p.template, &p.digestMinutes, &p.digestLastSent); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan pending notice: %w", err)
		}
		p.digest = digest == 1
		if p.due(now) {
			pending = append(pending, p)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("pending notices: %w", err)
	}
	rows.Close()

	sent := 0
	sentFamilies := map[string]bool{}
	for _, group := range groupNotices(pending) {
		if err := d.sendGroup(ctx, group); err != nil {
			d.log.Error("notification email failed", slog.String("error", err.Error()))
			continue
		}
		sent++
		if group[0].digest {
			sentFamilies[group[0].familyID] = true
		}
	}
	for familyID := range sentFamilies {
		if _, err := d.db.ExecContext(ctx,
			`UPDATE notification_settings SET digest_last_sent_at = ? WHERE family_id = ?`,
			now.Unix(), familyID); err != nil {
			d.log.Error("record digest send time", slog.String("error", err.Error()))
		}
	}
	return sent, nil
}

// groupNotices splits pending notices into emails. A digest group holds one recipient's notices;
// otherwise each notice stands alone. The order of the input is kept.
func groupNotices(all []pendingNotice) [][]pendingNotice {
	var groups [][]pendingNotice
	index := map[string]int{}
	for _, p := range all {
		if !p.digest {
			groups = append(groups, []pendingNotice{p})
			continue
		}
		key := p.recipientID
		if i, ok := index[key]; ok {
			groups[i] = append(groups[i], p)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, []pendingNotice{p})
	}
	return groups
}

// sendGroup queues one email for a group and clears the pending flag for its notices. The flag is
// only cleared after the email is stored, so a failure is retried on the next run.
func (d *Digester) sendGroup(ctx context.Context, group []pendingNotice) error {
	first := group[0]
	subject, body := d.compose(group)
	rendered := notifypolicy.Render(first.template, notifypolicy.Fields{
		Subject:    subject,
		Body:       body,
		LogoURL:    notifypolicy.LogoSrc,
		AppURL:     d.appURL,
		FamilyName: first.familyName,
	})
	if err := d.outbox.EnqueueHTML(ctx, first.familyID, first.to, subject, rendered); err != nil {
		return fmt.Errorf("queue email for family %s: %w", first.familyID, err)
	}
	for _, p := range group {
		if _, err := d.db.ExecContext(ctx,
			`UPDATE notifications SET email_pending = 0 WHERE id = ?`, p.id); err != nil {
			return fmt.Errorf("clear email_pending: %w", err)
		}
	}
	return nil
}

// compose returns the subject and the HTML body for a group of notices.
func (d *Digester) compose(group []pendingNotice) (string, string) {
	if len(group) == 1 {
		return "NestWall: " + group[0].body, "<p>" + html.EscapeString(group[0].body) + "</p>"
	}
	var b strings.Builder
	b.WriteString("<ul>")
	for _, p := range group {
		b.WriteString("<li>")
		b.WriteString(html.EscapeString(p.body))
		b.WriteString("</li>")
	}
	b.WriteString("</ul>")
	return fmt.Sprintf("NestWall: %d new notifications", len(group)), b.String()
}
