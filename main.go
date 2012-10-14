// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "flag"
  "github.com/codeslinger/log"
  "github.com/rcrowley/go-metrics"
  "os"
  "os/signal"
  "runtime"
  "syscall"
  "time"
)

var (
  smtpAddr     *string
  bannerDomain string
  mReg         metrics.Registry
)

func init() {
  smtpAddr = flag.String("smtpaddr", "0.0.0.0:1025", "Address on which to listen for SMTP connections")
}

func main() {
  flag.Parse()

  bannerDomain, err := os.Hostname()
  if err != nil {
    log.Error("could not determine local hostname")
    return
  }

  mReg = metrics.NewRegistry()
  metrics.RegisterRuntimeMemStats(mReg)
  go func(interval int) {
    metrics.CaptureRuntimeMemStats(mReg, true)
    time.Sleep(time.Duration(int64(1e9) * int64(interval)))
  }(2)

  runtime.GOMAXPROCS(runtime.NumCPU())

  exitChan := make(chan int)
  signalChan := make(chan os.Signal, 1)
  go func() {
    <-signalChan
    exitChan <- 1
  }()
  signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

  go RunTCP(NewSMTPService(*smtpAddr, bannerDomain, exitChan))
  <-exitChan
}

