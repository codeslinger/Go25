// vim:set ts=2 sw=2 et ai ft=go:
package main

import (
  "bufio"
  "errors"
  "fmt"
  "github.com/codeslinger/log"
  "net"
  "time"
)

// --- SMTP Session ---------------------------------------------------------

type SMTPSession struct {
  conn     *net.TCPConn
  r        *bufio.Reader
  remote   *net.TCPAddr
  state    sessionState
  service  *SMTPService
  message  *SMTPMessage
}

type sessionState int

const (
  connected sessionState = iota
  bannerSent
  heloReceived
  mailReceived
  rcptReceived
  dataReceived
  bodyReceived
)

var (
  MaxMsgSize        = 16777216  // FIXME: should be in config
  MaxIdleSeconds    = 120
  MaxLineLength     = 1024
  MinCommandLength  = 6
  MinMailLineLength = 14
  MinRcptLineLength = 12
)

var (
  AddressNotFound = errors.New("could not find email address in command syntax")
  MessageTooLong  = errors.New("Message body was over maximum size allowed")
  TimeoutError    = errors.New("session timed out")
)

var sizeLine = fmt.Sprintf("SIZE %d", MaxMsgSize)

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
func NewSMTPSession(conn *net.TCPConn, service *SMTPService) *SMTPSession {
  return &SMTPSession{
    r:        bufio.NewReaderSize(conn, MaxLineLength),
    conn:     conn,
    remote:   conn.RemoteAddr().(*net.TCPAddr),
    state:    connected,
    service:  service,
    message:  nil,
  }
}

// Greet a newly-connected SMTP client with the initial banner message.
func (s *SMTPSession) Greet() Verdict {
  s.state = bannerSent
  return s.respondWithVerdict(220, s.banner())
}

// Read, process and respond to a SMTP command(s) from the client.
func (s *SMTPSession) Process() Verdict {
  data, err := s.readLine()
  if err != nil {
    s.codeWithVerdict(221)
    return Terminate
  }
  if len(data) < MinCommandLength {
    return s.codeWithVerdict(500)
  }
  // I know, I know... its gross but its fast
  if data[0] == 'A' || data[0] == 'a' {
    if (data[1] == 'U' || data[1] == 'u') &&
       (data[2] == 'T' || data[2] == 't') &&
       (data[3] == 'H' || data[3] == 'h') &&
       data[4] == ' ' {
      return s.handleAuth(data)
    }
  } else if data[0] == 'D' || data[0] == 'd' {
    if (data[1] == 'A' || data[1] == 'a') &&
       (data[2] == 'T' || data[2] == 't') &&
       (data[3] == 'A' || data[3] == 'a') {
      return s.handleData(data)
    }
  } else if data[0] == 'E' || data[0] == 'e' {
    if data[1] == 'H' || data[1] == 'h' {
      if (data[2] == 'L' || data[2] == 'l') &&
         (data[3] == 'O' || data[3] == 'o') &&
         data[4] == ' ' {
        return s.handleEhlo(data)
      }
    } else if data[1] == 'X' || data[1] == 'x' {
      if (data[2] == 'P' || data[2] == 'p') &&
         (data[3] == 'N' || data[3] == 'n') &&
         data[4] == ' ' {
        return s.handleExpn(data)
      }
    }
  } else if data[0] == 'H' || data[0] == 'h' {
    if (data[1] == 'E' || data[1] == 'e') &&
       (data[2] == 'L' || data[2] == 'l') {
      if (data[3] == 'O' || data[3] == 'o') &&
         data[4] == ' ' {
        return s.handleHelo(data)
      } else if data[3] == 'P' || data[3] == 'p' {
        return s.handleHelp(data)
      }
    }
  } else if data[0] == 'M' || data[0] == 'm' {
    if len(data) < MinMailLineLength {
      return s.codeWithVerdict(500)
    }
    if (data[1] == 'A' || data[1] == 'a') &&
       (data[2] == 'I' || data[2] == 'i') &&
       (data[3] == 'L' || data[3] == 'l') &&
       data[4] == ' ' &&
       (data[5] == 'F' || data[5] == 'f') &&
       (data[6] == 'R' || data[6] == 'r') &&
       (data[7] == 'O' || data[7] == 'o') &&
       (data[8] == 'M' || data[8] == 'm') &&
       data[9] == ':' {
      return s.handleMail(data)
    }
  } else if data[0] == 'N' || data[0] == 'n' {
    if (data[1] == 'O' || data[1] == 'o') &&
       (data[2] == 'O' || data[2] == 'o') &&
       (data[3] == 'P' || data[3] == 'p') {
      return s.handleNoop(data)
    }
  } else if data[0] == 'Q' || data[0] == 'q' {
    if (data[1] == 'U' || data[1] == 'u') &&
       (data[2] == 'I' || data[2] == 'i') &&
       (data[3] == 'T' || data[3] == 't') {
      return s.handleQuit(data)
    }
  } else if data[0] == 'R' || data[0] == 'r' {
    if data[1] == 'C' || data[1] == 'c' {
      if len(data) < MinRcptLineLength {
        return s.codeWithVerdict(500)
      }
      if (data[2] == 'P' || data[2] == 'p') &&
         (data[3] == 'T' || data[3] == 't') &&
         data[4] == ' ' &&
         (data[5] == 'T' || data[5] == 't') &&
         (data[6] == 'O' || data[6] == 'o') &&
         data[7] == ':' {
        return s.handleRcpt(data)
      }
    } else if data[1] == 'S' || data[1] == 's' {
      if (data[2] == 'E' || data[2] == 'e') &&
         (data[3] == 'T' || data[3] == 't') {
        return s.handleRset(data)
      }
    }
  } else if data[0] == 'S' || data[0] == 's' {
    if len(data) < MinMailLineLength {
      return s.codeWithVerdict(500)
    }
    if data[1] == 'E' || data[1] == 'e' {
      if (data[2] == 'N' || data[2] == 'n') &&
         (data[3] == 'D' || data[3] == 'd') &&
         data[4] == ' ' &&
         (data[5] == 'F' || data[5] == 'f') &&
         (data[6] == 'R' || data[6] == 'r') &&
         (data[7] == 'O' || data[7] == 'o') &&
         (data[8] == 'M' || data[8] == 'm') &&
         data[9] == ':' {
        return s.handleSend(data)
      }
    } else if data[1] == 'A' || data[1] == 'a' {
      if (data[2] == 'M' || data[2] == 'm') &&
         (data[3] == 'L' || data[3] == 'l') &&
         data[4] == ' ' &&
         (data[5] == 'F' || data[5] == 'f') &&
         (data[6] == 'R' || data[6] == 'r') &&
         (data[7] == 'O' || data[7] == 'o') &&
         (data[8] == 'M' || data[8] == 'm') &&
         data[9] == ':' {
        return s.handleSaml(data)
      }
    } else if data[1] == 'O' || data[1] == 'o' {
      if (data[2] == 'M' || data[2] == 'm') &&
         (data[3] == 'L' || data[3] == 'l') &&
         data[4] == ' ' &&
         (data[5] == 'F' || data[5] == 'f') &&
         (data[6] == 'R' || data[6] == 'r') &&
         (data[7] == 'O' || data[7] == 'o') &&
         (data[8] == 'M' || data[8] == 'm') &&
         data[9] == ':' {
        return s.handleSoml(data)
      }
    }
  } else if data[0] == 'V' || data[0] == 'v' {
    if (data[1] == 'R' || data[1] == 'r') &&
       (data[2] == 'F' || data[2] == 'f') &&
       (data[3] == 'Y' || data[3] == 'y') {
      return s.handleVrfy(data)
    }
  }
  return s.codeWithVerdict(500)
}

// Process an AUTH command.
func (s *SMTPSession) handleAuth(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Process a DATA command.
func (s *SMTPSession) handleData(data []byte) Verdict {
  if s.message.To.Len() < 1 {
    return s.respondWithVerdict(554, "no valid recipients given")
  }
  if err := s.respondCode(354); err != nil {
    return Terminate
  }
  s.state = dataReceived
  body, err := s.readBody()
  if err != nil {
    log.Error("failed to read body of message: %v", err)
    return Terminate
  }
  s.message.Body = body
  s.state = bodyReceived
  return s.codeWithVerdict(250)
}

// Process an EHLO command.
func (s *SMTPSession) handleEhlo(data []byte) Verdict {
  if s.state > bannerSent {
    return s.codeWithVerdict(503)
  }
  if err := s.respondMulti(250, []string{s.heloLine(), sizeLine, "PIPELINING", "8BITMIME"}); err != nil {
    return Terminate
  }
  s.state = heloReceived
  return Continue
}

// Process an ETRN command.
func (s *SMTPSession) handleEtrn(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Process an EXPN command.
func (s *SMTPSession) handleExpn(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Process a HELO command.
func (s *SMTPSession) handleHelo(data []byte) Verdict {
  if s.state > bannerSent {
    return s.codeWithVerdict(503)
  }
  s.state = heloReceived
  return s.respondWithVerdict(250, s.heloLine())
}

// Process a HELP command.
func (s *SMTPSession) handleHelp(data []byte) Verdict {
  return s.codeWithVerdict(214)
}

// Process a MAIL FROM command.
func (s *SMTPSession) handleMail(data []byte) Verdict {
  if s.state != heloReceived {
    return s.codeWithVerdict(503)
  }
  from, err := s.extractAddress(data)
  if err != nil {
    return s.codeWithVerdict(501)
  }
  s.message = NewSMTPMessage(s.remote)
  s.message.From = from
  s.state = mailReceived
  return s.codeWithVerdict(250)
}

// Process a NOOP command.
func (s *SMTPSession) handleNoop(data []byte) Verdict {
  return s.codeWithVerdict(250)
}

// Process a QUIT command.
func (s *SMTPSession) handleQuit(data []byte) Verdict {
  s.respondCode(221)
  return Terminate
}

// Process a RCPT TO command.
func (s *SMTPSession) handleRcpt(data []byte) Verdict {
  if s.state != mailReceived && s.state != rcptReceived {
    return s.codeWithVerdict(503)
  }
  rcpt, err := s.extractAddress(data)
  if err != nil {
    return s.codeWithVerdict(501)
  }
  s.message.To.PushBack(rcpt)
  s.state = rcptReceived
  return s.codeWithVerdict(250)
}

// Process a RSET command.
func (s *SMTPSession) handleRset(data []byte) Verdict {
  if s.state >= heloReceived {
    s.state = heloReceived
    s.message = NewSMTPMessage(s.remote)
  }
  return s.codeWithVerdict(250)
}

// Process a SEND FROM command.
func (s *SMTPSession) handleSend(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Process a SAML FROM command.
func (s *SMTPSession) handleSaml(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Process a SOML FROM command.
func (s *SMTPSession) handleSoml(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Process a TURN command.
func (s *SMTPSession) handleTurn(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Process a VRFY command.
func (s *SMTPSession) handleVrfy(data []byte) Verdict {
  return s.codeWithVerdict(502)
}

// Respond to client, reporting session termination if there was an error
// writing to the socket.
func (s *SMTPSession) respondWithVerdict(code int, message string) Verdict {
  if err := s.respond(code, message); err != nil {
    log.Error("%s: failed to send response: %v", s.remote, err)
    return Terminate
  }
  return Continue
}

// Respond to client, reporting session termination if there was an error
// writing to the socket.
func (s *SMTPSession) codeWithVerdict(code int) Verdict {
  if err := s.respondCode(code); err != nil {
    log.Error("%s: failed to send response: %v", s.remote, err)
    return Terminate
  }
  return Continue
}

// Write a single-line response to this session.
func (s *SMTPSession) respond(code int, message string) error {
  return s.send(s.responseLine(code, " ", message))
}

// Write a single-line response from the ResponseMap for the given code.
func (s *SMTPSession) respondCode(code int) error {
  return s.send(ResponseMap[code])
}

// Write a multi-line response to this session.
func (s *SMTPSession) respondMulti(code int, messages []string) (err error) {
  for i := range messages {
    sep := "-"
    if i == len(messages) - 1 {
      sep = " "
    }
    err = s.send(s.responseLine(code, sep, messages[i]))
    if err != nil {
      return
    }
  }
  return
}

// Format SMTP response line with code and message.
func (s *SMTPSession) responseLine(code int, sep, message string) []byte {
  return []byte(fmt.Sprintf("%d%s%s\r\n", code, sep, message))
}

// Read in the <CRLF>.<CRLF>-terminated body of an SMTP message submission.
func (s *SMTPSession) readBody() (string, error) {
  // TODO: spill message to disk if its over a certain size
  body := make([]byte, MaxMsgSize)
  pos := 0
  for {
    n, err := s.slurp(body[pos:])
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
func (s *SMTPSession) extractAddress(line []byte) (string, error) {
  start, end := -1, -1
  for i := 0; i < len(line); i++ {
    if line[i] == '<' {
      start = i
    } else if line[i] == '>' {
      end = i
    }
  }
  if start > -1 && end > -1 && end > start {
    return string(line[start+1:end]), nil
  }
  return "", AddressNotFound
}

// Format line for greeting clients at initial connect time.
func (s *SMTPSession) banner() string {
  return fmt.Sprintf("%s %s Service ready at %s",
                     s.service.ServingDomain,
                     s.service.ServerIdent,
                     time.Now().Format(time.RFC1123Z))
}

// Format line for greeting clients in response to HELO/EHLO command.
func (s *SMTPSession) heloLine() string {
  return fmt.Sprintf("%s Hello [%s]", s.service.ServingDomain, s.remote.IP)
}

// Read a single line from the client.
func (s *SMTPSession) readLine() ([]byte, error) {
  if err := s.conn.SetReadDeadline(s.timeout()); err != nil {
    return nil, err
  }
  data, err := s.r.ReadBytes('\n')
  if err != nil {
    s.err("error reading from client", err)
    return nil, err
  }
  return data, nil
}

// Read input from client, up to size of given buffer.
func (s *SMTPSession) slurp(buf []byte) (int, error) {
  if err := s.conn.SetReadDeadline(s.timeout()); err != nil {
    return -1, err
  }
  n, err := s.r.Read(buf)
  if err != nil {
    s.err("error reading from client", err)
    return -1, err
  }
  return n, nil
}

// Send data to client. Returns an error if the write failed to complete in
// MaxIdleSeconds seconds.
func (s *SMTPSession) send(data []byte) (err error) {
  if err = s.conn.SetWriteDeadline(s.timeout()); err != nil {
    return
  }
  _, err = s.conn.Write(data)
  if err != nil {
    s.err("error writing to client", err)
    return err
  }
  return nil
}

// Returns time after which the session should be considered timed out due
// to inactivity.
func (s *SMTPSession) timeout() time.Time {
  return time.Now().Add(time.Second * time.Duration(MaxIdleSeconds))
}

// Log an error for this session.
func (s *SMTPSession) err(message string, err error) {
  log.Warn("%s: %s: %v", s.remote, message, err)
}

