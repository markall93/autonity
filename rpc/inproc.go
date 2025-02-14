// Copyright 2016 The go-ethereum Authors
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

package rpc

import (
	"context"
	"net"

	"github.com/clearmatics/autonity/common/ratelimit"
)

// DialInProc attaches an in-process connection to the given RPC server.
func DialInProc(handler *Server) *Client {
	initctx := context.Background()
	c, _ := newClient(initctx, func(context.Context) (ServerCodec, error) {
		p1, p2 := net.Pipe()
		go handler.ServeCodec(NewJSONCodec(p1), OptionMethodInvocation|OptionSubscriptions)
		return NewJSONCodec(p2), nil
	})
	return c
}

func DialInProcWithRate(handler *Server, rate, capacity int64) *Client {
	return DialInProcWithRateClock(handler, rate, capacity, nil)
}

func DialInProcWithRateClock(handler *Server, rate, capacity int64, clock ratelimit.Clock) *Client {
	initctx := context.Background()
	c, _ := newClient(initctx, func(context.Context) (ServerCodec, error) {
		p1, p2 := ratelimit.NewPipesWithClock(float64(rate), capacity, clock)

		go handler.ServeCodec(NewJSONCodec(p1), OptionMethodInvocation|OptionSubscriptions)
		return NewJSONCodec(p2), nil
	})
	return c
}
