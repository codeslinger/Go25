// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "fmt"
  "github.com/codeslinger/log"
  "net"
)

// --- SMTP Service ---------------------------------------------------------

var DefaultIdent = "ESMTP Go25"

type SMTPService struct {
  cfg           Config
  addr          *net.TCPAddr
  exited        chan int
  draining      bool
}

type Verdict int

const (
  Continue = iota
  Terminate
)

// Create a new SMTP server instance bound to the given TCP address.
func NewSMTPService(c Config, exited chan int) *SMTPService {
  return &SMTPService{
    cfg:      c,
    addr:     c.ListenLocal(),
    exited:   exited,
    draining: false,
  }
}

// Returns TCP address on which this server is listening.
func (s *SMTPService) Addr() *net.TCPAddr {
  return s.addr
}

// Shut down this SMTP server.
func (s *SMTPService) Shutdown() {
  s.draining = true
  s.exited <- 1
}

// Process an incoming SMTP connection.
func (s *SMTPService) Handle(conn *net.TCPConn) {
  defer func() {
    log.Trace(func() string {
      return fmt.Sprintf("%s: client disconnected", conn.RemoteAddr())
    })
    conn.Close()
  }()

  // Send a 421 error response if the server is in the process of shutting
  // down when the client connects.
  if s.draining {
    conn.Write(ResponseMap[421])
    return
  }
  session := NewSMTPSession(conn, s.cfg)
  if verdict := session.Greet(); verdict == Terminate {
    return
  }
  for {
    if verdict := session.Process(); verdict == Terminate {
      return
    }
  }
}

// Set TCP socket options on a new SMTP connection.
func (s *SMTPService) SetClientOptions(conn *net.TCPConn) error {
  if err := conn.SetKeepAlive(false); err != nil {
    log.Error("%s: SetKeepAlive: %v", conn.RemoteAddr(), err)
    return err
  }
  if err := conn.SetLinger(-1); err != nil {
    log.Error("%s: SetLinger: %v", conn.RemoteAddr(), err)
    return err
  }
  return nil
}

