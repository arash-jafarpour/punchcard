package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arash-jafarpour/punchcard/internal/testutil"
)

const testToken = "123456:bot-secret-token"

// writeReport creates a report file with the given name inside a temp dir.
func writeReport(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return path
}

// newSendClient points a client at the stub with the shared test token.
func newSendClient(t *testing.T, stub *testutil.TelegramStub, opts ...Option) *Client {
	t.Helper()
	client, err := New(stub.URL(), testToken, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// sleepRecorder collects the delays the retry loop asked for.
type sleepRecorder struct {
	delays []time.Duration
}

func (r *sleepRecorder) record(d time.Duration) { r.delays = append(r.delays, d) }

// roundTripFunc adapts a function into an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendDocumentUploadsMultipart(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Succeed(42)

	payload := []byte("# Report 2026-10-27\n\n- 3 commits\n")
	path := writeReport(t, "2026-10-27.md", payload)
	caption := "GitLab daily report 2026-10-27 · 3 projects · 12 commits"
	sleeps := &sleepRecorder{}
	client := newSendClient(t, stub, WithSleep(sleeps.record))

	result, err := client.SendDocument(context.Background(), "42", path, caption)
	if err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	if result == nil || result.MessageID != 42 {
		t.Fatalf("result = %+v, want MessageID 42", result)
	}
	if len(sleeps.delays) != 0 {
		t.Errorf("sleeps = %v, want none", sleeps.delays)
	}

	requests := stub.Requests()
	if len(requests) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(requests))
	}
	req := requests[0]
	if req.Err != nil {
		t.Fatalf("stub Err = %v, want nil", req.Err)
	}
	if req.HTTPMethod != http.MethodPost {
		t.Errorf("HTTPMethod = %q, want POST", req.HTTPMethod)
	}
	if req.Method != sendDocumentMethod {
		t.Errorf("Method = %q, want %q", req.Method, sendDocumentMethod)
	}
	if got, want := req.Path, "/bot"+testToken+"/"+sendDocumentMethod; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if req.Token != testToken {
		t.Errorf("Token = %q, want the test token", req.Token)
	}
	if req.RawQuery != "" {
		t.Errorf("RawQuery = %q, want empty", req.RawQuery)
	}
	if req.ChatID != "42" {
		t.Errorf("ChatID = %q, want 42", req.ChatID)
	}
	if req.Caption != caption {
		t.Errorf("Caption = %q, want %q", req.Caption, caption)
	}
	if req.DocumentName != "2026-10-27.md" {
		t.Errorf("DocumentName = %q, want 2026-10-27.md", req.DocumentName)
	}
	if req.DocumentType != documentMIME {
		t.Errorf("DocumentType = %q, want %q", req.DocumentType, documentMIME)
	}
	if !bytes.Equal(req.Document, payload) {
		t.Errorf("Document = %q, want %q", req.Document, payload)
	}
}

func TestSendDocumentEmptyCaptionIsPresent(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Succeed(7)
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	client := newSendClient(t, stub, WithSleep(func(time.Duration) {}))

	if _, err := client.SendDocument(context.Background(), "42", path, ""); err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	last := stub.Last()
	if last.Err != nil {
		t.Fatalf("stub Err = %v, want nil (the caption field must be present)", last.Err)
	}
	if last.Caption != "" {
		t.Errorf("Caption = %q, want empty", last.Caption)
	}
}

func TestSendDocumentOmitsParseMode(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	rawBody := make(chan []byte, 1)
	stub.Handle(sendDocumentMethod, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rawBody <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":5}}`)
	})
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	client := newSendClient(t, stub, WithSleep(func(time.Duration) {}))

	result, err := client.SendDocument(context.Background(), "42", path, "caption")
	if err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	if result == nil || result.MessageID != 5 {
		t.Fatalf("result = %+v, want MessageID 5", result)
	}

	body := <-rawBody
	if bytes.Contains(body, []byte("parse_mode")) {
		t.Errorf("multipart body contains parse_mode: %q", body)
	}
	if !bytes.Contains(body, []byte(`name="caption"`)) {
		t.Errorf("multipart body has no caption field: %q", body)
	}
}

func TestSendDocumentRateLimitHonoursRetryAfter(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.RateLimit(2)
	stub.Succeed(42)
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	sleeps := &sleepRecorder{}
	client := newSendClient(t, stub, WithSleep(sleeps.record))

	result, err := client.SendDocument(context.Background(), "42", path, "caption")
	if err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	if result == nil || result.MessageID != 42 {
		t.Fatalf("result = %+v, want MessageID 42", result)
	}
	if got := len(stub.Requests()); got != 2 {
		t.Errorf("recorded %d requests, want 2", got)
	}
	if !slices.Equal(sleeps.delays, []time.Duration{2 * time.Second}) {
		t.Errorf("sleeps = %v, want [2s]", sleeps.delays)
	}
	for i, req := range stub.Requests() {
		if req.Err != nil {
			t.Errorf("request %d: stub Err = %v, want nil", i, req.Err)
		}
		if !bytes.Equal(req.Document, []byte("report")) {
			t.Errorf("request %d: Document = %q, want a fresh copy of the file", i, req.Document)
		}
	}
}

func TestSendDocumentRateLimitWithoutRetryAfterFallsBackToBackoff(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Script(testutil.TelegramResponse{
		Status: http.StatusTooManyRequests,
		Body:   `{"ok":false,"error_code":429,"description":"Too Many Requests"}`,
	})
	stub.Succeed(42)
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	sleeps := &sleepRecorder{}
	client := newSendClient(t, stub, WithSleep(sleeps.record))

	if _, err := client.SendDocument(context.Background(), "42", path, "caption"); err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	if got := len(stub.Requests()); got != 2 {
		t.Errorf("recorded %d requests, want 2", got)
	}
	if !slices.Equal(sleeps.delays, []time.Duration{1 * time.Second}) {
		t.Errorf("sleeps = %v, want [1s] (standard backoff)", sleeps.delays)
	}
}

func TestSendDocumentRetriesServerErrorThenSucceeds(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.ServerError()
	stub.Succeed(7)
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	sleeps := &sleepRecorder{}
	client := newSendClient(t, stub, WithSleep(sleeps.record))

	result, err := client.SendDocument(context.Background(), "42", path, "caption")
	if err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	if result == nil || result.MessageID != 7 {
		t.Fatalf("result = %+v, want MessageID 7", result)
	}
	if got := len(stub.Requests()); got != 2 {
		t.Errorf("recorded %d requests, want 2", got)
	}
	if !slices.Equal(sleeps.delays, []time.Duration{1 * time.Second}) {
		t.Errorf("sleeps = %v, want [1s]", sleeps.delays)
	}
}

func TestSendDocumentExhaustsRetriesOnServerError(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.ServerError()
	stub.ServerError()
	stub.ServerError()
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	sleeps := &sleepRecorder{}
	client := newSendClient(t, stub, WithSleep(sleeps.record))

	_, err := client.SendDocument(context.Background(), "42", path, "caption")
	if err == nil {
		t.Fatal("SendDocument succeeded, want a 500 error")
	}
	if got := len(stub.Requests()); got != 3 {
		t.Errorf("recorded %d requests, want 3", got)
	}
	if !slices.Equal(sleeps.delays, []time.Duration{1 * time.Second, 2 * time.Second}) {
		t.Errorf("sleeps = %v, want [1s 2s]", sleeps.delays)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if apiErr.ErrorCode != http.StatusInternalServerError {
		t.Errorf("ErrorCode = %d, want 500", apiErr.ErrorCode)
	}
	if apiErr.Description != "Internal Server Error" {
		t.Errorf("Description = %q, want Internal Server Error", apiErr.Description)
	}
	if apiErr.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0", apiErr.RetryAfter)
	}
	if !IsStatus(err, http.StatusInternalServerError) {
		t.Error("IsStatus(err, 500) = false, want true")
	}
}

func TestSendDocumentClientErrorDoesNotRetry(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Fail(http.StatusBadRequest, "Bad Request: chat not found")
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	sleeps := &sleepRecorder{}
	client := newSendClient(t, stub, WithSleep(sleeps.record))

	_, err := client.SendDocument(context.Background(), "42", path, "caption")
	if err == nil {
		t.Fatal("SendDocument succeeded, want a 400 error")
	}
	if got := len(stub.Requests()); got != 1 {
		t.Errorf("recorded %d requests, want 1", got)
	}
	if len(sleeps.delays) != 0 {
		t.Errorf("sleeps = %v, want none", sleeps.delays)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
	if apiErr.ErrorCode != http.StatusBadRequest {
		t.Errorf("ErrorCode = %d, want 400", apiErr.ErrorCode)
	}
	if apiErr.Description != "Bad Request: chat not found" {
		t.Errorf("Description = %q, want the server description", apiErr.Description)
	}
	if !IsStatus(err, http.StatusBadRequest) {
		t.Error("IsStatus(err, 400) = false, want true")
	}
	if IsStatus(err, http.StatusInternalServerError) {
		t.Error("IsStatus(err, 500) = true, want false")
	}
}

func TestSendDocumentRetriesTransportErrors(t *testing.T) {
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	var calls atomic.Int32
	sleeps := &sleepRecorder{}
	client, err := New("https://api.telegram.org", testToken,
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("dial tcp: connection refused")
		})}),
		WithSleep(sleeps.record),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.SendDocument(context.Background(), "42", path, "caption")
	if err == nil {
		t.Fatal("SendDocument succeeded, want a transport error")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("transport calls = %d, want 3", got)
	}
	if !slices.Equal(sleeps.delays, []time.Duration{1 * time.Second, 2 * time.Second}) {
		t.Errorf("sleeps = %v, want [1s 2s]", sleeps.delays)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, want the inner transport error", err)
	}
}

func TestSendDocumentStopsWhenContextCancelled(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	client := newSendClient(t, stub, WithSleep(func(time.Duration) {}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.SendDocument(ctx, "42", path, "caption")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := len(stub.Requests()); got != 0 {
		t.Errorf("recorded %d requests, want 0", got)
	}
}

func TestSendDocumentRejectsOversizedCaption(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Succeed(1)
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	client := newSendClient(t, stub, WithSleep(func(time.Duration) {}))

	caption := strings.Repeat("a", maxCaptionBytes+1)
	_, err := client.SendDocument(context.Background(), "42", path, caption)
	if err == nil {
		t.Fatal("SendDocument accepted a 1025-byte caption")
	}
	if msg := err.Error(); !strings.Contains(msg, "1025") || !strings.Contains(msg, "1024") {
		t.Errorf("error = %q, want the observed size and the limit", msg)
	}
	if got := len(stub.Requests()); got != 0 {
		t.Errorf("recorded %d requests, want 0", got)
	}
}

func TestSendDocumentRejectsOversizedDocument(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Succeed(1)
	path := filepath.Join(t.TempDir(), "huge.md")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := file.Truncate(maxDocumentBytes + 1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	client := newSendClient(t, stub, WithSleep(func(time.Duration) {}))

	_, err = client.SendDocument(context.Background(), "42", path, "caption")
	if err == nil {
		t.Fatal("SendDocument accepted a document over 50 MB")
	}
	msg := err.Error()
	if !strings.Contains(msg, fmt.Sprint(maxDocumentBytes+1)) || !strings.Contains(msg, fmt.Sprint(maxDocumentBytes)) {
		t.Errorf("error = %q, want the observed size and the limit", msg)
	}
	if got := len(stub.Requests()); got != 0 {
		t.Errorf("recorded %d requests, want 0", got)
	}
}

func TestSendDocumentRejectsUnusableArguments(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Succeed(1)
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	client := newSendClient(t, stub, WithSleep(func(time.Duration) {}))

	tests := []struct {
		name   string
		chatID string
		path   string
		want   string
	}{
		{name: "empty chat id", chatID: "", path: path, want: "chat id"},
		{name: "whitespace chat id", chatID: " \t", path: path, want: "chat id"},
		{name: "empty path", chatID: "42", path: "", want: "path"},
		{name: "missing file", chatID: "42", path: filepath.Join(t.TempDir(), "missing.md"), want: "stat"},
		{name: "directory", chatID: "42", path: t.TempDir(), want: "regular file"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.SendDocument(context.Background(), test.chatID, test.path, "caption")
			if err == nil {
				t.Fatal("SendDocument succeeded, want an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to mention %q", err, test.want)
			}
		})
	}
	if got := len(stub.Requests()); got != 0 {
		t.Errorf("recorded %d requests, want 0", got)
	}
}

func TestSendDocumentKeepsFileOnFailure(t *testing.T) {
	stub := testutil.NewTelegramStub(t)
	stub.Fail(http.StatusBadRequest, "Bad Request: chat not found")
	payload := []byte("# Report\n\n- 3 commits\n")
	path := writeReport(t, "2026-10-27.md", payload)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	client := newSendClient(t, stub, WithSleep(func(time.Duration) {}))

	if _, err := client.SendDocument(context.Background(), "42", path, "caption"); err == nil {
		t.Fatal("SendDocument succeeded, want a 400 error")
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v (the report must stay on disk)", err)
	}
	if after.Size() != before.Size() {
		t.Errorf("size = %d, want %d", after.Size(), before.Size())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("content = %q, want %q", got, payload)
	}
}

func TestSendDocumentNeverLeaksToken(t *testing.T) {
	path := writeReport(t, "2026-10-27.md", []byte("report"))

	tests := []struct {
		name  string
		setup func(t *testing.T) (*Client, context.Context)
	}{
		{
			name: "api error",
			setup: func(t *testing.T) (*Client, context.Context) {
				stub := testutil.NewTelegramStub(t)
				stub.Fail(http.StatusBadRequest, "Bad Request: chat not found")
				return newSendClient(t, stub, WithSleep(func(time.Duration) {})), context.Background()
			},
		},
		{
			name: "server error",
			setup: func(t *testing.T) (*Client, context.Context) {
				stub := testutil.NewTelegramStub(t)
				stub.ServerError()
				stub.ServerError()
				stub.ServerError()
				return newSendClient(t, stub, WithSleep(func(time.Duration) {})), context.Background()
			},
		},
		{
			name: "transport error",
			setup: func(t *testing.T) (*Client, context.Context) {
				client, err := New("https://api.telegram.org", testToken,
					WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						return nil, errors.New("dial tcp: connection refused")
					})}),
					WithSleep(func(time.Duration) {}),
				)
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return client, context.Background()
			},
		},
		{
			name: "cancelled context",
			setup: func(t *testing.T) (*Client, context.Context) {
				stub := testutil.NewTelegramStub(t)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return newSendClient(t, stub, WithSleep(func(time.Duration) {})), ctx
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, ctx := test.setup(t)
			_, err := client.SendDocument(ctx, "42", path, "caption")
			if err == nil {
				t.Fatal("SendDocument succeeded, want an error")
			}
			msg := err.Error()
			if strings.Contains(msg, testToken) {
				t.Errorf("error %q leaks the bot token", msg)
			}
			if strings.Contains(msg, "/bot") {
				t.Errorf("error %q carries the request path", msg)
			}
		})
	}
}

func TestSendDocumentRedactsTokenFromTransportError(t *testing.T) {
	path := writeReport(t, "2026-10-27.md", []byte("report"))
	client, err := New("https://api.telegram.org", testToken,
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("proxy rejected %s", r.URL.String())
		})}),
		WithSleep(func(time.Duration) {}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.SendDocument(context.Background(), "42", path, "caption")
	if err == nil {
		t.Fatal("SendDocument succeeded, want a transport error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error %q leaks the bot token", err)
	}
	if !strings.Contains(err.Error(), redactedValue) {
		t.Errorf("error = %q, want a redacted token", err)
	}
}

func TestAPIErrorFormatting(t *testing.T) {
	err := &APIError{StatusCode: 429, ErrorCode: 429, Description: "Too Many Requests", RetryAfter: 2 * time.Second}
	if got, want := err.Error(), "telegram: sendDocument: 429 Too Many Requests (retry after 2s)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	err = &APIError{StatusCode: http.StatusInternalServerError}
	if got, want := err.Error(), "telegram: sendDocument: 500 Internal Server Error"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	err = &APIError{StatusCode: http.StatusNotFound, Description: "  Not Found: bot was blocked  "}
	if got, want := err.Error(), "telegram: sendDocument: 404 Not Found: bot was blocked"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
