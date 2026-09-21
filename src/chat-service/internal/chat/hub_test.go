package chat

import (
	"strings"
	"testing"
	"time"
)

func newTestSession(callsign string) *Session {
	return &Session{Callsign: callsign, UID: 10000, out: make(chan string, 32)}
}

// recv reads the next queued line for sess, failing the test if none
// arrives within a short deadline.
func recv(t *testing.T, sess *Session) string {
	t.Helper()
	select {
	case line := <-sess.out:
		return line
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: no line received", sess.Callsign)
		return ""
	}
}

func assertNoLine(t *testing.T, sess *Session) {
	t.Helper()
	select {
	case line := <-sess.out:
		t.Fatalf("%s: unexpected line %q", sess.Callsign, line)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestJoinRejectsDuplicateCallsign(t *testing.T) {
	h := NewHub()
	a1 := newTestSession("N0CALL")
	if err := h.Join(a1); err != nil {
		t.Fatalf("first Join: %v", err)
	}

	a2 := newTestSession("N0CALL")
	if err := h.Join(a2); err == nil {
		t.Fatal("second Join with same callsign: want error, got nil")
	}
}

func TestJoinAnnouncesToOthersNotSelf(t *testing.T) {
	h := NewHub()
	alice := newTestSession("ALICE")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join alice: %v", err)
	}
	assertNoLine(t, alice) // alice doesn't see her own join

	bob := newTestSession("BOB")
	if err := h.Join(bob); err != nil {
		t.Fatalf("Join bob: %v", err)
	}

	if got := recv(t, alice); got != "*** BOB has joined" {
		t.Errorf("alice got %q, want join notice for BOB", got)
	}
	assertNoLine(t, bob) // bob doesn't see his own join
}

func TestSendBroadcastsToGeneralExceptSender(t *testing.T) {
	h := NewHub()
	alice, bob := newTestSession("ALICE"), newTestSession("BOB")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join alice: %v", err)
	}
	if err := h.Join(bob); err != nil {
		t.Fatalf("Join bob: %v", err)
	}
	recv(t, alice) // discard bob's join notice

	h.Send(alice, "hello everyone")

	if got := recv(t, bob); got != "ALICE: hello everyone" {
		t.Errorf("bob got %q, want ALICE's broadcast", got)
	}
	assertNoLine(t, alice) // sender doesn't get their own message echoed
}

func TestChatAndLeaveRouting(t *testing.T) {
	h := NewHub()
	alice, bob, carol := newTestSession("ALICE"), newTestSession("BOB"), newTestSession("CAROL")
	for _, s := range []*Session{alice, bob, carol} {
		if err := h.Join(s); err != nil {
			t.Fatalf("Join %s: %v", s.Callsign, err)
		}
	}
	recv(t, alice) // bob's join
	recv(t, alice) // carol's join
	recv(t, bob)   // carol's join

	if err := h.EnterChat(alice, "BOB"); err != nil {
		t.Fatalf("EnterChat: %v", err)
	}
	if got := recv(t, bob); !strings.Contains(got, "ALICE wants to chat with you") {
		t.Fatalf("bob got %q, want invite notice", got)
	}

	// A private message from alice should reach only bob, not carol
	// (who's still in general).
	h.Send(alice, "hi bob")
	if got := recv(t, bob); got != "[private] ALICE: hi bob" {
		t.Errorf("bob got %q, want private message from ALICE", got)
	}
	assertNoLine(t, carol)

	// EnterChat only redirects alice's own target -- bob hasn't
	// reciprocated with his own /chat, so his unprefixed lines still go
	// to general and reach carol.
	h.Send(bob, "hello general")
	assertNoLine(t, alice)
	if got := recv(t, carol); got != "BOB: hello general" {
		t.Errorf("carol got %q, want BOB's general broadcast", got)
	}

	// General chatter from carol shouldn't reach alice (in a private
	// conversation) but should still reach bob (who never left general).
	h.Send(carol, "hello general 2")
	assertNoLine(t, alice)
	if got := recv(t, bob); got != "CAROL: hello general 2" {
		t.Errorf("bob got %q, want CAROL's general broadcast", got)
	}

	h.LeaveChat(alice)
	if !h.InGeneral(alice) {
		t.Error("InGeneral(alice) = false after LeaveChat")
	}

	// Back in general, alice should see carol's broadcasts again.
	h.Send(carol, "hello again")
	if got := recv(t, alice); got != "CAROL: hello again" {
		t.Errorf("alice got %q, want CAROL's general broadcast", got)
	}
}

func TestEnterChatRejectsUnknownTarget(t *testing.T) {
	h := NewHub()
	alice := newTestSession("ALICE")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if err := h.EnterChat(alice, "NOBODY"); err == nil {
		t.Fatal("EnterChat with unknown target: want error, got nil")
	}
	if !h.InGeneral(alice) {
		t.Error("failed EnterChat should leave sess in general")
	}
}

func TestMsgDoesNotChangeCurrentTarget(t *testing.T) {
	h := NewHub()
	alice, bob := newTestSession("ALICE"), newTestSession("BOB")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join alice: %v", err)
	}
	if err := h.Join(bob); err != nil {
		t.Fatalf("Join bob: %v", err)
	}
	recv(t, alice) // bob's join

	if err := h.Msg(alice, "BOB", "one-shot hi"); err != nil {
		t.Fatalf("Msg: %v", err)
	}
	if got := recv(t, bob); got != "[private] ALICE: one-shot hi" {
		t.Errorf("bob got %q, want ALICE's one-shot message", got)
	}
	if !h.InGeneral(alice) {
		t.Error("Msg should not change sender's current target")
	}

	if err := h.Msg(alice, "NOBODY", "hi"); err == nil {
		t.Fatal("Msg to unknown target: want error, got nil")
	}
}

func TestLeaveBouncesPrivateChatPartnerToGeneral(t *testing.T) {
	h := NewHub()
	alice, bob := newTestSession("ALICE"), newTestSession("BOB")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join alice: %v", err)
	}
	if err := h.Join(bob); err != nil {
		t.Fatalf("Join bob: %v", err)
	}
	recv(t, alice) // bob's join

	if err := h.EnterChat(alice, "BOB"); err != nil {
		t.Fatalf("EnterChat: %v", err)
	}

	h.Leave("BOB")

	if got := recv(t, alice); got != "*** BOB has left; returning to general" {
		t.Errorf("alice got %q, want bounce notice", got)
	}
	if !h.InGeneral(alice) {
		t.Error("alice should be back in general after BOB left")
	}
}

func TestBlockRejectsMsgAndChatInviteAndHidesGeneralChatter(t *testing.T) {
	h := NewHub()
	alice, bob := newTestSession("ALICE"), newTestSession("BOB")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join alice: %v", err)
	}
	if err := h.Join(bob); err != nil {
		t.Fatalf("Join bob: %v", err)
	}
	recv(t, alice) // bob's join notice

	h.Block(bob, "ALICE")

	// Msg from alice to bob now fails exactly like bob being offline,
	// and bob never sees it.
	if err := h.Msg(alice, "BOB", "hi"); err == nil {
		t.Fatal("Msg to a user who blocked the sender: want error, got nil")
	}
	assertNoLine(t, bob)

	// A /chat invite from alice to bob is rejected the same way, with no
	// notice delivered to bob -- a blocked user can't tell a block from
	// the target simply being offline.
	if err := h.EnterChat(alice, "BOB"); err == nil {
		t.Fatal("EnterChat targeting a user who blocked the sender: want error, got nil")
	}
	assertNoLine(t, bob)

	// bob shouldn't see alice's general chatter either.
	h.Send(alice, "hello general")
	assertNoLine(t, bob)

	// Blocking is one-directional: alice still sees bob's messages.
	h.Send(bob, "hi alice")
	if got := recv(t, alice); got != "BOB: hi alice" {
		t.Errorf("alice got %q, want BOB's broadcast", got)
	}
}

func TestUnblockRestoresAccess(t *testing.T) {
	h := NewHub()
	alice, bob := newTestSession("ALICE"), newTestSession("BOB")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join alice: %v", err)
	}
	if err := h.Join(bob); err != nil {
		t.Fatalf("Join bob: %v", err)
	}
	recv(t, alice) // bob's join notice

	h.Block(bob, "ALICE")
	if err := h.Msg(alice, "BOB", "hi"); err == nil {
		t.Fatal("Msg while blocked: want error, got nil")
	}

	h.Unblock(bob, "ALICE")
	if err := h.Msg(alice, "BOB", "hi again"); err != nil {
		t.Fatalf("Msg after Unblock: %v", err)
	}
	if got := recv(t, bob); got != "[private] ALICE: hi again" {
		t.Errorf("bob got %q, want ALICE's message after unblock", got)
	}
}

func TestBlockSuppressesLeaveBounceNotice(t *testing.T) {
	h := NewHub()
	alice, bob := newTestSession("ALICE"), newTestSession("BOB")
	if err := h.Join(alice); err != nil {
		t.Fatalf("Join alice: %v", err)
	}
	if err := h.Join(bob); err != nil {
		t.Fatalf("Join bob: %v", err)
	}
	recv(t, alice) // bob's join

	if err := h.EnterChat(alice, "BOB"); err != nil {
		t.Fatalf("EnterChat: %v", err)
	}
	recv(t, bob) // discard alice's invite notice

	h.Block(alice, "BOB")
	h.Leave("BOB")

	// alice blocked bob, so she shouldn't hear that he left, even
	// though her own target was still pointed at him.
	assertNoLine(t, alice)
	if !h.InGeneral(alice) {
		t.Error("alice should still be retargeted to general even though the notice is suppressed")
	}
}

func TestWhoListsSortedCallsigns(t *testing.T) {
	h := NewHub()
	for _, callsign := range []string{"BOB", "ALICE", "CAROL"} {
		if err := h.Join(newTestSession(callsign)); err != nil {
			t.Fatalf("Join %s: %v", callsign, err)
		}
	}

	who := h.Who()
	want := []string{"ALICE", "BOB", "CAROL"}
	if len(who) != len(want) {
		t.Fatalf("Who() = %v, want %v", who, want)
	}
	for i := range want {
		if who[i] != want[i] {
			t.Fatalf("Who() = %v, want %v", who, want)
		}
	}
}
