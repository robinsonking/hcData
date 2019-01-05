// Copyright (c) 2014-2016 The btcsuite developers
// Copyright (c) 2015-2018 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package chaincfg

import (
	"math"
	"time"

	"github.com/decred/dcrd/wire"
)

// TestNet2Params defines the network parameters for the test currency network.
// This network is sometimes simply called "testnet".
// This is the third public iteration of testnet.
var TestNet2Params = Params{
	Name:        "testnet2",
	Net:         wire.TestNet2,
	DefaultPort: "12008",
	DNSSeeds: []string{
		"testnet1.h.cash",
		"testnet2.h.cash",
		"testnet3.h.cash",
	},

	// Chain parameters
	GenesisBlock:             &testNet2GenesisBlock,
	GenesisHash:              &testNet2GenesisHash,
	PowLimit:                 testNetPowLimit,
	PowLimitBits:             0x1e00ffff,
	ReduceMinDifficulty:      false,
	MinDiffReductionTime:     0, // Does not apply since ReduceMinDifficulty false
	GenerateSupported:        true,
	MaximumBlockSizes:        []int{1310720},
	MaxTxSize:                1000000,
	TargetTimePerBlock:       time.Minute,
	WorkDiffAlpha:            1,
	WorkDiffWindowSize:       144,
	WorkDiffWindows:          20,
	TargetTimespan:           time.Minute * 144, // TimePerBlock * WindowSize
	RetargetAdjustmentFactor: 4,

	// Subsidy parameters.
	BaseSubsidy:              640000000, // ~84m = Premine + Total subsidy
	MulSubsidy:               999,
	DivSubsidy:               1000,
	SubsidyReductionInterval: 2048,
	WorkRewardProportion:     6,
	StakeRewardProportion:    3,
	BlockTaxProportion:       1,

	// Checkpoints ordered from oldest to newest.
	Checkpoints: []Checkpoint{},

	// Consensus rule change deployments.
	//
	// The miner confirmation window is defined as:
	//   target proof of work timespan / target proof of work spacing
	RuleChangeActivationQuorum:     2520, // 10 % of RuleChangeActivationInterval * TicketsPerBlock
	RuleChangeActivationMultiplier: 3,    // 75%
	RuleChangeActivationDivisor:    4,
	RuleChangeActivationInterval:   5040, // 1 week
	Deployments: map[uint32][]ConsensusDeployment{
		7: {{
			Vote: Vote{
				Id:          VoteIDMaxBlockSize,
				Description: "Change maximum allowed block size from 1MiB to 1.25MB",
				Mask:        0x0006, // Bits 1 and 2
				Choices: []Choice{{
					Id:          "abstain",
					Description: "abstain voting for change",
					Bits:        0x0000,
					IsAbstain:   true,
					IsNo:        false,
				}, {
					Id:          "no",
					Description: "reject changing max allowed block size",
					Bits:        0x0002, // Bit 1
					IsAbstain:   false,
					IsNo:        true,
				}, {
					Id:          "yes",
					Description: "accept changing max allowed block size",
					Bits:        0x0004, // Bit 2
					IsAbstain:   false,
					IsNo:        false,
				}},
			},
			StartTime:  0,             // Always available for vote
			ExpireTime: math.MaxInt64, // Never expires
		}},
	},

	// Enforce current block version once majority of the network has
	// upgraded.
	// 51% (51 / 100)
	// Reject previous block versions once a majority of the network has
	// upgraded.
	// 75% (75 / 100)
	BlockEnforceNumRequired: 51,
	BlockRejectNumRequired:  75,
	BlockUpgradeNumToCheck:  100,

	// Mempool parameters
	RelayNonStdTxs: true,

	// Address encoding magics
	NetworkAddressPrefix: "T",
	PubKeyAddrID:         [2]byte{0x28, 0xf7}, // starts with Tk
	PubKeyBlissAddrID:    [2]byte{0x0c, 0x66}, // starts with Tk
	PubKeyHashAddrID:     [2]byte{0x0f, 0x21}, // starts with Ts
	PKHEdwardsAddrID:     [2]byte{0x0f, 0x01}, // starts with Te
	PKHSchnorrAddrID:     [2]byte{0x0e, 0xe3}, // starts with TS
	PKHBlissAddrID:       [2]byte{0x0e, 0xf9}, // starts with Tb
	ScriptHashAddrID:     [2]byte{0x0e, 0xfc}, // starts with Tc
	PrivateKeyID:         [2]byte{0x23, 0x0e}, // starts with Pt

	// BIP32 hierarchical deterministic extended key magics
	HDPrivateKeyID: [4]byte{0x04, 0x35, 0x83, 0x97}, // starts with tprv
	HDPublicKeyID:  [4]byte{0x04, 0x35, 0x87, 0xd1}, // starts with tpub

	// BIP44 coin type used in the hierarchical deterministic path for
	// address generation.
	HDCoinType: uint32(171),

	// Hcd PoS parameters
	MinimumStakeDiff:        20000000, // 0.2 Coin
	TicketPoolSize:          1024,
	TicketsPerBlock:         5,
	TicketMaturity:          16,
	TicketExpiry:            6144, // 6*TicketPoolSize
	CoinbaseMaturity:        16,
	SStxChangeMaturity:      1,
	TicketPoolSizeWeight:    4,
	StakeDiffAlpha:          1,
	StakeDiffWindowSize:     144,
	StakeDiffWindows:        20,
	StakeVersionInterval:    144 * 2 * 7, // ~1 week
	MaxFreshStakePerBlock:   20,          // 4*TicketsPerBlock
	StakeEnabledHeight:      16 + 16,     // CoinbaseMaturity + TicketMaturity
	StakeValidationHeight:   775,         // Arbitrary
	StakeBaseSigScript:      []byte{0x00, 0x00},
	StakeMajorityMultiplier: 3,
	StakeMajorityDivisor:    4,

	// Hcd organization related parameters.
	// Organization address is TcYvmPS6xs41gJExBaeUzT55epgwtHzjMAC
	OrganizationPkScript:        hexDecode("5221031377eb7eb294ba8d0c81bb64a047c9b36561f3899507679b38cfcbf59e016f9421036806c694f4d5d617259b5fabaf9ad84c20c2bf57b1a171fb6048215d6d71e13e52ae"),
	OrganizationPkScriptVersion: 0,
	BlockOneLedger:              BlockOneLedgerTestNet2,
	OmniMoneyReceive:            "TsSmoC9HdBhDhq4ut4TqJY7SBjPqJFAPkGK",
	OmniStartHeight:			 46000,
}
