package chat

import (
	"bufio"
	"fmt"
	"net"
	"strings"
)

const helpText = `Chat commands:
  /who               list users currently connected to chat
  /msg <user> <text> send a one-shot private message to <user>
  /chat <user>       ask <user> to start a private conversation with you --
                     your own lines go to them from now on; they need to
                     /chat you back for their replies to reach you too
  /leave             return to general from a private conversation
  /block <user>      stop hearing from <user>: chat invites, messages, and
                     general chatter
  /unblock <user>    reverse a previous /block
  /quit              exit chat
  /help              show this list

Anything else you type goes to your current conversation: general by
default, or whoever you last entered with /chat.`

// Handle runs one chat-service connection to completion: it joins a
// session to hub under callsign, then reads lines from conn until EOF,
// an error, or /quit, dispatching each line as a command (if it starts
// with "/") or a message to the session's current target. See
// DESIGN.md §9.2 for the protocol.
func Handle(conn net.Conn, callsign string, uid int64, hub *Hub) {
	sess := &Session{Callsign: callsign, UID: uid, out: make(chan string, 32)}

	if err := hub.Join(sess); err != nil {
		fmt.Fprintln(conn, err)
		return
	}
	defer hub.Leave(callsign)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for line := range sess.out {
			if _, err := fmt.Fprintln(conn, line); err != nil {
				return
			}
		}
	}()
	defer func() {
		close(sess.out)
		<-writerDone
	}()

	sess.send("*** welcome to general chat -- /help for a list of commands")

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		if dispatch(hub, sess, scanner.Text()) {
			break
		}
	}
}

// dispatch handles one line of client input, returning true if the
// session should be closed (i.e. /quit was received).
func dispatch(hub *Hub, sess *Session, line string) (quit bool) {
	// The leading "/" is reserved with no escape mechanism for a
	// literal one -- see DESIGN.md §9.2 for why this is a deliberate
	// simplicity trade-off over an escaping rule.
	if !strings.HasPrefix(line, "/") {
		if line != "" {
			hub.Send(sess, line)
		}
		return false
	}

	fields := strings.Fields(line)
	cmd, args := fields[0], fields[1:]

	switch cmd {
	case "/who":
		sess.send(strings.Join(hub.Who(), " "))

	case "/msg":
		if len(args) < 2 {
			sess.send("usage: /msg <user> <text>")
			return false
		}
		target := strings.ToUpper(args[0])
		text := strings.Join(args[1:], " ")
		if err := hub.Msg(sess, target, text); err != nil {
			sess.send(err.Error())
		}

	case "/chat":
		if len(args) != 1 {
			sess.send("usage: /chat <user>")
			return false
		}
		target := strings.ToUpper(args[0])
		if err := hub.EnterChat(sess, target); err != nil {
			sess.send(err.Error())
			return false
		}
		sess.send(fmt.Sprintf("*** now sending to %s -- /leave to return to general", target))

	case "/leave":
		if hub.InGeneral(sess) {
			sess.send("*** you are already in general")
			return false
		}
		hub.LeaveChat(sess)
		sess.send("*** back in general")

	case "/block":
		if len(args) != 1 {
			sess.send("usage: /block <user>")
			return false
		}
		target := strings.ToUpper(args[0])
		hub.Block(sess, target)
		sess.send(fmt.Sprintf("*** blocking %s", target))

	case "/unblock":
		if len(args) != 1 {
			sess.send("usage: /unblock <user>")
			return false
		}
		target := strings.ToUpper(args[0])
		hub.Unblock(sess, target)
		sess.send(fmt.Sprintf("*** unblocked %s", target))

	case "/quit":
		sess.send("*** goodbye")
		return true

	case "/help":
		sess.send(helpText)

	default:
		sess.send(fmt.Sprintf("no such command: %s", cmd))
	}

	return false
}
