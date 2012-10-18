// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "bufio"
  "errors"
  "fmt"
  "github.com/codeslinger/log"
  "github.com/rcrowley/go-metrics"
  "io"
  "net"
  "os"
  "strconv"
  "strings"
  "time"
)

type Config interface {
  ListenLocal()   *net.TCPAddr
  ServingDomain() string
  SoftwareIdent() string
  Metrics()       metrics.Registry
}

type config struct {
  listenAddr          *net.TCPAddr
  domain              string
  ident               string
  loglevel            log.Level
  registry            metrics.Registry
  memStatsRefreshSecs int
}

var defaultListenAddr string = "0.0.0.0:1025"
var defaultSoftwareIdent string = "Go25"
var defaultMemStatsRefreshSecs int = 10
var defaultLogLevel log.Level = log.TRACE

// Return the local address on which this SMTP service is to listen.
func (c *config) ListenLocal() *net.TCPAddr {
  return c.listenAddr
}

// Return the domain from which this SMTP service runs.
func (c *config) ServingDomain() string {
  return c.domain
}

// Return the software identification string used in SMTP banners.
func (c *config) SoftwareIdent() string {
  return c.ident
}

// Return the registry of metrics for this server instance.
func (c *config) Metrics() metrics.Registry {
  return c.registry
}

func (c *config) String() string {
  return fmt.Sprintf("listen=%s domain=%s ident='%s' log=%s statsrefresh=%ds",
                     c.listenAddr,
                     c.domain,
                     c.ident,
                     c.loglevel,
                     c.memStatsRefreshSecs)
}

// Return a configuration record populated from the given file. If the file
// path given is an empty string, the default configuration record will be
// returned.
func LoadConfig(path string) (Config, error) {
  c := newConfig()
  err := c.setDefaults()
  if err != nil {
    return nil, err
  }
  if path != "" {
    if err = c.readConfig(path); err != nil {
      return nil, err
    }
  }
  log.SetLevel(c.loglevel)
  return c, nil
}

func newConfig() *config {
  return &config{
    registry:   metrics.NewRegistry(),
    domain:     "",
    ident:      "",
    listenAddr: nil,
  }
}

func (c *config) setDefaults() (err error) {
  if c.domain == "" {
    c.domain, err = os.Hostname()
    if err != nil {
      return
    }
  }
  if c.listenAddr == nil {
    err = c.setListenAddr(defaultListenAddr)
    if err != nil {
      return
    }
  }
  c.ident = defaultSoftwareIdent
  c.memStatsRefreshSecs = defaultMemStatsRefreshSecs
  c.loglevel = defaultLogLevel
  metrics.RegisterRuntimeMemStats(c.registry)
  go c.memStatsRefresh()
  return
}

func (c *config) readConfig(path string) (err error) {
  file, err := os.Open(path)
  if err != nil {
    return
  }
  rd := bufio.NewReader(file)
  idx := 0
  for {
    line, err := rd.ReadString('\n')
    if err != nil {
      if err == io.EOF {
        break
      }
      log.Error("I/O error reading from file: %s: %v", path, err)
      return err
    }
    idx++
    line = strings.Trim(line, "\r\n")
    // skip comments and blank lines
    if len(strings.Trim(line, " ")) == 0 || line[0] == '#' {
      continue
    }
    if err = c.parseLine(line, idx); err != nil {
      return err
    }
  }
  return nil
}

func (c *config) parseLine(line string, idx int) (err error) {
  parts := strings.SplitN(line, ":", 2)
  directive := strings.Trim(parts[0], " ")
  argument := strings.Trim(parts[1], " ")
  switch strings.ToLower(directive) {
  case "domain":
    if len(argument) == 0 {
      return errors.New(fmt.Sprintf("line %d: argument to 'domain' cannot be blank", idx))
    }
    c.domain = argument
  case "ident":
    if len(argument) == 0 {
      return errors.New(fmt.Sprintf("line %d: argument to 'ident' cannot be blank", idx))
    }
    c.ident = argument
  case "listen":
    if err = c.setListenAddr(argument); err != nil {
      return errors.New(fmt.Sprintf("line %d: failed to resolve 'listen' address: %v", idx, err))
    }
  case "loglevel":
    if len(argument) == 0 {
      return errors.New(fmt.Sprintf("line %d: argument to 'loglevel' cannot be blank", idx))
    }
    c.loglevel, err = c.parseLogLevel(argument)
    if err != nil {
      return errors.New(fmt.Sprintf("line %d: unknown log level ('%s'): %v", idx, argument, err))
    }
  case "statsrefresh":
    c.memStatsRefreshSecs, err = strconv.Atoi(argument)
    if err != nil {
      return errors.New(fmt.Sprintf("line %d: invalid argument to 'statsrefresh' ('%s'): %v", idx, argument, err))
    }
    if c.memStatsRefreshSecs < 1 {
      return errors.New(fmt.Sprintf("line %d: 'statsrefresh' interval cannot be <1 sec", idx))
    }
  default:
    return errors.New(fmt.Sprintf("line %d: unrecognized directive: %s", idx, directive))
  }
  return nil
}

func (c *config) setListenAddr(addr string) (err error) {
  c.listenAddr, err = net.ResolveTCPAddr("tcp", addr)
  return
}

func (c *config) memStatsRefresh() {
  metrics.CaptureRuntimeMemStats(c.registry, true)
  time.Sleep(time.Duration(int64(1e9) * int64(c.memStatsRefreshSecs)))
}

func (c *config) parseLogLevel(s string) (log.Level, error) {
  switch strings.ToLower(s) {
  case "trace": return log.TRACE, nil
  case "debug": return log.DEBUG, nil
  case "info": return log.INFO, nil
  case "warn": return log.WARN, nil
  case "error": return log.ERROR, nil
  case "critical": return log.CRITICAL, nil
  }
  return -1, errors.New("unknown log level")
}

