package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
	"github.com/emersion/go-smtp"
)

// Sender delivers one raw MIME message to the given envelope recipients.
type Sender interface {
	SendRaw(ctx context.Context, from string, to []string, raw []byte) (string, error)
}

// NewServer returns an SMTP server that hands every accepted message to s.
// It has no authentication: it must only listen on loopback inside the pod.
func NewServer(addr string, s Sender, maxBytes int64) *smtp.Server {
	srv := smtp.NewServer(&backend{sender: s})
	srv.Addr = addr
	srv.Domain = "ses-relay"
	srv.MaxMessageBytes = maxBytes
	srv.MaxRecipients = 50
	srv.ReadTimeout = 60 * time.Second
	srv.WriteTimeout = 60 * time.Second
	return srv
}

type backend struct{ sender Sender }

func (b *backend) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &session{sender: b.sender}, nil
}

type session struct {
	sender Sender
	from   string
	to     []string
}

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return err // ErrDataTooLarge is answered with 552 by go-smtp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := s.sender.SendRaw(ctx, s.from, s.to, buf.Bytes())
	if err != nil {
		slog.Warn("send failed", "recipients", len(s.to), "err", err)
		return smtpError(err)
	}
	slog.Info("sent", "message_id", id, "recipients", len(s.to))
	return nil
}

func (s *session) Reset() {
	s.from, s.to = "", nil
}

func (s *session) Logout() error { return nil }

// smtpError maps an SES error to an SMTP reply: throttling, paused sending and
// anything that is not an SES API error (network, credentials refresh) are
// temporary, so the client retries; SES rejecting the message is permanent.
func smtpError(err error) *smtp.SMTPError {
	var (
		throttled *types.TooManyRequestsException
		limit     *types.LimitExceededException
		paused    *types.SendingPausedException
		apiErr    smithy.APIError
	)
	temporary := &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "temporary SES failure, try again later"}
	switch {
	case errors.As(err, &throttled), errors.As(err, &limit), errors.As(err, &paused):
		return temporary
	case errors.As(err, &apiErr):
		return &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 6, 0}, Message: "rejected by SES: " + apiErr.ErrorCode()}
	default:
		return temporary
	}
}
