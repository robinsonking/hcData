// Copyright (c) 2013-2015 The btcsuite developers
// Copyright (c) 2016-2017 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package netparams

import "github.com/decred/dcrd/chaincfg"

// Params is used to group parameters for various networks such as the main
// network and test networks.
type Params struct {
	*chaincfg.Params
	JSONRPCClientPort string
	JSONRPCServerPort string
	GRPCServerPort    string
}

// MainNetParams contains parameters specific running dcrwallet and
// dcrd on the main network (wire.MainNet).
var MainNetParams = Params{
	Params:            &chaincfg.MainNetParams,
	JSONRPCClientPort: "14009",
	JSONRPCServerPort: "14010",
	GRPCServerPort:    "14011",
}

// TestNet3Params contains parameters specific running dcrwallet and
// dcrd on the test network (version 3) (wire.TestNet3).
var TestNet2Params = Params{
	Params:            &chaincfg.TestNet2Params,
	JSONRPCClientPort: "12009",
	JSONRPCServerPort: "12010",
	GRPCServerPort:    "12011",
}

// SimNetParams contains parameters specific to the simulation test network
// (wire.SimNet).
var SimNetParams = Params{
	Params:            &chaincfg.SimNetParams,
	JSONRPCClientPort: "13009",
	JSONRPCServerPort: "13010",
	GRPCServerPort:    "13011",
}
