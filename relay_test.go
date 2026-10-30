package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/emersion/go-smtp"
)

type fakeSender struct {
	mu    sync.Mutex
	calls int
	from  string
	to    []string
	raw   []byte
	err   error
}

func (f *fakeSender) SendRaw(_ context.Context, from string, to []string, raw []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.from, f.to, f.raw = from, to, raw
	if f.err != nil {
		return "", f.err
	}
	return "msg-1", nil
}

// startRelay runs NewServer on a random local port and returns its address.
func startRelay(t *testing.T, s Sender, maxBytes int64) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(l.Addr().String(), s, maxBytes)
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().String()
}

const message = "From: tawadi <news@tawadi.de>\r\n" +
	"To: anna@example.com\r\n" +
	"Subject: Tidyroom is live\r\n" +
	"List-Unsubscribe: <https://lists.tawadi.de/subscription/c/s>\r\n" +
	"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n" +
	"\r\n" +
	"Hello\r\n"

func send(t *testing.T, addr string, to []string, body []byte) error {
	t.Helper()
	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.SendMail("bounce@mail.tawadi.de", to, bytes.NewReader(body))
}

func smtpCode(t *testing.T, err error) int {
	t.Helper()
	var se *smtp.SMTPError
	if !errors.As(err, &se) {
		t.Fatalf("want *smtp.SMTPError, got %T: %v", err, err)
	}
	return se.Code
}

func TestRelayForwardsRawMessageAndEnvelope(t *testing.T) {
	f := &fakeSender{}
	addr := startRelay(t, f, 10<<20)

	to := []string{"anna@example.com", "ben@example.com"}
	if err := send(t, addr, to, []byte(message)); err != nil {
		t.Fatalf("send: %v", err)
	}

	if f.calls != 1 {
		t.Fatalf("SendRaw calls = %d, want 1", f.calls)
	}
	if f.from != "bounce@mail.tawadi.de" {
		t.Errorf("from = %q", f.from)
	}
	if strings.Join(f.to, ",") != strings.Join(to, ",") {
		t.Errorf("to = %v, want %v", f.to, to)
	}
	if string(f.raw) != message {
		t.Errorf("raw message changed:\n%q\nwant\n%q", f.raw, message)
	}
}

func TestThrottleIsTemporary(t *testing.T) {
	addr := startRelay(t, &fakeSender{err: &types.TooManyRequestsException{}}, 10<<20)
	err := send(t, addr, []string{"anna@example.com"}, []byte(message))
	if code := smtpCode(t, err); code != 451 {
		t.Fatalf("code = %d, want 451", code)
	}
}

func TestRejectIsPermanent(t *testing.T) {
	addr := startRelay(t, &fakeSender{err: &types.MessageRejected{}}, 10<<20)
	err := send(t, addr, []string{"anna@example.com"}, []byte(message))
	if code := smtpCode(t, err); code != 554 {
		t.Fatalf("code = %d, want 554", code)
	}
}

func TestOversizeRejected(t *testing.T) {
	f := &fakeSender{}
	addr := startRelay(t, f, 10<<20)
	big := []byte(message + strings.Repeat("x", 10<<20+1) + "\r\n")
	// go-smtp answers 552 and drops the connection while the client may still
	// be writing, so the client sees either the 552 or a reset.
	err := send(t, addr, []string{"anna@example.com"}, big)
	var se *smtp.SMTPError
	var ne *net.OpError
	switch {
	case errors.As(err, &se):
		if se.Code != 552 {
			t.Fatalf("code = %d, want 552", se.Code)
		}
	case errors.As(err, &ne):
	default:
		t.Fatalf("oversize message accepted or unexpected error: %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("SendRaw called %d times for an oversize message", f.calls)
	}
}
