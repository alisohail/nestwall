package notifypolicy

import (
	"html"
	"strings"
)

// DefaultTemplate is the HTML body used when a family has not saved its own. The placeholders are
// {{subject}}, {{body}}, {{logo_url}}, {{app_url}} and {{family_name}}.
const DefaultTemplate = `<!doctype html>
<html>
<body style="margin:0;padding:24px;background:#f4f6f8;font-family:Arial,Helvetica,sans-serif;color:#1d2433;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:12px;">
    <tr>
      <td style="padding:24px;">
        <a href="{{app_url}}" style="text-decoration:none;">
          <img src="{{logo_url}}" alt="NestWall" height="36" style="display:block;border:0;" />
        </a>
        <h1 style="font-size:20px;margin:20px 0 8px;">{{subject}}</h1>
        <p style="font-size:14px;color:#5a6474;margin:0 0 16px;">{{family_name}}</p>
        {{body}}
        <p style="margin:24px 0 0;">
          <a href="{{app_url}}" style="color:#0f766e;">Open NestWall</a>
        </p>
      </td>
    </tr>
  </table>
</body>
</html>`

// LogoSrc is the image source for {{logo_url}}. It refers to the logo attached to the message, so
// the image does not depend on a server address the recipient may not reach. It must match email.LogoCID;
// email cannot be imported here because it depends on auth, which depends on this package.
const LogoSrc = "cid:nestwall-logo"

// SampleSubject and SampleBody fill the preview shown on the template page.
const (
	SampleSubject = "Task approved: Tidy the kitchen"
	sampleBody    = `<p style="font-size:15px;line-height:1.5;margin:0;">Sam's task <strong>Tidy the kitchen</strong> was approved. 10 points were added.</p>`
)

// SampleBody returns the example body used by the template preview.
func SampleBody() string { return sampleBody }

// Placeholder values for a rendered email.
type Fields struct {
	Subject    string
	Body       string // already HTML
	LogoURL    string
	AppURL     string
	FamilyName string
}

// Render fills the template placeholders. Text values are escaped. Body is inserted as HTML by the
// caller. An empty template uses DefaultTemplate.
func Render(template string, f Fields) string {
	if strings.TrimSpace(template) == "" {
		template = DefaultTemplate
	}
	r := strings.NewReplacer(
		"{{subject}}", html.EscapeString(f.Subject),
		"{{body}}", f.Body,
		"{{logo_url}}", html.EscapeString(f.LogoURL),
		"{{app_url}}", html.EscapeString(f.AppURL),
		"{{family_name}}", html.EscapeString(f.FamilyName),
	)
	return r.Replace(template)
}
