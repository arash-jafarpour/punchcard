package testutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TelegramResponse is one scripted answer for the fake Bot API server. Status
// is the HTTP status code and Body is written verbatim. A zero Status means
// 200; an empty Body means the default success envelope with the next
// automatic message id.
type TelegramResponse struct {
	Status int
	Body   string
}

// TelegramRequest is one request received by TelegramStub. Method is the Bot
// API method parsed from /bot<token>/<method> (for example "sendDocument"),
// while HTTPMethod is the HTTP verb. Token is the bot token from the same
// path. Err carries malformed-path and malformed-multipart diagnostics; no
// stub handler ever fails the test directly.
type TelegramRequest struct {
	Method       string
	HTTPMethod   string
	Token        string
	Path         string
	RawQuery     string
	ChatID       string
	Caption      string
	DocumentName string
	DocumentType string
	Document     []byte
	Err          error
}

// TelegramStub is a scriptable fake Telegram Bot API server.
type TelegramStub struct {
	t      *testing.T
	server *httptest.Server

	mu            sync.Mutex
	handlers      map[string]http.HandlerFunc
	queue         []TelegramResponse
	requests      []TelegramRequest
	nextMessageID int
}

// NewTelegramStub starts a fake Bot API server and closes it when the test
// ends. The client under test is expected to be pointed at URL() and to
// append /bot<token>/<method> itself.
func NewTelegramStub(t *testing.T) *TelegramStub {
	t.Helper()
	stub := &TelegramStub{
		t:        t,
		handlers: make(map[string]http.HandlerFunc),
	}
	stub.server = httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(stub.server.Close)
	return stub
}

// URL is the base URL of the fake Bot API server.
func (s *TelegramStub) URL() string {
	s.t.Helper()
	return s.server.URL
}

// Script appends a response to the FIFO queue served across all methods.
func (s *TelegramStub) Script(resp TelegramResponse) {
	s.mu.Lock()
	s.queue = append(s.queue, resp)
	s.mu.Unlock()
}

// Succeed queues a 200 {"ok":true,"result":{"message_id":id}} envelope. A zero
// id means "next automatic id", assigned when the response is served.
func (s *TelegramStub) Succeed(messageID int) {
	if messageID == 0 {
		s.Script(TelegramResponse{Status: http.StatusOK})
		return
	}
	s.Script(TelegramResponse{Status: http.StatusOK, Body: successBody(messageID)})
}

// Fail queues the Bot API error envelope {"ok":false,"error_code":status,
// "description":description} behind status.
func (s *TelegramStub) Fail(status int, description string) {
	s.Script(TelegramResponse{Status: status, Body: errorBody(status, description, nil)})
}

// RateLimit queues a 429 whose body carries parameters.retry_after. The delay
// travels in the JSON body only; no Retry-After header is set.
func (s *TelegramStub) RateLimit(retryAfter int) {
	s.Script(TelegramResponse{
		Status: http.StatusTooManyRequests,
		Body:   errorBody(http.StatusTooManyRequests, "Too Many Requests", &retryAfter),
	})
}

// ServerError queues a 500 Bot API error envelope.
func (s *TelegramStub) ServerError() {
	s.Fail(http.StatusInternalServerError, "Internal Server Error")
}

// Handle registers a permanent handler for a Bot API method. It takes
// precedence over the script queue, which is left untouched for other
// requests. The handler receives the original request body.
func (s *TelegramStub) Handle(method string, handler http.HandlerFunc) {
	s.mu.Lock()
	s.handlers[method] = handler
	s.mu.Unlock()
}

// Requests returns a copy of every request received so far. Document bytes
// are copied too, so mutating the result does not change the stub's log.
func (s *TelegramStub) Requests() []TelegramRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TelegramRequest, len(s.requests))
	for i, req := range s.requests {
		out[i] = req
		if req.Document != nil {
			out[i].Document = append([]byte(nil), req.Document...)
		}
	}
	return out
}

// Last returns the most recent request, or the zero value when none has been
// received. Document bytes are copied.
func (s *TelegramStub) Last() TelegramRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return TelegramRequest{}
	}
	last := s.requests[len(s.requests)-1]
	if last.Document != nil {
		last.Document = append([]byte(nil), last.Document...)
	}
	return last
}

// Reset clears queued responses, recorded requests, and the automatic
// message id counter.
func (s *TelegramStub) Reset() {
	s.mu.Lock()
	s.queue = nil
	s.requests = nil
	s.nextMessageID = 0
	s.mu.Unlock()
}

func (s *TelegramStub) serve(w http.ResponseWriter, r *http.Request) {
	req := TelegramRequest{
		HTTPMethod: r.Method,
		Path:       r.URL.Path,
		RawQuery:   r.URL.RawQuery,
	}
	req.Token, req.Method, req.Err = parseBotPath(r.URL.Path)
	if req.Err != nil {
		s.record(req)
		writeTelegramJSON(w, http.StatusNotFound, errorBody(http.StatusNotFound, "Not Found", nil))
		return
	}

	s.mu.Lock()
	handler := s.handlers[req.Method]
	s.mu.Unlock()

	if handler != nil {
		s.record(req)
		handler(w, r)
		return
	}

	if err := decodeTelegramBody(r, &req); err != nil {
		req.Err = err
		s.record(req)
		writeTelegramJSON(w, http.StatusBadRequest, errorBody(http.StatusBadRequest, "Bad Request", nil))
		return
	}

	s.mu.Lock()
	s.requests = append(s.requests, req)
	resp := TelegramResponse{Status: http.StatusOK}
	if len(s.queue) > 0 {
		resp = s.queue[0]
		s.queue = s.queue[1:]
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	if resp.Body == "" {
		s.nextMessageID++
		resp.Body = successBody(s.nextMessageID)
	}
	s.mu.Unlock()

	writeTelegramJSON(w, resp.Status, resp.Body)
}

func (s *TelegramStub) record(req TelegramRequest) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
}

// parseBotPath splits /bot<token>/<method> into its token and method.
func parseBotPath(path string) (token, method string, err error) {
	shape := fmt.Errorf("telegram stub: path %q does not match /bot<token>/<method>", path)
	if !strings.HasPrefix(path, "/") {
		return "", "", shape
	}
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "bot") {
		return "", "", shape
	}
	token = strings.TrimPrefix(parts[0], "bot")
	method = parts[1]
	if token == "" || method == "" {
		return "", "", shape
	}
	return token, method, nil
}

// decodeTelegramBody captures the multipart fields the Bot API client is
// expected to send. Only multipart bodies are decoded; anything else leaves
// the request untouched and is reported as missing fields for sendDocument.
func decodeTelegramBody(r *http.Request, req *TelegramRequest) error {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		return validateTelegramFields(req)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("telegram stub: parse Content-Type %q: %w", contentType, err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return validateTelegramFields(req)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return errors.New("telegram stub: multipart Content-Type has no boundary")
	}

	var haveChatID, haveCaption, haveDocument bool
	reader := multipart.NewReader(r.Body, boundary)
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			return fmt.Errorf("telegram stub: read multipart body: %w", partErr)
		}
		switch part.FormName() {
		case "chat_id":
			data, readErr := io.ReadAll(part)
			if readErr != nil {
				return fmt.Errorf("telegram stub: read chat_id: %w", readErr)
			}
			req.ChatID = string(data)
			haveChatID = true
		case "caption":
			data, readErr := io.ReadAll(part)
			if readErr != nil {
				return fmt.Errorf("telegram stub: read caption: %w", readErr)
			}
			req.Caption = string(data)
			haveCaption = true
		case "document":
			data, readErr := io.ReadAll(part)
			if readErr != nil {
				return fmt.Errorf("telegram stub: read document: %w", readErr)
			}
			req.Document = data
			req.DocumentName = part.FileName()
			req.DocumentType = part.Header.Get("Content-Type")
			haveDocument = true
		default:
			if _, copyErr := io.Copy(io.Discard, part); copyErr != nil {
				return fmt.Errorf("telegram stub: discard form field: %w", copyErr)
			}
		}
	}
	if req.Method != "sendDocument" {
		return nil
	}
	var missing []string
	if !haveChatID {
		missing = append(missing, "chat_id")
	}
	if !haveCaption {
		missing = append(missing, "caption")
	}
	if !haveDocument {
		missing = append(missing, "document")
	}
	if len(missing) > 0 {
		return fmt.Errorf("telegram stub: sendDocument is missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func validateTelegramFields(req *TelegramRequest) error {
	if req.Method != "sendDocument" {
		return nil
	}
	return errors.New("telegram stub: sendDocument requires a multipart body with chat_id, caption and document")
}

func successBody(messageID int) string {
	envelope := struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}{OK: true}
	envelope.Result.MessageID = messageID
	return marshalTelegramJSON(envelope)
}

func errorBody(status int, description string, retryAfter *int) string {
	envelope := struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
		Parameters  *struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters,omitempty"`
	}{
		OK:          false,
		ErrorCode:   status,
		Description: description,
	}
	if retryAfter != nil {
		envelope.Parameters = &struct {
			RetryAfter int `json:"retry_after"`
		}{RetryAfter: *retryAfter}
	}
	return marshalTelegramJSON(envelope)
}

func marshalTelegramJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error_code":500,"description":"Internal Server Error"}`
	}
	return string(data)
}

func writeTelegramJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}
