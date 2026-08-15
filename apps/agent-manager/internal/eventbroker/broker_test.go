package eventbroker

import (
	"testing"
	"time"
)

func TestBrokerPublishesBySessionAndCoalesces(t *testing.T) {
	broker := New()
	first, unsubscribeFirst := broker.Subscribe("session-1")
	defer unsubscribeFirst()
	second, unsubscribeSecond := broker.Subscribe("session-2")
	defer unsubscribeSecond()

	broker.Publish("session-1")
	broker.Publish("session-1")

	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("session-1 subscriber was not notified")
	}
	select {
	case <-first:
		t.Fatal("duplicate notification was not coalesced")
	default:
	}
	select {
	case <-second:
		t.Fatal("different session subscriber was notified")
	default:
	}
}

func TestBrokerUnsubscribeIsIdempotent(t *testing.T) {
	broker := New()
	notifications, unsubscribe := broker.Subscribe("session-1")
	unsubscribe()
	unsubscribe()

	broker.Publish("session-1")
	select {
	case <-notifications:
		t.Fatal("unsubscribed listener was notified")
	default:
	}
}
