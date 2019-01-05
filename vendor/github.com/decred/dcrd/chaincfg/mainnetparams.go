// Copyright (c) 2014-2016 The btcsuite developers
// Copyright (c) 2015-2017 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package chaincfg

import (
	"time"

	"github.com/decred/dcrd/wire"
)

// MainNetParams defines the network parameters for the main Decred network.
var MainNetParams = Params{
	Name:        "mainnet",
	Net:         wire.MainNet,
	DefaultPort: "14008",
	DNSSeeds: []string{
		"mainnet1.h.cash",
		"mainnet2.h.cash",
		"mainnet3.h.cash",
		"mainnet4.h.cash",
		"mainnet5.h.cash",
	},

	// Chain parameters
	GenesisBlock:             &genesisBlock,
	GenesisHash:              &genesisHash,
	PowLimit:                 mainPowLimit,
	PowLimitBits:             0x1d00ffff,
	ReduceMinDifficulty:      false,
	MinDiffReductionTime:     0, // Does not apply since ReduceMinDifficulty false
	GenerateSupported:        false,
	MaximumBlockSizes:        []int{1000000},
	MaxTxSize:                1000000,
	TargetTimePerBlock:       time.Second * 150,
	WorkDiffAlpha:            1,
	WorkDiffWindowSize:       288,
	WorkDiffWindows:          20,
	TargetTimespan:           time.Second * 150 * 288, // TimePerBlock * WindowSize
	RetargetAdjustmentFactor: 4,

	// Subsidy parameters.
	BaseSubsidy:              640000000, // ~84m = Premine + Total subsidy
	MulSubsidy:               999,
	DivSubsidy:               1000,
	SubsidyReductionInterval: 12288,
	WorkRewardProportion:     6,
	StakeRewardProportion:    3,
	BlockTaxProportion:       1,

	// Checkpoints ordered from oldest to newest.
	Checkpoints: []Checkpoint{},

	// The miner confirmation window is defined as:
	//   target proof of work timespan / target proof of work spacing
	RuleChangeActivationQuorum:     4032, // 10 % of RuleChangeActivationInterval * TicketsPerBlock
	RuleChangeActivationMultiplier: 3,    // 75%
	RuleChangeActivationDivisor:    4,
	RuleChangeActivationInterval:   2016 * 4, // 4 weeks
	Deployments:                    map[uint32][]ConsensusDeployment{},

	// Enforce current block version once majority of the network has
	// upgraded.
	// 75% (750 / 1000)
	// Reject previous block versions once a majority of the network has
	// upgraded.
	// 95% (950 / 1000)
	BlockEnforceNumRequired: 750,
	BlockRejectNumRequired:  950,
	BlockUpgradeNumToCheck:  1000,

	// Mempool parameters
	RelayNonStdTxs: false,

	// Address encoding magics
	NetworkAddressPrefix: "H",
	PubKeyAddrID:         [2]byte{0x19, 0xa4}, // starts with Hk
	PubKeyBlissAddrID:    [2]byte{0x07, 0xc3}, // starts with Hk
	PubKeyHashAddrID:     [2]byte{0x09, 0x7f}, // starts with Hs
	PKHEdwardsAddrID:     [2]byte{0x09, 0x60}, // starts with He
	PKHSchnorrAddrID:     [2]byte{0x09, 0x41}, // starts with HS
	PKHBlissAddrID:       [2]byte{0x09, 0x58}, // starts with Hb
	ScriptHashAddrID:     [2]byte{0x09, 0x5a}, // starts with Hc
	PrivateKeyID:         [2]byte{0x19, 0xab}, // starts with Hm

	// BIP32 hierarchical deterministic extended key magics
	HDPrivateKeyID: [4]byte{0x02, 0xfd, 0xa4, 0xe8}, // starts with dprv
	HDPublicKeyID:  [4]byte{0x02, 0xfd, 0xa9, 0x26}, // starts with dpub

	// BIP44 coin type used in the hierarchical deterministic path for
	// address generation.
	HDCoinType: uint32(171),

	// Hcd PoS parameters
	MinimumStakeDiff:        2 * 1e8, // 2 Coin
	TicketPoolSize:          8192,
	TicketsPerBlock:         5,
	TicketMaturity:          512,
	TicketExpiry:            40960, // 5*TicketPoolSize
	CoinbaseMaturity:        512,
	SStxChangeMaturity:      1,
	TicketPoolSizeWeight:    4,
	StakeDiffAlpha:          1, // Minimal
	StakeDiffWindowSize:     288,
	StakeDiffWindows:        20,
	StakeVersionInterval:    288 * 2 * 7, // ~1 week
	MaxFreshStakePerBlock:   20,          // 4*TicketsPerBlock
	StakeEnabledHeight:      512 + 512,   // CoinbaseMaturity + TicketMaturity
	StakeValidationHeight:   4096,        // ~7 days
	StakeBaseSigScript:      []byte{0x00, 0x00},
	StakeMajorityMultiplier: 3,
	StakeMajorityDivisor:    4,

	// Hcd organization related parameters
	// Organization address is xxxxxxx
	OrganizationPkScript:        hexDecode("76a9141842627102a8a153c1a8db39c9a30c0f8f5263d988ac"),
	OrganizationPkScriptVersion: 0,
	BlockOneLedger:              BlockOneLedgerMainNet,
	OmniMoneyReceive:            "HsTJckn6hjhP4QYHF7CE87ok3y5TDA2gd6D",
	OmniStartHeight:			 46000,
}
