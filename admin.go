// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "fmt"
  "github.com/codeslinger/log"
  "net"
)

// --- Admin Service --------------------------------------------------------

type AdminService struct {
  addr     string
  exited   chan int
  draining bool
}

// Create a new admin service instance bound to the given TCP address.
func NewAdminService(addr string) *AdminService {
  return &AdminService{
    addr:     addr,
    exited:   make(chan int, 1),
    draining: false,
  }
}

// Returns TCP address on which this server is listening.
func (a *AdminService) Addr() string {
  return a.addr
}

// Returns channel indicating when this server has exited.
func (a *AdminService) Exited() chan int {
  return a.exited
}

// Shut down this admin server.
func (a *AdminService) Shutdown() {
  a.draining = true
  a.exited <- 1
}

// Process an incoming admin service connection.
func (a *AdminService) Handle(conn *net.TCPConn) {
  defer func() {
    log.Trace(func() string {
      return fmt.Sprintf("%s: client disconnected", conn.RemoteAddr())
    })
    conn.Close()
  }()
  if a.draining {
    return
  }
}

// Set TCP socket options on a new admin service connection.
func (a *AdminService) SetClientOptions(conn *net.TCPConn) error {
  if err := conn.SetKeepAlive(true); err != nil {
    log.Error("%s: SetKeepAlive: %v", conn.RemoteAddr(), err)
    return err
  }
  if err := conn.SetLinger(1); err != nil {
    log.Error("%s: SetLinger: %v", conn.RemoteAddr(), err)
    return err
  }
  return nil
}

