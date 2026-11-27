package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nikolap994/magnetar/internal/config"
)

func TestSendNtfyAndWebhook(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/topic":
			got = append(got, "ntfy:"+r.Header.Get("Title")+":"+string(b))
		case "/hook":
			var m map[string]any
			json.Unmarshal(b, &m)
			got = append(got, "hook:"+m["title"].(string)+":"+m["message"].(string))
		}
	}))
	defer srv.Close()
	s := config.Defaults()
	if Configured(s) {
		t.Fatal("nothing configured by default")
	}
	s.NotifyNtfyURL, s.NotifyWebhookURL = srv.URL+"/topic", srv.URL+"/hook"
	if err := Send(context.Background(), srv.Client(), s, "Download complete", "Linux ISO"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "ntfy:Download complete:Linux ISO" || got[1] != "hook:Download complete:Linux ISO" {
		t.Fatalf("got %v", got)
	}
}
