package transaction

import (
	"testing"
	"time"
)

func TestInviteClientTransaction(t *testing.T) {
	transaction := NewClient("INVITE", true)

	if transaction.State() != Calling {
		t.Fatalf("initial state = %q", transaction.State())
	}

	if interval, ok := transaction.RetransmissionInterval(); !ok || interval != 500*time.Millisecond {
		t.Fatalf("initial retransmission = %v, %t", interval, ok)
	}

	transaction.Retransmitted()
	transaction.Receive(180)

	if transaction.State() != Proceeding {
		t.Fatalf("proceeding state = %q", transaction.State())
	}

	if _, ok := transaction.RetransmissionInterval(); ok {
		t.Fatal("INVITE retransmission continued after provisional response")
	}

	if duplicate := transaction.Receive(200); duplicate {
		t.Fatal("first final response reported as duplicate")
	}

	if duplicate := transaction.Receive(200); !duplicate {
		t.Fatal("repeated final response was not reported as duplicate")
	}
}

func TestNonInviteRetransmitsWhileProceeding(t *testing.T) {
	transaction := NewClient("OPTIONS", true)
	transaction.Receive(100)

	if transaction.State() != Proceeding {
		t.Fatalf("state = %q", transaction.State())
	}

	for index := 0; index < 5; index++ {
		transaction.Retransmitted()
	}

	if interval, ok := transaction.RetransmissionInterval(); !ok || interval != 4*time.Second {
		t.Fatalf("capped retransmission = %v, %t", interval, ok)
	}
}

func TestServerInviteTransaction(t *testing.T) {
	transaction := NewServer("INVITE")
	transaction.Send(180)

	if transaction.State() != Proceeding {
		t.Fatalf("proceeding state = %q", transaction.State())
	}

	transaction.Send(486)
	transaction.Acknowledge()

	if transaction.State() != Confirmed {
		t.Fatalf("confirmed state = %q", transaction.State())
	}

	transaction.Terminate()

	if transaction.State() != Terminated {
		t.Fatalf("terminated state = %q", transaction.State())
	}
}
