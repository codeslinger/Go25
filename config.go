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
  "runtime"
  "strconv"
  "strings"
  "time"
)

type Config interface {
  ListenLocal()   *net.TCPAddr
  MaxIdleSecs()   int
  MaxMsgSize()    int
  ServingDomain() string
  SoftwareIdent() string
  Metrics()       metrics.Registry
  Cores()         int
}

type config struct {
  domain              string
  ident               string
  listenAddr          *net.TCPAddr
  loglevel            log.Level
  maxIdleSecs         int
  maxMsgSize          int
  memStatsRefreshSecs int
  registry            metrics.Registry
  cores               int
}

const (
  defaultListenAddr          = "0.0.0.0:1025"
  defaultSoftwareIdent       = "Go25"
  defaultMemStatsRefreshSecs = 10
  defaultLogLevel            = log.TRACE
  defaultMaxIdleSecs         = 120
  defaultMaxMsgSize          = 16777216
)

// Return the local address on which this SMTP service is to listen.
func (c *config) ListenLocal() *net.TCPAddr {
  return c.listenAddr
}

// Return the timeout at which a session is considered idle and should be
// terminated, in seconds.
func (c *config) MaxIdleSecs() int {
  return c.maxIdleSecs
}

// Return the maximum size of a message allowed, in bytes.
func (c *config) MaxMsgSize() int {
  return c.maxMsgSize
}

// Return the registry of metrics for this server instance.
func (c *config) Metrics() metrics.Registry {
  return c.registry
}

// Return the domain from which this SMTP service runs.
func (c *config) ServingDomain() string {
  return c.domain
}

// Return the software identification string used in SMTP banners.
func (c *config) SoftwareIdent() string {
  return c.ident
}

// Return the number of CPU cores to use.
func (c *config) Cores() int {
  return c.cores
}

func (c *config) String() string {
  return fmt.Sprintf(
    "listen=%s domain=%s ident='%s' log=%s maxidle=%ds maxmsg=%dB statsrefresh=%ds cores=%d",
    c.listenAddr,
    c.domain,
    c.ident,
    c.loglevel,
    c.maxIdleSecs,
    c.maxMsgSize,
    c.memStatsRefreshSecs,
    c.cores)
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
  c.maxIdleSecs = defaultMaxIdleSecs
  c.maxMsgSize = defaultMaxMsgSize
  c.cores = runtime.NumCPU()
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
  case "cores":
    if len(argument) == 0 {
      return errors.New(fmt.Sprintf("line %d: argument to 'cores' cannot be blank", idx))
    }
    c.cores, err = strconv.Atoi(argument)
    if err != nil {
      return errors.New(fmt.Sprintf("line %d: malformed integer: %s", idx, argument))
    }
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
  case "maxidle":
    c.maxIdleSecs, err = strconv.Atoi(argument)
    if err != nil {
      return errors.New(fmt.Sprintf("line %d: invalid argument to 'maxidle' ('%s'): %v", idx, argument, err))
    }
    if c.maxIdleSecs < 1 {
      return errors.New(fmt.Sprintf("line %d: 'maxidle' value cannot be <1 second", idx))
    }
  case "maxmsgsize":
    c.maxMsgSize, err = strconv.Atoi(argument)
    if err != nil {
      return errors.New(fmt.Sprintf("line %d: invalid argument to 'maxmsgsize' ('%s'): %v", idx, argument, err))
    }
    if c.maxMsgSize < 1 {
      return errors.New(fmt.Sprintf("line %d: 'maxmsgsize' value cannot be <1 byte", idx))
    }
  case "statsrefresh":
    c.memStatsRefreshSecs, err = strconv.Atoi(argument)
    if err != nil {
      return errors.New(fmt.Sprintf("line %d: invalid argument to 'statsrefresh' ('%s'): %v", idx, argument, err))
    }
    if c.memStatsRefreshSecs < 1 {
      return errors.New(fmt.Sprintf("line %d: 'statsrefresh' interval cannot be <1 second", idx))
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

