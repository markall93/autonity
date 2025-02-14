// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"crypto/ecdsa"
	"math/big"
	"sync"
	"time"

	"github.com/clearmatics/autonity/common"
	"github.com/clearmatics/autonity/consensus/istanbul"
	"github.com/clearmatics/autonity/consensus/istanbul/validator"
	"github.com/clearmatics/autonity/core/rawdb"
	"github.com/clearmatics/autonity/crypto"
	"github.com/clearmatics/autonity/ethdb"
	"github.com/clearmatics/autonity/event"
	elog "github.com/clearmatics/autonity/log"
)

var testLogger = elog.New()

type testSystemBackend struct {
	id  uint64
	sys *testSystem

	engine Engine
	peers  istanbul.ValidatorSet
	events *event.TypeMux

	committedMsgs []testCommittedMsgs
	msgMutex      sync.RWMutex
	sentMsgs      [][]byte // store the message when Send is called by core

	address common.Address
	db      ethdb.Database
}

type testCommittedMsgs struct {
	commitProposal istanbul.Proposal
	committedSeals [][]byte
}

// ==============================================
//
// define the functions that needs to be provided for Istanbul.

func (b *testSystemBackend) Address() common.Address {
	return b.address
}

// Peers returns all connected peers
func (b *testSystemBackend) Validators(number uint64) istanbul.ValidatorSet {
	return b.peers
}

func (b *testSystemBackend) EventMux() *event.TypeMux {
	return b.events
}

func (b *testSystemBackend) LenCommittedMsgs() int {
	b.msgMutex.RLock()
	defer b.msgMutex.RUnlock()
	return len(b.committedMsgs)
}

func (b *testSystemBackend) GetCommittedMsg(i int) testCommittedMsgs {
	b.msgMutex.RLock()
	defer b.msgMutex.RUnlock()
	return b.committedMsgs[i]
}

func (b *testSystemBackend) AddCommittedMsg(msg testCommittedMsgs) []testCommittedMsgs {
	b.msgMutex.Lock()
	defer b.msgMutex.Unlock()
	b.committedMsgs = append(b.committedMsgs, msg)

	return b.committedMsgs
}

func (b *testSystemBackend) Send(message []byte, target common.Address) error {
	testLogger.Info("enqueuing a message...", "address", b.Address())
	b.sentMsgs = append(b.sentMsgs, message)
	b.sys.queuedMessage <- istanbul.MessageEvent{
		Payload: message,
	}
	return nil
}

func (b *testSystemBackend) Broadcast(valSet istanbul.ValidatorSet, message []byte) error {
	testLogger.Info("enqueuing a message...", "address", b.Address())
	b.sentMsgs = append(b.sentMsgs, message)
	b.sys.queuedMessage <- istanbul.MessageEvent{
		Payload: message,
	}
	return nil
}

func (b *testSystemBackend) Gossip(valSet istanbul.ValidatorSet, message []byte) error {
	testLogger.Warn("not sign any data")
	return nil
}

func (b *testSystemBackend) Commit(proposal istanbul.Proposal, seals [][]byte) error {
	testLogger.Info("commit message", "address", b.Address())
	b.AddCommittedMsg(testCommittedMsgs{
		commitProposal: proposal,
		committedSeals: seals,
	})

	// fake new head events
	go func() {
		_ = b.events.Post(istanbul.FinalCommittedEvent{})
	}()
	return nil
}

func (b *testSystemBackend) Verify(proposal istanbul.Proposal) (time.Duration, error) {
	return 0, nil
}

func (b *testSystemBackend) Sign(data []byte) ([]byte, error) {
	testLogger.Warn("not sign any data")
	return data, nil
}

func (b *testSystemBackend) CheckSignature([]byte, common.Address, []byte) error {
	return nil
}

func (b *testSystemBackend) CheckValidatorSignature(data []byte, sig []byte) (common.Address, error) {
	return common.Address{}, nil
}

func (b *testSystemBackend) Hash(_ interface{}) common.Hash {
	return common.BytesToHash([]byte("Test"))
}

func (b *testSystemBackend) NewRequest(request istanbul.Proposal) {
	go func() {
		_ = b.events.Post(istanbul.RequestEvent{
			Proposal: request,
		})
	}()
}

func (b *testSystemBackend) HasBadProposal(hash common.Hash) bool {
	return false
}

func (b *testSystemBackend) LastProposal() (istanbul.Proposal, common.Address) {
	l := b.LenCommittedMsgs()
	if l > 0 {
		return b.GetCommittedMsg(l - 1).commitProposal, common.Address{}
	}
	return makeBlock(0), common.Address{}
}

// Only block height 5 will return true
func (b *testSystemBackend) HasPropsal(hash common.Hash, number *big.Int) bool {
	return number.Cmp(big.NewInt(5)) == 0
}

func (b *testSystemBackend) GetProposer(number uint64) common.Address {
	return common.Address{}
}

func (b *testSystemBackend) ParentValidators(proposal istanbul.Proposal) istanbul.ValidatorSet {
	return b.peers
}

func (b *testSystemBackend) SetProposedBlockHash(hash common.Hash) {
	return
}

// ==============================================
//
// define the struct that need to be provided for integration tests.

type testSystem struct {
	backends []*testSystemBackend

	queuedMessage chan istanbul.MessageEvent
	quit          chan struct{}
}

func newTestSystem(n uint64) *testSystem {
	testLogger.SetHandler(elog.StdoutHandler)
	return &testSystem{
		backends: make([]*testSystemBackend, n),

		queuedMessage: make(chan istanbul.MessageEvent),
		quit:          make(chan struct{}),
	}
}

func generateValidators(n int) []common.Address {
	vals := make([]common.Address, 0)
	for i := 0; i < n; i++ {
		privateKey, _ := crypto.GenerateKey()
		vals = append(vals, crypto.PubkeyToAddress(privateKey.PublicKey))
	}
	return vals
}

func newTestValidatorSet(n int) istanbul.ValidatorSet {
	return validator.NewSet(generateValidators(n), istanbul.RoundRobin)
}

// FIXME: int64 is needed for N and F
func NewTestSystemWithBackend(n, f uint64) *testSystem {
	testLogger.SetHandler(elog.StdoutHandler)

	addrs := generateValidators(int(n))
	sys := newTestSystem(n)
	config := istanbul.DefaultConfig

	for i := uint64(0); i < n; i++ {
		vset := validator.NewSet(addrs, istanbul.RoundRobin)
		backend := sys.NewBackend(i)
		backend.peers = vset
		backend.address = vset.GetByIndex(i).Address()

		core := New(backend, config).(*core)
		core.state = StateAcceptRequest
		core.current = newRoundState(&istanbul.View{
			Round:    big.NewInt(0),
			Sequence: big.NewInt(1),
		}, vset, common.Hash{}, nil, nil, func(hash common.Hash) bool {
			return false
		})
		core.valSet = vset
		core.logger = testLogger
		core.validateFn = backend.CheckValidatorSignature

		backend.engine = core
	}

	return sys
}

// listen will consume messages from queue and deliver a message to core
func (t *testSystem) listen() {
	for {
		select {
		case <-t.quit:
			return
		case queuedMessage := <-t.queuedMessage:
			testLogger.Info("consuming a queue message...")
			for _, backend := range t.backends {
				go backend.EventMux().Post(queuedMessage)
			}
		}
	}
}

// Run will start system components based on given flag, and returns a closer
// function that caller can control lifecycle
//
// Given a true for core if you want to initialize core engine.
func (t *testSystem) Run(core bool) func() {
	for _, b := range t.backends {
		if core {
			b.engine.Start() // start Istanbul core
		}
	}

	go t.listen()
	closer := func() { t.stop(core) }
	return closer
}

func (t *testSystem) stop(core bool) {
	close(t.quit)

	for _, b := range t.backends {
		if core {
			b.engine.Stop()
		}
	}
}

func (t *testSystem) NewBackend(id uint64) *testSystemBackend {
	// assume always success
	ethDB := rawdb.NewMemoryDatabase()
	backend := &testSystemBackend{
		id:     id,
		sys:    t,
		events: new(event.TypeMux),
		db:     ethDB,
	}

	t.backends[id] = backend
	return backend
}

// ==============================================
//
// helper functions.

func getPublicKeyAddress(privateKey *ecdsa.PrivateKey) common.Address {
	return crypto.PubkeyToAddress(privateKey.PublicKey)
}
