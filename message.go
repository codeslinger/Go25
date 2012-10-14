// vim:set ts=2 sw=2 et ai ft=go:
package main

import "net"

// --- SMTP message submission ----------------------------------------------

// Represents a single SMTP message submission.
type SMTPMessage struct {
  Remote *net.TCPAddr
  From   string
  To     []string
  Body   string
}

// Create a new record for an SMTP message submission.
func NewSMTPMessage() *SMTPMessage {
  return &SMTPMessage{
    Remote: nil,
    From:   "",
    To:     make([]string, 0),
    Body:   "",
  }
}

// Add a recipient to this SMTP message submission.
func (m *SMTPMessage) AddRecipient(rcpt string) {
  m.To = append(m.To, rcpt)
}

