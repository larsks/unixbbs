#!/bin/sh
# Line-oriented chat client. See DESIGN.md §9.1.
#
# This is deliberately just socat dialing the socket directly from the
# already-unprivileged login shell -- the connection's peer credentials
# are this session's own uid, which is chat-service's entire identity
# mechanism (no login step, no client-supplied handle). Unlike the mail
# relay (§2.2) or presence registration (§4.2), no TCP-loopback shim and
# no entrypoint change are needed: chat's client just is socat, so
# there's no protocol gap to work around.
#
# Deliberately *not* "raw" stdio: the controlling terminal's own
# existing canonical mode (line editing, local echo) is left alone, so
# chat-service never has to implement echo/backspace handling itself --
# it only ever sees complete lines, same as everything else here.
exec socat - UNIX-CONNECT:/bbs-sock/chat/chat.sock
