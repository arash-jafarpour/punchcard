package telegram

import (
	"errors"
	"net/url"
	"strings"
)

// redactedValue replaces every secret found in an error message.
const redactedValue = "[redacted]"

// redactedError keeps the original cause reachable for errors.Is/errors.As
// while presenting a message with secrets removed.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.cause }

// redactError removes the bot token and any non-empty proxy password from err.
// It is nil-safe and unwraps *url.Error to its inner error first, because the
// request URL embeds the token and must never reach a log. Unwrapping before
// scrubbing keeps errors.Is(err, context.Canceled) and friends working.
func (c *Client) redactError(err error) error {
	return redactError(err, c.token, c.proxyPassword)
}

// redactError scrubs secrets from err. Empty secrets are skipped: passing them
// to strings.ReplaceAll would corrupt the message.
func redactError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}

	cause := err
	var urlErr *url.Error
	if errors.As(cause, &urlErr) {
		cause = urlErr.Err
	}

	msg := cause.Error()
	redacted := msg
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		redacted = strings.ReplaceAll(redacted, secret, redactedValue)
	}
	if redacted == msg {
		return cause
	}
	return &redactedError{msg: redacted, cause: cause}
}
