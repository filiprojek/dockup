// Package notify sends update summaries to ntfy, Gotify or a generic webhook.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/filiprojek/dockup/internal/config"
	"github.com/filiprojek/dockup/internal/updates"
)

type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Data  any    `json:"data,omitempty"`
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

var client = &http.Client{Timeout: 15 * time.Second}

func post(ctx context.Context, url string, body io.Reader, header map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	for k, v := range header {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("POST %s: %s %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

type Ntfy config.Ntfy

func (n Ntfy) Send(ctx context.Context, m Message) error {
	h := map[string]string{"Title": m.Title, "Tags": "whale"}
	if n.Token != "" {
		h["Authorization"] = "Bearer " + n.Token
	}
	return post(ctx, strings.TrimRight(n.URL, "/")+"/"+n.Topic, strings.NewReader(m.Body), h)
}

type Gotify config.Gotify

func (g Gotify) Send(ctx context.Context, m Message) error {
	b, _ := json.Marshal(map[string]any{"title": m.Title, "message": m.Body, "priority": 5})
	return post(ctx, strings.TrimRight(g.URL, "/")+"/message", bytes.NewReader(b),
		map[string]string{"X-Gotify-Key": g.Token, "Content-Type": "application/json"})
}

type Webhook config.Webhook

func (w Webhook) Send(ctx context.Context, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return post(ctx, w.URL, bytes.NewReader(b), map[string]string{"Content-Type": "application/json"})
}

func FromConfig(c config.Notify) []Sender {
	var out []Sender
	if c.Ntfy != nil {
		out = append(out, Ntfy(*c.Ntfy))
	}
	if c.Gotify != nil {
		out = append(out, Gotify(*c.Gotify))
	}
	if c.Webhook != nil {
		out = append(out, Webhook(*c.Webhook))
	}
	return out
}

// SendAll sends to every sender, collecting errors.
func SendAll(ctx context.Context, senders []Sender, m Message) error {
	var errs []error
	for _, s := range senders {
		if err := s.Send(ctx, m); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// FromReport summarises a check. ok is false when there's nothing to say.
func FromReport(r updates.Report) (Message, bool) {
	need := r.NeedsUpdate()
	errCount := r.Counts()[updates.StatusError]
	if len(need) == 0 && errCount == 0 {
		return Message{}, false
	}
	projects := r.ProjectsNeedingUpdate()
	byProject := map[string][]string{}
	for _, x := range need {
		byProject[x.Project] = append(byProject[x.Project], x.Service)
	}
	var lines []string
	for _, p := range projects {
		lines = append(lines, p+": "+strings.Join(byProject[p], ", "))
	}
	if errCount > 0 {
		lines = append(lines, fmt.Sprintf("(%d check %s)", errCount, plural(errCount, "error", "errors")))
	}
	return Message{
		Title: fmt.Sprintf("dockup: %d %s in %d %s", len(need), plural(len(need), "update", "updates"), len(projects), plural(len(projects), "project", "projects")),
		Body:  strings.Join(lines, "\n"),
		Data:  need,
	}, true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
