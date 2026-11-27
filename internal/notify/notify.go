// Package notify sends push notifications to the user's phone through ntfy,
// Telegram or a generic webhook.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
)

// Configured reports whether any channel is set up.
func Configured(s config.Settings) bool {
	return s.NotifyNtfyURL != "" || (s.NotifyTelegramToken != "" && s.NotifyTelegramChat != "") || s.NotifyWebhookURL != ""
}

// Send delivers a message to every configured channel and returns the
// errors joined.
func Send(ctx context.Context, client *http.Client, s config.Settings, title, body string) error {
	var errs []error
	if s.NotifyNtfyURL != "" {
		errs = append(errs, ntfy(ctx, client, s.NotifyNtfyURL, title, body))
	}
	if s.NotifyTelegramToken != "" && s.NotifyTelegramChat != "" {
		errs = append(errs, telegram(ctx, client, s.NotifyTelegramToken, s.NotifyTelegramChat, title, body))
	}
	if s.NotifyWebhookURL != "" {
		errs = append(errs, webhook(ctx, client, s.NotifyWebhookURL, title, body))
	}
	return errors.Join(errs...)
}

func do(client *http.Client, req *http.Request, what string) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: server answered %s", what, resp.Status)
	}
	return nil
}

// ntfy: POST the body to the topic URL (https://ntfy.sh/<topic> or a
// self-hosted server); the title goes in a header.
func ntfy(ctx context.Context, client *http.Client, topicURL, title, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, topicURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", headerSafe(title))
	req.Header.Set("Tags", "magnet")
	return do(client, req, "ntfy")
}

func telegram(ctx context.Context, client *http.Client, token, chat, title, body string) error {
	payload, _ := json.Marshal(map[string]any{"chat_id": chat, "text": title + "\n" + body, "disable_web_page_preview": true})
	u := "https://api.telegram.org/bot" + url.PathEscape(token) + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(client, req, "Telegram")
}

// webhook posts JSON {"title","message","app","time"}; Discord and Slack style
// "content"/"text" fields are included so their webhooks work as well.
func webhook(ctx context.Context, client *http.Client, hookURL, title, body string) error {
	text := title + ": " + body
	payload, _ := json.Marshal(map[string]any{"title": title, "message": body, "app": "Magnetar",
		"time": time.Now().UTC().Format(time.RFC3339), "content": text, "text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(client, req, "webhook")
}

// headerSafe keeps header values to printable ASCII (ntfy reads UTF-8 titles
// fine, but Go refuses some characters in headers).
func headerSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}
