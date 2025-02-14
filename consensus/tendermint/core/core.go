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
	"bytes"
	"context"
	"errors"
	"math"
	"math/big"
	"sync"
	"time"

	"github.com/clearmatics/autonity/common"
	"github.com/clearmatics/autonity/consensus/tendermint/config"
	"github.com/clearmatics/autonity/consensus/tendermint/validator"
	"github.com/clearmatics/autonity/core/types"
	"github.com/clearmatics/autonity/event"
	"github.com/clearmatics/autonity/log"
	"gopkg.in/karalabe/cookiejar.v2/collections/prque"
)

var (
	// errNotFromProposer is returned when received message is supposed to be from
	// proposer.
	errNotFromProposer = errors.New("message does not come from proposer")
	// errFutureHeightMessage is returned when currentRoundState view is earlier than the
	// view of the received message.
	errFutureHeightMessage = errors.New("future height message")
	// errOldHeightMessage is returned when the received message's view is earlier
	// than currentRoundState view.
	errOldHeightMessage = errors.New("old height message")
	// errOldRoundMessage message is returned when message is of the same Height but form a smaller round
	errOldRoundMessage = errors.New("same height but old round message")
	// errFutureRoundMessage message is returned when message is of the same Height but form a newer round
	errFutureRoundMessage = errors.New("same height but future round message")
	// errFutureStepMessage message is returned when it's a prevote or precommit message of the same Height same round
	// while the current step is propose.
	errFutureStepMessage = errors.New("same round but future step message")
	// errInvalidMessage is returned when the message is malformed.
	errInvalidMessage = errors.New("invalid message")
	// errInvalidSenderOfCommittedSeal is returned when the committed seal is not from the sender of the message.
	errInvalidSenderOfCommittedSeal = errors.New("invalid sender of committed seal")
	// errFailedDecodeProposal is returned when the PROPOSAL message is malformed.
	errFailedDecodeProposal = errors.New("failed to decode PROPOSAL")
	// errFailedDecodePrevote is returned when the PREVOTE message is malformed.
	errFailedDecodePrevote = errors.New("failed to decode PREVOTE")
	// errFailedDecodePrecommit is returned when the PRECOMMIT message is malformed.
	errFailedDecodePrecommit = errors.New("failed to decode PRECOMMIT")
	// errFailedDecodeVote is returned for when PREVOTE or PRECOMMIT is malformed.
	errFailedDecodeVote = errors.New("failed to decode vote")
	// errNilPrevoteSent is returned when timer could be stopped in time
	errNilPrevoteSent = errors.New("timer expired and nil prevote sent")
	// errNilPrecommitSent is returned when timer could be stopped in time
	errNilPrecommitSent = errors.New("timer expired and nil precommit sent")
	// errMovedToNewRound is returned when timer could be stopped in time
	errMovedToNewRound = errors.New("timer expired and new round started")
)

// New creates an Tendermint consensus core
func New(backend Backend, config *config.Config) *core {
	logger := log.New("addr", backend.Address().String())
	return &core{
		config:                       config,
		address:                      backend.Address(),
		logger:                       logger,
		backend:                      backend,
		backlogs:                     make(map[validator.Validator]*prque.Prque),
		pendingUnminedBlocks:         make(map[uint64]*types.Block),
		pendingUnminedBlockCh:        make(chan *types.Block),
		stopped:                      make(chan struct{}, 3),
		isStarting:                   new(uint32),
		isStarted:                    new(uint32),
		isStopping:                   new(uint32),
		isStopped:                    new(uint32),
		valSet:                       new(validatorSet),
		futureRoundsChange:           make(map[int64]int64),
		currentHeightOldRoundsStates: make(map[int64]*roundState),
		lockedRound:                  big.NewInt(-1),
		validRound:                   big.NewInt(-1),
		currentRoundState:            new(roundState),
		proposeTimeout:               newTimeout(propose, logger),
		prevoteTimeout:               newTimeout(prevote, logger),
		precommitTimeout:             newTimeout(precommit, logger),
	}
}

type core struct {
	config  *config.Config
	address common.Address
	logger  log.Logger

	backend Backend
	cancel  context.CancelFunc

	messageEventSub         *event.TypeMuxSubscription
	newUnminedBlockEventSub *event.TypeMuxSubscription
	committedSub            *event.TypeMuxSubscription
	timeoutEventSub         *event.TypeMuxSubscription
	syncEventSub            *event.TypeMuxSubscription
	futureProposalTimer     *time.Timer
	stopped                 chan struct{}
	isStarted               *uint32
	isStarting              *uint32
	isStopping              *uint32
	isStopped               *uint32

	valSet *validatorSet

	backlogs   map[validator.Validator]*prque.Prque
	backlogsMu sync.Mutex

	currentRoundState *roundState

	// map[Height]UnminedBlock
	pendingUnminedBlocks     map[uint64]*types.Block
	pendingUnminedBlocksMu   sync.Mutex
	pendingUnminedBlockCh    chan *types.Block
	isWaitingForUnminedBlock bool

	sentProposal          bool
	sentPrevote           bool
	sentPrecommit         bool
	setValidRoundAndValue bool

	lockedRound *big.Int
	validRound  *big.Int
	lockedValue *types.Block
	validValue  *types.Block

	currentHeightOldRoundsStates   map[int64]*roundState
	currentHeightOldRoundsStatesMu sync.RWMutex

	proposeTimeout   *timeout
	prevoteTimeout   *timeout
	precommitTimeout *timeout

	//map[futureRoundNumber]NumberOfMessagesReceivedForTheRound
	futureRoundsChange map[int64]int64
}

func (c *core) GetCurrentHeightMessages() []*Message {
	c.currentHeightOldRoundsStatesMu.RLock()
	defer c.currentHeightOldRoundsStatesMu.RUnlock()

	msgs := make([][]*Message, len(c.currentHeightOldRoundsStates)+1)
	var totalLen int
	for i, state := range c.currentHeightOldRoundsStates {
		msgs[i] = state.GetMessages()
		totalLen += len(msgs[i])
	}
	msgs[len(msgs)-1] = c.currentRoundState.GetMessages()

	totalLen += len(msgs[len(msgs)-1])

	result := make([]*Message, 0, totalLen)
	for _, ms := range msgs {
		result = append(result, ms...)
	}

	return result
}

func (c *core) IsValidator(address common.Address) bool {
	_, val := c.valSet.GetByAddress(address)
	return val != nil
}

func (c *core) finalizeMessage(msg *Message) ([]byte, error) {
	var err error

	// Sign message
	data, err := msg.PayloadNoSig()
	if err != nil {
		return nil, err
	}
	msg.Signature, err = c.backend.Sign(data)
	if err != nil {
		return nil, err
	}

	// Convert to payload
	payload, err := msg.Payload()
	if err != nil {
		return nil, err
	}

	return payload, nil
}

func (c *core) broadcast(ctx context.Context, msg *Message) {
	logger := c.logger.New("step", c.currentRoundState.Step())

	payload, err := c.finalizeMessage(msg)
	if err != nil {
		logger.Error("Failed to finalize message", "msg", msg, "err", err)
		return
	}

	// Broadcast payload
	logger.Debug("broadcasting", "msg", msg.String())
	if err = c.backend.Broadcast(ctx, c.valSet.Copy(), payload); err != nil {
		logger.Error("Failed to broadcast message", "msg", msg, "err", err)
		return
	}
}

func (c *core) isProposer() bool {
	return c.valSet.IsProposer(c.address)
}

func (c *core) commit() {
	c.setStep(precommitDone)

	proposal := c.currentRoundState.Proposal()

	if proposal != nil {
		if proposal.ProposalBlock != nil {
			c.logger.Warn("commit a block", "hash", proposal.ProposalBlock.Header().Hash())
		} else {
			c.logger.Error("commit a NIL block",
				"block", proposal.ProposalBlock,
				"height", c.currentRoundState.height.String(),
				"round", c.currentRoundState.round.String())
		}

		committedSeals := make([][]byte, c.currentRoundState.Precommits.VotesSize(proposal.ProposalBlock.Hash()))
		for i, v := range c.currentRoundState.Precommits.Values(proposal.ProposalBlock.Hash()) {
			committedSeals[i] = make([]byte, types.BFTExtraSeal)
			copy(committedSeals[i][:], v.CommittedSeal[:])
		}

		if err := c.backend.Commit(*proposal.ProposalBlock, committedSeals); err != nil {
			c.logger.Error("Failed to Commit block", "err", err)
			return
		}
	}
}

// Metric collecton of round change and height change.
func (c *core) measureHeightRoundMetrics(round *big.Int) {
	if round.Cmp(common.Big0) == 0 {
		// in case of height change, round changed too, so count it also.
		tendermintRoundChangeMeter.Mark(1)
		tendermintHeightChangeMeter.Mark(1)
	} else {
		tendermintRoundChangeMeter.Mark(1)
	}
}

// startRound starts a new round. if round equals to 0, it means to starts a new height
func (c *core) startRound(ctx context.Context, round *big.Int) {

	c.measureHeightRoundMetrics(round)
	lastCommittedProposalBlock, lastCommittedProposalBlockProposer := c.backend.LastCommittedProposal()
	height := new(big.Int).Add(lastCommittedProposalBlock.Number(), common.Big1)

	c.setCore(round, height, lastCommittedProposalBlockProposer)

	// c.setStep(propose) will process the pending unmined blocks sent by the backed.Seal() and set c.lastestPendingRequest
	c.setStep(propose)

	c.logger.Debug("Starting new Round", "Height", height, "Round", round)

	// If the node is the proposer for this round then it would propose validValue or a new block, otherwise,
	// proposeTimeout is started, where the node waits for a proposal from the proposer of the current round.
	if c.isProposer() {
		// validValue and validRound represent a block they received a quorum of prevote and the round quorum was
		// received, respectively. If the block is not committed in that round then the round is changed.
		// The new proposer will chose the validValue, if present, which was set in one of the previous rounds otherwise
		// they propose a new block.
		var p *types.Block
		if c.validValue != nil {
			p = c.validValue
		} else {
			p = c.getUnminedBlock()
			if p == nil {
				select {
				case <-ctx.Done():
					return
				case p = <-c.pendingUnminedBlockCh:
				}
			}
		}
		c.sendProposal(ctx, p)
	} else {
		timeoutDuration := timeoutPropose(round.Int64())
		c.proposeTimeout.scheduleTimeout(timeoutDuration, round.Int64(), height.Int64(), c.onTimeoutPropose)
		c.logger.Debug("Scheduled Propose Timeout", "Timeout Duration", timeoutDuration)
	}
}

func (c *core) setCore(r *big.Int, h *big.Int, lastProposer common.Address) {
	// Start of new height where round is 0
	if r.Int64() == 0 {
		// Set the shared round values to initial values
		c.lockedRound = big.NewInt(-1)
		c.lockedValue = nil
		c.validRound = big.NewInt(-1)
		c.validValue = nil

		// Set validator set for height
		valSet := c.backend.Validators(h.Uint64())
		c.valSet.set(valSet)

		// Assuming that round == 0 only when the node moves to a new height
		// Therefore, resetting round related maps
		c.currentHeightOldRoundsStatesMu.Lock()
		c.currentHeightOldRoundsStates = make(map[int64]*roundState)
		c.currentHeightOldRoundsStatesMu.Unlock()
		c.futureRoundsChange = make(map[int64]int64)
	}
	// Reset all timeouts
	c.proposeTimeout.reset(propose)
	c.prevoteTimeout.reset(prevote)
	c.precommitTimeout.reset(precommit)

	// Get all rounds from c.futureRoundsChange and remove previous rounds
	var i int64
	for i = 0; i <= r.Int64(); i++ {
		if _, ok := c.futureRoundsChange[i]; ok {
			delete(c.futureRoundsChange, i)
		}
	}
	// Add a copy of c.currentRoundState to c.currentHeightOldRoundsStates and then update c.currentRoundState
	// We only add old round prevote messages to c.currentHeightOldRoundsStates, while future messages are sent to the
	// backlog which are processed when the step is set to propose
	if r.Int64() > 0 {
		// This is a shallow copy, should be fine for now
		c.currentHeightOldRoundsStatesMu.Lock()
		c.currentHeightOldRoundsStates[r.Int64()-1] = c.currentRoundState
		c.currentHeightOldRoundsStatesMu.Unlock()
	}
	c.currentRoundState.Update(r, h)

	// Calculate new proposer
	c.valSet.CalcProposer(lastProposer, r.Uint64())
	c.sentProposal = false
	c.sentPrevote = false
	c.sentPrecommit = false
	c.setValidRoundAndValue = false
}

func (c *core) acceptVote(roundState *roundState, step Step, hash common.Hash, msg Message) {
	emptyHash := hash == (common.Hash{})
	switch step {
	case prevote:
		if emptyHash {
			roundState.Prevotes.AddNilVote(msg)
		} else {
			roundState.Prevotes.AddVote(hash, msg)
		}
	case precommit:
		if emptyHash {
			roundState.Precommits.AddNilVote(msg)
		} else {
			roundState.Precommits.AddVote(hash, msg)
		}
	}
}

func (c *core) setStep(step Step) {
	c.currentRoundState.SetStep(step)
	c.processBacklog()
}

func (c *core) stopFutureProposalTimer() {
	if c.futureProposalTimer != nil {
		c.futureProposalTimer.Stop()
	}
}

func (c *core) Quorum(i int) bool {
	return float64(i) >= math.Ceil(float64(2)/float64(3)*float64(c.valSet.Size()))
}

// PrepareCommittedSeal returns a committed seal for the given hash
func PrepareCommittedSeal(hash common.Hash) []byte {
	var buf bytes.Buffer
	buf.Write(hash.Bytes())
	buf.Write([]byte{byte(msgPrecommit)})
	return buf.Bytes()
}
