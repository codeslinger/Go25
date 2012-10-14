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
  Addr() string
  Shutdown()
}

func RunTCP(t TCPService) {
  localAddr, err := net.ResolveTCPAddr("tcp", t.Addr())
  if err != nil {
    log.Error("could not resolve bind address: %s", t.Addr())
    return
  }
  l, err := net.ListenTCP("tcp", localAddr)
  if err != nil {
    log.Error("failed to bind to local address %s", localAddr)
    return
  }
  defer l.Close()

  log.Info("listening for connections on %s", localAddr)
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
      return fmt.Sprintf("%s: client connected to %s", conn.RemoteAddr(), localAddr)
    })
    go t.Handle(conn)
  }
}

