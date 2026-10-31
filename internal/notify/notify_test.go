package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/filiprojek/dockup/internal/config"
	"github.com/filiprojek/dockup/internal/updates"
)

type captured struct {
	method, path, query string
	header              http.Header
	body                string
}

func server(t *testing.T, status int) (*httptest.Server, *[]captured) {
	var got []captured
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, captured{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(b)})
		w.WriteHeader(status)
	}))
	t.Cleanup(s.Close)
	return s, &got
}

var msg = Message{Title: "dockup: 2 updates", Body: "immich: immich-server\ngitea: server", Data: map[string]any{"count": 2}}

func TestNtfy(t *testing.T) {
	s, got := server(t, 200)
	err := Ntfy{URL: s.URL + "/", Topic: "srv", Token: "tk"}.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	c := (*got)[0]
	if c.method != "POST" || c.path != "/srv" || c.body != msg.Body {
		t.Errorf("%+v", c)
	}
	if c.header.Get("Title") != msg.Title || c.header.Get("Authorization") != "Bearer tk" {
		t.Errorf("headers %v", c.header)
	}
}

func TestNtfyNoToken(t *testing.T) {
	s, got := server(t, 200)
	Ntfy{URL: s.URL, Topic: "srv"}.Send(context.Background(), msg)
	if h := (*got)[0].header.Get("Authorization"); h != "" {
		t.Errorf("unexpected auth header %q", h)
	}
}

func TestGotify(t *testing.T) {
	s, got := server(t, 200)
	if err := (Gotify{URL: s.URL, Token: "gt"}).Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	c := (*got)[0]
	if c.path != "/message" || c.header.Get("X-Gotify-Key") != "gt" {
		t.Errorf("%+v", c)
	}
	var p struct {
		Title, Message string
		Priority       int
	}
	json.Unmarshal([]byte(c.body), &p)
	if p.Title != msg.Title || p.Message != msg.Body || p.Priority == 0 {
		t.Errorf("payload %+v", p)
	}
}

func TestWebhook(t *testing.T) {
	s, got := server(t, 204)
	if err := (Webhook{URL: s.URL + "/hook"}).Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	c := (*got)[0]
	if c.header.Get("Content-Type") != "application/json" {
		t.Errorf("content-type %q", c.header.Get("Content-Type"))
	}
	var p struct {
		Title, Body string
		Data        map[string]any
	}
	json.Unmarshal([]byte(c.body), &p)
	if p.Title != msg.Title || p.Data["count"] != 2.0 {
		t.Errorf("payload %s", c.body)
	}
}

func TestHTTPErrorStatus(t *testing.T) {
	s, _ := server(t, 500)
	if err := (Webhook{URL: s.URL}).Send(context.Background(), msg); err == nil {
		t.Error("expected error on 500")
	}
}

func TestFromConfigAndSendAll(t *testing.T) {
	s1, g1 := server(t, 200)
	s2, _ := server(t, 500)
	senders := FromConfig(config.Notify{
		Ntfy:    &config.Ntfy{URL: s1.URL, Topic: "a"},
		Webhook: &config.Webhook{URL: s2.URL},
	})
	if len(senders) != 2 {
		t.Fatalf("%d senders", len(senders))
	}
	err := SendAll(context.Background(), senders, msg)
	if err == nil || len(*g1) != 1 {
		t.Errorf("one failing sender should not stop others; err=%v", err)
	}
	if len(FromConfig(config.Notify{})) != 0 {
		t.Error("empty config should yield no senders")
	}
}

func TestFromReport(t *testing.T) {
	rep := updates.Report{Results: []updates.Result{
		{Project: "immich", Service: "immich-server", Status: updates.StatusOutdated},
		{Project: "immich", Service: "redis", Status: updates.StatusUpToDate},
		{Project: "immich", Service: "ml", Status: updates.StatusPendingRecreate},
		{Project: "gitea", Service: "server", Status: updates.StatusOutdated},
		{Project: "x", Service: "y", Status: updates.StatusError, Reason: "401"},
	}}
	m, ok := FromReport(rep)
	if !ok {
		t.Fatal("expected a message")
	}
	if m.Title != "dockup: 3 updates in 2 projects" {
		t.Errorf("title %q", m.Title)
	}
	if !strings.Contains(m.Body, "immich: immich-server, ml") || !strings.Contains(m.Body, "gitea: server") {
		t.Errorf("body %q", m.Body)
	}
	if !strings.Contains(m.Body, "1 check error") {
		t.Errorf("errors not mentioned: %q", m.Body)
	}

	if _, ok := FromReport(updates.Report{Results: []updates.Result{{Status: updates.StatusUpToDate}}}); ok {
		t.Error("nothing to notify should return false")
	}
}
