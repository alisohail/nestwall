package email

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// The logo is attached inline only when the HTML refers to it, so plain and other HTML mail is unchanged.
func TestBuildMessageEmbedsLogoForCIDReference(t *testing.T) {
	raw := buildMessage(Account{FromAddress: "from@example.com"}, Message{
		To: "parent@example.com", Subject: "Hi", Body: `<img src="cid:nestwall-logo">`, HTML: true,
	})
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/related" {
		t.Fatalf("content type = %q (%v), want multipart/related", msg.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(msg.Body, params["boundary"])

	htmlPart, err := reader.NextPart()
	if err != nil {
		t.Fatalf("html part: %v", err)
	}
	htmlBody, _ := io.ReadAll(htmlPart)
	if !strings.Contains(string(htmlBody), `src="cid:nestwall-logo"`) {
		t.Fatalf("html part lost the cid reference: %s", htmlBody)
	}

	imgPart, err := reader.NextPart()
	if err != nil {
		t.Fatalf("image part: %v", err)
	}
	if got := imgPart.Header.Get("Content-ID"); got != "<nestwall-logo>" {
		t.Fatalf("Content-ID = %q", got)
	}
	if got := imgPart.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("image Content-Type = %q", got)
	}
	encoded, _ := io.ReadAll(imgPart)
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(encoded), "\r\n", ""))
	if err != nil {
		t.Fatalf("decode image: %v", err)
	}
	if !bytes.Equal(decoded, logoPNG) || !bytes.HasPrefix(decoded, []byte("\x89PNG")) {
		t.Fatalf("attached image is not the NestWall PNG (%d bytes)", len(decoded))
	}
}

func TestBuildMessageWithoutLogoReferenceIsUnchanged(t *testing.T) {
	raw := string(buildMessage(Account{FromAddress: "from@example.com"}, Message{
		To: "parent@example.com", Subject: "Hi", Body: "<p>plain</p>", HTML: true,
	}))
	if strings.Contains(raw, "multipart/related") || strings.Contains(raw, "image/png") {
		t.Fatalf("message without a logo reference should not carry an image part:\n%s", raw)
	}
	if !strings.Contains(raw, "Content-Type: text/html; charset=utf-8\r\n\r\n<p>plain</p>\r\n") {
		t.Fatalf("html body not written as before:\n%s", raw)
	}
}
