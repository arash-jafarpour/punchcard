package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

const testBotToken = "123456:bot-secret-token"

func TestRedactError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		secrets []string
		want    string
		absent  []string
	}{
		{
			name: "url error drops the token-bearing URL",
			err: &url.Error{
				Op:  "Post",
				URL: "https://api.telegram.org/bot" + testBotToken + "/sendDocument",
				Err: errors.New("dial tcp 127.0.0.1:443: connection refused"),
			},
			secrets: []string{testBotToken},
			want:    "dial tcp 127.0.0.1:443: connection refused",
			absent:  []string{testBotToken, "api.telegram.org"},
		},
		{
			name: "wrapped url error",
			err: fmt.Errorf("telegram: deliver: %w", &url.Error{
				Op:  "Post",
				URL: "https://api.telegram.org/bot" + testBotToken + "/sendDocument",
				Err: errors.New("timeout"),
			}),
			secrets: []string{testBotToken},
			want:    "timeout",
			absent:  []string{testBotToken, "api.telegram.org"},
		},
		{
			name:    "token in plain wrapped error",
			err:     fmt.Errorf("post /bot%s/sendDocument: %w", testBotToken, errors.New("boom")),
			secrets: []string{testBotToken},
			want:    "post /bot[redacted]/sendDocument: boom",
			absent:  []string{testBotToken},
		},
		{
			name:    "proxy password scrubbed",
			err:     &url.Error{Op: "Post", URL: "https://api.telegram.org/bot123/sendDocument", Err: errors.New("proxyconnect tcp: password=hunter2 rejected")},
			secrets: []string{testBotToken, "hunter2"},
			want:    "proxyconnect tcp: password=[redacted] rejected",
			absent:  []string{"hunter2"},
		},
		{
			name:    "both secrets scrubbed",
			err:     errors.New("bot " + testBotToken + " via hunter2 failed"),
			secrets: []string{testBotToken, "hunter2"},
			want:    "bot [redacted] via [redacted] failed",
			absent:  []string{testBotToken, "hunter2"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := redactError(test.err, test.secrets...)
			if got == nil {
				t.Fatal("redactError returned nil")
			}
			if got.Error() != test.want {
				t.Errorf("redactError() = %q, want %q", got, test.want)
			}
			for _, secret := range test.absent {
				if strings.Contains(got.Error(), secret) {
					t.Errorf("redacted error %q still contains %q", got, secret)
				}
			}
		})
	}
}

func TestRedactErrorPreservesIdentity(t *testing.T) {
	err := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + testBotToken + "/sendDocument",
		Err: context.Canceled,
	}
	got := redactError(err, testBotToken)
	if !errors.Is(got, context.Canceled) {
		t.Errorf("errors.Is(%v, context.Canceled) = false, want true", got)
	}

	sentinel := errors.New("sentinel")
	wrapped := fmt.Errorf("post /bot%s/sendDocument: %w", testBotToken, sentinel)
	if got := redactError(wrapped, testBotToken); !errors.Is(got, sentinel) {
		t.Errorf("errors.Is(%v, sentinel) = false, want true", got)
	}
}

func TestRedactErrorUntouchedWithoutSecret(t *testing.T) {
	plain := errors.New("boom")
	if got := redactError(plain, testBotToken, ""); got != plain {
		t.Errorf("redactError(plain) = %v, want the original error", got)
	}

	if got := redactError(nil, testBotToken); got != nil {
		t.Errorf("redactError(nil) = %v, want nil", got)
	}
}

func TestRedactErrorSkipsEmptySecrets(t *testing.T) {
	plain := errors.New("boom")
	if got := redactError(plain, "", ""); got.Error() != "boom" {
		t.Errorf("redactError() = %q, want %q", got, "boom")
	}
}

func TestClientRedactErrorUsesBotTokenAndProxyPassword(t *testing.T) {
	client, err := New("https://api.telegram.org", testBotToken, WithProxy("http://user:hunter2@127.0.0.1:8080"))
	if err != nil {
		t.Fatal(err)
	}
	err = fmt.Errorf("send /bot%s/sendDocument via hunter2: %w", testBotToken, errors.New("failed"))
	got := client.redactError(err)
	if strings.Contains(got.Error(), testBotToken) {
		t.Errorf("redacted error %q leaks the bot token", got)
	}
	if strings.Contains(got.Error(), "hunter2") {
		t.Errorf("redacted error %q leaks the proxy password", got)
	}
}
