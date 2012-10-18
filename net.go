// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "fmt"
  "github.com/codeslinger/log"
  "net"
)

type TCPService interface {
  SetClientOptions(*net.TCPConn) error
  Handle(*net.TCPConn)
  Addr() *net.TCPAddr
  Shutdown()
}

func RunTCP(t TCPService) {
  l, err := net.ListenTCP("tcp", t.Addr())
  if err != nil {
    log.Error("failed to bind to local address %s", t.Addr())
    t.Shutdown()
    return
  }
  defer l.Close()

  log.Info("listening for connections on %s", t.Addr())
  for {
    conn, err := l.AcceptTCP()
    if err != nil {
      log.Error("failed to accept connection: %v", err)
      continue
    }
    if err := t.SetClientOptions(conn); err != nil {
      conn.Close()
      continue
    }
    log.Trace(func() string {
      return fmt.Sprintf("%s: client connected to %s", conn.RemoteAddr(), t.Addr())
    })
    go t.Handle(conn)
  }
}

