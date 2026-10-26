package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

func TestCheckTelegramConnection(t *testing.T) {
	var mu sync.Mutex
	var sent []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botGOOD/getMe":
			_, _ = io.WriteString(w, `{"ok":true,"result":{"username":"proxima_alerts_bot"}}`)
		case "/botGOOD/sendMessage":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sent = append(sent, body)
			mu.Unlock()
			if body["chat_id"] == "-100bad" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"ok":false,"description":"Bad Request: chat not found"}`)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"ok":false,"description":"Unauthorized"}`)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	res := CheckTelegramConnection(ctx, srv.URL, "GOOD", "-1001")
	if !res.OK || len(res.Checks) != 3 || res.Checks[1].Detail != "@proxima_alerts_bot" || res.ElapsedMS < 0 {
		t.Fatalf("working bot: %+v", res)
	}
	if len(sent) != 1 || sent[0]["chat_id"] != "-1001" || sent[0]["text"] == "" {
		t.Fatalf("one test message must go to the chat: %v", sent)
	}

	res = CheckTelegramConnection(ctx, srv.URL, "WRONG", "-1001")
	if res.OK || res.Checks[len(res.Checks)-1].Step != "token" || res.Checks[len(res.Checks)-1].Detail != "Unauthorized" {
		t.Fatalf("bad token must fail at the token step: %+v", res)
	}

	res = CheckTelegramConnection(ctx, srv.URL, "GOOD", "-100bad")
	last := res.Checks[len(res.Checks)-1]
	if res.OK || last.Step != "send" || !strings.Contains(last.Detail, "chat not found") {
		t.Fatalf("unknown chat must fail at the send step with Telegram's reason: %+v", res)
	}

	res = CheckTelegramConnection(ctx, srv.URL, "", "-1001")
	if res.OK || len(res.Checks) != 1 || res.Checks[0].Step != "config" {
		t.Fatalf("missing token must stop at config: %+v", res)
	}

	res = CheckTelegramConnection(ctx, "http://127.0.0.1:1", "SECRET-TOKEN", "-1001")
	for _, c := range res.Checks {
		if strings.Contains(c.Detail, "SECRET-TOKEN") {
			t.Fatalf("an unreachable API must not leak the bot token: %+v", res)
		}
	}
}

type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	deny    bool
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deny {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
		return
	}
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[r.URL.Path] = b
	case http.MethodGet:
		b, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>not found</Message></Error>`)
			return
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objects, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestCheckS3Connection(t *testing.T) {
	store := &fakeS3{objects: map[string][]byte{}}
	srv := httptest.NewServer(store)
	defer srv.Close()
	cfg := config.S3Config{Endpoint: srv.URL, Bucket: "backups", AccessKey: "AK", SecretKey: "SK", Region: "auto"}
	ctx := context.Background()

	res := CheckS3Connection(ctx, cfg)
	if !res.OK || len(res.Checks) != 4 {
		t.Fatalf("working bucket: %+v", res)
	}
	if len(store.objects) != 0 {
		t.Fatalf("the probe object must be deleted, left %v", store.objects)
	}

	store.deny = true
	res = CheckS3Connection(ctx, cfg)
	last := res.Checks[len(res.Checks)-1]
	if res.OK || last.Step != "write" || !strings.Contains(last.Detail, "AccessDenied") {
		t.Fatalf("denied bucket must fail at write with the S3 error code: %+v", res)
	}

	res = CheckS3Connection(ctx, config.S3Config{Endpoint: srv.URL})
	if res.OK || len(res.Checks) != 1 || !strings.Contains(res.Checks[0].Detail, "bucket") {
		t.Fatalf("missing fields must stop at config and name them: %+v", res)
	}
}

func TestMergeS3KeepsSavedSecretsForBlankFields(t *testing.T) {
	got := MergeS3(config.S3Config{Endpoint: "https://new"}, config.S3Config{Endpoint: "https://old", Bucket: "b", AccessKey: "a", SecretKey: "s", Region: "r"})
	want := config.S3Config{Endpoint: "https://new", Bucket: "b", AccessKey: "a", SecretKey: "s", Region: "r"}
	if got != want {
		t.Fatalf("MergeS3 = %+v, want %+v", got, want)
	}
}
