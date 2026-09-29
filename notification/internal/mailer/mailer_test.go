package mailer

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSMTP is a minimal SMTP server: it answers each command with the reply configured
// for its verb (default 250), records the message, and hangs up instead of answering
// QUIT when dropOnQuit is set.
type fakeSMTP struct {
	replies    map[string]string
	dropOnQuit bool
	messages   chan string
}

func startFakeSMTP(t *testing.T, f *fakeSMTP) *Mailer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	f.messages = make(chan string, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	return &Mailer{host: host, port: port, from: "calendar@example.com"}
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(verb, dflt string) { conn.Write([]byte(f.reply(verb, dflt) + "\r\n")) }
	reply("GREETING", "220 fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		switch verb {
		case "EHLO", "HELO":
			conn.Write([]byte("250-fake\r\n250 8BITMIME\r\n"))
		case "DATA":
			reply("DATA", "354 go ahead")
			var msg strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				msg.WriteString(l)
			}
			f.messages <- msg.String()
			reply("END", "250 queued")
		case "QUIT":
			if f.dropOnQuit {
				return
			}
			reply("QUIT", "221 bye")
			return
		default:
			reply(verb, "250 ok")
		}
	}
}

func (f *fakeSMTP) reply(verb, dflt string) string {
	if r, ok := f.replies[verb]; ok {
		return r
	}
	return dflt
}

func TestSend_HeadersCantBeInjected(t *testing.T) {
	f := &fakeSMTP{}
	m := startFakeSMTP(t, f)
	require.NoError(t, m.SendInvitation("guest@example.com", "Lunch\r\nBcc: attacker@evil.test\r\n\r\nfake body",
		"Room 1\r.\r\nMAIL FROM:<x@evil.test>", time.Now(), "http://a", "http://d"))
	msg := <-f.messages
	headers, body, _ := strings.Cut(msg, "\r\n\r\n")
	assert.NotContains(t, headers, "\r\nBcc:", "no injected header")
	assert.Contains(t, headers, "Subject: Invitation: Lunch Bcc: attacker@evil.test fake body\r\n")
	assert.NotContains(t, body, "\r.\r\n", "a bare CR can't escape dot-stuffing")
	assert.Contains(t, body, "Where: Room 1 . MAIL FROM:<x@evil.test>")
}

func TestSend_LongNonASCIISubjectIsFolded(t *testing.T) {
	f := &fakeSMTP{}
	m := startFakeSMTP(t, f)
	require.NoError(t, m.SendReminder("guest@example.com", strings.Repeat("会議", 150), time.Now()))
	msg := <-f.messages
	for _, line := range strings.Split(msg, "\r\n") {
		assert.LessOrEqual(t, len(line), 998, "SMTP line length limit")
	}
	assert.Contains(t, msg, "?=\r\n =?utf-8?q?", "encoded-words are folded onto continuation lines")
}

func TestSend_ClassifiesFailures(t *testing.T) {
	// The relay rejects this recipient: that message's problem, and permanent.
	m := startFakeSMTP(t, &fakeSMTP{replies: map[string]string{"RCPT": "550 5.1.1 no such user"}})
	err := m.SendReminder("nobody@example.com", "Sync", time.Now())
	require.Error(t, err)
	assert.True(t, Permanent(err))
	assert.False(t, Systemic(err))

	// A temporary refusal of the recipient (greylisting): retry later.
	m = startFakeSMTP(t, &fakeSMTP{replies: map[string]string{"RCPT": "450 4.2.0 try later"}})
	err = m.SendReminder("guest@example.com", "Sync", time.Now())
	require.Error(t, err)
	assert.False(t, Permanent(err))
	assert.False(t, Systemic(err))

	// The relay refuses the sender (it wants AUTH): every message would fail — not this
	// recipient's fault, so never permanent for it.
	m = startFakeSMTP(t, &fakeSMTP{replies: map[string]string{"MAIL": "530 5.7.0 authentication required"}})
	err = m.SendReminder("guest@example.com", "Sync", time.Now())
	require.Error(t, err)
	assert.True(t, Systemic(err))
	assert.False(t, Permanent(err))

	// Nothing listening.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	host, port, _ := net.SplitHostPort(addr)
	err = (&Mailer{host: host, port: port, from: "calendar@example.com"}).SendReminder("guest@example.com", "Sync", time.Now())
	require.Error(t, err)
	assert.True(t, Systemic(err))
}

func TestSend_AcceptedMessageIsSentEvenIfQuitFails(t *testing.T) {
	f := &fakeSMTP{dropOnQuit: true}
	m := startFakeSMTP(t, f)
	assert.NoError(t, m.SendReminder("guest@example.com", "Sync", time.Now()),
		"the relay accepted it; a lost QUIT reply mustn't get it sent again")
	assert.Len(t, f.messages, 1)
}
