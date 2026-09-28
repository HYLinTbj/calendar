package mailer

import (
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"time"
)

// Bounds on one SMTP conversation, so a relay that stops answering can't stall the
// worker (which sends everything from one goroutine) indefinitely.
const (
	dialTimeout    = 10 * time.Second
	sessionTimeout = 60 * time.Second
)

// maxSubjectRunes caps the subject; titles have no length limit of their own.
const maxSubjectRunes = 200

// RecipientError is a failure specific to one message and its recipient: the relay
// refused the recipient (RCPT) or the message itself (after DATA). Anything else — no
// connection, a refused greeting or sender — is the relay's problem and would fail
// every message alike.
type RecipientError struct{ Err error }

func (e *RecipientError) Error() string { return e.Err.Error() }
func (e *RecipientError) Unwrap() error { return e.Err }

type Mailer struct {
	host string
	port string
	from string
}

func New() *Mailer {
	return &Mailer{
		host: getEnv("SMTP_HOST", "localhost"),
		port: getEnv("SMTP_PORT", "1025"),
		from: getEnv("SMTP_FROM", "calendar@example.com"),
	}
}

func (m *Mailer) SendInvitation(to, title, location string, startTime time.Time, acceptURL, declineURL string) error {
	body := fmt.Sprintf(
		"You are invited to \"%s\"\r\nWhen: %s\r\nWhere: %s\r\n\r\nAccept:  %s\r\nDecline: %s\r\n",
		oneLine(title), startTime.Format(time.RFC1123), oneLine(location), acceptURL, declineURL,
	)
	return m.send(to, "Invitation: "+title, body)
}

// SendReminder sends one attendee a reminder. Recipients get separate messages so one
// rejected address doesn't stop the rest.
func (m *Mailer) SendReminder(to, title string, startTime time.Time) error {
	body := fmt.Sprintf("Your event \"%s\" starts at %s.\r\n", oneLine(title), startTime.Format(time.RFC1123))
	return m.send(to, "Reminder: "+title, body)
}

// Permanent reports whether retrying this message can't help: the relay rejected the
// recipient or message outright (5xx), or the address can't be put in an SMTP command.
func Permanent(err error) bool {
	var re *RecipientError
	if !errors.As(err, &re) {
		return false
	}
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		return tpErr.Code >= 500
	}
	return strings.Contains(err.Error(), "must not contain CR or LF")
}

// Systemic reports whether err is the relay's (unreachable, or refusing this sender)
// rather than one message's, so every other send would fail the same way.
func Systemic(err error) bool {
	var re *RecipientError
	return !errors.As(err, &re)
}

// send delivers a plain-text message to one recipient.
func (m *Mailer) send(to, subject, body string) error {
	msg := "From: " + m.from + "\r\n" +
		"To: " + oneLine(to) + "\r\n" +
		"Subject: " + encodeSubject(subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" + body

	// smtp.SendMail, but with timeouts.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(m.host, m.port), dialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(sessionTimeout)); err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.host}); err != nil {
			return err
		}
	}
	if err := c.Mail(m.from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return &RecipientError{err}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	// Close sends the terminating "." and reads the relay's verdict on this message.
	if err := w.Close(); err != nil {
		return &RecipientError{err}
	}
	// The message is accepted. A failed QUIT doesn't change that, and reporting it
	// would get the message sent again.
	_ = c.Quit()
	return nil
}

// encodeSubject makes subject a safe header value: one line, capped at maxSubjectRunes,
// RFC 2047-encoded when not plain ASCII, and folded between encoded-words so a long
// non-ASCII subject doesn't exceed SMTP's line length limit.
func encodeSubject(subject string) string {
	subject = oneLine(subject)
	if r := []rune(subject); len(r) > maxSubjectRunes {
		subject = string(r[:maxSubjectRunes-1]) + "…"
	}
	return strings.ReplaceAll(mime.QEncoding.Encode("utf-8", subject), "?= =?", "?=\r\n =?")
}

// oneLine joins the lines of user-supplied text (an event title, say) put into a
// message. A line break in a header ends it, so left in, it would let the text inject
// further headers or a body of its own; in the body, a bare CR followed by "." escapes
// SMTP's dot-stuffing (the "SMTP smuggling" trick) on relays that end data on it.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
