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
	"context"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/clearmatics/autonity/common"
	"github.com/clearmatics/autonity/consensus"
	"github.com/clearmatics/autonity/consensus/tendermint/crypto"
	"github.com/clearmatics/autonity/consensus/tendermint/events"
	"github.com/clearmatics/autonity/consensus/tendermint/validator"
	"github.com/clearmatics/autonity/core/types"
)

// Start implements core.Engine.Start
func (c *core) Start(ctx context.Context, chain consensus.ChainReader, currentBlock func() *types.Block, hasBadBlock func(hash common.Hash) bool) error {
	// prevent double start
	if atomic.LoadUint32(c.isStarted) == 1 {
		return nil
	}
	if !atomic.CompareAndSwapUint32(c.isStarting, 0, 1) {
		return nil
	}
	defer func() {
		atomic.StoreUint32(c.isStarting, 0)
		atomic.StoreUint32(c.isStopped, 0)
		atomic.StoreUint32(c.isStarted, 1)
	}()

	ctx, c.cancel = context.WithCancel(ctx)

	err := c.backend.Start(ctx, chain, currentBlock, hasBadBlock)
	if err != nil {
		return err
	}

	c.subscribeEvents()

	// set currentRoundState before starting go routines
	lastCommittedProposalBlock, _ := c.backend.LastCommittedProposal()
	height := new(big.Int).Add(lastCommittedProposalBlock.Number(), common.Big1)
	c.currentRoundState.Update(big.NewInt(0), height)

	//We need a separate go routine to keep c.latestPendingUnminedBlock up to date
	go c.handleNewUnminedBlockEvent(ctx)

	//We want to sequentially handle all the event which modify the current consensus state
	go c.handleConsensusEvents(ctx)

	go c.backend.HandleUnhandledMsgs(ctx)

	return nil
}

// Stop implements core.Engine.Stop
func (c *core) Stop() error {
	// prevent double stop
	if atomic.LoadUint32(c.isStopped) == 1 {
		return nil
	}
	if !atomic.CompareAndSwapUint32(c.isStopping, 0, 1) {
		return nil
	}
	defer func() {
		atomic.StoreUint32(c.isStopping, 0)
		atomic.StoreUint32(c.isStopped, 1)
		atomic.StoreUint32(c.isStarted, 0)
	}()

	c.logger.Info("stopping tendermint.core", "addr", c.address.String())

	_ = c.proposeTimeout.stopTimer()
	_ = c.prevoteTimeout.stopTimer()
	_ = c.precommitTimeout.stopTimer()

	c.cancel()

	c.stopFutureProposalTimer()
	c.unsubscribeEvents()

	<-c.stopped
	<-c.stopped

	err := c.backend.Close()
	if err != nil {
		return err
	}

	return nil
}

func (c *core) subscribeEvents() {
	s := c.backend.Subscribe(events.MessageEvent{}, backlogEvent{})
	c.messageEventSub = s

	s1 := c.backend.Subscribe(events.NewUnminedBlockEvent{})
	c.newUnminedBlockEventSub = s1

	s2 := c.backend.Subscribe(TimeoutEvent{})
	c.timeoutEventSub = s2

	s3 := c.backend.Subscribe(events.CommitEvent{})
	c.committedSub = s3

	s4 := c.backend.Subscribe(events.SyncEvent{})
	c.syncEventSub = s4
}

// Unsubscribe all messageEventSub
func (c *core) unsubscribeEvents() {
	c.messageEventSub.Unsubscribe()
	c.newUnminedBlockEventSub.Unsubscribe()
	c.timeoutEventSub.Unsubscribe()
	c.committedSub.Unsubscribe()
	c.syncEventSub.Unsubscribe()
}

// TODO: update all of the TypeMuxSilent to event.Feed and should not use backend.EventMux for core internal messageEventSub: backlogEvent, TimeoutEvent

func (c *core) handleNewUnminedBlockEvent(ctx context.Context) {
eventLoop:
	for {
		select {
		case e, ok := <-c.newUnminedBlockEventSub.Chan():
			if !ok {
				break eventLoop
			}
			newUnminedBlockEvent := e.Data.(events.NewUnminedBlockEvent)
			pb := &newUnminedBlockEvent.NewUnminedBlock
			c.storeUnminedBlockMsg(pb)
		case <-ctx.Done():
			c.logger.Info("handleNewUnminedBlockEvent is stopped", "event", ctx.Err())
			break eventLoop
		}
	}

	c.stopped <- struct{}{}
}

func (c *core) handleConsensusEvents(ctx context.Context) {
	// Start a new round from last height + 1
	c.startRound(ctx, common.Big0)

	go c.syncLoop(ctx)

eventLoop:
	for {
		select {
		case ev, ok := <-c.messageEventSub.Chan():
			if !ok {
				break eventLoop
			}
			// A real ev arrived, process interesting content
			switch e := ev.Data.(type) {
			case events.MessageEvent:
				if len(e.Payload) == 0 {
					c.logger.Error("core.handleConsensusEvents Get message(MessageEvent) empty payload")
				}

				if err := c.handleMsg(ctx, e.Payload); err != nil {
					c.logger.Debug("core.handleConsensusEvents Get message(MessageEvent) payload failed", "err", err)
					continue
				}
				c.backend.Gossip(ctx, c.valSet.Copy(), e.Payload)
			case backlogEvent:
				// No need to check signature for internal messages
				c.logger.Debug("Started handling backlogEvent")
				err := c.handleCheckedMsg(ctx, e.msg, e.src)
				if err != nil {
					c.logger.Debug("core.handleConsensusEvents handleCheckedMsg message failed", "err", err)
					continue
				}

				p, err := e.msg.Payload()
				if err != nil {
					c.logger.Debug("core.handleConsensusEvents Get message payload failed", "err", err)
					continue
				}

				c.backend.Gossip(ctx, c.valSet.Copy(), p)
			}
		case ev, ok := <-c.timeoutEventSub.Chan():
			if !ok {
				break eventLoop
			}
			if timeoutE, ok := ev.Data.(TimeoutEvent); ok {
				switch timeoutE.step {
				case msgProposal:
					c.handleTimeoutPropose(ctx, timeoutE)
				case msgPrevote:
					c.handleTimeoutPrevote(ctx, timeoutE)
				case msgPrecommit:
					c.handleTimeoutPrecommit(ctx, timeoutE)
				}
			}
		case ev, ok := <-c.committedSub.Chan():
			if !ok {
				break eventLoop
			}
			switch ev.Data.(type) {
			case events.CommitEvent:
				c.handleCommit(ctx)
			}
		case <-ctx.Done():
			c.logger.Info("handleConsensusEvents is stopped", "event", ctx.Err())
			break eventLoop
		}
	}

	c.stopped <- struct{}{}
}

func (c *core) syncLoop(ctx context.Context) {
	/*
		this method is responsible for asking the network to send us the current consensus state
		and to process sync queries events.
	*/
	timer := time.NewTimer(10 * time.Second)

	round := c.currentRoundState.Round()
	height := c.currentRoundState.Height()

	// Ask for sync when the engine starts
	c.backend.AskSync(c.valSet.Copy())

	for {
		select {
		case <-timer.C:
			currentRound := c.currentRoundState.Round()
			currentHeight := c.currentRoundState.Height()

			// we only ask for sync if the current view stayed the same for the past 10 seconds
			if currentHeight.Cmp(height) == 0 && currentRound.Cmp(round) == 0 {
				c.backend.AskSync(c.valSet.Copy())
			}
			round = currentRound
			height = currentHeight
			timer = time.NewTimer(10 * time.Second)
		case ev, ok := <-c.syncEventSub.Chan():
			if !ok {
				return
			}
			event := ev.Data.(events.SyncEvent)
			c.logger.Info("Processing sync message", "from", event.Addr)
			c.SyncPeer(event.Addr)
		case <-ctx.Done():
			return
		}
	}
}

// sendEvent sends event to mux
func (c *core) sendEvent(ev interface{}) {
	c.backend.Post(ev)
}

func (c *core) handleMsg(ctx context.Context, payload []byte) error {
	logger := c.logger.New()

	// Decode message and check its signature
	msg := new(Message)

	sender, err := msg.FromPayload(payload, c.valSet.Copy(), crypto.CheckValidatorSignature)
	if err != nil {
		logger.Error("Failed to decode message from payload", "err", err)
		return err
	}

	return c.handleCheckedMsg(ctx, msg, *sender)
}

func (c *core) handleCheckedMsg(ctx context.Context, msg *Message, sender validator.Validator) error {
	logger := c.logger.New("address", c.address, "from", sender)

	// Store the message if it's a future message
	testBacklog := func(err error) error {
		// We want to store only future messages in backlog
		if err == errFutureHeightMessage {
			logger.Debug("Storing future height message in backlog")
			c.storeBacklog(msg, sender)
		} else if err == errFutureRoundMessage {
			logger.Debug("Storing future round message in backlog")
			c.storeBacklog(msg, sender)
			//We cannot move to a round in a new height without receiving a new block
			var msgRound int64
			if msg.Code == msgProposal {
				var p Proposal
				if e := msg.Decode(&p); e != nil {
					return errFailedDecodeProposal
				}
				msgRound = p.Round.Int64()

			} else {
				var v Vote
				if e := msg.Decode(&v); e != nil {
					return errFailedDecodeVote
				}
				msgRound = v.Round.Int64()
			}

			c.futureRoundsChange[msgRound] = c.futureRoundsChange[msgRound] + 1
			totalFutureRoundMessages := c.futureRoundsChange[msgRound]

			if totalFutureRoundMessages > int64(c.valSet.F()) {
				logger.Debug("Received ceil(N/3) - 1 messages for higher round", "New round", msgRound)
				c.startRound(ctx, big.NewInt(msgRound))
			}
		} else if err == errFutureStepMessage {
			logger.Debug("Storing future step message in backlog")
			c.storeBacklog(msg, sender)
		}

		return err
	}

	switch msg.Code {
	case msgProposal:
		logger.Debug("tendermint.MessageEvent: PROPOSAL")
		return testBacklog(c.handleProposal(ctx, msg))
	case msgPrevote:
		logger.Debug("tendermint.MessageEvent: PREVOTE")
		return testBacklog(c.handlePrevote(ctx, msg))
	case msgPrecommit:
		logger.Debug("tendermint.MessageEvent: PRECOMMIT")
		return testBacklog(c.handlePrecommit(ctx, msg))
	default:
		logger.Error("Invalid message", "msg", msg)
	}

	return errInvalidMessage
}
