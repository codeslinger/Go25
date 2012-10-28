// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "flag"
  "github.com/codeslinger/log"
  "os"
  "os/signal"
  "runtime"
  "syscall"
)

var configPath *string
var cfg Config

func init() {
  configPath = flag.String("config", "", "Path to configuration file")
}

func main() {
  flag.Parse()
  cfg, err := LoadConfig(*configPath)
  if err != nil {
    log.Error("failed to load config: %s", err)
    return
  }
  log.Info("loaded config: %s", cfg)
  runtime.GOMAXPROCS(cfg.Cores())
  exitChan := trapSignals()
  go RunTCP(NewSMTPService(cfg, exitChan))
  <-exitChan
}

func trapSignals() chan int {
  exitChan := make(chan int)
  signalChan := make(chan os.Signal, 1)
  go func() {
    <-signalChan
    exitChan <- 1
  }()
  signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
  return exitChan
}

