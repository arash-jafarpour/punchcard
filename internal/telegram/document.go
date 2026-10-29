package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// maxDocumentBytes is the Bot API cap for an uploaded document (50 MB).
	maxDocumentBytes = 50 << 20
	// maxCaptionBytes is the Bot API cap for a media caption (1024 bytes).
	maxCaptionBytes = 1024
	// maxResponseBytes bounds how much of a Bot API reply is read.
	maxResponseBytes = 1 << 20
	// documentMIME marks the report as Markdown for the recipient.
	documentMIME = "text/markdown; charset=utf-8"
	// sendDocumentMethod is the Bot API method this file implements.
	sendDocumentMethod = "sendDocument"
)

// Result is the interesting part of a successful Bot API reply.
type Result struct {
	MessageID int64
}

// APIError is a Bot API failure: a non-2xx status, or a 2xx carrying
// {"ok":false}. It never carries the request URL, which embeds the bot token.
type APIError struct {
	StatusCode  int
	ErrorCode   int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	code := e.ErrorCode
	if code == 0 {
		code = e.StatusCode
	}
	description := strings.TrimSpace(e.Description)
	if description == "" {
		description = http.StatusText(e.StatusCode)
	}
	if description == "" {
		description = http.StatusText(code)
	}
	msg := fmt.Sprintf("telegram: %s: %d", sendDocumentMethod, code)
	if description != "" {
		msg += " " + description
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	return msg
}

// IsStatus reports whether err is an APIError with the given HTTP status code.
func IsStatus(err error, code int) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == code
	}
	return false
}

// sendOutcome is one sendDocument attempt, classified for the retry loop.
type sendOutcome struct {
	result    *Result
	err       error
	retryable bool
}

// apiEnvelope is the reply shape shared by success and failure. Result stays
// raw so a failure body is never forced through the success shape.
type apiEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// SendDocument uploads the file at path to chatID with sendDocument and a
// plain-text caption. The file is stat-ed once before any socket work, so a
// rejected or failed send never touches it, and each attempt streams a fresh
// multipart body from a newly opened file.
//
// 429, 5xx and transport failures are retried up to c.maxAttempts total
// attempts (3 when unset) with 1s then 2s backoff; a 429's
// parameters.retry_after overrides the delay. Every other 4xx fails at once.
// Returned errors never carry the bot token.
func (c *Client) SendDocument(ctx context.Context, chatID, path, caption string) (*Result, error) {
	if strings.TrimSpace(chatID) == "" {
		return nil, c.redactError(errors.New("telegram: chat id is empty"))
	}
	if path == "" {
		return nil, c.redactError(errors.New("telegram: document path is empty"))
	}
	if len(caption) > maxCaptionBytes {
		return nil, c.redactError(fmt.Errorf("telegram: caption is %d bytes, limit %d", len(caption), maxCaptionBytes))
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, c.redactError(fmt.Errorf("telegram: stat document: %w", err))
	}
	if !info.Mode().IsRegular() {
		return nil, c.redactError(errors.New("telegram: document is not a regular file"))
	}
	if info.Size() > maxDocumentBytes {
		return nil, c.redactError(fmt.Errorf("telegram: document is %d bytes, limit %d", info.Size(), maxDocumentBytes))
	}

	attempts := c.maxAttempts
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, c.redactError(err)
		}

		target := *c.base
		target.Path = c.base.Path + "/bot" + c.token + "/" + sendDocumentMethod
		target.RawQuery = ""
		target.Fragment = ""

		outcome := c.sendDocumentAttempt(ctx, &target, chatID, path, caption)
		if outcome.err == nil {
			return outcome.result, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, c.redactError(err)
		}
		lastErr = outcome.err
		if !outcome.retryable || attempt == attempts-1 {
			break
		}

		delay := retryBackoff(attempt)
		var apiErr *APIError
		if errors.As(outcome.err, &apiErr) && apiErr.RetryAfter > 0 {
			delay = apiErr.RetryAfter
		}
		if !c.waitForRetry(ctx, delay) {
			return nil, c.redactError(ctx.Err())
		}
	}
	return nil, c.redactError(lastErr)
}

// sendDocumentAttempt performs one POST and classifies its outcome.
func (c *Client) sendDocumentAttempt(ctx context.Context, target *url.URL, chatID, path, caption string) sendOutcome {
	file, err := os.Open(path)
	if err != nil {
		return sendOutcome{err: fmt.Errorf("telegram: open document: %w", err)}
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	// The writer lives in its own goroutine because the transport reads the
	// body lazily. It owns closing both mw and the file on every path.
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		defer file.Close()
		writeErr := writeDocumentForm(mw, file, chatID, caption, filepath.Base(path))
		if closeErr := mw.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = pw.CloseWithError(writeErr)
			return
		}
		_ = pw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-writeDone
		return sendOutcome{err: fmt.Errorf("telegram: build request: %w", err)}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-writeDone
		if ctxErr := ctx.Err(); ctxErr != nil {
			return sendOutcome{err: ctxErr}
		}
		// Unwrap *url.Error so the formatted error never carries the URL,
		// which embeds the bot token.
		return sendOutcome{
			err:       fmt.Errorf("telegram: send document: %w", unwrapURLError(err)),
			retryable: true,
		}
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	_ = resp.Body.Close()
	// Release the writer if the server answered without consuming the body.
	_ = pr.Close()
	<-writeDone

	if readErr != nil {
		return sendOutcome{
			err:       fmt.Errorf("telegram: read response: %w", readErr),
			retryable: true,
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return sendOutcome{err: ctxErr}
	}
	return classifyResponse(resp.StatusCode, body)
}

// classifyResponse maps a status and body onto a Result or an APIError.
func classifyResponse(status int, body []byte) sendOutcome {
	var envelope apiEnvelope
	decodeErr := json.Unmarshal(body, &envelope)
	successStatus := status >= 200 && status < 300

	switch {
	case successStatus && decodeErr != nil:
		return sendOutcome{err: fmt.Errorf("telegram: %s: decode response: %w", sendDocumentMethod, decodeErr)}
	case successStatus && envelope.OK:
		if len(envelope.Result) == 0 {
			return sendOutcome{err: fmt.Errorf("telegram: %s: response has no result", sendDocumentMethod)}
		}
		var payload struct {
			MessageID int64 `json:"message_id"`
		}
		if err := json.Unmarshal(envelope.Result, &payload); err != nil {
			return sendOutcome{err: fmt.Errorf("telegram: %s: decode result: %w", sendDocumentMethod, err)}
		}
		return sendOutcome{result: &Result{MessageID: payload.MessageID}}
	}

	apiErr := &APIError{StatusCode: status, ErrorCode: status}
	if decodeErr == nil {
		if envelope.ErrorCode != 0 {
			apiErr.ErrorCode = envelope.ErrorCode
		}
		apiErr.Description = envelope.Description
		if envelope.Parameters != nil && envelope.Parameters.RetryAfter > 0 {
			apiErr.RetryAfter = time.Duration(envelope.Parameters.RetryAfter) * time.Second
		}
	}
	return sendOutcome{
		err:       apiErr,
		retryable: status == http.StatusTooManyRequests || status >= 500,
	}
}

// writeDocumentForm writes the form fields and the document part into mw. The
// caption is always written, even when empty, so the form shape never depends
// on the report.
func writeDocumentForm(mw *multipart.Writer, file io.Reader, chatID, caption, filename string) error {
	if err := mw.WriteField("chat_id", chatID); err != nil {
		return err
	}
	if err := mw.WriteField("caption", caption); err != nil {
		return err
	}

	// CreateFormFile would force application/octet-stream, so build the part
	// by hand. mime.FormatMediaType quotes the filename when it needs to.
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     "document",
		"filename": filename,
	}))
	header.Set("Content-Type", documentMIME)
	part, err := mw.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	return err
}

// unwrapURLError strips the *url.Error wrapper, whose message embeds the
// token-bearing request URL.
func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// retryBackoff is the delay after a retryable failure: 1s, then 2s, then 4s…
func retryBackoff(attempt int) time.Duration {
	if attempt > 3 {
		attempt = 3
	}
	return time.Second << attempt
}

// waitForRetry sleeps for d and reports whether the context is still live.
// Without an injected sleeper the wait is interruptible by ctx.Done().
func (c *Client) waitForRetry(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	if c.sleep != nil {
		c.sleep(d)
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
