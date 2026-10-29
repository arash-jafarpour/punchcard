package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
)

func doTelegramRequest(t *testing.T, method, url, contentType string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, data
}

func decodeTelegramJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode response %q: %v", data, err)
	}
	return body
}

func telegramResult(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %#v", body)
	}
	return result
}

func telegramMultipart(t *testing.T, chatID, caption string, includeCaption bool, filename, contentType string, document []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("chat_id", chatID); err != nil {
		t.Fatalf("write chat_id: %v", err)
	}
	if includeCaption {
		if err := writer.WriteField("caption", caption); err != nil {
			t.Fatalf("write caption: %v", err)
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="document"; filename="%s"`, filename))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create document part: %v", err)
	}
	if _, err := part.Write(document); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, writer.FormDataContentType()
}

func TestTelegramStub_DefaultSuccessIncrementsMessageID(t *testing.T) {
	stub := NewTelegramStub(t)

	for want := 1; want <= 2; want++ {
		resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", want, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("request %d: Content-Type = %q, want application/json", want, got)
		}
		body := decodeTelegramJSON(t, data)
		if body["ok"] != true {
			t.Fatalf("request %d: ok = %v, want true", want, body["ok"])
		}
		if got := int(telegramResult(t, body)["message_id"].(float64)); got != want {
			t.Fatalf("request %d: message_id = %d, want %d", want, got, want)
		}
	}
	if got := len(stub.Requests()); got != 2 {
		t.Fatalf("recorded %d requests, want 2", got)
	}
}

func TestTelegramStub_ScriptedResponsesInOrder(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Succeed(42)
	stub.Fail(http.StatusBadRequest, "Bad Request: chat not found")

	resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", resp.StatusCode)
	}
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 42 {
		t.Fatalf("first message_id = %d, want 42", got)
	}

	resp, data = doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("second status = %d, want 400", resp.StatusCode)
	}
	body := decodeTelegramJSON(t, data)
	if body["ok"] != false {
		t.Fatalf("second ok = %v, want false", body["ok"])
	}
	if got := int(body["error_code"].(float64)); got != http.StatusBadRequest {
		t.Fatalf("second error_code = %d, want 400", got)
	}

	resp, data = doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("third status = %d, want 200", resp.StatusCode)
	}
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 1 {
		t.Fatalf("third message_id = %d, want 1 (fresh counter)", got)
	}
}

func TestTelegramStub_SucceedZeroUsesNextAutoID(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Succeed(0)

	resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 1 {
		t.Fatalf("message_id = %d, want 1", got)
	}
}

func TestTelegramStub_FailEnvelope(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Fail(http.StatusBadRequest, "Bad Request: chat not found")

	resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := decodeTelegramJSON(t, data)
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false", body["ok"])
	}
	if got := int(body["error_code"].(float64)); got != http.StatusBadRequest {
		t.Fatalf("error_code = %d, want 400", got)
	}
	if got := body["description"]; got != "Bad Request: chat not found" {
		t.Fatalf("description = %v, want the scripted text", got)
	}
}

func TestTelegramStub_RateLimitEnvelope(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.RateLimit(2)

	resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After header = %q, want none", got)
	}
	body := decodeTelegramJSON(t, data)
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false", body["ok"])
	}
	if got := int(body["error_code"].(float64)); got != http.StatusTooManyRequests {
		t.Fatalf("error_code = %d, want 429", got)
	}
	if got := body["description"]; got != "Too Many Requests" {
		t.Fatalf("description = %v, want Too Many Requests", got)
	}
	parameters, ok := body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("body has no parameters object: %#v", body)
	}
	if got := int(parameters["retry_after"].(float64)); got != 2 {
		t.Fatalf("parameters.retry_after = %d, want 2", got)
	}
}

func TestTelegramStub_ServerErrorEnvelope(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.ServerError()

	resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	body := decodeTelegramJSON(t, data)
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false", body["ok"])
	}
	if got := int(body["error_code"].(float64)); got != http.StatusInternalServerError {
		t.Fatalf("error_code = %d, want 500", got)
	}
}

func TestTelegramStub_RecordsPathAndQuery(t *testing.T) {
	stub := NewTelegramStub(t)
	if got := stub.Last(); !reflect.DeepEqual(got, TelegramRequest{}) {
		t.Fatalf("Last before any request = %#v, want zero value", got)
	}

	doTelegramRequest(t, http.MethodGet, stub.URL()+"/bot123:SECRET/sendMessage?foo=bar&baz=1", "", nil)

	last := stub.Last()
	if last.Method != "sendMessage" {
		t.Fatalf("Method = %q, want sendMessage", last.Method)
	}
	if last.HTTPMethod != http.MethodGet {
		t.Fatalf("HTTPMethod = %q, want GET", last.HTTPMethod)
	}
	if last.Token != "123:SECRET" {
		t.Fatalf("Token = %q, want 123:SECRET", last.Token)
	}
	if last.Path != "/bot123:SECRET/sendMessage" {
		t.Fatalf("Path = %q", last.Path)
	}
	if last.RawQuery != "foo=bar&baz=1" {
		t.Fatalf("RawQuery = %q", last.RawQuery)
	}
	if last.Err != nil {
		t.Fatalf("Err = %v, want nil", last.Err)
	}
}

func TestTelegramStub_RecordsMultipartDocument(t *testing.T) {
	stub := NewTelegramStub(t)
	payload := []byte("# 2026-10-27\n\n- 3 commits\n")
	caption := "GitLab daily report 2026-10-27 · 3 projects"
	body, contentType := telegramMultipart(t, "42", caption, true, "2026-10-27.md", "text/markdown", payload)

	resp, _ := doTelegramRequest(t, http.MethodPost, stub.URL()+"/bot123:ABC/sendDocument", contentType, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	last := stub.Last()
	if last.Err != nil {
		t.Fatalf("Err = %v, want nil", last.Err)
	}
	if last.Token != "123:ABC" || last.Method != "sendDocument" {
		t.Fatalf("token/method = %q/%q", last.Token, last.Method)
	}
	if last.ChatID != "42" {
		t.Fatalf("ChatID = %q, want 42", last.ChatID)
	}
	if last.Caption != caption {
		t.Fatalf("Caption = %q, want %q", last.Caption, caption)
	}
	if last.DocumentName != "2026-10-27.md" {
		t.Fatalf("DocumentName = %q, want 2026-10-27.md", last.DocumentName)
	}
	if last.DocumentType != "text/markdown" {
		t.Fatalf("DocumentType = %q, want text/markdown", last.DocumentType)
	}
	if !bytes.Equal(last.Document, payload) {
		t.Fatalf("Document = %q, want %q", last.Document, payload)
	}

	logged := stub.Requests()
	if len(logged) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(logged))
	}
	logged[0].Caption = "mutated"
	logged[0].ChatID = "mutated"
	logged[0].Document[0] = 'X'

	after := stub.Last()
	if after.Caption != caption || after.ChatID != "42" {
		t.Fatalf("Requests() aliased stub state: caption=%q chat=%q", after.Caption, after.ChatID)
	}
	if !bytes.Equal(after.Document, payload) {
		t.Fatalf("Requests() aliased document bytes: %q", after.Document)
	}
}

func TestTelegramStub_UnknownPathKeepsQueue(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Succeed(7)

	resp, data := doTelegramRequest(t, http.MethodGet, stub.URL()+"/wrong/path", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := decodeTelegramJSON(t, data)
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false", body["ok"])
	}
	if got := int(body["error_code"].(float64)); got != http.StatusNotFound {
		t.Fatalf("error_code = %d, want 404", got)
	}
	quick := stub.Last()
	if quick.Err == nil {
		t.Fatal("Err = nil, want a path-shape diagnostic")
	}
	if !strings.Contains(quick.Err.Error(), "/bot<token>/<method>") {
		t.Fatalf("Err = %q, want the expected path shape", quick.Err)
	}

	_, data = doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 7 {
		t.Fatalf("message_id = %d, want the untouched scripted 7", got)
	}
}

func TestTelegramStub_MissingFieldKeepsQueue(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Succeed(7)

	body, contentType := telegramMultipart(t, "42", "ignored", false, "2026-10-27.md", "text/markdown", []byte("report"))
	resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendDocument", contentType, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	result := decodeTelegramJSON(t, data)
	if got := int(result["error_code"].(float64)); got != http.StatusBadRequest {
		t.Fatalf("error_code = %d, want 400", got)
	}
	rejected := stub.Last()
	if rejected.Err == nil {
		t.Fatal("Err = nil, want a missing-field diagnostic")
	}
	if !strings.Contains(rejected.Err.Error(), "caption") {
		t.Fatalf("Err = %q, want it to name caption", rejected.Err)
	}

	_, data = doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 7 {
		t.Fatalf("message_id = %d, want the untouched scripted 7", got)
	}
}

func TestTelegramStub_MalformedMultipartKeepsQueue(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Succeed(7)

	resp, _ := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendDocument", "multipart/form-data", strings.NewReader("not really multipart"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := stub.Last().Err; got == nil {
		t.Fatal("Err = nil, want a boundary diagnostic")
	}

	_, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 7 {
		t.Fatalf("message_id = %d, want the untouched scripted 7", got)
	}
}

func TestTelegramStub_HandleBypassesQueue(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Succeed(7)
	stub.Handle("sendDocument", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":99}}`)
	})

	body, contentType := telegramMultipart(t, "42", "caption", true, "2026-10-27.md", "text/markdown", []byte("report"))
	resp, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendDocument", contentType, body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want the override's 202", resp.StatusCode)
	}
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 99 {
		t.Fatalf("message_id = %d, want 99", got)
	}
	if got := len(stub.Requests()); got != 1 {
		t.Fatalf("recorded %d requests, want 1", got)
	}

	_, data = doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 7 {
		t.Fatalf("message_id = %d, want the untouched scripted 7", got)
	}
}

func TestTelegramStub_Reset(t *testing.T) {
	stub := NewTelegramStub(t)
	stub.Succeed(7)
	doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)

	stub.Reset()

	if got := len(stub.Requests()); got != 0 {
		t.Fatalf("recorded %d requests after Reset, want 0", got)
	}
	if got := stub.Last(); !reflect.DeepEqual(got, TelegramRequest{}) {
		t.Fatalf("Last after Reset = %#v, want zero value", got)
	}

	_, data := doTelegramRequest(t, http.MethodPost, stub.URL()+"/botTOKEN/sendMessage", "application/json", nil)
	if got := int(telegramResult(t, decodeTelegramJSON(t, data))["message_id"].(float64)); got != 1 {
		t.Fatalf("message_id after Reset = %d, want 1", got)
	}
}
