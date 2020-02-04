package pm

import (
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func defaultSignedTicket(senderNonce uint32) *SignedTicket {
	return &SignedTicket{
		&Ticket{FaceValue: big.NewInt(50), SenderNonce: senderNonce, ParamsExpirationBlock: big.NewInt(0)},
		[]byte("foo"),
		big.NewInt(7),
	}
}

type queueConsumer struct {
	redeemable []*SignedTicket
	mu         sync.Mutex
}

// Redeemable returns the consumed redeemable tickets from a ticket queue
func (qc *queueConsumer) Redeemable() []*SignedTicket {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	return qc.redeemable
}

// Wait receives on the output channel from a ticket queue
// until it has received a certain number of tickets and then exits
func (qc *queueConsumer) Wait(num int, e RedeemableEmitter) {
	count := 0

	for count < num {
		select {
		case ticket := <-e.Redeemable():
			count++

			qc.mu.Lock()
			qc.redeemable = append(qc.redeemable, ticket)
			qc.mu.Unlock()
		}
	}
}

func TestTicketQueueLoop(t *testing.T) {
	assert := assert.New(t)

	tm := &stubTimeManager{}

	q := newTicketQueue(tm.SubscribeBlocks)
	q.Start()
	defer q.Stop()

	// Test adding tickets

	numTickets := 10

	for i := 0; i < numTickets; i++ {
		q.Add(defaultSignedTicket(uint32(i)))
	}

	assert.Equal(int32(numTickets), q.Length())

	// Length should not change since signaled max float is insufficient
	time.Sleep(time.Millisecond * 20)
	assert.Equal(int32(numTickets), q.Length())

	qc := &queueConsumer{}

	// Wait for all numTickets tickets to be
	// received on the output channel returned by Redeemable()
	go qc.Wait(numTickets, q)

	// Test signaling a new blockNum and remove tickets
	tm.blockNumSink <- big.NewInt(1)

	// Queue should be empty now
	time.Sleep(time.Second * 2)
	assert.Equal(int32(0), q.Length())

	// The popped tickets should be in the same order
	// that they were added i.e. since we added them
	// synchronously with sender nonces 0..9 the array
	// of popped tickets should have sender nonces 0..9
	// in order
	redeemable := qc.Redeemable()
	for i := 0; i < numTickets; i++ {
		assert.Equal(uint32(i), redeemable[i].SenderNonce)
	}
}
