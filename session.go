// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "errors"
  "fmt"
  "github.com/codeslinger/log"
  "io"
  "net"
  "time"
)

// --- SMTP Session ---------------------------------------------------------

type SMTPSession struct {
  remote   *net.TCPAddr
  client   io.ReadWriter
  input    []byte
  state    sessionState
  ident    *string
  domain   *string
  message  *SMTPMessage
}

type sessionState int

const (
  connected = iota
  bannerSent
  heloReceived
  mailReceived
  rcptReceived
  dataReceived
  bodyReceived
)

var (
  MaxMsgSize        = 16777216  // FIXME: should be in config
  MaxLineLength     = 1024
  MinCommandLength  = 6
  MinMailLineLength = 14
  MinRcptLineLength = 12
  InputBufSize      = 2048
)

var (
  AddressNotFound = errors.New("could not find email address in command syntax")
  LineTooLong     = errors.New("command line too long")
  InvalidSentinel = errors.New("line not terminated with CRLF")
  InvalidCommand  = errors.New("invalid SMTP command")
  MessageTooLong  = errors.New("Message body was over maximum size allowed")
  ReadInterrupted = errors.New("read from client interrupted")
  TimeoutError    = errors.New("session timed out")
)

var ResponseMap = map[int][]byte {
  211: []byte("211 System status, or system help reply\r\n"),
  214: []byte("214 http://www.ietf.org/rfc/rfc2821.txt\r\n"),
  220: []byte("220 Service ready\r\n"),
  221: []byte("221 Service closing transmission channel\r\n"),
  250: []byte("250 OK\r\n"),
  251: []byte("251 User not local; will attempt to forward\r\n"),
  252: []byte("252 Cannot VRFY user, but will accept message and attempt delivery\r\n"),
  354: []byte("354 Start mail input; end with <CRLF>.<CRLF>\r\n"),
  421: []byte("421 Service not available, closing transmission channel\r\n"),
  450: []byte("450 Requested mail action not taken: mailbox unavailable\r\n"),
  451: []byte("451 Requested action aborted: local error in processing\r\n"),
  452: []byte("452 Requested action not taken: insufficient system storage\r\n"),
  500: []byte("500 Syntax error, command unrecognized\r\n"),
  501: []byte("501 Syntax error in parameters or arguments\r\n"),
  502: []byte("502 Command not implemented\r\n"),
  503: []byte("503 Bad sequence of commands\r\n"),
  504: []byte("504 Command parameter not implemented\r\n"),
  550: []byte("550 Requested action not taken: mailbox unavailable\r\n"),
  551: []byte("551 User not local\r\n"),
  552: []byte("552 Requested mail action aborted: exceeded storage allocation\r\n"),
  553: []byte("553 Requested action not taken: mailbox name not allowed\r\n"),
  554: []byte("554 Transaction failed\r\n"),
}

// Create a new SMTP session record.
func NewSMTPSession(client io.ReadWriter,
                    remoteAddr *net.TCPAddr,
                    ident, domain *string) *SMTPSession {
  return &SMTPSession{
    remote:   remoteAddr,
    client:   client,
    input:    make([]byte, InputBufSize),
    state:    connected,
    ident:    ident,
    domain:   domain,
    message:  NewSMTPMessage(),
  }
}

// Write a single-line response from the ResponseMap for the given code.
func (s *SMTPSession) R(code int) (err error) {
  _, err = s.client.Write(ResponseMap[code])
  return
}

// Greet a newly-connected SMTP client with the initial banner message.
func (s *SMTPSession) Greet(shutdown bool) error {
  s.state = bannerSent
  return s.respond(220, s.banner())
}

// TODO: make this pipelining-safe
// Read, process and respond to a SMTP command(s) from the client.
func (s *SMTPSession) Process() (Verdict, error) {
  size, err := s.readLine()
  if err != nil {
    if err == LineTooLong || err == InvalidSentinel {
      return Continue, s.R(500)
    }
    return Continue, s.R(554)
  }
  if size < MinCommandLength {
    return Continue, s.R(500)
  }
  // I know, I know... its gross but its fast
  if s.input[0] == 'A' || s.input[0] == 'a' {
    if (s.input[1] == 'U' || s.input[1] == 'u') &&
       (s.input[2] == 'T' || s.input[2] == 't') &&
       (s.input[3] == 'H' || s.input[3] == 'h') &&
       s.input[4] == ' ' {
      return s.handleAuth(size)
    }
  } else if s.input[0] == 'D' || s.input[0] == 'd' {
    if (s.input[1] == 'A' || s.input[1] == 'a') &&
       (s.input[2] == 'T' || s.input[2] == 't') &&
       (s.input[3] == 'A' || s.input[3] == 'a') {
      return s.handleData(size)
    }
  } else if s.input[0] == 'E' || s.input[0] == 'e' {
    if s.input[1] == 'H' || s.input[1] == 'h' {
      if (s.input[2] == 'L' || s.input[2] == 'l') &&
         (s.input[3] == 'O' || s.input[3] == 'o') &&
         s.input[4] == ' ' {
        return s.handleEhlo(size)
      }
    } else if s.input[1] == 'X' || s.input[1] == 'x' {
      if (s.input[2] == 'P' || s.input[2] == 'p') &&
         (s.input[3] == 'N' || s.input[3] == 'n') &&
         s.input[4] == ' ' {
        return s.handleExpn(size)
      }
    }
  } else if s.input[0] == 'H' || s.input[0] == 'h' {
    if (s.input[1] == 'E' || s.input[1] == 'e') &&
       (s.input[2] == 'L' || s.input[2] == 'l') {
      if (s.input[3] == 'O' || s.input[3] == 'o') && s.input[4] == ' ' {
        return s.handleHelo(size)
      } else if s.input[3] == 'P' || s.input[3] == 'p' {
        return s.handleHelp(size)
      }
    }
  } else if s.input[0] == 'M' || s.input[0] == 'm' {
    if size < MinMailLineLength {
      return Continue, s.R(500)
    }
    if (s.input[1] == 'A' || s.input[1] == 'a') &&
       (s.input[2] == 'I' || s.input[2] == 'i') &&
       (s.input[3] == 'L' || s.input[3] == 'l') &&
       s.input[4] == ' ' &&
       (s.input[5] == 'F' || s.input[5] == 'f') &&
       (s.input[6] == 'R' || s.input[6] == 'r') &&
       (s.input[7] == 'O' || s.input[7] == 'o') &&
       (s.input[8] == 'M' || s.input[8] == 'm') &&
       s.input[9] == ':' {
      return s.handleMail(size)
    }
    log.Trace(string(s.input))
  } else if s.input[0] == 'N' || s.input[0] == 'n' {
    if (s.input[1] == 'O' || s.input[1] == 'o') &&
       (s.input[2] == 'O' || s.input[2] == 'o') &&
       (s.input[3] == 'P' || s.input[3] == 'p') {
      return s.handleNoop(size)
    }
  } else if s.input[0] == 'Q' || s.input[0] == 'q' {
    if (s.input[1] == 'U' || s.input[1] == 'u') &&
       (s.input[2] == 'I' || s.input[2] == 'i') &&
       (s.input[3] == 'T' || s.input[3] == 't') {
      return s.handleQuit(size)
    }
  } else if s.input[0] == 'R' || s.input[0] == 'r' {
    if s.input[1] == 'C' || s.input[1] == 'c' {
      if size < MinRcptLineLength {
        return Continue, s.R(500)
      }
      if (s.input[2] == 'P' || s.input[2] == 'p') &&
         (s.input[3] == 'T' || s.input[3] == 't') &&
         s.input[4] == ' ' &&
         (s.input[5] == 'T' || s.input[5] == 't') &&
         (s.input[6] == 'O' || s.input[6] == 'o') &&
         s.input[7] == ':' {
        return s.handleRcpt(size)
      }
    } else if s.input[1] == 'S' || s.input[1] == 's' {
      if (s.input[2] == 'E' || s.input[2] == 'e') &&
         (s.input[3] == 'T' || s.input[3] == 't') {
        return s.handleRset(size)
      }
    }
  } else if s.input[0] == 'S' || s.input[0] == 's' {
    if size < MinMailLineLength {
      return Continue, s.R(500)
    }
    if s.input[1] == 'E' || s.input[1] == 'e' {
      if (s.input[2] == 'N' || s.input[2] == 'n') &&
         (s.input[3] == 'D' || s.input[3] == 'd') &&
         s.input[4] == ' ' &&
         (s.input[5] == 'F' || s.input[5] == 'f') &&
         (s.input[6] == 'R' || s.input[6] == 'r') &&
         (s.input[7] == 'O' || s.input[7] == 'o') &&
         (s.input[8] == 'M' || s.input[8] == 'm') &&
         s.input[9] == ':' {
        return s.handleSend(size)
      }
    } else if s.input[1] == 'A' || s.input[1] == 'a' {
      if (s.input[2] == 'M' || s.input[2] == 'm') &&
         (s.input[3] == 'L' || s.input[3] == 'l') &&
         s.input[4] == ' ' &&
         (s.input[5] == 'F' || s.input[5] == 'f') &&
         (s.input[6] == 'R' || s.input[6] == 'r') &&
         (s.input[7] == 'O' || s.input[7] == 'o') &&
         (s.input[8] == 'M' || s.input[8] == 'm') &&
         s.input[9] == ':' {
        return s.handleSaml(size)
      }
    } else if s.input[1] == 'O' || s.input[1] == 'o' {
      if (s.input[2] == 'M' || s.input[2] == 'm') &&
         (s.input[3] == 'L' || s.input[3] == 'l') &&
         s.input[4] == ' ' &&
         (s.input[5] == 'F' || s.input[5] == 'f') &&
         (s.input[6] == 'R' || s.input[6] == 'r') &&
         (s.input[7] == 'O' || s.input[7] == 'o') &&
         (s.input[8] == 'M' || s.input[8] == 'm') &&
         s.input[9] == ':' {
        return s.handleSoml(size)
      }
    }
  } else if s.input[0] == 'V' || s.input[0] == 'v' {
    if (s.input[1] == 'R' || s.input[1] == 'r') &&
       (s.input[2] == 'F' || s.input[2] == 'f') &&
       (s.input[3] == 'Y' || s.input[3] == 'y') {
      return s.handleVrfy(size)
    }
  }
  return Continue, s.R(500)
}

// Process an AUTH command.
func (s *SMTPSession) handleAuth(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Process a DATA command.
func (s *SMTPSession) handleData(size int) (Verdict, error) {
  if len(s.message.To) < 1 {
    return Continue, s.respond(554, "no valid recipients given")
  }
  err := s.R(354)
  if err != nil {
    return Terminate, err
  }
  s.state = dataReceived
  body, err := s.readBody()
  if err != nil {
    return Continue, err
  }
  s.message.Body = body
  s.state = bodyReceived
  return Continue, s.R(250)
}

// Process an EHLO command.
func (s *SMTPSession) handleEhlo(size int) (Verdict, error) {
  if s.state > bannerSent {
    return Continue, s.R(503)
  }
  s.state = heloReceived
  return Continue, s.respondMulti(
    250,
    []string{s.heloLine(),
             fmt.Sprintf("SIZE %d", MaxMsgSize),
             "PIPELINING",
             "8BITMIME"})
}

// Process an ETRN command.
func (s *SMTPSession) handleEtrn(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Process an EXPN command.
func (s *SMTPSession) handleExpn(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Process a HELO command.
func (s *SMTPSession) handleHelo(size int) (Verdict, error) {
  if s.state > bannerSent {
    return Continue, s.R(503)
  }
  s.state = heloReceived
  return Continue, s.respond(250, s.heloLine())
}

// Process a HELP command.
func (s *SMTPSession) handleHelp(size int) (Verdict, error) {
  return Continue, s.R(214)
}

// Process a MAIL FROM command.
func (s *SMTPSession) handleMail(size int) (Verdict, error) {
  if s.state != heloReceived {
    return Continue, s.R(503)
  }
  from, err := s.extractAddress(0, size)
  if err != nil {
    return Terminate, s.R(501)
  }
  s.message.Remote = s.remote
  s.message.From = from
  s.state = mailReceived
  return Continue, s.R(250)
}

// Process a NOOP command.
func (s *SMTPSession) handleNoop(size int) (Verdict, error) {
  return Continue, s.R(250)
}

// Process a QUIT command.
func (s *SMTPSession) handleQuit(size int) (Verdict, error) {
  err := s.R(221)
  return Terminate, err
}

// Process a RCPT TO command.
func (s *SMTPSession) handleRcpt(size int) (Verdict, error) {
  if s.state != mailReceived && s.state != rcptReceived {
    return Continue, s.R(503)
  }
  rcpt, err := s.extractAddress(0, size)
  if err != nil {
    return Continue, s.R(501)
  }
  s.message.AddRecipient(rcpt)
  s.state = rcptReceived
  return Continue, s.R(250)
}

// Process a RSET command.
func (s *SMTPSession) handleRset(size int) (Verdict, error) {
  if s.state >= heloReceived {
    s.state = heloReceived
    s.message = NewSMTPMessage()
  }
  return Continue, s.R(250)
}

// Process a SEND FROM command.
func (s *SMTPSession) handleSend(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Process a SAML FROM command.
func (s *SMTPSession) handleSaml(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Process a SOML FROM command.
func (s *SMTPSession) handleSoml(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Process a TURN command.
func (s *SMTPSession) handleTurn(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Process a VRFY command.
func (s *SMTPSession) handleVrfy(size int) (Verdict, error) {
  return Continue, s.R(502)
}

// Format SMTP response line with code and message.
func (s *SMTPSession) responseLine(code int, sep, message string) []byte {
  return []byte(fmt.Sprintf("%d%s%s\r\n", code, sep, message))
}

// Write a single-line response to this session.
func (s *SMTPSession) respond(code int, message string) (err error) {
  _, err = s.client.Write(s.responseLine(code, " ", message))
  return
}

// Write a multi-line response to this session.
func (s *SMTPSession) respondMulti(code int, messages []string) (err error) {
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
func (s *SMTPSession) readLine() (int, error) {
  pos := 0
  for {
    n, err := s.client.Read(s.input[pos:])
    if err != nil {
      return -1, err
    }
    pos += n
    for b := range s.input {
      if s.input[b] == '\n' {
        if s.input[b-1] == '\r' {
          return b + 1, nil
        }
        return -1, InvalidSentinel
      }
    }
    if pos + 1 > MaxLineLength {
      return -1, LineTooLong
    }
  }
  return -1, ReadInterrupted
}

// Read in the <CRLF>.<CRLF>-terminated body of an SMTP message submission.
func (s *SMTPSession) readBody() (string, error) {
  // TODO: spill message to disk if its over a certain size
  body := make([]byte, MaxMsgSize)
  pos := 0
  for {
    n, err := s.client.Read(body[pos:])
    if err != nil {
      return "", err
    }
    pos += n
    if body[pos-1] == '\n' &&
       body[pos-2] == '\r' &&
       body[pos-3] == '.'  &&
       body[pos-4] == '\n' &&
       body[pos-5] == '\r' {
      break
    }
    if pos >= MaxMsgSize {
      return "", MessageTooLong
    }
  }
  return string(body[0:pos-5]), nil
}

// Extract the email address part of an SMTP command line that should
// contain one (i.e. the stuff between the < and > in MAIL/RCPT commands).
func (s *SMTPSession) extractAddress(begin, size int) (string, error) {
  start, end := -1, -1
  for i := begin; i < size + begin; i++ {
    if s.input[i] == '<' {
      start = i
    } else if s.input[i] == '>' {
      end = i
    }
  }
  if start > -1 && end > -1 && end > start {
    return string(s.input[start+1:end]), nil
  }
  return "", AddressNotFound
}

// Format line for greeting clients at initial connect time.
func (s *SMTPSession) banner() string {
  return fmt.Sprintf("%s %s Service ready at %s",
                     *s.domain,
                     *s.ident,
                     time.Now().Format(time.RFC1123Z))
}

// Format line for greeting clients in response to HELO/EHLO command.
func (s *SMTPSession) heloLine() string {
  return fmt.Sprintf("%s Hello [%s]", *s.domain, s.remote.IP)
}

