package common

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

type StubRoundsManager struct {
	Round *big.Int
	Sub   *stubSubscription
	Sink  chan<- types.Log
}

func (s *StubRoundsManager) LastInitializedRound() *big.Int { return s.Round }

func (s *StubRoundsManager) Subscribe(sink chan<- types.Log) event.Subscription {
	s.Sink = sink
	s.Sub = &stubSubscription{errCh: make(<-chan error)}
	return s.Sub
}

type stubSubscription struct {
	errCh        <-chan error
	unsubscribed bool
}

func (s *stubSubscription) Unsubscribe() {
	s.unsubscribed = true
}

func (s *stubSubscription) Err() <-chan error {
	return s.errCh
}
