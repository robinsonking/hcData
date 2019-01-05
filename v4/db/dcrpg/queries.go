// Copyright (c) 2018, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package dcrpg

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/decred/dcrd/blockchain/stake"
	"github.com/decred/dcrd/chaincfg"
	"github.com/decred/dcrd/dcrutil"
	"github.com/decred/dcrd/txscript"
	"github.com/decred/dcrd/wire"
	apitypes "github.com/decred/dcrdata/v4/api/types"
	"github.com/decred/dcrdata/v4/db/dbtypes"
	"github.com/decred/dcrdata/v4/db/dcrpg/internal"
	"github.com/decred/dcrdata/v4/txhelpers"
	humanize "github.com/dustin/go-humanize"
	"github.com/lib/pq"
)

// outputCountType defines the modes of the output count chart data.
// outputCountByAllBlocks defines count per block i.e. solo and pooled tickets
// count per block. outputCountByTicketPoolWindow defines the output count per
// given ticket price window
type outputCountType int

const (
	outputCountByAllBlocks outputCountType = iota
	outputCountByTicketPoolWindow
)

// Maintenance functions

// closeRows closes the input sql.Rows, logging any error.
func closeRows(rows *sql.Rows) {
	if e := rows.Close(); e != nil {
		log.Errorf("Close of Query failed: %v", e)
	}
}

// sqlExec executes the SQL statement string with any optional arguments, and
// returns the nuber of rows affected.
func sqlExec(db *sql.DB, stmt, execErrPrefix string, args ...interface{}) (int64, error) {
	res, err := db.Exec(stmt, args...)
	if err != nil {
		return 0, fmt.Errorf(execErrPrefix + " " + err.Error())
	}
	if res == nil {
		return 0, nil
	}

	var N int64
	N, err = res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf(`error in RowsAffected: %v`, err)
	}
	return N, err
}

// sqlExecStmt executes the prepared SQL statement with any optional arguments,
// and returns the nuber of rows affected.
func sqlExecStmt(stmt *sql.Stmt, execErrPrefix string, args ...interface{}) (int64, error) {
	res, err := stmt.Exec(args...)
	if err != nil {
		return 0, fmt.Errorf("%v %v", execErrPrefix, err)
	}
	if res == nil {
		return 0, nil
	}

	var N int64
	N, err = res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf(`error in RowsAffected: %v`, err)
	}
	return N, err
}

// ExistsIndex checks if the specified index name exists.
func ExistsIndex(db *sql.DB, indexName string) (exists bool, err error) {
	err = db.QueryRow(internal.IndexExists, indexName, "public").Scan(&exists)
	if err == sql.ErrNoRows {
		err = nil
	}
	return
}

// IsUniqueIndex checks if the given index name is defined as UNIQUE.
func IsUniqueIndex(db *sql.DB, indexName string) (isUnique bool, err error) {
	err = db.QueryRow(internal.IndexIsUnique, indexName, "public").Scan(&isUnique)
	return
}

// DeleteDuplicateVins deletes rows in vin with duplicate tx information,
// leaving the one row with the lowest id.
func DeleteDuplicateVins(db *sql.DB) (int64, error) {
	execErrPrefix := "failed to delete duplicate vins: "

	existsIdx, err := ExistsIndex(db, "uix_vin")
	if err != nil {
		return 0, err
	} else if !existsIdx {
		return sqlExec(db, internal.DeleteVinsDuplicateRows, execErrPrefix)
	}

	if isuniq, err := IsUniqueIndex(db, "uix_vin"); err != nil && err != sql.ErrNoRows {
		return 0, err
	} else if isuniq {
		return 0, nil
	}

	return sqlExec(db, internal.DeleteVinsDuplicateRows, execErrPrefix)
}

// DeleteDuplicateVouts deletes rows in vouts with duplicate tx information,
// leaving the one row with the lowest id.
func DeleteDuplicateVouts(db *sql.DB) (int64, error) {
	execErrPrefix := "failed to delete duplicate vouts: "

	existsIdx, err := ExistsIndex(db, "uix_vout_txhash_ind")
	if err != nil {
		return 0, err
	} else if !existsIdx {
		return sqlExec(db, internal.DeleteVoutDuplicateRows, execErrPrefix)
	}

	if isuniq, err := IsUniqueIndex(db, "uix_vout_txhash_ind"); err != nil && err != sql.ErrNoRows {
		return 0, err
	} else if isuniq {
		return 0, nil
	}

	return sqlExec(db, internal.DeleteVoutDuplicateRows, execErrPrefix)
}

// DeleteDuplicateTxns deletes rows in transactions with duplicate tx-block
// hashes, leaving the one row with the lowest id.
func DeleteDuplicateTxns(db *sql.DB) (int64, error) {
	execErrPrefix := "failed to delete duplicate transactions: "

	existsIdx, err := ExistsIndex(db, "uix_tx_hashes")
	if err != nil {
		return 0, err
	} else if !existsIdx {
		return sqlExec(db, internal.DeleteTxDuplicateRows, execErrPrefix)
	}

	if isuniq, err := IsUniqueIndex(db, "uix_tx_hashes"); err != nil && err != sql.ErrNoRows {
		return 0, err
	} else if isuniq {
		return 0, nil
	}

	return sqlExec(db, internal.DeleteTxDuplicateRows, execErrPrefix)
}

// DeleteDuplicateTickets deletes rows in tickets with duplicate tx-block
// hashes, leaving the one row with the lowest id.
func DeleteDuplicateTickets(db *sql.DB) (int64, error) {
	if isuniq, err := IsUniqueIndex(db, "uix_ticket_hashes_index"); err != nil && err != sql.ErrNoRows {
		return 0, err
	} else if isuniq {
		return 0, nil
	}
	execErrPrefix := "failed to delete duplicate tickets: "
	return sqlExec(db, internal.DeleteTicketsDuplicateRows, execErrPrefix)
}

// DeleteDuplicateVotes deletes rows in votes with duplicate tx-block hashes,
// leaving the one row with the lowest id.
func DeleteDuplicateVotes(db *sql.DB) (int64, error) {
	if isuniq, err := IsUniqueIndex(db, "uix_votes_hashes_index"); err != nil && err != sql.ErrNoRows {
		return 0, err
	} else if isuniq {
		return 0, nil
	}
	execErrPrefix := "failed to delete duplicate votes: "
	return sqlExec(db, internal.DeleteVotesDuplicateRows, execErrPrefix)
}

// DeleteDuplicateMisses deletes rows in misses with duplicate tx-block hashes,
// leaving the one row with the lowest id.
func DeleteDuplicateMisses(db *sql.DB) (int64, error) {
	if isuniq, err := IsUniqueIndex(db, "uix_misses_hashes_index"); err != nil && err != sql.ErrNoRows {
		return 0, err
	} else if isuniq {
		return 0, nil
	}
	execErrPrefix := "failed to delete duplicate misses: "
	return sqlExec(db, internal.DeleteMissesDuplicateRows, execErrPrefix)
}

// --- stake (votes, tickets, misses) tables ---

// InsertTickets takes a slice of *dbtypes.Tx and corresponding DB row IDs for
// transactions, extracts the tickets, and inserts the tickets into the
// database. Outputs are a slice of DB row IDs of the inserted tickets, and an
// error.
func InsertTickets(db *sql.DB, dbTxns []*dbtypes.Tx, txDbIDs []uint64, checked, updateExistingRecords bool) ([]uint64, []*dbtypes.Tx, error) {
	dbtx, err := db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to begin database transaction: %v", err)
	}

	// Prepare ticket insert statement, optionally updating a row if it conflicts
	// with the unique index on (tx_hash, block_hash).
	stmt, err := dbtx.Prepare(internal.MakeTicketInsertStatement(checked, updateExistingRecords))
	if err != nil {
		log.Errorf("Ticket INSERT prepare: %v", err)
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, nil, err
	}

	// Choose only SSTx
	var ticketTx []*dbtypes.Tx
	var ticketDbIDs []uint64
	for i, tx := range dbTxns {
		if tx.TxType == int16(stake.TxTypeSStx) {
			ticketTx = append(ticketTx, tx)
			ticketDbIDs = append(ticketDbIDs, txDbIDs[i])
		}
	}

	// Insert each ticket
	ids := make([]uint64, 0, len(ticketTx))
	for i, tx := range ticketTx {
		// Reference Vouts[0] to determine stakesubmission address and if multisig
		var stakesubmissionAddress string
		var isMultisig bool
		if len(tx.Vouts) > 0 {
			if len(tx.Vouts[0].ScriptPubKeyData.Addresses) > 0 {
				stakesubmissionAddress = tx.Vouts[0].ScriptPubKeyData.Addresses[0]
			}
			// scriptSubClass, _, _, _ := txscript.ExtractPkScriptAddrs(
			// 	tx.Vouts[0].Version, tx.Vouts[0].ScriptPubKey[1:], chainParams)
			scriptSubClass, _ := txscript.GetStakeOutSubclass(tx.Vouts[0].ScriptPubKey)
			isMultisig = scriptSubClass == txscript.MultiSigTy
		}

		price := dcrutil.Amount(tx.Vouts[0].Value).ToCoin()
		fee := dcrutil.Amount(tx.Fees).ToCoin()
		isSplit := tx.NumVin > 1

		var id uint64
		err := stmt.QueryRow(
			tx.TxID, tx.BlockHash, tx.BlockHeight, ticketDbIDs[i],
			stakesubmissionAddress, isMultisig, isSplit, tx.NumVin,
			price, fee, dbtypes.TicketUnspent, dbtypes.PoolStatusLive,
			tx.IsMainchainBlock).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbtx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return nil, nil, err
		}
		ids = append(ids, id)
	}

	// Close prepared statement. Ignore errors as we'll Commit regardless.
	_ = stmt.Close()

	return ids, ticketTx, dbtx.Commit()

}

// InsertVotes takes a slice of *dbtypes.Tx, which must contain all the stake
// transactions in a block, extracts the votes, and inserts the votes into the
// database. The input MsgBlockPG contains each stake transaction's MsgTx in
// STransactions, and they must be in the same order as the dbtypes.Tx slice.
//
// This function also identifies and stores missed votes using
// msgBlock.Validators, which lists the ticket hashes called to vote on the
// previous block (msgBlock.WinningTickets are the lottery winners to be mined
// in the next block).
//
// The TicketTxnIDGetter is used to get the spent tickets' row IDs. The get
// function, TxnDbID, is called with the expire argument set to false, so that
// subsequent cache lookups by other consumers will succeed.
//
// Outputs are slices of DB row IDs for the votes and misses, and an error.
func InsertVotes(db *sql.DB, dbTxns []*dbtypes.Tx, _ /*txDbIDs*/ []uint64, fTx *TicketTxnIDGetter,
	msgBlock *MsgBlockPG, checked, updateExistingRecords bool, params *chaincfg.Params) ([]uint64,
	[]*dbtypes.Tx, []string, []uint64, map[string]uint64, error) {
	// Choose only SSGen txns
	msgTxs := msgBlock.STransactions
	var voteTxs []*dbtypes.Tx
	var voteMsgTxs []*wire.MsgTx
	//var voteTxDbIDs []uint64 // not used presently
	for i, tx := range dbTxns {
		if tx.TxType == int16(stake.TxTypeSSGen) {
			voteTxs = append(voteTxs, tx)
			voteMsgTxs = append(voteMsgTxs, msgTxs[i])
			//voteTxDbIDs = append(voteTxDbIDs, txDbIDs[i])
			if tx.TxID != msgTxs[i].TxHash().String() {
				return nil, nil, nil, nil, nil, fmt.Errorf("txid of dbtypes.Tx does not match that of msgTx")
			}
		}
	}

	if len(voteTxs) == 0 {
		return nil, nil, nil, nil, nil, nil
	}

	// Start DB transaction.
	dbtx, err := db.Begin()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("unable to begin database transaction: %v", err)
	}

	// Prepare vote insert statement, optionally updating a row if it conflicts
	// with the unique index on (tx_hash, block_hash).
	voteInsert := internal.MakeVoteInsertStatement(checked, updateExistingRecords)
	voteStmt, err := dbtx.Prepare(voteInsert)
	if err != nil {
		log.Errorf("Votes INSERT prepare: %v", err)
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, nil, nil, nil, nil, err
	}

	// Prepare agenda status insert statement.
	agendaStmt, err := dbtx.Prepare(internal.MakeAgendaInsertStatement(checked))
	if err != nil {
		log.Errorf("Agendas INSERT prepare: %v", err)
		_ = voteStmt.Close()
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, nil, nil, nil, nil, err
	}

	bail := func() {
		// Already up a creek. Just log any Rollback error.
		_ = voteStmt.Close()
		_ = agendaStmt.Close()
		if errRoll := dbtx.Rollback(); errRoll != nil {
			log.Errorf("Rollback failed: %v", errRoll)
		}
	}

	// Insert each vote, and build list of missed votes equal to
	// setdiff(Validators, votes).
	candidateBlockHash := msgBlock.Header.PrevBlock.String()
	ids := make([]uint64, 0, len(voteTxs))
	spentTicketHashes := make([]string, 0, len(voteTxs))
	spentTicketDbIDs := make([]uint64, 0, len(voteTxs))
	misses := make([]string, len(msgBlock.Validators))
	copy(misses, msgBlock.Validators)
	for i, tx := range voteTxs {
		msgTx := voteMsgTxs[i]
		voteVersion := stake.SSGenVersion(msgTx)
		validBlock, voteBits, err := txhelpers.SSGenVoteBlockValid(msgTx)
		if err != nil {
			bail()
			return nil, nil, nil, nil, nil, err
		}

		voteReward := dcrutil.Amount(msgTx.TxIn[0].ValueIn).ToCoin()
		stakeSubmissionAmount := dcrutil.Amount(msgTx.TxIn[1].ValueIn).ToCoin()
		stakeSubmissionTxHash := msgTx.TxIn[1].PreviousOutPoint.Hash.String()
		spentTicketHashes = append(spentTicketHashes, stakeSubmissionTxHash)

		// Lookup the row ID in the transactions table for the ticket purchase.
		var ticketTxDbID uint64
		if fTx != nil {
			ticketTxDbID, err = fTx.TxnDbID(stakeSubmissionTxHash, false)
			if err != nil {
				bail()
				return nil, nil, nil, nil, nil, err
			}
		}
		spentTicketDbIDs = append(spentTicketDbIDs, ticketTxDbID)

		// Remove the spent ticket from missed list.
		for im := range misses {
			if misses[im] == stakeSubmissionTxHash {
				misses[im] = misses[len(misses)-1]
				misses = misses[:len(misses)-1]
				break
			}
		}

		// votes table insert
		var id uint64
		err = voteStmt.QueryRow(
			tx.BlockHeight, tx.TxID, tx.BlockHash, candidateBlockHash,
			voteVersion, voteBits, validBlock.Validity,
			stakeSubmissionTxHash, ticketTxDbID, stakeSubmissionAmount,
			voteReward, tx.IsMainchainBlock).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			bail()
			return nil, nil, nil, nil, nil, err
		}
		ids = append(ids, id)

		// agendas table, not modified if not updating existing records.
		if checked && !updateExistingRecords {
			continue // rest of loop deals with agendas table
		}

		_, _, _, choices, err := txhelpers.SSGenVoteChoices(msgTx, params)
		if err != nil {
			bail()
			return nil, nil, nil, nil, nil, err
		}

		var rowID uint64
		for _, val := range choices {
			index, err := dbtypes.ChoiceIndexFromStr(val.Choice.Id)
			if err != nil {
				bail()
				return nil, nil, nil, nil, nil, err
			}

			lockedIn, activated, hardForked := false, false, false

			// THIS IS A TEMPORARY SOLUTION till activated, lockedIn and hardforked
			// height values can be sent via an rpc method.
			progress, ok := VotingMilestones[val.ID]
			if ok {
				lockedIn = (progress.LockedIn == tx.BlockHeight)
				activated = (progress.Activated == tx.BlockHeight)
				hardForked = (progress.HardForked == tx.BlockHeight)
			}

			err = agendaStmt.QueryRow(val.ID, index, tx.TxID, tx.BlockHeight,
				tx.BlockTime.T, lockedIn, activated, hardForked).Scan(&rowID)
			if err != nil {
				bail()
				return nil, nil, nil, nil, nil, err
			}
		}
	}

	// Close prepared statements. Ignore errors as we'll Commit regardless.
	_ = voteStmt.Close()
	_ = agendaStmt.Close()

	// If the validators are available, miss accounting should be accurate.
	if len(msgBlock.Validators) > 0 && len(ids)+len(misses) != 5 {
		fmt.Println(misses)
		fmt.Println(voteTxs)
		_ = dbtx.Rollback()
		panic(fmt.Sprintf("votes (%d) + misses (%d) != 5", len(ids), len(misses)))
	}

	// Store missed tickets.
	missHashMap := make(map[string]uint64)
	if len(misses) > 0 {
		// Insert misses, optionally updating a row if it conflicts with the
		// unique index on (ticket_hash, block_hash).
		stmtMissed, err := dbtx.Prepare(internal.MakeMissInsertStatement(checked, updateExistingRecords))
		if err != nil {
			log.Errorf("Miss INSERT prepare: %v", err)
			_ = dbtx.Rollback() // try, but we want the Prepare error back
			return nil, nil, nil, nil, nil, err
		}

		// Insert the miss in the misses table, and store the row ID of the
		// new/existing/updated miss.
		blockHash := msgBlock.BlockHash().String()
		for i := range misses {
			var id uint64
			err = stmtMissed.QueryRow(
				msgBlock.Header.Height, blockHash, candidateBlockHash,
				misses[i]).Scan(&id)
			if err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				_ = stmtMissed.Close() // try, but we want the QueryRow error back
				if errRoll := dbtx.Rollback(); errRoll != nil {
					log.Errorf("Rollback failed: %v", errRoll)
				}
				return nil, nil, nil, nil, nil, err
			}
			missHashMap[misses[i]] = id
		}
		_ = stmtMissed.Close()
	}

	return ids, voteTxs, spentTicketHashes, spentTicketDbIDs, missHashMap, dbtx.Commit()
}

// RetrieveMissedVotesInBlock gets a list of ticket hashes that were called to
// vote in the given block, but missed their vote.
func RetrieveMissedVotesInBlock(ctx context.Context, db *sql.DB, blockHash string) (ticketHashes []string, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectMissesInBlock, blockHash)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	for rows.Next() {
		var hash string
		err = rows.Scan(&hash)
		if err != nil {
			break
		}

		ticketHashes = append(ticketHashes, hash)
	}
	return
}

// RetrieveAllRevokes gets for all ticket revocations the row IDs (primary
// keys), transaction hashes, block heights. It also gets the row ID in the vins
// table for the first input of the revocation transaction, which should
// correspond to the stakesubmission previous outpoint of the ticket purchase.
// This function is used in UpdateSpendingInfoInAllTickets, so it should not be
// subject to timeouts.
func RetrieveAllRevokes(ctx context.Context, db *sql.DB) (ids []uint64, hashes []string, heights []int64, vinDbIDs []uint64, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectAllRevokes)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	defer closeRows(rows)

	for rows.Next() {
		var id, vinDbID uint64
		var height int64
		var hash string
		err = rows.Scan(&id, &hash, &height, &vinDbID)
		if err != nil {
			break
		}

		ids = append(ids, id)
		heights = append(heights, height)
		hashes = append(hashes, hash)
		vinDbIDs = append(vinDbIDs, vinDbID)
	}
	return
}

// RetrieveAllVotesDbIDsHeightsTicketDbIDs gets for all votes the row IDs
// (primary keys) in the votes table, the block heights, and the row IDs in the
// tickets table of the spent tickets. This function is used in
// UpdateSpendingInfoInAllTickets, so it should not be subject to timeouts.
func RetrieveAllVotesDbIDsHeightsTicketDbIDs(ctx context.Context, db *sql.DB) (ids []uint64, heights []int64,
	ticketDbIDs []uint64, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectAllVoteDbIDsHeightsTicketDbIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var id, ticketDbID uint64
		var height int64
		err = rows.Scan(&id, &height, &ticketDbID)
		if err != nil {
			break
		}

		ids = append(ids, id)
		heights = append(heights, height)
		ticketDbIDs = append(ticketDbIDs, ticketDbID)
	}
	return
}

// retrieveWindowBlocks fetches chunks of windows using the limit and offset provided
// for a window size of chaincfg.Params.StakeDiffWindowSize.
func retrieveWindowBlocks(ctx context.Context, db *sql.DB, windowSize int64, limit, offset uint64) ([]*dbtypes.BlocksGroupedInfo, error) {
	rows, err := db.QueryContext(ctx, internal.SelectWindowsByLimit, windowSize, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("retrieveWindowBlocks failed: error: %v", err)
	}

	data := make([]*dbtypes.BlocksGroupedInfo, 0)
	for rows.Next() {
		var difficulty float64
		var timestamp dbtypes.TimeDef
		var startBlock, sbits, count int64
		var blockSizes, votes, txs, revocations, tickets uint64

		err = rows.Scan(&startBlock, &difficulty, &txs, &tickets, &votes,
			&revocations, &blockSizes, &sbits, &timestamp.T, &count)
		if err != nil {
			return nil, err
		}

		endBlock := startBlock + windowSize
		index := dbtypes.CalculateWindowIndex(endBlock, windowSize)

		data = append(data, &dbtypes.BlocksGroupedInfo{
			IndexVal:      index, //window index at the endblock
			EndBlock:      endBlock,
			Voters:        votes,
			Transactions:  txs,
			FreshStake:    tickets,
			Revocations:   revocations,
			BlocksCount:   count,
			Difficulty:    difficulty,
			TicketPrice:   sbits,
			Size:          int64(blockSizes),
			FormattedSize: humanize.Bytes(blockSizes),
			StartTime:     timestamp,
		})
	}

	return data, nil
}

// retrieveTimeBasedBlockListing fetches blocks in chunks based on their block
// time using the limit and offset provided. The time-based blocks groupings
// include but are not limited to day, week, month and year.
func retrieveTimeBasedBlockListing(ctx context.Context, db *sql.DB, timeInterval string,
	limit, offset uint64) ([]*dbtypes.BlocksGroupedInfo, error) {
	rows, err := db.QueryContext(ctx, internal.SelectBlocksTimeListingByLimit, timeInterval,
		limit, offset)
	if err != nil {
		return nil, fmt.Errorf("retrieveTimeBasedBlockListing failed: error: %v", err)
	}

	var data []*dbtypes.BlocksGroupedInfo
	for rows.Next() {
		var startTime, endTime, indexVal dbtypes.TimeDef
		var txs, tickets, votes, revocations, blockSizes uint64
		var blocksCount, endBlock int64

		err = rows.Scan(&indexVal.T, &endBlock, &txs, &tickets, &votes,
			&revocations, &blockSizes, &blocksCount, &startTime.T, &endTime.T)
		if err != nil {
			return nil, err
		}

		data = append(data, &dbtypes.BlocksGroupedInfo{
			EndBlock:           endBlock,
			Voters:             votes,
			Transactions:       txs,
			FreshStake:         tickets,
			Revocations:        revocations,
			BlocksCount:        blocksCount,
			Size:               int64(blockSizes),
			FormattedSize:      humanize.Bytes(blockSizes),
			StartTime:          startTime,
			FormattedStartTime: startTime.T.Format("2006-01-02"),
			EndTime:            endTime,
			FormattedEndTime:   endTime.T.Format("2006-01-02"),
		})
	}
	return data, nil
}

// RetrieveUnspentTickets gets all unspent tickets.
func RetrieveUnspentTickets(ctx context.Context, db *sql.DB) (ids []uint64, hashes []string, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectUnspentTickets)
	if err != nil {
		return ids, hashes, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var id uint64
		var hash string
		err = rows.Scan(&id, &hash)
		if err != nil {
			break
		}

		ids = append(ids, id)
		hashes = append(hashes, hash)
	}

	return ids, hashes, err
}

// RetrieveTicketIDByHashNoCancel gets the db row ID (primary key) in the
// tickets table for the given ticket hash. As the name implies, this query
// should not accept a cancelable context.
func RetrieveTicketIDByHashNoCancel(db *sql.DB, ticketHash string) (id uint64, err error) {
	err = db.QueryRow(internal.SelectTicketIDByHash, ticketHash).Scan(&id)
	return
}

// RetrieveTicketStatusByHash gets the spend status and ticket pool status for
// the given ticket hash.
func RetrieveTicketStatusByHash(ctx context.Context, db *sql.DB, ticketHash string) (id uint64,
	spendStatus dbtypes.TicketSpendType, poolStatus dbtypes.TicketPoolStatus, err error) {
	err = db.QueryRowContext(ctx, internal.SelectTicketStatusByHash, ticketHash).
		Scan(&id, &spendStatus, &poolStatus)
	return
}

// RetrieveTicketIDsByHashes gets the db row IDs (primary keys) in the tickets
// table for the given ticket purchase transaction hashes.
func RetrieveTicketIDsByHashes(ctx context.Context, db *sql.DB, ticketHashes []string) (ids []uint64, err error) {
	var dbtx *sql.Tx
	dbtx, err = db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelDefault,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to begin database transaction: %v", err)
	}

	stmt, err := dbtx.Prepare(internal.SelectTicketIDByHash)
	if err != nil {
		log.Errorf("Tickets SELECT prepare: %v", err)
		_ = stmt.Close()
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, err
	}

	ids = make([]uint64, 0, len(ticketHashes))
	for ih := range ticketHashes {
		var id uint64
		err = stmt.QueryRow(ticketHashes[ih]).Scan(&id)
		if err != nil {
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbtx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return ids, fmt.Errorf("Tickets SELECT exec failed: %v", err)
		}
		ids = append(ids, id)
	}

	// Close prepared statement. Ignore errors as we'll Commit regardless.
	_ = stmt.Close()

	return ids, dbtx.Commit()
}

// retrieveTicketsByDate fetches the tickets in the current ticketpool order by the
// purchase date. The maturity block is needed to identify immature tickets.
// The grouping is done using the time-based group names provided e.g. months,
// days, weeks and years.
func retrieveTicketsByDate(ctx context.Context, db *sql.DB, maturityBlock int64, groupBy string) (*dbtypes.PoolTicketsData, error) {
	rows, err := db.QueryContext(ctx, internal.MakeSelectTicketsByPurchaseDate(groupBy), maturityBlock)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	tickets := new(dbtypes.PoolTicketsData)
	for rows.Next() {
		var immature, live uint64
		var timestamp dbtypes.TimeDef
		var price, total float64
		err = rows.Scan(&timestamp.T, &price, &immature, &live)
		if err != nil {
			return nil, fmt.Errorf("retrieveTicketsByDate %v", err)
		}

		tickets.Time = append(tickets.Time, timestamp)
		tickets.Immature = append(tickets.Immature, immature)
		tickets.Live = append(tickets.Live, live)

		// Returns the average value of a ticket depending on the grouping mode used
		price = price * 100000000
		total = float64(live + immature)
		tickets.Price = append(tickets.Price, dcrutil.Amount(price/total).ToCoin())
	}

	return tickets, nil
}

// retrieveTicketByPrice fetches the tickets in the current ticketpool ordered by the
// purchase price. The maturity block is needed to identify immature tickets.
// The grouping is done using the time-based group names provided e.g. months,
// days, weeks and years.
func retrieveTicketByPrice(ctx context.Context, db *sql.DB, maturityBlock int64) (*dbtypes.PoolTicketsData, error) {
	// Create the query statement and retrieve rows
	rows, err := db.QueryContext(ctx, internal.SelectTicketsByPrice, maturityBlock)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	tickets := new(dbtypes.PoolTicketsData)
	for rows.Next() {
		var live, immature uint64
		var price float64
		err = rows.Scan(&price, &immature, &live)
		if err != nil {
			return nil, fmt.Errorf("retrieveTicketByPrice %v", err)
		}

		tickets.Immature = append(tickets.Immature, immature)
		tickets.Live = append(tickets.Live, live)
		tickets.Price = append(tickets.Price, price)
	}

	return tickets, nil
}

// retrieveTicketsGroupedByType fetches the count of tickets in the current
// ticketpool grouped by ticket type (inferred by their output counts). The
// grouping used here i.e. solo, pooled and tixsplit is just a guessing based on
// commonly structured ticket purchases.
func retrieveTicketsGroupedByType(ctx context.Context, db *sql.DB) (*dbtypes.PoolTicketsData, error) {
	var entry dbtypes.PoolTicketsData
	rows, err := db.QueryContext(ctx, internal.SelectTicketsByType)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var txType, txTypeCount uint64
		err = rows.Scan(&txType, &txTypeCount)

		if err != nil {
			return nil, fmt.Errorf("retrieveTicketsGroupedByType %v", err)
		}

		switch txType {
		case 1:
			entry.Solo = txTypeCount
		case 2:
			entry.Pooled = txTypeCount
		case 3:
			entry.TxSplit = txTypeCount
		}
	}

	return &entry, nil
}

func retrieveTicketSpendTypePerBlock(ctx context.Context, db *sql.DB) (*dbtypes.ChartsData, error) {
	var items = new(dbtypes.ChartsData)
	rows, err := db.QueryContext(ctx, internal.SelectTicketSpendTypeByBlock)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	for rows.Next() {
		var height, unspent, revoked uint64
		err = rows.Scan(&height, &unspent, &revoked)
		if err != nil {
			return nil, err
		}

		items.Height = append(items.Height, height)
		items.Unspent = append(items.Unspent, unspent)
		items.Revoked = append(items.Revoked, revoked)
	}
	return items, nil
}

// SetPoolStatusForTickets sets the ticket pool status for the tickets specified
// by db row ID.
func SetPoolStatusForTickets(db *sql.DB, ticketDbIDs []uint64, poolStatuses []dbtypes.TicketPoolStatus) (int64, error) {
	if len(ticketDbIDs) == 0 {
		return 0, nil
	}
	dbtx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf(`unable to begin database transaction: %v`, err)
	}

	var stmt *sql.Stmt
	stmt, err = dbtx.Prepare(internal.SetTicketPoolStatusForTicketDbID)
	if err != nil {
		// Already up a creek. Just return error from Prepare.
		_ = dbtx.Rollback()
		return 0, fmt.Errorf("tickets SELECT prepare failed: %v", err)
	}

	var totalTicketsUpdated int64
	rowsAffected := make([]int64, len(ticketDbIDs))
	for i, ticketDbID := range ticketDbIDs {
		rowsAffected[i], err = sqlExecStmt(stmt, "failed to set ticket spending info: ",
			ticketDbID, poolStatuses[i])
		if err != nil {
			_ = stmt.Close()
			return 0, dbtx.Rollback()
		}
		totalTicketsUpdated += rowsAffected[i]
		if rowsAffected[i] != 1 {
			log.Warnf("Updated pool status for %d tickets, expecting just 1 (%d, %v)!",
				rowsAffected[i], ticketDbID, poolStatuses[i])
		}
	}

	_ = stmt.Close()

	return totalTicketsUpdated, dbtx.Commit()
}

// SetPoolStatusForTicketsByHash sets the ticket pool status for the tickets
// specified by ticket purchase transaction hash.
func SetPoolStatusForTicketsByHash(db *sql.DB, tickets []string,
	poolStatuses []dbtypes.TicketPoolStatus) (int64, error) {
	if len(tickets) == 0 {
		return 0, nil
	}
	dbtx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf(`unable to begin database transaction: %v`, err)
	}

	var stmt *sql.Stmt
	stmt, err = dbtx.Prepare(internal.SetTicketPoolStatusForHash)
	if err != nil {
		// Already up a creek. Just return error from Prepare.
		_ = dbtx.Rollback()
		return 0, fmt.Errorf("tickets SELECT prepare failed: %v", err)
	}

	var totalTicketsUpdated int64
	rowsAffected := make([]int64, len(tickets))
	for i, ticket := range tickets {
		rowsAffected[i], err = sqlExecStmt(stmt, "failed to set ticket pool status: ",
			ticket, poolStatuses[i])
		if err != nil {
			_ = stmt.Close()
			return 0, dbtx.Rollback()
		}
		totalTicketsUpdated += rowsAffected[i]
		if rowsAffected[i] != 1 {
			log.Warnf("Updated pool status for %d tickets, expecting just 1 (%s, %v)!",
				rowsAffected[i], ticket, poolStatuses[i])
			// TODO: go get the info to add it to the tickets table
		}
	}

	_ = stmt.Close()

	return totalTicketsUpdated, dbtx.Commit()
}

// SetSpendingForTickets sets the spend type, spend height, spending transaction
// row IDs (in the table relevant to the spend type), and ticket pool status for
// the given tickets specified by their db row IDs.
func SetSpendingForTickets(db *sql.DB, ticketDbIDs, spendDbIDs []uint64,
	blockHeights []int64, spendTypes []dbtypes.TicketSpendType,
	poolStatuses []dbtypes.TicketPoolStatus) (int64, error) {
	dbtx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf(`unable to begin database transaction: %v`, err)
	}

	var stmt *sql.Stmt
	stmt, err = dbtx.Prepare(internal.SetTicketSpendingInfoForTicketDbID)
	if err != nil {
		// Already up a creek. Just return error from Prepare.
		_ = dbtx.Rollback()
		return 0, fmt.Errorf("tickets SELECT prepare failed: %v", err)
	}

	var totalTicketsUpdated int64
	rowsAffected := make([]int64, len(ticketDbIDs))
	for i, ticketDbID := range ticketDbIDs {
		rowsAffected[i], err = sqlExecStmt(stmt, "failed to set ticket spending info: ",
			ticketDbID, blockHeights[i], spendDbIDs[i], spendTypes[i], poolStatuses[i])
		if err != nil {
			_ = stmt.Close()
			return 0, dbtx.Rollback()
		}
		totalTicketsUpdated += rowsAffected[i]
		if rowsAffected[i] != 1 {
			log.Warnf("Updated spending info for %d tickets, expecting just 1!",
				rowsAffected[i])
		}
	}

	_ = stmt.Close()

	return totalTicketsUpdated, dbtx.Commit()
}

// setSpendingForTickets is identical to SetSpendingForTickets except it takes a
// database transaction that was begun and will be committed by the caller.
func setSpendingForTickets(dbtx *sql.Tx, ticketDbIDs, spendDbIDs []uint64,
	blockHeights []int64, spendTypes []dbtypes.TicketSpendType,
	poolStatuses []dbtypes.TicketPoolStatus) error {
	stmt, err := dbtx.Prepare(internal.SetTicketSpendingInfoForTicketDbID)
	if err != nil {
		return fmt.Errorf("tickets SELECT prepare failed: %v", err)
	}

	rowsAffected := make([]int64, len(ticketDbIDs))
	for i, ticketDbID := range ticketDbIDs {
		rowsAffected[i], err = sqlExecStmt(stmt, "failed to set ticket spending info: ",
			ticketDbID, blockHeights[i], spendDbIDs[i], spendTypes[i], poolStatuses[i])
		if err != nil {
			_ = stmt.Close()
			return err
		}
		if rowsAffected[i] != 1 {
			log.Warnf("Updated spending info for %d tickets, expecting just 1!",
				rowsAffected[i])
		}
	}

	return stmt.Close()
}

// --- addresses table ---

// InsertAddressRow inserts an AddressRow (input or output), returning the row
// ID in the addresses table of the inserted data.
func InsertAddressRow(db *sql.DB, dbA *dbtypes.AddressRow, dupCheck, updateExistingRecords bool) (uint64, error) {
	sqlStmt := internal.MakeAddressRowInsertStatement(dupCheck, updateExistingRecords)
	var id uint64
	err := db.QueryRow(sqlStmt, dbA.Address, dbA.MatchingTxHash, dbA.TxHash,
		dbA.TxVinVoutIndex, dbA.VinVoutDbID, dbA.Value, dbA.TxBlockTime.T,
		dbA.IsFunding, dbA.ValidMainChain, dbA.TxType).Scan(&id)
	return id, err
}

// InsertAddressRows inserts multiple transaction inputs or outputs for certain
// addresses ([]AddressRow). The row IDs of the inserted data are returned.
func InsertAddressRows(db *sql.DB, dbAs []*dbtypes.AddressRow, dupCheck, updateExistingRecords bool) ([]uint64, error) {
	// Begin a new transaction.
	dbtx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("unable to begin database transaction: %v", err)
	}

	// Prepare the addresses row insert statement.
	stmt, err := dbtx.Prepare(internal.MakeAddressRowInsertStatement(dupCheck, updateExistingRecords))
	if err != nil {
		log.Errorf("AddressRow INSERT prepare: %v", err)
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, err
	}

	// Insert each addresses table row, storing the inserted row IDs.
	ids := make([]uint64, 0, len(dbAs))
	for _, dbA := range dbAs {
		var id uint64
		err := stmt.QueryRow(dbA.Address, dbA.MatchingTxHash, dbA.TxHash,
			dbA.TxVinVoutIndex, dbA.VinVoutDbID, dbA.Value, dbA.TxBlockTime.T,
			dbA.IsFunding, dbA.ValidMainChain, dbA.TxType).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				log.Errorf("failed to insert/update an AddressRow: %v", *dbA)
				continue
			}
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbtx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return nil, err
		}
		ids = append(ids, id)
	}

	// Close prepared statement. Ignore errors as we'll Commit regardless.
	_ = stmt.Close()

	return ids, dbtx.Commit()
}

func RetrieveAddressUnspent(ctx context.Context, db *sql.DB, address string) (count, totalAmount int64, err error) {
	err = db.QueryRowContext(ctx, internal.SelectAddressUnspentCountANDValue, address).
		Scan(&count, &totalAmount)
	return
}

func RetrieveAddressSpent(ctx context.Context, db *sql.DB, address string) (count, totalAmount int64, err error) {
	err = db.QueryRowContext(ctx, internal.SelectAddressSpentCountANDValue, address).
		Scan(&count, &totalAmount)
	return
}

// retrieveAddressTxsCount return the number of record groups, where grouping is
// done by a specified time interval, for an address.
func retrieveAddressTxsCount(ctx context.Context, db *sql.DB, address, interval string) (count int64, err error) {
	err = db.QueryRowContext(ctx, internal.MakeSelectAddressTimeGroupingCount(interval), address).Scan(&count)
	return
}

// RetrieveAddressSpentUnspent gets the numbers of spent and unspent outpoints
// for the given address, the total amounts spent and unspent, and the the
// number of distinct spending transactions.
func RetrieveAddressSpentUnspent(ctx context.Context, db *sql.DB, address string) (numSpent, numUnspent,
	amtSpent, amtUnspent, numMergedSpent int64, err error) {
	// The sql.Tx does not have a timeout, as the individial queries will.
	var dbtx *sql.Tx
	dbtx, err = db.BeginTx(context.Background(), &sql.TxOptions{
		Isolation: sql.LevelDefault,
		ReadOnly:  true,
	})
	if err != nil {
		err = fmt.Errorf("unable to begin database transaction: %v", err)
		return
	}

	// Query for spent and unspent totals.
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectAddressSpentUnspentCountAndValue, address)
	if err != nil && err != sql.ErrNoRows {
		if errRoll := dbtx.Rollback(); errRoll != nil {
			log.Errorf("Rollback failed: %v", errRoll)
		}
		err = fmt.Errorf("failed to query spent and unspent amounts: %v", err)
		return
	}
	if err == sql.ErrNoRows {
		_ = dbtx.Commit()
		return
	}

	for rows.Next() {
		var count, totalValue int64
		var noMatchingTx, isFunding bool
		err = rows.Scan(&count, &totalValue, &isFunding, &noMatchingTx)
		if err != nil {
			break
		}

		// Unspent == funding with no matching transaction
		if isFunding && noMatchingTx {
			numUnspent = count
			amtUnspent = totalValue
		}
		// Spent == spending (but ensure a matching transaction is set)
		if !isFunding {
			if noMatchingTx {
				log.Errorf("Found spending transactions with matching_tx_hash"+
					" unset for %s!", address)
				continue
			}
			numSpent = count
			amtSpent = totalValue
		}
	}
	closeRows(rows)

	// Query for spending transaction count, repeated transaction hashes merged.
	var nms sql.NullInt64
	err = dbtx.QueryRowContext(ctx, internal.SelectAddressesMergedSpentCount, address).
		Scan(&nms)
	if err != nil && err != sql.ErrNoRows {
		if errRoll := dbtx.Rollback(); errRoll != nil {
			log.Errorf("Rollback failed: %v", errRoll)
		}
		err = fmt.Errorf("failed to query merged spent count: %v", err)
		return
	}

	numMergedSpent = nms.Int64
	if !nms.Valid {
		log.Debug("Merged debit spent count is not valid")
	}

	err = dbtx.Commit()
	return
}

// RetrieveAddressUTXOs gets the unspent transaction outputs (UTXOs) paying to
// the specified address. The input current block height is used to compute
// confirmations of the located transactions.
func RetrieveAddressUTXOs(ctx context.Context, db *sql.DB, address string, currentBlockHeight int64) ([]apitypes.AddressTxnOutput, error) {
	stmt, err := db.Prepare(internal.SelectAddressUnspentWithTxn)
	if err != nil {
		log.Error(err)
		return nil, err
	}

	rows, err := stmt.QueryContext(ctx, address)
	// _ = stmt.Close() // or does Rows.Close() do it?
	if err != nil {
		log.Error(err)
		return nil, err
	}
	defer closeRows(rows)

	var outputs []apitypes.AddressTxnOutput
	for rows.Next() {
		pkScript := []byte{}
		var blockHeight, atoms int64
		var blocktime dbtypes.TimeDef
		txnOutput := apitypes.AddressTxnOutput{}
		if err = rows.Scan(&txnOutput.Address, &txnOutput.TxnID,
			&atoms, &blockHeight, &blocktime.T, &txnOutput.Vout, &pkScript); err != nil {
			log.Error(err)
		}
		txnOutput.ScriptPubKey = hex.EncodeToString(pkScript)
		txnOutput.Amount = dcrutil.Amount(atoms).ToCoin()
		txnOutput.Satoshis = atoms
		txnOutput.Height = blockHeight
		txnOutput.Confirmations = currentBlockHeight - blockHeight + 1
		outputs = append(outputs, txnOutput)
	}

	return outputs, nil
}

// RetrieveAddressTxnsOrdered will get all transactions for addresses provided
// and return them sorted by time in descending order. It will also return a
// short list of recently (defined as greater than recentBlockHeight) confirmed
// transactions that can be used to validate mempool status.
func RetrieveAddressTxnsOrdered(ctx context.Context, db *sql.DB, addresses []string, recentBlockHeight int64) (txs []string, recenttxs []string, err error) {
	var txHash string
	var height int64
	var stmt *sql.Stmt
	stmt, err = db.Prepare(internal.SelectAddressesAllTxn)
	if err != nil {
		return nil, nil, err
	}

	var rows *sql.Rows
	rows, err = stmt.QueryContext(ctx, pq.Array(addresses))
	// _ = stmt.Close() // or does Rows.Close do it?
	if err != nil {
		return nil, nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		err = rows.Scan(&txHash, &height)
		if err != nil {
			return // return what we got, plus the error
		}
		txs = append(txs, txHash)
		if height > recentBlockHeight {
			recenttxs = append(recenttxs, txHash)
		}
	}
	return
}

// RetrieveAllAddressTxns retrieves all rows of the address table pertaining to
// the given address.
func RetrieveAllAddressTxns(ctx context.Context, db *sql.DB, address string) ([]uint64, []*dbtypes.AddressRow, error) {
	rows, err := db.QueryContext(ctx, internal.SelectAddressAllByAddress, address)
	if err != nil {
		return nil, nil, err
	}

	defer closeRows(rows)

	return scanAddressQueryRows(rows)
}

func RetrieveAddressTxns(ctx context.Context, db *sql.DB, address string, N, offset int64) ([]uint64, []*dbtypes.AddressRow, error) {
	return retrieveAddressTxns(ctx, db, address, N, offset,
		internal.SelectAddressLimitNByAddress, false)
}

func RetrieveAddressDebitTxns(ctx context.Context, db *sql.DB, address string, N, offset int64) ([]uint64, []*dbtypes.AddressRow, error) {
	return retrieveAddressTxns(ctx, db, address, N, offset,
		internal.SelectAddressDebitsLimitNByAddress, false)
}

func RetrieveAddressCreditTxns(ctx context.Context, db *sql.DB, address string, N, offset int64) ([]uint64, []*dbtypes.AddressRow, error) {
	return retrieveAddressTxns(ctx, db, address, N, offset,
		internal.SelectAddressCreditsLimitNByAddress, false)
}

func RetrieveAddressMergedDebitTxns(ctx context.Context, db *sql.DB, address string, N, offset int64) ([]uint64, []*dbtypes.AddressRow, error) {
	return retrieveAddressTxns(ctx, db, address, N, offset,
		internal.SelectAddressMergedDebitView, true)
}

func retrieveAddressTxns(ctx context.Context, db *sql.DB, address string, N, offset int64,
	statement string, isMergedDebitView bool) ([]uint64, []*dbtypes.AddressRow, error) {
	rows, err := db.QueryContext(ctx, statement, address, N, offset)
	if err != nil {
		return nil, nil, err
	}

	defer closeRows(rows)

	if isMergedDebitView {
		addr, err := scanPartialAddressQueryRows(rows, address)
		return nil, addr, err
	}
	return scanAddressQueryRows(rows)
}

func scanPartialAddressQueryRows(rows *sql.Rows, addr string) (addressRows []*dbtypes.AddressRow, err error) {
	for rows.Next() {
		var addr = dbtypes.AddressRow{Address: addr}
		var blockTime dbtypes.TimeDef

		err = rows.Scan(&addr.TxHash, &addr.ValidMainChain, &blockTime.T,
			&addr.Value, &addr.MergedDebitCount)
		addr.TxBlockTime = blockTime
		if err != nil {
			return
		}
		addressRows = append(addressRows, &addr)
	}
	return
}

func scanAddressQueryRows(rows *sql.Rows) (ids []uint64, addressRows []*dbtypes.AddressRow, err error) {
	for rows.Next() {
		var id uint64
		var addr dbtypes.AddressRow
		var txHash sql.NullString
		var blockTime dbtypes.TimeDef
		var txVinIndex, vinDbID sql.NullInt64
		// Scan values in order of columns listed in internal.addrsColumnNames
		err = rows.Scan(&id, &addr.Address, &addr.MatchingTxHash, &txHash, &addr.TxType,
			&addr.ValidMainChain, &txVinIndex, &blockTime.T, &vinDbID,
			&addr.Value, &addr.IsFunding)
		if err != nil {
			return
		}

		addr.TxBlockTime = blockTime

		if txHash.Valid {
			addr.TxHash = txHash.String
		}
		if txVinIndex.Valid {
			addr.TxVinVoutIndex = uint32(txVinIndex.Int64)
		}
		if vinDbID.Valid {
			addr.VinVoutDbID = uint64(vinDbID.Int64)
		}

		ids = append(ids, id)
		addressRows = append(addressRows, &addr)
	}
	return
}

// RetrieveAddressIDsByOutpoint fetches all address row IDs for a given outpoint
// (hash:index).
// Update Vin due to DCRD AMOUNTIN - START - DO NOT MERGE CHANGES IF DCRD FIXED
func RetrieveAddressIDsByOutpoint(ctx context.Context, db *sql.DB, txHash string, voutIndex uint32) ([]uint64, []string, int64, error) {
	var ids []uint64
	var addresses []string
	var value int64
	rows, err := db.QueryContext(ctx, internal.SelectAddressIDsByFundingOutpoint, txHash, voutIndex)
	if err != nil {
		return ids, addresses, 0, err
	}

	defer closeRows(rows)

	for rows.Next() {
		var id uint64
		var addr string
		err = rows.Scan(&id, &addr, &value)
		if err != nil {
			break
		}

		ids = append(ids, id)
		addresses = append(addresses, addr)
	}
	return ids, addresses, value, err
} // Update Vin due to DCRD AMOUNTIN - END

// retrieveOldestTxBlockTime helps choose the most appropriate address page
// graph grouping to load by default depending on when the first transaction to
// the specific address was made.
func retrieveOldestTxBlockTime(ctx context.Context, db *sql.DB, addr string) (blockTime dbtypes.TimeDef, err error) {
	err = db.QueryRowContext(ctx, internal.SelectAddressOldestTxBlockTime, addr).Scan(&blockTime.T)
	return
}

// retrieveTxHistoryByType fetches the transaction types count for all the
// transactions associated with a given address for the given time interval.
// The time interval is grouping records by week, month, year, day and all.
// For all time interval, transactions are grouped by the unique
// timestamps (blocks) available.
func retrieveTxHistoryByType(ctx context.Context, db *sql.DB, addr, timeInterval string) (*dbtypes.ChartsData, error) {
	rows, err := db.QueryContext(ctx, internal.MakeSelectAddressTxTypesByAddress(timeInterval),
		addr)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	items := new(dbtypes.ChartsData)
	for rows.Next() {
		var blockTime dbtypes.TimeDef
		var sentRtx, receivedRtx, tickets, votes, revokeTx uint64
		err = rows.Scan(&blockTime.T, &sentRtx, &receivedRtx, &tickets, &votes, &revokeTx)
		if err != nil {
			return nil, err
		}

		items.Time = append(items.Time, blockTime)
		items.SentRtx = append(items.SentRtx, sentRtx)
		items.ReceivedRtx = append(items.ReceivedRtx, receivedRtx)
		items.Tickets = append(items.Tickets, tickets)
		items.Votes = append(items.Votes, votes)
		items.RevokeTx = append(items.RevokeTx, revokeTx)
	}
	return items, nil
}

// retrieveTxHistoryByAmount fetches the transaction amount flow i.e. received
// and sent amount for all the transactions associated with a given address and for
// the given time interval. The time interval is grouping records by week,
// month, year, day and all. For all time interval, transactions are grouped by
// the unique timestamps (blocks) available.
func retrieveTxHistoryByAmountFlow(ctx context.Context, db *sql.DB, addr, timeInterval string) (*dbtypes.ChartsData, error) {
	var items = new(dbtypes.ChartsData)

	rows, err := db.QueryContext(ctx, internal.MakeSelectAddressAmountFlowByAddress(timeInterval), addr)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	for rows.Next() {
		var blockTime dbtypes.TimeDef
		var received, sent uint64
		err = rows.Scan(&blockTime.T, &received, &sent)
		if err != nil {
			return nil, err
		}

		items.Time = append(items.Time, blockTime)
		items.Received = append(items.Received, dcrutil.Amount(received).ToCoin())
		items.Sent = append(items.Sent, dcrutil.Amount(sent).ToCoin())
		// Net represents the difference between the received and sent amount for a
		// given block. If the difference is positive then the value is unspent amount
		// otherwise if the value is zero then all amount is spent and if the net amount
		// is negative then for the given block more amount was sent than received.
		items.Net = append(items.Net, dcrutil.Amount(received-sent).ToCoin())
	}
	return items, nil
}

// retrieveTxHistoryByUnspentAmount fetches the unspent amount for all the
// transactions associated with a given address for the given time interval.
// The time interval is grouping records by week, month, year, day and all.
// For all time interval, transactions are grouped by the unique
// timestamps (blocks) available.
func retrieveTxHistoryByUnspentAmount(ctx context.Context, db *sql.DB, addr, timeInterval string) (*dbtypes.ChartsData, error) {
	var totalAmount uint64
	var items = new(dbtypes.ChartsData)

	rows, err := db.QueryContext(ctx, internal.MakeSelectAddressUnspentAmountByAddress(timeInterval), addr)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	for rows.Next() {
		var blockTime dbtypes.TimeDef
		var amount uint64
		err = rows.Scan(&blockTime.T, &amount)
		if err != nil {
			return nil, err
		}

		items.Time = append(items.Time, blockTime)

		// Return commmulative amount data for the unspent chart type
		totalAmount += amount
		items.Amount = append(items.Amount, dcrutil.Amount(totalAmount).ToCoin())
	}
	return items, nil
}

// --- vins and vouts tables ---

// InsertVin either inserts, attempts to insert, or upserts the given vin data
// into the vins table. If checked=false, an unconditional insert as attempted,
// which may result in a violation of a unique index constraint (error). If
// checked=true, a constraint violation may be handled in one of two ways:
// update the conflicting row (upsert), or do nothing. In all cases, the id of
// the new/updated/conflicting row is returned. The updateOnConflict argumenet
// may be omitted, in which case an upsert will be favored over no nothing, but
// only if checked=true.
func InsertVin(db *sql.DB, dbVin dbtypes.VinTxProperty, checked bool, updateOnConflict ...bool) (id uint64, err error) {
	doUpsert := true
	if len(updateOnConflict) > 0 {
		doUpsert = updateOnConflict[0]
	}
	err = db.QueryRow(internal.MakeVinInsertStatement(checked, doUpsert),
		dbVin.TxID, dbVin.TxIndex, dbVin.TxTree,
		dbVin.PrevTxHash, dbVin.PrevTxIndex, dbVin.PrevTxTree,
		dbVin.ValueIn, dbVin.IsValid, dbVin.IsMainchain, dbVin.Time.T,
		dbVin.TxType).Scan(&id)
	return
}

// InsertVins is like InsertVin, except that it operates on a slice of vin data.
func InsertVins(db *sql.DB, dbVins dbtypes.VinTxPropertyARRAY, checked bool, updateOnConflict ...bool) ([]uint64, error) {
	dbtx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("unable to begin database transaction: %v", err)
	}

	doUpsert := true
	if len(updateOnConflict) > 0 {
		doUpsert = updateOnConflict[0]
	}
	stmt, err := dbtx.Prepare(internal.MakeVinInsertStatement(checked, doUpsert))
	if err != nil {
		log.Errorf("Vin INSERT prepare: %v", err)
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, err
	}

	// TODO/Question: Should we skip inserting coinbase txns, which have same PrevTxHash?

	ids := make([]uint64, 0, len(dbVins))
	for _, vin := range dbVins {
		var id uint64
		err = stmt.QueryRow(vin.TxID, vin.TxIndex, vin.TxTree,
			vin.PrevTxHash, vin.PrevTxIndex, vin.PrevTxTree,
			vin.ValueIn, vin.IsValid, vin.IsMainchain, vin.Time.T, vin.TxType).Scan(&id)
		if err != nil {
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbtx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return ids, fmt.Errorf("InsertVins INSERT exec failed: %v", err)
		}
		ids = append(ids, id)
	}

	// Close prepared statement. Ignore errors as we'll Commit regardless.
	_ = stmt.Close()

	return ids, dbtx.Commit()
}

// InsertVout either inserts, attempts to insert, or upserts the given vout data
// into the vouts table. If checked=false, an unconditional insert as attempted,
// which may result in a violation of a unique index constraint (error). If
// checked=true, a constraint violation may be handled in one of two ways:
// update the conflicting row (upsert), or do nothing. In all cases, the id of
// the new/updated/conflicting row is returned. The updateOnConflict argumenet
// may be omitted, in which case an upsert will be favored over no nothing, but
// only if checked=true.
func InsertVout(db *sql.DB, dbVout *dbtypes.Vout, checked bool, updateOnConflict ...bool) (uint64, error) {
	doUpsert := true
	if len(updateOnConflict) > 0 {
		doUpsert = updateOnConflict[0]
	}
	insertStatement := internal.MakeVoutInsertStatement(checked, doUpsert)
	var id uint64
	err := db.QueryRow(insertStatement,
		dbVout.TxHash, dbVout.TxIndex, dbVout.TxTree,
		dbVout.Value, dbVout.Version,
		dbVout.ScriptPubKey, dbVout.ScriptPubKeyData.ReqSigs,
		dbVout.ScriptPubKeyData.Type,
		pq.Array(dbVout.ScriptPubKeyData.Addresses)).Scan(&id)
	return id, err
}

// InsertVouts is like InsertVout, except that it operates on a slice of vout
// data.
func InsertVouts(db *sql.DB, dbVouts []*dbtypes.Vout, checked bool, updateOnConflict ...bool) ([]uint64, []dbtypes.AddressRow, error) {
	// All inserts in atomic DB transaction
	dbtx, err := db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to begin database transaction: %v", err)
	}

	doUpsert := true
	if len(updateOnConflict) > 0 {
		doUpsert = updateOnConflict[0]
	}
	stmt, err := dbtx.Prepare(internal.MakeVoutInsertStatement(checked, doUpsert))
	if err != nil {
		log.Errorf("Vout INSERT prepare: %v", err)
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, nil, err
	}

	addressRows := make([]dbtypes.AddressRow, 0, len(dbVouts)) // may grow with multisig
	ids := make([]uint64, 0, len(dbVouts))
	for _, vout := range dbVouts {
		var id uint64
		err = stmt.QueryRow(
			vout.TxHash, vout.TxIndex, vout.TxTree, vout.Value, vout.Version,
			vout.ScriptPubKey, vout.ScriptPubKeyData.ReqSigs,
			vout.ScriptPubKeyData.Type,
			pq.Array(vout.ScriptPubKeyData.Addresses)).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbtx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return nil, nil, err
		}
		for _, addr := range vout.ScriptPubKeyData.Addresses {
			addressRows = append(addressRows, dbtypes.AddressRow{
				Address:        addr,
				TxHash:         vout.TxHash,
				TxVinVoutIndex: vout.TxIndex,
				VinVoutDbID:    id,
				TxType:         vout.TxType,
				Value:          vout.Value,
				// Not set here are: ValidMainchain, MatchingTxHash, IsFunding,
				// and TxBlockTime.
			})
		}
		ids = append(ids, id)
	}

	// Close prepared statement. Ignore errors as we'll Commit regardless.
	_ = stmt.Close()

	return ids, addressRows, dbtx.Commit()
}

func RetrievePkScriptByVinID(ctx context.Context, db *sql.DB, vinID uint64) (pkScript []byte, ver uint16, err error) {
	err = db.QueryRowContext(ctx, internal.SelectPkScriptByVinID, vinID).Scan(&ver, &pkScript)
	return
}

func RetrievePkScriptByVoutID(ctx context.Context, db *sql.DB, voutID uint64) (pkScript []byte, ver uint16, err error) {
	err = db.QueryRowContext(ctx, internal.SelectPkScriptByID, voutID).Scan(&ver, &pkScript)
	return
}

func RetrievePkScriptByOutpoint(ctx context.Context, db *sql.DB, txHash string, voutIndex uint32) (pkScript []byte, ver uint16, err error) {
	err = db.QueryRowContext(ctx, internal.SelectPkScriptByOutpoint, txHash, voutIndex).Scan(&ver, &pkScript)
	return
}

func RetrieveVoutIDByOutpoint(ctx context.Context, db *sql.DB, txHash string, voutIndex uint32) (id uint64, err error) {
	err = db.QueryRowContext(ctx, internal.SelectVoutIDByOutpoint, txHash, voutIndex).Scan(&id)
	return
}

func RetrieveVoutValue(ctx context.Context, db *sql.DB, txHash string, voutIndex uint32) (value uint64, err error) {
	err = db.QueryRowContext(ctx, internal.RetrieveVoutValue, txHash, voutIndex).Scan(&value)
	return
}

func RetrieveVoutValues(ctx context.Context, db *sql.DB, txHash string) (values []uint64, txInds []uint32, txTrees []int8, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.RetrieveVoutValues, txHash)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var v uint64
		var ind uint32
		var tree int8
		err = rows.Scan(&v, &ind, &tree)
		if err != nil {
			break
		}

		values = append(values, v)
		txInds = append(txInds, ind)
		txTrees = append(txTrees, tree)
	}

	return
}

// RetrieveAllVinDbIDs gets every row ID (the primary keys) for the vins table.
// This function is used in UpdateSpendingInfoInAllAddresses, so it should not
// be subject to timeouts.
func RetrieveAllVinDbIDs(db *sql.DB) (vinDbIDs []uint64, err error) {
	var rows *sql.Rows
	rows, err = db.Query(internal.SelectVinIDsALL)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var id uint64
		err = rows.Scan(&id)
		if err != nil {
			break
		}

		vinDbIDs = append(vinDbIDs, id)
	}

	return
}

// RetrieveFundingOutpointByTxIn gets the previous outpoint for a transaction
// input specified by transaction hash and input index.
func RetrieveFundingOutpointByTxIn(ctx context.Context, db *sql.DB, txHash string,
	vinIndex uint32) (id uint64, tx string, index uint32, tree int8, err error) {
	err = db.QueryRowContext(ctx, internal.SelectFundingOutpointByTxIn, txHash, vinIndex).
		Scan(&id, &tx, &index, &tree)
	return
}

// RetrieveFundingOutpointByVinID gets the previous outpoint for a transaction
// input specified by row ID in the vins table.
func RetrieveFundingOutpointByVinID(ctx context.Context, db *sql.DB, vinDbID uint64) (tx string, index uint32, tree int8, err error) {
	err = db.QueryRowContext(ctx, internal.SelectFundingOutpointByVinID, vinDbID).
		Scan(&tx, &index, &tree)
	return
}

// RetrieveFundingOutpointIndxByVinID gets the transaction output index of the
// previous outpoint for a transaction input specified by row ID in the vins
// table.
func RetrieveFundingOutpointIndxByVinID(ctx context.Context, db *sql.DB, vinDbID uint64) (idx uint32, err error) {
	err = db.QueryRowContext(ctx, internal.SelectFundingOutpointIndxByVinID, vinDbID).Scan(&idx)
	return
}

// RetrieveFundingTxByTxIn gets the transaction hash of the previous outpoint
// for a transaction input specified by hash and input index.
func RetrieveFundingTxByTxIn(ctx context.Context, db *sql.DB, txHash string, vinIndex uint32) (id uint64, tx string, err error) {
	err = db.QueryRowContext(ctx, internal.SelectFundingTxByTxIn, txHash, vinIndex).
		Scan(&id, &tx)
	return
}

// RetrieveFundingTxByVinDbID gets the transaction hash of the previous outpoint
// for a transaction input specified by row ID in the vins table. This function
// is used only in UpdateSpendingInfoInAllTickets, so it should not be subject
// to timeouts.
func RetrieveFundingTxByVinDbID(ctx context.Context, db *sql.DB, vinDbID uint64) (tx string, err error) {
	err = db.QueryRowContext(ctx, internal.SelectFundingTxByVinID, vinDbID).Scan(&tx)
	return
}

// TODO: this does not appear correct.
func RetrieveFundingTxsByTx(db *sql.DB, txHash string) ([]uint64, []*dbtypes.Tx, error) {
	var ids []uint64
	var txs []*dbtypes.Tx
	rows, err := db.Query(internal.SelectFundingTxsByTx, txHash)
	if err != nil {
		return ids, txs, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var id uint64
		var tx dbtypes.Tx
		err = rows.Scan(&id, &tx)
		if err != nil {
			break
		}

		ids = append(ids, id)
		txs = append(txs, &tx)
	}

	return ids, txs, err
}

// RetrieveSpendingTxByVinID gets the spending transaction input (hash, vin
// number, and tx tree) for the transaction input specified by row ID in the
// vins table.
func RetrieveSpendingTxByVinID(ctx context.Context, db *sql.DB, vinDbID uint64) (tx string,
	vinIndex uint32, tree int8, err error) {
	err = db.QueryRowContext(ctx, internal.SelectSpendingTxByVinID, vinDbID).
		Scan(&tx, &vinIndex, &tree)
	return
}

// RetrieveSpendingTxByTxOut gets any spending transaction input info for a
// previous outpoint specified by funding transaction hash and vout number. This
// function is called by SpendingTransaction, an important part of the address
// page loading.
func RetrieveSpendingTxByTxOut(ctx context.Context, db *sql.DB, txHash string,
	voutIndex uint32) (id uint64, tx string, vin uint32, tree int8, err error) {
	err = db.QueryRowContext(ctx, internal.SelectSpendingTxByPrevOut,
		txHash, voutIndex).Scan(&id, &tx, &vin, &tree)
	return
}

// RetrieveSpendingTxsByFundingTx gets info on all spending transaction inputs
// for the given funding transaction specified by DB row ID. This function is
// called by SpendingTransactions, an important part of the transaction page
// loading, among other functions..
func RetrieveSpendingTxsByFundingTx(ctx context.Context, db *sql.DB, fundingTxID string) (dbIDs []uint64,
	txns []string, vinInds []uint32, voutInds []uint32, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectSpendingTxsByPrevTx, fundingTxID)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var id uint64
		var tx string
		var vin, vout uint32
		err = rows.Scan(&id, &tx, &vin, &vout)
		if err != nil {
			break
		}

		dbIDs = append(dbIDs, id)
		txns = append(txns, tx)
		vinInds = append(vinInds, vin)
		voutInds = append(voutInds, vout)
	}

	return
}

// RetrieveSpendingTxsByFundingTxWithBlockHeight will retrieve all transactions,
// indexes and block heights funded by a specific transaction. This function is
// used by the DCR to Insight transaction converter.
func RetrieveSpendingTxsByFundingTxWithBlockHeight(ctx context.Context, db *sql.DB, fundingTxID string) (aSpendByFunHash []*apitypes.SpendByFundingHash, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectSpendingTxsByPrevTxWithBlockHeight, fundingTxID)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var addr apitypes.SpendByFundingHash
		err = rows.Scan(&addr.FundingTxVoutIndex,
			&addr.SpendingTxHash, &addr.SpendingTxVinIndex, &addr.BlockHeight)
		if err != nil {
			return
		}

		aSpendByFunHash = append(aSpendByFunHash, &addr)
	}
	return
}

// RetrieveVinByID gets from the vins table for the provided row ID.
func RetrieveVinByID(ctx context.Context, db *sql.DB, vinDbID uint64) (prevOutHash string, prevOutVoutInd uint32,
	prevOutTree int8, txHash string, txVinInd uint32, txTree int8, valueIn int64, err error) {
	var blockTime dbtypes.TimeDef
	var isValid, isMainchain bool
	var txType uint32
	err = db.QueryRowContext(ctx, internal.SelectAllVinInfoByID, vinDbID).
		Scan(&txHash, &txVinInd, &txTree, &isValid, &isMainchain, &blockTime.T,
			&prevOutHash, &prevOutVoutInd, &prevOutTree, &valueIn, &txType)
	return
}

// RetrieveVinsByIDs retrieves vin details for the rows of the vins table
// specified by the provided row IDs. This function is an important part of the
// transaction page.
func RetrieveVinsByIDs(ctx context.Context, db *sql.DB, vinDbIDs []uint64) ([]dbtypes.VinTxProperty, error) {
	vins := make([]dbtypes.VinTxProperty, len(vinDbIDs))
	for i, id := range vinDbIDs {
		vin := &vins[i]
		err := db.QueryRowContext(ctx, internal.SelectAllVinInfoByID, id).Scan(&vin.TxID,
			&vin.TxIndex, &vin.TxTree, &vin.IsValid, &vin.IsMainchain,
			&vin.Time.T, &vin.PrevTxHash, &vin.PrevTxIndex, &vin.PrevTxTree,
			&vin.ValueIn, &vin.TxType)
		if err != nil {
			return nil, err
		}
	}
	return vins, nil
}

// RetrieveVoutsByIDs retrieves vout details for the rows of the vouts table
// specified by the provided row IDs. This function is an important part of the
// transaction page.
func RetrieveVoutsByIDs(ctx context.Context, db *sql.DB, voutDbIDs []uint64) ([]dbtypes.Vout, error) {
	vouts := make([]dbtypes.Vout, len(voutDbIDs))
	for i, id := range voutDbIDs {
		vout := &vouts[i]
		var id0 uint64
		var reqSigs uint32
		var scriptType, addresses string
		err := db.QueryRowContext(ctx, internal.SelectVoutByID, id).Scan(&id0, &vout.TxHash,
			&vout.TxIndex, &vout.TxTree, &vout.Value, &vout.Version,
			&vout.ScriptPubKey, &reqSigs, &scriptType, &addresses)
		if err != nil {
			return nil, err
		}
		// Parse the addresses array
		replacer := strings.NewReplacer("{", "", "}", "")
		addresses = replacer.Replace(addresses)

		vout.ScriptPubKeyData.ReqSigs = reqSigs
		vout.ScriptPubKeyData.Type = scriptType
		// If there are no addresses, the Addresses should be nil or length
		// zero. However, strings.Split will return [""] if addresses is "".
		// If that is the case, leave it as a nil slice.
		if len(addresses) > 0 {
			vout.ScriptPubKeyData.Addresses = strings.Split(addresses, ",")
		}
	}
	return vouts, nil
}

// SetSpendingForVinDbIDs updates rows of the addresses table with spending
// information from the rows of the vins table specified by vinDbIDs. This does
// not insert the spending transaction into the addresses table.
func SetSpendingForVinDbIDs(db *sql.DB, vinDbIDs []uint64) ([]int64, int64, error) {
	// Get funding details for vin and set them in the address table.
	dbtx, err := db.Begin()
	if err != nil {
		return nil, 0, fmt.Errorf(`unable to begin database transaction: %v`, err)
	}

	var vinGetStmt *sql.Stmt
	vinGetStmt, err = dbtx.Prepare(internal.SelectVinVoutPairByID)
	if err != nil {
		log.Errorf("Vin SELECT prepare failed: %v", err)
		// Already up a creek. Just return error from Prepare.
		_ = dbtx.Rollback()
		return nil, 0, err
	}

	bail := func() error {
		// Already up a creek. Just return error from Prepare.
		_ = vinGetStmt.Close()
		return dbtx.Rollback()
	}

	addressRowsUpdated := make([]int64, len(vinDbIDs))
	var totalUpdated int64

	for iv, vinDbID := range vinDbIDs {
		// Get the funding tx outpoint from the vins table.
		var prevOutHash, txHash string
		var prevOutVoutInd, txVinInd uint32
		err = vinGetStmt.QueryRow(vinDbID).Scan(
			&txHash, &txVinInd, &prevOutHash, &prevOutVoutInd)
		if err != nil {
			return addressRowsUpdated, 0, fmt.Errorf(`SelectVinVoutPairByID: `+
				`%v + %v (rollback)`, err, bail())
		}

		// Skip coinbase inputs.
		if bytes.Equal(zeroHashStringBytes, []byte(prevOutHash)) {
			continue
		}

		// Set the spending tx info (addresses table) for the funding transaction
		// rows indicated by the vin DB ID.
		addressRowsUpdated[iv], err = setSpendingForFundingOP(dbtx,
			prevOutHash, prevOutVoutInd, txHash, txVinInd)
		if err != nil {
			return addressRowsUpdated, 0, fmt.Errorf(`insertSpendingTxByPrptStmt: `+
				`%v + %v (rollback)`, err, bail())
		}

		totalUpdated += addressRowsUpdated[iv]
	}

	// Close prepared statements. Ignore errors as we'll Commit regardless.
	_ = vinGetStmt.Close()

	return addressRowsUpdated, totalUpdated, dbtx.Commit()
}

// SetSpendingForVinDbIDs updates rows of the addresses table with spending
// information from the row of the vins table specified by vinDbID. This does
// not insert the spending transaction into the addresses table.
func SetSpendingForVinDbID(db *sql.DB, vinDbID uint64) (int64, error) {
	// Get funding details for the vin and set them in the address table.
	dbtx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf(`unable to begin database transaction: %v`, err)
	}

	// Get the funding tx outpoint from the vins table.
	var prevOutHash, txHash string
	var prevOutVoutInd, txVinInd uint32
	err = dbtx.QueryRow(internal.SelectVinVoutPairByID, vinDbID).
		Scan(&txHash, &txVinInd, &prevOutHash, &prevOutVoutInd)
	if err != nil {
		return 0, fmt.Errorf(`SetSpendingByVinID: %v + %v `+
			`(rollback)`, err, dbtx.Rollback())
	}

	// Skip coinbase inputs.
	if bytes.Equal(zeroHashStringBytes, []byte(prevOutHash)) {
		return 0, dbtx.Rollback()
	}

	// Set the spending tx info (addresses table) for the funding transaction
	// rows indicated by the vin DB ID.
	N, err := setSpendingForFundingOP(dbtx, prevOutHash, prevOutVoutInd,
		txHash, txVinInd)
	if err != nil {
		return 0, fmt.Errorf(`RowsAffected: %v + %v (rollback)`,
			err, dbtx.Rollback())
	}

	return N, dbtx.Commit()
}

// SetSpendingForFundingOP updates funding rows of the addresses table with the
// provided spending transaction output info.
func SetSpendingForFundingOP(db *sql.DB, fundingTxHash string, fundingTxVoutIndex uint32,
	spendingTxHash string, _ /*spendingTxVinIndex*/ uint32) (int64, error) {
	// Update the matchingTxHash for the funding tx output. matchingTxHash here
	// is the hash of the funding tx.
	res, err := db.Exec(internal.SetAddressMatchingTxHashForOutpoint,
		spendingTxHash, fundingTxHash, fundingTxVoutIndex)
	if err != nil || res == nil {
		return 0, fmt.Errorf("SetAddressMatchingTxHashForOutpoint: %v", err)
	}

	return res.RowsAffected()
}

func setSpendingForFundingOP(dbtx *sql.Tx, fundingTxHash string, fundingTxVoutIndex uint32,
	spendingTxHash string, _ /*spendingTxVinIndex*/ uint32) (int64, error) {
	// Update the matchingTxHash for the funding tx output. matchingTxHash here
	// is the hash of the funding tx.
	res, err := dbtx.Exec(internal.SetAddressMatchingTxHashForOutpoint,
		spendingTxHash, fundingTxHash, fundingTxVoutIndex)
	if err != nil || res == nil {
		return 0, fmt.Errorf("SetAddressMatchingTxHashForOutpoint: %v", err)
	}

	return res.RowsAffected()
}

// InsertSpendingAddressRow inserts a new spending tx row, and updates any
// corresponding funding tx row.
func InsertSpendingAddressRow(db *sql.DB, fundingTxHash string,
	fundingTxVoutIndex uint32, fundingTxTree int8, spendingTxHash string,
	spendingTxVinIndex uint32, vinDbID uint64, utxoData *UTXOData, checked, updateExisting, isValidMainchain bool,
	txType int16, updateFundingRow bool, spendingTXBlockTime dbtypes.TimeDef) (int64, error) {
	// Only allow atomic transactions to happen
	dbtx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf(`unable to begin database transaction: %v`, err)
	}

	c, err := insertSpendingAddressRow(dbtx, fundingTxHash, fundingTxVoutIndex,
		fundingTxTree, spendingTxHash, spendingTxVinIndex, vinDbID, utxoData, checked,
		updateExisting, isValidMainchain, txType, updateFundingRow, spendingTXBlockTime)
	if err != nil {
		return 0, fmt.Errorf(`RowsAffected: %v + %v (rollback)`,
			err, dbtx.Rollback())
	}

	return c, dbtx.Commit()
}

// insertSpendingAddressRow inserts a new row in the addresses table for a new
// transaction input, and updates the spending information for the addresses
// table row corresponding to the previous outpoint.
func insertSpendingAddressRow(tx *sql.Tx, fundingTxHash string, fundingTxVoutIndex uint32,
	fundingTxTree int8, spendingTxHash string, spendingTxVinIndex uint32, vinDbID uint64,
	utxoData *UTXOData, checked, updateExisting, validMainchain bool, txType int16, updateFundingRow bool, blockT ...dbtypes.TimeDef) (int64, error) {

	// Select id, address and value from the matching funding tx.
	// A maximum of one row and a minimum of none are expected.
	var addr string
	var value uint64
	if utxoData == nil {
		// The addresses column of the vouts table contains an array of
		// addresses that the pkScript pays to (i.e. >1 for multisig).
		var addrArray string
		err := tx.QueryRow(internal.SelectAddressByTxHash,
			fundingTxHash, fundingTxVoutIndex, fundingTxTree).Scan(&addrArray, &value)
		switch err {
		case sql.ErrNoRows, nil:
			// If no row found or error is nil, continue
		default:
			return 0, fmt.Errorf("SelectAddressByTxHash: %v", err)
		}

		// Get first address in list.  TODO: actually handle bare multisig.
		replacer := strings.NewReplacer("{", "", "}", "")
		addrArray = replacer.Replace(addrArray)
		addr = strings.Split(addrArray, ",")[0]
	} else {
		addr = utxoData.Address
		value = uint64(utxoData.Value)
	}

	// Check if the block time was provided.
	var blockTime dbtypes.TimeDef
	if len(blockT) > 0 {
		blockTime = blockT[0]
	} else {
		// Fetch the block time from the tx table.
		err := tx.QueryRow(internal.SelectTxBlockTimeByHash, spendingTxHash).Scan(&blockTime.T)
		if err != nil {
			return 0, fmt.Errorf("SelectTxBlockTimeByHash: %v", err)
		}
	}

	// Insert the new spending tx input row.
	var isFunding bool
	var rowID uint64
	sqlStmt := internal.MakeAddressRowInsertStatement(checked, updateExisting)
	err := tx.QueryRow(sqlStmt, addr, fundingTxHash, spendingTxHash,
		spendingTxVinIndex, vinDbID, value, blockTime.T, isFunding,
		validMainchain, txType).Scan(&rowID)
	if err != nil {
		return 0, fmt.Errorf("InsertAddressRow: %v", err)
	}

	if updateFundingRow {
		// Update the matching funding addresses row with the spending info.
		return setSpendingForFundingOP(tx, fundingTxHash, fundingTxVoutIndex,
			spendingTxHash, spendingTxVinIndex)
	}
	return 0, nil
}

// retrieveCoinSupply fetches the coin supply data from the vins table.
func retrieveCoinSupply(ctx context.Context, db *sql.DB) (*dbtypes.ChartsData, error) {
	rows, err := db.QueryContext(ctx, internal.SelectCoinSupply)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var sum float64
	items := new(dbtypes.ChartsData)
	for rows.Next() {
		var value int64
		var timestamp dbtypes.TimeDef
		err = rows.Scan(&timestamp.T, &value)
		if err != nil {
			return nil, err
		}

		if value < 0 {
			value = 0
		}
		sum += dcrutil.Amount(value).ToCoin()
		items.Time = append(items.Time, timestamp)
		items.ValueF = append(items.ValueF, sum)
	}

	return items, nil
}

// --- agendas table ---

// retrieveAgendaVoteChoices retrieves for the specified agenda the vote counts
// for each choice and the total number of votes. The interval size is either a
// single block or a day, as specified by byType, where a value of 1 indicates a
// block and 0 indicates a day-long interval. For day intervals, the counts
// accumulate over time (cumulative sum), whereas for block intervals the counts
// are just for the block. The total length of time over all intervals always
// spans the locked-in period of the agenda.
func retrieveAgendaVoteChoices(ctx context.Context, db *sql.DB, agendaID string, byType int) (*dbtypes.AgendaVoteChoices, error) {
	// Query with block or day interval size
	var query = internal.SelectAgendasAgendaVotesByTime
	if byType == 1 {
		query = internal.SelectAgendasAgendaVotesByHeight
	}

	rows, err := db.QueryContext(ctx, query, dbtypes.Yes, dbtypes.Abstain, dbtypes.No,
		agendaID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	// Sum abstain, yes, no, and total votes
	var a, y, n, t uint64
	totalVotes := new(dbtypes.AgendaVoteChoices)
	for rows.Next() {
		var blockTime dbtypes.TimeDef
		var abstain, yes, no, total, height uint64
		if byType == 0 {
			err = rows.Scan(&blockTime.T, &yes, &abstain, &no, &total)
		} else {
			err = rows.Scan(&height, &yes, &abstain, &no, &total)
		}
		if err != nil {
			return nil, err
		}

		// For day intervals, counts are cumulative
		if byType == 0 {
			a += abstain
			y += yes
			n += no
			t += total
			totalVotes.Time = append(totalVotes.Time, blockTime)
		} else {
			a = abstain
			y = yes
			n = no
			t = total
			totalVotes.Height = append(totalVotes.Height, height)
		}

		totalVotes.Abstain = append(totalVotes.Abstain, a)
		totalVotes.Yes = append(totalVotes.Yes, y)
		totalVotes.No = append(totalVotes.No, n)
		totalVotes.Total = append(totalVotes.Total, t)
	}

	return totalVotes, nil
}

// --- transactions table ---

func InsertTx(db *sql.DB, dbTx *dbtypes.Tx, checked, updateExistingRecords bool) (uint64, error) {
	insertStatement := internal.MakeTxInsertStatement(checked, updateExistingRecords)
	var id uint64
	err := db.QueryRow(insertStatement,
		dbTx.BlockHash, dbTx.BlockHeight, dbTx.BlockTime.T, dbTx.Time.T,
		dbTx.TxType, dbTx.Version, dbTx.Tree, dbTx.TxID, dbTx.BlockIndex,
		dbTx.Locktime, dbTx.Expiry, dbTx.Size, dbTx.Spent, dbTx.Sent, dbTx.Fees,
		dbTx.NumVin, dbtypes.UInt64Array(dbTx.VinDbIds),
		dbTx.NumVout, dbtypes.UInt64Array(dbTx.VoutDbIds),
		dbTx.IsValidBlock, dbTx.IsMainchainBlock).Scan(&id)
	return id, err
}

func InsertTxns(db *sql.DB, dbTxns []*dbtypes.Tx, checked, updateExistingRecords bool) ([]uint64, error) {
	dbtx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("unable to begin database transaction: %v", err)
	}

	stmt, err := dbtx.Prepare(internal.MakeTxInsertStatement(checked, updateExistingRecords))
	if err != nil {
		log.Errorf("Transaction INSERT prepare: %v", err)
		_ = dbtx.Rollback() // try, but we want the Prepare error back
		return nil, err
	}

	ids := make([]uint64, 0, len(dbTxns))
	for _, tx := range dbTxns {
		var id uint64
		err := stmt.QueryRow(
			tx.BlockHash, tx.BlockHeight, tx.BlockTime.T, tx.Time.T,
			tx.TxType, tx.Version, tx.Tree, tx.TxID, tx.BlockIndex,
			tx.Locktime, tx.Expiry, tx.Size, tx.Spent, tx.Sent, tx.Fees,
			tx.NumVin, dbtypes.UInt64Array(tx.VinDbIds),
			tx.NumVout, dbtypes.UInt64Array(tx.VoutDbIds), tx.IsValidBlock,
			tx.IsMainchainBlock).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbtx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return nil, err
		}
		ids = append(ids, id)
	}

	// Close prepared statement. Ignore errors as we'll Commit regardless.
	_ = stmt.Close()

	return ids, dbtx.Commit()
}

// RetrieveDbTxByHash retrieves a row of the transactions table corresponding to
// the given transaction hash. Transactions in valid and mainchain blocks are
// chosen first. This function is used by FillAddressTransactions, an important
// component of the addresses page.
func RetrieveDbTxByHash(ctx context.Context, db *sql.DB, txHash string) (id uint64, dbTx *dbtypes.Tx, err error) {
	dbTx = new(dbtypes.Tx)
	vinDbIDs := dbtypes.UInt64Array(dbTx.VinDbIds)
	voutDbIDs := dbtypes.UInt64Array(dbTx.VoutDbIds)
	err = db.QueryRowContext(ctx, internal.SelectFullTxByHash, txHash).Scan(&id,
		&dbTx.BlockHash, &dbTx.BlockHeight, &dbTx.BlockTime.T, &dbTx.Time.T,
		&dbTx.TxType, &dbTx.Version, &dbTx.Tree, &dbTx.TxID, &dbTx.BlockIndex,
		&dbTx.Locktime, &dbTx.Expiry, &dbTx.Size, &dbTx.Spent, &dbTx.Sent,
		&dbTx.Fees, &dbTx.NumVin, &vinDbIDs, &dbTx.NumVout, &voutDbIDs,
		&dbTx.IsValidBlock, &dbTx.IsMainchainBlock)
	dbTx.VinDbIds = vinDbIDs
	dbTx.VoutDbIds = voutDbIDs
	return
}

// RetrieveFullTxByHash gets all data from the transactions table for the
// transaction specified by its hash. Transactions in valid and mainchain blocks
// are chosen first. See also RetrieveDbTxByHash.
func RetrieveFullTxByHash(ctx context.Context, db *sql.DB, txHash string) (id uint64,
	blockHash string, blockHeight int64, blockTime, timeVal dbtypes.TimeDef,
	txType int16, version int32, tree int8, blockInd uint32,
	lockTime, expiry int32, size uint32, spent, sent, fees int64,
	numVin int32, vinDbIDs []int64, numVout int32, voutDbIDs []int64,
	isValidBlock, isMainchainBlock bool, err error) {
	var hash string
	err = db.QueryRowContext(ctx, internal.SelectFullTxByHash, txHash).Scan(&id, &blockHash,
		&blockHeight, &blockTime.T, &timeVal.T, &txType, &version, &tree,
		&hash, &blockInd, &lockTime, &expiry, &size, &spent, &sent, &fees,
		&numVin, &vinDbIDs, &numVout, &voutDbIDs,
		&isValidBlock, &isMainchainBlock)
	return
}

// RetrieveDbTxsByHash retrieves all the rows of the transactions table,
// including the primary keys/ids, for the given transaction hash. This function
// is used by the transaction page via ChainDB.Transaction.
func RetrieveDbTxsByHash(ctx context.Context, db *sql.DB, txHash string) (ids []uint64, dbTxs []*dbtypes.Tx, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectFullTxsByHash, txHash)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var id uint64
		var dbTx dbtypes.Tx
		var vinids, voutids dbtypes.UInt64Array
		// vinDbIDs := dbtypes.UInt64Array(dbTx.VinDbIds)
		// voutDbIDs := dbtypes.UInt64Array(dbTx.VoutDbIds)

		err = rows.Scan(&id,
			&dbTx.BlockHash, &dbTx.BlockHeight, &dbTx.BlockTime.T, &dbTx.Time.T,
			&dbTx.TxType, &dbTx.Version, &dbTx.Tree, &dbTx.TxID, &dbTx.BlockIndex,
			&dbTx.Locktime, &dbTx.Expiry, &dbTx.Size, &dbTx.Spent, &dbTx.Sent,
			&dbTx.Fees, &dbTx.NumVin, &vinids, &dbTx.NumVout, &voutids,
			&dbTx.IsValidBlock, &dbTx.IsMainchainBlock)
		if err != nil {
			break
		}

		dbTx.VinDbIds = vinids
		dbTx.VoutDbIds = voutids

		ids = append(ids, id)
		dbTxs = append(dbTxs, &dbTx)
	}
	return
}

// RetrieveTxnsVinsByBlock retrieves for all the transactions in the specified
// block the vin_db_ids arrays, is_valid, and is_mainchain. This function is
// used by handleVinsTableMainchainupgrade, so it should not be subject to
// timeouts.
func RetrieveTxnsVinsByBlock(ctx context.Context, db *sql.DB, blockHash string) (vinDbIDs []dbtypes.UInt64Array,
	areValid []bool, areMainchain []bool, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectTxnsVinsByBlock, blockHash)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var ids dbtypes.UInt64Array
		var isValid, isMainchain bool
		err = rows.Scan(&ids, &isValid, &isMainchain)
		if err != nil {
			break
		}

		vinDbIDs = append(vinDbIDs, ids)
		areValid = append(areValid, isValid)
		areMainchain = append(areMainchain, isMainchain)
	}
	return
}

// RetrieveTxnsVinsVoutsByBlock retrieves for all the transactions in the
// specified block the vin_db_ids and vout_db_ids arrays. This function is used
// only by UpdateLastAddressesValid and other setting functions, where it should
// not be subject to a timeout.
func RetrieveTxnsVinsVoutsByBlock(ctx context.Context, db *sql.DB, blockHash string, onlyRegular bool) (vinDbIDs, voutDbIDs []dbtypes.UInt64Array,
	areMainchain []bool, err error) {
	stmt := internal.SelectTxnsVinsVoutsByBlock
	if onlyRegular {
		stmt = internal.SelectRegularTxnsVinsVoutsByBlock
	}

	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, stmt, blockHash)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var vinIDs, voutIDs dbtypes.UInt64Array
		var isMainchain bool
		err = rows.Scan(&vinIDs, &voutIDs, &isMainchain)
		if err != nil {
			break
		}

		vinDbIDs = append(vinDbIDs, vinIDs)
		voutDbIDs = append(voutDbIDs, voutIDs)
		areMainchain = append(areMainchain, isMainchain)
	}
	return
}

func RetrieveTxByHash(ctx context.Context, db *sql.DB, txHash string) (id uint64, blockHash string,
	blockInd uint32, tree int8, err error) {
	err = db.QueryRowContext(ctx, internal.SelectTxByHash, txHash).Scan(&id, &blockHash, &blockInd, &tree)
	return
}

func RetrieveTxBlockTimeByHash(ctx context.Context, db *sql.DB, txHash string) (blockTime dbtypes.TimeDef, err error) {
	err = db.QueryRowContext(ctx, internal.SelectTxBlockTimeByHash, txHash).Scan(&blockTime.T)
	return
}

// This is used by update functions, so care should be taken to not timeout in
// these cases.
func RetrieveTxsByBlockHash(ctx context.Context, db *sql.DB, blockHash string) (ids []uint64, txs []string,
	blockInds []uint32, trees []int8, blockTimes []dbtypes.TimeDef, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectTxsByBlockHash, blockHash)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var id uint64
		var blockTime dbtypes.TimeDef
		var tx string
		var bind uint32
		var tree int8
		err = rows.Scan(&id, &tx, &bind, &tree, &blockTime.T)
		if err != nil {
			break
		}

		ids = append(ids, id)
		txs = append(txs, tx)
		blockInds = append(blockInds, bind)
		trees = append(trees, tree)
		blockTimes = append(blockTimes, blockTime)
	}

	return
}

// RetrieveTxnsBlocks retrieves for the specified transaction hash the following
// data for each block containing the transactions: block_hash, block_index,
// is_valid, is_mainchain.
func RetrieveTxnsBlocks(ctx context.Context, db *sql.DB, txHash string) (blockHashes []string, blockHeights, blockIndexes []uint32, areValid, areMainchain []bool, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectTxsBlocks, txHash)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var hash string
		var height, idx uint32
		var isValid, isMainchain bool
		err = rows.Scan(&height, &hash, &idx, &isValid, &isMainchain)
		if err != nil {
			break
		}

		blockHeights = append(blockHeights, height)
		blockHashes = append(blockHashes, hash)
		blockIndexes = append(blockIndexes, idx)
		areValid = append(areValid, isValid)
		areMainchain = append(areMainchain, isMainchain)
	}
	return
}

func retrieveTxPerDay(ctx context.Context, db *sql.DB) (*dbtypes.ChartsData, error) {
	rows, err := db.QueryContext(ctx, internal.SelectTxsPerDay)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	items := new(dbtypes.ChartsData)
	for rows.Next() {
		var blockTime dbtypes.TimeDef
		var count uint64
		err = rows.Scan(&blockTime.T, &count)
		if err != nil {
			return nil, err
		}

		items.Time = append(items.Time, blockTime)
		items.Count = append(items.Count, count)
	}
	return items, nil
}

func retrieveTicketByOutputCount(ctx context.Context, db *sql.DB, dataType outputCountType) (*dbtypes.ChartsData, error) {
	var query string
	switch dataType {
	case outputCountByAllBlocks:
		query = internal.SelectTicketsOutputCountByAllBlocks
	case outputCountByTicketPoolWindow:
		query = internal.SelectTicketsOutputCountByTPWindow
	default:
		return nil, fmt.Errorf("unknown output count type '%v'", dataType)
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	items := new(dbtypes.ChartsData)
	for rows.Next() {
		var height, solo, pooled uint64
		err = rows.Scan(&height, &solo, &pooled)
		if err != nil {
			return nil, err
		}

		items.Height = append(items.Height, height)
		items.Solo = append(items.Solo, solo)
		items.Pooled = append(items.Pooled, pooled)
	}
	return items, nil
}

// retrieveChainWork assembles both block-by-block chainwork data
// and a rolling average for network hashrate data.
func retrieveChainWork(db *sql.DB) (*dbtypes.ChartsData, *dbtypes.ChartsData, error) {
	// Grab all chainwork points in rows of (time, chainwork).
	rows, err := db.Query(internal.SelectChainWork)
	if err != nil {
		return nil, nil, err
	}
	defer closeRows(rows)

	// Assemble chainwork and hashrate simultaneously.
	// Chainwork is stored as a 32-byte hex string, so in order to
	// do math, math/big types are used.
	workdata := new(dbtypes.ChartsData)
	hashrates := new(dbtypes.ChartsData)
	var blocktime dbtypes.TimeDef
	var workhex string

	// In order to store these large values as uint64, they are represented
	// as exahash (10^18) for work, and terahash/s (10^12) for hashrate.
	bigExa := big.NewInt(int64(1e18))
	bigTera := big.NewInt(int64(1e12))

	// chainWorkPt is stored for a rolling average.
	type chainWorkPt struct {
		work *big.Int
		time time.Time
	}
	// How many blocks to average across for hashrate.
	// 120 is the default returned by the RPC method `getnetworkhashps`.
	var averagingLength int = 120
	// points is used as circular storage.
	points := make([]chainWorkPt, averagingLength)
	var thisPt, lastPt chainWorkPt
	var idx, workingIdx, lastIdx int
	for rows.Next() {
		// Get the chainwork.
		err = rows.Scan(&blocktime.T, &workhex)
		if err != nil {
			return nil, nil, err
		}
		bigwork := new(big.Int)
		exawork := new(big.Int)
		bigwork, ok := bigwork.SetString(workhex, 16)
		if !ok {
			log.Errorf("Failed to make big.Int from chainwork %s", workhex)
			break
		}
		exawork.Set(bigwork)
		exawork.Div(bigwork, bigExa)
		if !exawork.IsUint64() {
			log.Errorf("Failed to make uint64 from chainwork %s", workhex)
			break
		}
		workdata.ChainWork = append(workdata.ChainWork, exawork.Uint64())
		workdata.Time = append(workdata.Time, blocktime)

		workingIdx = idx % averagingLength
		points[workingIdx] = chainWorkPt{bigwork, blocktime.T}
		if idx >= averagingLength {
			// lastIdx is actually the point averagingLength blocks ago.
			lastIdx = (workingIdx + 1) % averagingLength
			lastPt = points[lastIdx]
			thisPt = points[workingIdx]
			diff := new(big.Int)
			diff.Set(thisPt.work)
			diff.Sub(diff, lastPt.work)
			rate := diff.Div(diff, big.NewInt(int64(thisPt.time.Sub(lastPt.time).Seconds())))
			rate.Div(rate, bigTera)
			if !rate.IsUint64() {
				log.Errorf("Failed to make uint64 from rate")
				break
			}
			tDef := dbtypes.TimeDef{T: thisPt.time}
			hashrates.Time = append(hashrates.Time, tDef)
			hashrates.NetHash = append(hashrates.NetHash, rate.Uint64())
		}
		idx += 1
	}
	return workdata, hashrates, nil
}

// --- blocks and block_chain tables ---

func InsertBlock(db *sql.DB, dbBlock *dbtypes.Block, isValid, isMainchain, checked bool) (uint64, error) {
	insertStatement := internal.MakeBlockInsertStatement(dbBlock, checked)
	var id uint64
	err := db.QueryRow(insertStatement,
		dbBlock.Hash, dbBlock.Height, dbBlock.Size, isValid, isMainchain,
		dbBlock.Version, dbBlock.MerkleRoot, dbBlock.StakeRoot,
		dbBlock.NumTx, dbBlock.NumRegTx, dbBlock.NumStakeTx,
		dbBlock.Time.T, dbBlock.Nonce, dbBlock.VoteBits,
		dbBlock.FinalState, dbBlock.Voters, dbBlock.FreshStake,
		dbBlock.Revocations, dbBlock.PoolSize, dbBlock.Bits,
		dbBlock.SBits, dbBlock.Difficulty, dbBlock.ExtraData,
		dbBlock.StakeVersion, dbBlock.PreviousHash, dbBlock.ChainWork).Scan(&id)
	return id, err
}

// InsertBlockPrevNext inserts a new row of the block_chain table.
func InsertBlockPrevNext(db *sql.DB, blockDbID uint64,
	hash, prev, next string) error {
	rows, err := db.Query(internal.InsertBlockPrevNext, blockDbID, prev, hash, next)
	if err == nil {
		return rows.Close()
	}
	return err
}

// RetrieveBestBlockHeight gets the best block height (main chain only).
func RetrieveBestBlockHeight(ctx context.Context, db *sql.DB) (height uint64, hash string, id uint64, err error) {
	err = db.QueryRowContext(ctx, internal.RetrieveBestBlockHeight).Scan(&id, &hash, &height)
	return
}

// RetrieveBestBlockHeightAny gets the best block height, including side chains.
func RetrieveBestBlockHeightAny(ctx context.Context, db *sql.DB) (height uint64, hash string, id uint64, err error) {
	err = db.QueryRowContext(ctx, internal.RetrieveBestBlockHeightAny).Scan(&id, &hash, &height)
	return
}

// RetrieveBlockHash retrieves the hash of the block at the given height, if it
// exists (be sure to check error against sql.ErrNoRows!). WARNING: this returns
// the most recently added block at this height, but there may be others.
func RetrieveBlockHash(ctx context.Context, db *sql.DB, idx int64) (hash string, err error) {
	err = db.QueryRowContext(ctx, internal.SelectBlockHashByHeight, idx).Scan(&hash)
	return
}

// RetrieveBlockHeight retrieves the height of the block with the given hash, if
// it exists (be sure to check error against sql.ErrNoRows!).
func RetrieveBlockHeight(ctx context.Context, db *sql.DB, hash string) (height int64, err error) {
	err = db.QueryRowContext(ctx, internal.SelectBlockHeightByHash, hash).Scan(&height)
	return
}

// RetrieveBlockVoteCount gets the number of votes mined in a block.
func RetrieveBlockVoteCount(ctx context.Context, db *sql.DB, hash string) (numVotes int16, err error) {
	err = db.QueryRowContext(ctx, internal.SelectBlockVoteCount, hash).Scan(&numVotes)
	return
}

// RetrieveBlocksHashesAll retrieve the hash of every block in the blocks table,
// ordered by their row ID.
func RetrieveBlocksHashesAll(ctx context.Context, db *sql.DB) ([]string, error) {
	var hashes []string
	rows, err := db.QueryContext(ctx, internal.SelectBlocksHashes)
	if err != nil {
		return hashes, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var hash string
		err = rows.Scan(&hash)
		if err != nil {
			break
		}

		hashes = append(hashes, hash)
	}
	return hashes, err
}

// RetrieveBlockChainDbID retrieves the row id in the block_chain table of the
// block with the given hash, if it exists (be sure to check error against
// sql.ErrNoRows!).
func RetrieveBlockChainDbID(ctx context.Context, db *sql.DB, hash string) (dbID uint64, err error) {
	err = db.QueryRowContext(ctx, internal.SelectBlockChainRowIDByHash, hash).Scan(&dbID)
	return
}

// RetrieveSideChainBlocks retrieves the block chain status for all known side
// chain blocks.
func RetrieveSideChainBlocks(ctx context.Context, db *sql.DB) (blocks []*dbtypes.BlockStatus, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectSideChainBlocks)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var bs dbtypes.BlockStatus
		err = rows.Scan(&bs.IsValid, &bs.Height, &bs.PrevHash, &bs.Hash, &bs.NextHash)
		if err != nil {
			return
		}

		blocks = append(blocks, &bs)
	}
	return
}

// RetrieveSideChainTips retrieves the block chain status for all known side
// chain tip blocks.
func RetrieveSideChainTips(ctx context.Context, db *sql.DB) (blocks []*dbtypes.BlockStatus, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectSideChainTips)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		// NextHash is empty in all cases as these are chain tips.
		var bs dbtypes.BlockStatus
		err = rows.Scan(&bs.IsValid, &bs.Height, &bs.PrevHash, &bs.Hash)
		if err != nil {
			return
		}

		blocks = append(blocks, &bs)
	}
	return
}

// RetrieveDisapprovedBlocks retrieves the block chain status for all blocks
// that had their regular transactions invalidated by stakeholder disapproval.
func RetrieveDisapprovedBlocks(ctx context.Context, db *sql.DB) (blocks []*dbtypes.BlockStatus, err error) {
	var rows *sql.Rows
	rows, err = db.QueryContext(ctx, internal.SelectDisapprovedBlocks)
	if err != nil {
		return
	}
	defer closeRows(rows)

	for rows.Next() {
		var bs dbtypes.BlockStatus
		err = rows.Scan(&bs.IsMainchain, &bs.Height, &bs.PrevHash, &bs.Hash, &bs.NextHash)
		if err != nil {
			return
		}

		blocks = append(blocks, &bs)
	}
	return
}

// RetrieveBlockStatus retrieves the block chain status for the block with the
// specified hash.
func RetrieveBlockStatus(ctx context.Context, db *sql.DB, hash string) (bs dbtypes.BlockStatus, err error) {
	err = db.QueryRowContext(ctx, internal.SelectBlockStatus, hash).Scan(&bs.IsValid,
		&bs.IsMainchain, &bs.Height, &bs.PrevHash, &bs.Hash, &bs.NextHash)
	return
}

// RetrieveBlockFlags retrieves the block's is_valid and is_mainchain flags.
func RetrieveBlockFlags(ctx context.Context, db *sql.DB, hash string) (isValid bool, isMainchain bool, err error) {
	err = db.QueryRowContext(ctx, internal.SelectBlockFlags, hash).Scan(&isValid, &isMainchain)
	return
}

// RetrieveBlockSummaryByTimeRange retrieves the slice of block summaries for
// the given time range. The limit specifies the number of most recent block
// summaries to return. A limit of 0 indicates all blocks in the time range
// should be included.
func RetrieveBlockSummaryByTimeRange(ctx context.Context, db *sql.DB, minTime, maxTime int64, limit int) ([]dbtypes.BlockDataBasic, error) {
	var blocks []dbtypes.BlockDataBasic
	var stmt *sql.Stmt
	var rows *sql.Rows
	var err error

	if limit == 0 {
		stmt, err = db.Prepare(internal.SelectBlockByTimeRangeSQLNoLimit)
		if err != nil {
			return nil, err
		}
		rows, err = stmt.QueryContext(ctx, minTime, maxTime)
	} else {
		stmt, err = db.Prepare(internal.SelectBlockByTimeRangeSQL)
		if err != nil {
			return nil, err
		}
		rows, err = stmt.QueryContext(ctx, minTime, maxTime, limit)
	}

	if err != nil {
		log.Error(err)
		return nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var dbBlock dbtypes.BlockDataBasic
		var blockTime dbtypes.TimeDef
		if err = rows.Scan(&dbBlock.Hash, &dbBlock.Height, &dbBlock.Size, &blockTime.T, &dbBlock.NumTx); err != nil {
			log.Errorf("Unable to scan for block fields: %v", err)
		}
		dbBlock.Time = blockTime
		blocks = append(blocks, dbBlock)
	}
	if err = rows.Err(); err != nil {
		log.Error(err)
	}
	return blocks, nil
}

// RetrieveTicketsPriceByHeight fetches the ticket price and its timestamp that
// are used to display the ticket price variation on ticket price chart. These
// data are fetched at an interval of chaincfg.Params.StakeDiffWindowSize.
func RetrieveTicketsPriceByHeight(ctx context.Context, db *sql.DB, val int64) (*dbtypes.ChartsData, error) {
	rows, err := db.QueryContext(ctx, internal.SelectBlocksTicketsPrice, val)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	items := new(dbtypes.ChartsData)
	for rows.Next() {
		var timestamp dbtypes.TimeDef
		var price uint64
		var difficulty float64
		err = rows.Scan(&price, &timestamp.T, &difficulty)
		if err != nil {
			return nil, err
		}

		items.Time = append(items.Time, timestamp)
		priceCoin := dcrutil.Amount(price).ToCoin()
		items.ValueF = append(items.ValueF, priceCoin)
		items.Difficulty = append(items.Difficulty, difficulty)
	}

	return items, nil
}

// RetrievePreviousHashByBlockHash retrieves the previous block hash for the
// given block from the blocks table.
func RetrievePreviousHashByBlockHash(ctx context.Context, db *sql.DB, hash string) (previousHash string, err error) {
	err = db.QueryRowContext(ctx, internal.SelectBlocksPreviousHash, hash).Scan(&previousHash)
	return
}

// SetMainchainByBlockHash is used to set the is_mainchain flag for the given
// block. This is required to handle a reoganization.
func SetMainchainByBlockHash(db *sql.DB, hash string, isMainchain bool) (previousHash string, err error) {
	err = db.QueryRow(internal.UpdateBlockMainchain, hash, isMainchain).Scan(&previousHash)
	return
}

func retrieveBlockTicketsPoolValue(ctx context.Context, db *sql.DB) (*dbtypes.ChartsData, error) {
	rows, err := db.QueryContext(ctx, internal.SelectBlocksBlockSize)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	items := new(dbtypes.ChartsData)
	var oldTimestamp int64
	var chainsize uint64
	for rows.Next() {
		var timestamp dbtypes.TimeDef
		var blockSize, blocksCount, blockHeight uint64
		err = rows.Scan(&timestamp.T, &blockSize, &blocksCount, &blockHeight)
		if err != nil {
			return nil, err
		}

		val := oldTimestamp - timestamp.T.Unix()
		if val < 0 {
			val = val * -1
		}
		chainsize += blockSize
		oldTimestamp = timestamp.T.Unix()
		items.Time = append(items.Time, timestamp)
		items.Size = append(items.Size, blockSize)
		items.ChainSize = append(items.ChainSize, chainsize)
		items.Count = append(items.Count, blocksCount)
		items.ValueF = append(items.ValueF, float64(val))
		items.Value = append(items.Value, blockHeight)
	}

	return items, nil
}

// -- UPDATE functions for various tables ---

// UpdateTransactionsMainchain sets the is_mainchain column for the transactions
// in the specified block.
func UpdateTransactionsMainchain(db *sql.DB, blockHash string, isMainchain bool) (int64, []uint64, error) {
	rows, err := db.Query(internal.UpdateTxnsMainchainByBlock, isMainchain, blockHash)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to update transactions is_mainchain: %v", err)
	}
	defer closeRows(rows)

	var numRows int64
	var txRowIDs []uint64
	for rows.Next() {
		var id uint64
		err = rows.Scan(&id)
		if err != nil {
			break
		}

		txRowIDs = append(txRowIDs, id)
		numRows++
	}

	return numRows, txRowIDs, nil
}

// UpdateTransactionsValid sets the is_valid column of the transactions table
// for the regular (non-stake) transactions in the specified block.
func UpdateTransactionsValid(db *sql.DB, blockHash string, isValid bool) (int64, []uint64, error) {
	rows, err := db.Query(internal.UpdateRegularTxnsValidByBlock, isValid, blockHash)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to update regular transactions is_valid: %v", err)
	}
	defer closeRows(rows)

	var numRows int64
	var txRowIDs []uint64
	for rows.Next() {
		var id uint64
		err = rows.Scan(&id)
		if err != nil {
			break
		}

		txRowIDs = append(txRowIDs, id)
		numRows++
	}

	return numRows, txRowIDs, nil
}

// UpdateVotesMainchain sets the is_mainchain column for the votes in the
// specified block.
func UpdateVotesMainchain(db *sql.DB, blockHash string, isMainchain bool) (int64, error) {
	numRows, err := sqlExec(db, internal.UpdateVotesMainchainByBlock,
		"failed to update votes is_mainchain: ", isMainchain, blockHash)
	if err != nil {
		return 0, err
	}
	return numRows, nil
}

// UpdateTicketsMainchain sets the is_mainchain column for the tickets in the
// specified block.
func UpdateTicketsMainchain(db *sql.DB, blockHash string, isMainchain bool) (int64, error) {
	numRows, err := sqlExec(db, internal.UpdateTicketsMainchainByBlock,
		"failed to update tickets is_mainchain: ", isMainchain, blockHash)
	if err != nil {
		return 0, err
	}
	return numRows, nil
}

// UpdateAddressesMainchainByIDs sets the valid_mainchain column for the
// addresses specified by their vin (spending) or vout (funding) row IDs.
func UpdateAddressesMainchainByIDs(db *sql.DB, vinsBlk, voutsBlk []dbtypes.UInt64Array, isValidMainchain bool) (numSpendingRows, numFundingRows int64, err error) {
	// Spending/vins: Set valid_mainchain for the is_funding=false addresses
	// table rows using the vins row ids.
	var numUpdated int64
	for iTxn := range vinsBlk {
		for _, vin := range vinsBlk[iTxn] {
			numUpdated, err = sqlExec(db, internal.SetAddressMainchainForVinIDs,
				"failed to update spending addresses is_mainchain: ", isValidMainchain, vin)
			if err != nil {
				return
			}
			numSpendingRows += numUpdated
		}
	}

	// Funding/vouts: Set valid_mainchain for the is_funding=true addresses
	// table rows using the vouts row ids.
	for iTxn := range voutsBlk {
		for _, vout := range voutsBlk[iTxn] {
			numUpdated, err = sqlExec(db, internal.SetAddressMainchainForVoutIDs,
				"failed to update funding addresses is_mainchain: ", isValidMainchain, vout)
			if err != nil {
				return
			}
			numFundingRows += numUpdated
		}
	}
	return
}

// UpdateLastBlockValid updates the is_valid column of the block specified by
// the row id for the blocks table.
func UpdateLastBlockValid(db *sql.DB, blockDbID uint64, isValid bool) error {
	numRows, err := sqlExec(db, internal.UpdateLastBlockValid,
		"failed to update last block validity: ", blockDbID, isValid)
	if err != nil {
		return err
	}
	if numRows != 1 {
		return fmt.Errorf("UpdateLastBlockValid failed to update exactly 1 row"+
			"(%d)", numRows)
	}
	return nil
}

// UpdateLastVins updates the is_valid and is_mainchain columns in the vins
// table for all of the transactions in the block specified by the given block
// hash.
func UpdateLastVins(db *sql.DB, blockHash string, isValid, isMainchain bool) error {
	// Retrieve the hash for every transaction in this block. A context with no
	// deadline or cancellation function is used since this UpdateLastVins needs
	// to complete to ensure DB integrity.
	_, txs, _, trees, timestamps, err := RetrieveTxsByBlockHash(context.Background(), db, blockHash)
	if err != nil {
		return err
	}

	for i, txHash := range txs {
		n, err := sqlExec(db, internal.SetIsValidIsMainchainByTxHash,
			"failed to update last vins tx validity: ", isValid, isMainchain,
			txHash, timestamps[i].T, trees[i])
		if err != nil {
			return err
		}

		if n < 1 {
			return fmt.Errorf(" failed to update at least 1 row")
		}
	}

	return nil
}

// UpdateLastAddressesValid sets valid_mainchain as specified by isValid for
// addresses table rows pertaining to regular (non-stake) transactions found in
// the given block.
func UpdateLastAddressesValid(db *sql.DB, blockHash string, isValid bool) error {
	// The queries in this function should not timeout or (probably) canceled,
	// so use a background context.
	ctx := context.Background()

	// Get the row ids of all vins and vouts of regular txns in this block.
	onlyRegularTxns := true
	vinDbIDsBlk, voutDbIDsBlk, _, err := RetrieveTxnsVinsVoutsByBlock(ctx, db, blockHash, onlyRegularTxns)
	if err != nil {
		return fmt.Errorf("unable to retrieve vin data for block %s: %v", blockHash, err)
	}
	// Using vins and vouts row ids, update the valid_mainchain colume of the
	// rows of the address table referring to these vins and vouts.
	numAddrSpending, numAddrFunding, err := UpdateAddressesMainchainByIDs(db,
		vinDbIDsBlk, voutDbIDsBlk, isValid)
	if err != nil {
		log.Errorf("Failed to set addresses rows in block %s as sidechain: %v", blockHash, err)
	}
	addrsUpdated := numAddrSpending + numAddrFunding
	log.Debugf("Rows of addresses table updated: %d", addrsUpdated)
	return err
}

// UpdateBlockNext sets the next block's hash for the specified row of the
// block_chain table specified by DB row ID.
func UpdateBlockNext(db *sql.DB, blockDbID uint64, next string) error {
	res, err := db.Exec(internal.UpdateBlockNext, blockDbID, next)
	if err != nil {
		return err
	}
	numRows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if numRows != 1 {
		return fmt.Errorf("UpdateBlockNext failed to update exactly 1 row (%d)", numRows)
	}
	return nil
}

// UpdateBlockNextByHash sets the next block's hash for the block in the
// block_chain table specified by hash.
func UpdateBlockNextByHash(db *sql.DB, this, next string) error {
	res, err := db.Exec(internal.UpdateBlockNextByHash, this, next)
	if err != nil {
		return err
	}
	numRows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if numRows != 1 {
		return fmt.Errorf("UpdateBlockNextByHash failed to update exactly 1 row (%d)", numRows)
	}
	return nil
}
