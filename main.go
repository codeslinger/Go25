// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "flag"
  "github.com/codeslinger/log"
  "os"
  "runtime"
)

var smtpAddr, adminAddr *string
var bannerDomain string

func init() {
  smtpAddr = flag.String("smtpaddr", "0.0.0.0:1025", "Address on which to listen for SMTP connections")
  adminAddr = flag.String("adminaddr", "127.0.0.1:7080", "Address on which to listen for admin connections")
}

func main() {
  flag.Parse()
  bannerDomain, err := os.Hostname()
  if err != nil {
    log.Critical("could not determine local hostname")
  }
  runtime.GOMAXPROCS(runtime.NumCPU())
  admin := NewAdminService(*adminAddr)
  smtp := NewSMTPService(*smtpAddr, bannerDomain)
  go RunTCP(admin)
  go RunTCP(smtp)
  for {
    select {
    case <-admin.Exited(): return
    case <-smtp.Exited(): return
    }
  }
}

