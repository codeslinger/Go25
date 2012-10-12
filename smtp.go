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
  ServerIdent   string
  ServingDomain string
  addr          string
  exited        chan int
  draining      bool
}

// Create a new SMTP server instance bound to the given TCP address.
func NewSMTPService(addr, domain string) *SMTPService {
  return &SMTPService{
    ServerIdent:    DefaultIdent,
    ServingDomain:  domain,
    addr:           addr,
    exited:         make(chan int, 1),
    draining:       false,
  }
}

// Returns TCP address on which this server is listening.
func (s *SMTPService) Addr() string {
  return s.addr
}

// Returns channel indicating when this server has exited.
func (s *SMTPService) Exited() chan int {
  return s.exited
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

  session := NewSMTPSession(conn,
                            conn.RemoteAddr().(*net.TCPAddr),
                            &s.ServerIdent,
                            &s.ServingDomain)
  err := session.Greet(s.draining)
  if err != nil {
    log.Error("%s: failed to send greeting banner: %v", conn.RemoteAddr(), err)
    return
  }
  for {
    err = session.Process()
    if err != nil {
      if err != SessionClosedByClient {
        log.Error("%s: failed to read command: %v", conn.RemoteAddr(), err)
      }
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

