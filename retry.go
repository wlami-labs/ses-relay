package main

import (
	"context"
	"time"
)

// retrySender retries temporary SES failures (throttling, paused sending,
// network) inside the relay. The SMTP client cannot be relied on for that:
// Listmonk's SMTP pool does not retry a 451 reply, it skips the recipient.
type retrySender struct {
	next     Sender
	attempts int
	base     time.Duration
}

// withRetry makes up to `attempts` calls, waiting base, 2*base, 4*base, ...
// between them. Permanent errors (smtpError code 5xx) are returned at once.
func withRetry(s Sender, attempts int, base time.Duration) Sender {
	return &retrySender{next: s, attempts: attempts, base: base}
}

func (r *retrySender) SendRaw(ctx context.Context, from string, to []string, raw []byte) (string, error) {
	wait := r.base
	var err error
	for i := 0; i < r.attempts; i++ {
		var id string
		if id, err = r.next.SendRaw(ctx, from, to, raw); err == nil {
			return id, nil
		}
		if smtpError(err).Code < 500 && i < r.attempts-1 {
			select {
			case <-time.After(wait):
				wait *= 2
				continue
			case <-ctx.Done():
				return "", err
			}
		}
		break
	}
	return "", err
}
