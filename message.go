// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "container/list"
  "net"
)

// --- SMTP message submission ----------------------------------------------

// Represents a single SMTP message submission.
type SMTPMessage struct {
  Remote *net.TCPAddr
  From   string
  To     *list.List
  Body   string
}

// Create a new record for an SMTP message submission.
func NewSMTPMessage(addr *net.TCPAddr) *SMTPMessage {
  return &SMTPMessage{
    Remote: addr,
    From:   "",
    To:     list.New(),
    Body:   "",
  }
}

