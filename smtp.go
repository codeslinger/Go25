// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "errors"
  "fmt"
  "io"
  "github.com/codeslinger/log"
  "net"
)

// --- SMTP Session ---------------------------------------------------------

type SMTPSession struct {
  conn   *net.TCPConn
  client io.ReadWriter
  domain string
  ident  string
  input  []byte
  state  sessionState
  from   string
  rcpts  []string
}

type sessionState int

const (
  connected = iota
  bannerSent
  heloRecevied
  mailReceived
  rcptReceived
  dataReceived
  bodyReceived
)

var (
  MaxLineLength         = 1024
  MaxRcptsPerMessage    = 10
  InShutdown            = errors.New("service shutting down")
  LineTooLong           = errors.New("command line too long")
  InvalidSentinel       = errors.New("line not terminated with CRLF")
  InvalidCommand        = errors.New("invalid SMTP command")
  InvalidArgument       = errors.New("SMTP command requested should have no arguments")
  ReadInterrupted       = errors.New("read from client interrupted")
  SessionClosedByClient = errors.New("session terminated by client")
)

// Create a new SMTP session record.
func NewSMTPSession(conn *net.TCPConn, domain, ident string) *SMTPSession {
  return &SMTPSession{
    conn:   conn,
    client: conn,
    domain: domain,
    ident:  ident,
    input:  make([]byte, MaxLineLength + 8),
    state:  connected,
    from:   "",
    rcpts:  make([]string, MaxRcptsPerMessage),
  }
}

func (s *SMTPSession) responseLine(code int, sep, message string) []byte {
  return []byte(fmt.Sprintf("%d%s%s\r\n", code, sep, message))
}

// Write a single-line response to this session.
func (s *SMTPSession) Respond(code int, message string) (err error) {
  _, err = s.client.Write(s.responseLine(code, " ", message))
  return
}

// Write a multi-line response to this session.
func (s *SMTPSession) RespondMulti(code int, messages []string) (err error) {
  for i := range messages {
    sep := "-"
    if i == len(messages) - 1 {
      sep = " "
    }
    _, err = s.client.Write(s.responseLine(code, sep, messages[i]))
    if err != nil {
      return
    }
  }
  return
}

// Read a single CRLF-terminated line from the client.
func (s *SMTPSession) readLine() (pos int, err error) {
  for {
    n, err := s.client.Read(s.input[pos:])
    if err != nil {
      return -1, err
    }
    pos += n
    for b := range s.input {
      if s.input[b] == '\n' {
        if s.input[b-1] == '\r' {
          return b, nil
        }
        return -1, InvalidSentinel
      }
    }
    if pos > MaxLineLength {
      return -1, LineTooLong
    }
  }
  return -1, ReadInterrupted
}

func (s *SMTPSession) notImplemented() error {
  return s.Respond(502, "Command not implemented")
}

func (s *SMTPSession) handleAuth(pos int) error {
  return s.notImplemented()
}

func (s *SMTPSession) handleData(pos int) error {
  err := s.Respond(354, "")
  if err != nil {
    return err
  }
  // TODO: read message body here, looking for CRLF.CRLF sentinel
  return nil
}

func (s *SMTPSession) handleEhlo(pos int) error {
  return s.notImplemented()
}

func (s *SMTPSession) handleExpn(pos int) error {
  return s.notImplemented()
}

func (s *SMTPSession) handleHelo(pos int) error {
  return s.notImplemented()
}

func (s *SMTPSession) handleHelp(pos int) error {
  return s.notImplemented()
}

func (s *SMTPSession) handleMail(pos int) error {
  if s.state != heloRecevied {
    return s.Respond(503, "Bad sequence of commands")
  }
  if s.input[10] != '<' && s.input[pos-3] != '>' {
    return s.Respond(555, "Syntax error")
  }
  s.from = string(s.input[11:pos-2])
  s.state = mailReceived
  return nil
}

func (s *SMTPSession) handleNoop(pos int) error {
  return s.notImplemented()
}

func (s *SMTPSession) handleQuit(pos int) error {
  err := s.Respond(221, "closing connection")
  if err != nil {
    return err
  }
  return SessionClosedByClient
}

func (s *SMTPSession) handleRcpt(pos int) error {
  if s.state != mailReceived || s.state != rcptReceived {
    return s.Respond(503, "Bad sequence of commands")
  }
  if s.input[8] != '<' || s.input[pos-3] != '>' {
    return s.Respond(555, "Syntax error")
  }
  // TODO: add to s.rcpts here
  //rcpt := string(s.input[9:pos-2])
  s.state = rcptReceived
  return nil
}

func (s *SMTPSession) handleRset(pos int) error {
  s.state = bannerSent
  return s.Respond(250, "Flushed")
}

func (s *SMTPSession) handleVrfy(pos int) error {
  return s.notImplemented()
}

// Greet a newly-connected SMTP client with the initial banner message.
// Will send a 554 error response if the server is in the process of shutting
// down when the client connects.
func (s *SMTPSession) Greet(shutdown bool) error {
  if shutdown {
    // RFC 2821 says the only acceptable response other than a 220 on connect
    // is a 554, so that's what we use
    s.Respond(554, "Service unavailable; shutting down")
    return InShutdown
  }
  err := s.Respond(220, fmt.Sprintf("%s ESMTP %s", s.domain, s.ident))
  if err != nil {
    log.Error("%s: failed to write banner: %v", s.conn.RemoteAddr(), err)
    return err
  }
  s.state = bannerSent
  return nil
}

// Read, process and respond to a single SMTP command from the client.
func (s *SMTPSession) HandleCommand() (err error) {
  pos, err := s.readLine()
  if err != nil {
    return
  }
  err = InvalidCommand
  if pos < 4 {
    return
  }
  // I know, I know... its gross but its fast
  if s.input[0] == 'A' || s.input[0] == 'a' {
    if (s.input[1] == 'U' || s.input[1] == 'u') &&
       (s.input[2] == 'T' || s.input[2] == 't') &&
       (s.input[3] == 'H' || s.input[3] == 'h') &&
       s.input[4] == ' ' {
      s.handleAuth(pos)
    }
  } else if s.input[0] == 'D' || s.input[0] == 'd' {
    if (s.input[1] == 'A' || s.input[1] == 'a') &&
       (s.input[2] == 'T' || s.input[2] == 't') &&
       (s.input[3] == 'A' || s.input[3] == 'a') {
      s.handleData(pos)
    }
  } else if s.input[0] == 'E' || s.input[0] == 'e' {
    if s.input[1] == 'H' || s.input[1] == 'h' {
      if (s.input[2] == 'L' || s.input[2] == 'l') &&
         (s.input[3] == 'O' || s.input[3] == 'o') &&
         s.input[4] == ' ' {
        s.handleEhlo(pos)
      }
    } else if s.input[1] == 'X' || s.input[1] == 'x' {
      if (s.input[2] == 'P' || s.input[2] == 'p') &&
         (s.input[3] == 'N' || s.input[3] == 'n') &&
         s.input[4] == ' ' {
        s.handleExpn(pos)
      }
    }
  } else if s.input[0] == 'H' || s.input[0] == 'h' {
    if (s.input[1] == 'E' || s.input[1] == 'e') &&
       (s.input[2] == 'L' || s.input[2] == 'l') {
      if (s.input[3] == 'O' || s.input[3] == 'o') && s.input[4] == ' ' {
        s.handleHelo(pos)
      } else if s.input[3] == 'P' || s.input[3] == 'p' {
        s.handleHelp(pos)
      }
    }
  } else if s.input[0] == 'M' || s.input[0] == 'm' {
    if pos < 10 {
      return
    }
    if (s.input[1] == 'A' || s.input[1] == 'a') &&
       (s.input[2] == 'I' || s.input[2] == 'i') &&
       (s.input[3] == 'L' || s.input[2] == 'l') &&
       s.input[4] == ' ' &&
       (s.input[5] == 'F' || s.input[2] == 'f') &&
       (s.input[6] == 'R' || s.input[2] == 'r') &&
       (s.input[7] == 'O' || s.input[2] == 'o') &&
       (s.input[8] == 'M' || s.input[2] == 'm') &&
       s.input[9] == ':' {
      s.handleMail(pos)
    }
  } else if s.input[0] == 'N' || s.input[0] == 'n' {
    if (s.input[1] == 'O' || s.input[1] == 'o') &&
       (s.input[2] == 'O' || s.input[2] == 'o') &&
       (s.input[3] == 'P' || s.input[3] == 'p') {
      s.handleNoop(pos)
    }
  } else if s.input[0] == 'Q' || s.input[0] == 'q' {
    if (s.input[1] == 'U' || s.input[1] == 'u') &&
       (s.input[2] == 'I' || s.input[2] == 'i') &&
       (s.input[3] == 'T' || s.input[3] == 't') {
      s.handleQuit(pos)
    }
  } else if s.input[0] == 'R' || s.input[0] == 'r' {
    if pos < 8 {
      return
    }
    if s.input[1] == 'C' || s.input[1] == 'c' {
      if (s.input[2] == 'P' || s.input[2] == 'p') &&
         (s.input[3] == 'T' || s.input[3] == 't') &&
         s.input[4] == ' ' &&
         (s.input[5] == 'T' || s.input[5] == 't') &&
         (s.input[6] == 'O' || s.input[6] == 'o') &&
         s.input[7] == ':' {
        s.handleRcpt(pos)
      }
    } else if s.input[1] == 'S' || s.input[1] == 's' {
      if (s.input[2] == 'E' || s.input[2] == 'e') &&
         (s.input[3] == 'T' || s.input[3] == 't') {
        s.handleRset(pos)
      }
    }
  } else if s.input[0] == 'V' || s.input[0] == 'v' {
    if (s.input[1] == 'R' || s.input[1] == 'r') &&
       (s.input[2] == 'F' || s.input[2] == 'f') &&
       (s.input[3] == 'Y' || s.input[3] == 'y') {
      s.handleVrfy(pos)
    }
  }
  return
}

// --- SMTP Service ---------------------------------------------------------

var DefaultServerName = "qsendfix"

type SMTPService struct {
  addr       string
  exited     chan int
  draining   bool

  // server software identification
  ServerName string
  // domain name to report to clients in initial banner
  HELOName   string
}

// Create a new SMTP server instance bound to the given TCP address.
func NewSMTPService(addr string, serverDomain string) *SMTPService {
  return &SMTPService{
    addr:       addr,
    exited:     make(chan int, 1),
    draining:   false,
    ServerName: DefaultServerName,
    HELOName:   serverDomain,
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

  session := NewSMTPSession(conn, s.ServerName, s.HELOName)
  err := session.Greet(s.draining)
  if err != nil {
    log.Error("%s: failed to send greeting banner: %v", conn.RemoteAddr(), err)
    return
  }
  for {
    err = session.HandleCommand()
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

