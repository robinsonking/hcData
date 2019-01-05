// Copyright (c) 2018, The Decred developers
// Copyright (c) 2017, Jonathan Chappelow
// See LICENSE for details.

package dcrsqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/dcrutil"
	apitypes "github.com/decred/dcrdata/v4/api/types"
	"github.com/decred/dcrdata/v4/blockdata"
	"github.com/decred/dcrdata/v4/db/dbtypes"
	"github.com/decred/dcrdata/v4/explorer"
	"github.com/decred/dcrdata/v4/rpcutils"
	"github.com/decred/dcrdata/v4/txhelpers"
)

const (
	rescanLogBlockChunk      = 1000
	InitialLoadSyncStatusMsg = "(Lite Mode) Syncing stake and base DBs..."
)

// DBHeights returns the best block heights of: SQLite database tables (block
// summary and stake info tables), the stake database (ffldb_stake), and the
// lowest of these. An error value is returned if any database is inaccessible.
func (db *wiredDB) DBHeights() (lowest int64, summaryHeight int64, stakeInfoHeight int64,
	stakeDatabaseHeight int64, err error) {
	// Get DB's best block (for block summary and stake info tables)
	if summaryHeight, err = db.GetBlockSummaryHeight(); err != nil {
		return 0, 0, 0, -1, fmt.Errorf("GetBlockSummaryHeight failed: %v", err)
	}
	if stakeInfoHeight, err = db.GetStakeInfoHeight(); err != nil {
		return 0, 0, 0, -1, fmt.Errorf("GetStakeInfoHeight failed: %v", err)
	}

	// Create a new database to store the accepted stake node data into.
	if db.sDB == nil || db.sDB.BestNode == nil {
		return 0, 0, 0, -1, fmt.Errorf("stake DB is missing")
	}
	stakeDatabaseHeight = int64(db.sDB.Height())

	lowest = stakeInfoHeight
	if summaryHeight < stakeInfoHeight {
		lowest = summaryHeight
	}
	if stakeDatabaseHeight < lowest {
		lowest = stakeDatabaseHeight
	}

	return
}

func (db *wiredDB) initWaitChan(waitChan chan chainhash.Hash) {
	db.waitChan = waitChan
}

// RewindStakeDB attempts to disconnect blocks from the stake database to reach
// the specified height. A channel must be provided for signaling if the rewind
// should abort. If the specified height is greater than the current stake DB
// height, RewindStakeDB will exit without error, returning the current stake DB
// height and a nil error.
func (db *wiredDB) RewindStakeDB(ctx context.Context, toHeight int64) (stakeDBHeight int64, err error) {
	// rewind best node in ticket db
	stakeDBHeight = int64(db.sDB.Height())
	if toHeight < 0 {
		toHeight = 0
	}
	fromHeight := stakeDBHeight
	log.Infof("Rewinding from %d to %d", fromHeight, toHeight)
	for stakeDBHeight > toHeight {
		if stakeDBHeight == fromHeight || stakeDBHeight%200 == 0 {
			log.Infof("Rewinding from %d to %d", stakeDBHeight, toHeight)
		}
		// check for quit signal
		select {
		case <-ctx.Done():
			log.Infof("Rewind cancelled at height %d.", stakeDBHeight)
			return
		default:
		}
		if err = db.sDB.DisconnectBlock(false); err != nil {
			return
		}
		stakeDBHeight = int64(db.sDB.Height())
		log.Tracef("Stake db now at height %d.", stakeDBHeight)
	}
	return
}

func (db *wiredDB) resyncDB(ctx context.Context, blockGetter rpcutils.BlockGetter,
	fetchToHeight int64, updateExplorer chan *chainhash.Hash,
	barLoad chan *dbtypes.ProgressBarLoad) (int64, error) {
	// Determine if we're in lite mode, when we are the "master" who sets the
	// pace rather than waiting on other consumers to get done with the stakedb.
	master := blockGetter == nil || blockGetter.(*rpcutils.BlockGate) == nil

	// Get chain servers's best block.
	_, height, err := db.client.GetBestBlock()
	if err != nil {
		return -1, fmt.Errorf("GetBestBlock failed: %v", err)
	}

	// Time this function.
	defer func(start time.Time, perr *error) {
		if *perr == nil {
			log.Infof("resyncDBWithPoolValue completed in %v", time.Since(start))
			return
		}
		log.Errorf("resyncDBWithPoolValue failed: %v", err)
	}(time.Now(), &err)

	// Check and report heights of the DBs. dbHeight is the lowest of the
	// heights, and may be -1 with an empty SQLite DB.
	dbHeight, summaryHeight, stakeInfoHeight, stakeDBHeight, err := db.DBHeights()
	if err != nil {
		return -1, fmt.Errorf("DBHeights failed: %v", err)
	}
	if dbHeight < -1 {
		panic("invalid starting height")
	}

	log.Info("Current best block (chain server):    ", height)
	log.Info("Current best block (sqlite block DB): ", summaryHeight)
	if stakeInfoHeight != summaryHeight {
		log.Error("Current best block (sqlite stake DB): ", stakeInfoHeight)
		return -1, fmt.Errorf("SQLite database (dcrdata.sqlt.db) is corrupted")
	}
	log.Info("Current best block (stakedb):         ", stakeDBHeight)

	// Attempt to rewind stake database, if needed, forcing it to the lowest DB
	// height (or 0 if the lowest DB height is -1).
	if stakeDBHeight > dbHeight && stakeDBHeight > 0 {
		if dbHeight < 0 || stakeDBHeight > 2*dbHeight {
			return -1, fmt.Errorf("delete stake db (ffldb_stake) and try again")
		}
		log.Infof("Rewinding stake node from %d to %d", stakeDBHeight, dbHeight)
		// Rewind best node in ticket DB to larger of lowest DB height or zero.
		stakeDBHeight, err = db.RewindStakeDB(ctx, dbHeight)
		if err != nil {
			return dbHeight, fmt.Errorf("RewindStakeDB failed: %v", err)
		}
	}

	// Start syncing at or after DB height depending on whether an external
	// MasterBlockGetter is already configured to relay the current best block,
	// in which case we receive and discard it to maintain synchronization with
	// the auxiliary DB.
	startHeight := dbHeight

	// When coordinating with an external MasterBlockGetter, do not start beyond
	// fetchToHeight, which is intended to indicate where the MasterBlockGetter
	// will be relaying blocks, and potentially relying on stakedb block
	// connection notifications that are triggered in this function.
	if !master {
		// stakedb height may not be larger than fetchToHeight if there is an
		// external MasterBlockGetter since it is likely to require notification
		// of block connection in stakedb starting at height fetchToHeight.
		if fetchToHeight < stakeDBHeight {
			return startHeight, fmt.Errorf("fetchToHeight may not be less than stakedb height")
		}

		// Start at the next block we don't have in both SQLite and stakedb, but
		// do not start beyond fetchToHeight if there is an external
		// MasterBlockGetter, the owner of which should already be configured to
		// send the block at fetchToHeight over the waitChan (e.g. the call to
		// UpdateToBlock in (*ChainDB).SyncChainDB).
		if fetchToHeight > startHeight {
			startHeight++
		}
	} else {
		// Begin at the next block not in all DBs.
		startHeight++
	}

	// At least this many blocks to check (another may come in before finishing)
	minBlocksToCheck := height - dbHeight
	if minBlocksToCheck < 1 {
		if minBlocksToCheck < 0 {
			return dbHeight, fmt.Errorf("chain server behind DBs")
		}
		// dbHeight == height
		log.Infof("SQLite already synchronized with node at height %d.", height)
		return height, nil
	}

	// Initialize the progress bars on the sync status page.
	if barLoad != nil && db.updateStatusSync {
		barLoad <- &dbtypes.ProgressBarLoad{
			Msg:   InitialLoadSyncStatusMsg,
			BarID: dbtypes.InitialDBLoad,
		}
	}

	timeStart := time.Now()
	for i := startHeight; i <= height; i++ {
		// check for quit signal
		select {
		case <-ctx.Done():
			log.Infof("Rescan cancelled at height %d.", i)
			return i - 1, nil
		default:
		}

		if i == 764 {
			fmt.Println("ttt")

		}
		log.Infof("Rescanning  at height %d.", i)
		//log.Warn("Rescanning  at height %d.", i)
		// Either fetch the block or wait for a signal that it is ready
		var block *dcrutil.Block
		var blockhash chainhash.Hash
		if master || i < fetchToHeight {
			// Not coordinating with blockGetter for this block
			var h *chainhash.Hash
			block, h, err = db.getBlock(i)
			if err != nil {
				return i - 1, fmt.Errorf("getBlock failed (%d): %v", i, err)
			}
			blockhash = *h
		} else {
			// Wait for this block to become available in the MasterBlockGetter
			select {
			case blockhash = <-db.waitChan:
			case <-ctx.Done():
				log.Infof("Rescan cancelled at height %d.", i)
				return i - 1, nil
			}
			block, err = blockGetter.Block(blockhash)
			if err != nil {
				return i - 1, fmt.Errorf("blockGetter.Block failed (%s): %v", blockhash, err)
			}
			// Before connecting the block in the StakeDatabase, request
			// notification for the next block.
			db.waitChan = blockGetter.WaitForHeight(i + 1)
		}

		// Advance stakedb height, which should always be less than or equal to
		// SQLite height, except when SQLite is empty since stakedb always has
		// genesis, as enforced by the rewinding code in this function.
		if i > stakeDBHeight {
		//	if i != int64(db.sDB.Height()+1) {
	//			panic(fmt.Sprintf("about to connect the wrong block: %d, %d", i, db.sDB.Height()))
	//		}
			if err = db.sDB.ConnectBlock(block); err != nil {
				return i - 1, err
			}
		}
		stakeDBHeight = int64(db.sDB.Height()) // i

		if (i-1)%rescanLogBlockChunk == 0 && i-1 != startHeight || i == startHeight {
			if i == 0 {
				log.Infof("Scanning genesis block into stakedb and sqlite block db.")
			} else {
				endRangeBlock := rescanLogBlockChunk * (1 + (i-1)/rescanLogBlockChunk)
				if endRangeBlock > height {
					endRangeBlock = height
				}
				log.Infof("Scanning blocks %d to %d (%d live)...",
					i, endRangeBlock, db.sDB.PoolSize())

				// If updateStatusSync is set to true then this is the only way that sync progress will be updated.
				if barLoad != nil && db.updateStatusSync {
					timeTakenPerBlock := (time.Since(timeStart).Seconds() / float64(endRangeBlock-i))

					barLoad <- &dbtypes.ProgressBarLoad{
						From:      i,
						To:        height,
						Timestamp: int64(timeTakenPerBlock * float64(height-endRangeBlock)), //timeToComplete
						Msg:       InitialLoadSyncStatusMsg,
						BarID:     dbtypes.InitialDBLoad,
					}

					timeStart = time.Now()
				}
			}
		}

		// If SQLite is ahead, go to next block (stakedb may be catching up).
		if i <= summaryHeight && i <= stakeInfoHeight {
			// update height, the end condition for the loop
			if _, height, err = db.client.GetBestBlock(); err != nil {
				return i - 1, fmt.Errorf("rpcclient.GetBestBlock failed: %v", err)
			}
			continue
		}

		tpi, found := db.sDB.PoolInfo(blockhash)
		if !found {
			if i != 0 {
				log.Errorf("Unable to find block (%v) in pool info cache. Resync is malfunctioning!", blockhash)
			}
			tpi = db.sDB.PoolInfoBest()
		}
		if int64(tpi.Height) != i {
			log.Errorf("Ticket pool info not available for block %v.", blockhash)
			tpi = nil
		}

		header := block.MsgBlock().Header
		diffRatio := txhelpers.GetDifficultyRatio(header.Bits, db.params)

		blockSummary := apitypes.BlockDataBasic{
			Height:     header.Height,
			Size:       header.Size,
			Hash:       blockhash.String(),
			Difficulty: diffRatio,
			StakeDiff:  dcrutil.Amount(header.SBits).ToCoin(),
			Time:       apitypes.TimeAPI{S: dbtypes.TimeDef{T: header.Timestamp}},
			PoolInfo:   tpi,
		}

		// Allow different summaryHeight and stakeInfoHeight values to be
		// handled, although this should never happen.
		if i > summaryHeight {
			if err = db.StoreBlockSummary(&blockSummary); err != nil {
				return i - 1, fmt.Errorf("Unable to store block summary in database: %v", err)
			}
			summaryHeight = i
		}

		if i <= stakeInfoHeight {
			// update height, the end condition for the loop
			if _, height, err = db.client.GetBestBlock(); err != nil {
				return i - 1, fmt.Errorf("rpcclient.GetBestBlock failed: %v", err)
			}
			continue
		}

		// Stake info
		si := apitypes.StakeInfoExtended{
			Hash: blockSummary.Hash,
		}

		// Ticket fee info
		fib := txhelpers.FeeRateInfoBlock(block)
		if fib == nil {
			return i - 1, fmt.Errorf("FeeRateInfoBlock failed")
		}
		si.Feeinfo = *fib

		// Price window number and block index
		winSize := uint32(db.params.StakeDiffWindowSize)
		si.PriceWindowNum = int(i) / int(winSize)
		si.IdxBlockInWindow = int(i)%int(winSize) + 1

		// Ticket pool info
		si.PoolInfo = blockSummary.PoolInfo

		if err = db.StoreStakeInfoExtended(&si); err != nil {
			return i - 1, fmt.Errorf("Unable to store stake info in database: %v", err)
		}
		stakeInfoHeight = i

		// If updating explore is activated, update it at intervals of 200 blocks.
		if updateExplorer != nil && i%200 == 0 && explorer.SyncExplorerUpdateStatus() && db.updateStatusSync {
			updateExplorer <- &blockhash
			select {
			case db.updateStatusChan <- uint32(i):
			default:
			}
		}

		// Update height, the end condition for the loop.
		if _, height, err = db.client.GetBestBlock(); err != nil {
			return i, fmt.Errorf("rpcclient.GetBestBlock failed: %v", err)
		}
	}

	if barLoad != nil && db.updateStatusSync {
		barLoad <- &dbtypes.ProgressBarLoad{
			From:     height,
			To:       height,
			Msg:      InitialLoadSyncStatusMsg,
			BarID:    dbtypes.InitialDBLoad,
			Subtitle: "sync complete",
		}
	}

	log.Infof("Rescan finished successfully at height %d.", height)

	_, summaryHeight, stakeInfoHeight, stakeDBHeight, err = db.DBHeights()
	if err != nil {
		return -1, fmt.Errorf("DBHeights failed: %v", err)
	}

	log.Debug("New best block (chain server):    ", height)
	log.Debug("New best block (sqlite block DB): ", summaryHeight)
	if stakeInfoHeight != summaryHeight {
		log.Error("New best block (sqlite stake DB): ", stakeInfoHeight)
		return -1, fmt.Errorf("SQLite database (dcrdata.sqlt.db) is corrupted")
	}
	log.Debug("New best block (stakedb):         ", stakeDBHeight)

	return height, nil
}

func (db *wiredDB) getBlock(ind int64) (*dcrutil.Block, *chainhash.Hash, error) {
	blockhash, err := db.client.GetBlockHash(ind)
	if err != nil {
		return nil, nil, fmt.Errorf("GetBlockHash(%d) failed: %v", ind, err)
	}

	msgBlock, err := db.client.GetBlock(blockhash)
	if err != nil {
		return nil, blockhash,
			fmt.Errorf("GetBlock failed (%s): %v", blockhash, err)
	}
	block := dcrutil.NewBlock(msgBlock)

	return block, blockhash, nil
}

// ImportSideChains imports all side chains. Similar to pgblockchain.MissingSideChainBlocks
// plus the rest from main.go
func (db *wiredDB) ImportSideChains(collector *blockdata.Collector) error {
	tips, err := rpcutils.SideChains(db.client)
	if err != nil {
		return err
	}
	var hashlist []*chainhash.Hash
	for it := range tips {
		log.Tracef("Primary DB -> Getting base DB side chain with tip %s at %d.", tips[it].Hash, tips[it].Height)
		sideChain, err := rpcutils.SideChainFull(db.client, tips[it].Hash)
		if err != nil {
			log.Errorf("Primary DB -> Unable to get side chain blocks for chain tip %s: %v", tips[it].Hash, err)
			return err
		}

		// For each block in the side chain, check if it already stored.
		for is := range sideChain {
			// Check for the block hash in the DB.
			isMainchainNow, err := db.getMainchainStatus(sideChain[is])
			if isMainchainNow || err == sql.ErrNoRows {
				blockhash, err := chainhash.NewHashFromStr(sideChain[is])
				if err != nil {
					log.Errorf("Primary DB -> Invalid block hash %s: %v.", blockhash, err)
					continue
				}
				hashlist = append(hashlist, blockhash)
			}
		}
	}
	log.Infof("Primary DB -> %d new sidechain block(s) to import", len(hashlist))
	for _, blockhash := range hashlist {
		// Collect block data.
		blockDataBasic, _ := collector.CollectAPITypes(blockhash)
		log.Debugf("Primary DB -> Importing block %s (height %d) into primary DB.",
			blockhash.String(), blockDataBasic.Height)
		if blockDataBasic == nil {
			// Do not quit if unable to collect side chain block data.
			log.Error("Primary DB -> Unable to collect data for side chain block %s", blockhash.String())
			continue
		}
		err := db.StoreSideBlockSummary(blockDataBasic)
		if err != nil {
			log.Errorf("Primary DB -> Failed to store block %s", blockhash.String())
		}
	}
	return nil
}
