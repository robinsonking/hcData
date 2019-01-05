// Copyright (c) 2018, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package insight

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/decred/dcrd/chaincfg"
	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/dcrjson"
	"github.com/decred/dcrd/dcrutil"
	"github.com/decred/dcrd/rpcclient"
	apitypes "github.com/decred/dcrdata/v4/api/types"
	"github.com/decred/dcrdata/v4/db/dbtypes"
	"github.com/decred/dcrdata/v4/db/dcrpg"
	m "github.com/decred/dcrdata/v4/middleware"
	"github.com/decred/dcrdata/v4/semver"
	"github.com/decred/dcrdata/v4/txhelpers"
)

// DataSourceLite specifies an interface for collecting data from the built-in
// databases (i.e. SQLite, storm, ffldb)
type DataSourceLite interface {
	UnconfirmedTxnsForAddress(address string) (*txhelpers.AddressOutpoints, int64, error)
}

type insightApiContext struct {
	nodeClient *rpcclient.Client
	BlockData  *dcrpg.ChainDBRPC
	params     *chaincfg.Params
	MemPool    DataSourceLite
	Status     apitypes.Status
	JSONIndent string
}

// NewInsightContext Constructor for insightApiContext
func NewInsightContext(client *rpcclient.Client, blockData *dcrpg.ChainDBRPC, params *chaincfg.Params, memPoolData DataSourceLite, JSONIndent string) *insightApiContext {
	conns, _ := client.GetConnectionCount()
	nodeHeight, _ := client.GetBlockCount()
	version := semver.NewSemver(1, 0, 0)

	newContext := insightApiContext{
		nodeClient: client,
		BlockData:  blockData,
		params:     params,
		MemPool:    memPoolData,
		Status: apitypes.Status{
			Height:          uint32(nodeHeight),
			NodeConnections: conns,
			APIVersion:      APIVersion,
			DcrdataVersion:  version.String(),
			NetworkName:     params.Name,
		},
	}
	return &newContext
}

func (c *insightApiContext) getIndentQuery(r *http.Request) (indent string) {
	useIndentation := r.URL.Query().Get("indent")
	if useIndentation == "1" || useIndentation == "true" {
		indent = c.JSONIndent
	}
	return
}

// Insight API successful response for JSON return items.
func writeJSON(w http.ResponseWriter, thing interface{}, indent string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", indent)
	if err := encoder.Encode(thing); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

// Insight API error response for a BAD REQUEST.  This means the request was
// malformed in some way or the request HASH, ADDRESS, BLOCK was not valid.
func writeInsightError(w http.ResponseWriter, str string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	io.WriteString(w, str)
}

// Insight API response for an item NOT FOUND.  This means the request was valid
// but no records were found for the item in question.  For some endpoints
// responding with an empty array [] is expected such as a transaction query for
// addresses with no transactions.
func writeInsightNotFound(w http.ResponseWriter, str string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, str)
}

func (c *insightApiContext) getTransaction(w http.ResponseWriter, r *http.Request) {
	txid := m.GetTxIDCtx(r)
	if txid == "" {
		apiLog.Errorf("Txid cannot be empty")
		writeInsightError(w, fmt.Sprintf("Txid cannot be empty"))
		return
	}

	// Return raw transaction
	txOld, err := c.BlockData.GetRawTransaction(txid)
	if err != nil {
		apiLog.Errorf("Unable to get transaction %s", txid)
		writeInsightNotFound(w, fmt.Sprintf("Unable to get transaction (%s)", txid))
		return
	}

	txsOld := []*dcrjson.TxRawResult{txOld}

	// convert to insight struct
	txsNew, err := c.TxConverter(txsOld)

	if err != nil {
		apiLog.Errorf("Error Processing Transactions")
		writeInsightError(w, fmt.Sprintf("Error Processing Transactions"))
		return
	}

	writeJSON(w, txsNew[0], c.getIndentQuery(r))
}

func (c *insightApiContext) getTransactionHex(w http.ResponseWriter, r *http.Request) {
	txid := m.GetTxIDCtx(r)
	if txid == "" {
		writeInsightError(w, "TxId must not be empty")
		return
	}

	txHex := c.BlockData.GetTransactionHex(txid)
	if txHex == "" {
		writeInsightNotFound(w, fmt.Sprintf("Unable to get transaction (%s)", txHex))
		return
	}

	hexOutput := &apitypes.InsightRawTx{
		Rawtx: txHex,
	}

	writeJSON(w, hexOutput, c.getIndentQuery(r))
}

func (c *insightApiContext) getBlockSummary(w http.ResponseWriter, r *http.Request) {
	// attempt to get hash of block set by hash or (fallback) height set on path
	hash, ok := c.GetInsightBlockHashCtx(r)
	if !ok {
		idx, ok := c.GetInsightBlockIndexCtx(r)
		if !ok {
			writeInsightError(w, "Must provide an index or block hash")
			return
		}
		var err error
		hash, err = c.BlockData.ChainDB.GetBlockHash(int64(idx))
		if dbtypes.IsTimeoutErr(err) {
			apiLog.Errorf("GetBlockHash: %v", err)
			http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			writeInsightError(w, "Unable to get block hash from index")
			return
		}
	}
	blockDcrd := c.BlockData.GetBlockVerboseByHash(hash, false)
	if blockDcrd == nil {
		writeInsightNotFound(w, "Unable to get block")
		return
	}

	blockSummary := []*dcrjson.GetBlockVerboseResult{blockDcrd}
	blockInsight, err := c.DcrToInsightBlock(blockSummary)
	if err != nil {
		apiLog.Errorf("Unable to process block (%s)", hash)
		writeInsightError(w, "Unable to Process Block")
		return
	}

	writeJSON(w, blockInsight, c.getIndentQuery(r))
}

func (c *insightApiContext) getBlockHash(w http.ResponseWriter, r *http.Request) {
	idx, ok := c.GetInsightBlockIndexCtx(r)
	if !ok {
		writeInsightError(w, "No index found in query")
		return
	}
	if idx < 0 || idx > c.BlockData.ChainDB.GetHeight() {
		writeInsightError(w, "Block height out of range")
		return
	}
	hash, err := c.BlockData.ChainDB.GetBlockHash(int64(idx))
	if dbtypes.IsTimeoutErr(err) {
		apiLog.Errorf("GetBlockHash: %v", err)
		http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
		return
	}
	if err != nil || hash == "" {
		writeInsightNotFound(w, "Not found")
		return
	}

	blockOutput := struct {
		BlockHash string `json:"blockHash"`
	}{
		hash,
	}
	writeJSON(w, blockOutput, c.getIndentQuery(r))
}

func (c *insightApiContext) getBlockChainHashCtx(r *http.Request) *chainhash.Hash {
	hash, err := chainhash.NewHashFromStr(c.getBlockHashCtx(r))
	if err != nil {
		apiLog.Errorf("Failed to parse block hash: %v", err)
		return nil
	}
	return hash
}

func (c *insightApiContext) getRawBlock(w http.ResponseWriter, r *http.Request) {

	hash, ok := c.GetInsightBlockHashCtx(r)
	if !ok {
		idx, ok := c.GetInsightBlockIndexCtx(r)
		if !ok {
			writeInsightError(w, "Must provide an index or block hash")
			return
		}
		var err error
		hash, err = c.BlockData.ChainDB.GetBlockHash(int64(idx))
		if dbtypes.IsTimeoutErr(err) {
			apiLog.Errorf("GetBlockHash: %v", err)
			http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			writeInsightError(w, "Unable to get block hash from index")
			return
		}
	}
	chainHash, err := chainhash.NewHashFromStr(hash)
	if err != nil {
		writeInsightError(w, fmt.Sprintf("Failed to parse block hash: %v", err))
		return
	}

	blockMsg, err := c.nodeClient.GetBlock(chainHash)
	if err != nil {
		writeInsightNotFound(w, fmt.Sprintf("Failed to retrieve block %s: %v", chainHash.String(), err))
		return
	}
	var blockHex bytes.Buffer
	if err = blockMsg.Serialize(&blockHex); err != nil {
		apiLog.Errorf("Failed to serialize block: %v", err)
		writeInsightError(w, fmt.Sprintf("Failed to serialize block"))
		return
	}

	blockJSON := struct {
		BlockHash string `json:"rawblock"`
	}{
		hex.EncodeToString(blockHex.Bytes()),
	}
	writeJSON(w, blockJSON, c.getIndentQuery(r))
}

func (c *insightApiContext) broadcastTransactionRaw(w http.ResponseWriter, r *http.Request) {
	// Check for rawtx
	rawHexTx, ok := c.GetRawHexTx(r)
	if !ok {
		// JSON extraction failed or rawtx blank.  Error message already returned.
		return
	}

	// Check maximum transaction size
	if len(rawHexTx)/2 > c.params.MaxTxSize {
		writeInsightError(w, fmt.Sprintf("Rawtx length exceeds maximum allowable characters (%d bytes received)", len(rawHexTx)/2))
		return
	}

	// Broadcast
	txid, err := c.BlockData.SendRawTransaction(rawHexTx)
	if err != nil {
		apiLog.Errorf("Unable to send transaction %s", rawHexTx)
		writeInsightError(w, fmt.Sprintf("SendRawTransaction failed: %v", err))
		return
	}

	// Respond with hash of broadcasted transaction
	txidJSON := struct {
		TxidHash string `json:"txid"`
	}{
		txid,
	}
	writeJSON(w, txidJSON, c.getIndentQuery(r))
}

func (c *insightApiContext) getAddressesTxnOutput(w http.ResponseWriter, r *http.Request) {
	address := m.GetAddressCtx(r) // Required
	if address == "" {
		writeInsightError(w, "Address cannot be empty")
		return
	}

	// Allow Addresses to be single or multiple separated by a comma.
	addresses := strings.Split(address, ",")

	// Initialize Output Structure
	txnOutputs := make([]apitypes.AddressTxnOutput, 0)

	for _, address := range addresses {
		confirmedTxnOutputs, err := c.BlockData.ChainDB.AddressUTXO(address)
		if dbtypes.IsTimeoutErr(err) {
			apiLog.Errorf("AddressUTXO: %v", err)
			http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			apiLog.Errorf("Error getting UTXOs: %v", err)
			continue
		}

		addressOuts, _, err := c.MemPool.UnconfirmedTxnsForAddress(address)
		if err != nil {
			apiLog.Errorf("Error getting unconfirmed transactions: %v", err)
			continue
		}

		if addressOuts != nil {
			// If there is any mempool add to the utxo set
		FUNDING_TX_DUPLICATE_CHECK:
			for _, f := range addressOuts.Outpoints {
				fundingTx, ok := addressOuts.TxnsStore[f.Hash]
				if !ok {
					apiLog.Errorf("An outpoint's transaction is not available in TxnStore.")
					continue
				}
				if fundingTx.Confirmed() {
					apiLog.Errorf("An outpoint's transaction is unexpectedly confirmed.")
					continue
				}
				// TODO: Confirmed() not always return true for txs that have
				// already been confirmed in a block.  The mempool cache update
				// process should correctly update these.  Until we sort out why we
				// need to do one more search on utxo and do not add if this is
				// already in the list as a confirmed tx.
				for _, utxo := range confirmedTxnOutputs {
					if utxo.Vout == f.Index && utxo.TxnID == f.Hash.String() {
						continue FUNDING_TX_DUPLICATE_CHECK
					}
				}

				txnOutput := apitypes.AddressTxnOutput{
					Address:       address,
					TxnID:         fundingTx.Hash().String(),
					Vout:          f.Index,
					ScriptPubKey:  hex.EncodeToString(fundingTx.Tx.TxOut[f.Index].PkScript),
					Amount:        dcrutil.Amount(fundingTx.Tx.TxOut[f.Index].Value).ToCoin(),
					Satoshis:      fundingTx.Tx.TxOut[f.Index].Value,
					Confirmations: 0,
					BlockTime:     fundingTx.MemPoolTime,
				}
				txnOutputs = append(txnOutputs, txnOutput)
			}
		}
		txnOutputs = append(txnOutputs, confirmedTxnOutputs...)

		// Search for items in mempool that spend utxo (matching hash and index)
		// and remove those from the set
		for _, f := range addressOuts.PrevOuts {
			spendingTx, ok := addressOuts.TxnsStore[f.TxSpending]
			if !ok {
				apiLog.Errorf("An outpoint's transaction is not available in TxnStore.")
				continue
			}
			if spendingTx.Confirmed() {
				apiLog.Errorf("A transaction spending the outpoint of an unconfirmed transaction is unexpectedly confirmed.")
				continue
			}
			for g, utxo := range txnOutputs {
				if utxo.Vout == f.PreviousOutpoint.Index && utxo.TxnID == f.PreviousOutpoint.Hash.String() {
					// Found a utxo that is unconfirmed spent.  Remove from slice
					txnOutputs = append(txnOutputs[:g], txnOutputs[g+1:]...)
				}
			}
		}
	}
	// Final sort by timestamp desc if unconfirmed and by confirmations
	// ascending if confirmed
	sort.Slice(txnOutputs, func(i, j int) bool {
		if txnOutputs[i].Confirmations == 0 && txnOutputs[j].Confirmations == 0 {
			return txnOutputs[i].BlockTime > txnOutputs[j].BlockTime
		}
		return txnOutputs[i].Confirmations < txnOutputs[j].Confirmations
	})

	writeJSON(w, txnOutputs, c.getIndentQuery(r))
}

func (c *insightApiContext) getTransactions(w http.ResponseWriter, r *http.Request) {
	hash := m.GetBlockHashCtx(r)
	address := m.GetAddressCtx(r)
	if hash == "" && address == "" {
		writeInsightError(w, "Required query parameters (address or block) not present.")
		return
	}

	if hash != "" {
		blkTrans := c.BlockData.GetBlockVerboseByHash(hash, true)
		if blkTrans == nil {
			apiLog.Errorf("Unable to get block %s transactions", hash)
			writeInsightError(w, fmt.Sprintf("Unable to get block %s transactions", hash))
			return
		}

		txsOld := []*dcrjson.TxRawResult{}
		txcount := len(blkTrans.RawTx) + len(blkTrans.RawSTx)
		// Merge tx and stx together and limit result to 10 max
		count := 0
		for i := range blkTrans.RawTx {
			txsOld = append(txsOld, &blkTrans.RawTx[i])
			count++
			if count > 10 {
				break
			}
		}
		if count < 10 {
			for i := range blkTrans.RawSTx {
				txsOld = append(txsOld, &blkTrans.RawSTx[i])
				count++
				if count > 10 {
					break
				}
			}
		}

		// Convert to Insight struct
		txsNew, err := c.TxConverter(txsOld)
		if err != nil {
			apiLog.Error("Error Processing Transactions")
			writeInsightError(w, "Error Processing Transactions")
			return
		}

		blockTransactions := apitypes.InsightBlockAddrTxSummary{
			PagesTotal: int64(txcount),
			Txs:        txsNew,
		}
		writeJSON(w, blockTransactions, c.getIndentQuery(r))
		return
	}

	if address != "" {
		// Validate Address
		_, err := dcrutil.DecodeAddress(address)
		if err != nil {
			writeInsightError(w, fmt.Sprintf("Address is invalid (%s)", address))
			return
		}
		addresses := []string{address}
		rawTxs, recentTxs, err :=
			c.BlockData.ChainDB.InsightAddressTransactions(addresses, int64(c.Status.Height-2))
		if dbtypes.IsTimeoutErr(err) {
			apiLog.Errorf("InsightAddressTransactions: %v", err)
			http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			writeInsightError(w,
				fmt.Sprintf("Error retrieving transactions for addresss %s (%v)",
					addresses, err))
			return
		}

		addressOuts, _, err := c.MemPool.UnconfirmedTxnsForAddress(address)
		UnconfirmedTxs := []string{}

		if err != nil {
			writeInsightError(w, fmt.Sprintf("Error gathering mempool transactions (%v)", err))
			return
		}

	FUNDING_TX_DUPLICATE_CHECK:
		for _, f := range addressOuts.Outpoints {
			// Confirm its not already in our recent transactions
			for _, v := range recentTxs {
				if v == f.Hash.String() {
					continue FUNDING_TX_DUPLICATE_CHECK
				}
			}
			UnconfirmedTxs = append(UnconfirmedTxs, f.Hash.String()) // Funding tx
			recentTxs = append(recentTxs, f.Hash.String())
		}
	SPENDING_TX_DUPLICATE_CHECK:
		for _, f := range addressOuts.PrevOuts {
			for _, v := range recentTxs {
				if v == f.TxSpending.String() {
					continue SPENDING_TX_DUPLICATE_CHECK
				}
			}
			UnconfirmedTxs = append(UnconfirmedTxs, f.TxSpending.String()) // Spending tx
			recentTxs = append(recentTxs, f.TxSpending.String())
		}

		// Merge unconfirmed with confirmed transactions
		rawTxs = append(UnconfirmedTxs, rawTxs...)

		txcount := len(rawTxs)

		if txcount > 10 {
			rawTxs = rawTxs[0:10]
		}

		txsOld := []*dcrjson.TxRawResult{}
		for _, rawTx := range rawTxs {
			txOld, err1 := c.BlockData.GetRawTransaction(rawTx)
			if err1 != nil {
				apiLog.Errorf("Unable to get transaction %s", rawTx)
				writeInsightError(w, fmt.Sprintf("Error gathering transaction details (%s)", err1))
				return
			}
			txsOld = append(txsOld, txOld)
		}

		// Convert to Insight struct
		txsNew, err := c.TxConverter(txsOld)
		if err != nil {
			apiLog.Error("Error Processing Transactions")
			writeInsightError(w, "Error Processing Transactions")
			return
		}

		addrTransactions := apitypes.InsightBlockAddrTxSummary{
			PagesTotal: int64(txcount),
			Txs:        txsNew,
		}
		writeJSON(w, addrTransactions, c.getIndentQuery(r))
	}
}

func (c *insightApiContext) getAddressesTxn(w http.ResponseWriter, r *http.Request) {
	address := m.GetAddressCtx(r) // Required
	if address == "" {
		writeInsightError(w, "Address cannot be empty")
		return
	}

	noAsm := c.GetNoAsmCtx(r)             // Optional
	noScriptSig := c.GetNoScriptSigCtx(r) // Optional
	noSpent := c.GetNoSpentCtx(r)         // Optional
	from := c.GetFromCtx(r)               // Optional
	to, ok := c.GetToCtx(r)               // Optional
	if !ok {
		to = from + 10
	}

	// Allow Addresses to be single or multiple separated by a comma.
	addresses := strings.Split(address, ",")

	// Initialize Output Structure
	addressOutput := new(apitypes.InsightMultiAddrsTxOutput)
	UnconfirmedTxs := []string{}

	rawTxs, recentTxs, err :=
		c.BlockData.ChainDB.InsightAddressTransactions(addresses, int64(c.Status.Height-2))
	if dbtypes.IsTimeoutErr(err) {
		apiLog.Errorf("InsightAddressTransactions: %v", err)
		http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		writeInsightError(w,
			fmt.Sprintf("Error retrieving transactions for addresss %s (%s)",
				addresses, err))
		return
	}

	// Confirm all addresses are valid and pull unconfirmed transactions for all addresses
	for _, addr := range addresses {
		address, err := dcrutil.DecodeAddress(addr)
		if err != nil {
			writeInsightError(w, fmt.Sprintf("Address is invalid (%s)", addr))
			return
		}
		addressOuts, _, err := c.MemPool.UnconfirmedTxnsForAddress(address.String())
		if err != nil {
			writeInsightError(w, fmt.Sprintf("Error gathering mempool transactions (%s)", err))
			return
		}

	FUNDING_TX_DUPLICATE_CHECK:
		for _, f := range addressOuts.Outpoints {
			// Confirm its not already in our recent transactions
			for _, v := range recentTxs {
				if v == f.Hash.String() {
					continue FUNDING_TX_DUPLICATE_CHECK
				}
			}
			UnconfirmedTxs = append(UnconfirmedTxs, f.Hash.String()) // Funding tx
			recentTxs = append(recentTxs, f.Hash.String())
		}
	SPENDING_TX_DUPLICATE_CHECK:
		for _, f := range addressOuts.PrevOuts {
			for _, v := range recentTxs {
				if v == f.TxSpending.String() {
					continue SPENDING_TX_DUPLICATE_CHECK
				}
			}
			UnconfirmedTxs = append(UnconfirmedTxs, f.TxSpending.String()) // Spending tx
			recentTxs = append(recentTxs, f.TxSpending.String())
		}
	}

	// Merge unconfirmed with confirmed transactions
	rawTxs = append(UnconfirmedTxs, rawTxs...)

	txcount := len(rawTxs)
	addressOutput.TotalItems = int64(txcount)

	if txcount > 0 {
		if int(from) > txcount {
			from = int64(txcount)
		}
		if int(from) < 0 {
			from = 0
		}
		if int(to) > txcount {
			to = int64(txcount)
		}
		if int(to) < 0 {
			to = 0
		}
		if from > to {
			to = from
		}
		if (to - from) > 50 {
			writeInsightError(w, fmt.Sprintf("\"from\" (%d) and \"to\" (%d) range should be less than or equal to 50", from, to))
			return
		}
		// Final Slice Extraction
		rawTxs = rawTxs[from:to]
	}
	addressOutput.From = int(from)
	addressOutput.To = int(to)

	txsOld := []*dcrjson.TxRawResult{}
	for _, rawTx := range rawTxs {
		txOld, err := c.BlockData.GetRawTransaction(rawTx)
		if err != nil {
			apiLog.Errorf("Unable to get transaction %s", rawTx)
			writeInsightError(w, fmt.Sprintf("Error gathering transaction details (%s)", err))
			return
		}
		txsOld = append(txsOld, txOld)
	}

	// Convert to Insight API struct
	txsNew, err := c.DcrToInsightTxns(txsOld, noAsm, noScriptSig, noSpent)
	if err != nil {
		apiLog.Error("Unable to process transactions")
		writeInsightError(w, fmt.Sprintf("Unable to convert transactions (%s)", err))
		return
	}
	addressOutput.Items = append(addressOutput.Items, txsNew...)
	if addressOutput.Items == nil {
		// Make sure we pass an empty array not null to json response if no Tx
		addressOutput.Items = make([]apitypes.InsightTx, 0)
	}
	writeJSON(w, addressOutput, c.getIndentQuery(r))
}

func (c *insightApiContext) getAddressBalance(w http.ResponseWriter, r *http.Request) {
	address := m.GetAddressCtx(r)
	if address == "" {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	addressInfo, err := c.BlockData.ChainDB.AddressBalance(address, 20, 0)
	if dbtypes.IsTimeoutErr(err) {
		apiLog.Errorf("AddressBalance: %v", err)
		http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
		return
	}
	if err != nil || addressInfo == nil {
		apiLog.Warnf("AddressBalance: %v", err)
		http.Error(w, http.StatusText(422), 422)
		return
	}
	writeJSON(w, addressInfo.TotalUnspent, c.getIndentQuery(r))
}

func (c *insightApiContext) getSyncInfo(w http.ResponseWriter, r *http.Request) {

	blockChainHeight, err := c.nodeClient.GetBlockCount()

	// To insure JSON encodes an error properly as a string or no error as null
	// its easiest to use a pointer to a string.
	var errorString *string
	if err != nil {
		s := err.Error()
		errorString = &s
	} else {
		errorString = nil
	}

	height := c.BlockData.GetHeight()

	syncPercentage := int((float64(height) / float64(blockChainHeight)) * 100)

	st := "syncing"
	if syncPercentage == 100 {
		st = "finished"
	}

	syncInfo := struct {
		Status           string  `json:"status"`
		BlockChainHeight int64   `json:"blockChainHeight"`
		SyncPercentage   int     `json:"syncPercentage"`
		Height           int     `json:"height"`
		Error            *string `json:"error"`
		Type             string  `json:"type"`
	}{
		st,
		blockChainHeight,
		syncPercentage,
		height,
		errorString,
		"from RPC calls",
	}
	writeJSON(w, syncInfo, c.getIndentQuery(r))
}

func (c *insightApiContext) getStatusInfo(w http.ResponseWriter, r *http.Request) {
	statusInfo := m.GetStatusInfoCtx(r)

	// best block idx is also embedded through the middleware.  We could use
	// this value or the other best blocks as done below.  Which one is best?
	// idx := m.GetBlockIndexCtx(r)

	infoResult, err := c.nodeClient.GetInfo()
	if err != nil {
		apiLog.Error("Error getting status")
		writeInsightError(w, fmt.Sprintf("Error getting status (%s)", err))
		return
	}

	switch statusInfo {
	case "getDifficulty":
		info := struct {
			Difficulty float64 `json:"difficulty"`
		}{
			infoResult.Difficulty,
		}
		writeJSON(w, info, c.getIndentQuery(r))
	case "getBestBlockHash":
		blockhash, err := c.nodeClient.GetBlockHash(int64(infoResult.Blocks))
		if err != nil {
			apiLog.Errorf("Error getting block hash %d (%s)", infoResult.Blocks, err)
			writeInsightError(w, fmt.Sprintf("Error getting block hash %d (%s)", infoResult.Blocks, err))
			return
		}

		info := struct {
			BestBlockHash string `json:"bestblockhash"`
		}{
			blockhash.String(),
		}
		writeJSON(w, info, c.getIndentQuery(r))
	case "getLastBlockHash":
		blockhashtip, err := c.nodeClient.GetBlockHash(int64(infoResult.Blocks))
		if err != nil {
			apiLog.Errorf("Error getting block hash %d (%s)", infoResult.Blocks, err)
			writeInsightError(w, fmt.Sprintf("Error getting block hash %d (%s)", infoResult.Blocks, err))
			return
		}
		lastblockhash, err := c.nodeClient.GetBlockHash(int64(c.Status.Height))
		if err != nil {
			apiLog.Errorf("Error getting block hash %d (%s)", c.Status.Height, err)
			writeInsightError(w, fmt.Sprintf("Error getting block hash %d (%s)", c.Status.Height, err))
			return
		}

		info := struct {
			SyncTipHash   string `json:"syncTipHash"`
			LastBlockHash string `json:"lastblockhash"`
		}{
			blockhashtip.String(),
			lastblockhash.String(),
		}
		writeJSON(w, info, c.getIndentQuery(r))
	default:
		info := struct {
			Version         int32   `json:"version"`
			Protocolversion int32   `json:"protocolversion"`
			Blocks          int32   `json:"blocks"`
			NodeTimeoffset  int64   `json:"timeoffset"`
			NodeConnections int32   `json:"connections"`
			Proxy           string  `json:"proxy"`
			Difficulty      float64 `json:"difficulty"`
			Testnet         bool    `json:"testnet"`
			Relayfee        float64 `json:"relayfee"`
			Errors          string  `json:"errors"`
		}{
			infoResult.Version,
			infoResult.ProtocolVersion,
			infoResult.Blocks,
			infoResult.TimeOffset,
			infoResult.Connections,
			infoResult.Proxy,
			infoResult.Difficulty,
			infoResult.TestNet,
			infoResult.RelayFee,
			infoResult.Errors,
		}

		writeJSON(w, info, c.getIndentQuery(r))
	}

}

func (c *insightApiContext) getBlockSummaryByTime(w http.ResponseWriter, r *http.Request) {
	blockDate := m.GetBlockDateCtx(r)
	limit := c.GetLimitCtx(r)

	summaryOutput := apitypes.InsightBlocksSummaryResult{}
	layout := "2006-01-02 15:04:05"
	blockDateToday := time.Now().UTC().Format("2006-01-02")

	if blockDate == "" {
		blockDate = blockDateToday
	}

	if blockDateToday == blockDate {
		summaryOutput.Pagination.IsToday = true
	}
	minDate, err := time.Parse(layout, blockDate+" 00:00:00")
	if err != nil {
		writeInsightError(w, fmt.Sprintf("Unable to retrieve block summary using time %s: %v", blockDate, err))
		return
	}

	maxDate, err := time.Parse(layout, blockDate+" 23:59:59")
	if err != nil {
		writeInsightError(w, fmt.Sprintf("Unable to retrieve block summary using time %s: %v", blockDate, err))
		return
	}
	summaryOutput.Pagination.Next = minDate.AddDate(0, 0, 1).Format("2006-01-02")
	summaryOutput.Pagination.Prev = minDate.AddDate(0, 0, -1).Format("2006-01-02")

	summaryOutput.Pagination.Current = blockDate

	minTime, maxTime := minDate.Unix(), maxDate.Unix()
	summaryOutput.Pagination.CurrentTs = maxTime
	summaryOutput.Pagination.MoreTs = maxTime

	blockSummary, err := c.BlockData.ChainDB.BlockSummaryTimeRange(minTime, maxTime, 0)
	if dbtypes.IsTimeoutErr(err) {
		apiLog.Errorf("BlockSummaryTimeRange: %v", err)
		http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		writeInsightError(w, fmt.Sprintf("Unable to retrieve block summaries: %v", err))
		return
	}

	outputBlockSummary := []dbtypes.BlockDataBasic{}

	// Generate the pagination parameters More and MoreTs and limit the result.
	if limit > 0 {
		for i, block := range blockSummary {
			if i >= limit {
				summaryOutput.Pagination.More = true
				break
			}
			outputBlockSummary = append(outputBlockSummary, block)
			blockTime := block.Time.T.Unix()
			if blockTime < summaryOutput.Pagination.MoreTs {
				summaryOutput.Pagination.MoreTs = blockTime
			}
		}
		summaryOutput.Blocks = outputBlockSummary
	} else {
		summaryOutput.Blocks = blockSummary
		summaryOutput.Pagination.More = false
		summaryOutput.Pagination.MoreTs = minTime
	}

	summaryOutput.Length = len(summaryOutput.Blocks)

	writeJSON(w, summaryOutput, c.getIndentQuery(r))

}

func (c *insightApiContext) getAddressInfo(w http.ResponseWriter, r *http.Request) {
	address := m.GetAddressCtx(r)
	command, isCmd := c.GetAddressCommandCtx(r)

	_, err := dcrutil.DecodeAddress(address)
	if err != nil {
		writeInsightError(w, "Invalid Address")
		return
	}

	noTxList := c.GetNoTxListCtx(r)

	from := c.GetFromCtx(r)
	to, ok := c.GetToCtx(r)
	if !ok || to <= from {
		to = from + 1000
	}

	// Get Confirmed Balances
	var unconfirmedBalanceSat int64
	_, _, totalSpent, totalUnspent, _, err := c.BlockData.ChainDB.AddressSpentUnspent(address)
	if dbtypes.IsTimeoutErr(err) {
		apiLog.Errorf("AddressSpentUnspent: %v", err)
		http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		apiLog.Errorf("AddressSpentUnspent: %v", err)
		http.Error(w, "Unexpected error retrieving address info.", http.StatusInternalServerError)
		return
	}

	if isCmd {
		switch command {
		case "balance":
			writeJSON(w, totalUnspent, c.getIndentQuery(r))
			return
		case "totalReceived":
			writeJSON(w, totalSpent+totalUnspent, c.getIndentQuery(r))
			return
		case "totalSent":
			writeJSON(w, totalSpent, c.getIndentQuery(r))
			return
		}
	}

	addresses := []string{address}

	// Get confirmed transactions.
	rawTxs, recentTxs, err :=
		c.BlockData.ChainDB.InsightAddressTransactions(addresses, int64(c.Status.Height-2))
	if dbtypes.IsTimeoutErr(err) {
		apiLog.Errorf("InsightAddressTransactions: %v", err)
		http.Error(w, "Database timeout.", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		apiLog.Errorf("Error retrieving transactions for addresss %s: %v",
			addresses, err)
		http.Error(w, "Error retrieving transactions for that addresss.",
			http.StatusInternalServerError)
		return
	}
	confirmedTxCount := len(rawTxs)

	// Get unconfirmed transactions.
	unconfirmedTxs := []string{}
	addressOuts, _, err := c.MemPool.UnconfirmedTxnsForAddress(address)
	if err != nil {
		apiLog.Errorf("Error in getting unconfirmed transactions")
	}
	if addressOuts != nil {
	FUNDING_TX_DUPLICATE_CHECK:
		for _, f := range addressOuts.Outpoints {
			// Confirm it's not already in our recent transactions.
			for _, v := range recentTxs {
				if v == f.Hash.String() {
					continue FUNDING_TX_DUPLICATE_CHECK
				}
			}
			fundingTx, ok := addressOuts.TxnsStore[f.Hash]
			if !ok {
				apiLog.Errorf("An outpoint's transaction is not available in TxnStore.")
				continue
			}
			if fundingTx.Confirmed() {
				apiLog.Errorf("An outpoint's transaction is unexpectedly confirmed.")
				continue
			}
			unconfirmedBalanceSat += fundingTx.Tx.TxOut[f.Index].Value
			unconfirmedTxs = append(unconfirmedTxs, f.Hash.String()) // Funding tx
			recentTxs = append(recentTxs, f.Hash.String())
		}
	SPENDING_TX_DUPLICATE_CHECK:
		for _, f := range addressOuts.PrevOuts {
			for _, v := range recentTxs {
				if v == f.TxSpending.String() {
					continue SPENDING_TX_DUPLICATE_CHECK
				}
			}
			spendingTx, ok := addressOuts.TxnsStore[f.TxSpending]
			if !ok {
				apiLog.Errorf("An outpoint's transaction is not available in TxnStore.")
				continue
			}
			if spendingTx.Confirmed() {
				apiLog.Errorf("A transaction spending the outpoint of an unconfirmed transaction is unexpectedly confirmed.")
				continue
			}

			// Sent total sats has to be a lookup of the vout:i prevout value
			// because vin:i valuein is not reliable from dcrd at present
			prevhash := spendingTx.Tx.TxIn[f.InputIndex].PreviousOutPoint.Hash
			previndex := spendingTx.Tx.TxIn[f.InputIndex].PreviousOutPoint.Index
			valuein := addressOuts.TxnsStore[prevhash].Tx.TxOut[previndex].Value
			unconfirmedBalanceSat -= valuein
			unconfirmedTxs = append(unconfirmedTxs, f.TxSpending.String()) // Spending tx
			recentTxs = append(recentTxs, f.TxSpending.String())
		}
	}

	if isCmd {
		switch command {
		case "unconfirmedBalance":
			writeJSON(w, unconfirmedBalanceSat, c.getIndentQuery(r))
			return
		}
	}

	// Merge Unconfirmed with Confirmed transactions
	rawTxs = append(unconfirmedTxs, rawTxs...)

	// Final Slice Extraction
	txcount := len(rawTxs)
	if txcount > 0 {
		if int(from) > txcount {
			from = int64(txcount)
		}
		if int(from) < 0 {
			from = 0
		}
		if int(to) > txcount {
			to = int64(txcount)
		}
		if int(to) < 0 {
			to = 0
		}
		if from > to {
			to = from
		}
		if (to - from) > 1000 {
			writeInsightError(w, fmt.Sprintf("\"from\" (%d) and \"to\" (%d) range should be less than or equal to 1000", from, to))
			return
		}

		rawTxs = rawTxs[from:to]
	}

	addressInfo := apitypes.InsightAddressInfo{
		Address:                  address,
		TotalReceivedSat:         (totalSpent + totalUnspent),
		TotalSentSat:             totalSpent,
		BalanceSat:               totalUnspent,
		TotalReceived:            dcrutil.Amount(totalSpent + totalUnspent).ToCoin(),
		TotalSent:                dcrutil.Amount(totalSpent).ToCoin(),
		Balance:                  dcrutil.Amount(totalUnspent).ToCoin(),
		TxAppearances:            int64(confirmedTxCount),
		UnconfirmedBalance:       dcrutil.Amount(unconfirmedBalanceSat).ToCoin(),
		UnconfirmedBalanceSat:    unconfirmedBalanceSat,
		UnconfirmedTxAppearances: int64(len(unconfirmedTxs)),
	}

	if noTxList == 0 {
		addressInfo.TransactionsID = rawTxs
	}

	writeJSON(w, addressInfo, c.getIndentQuery(r))
}

func (c *insightApiContext) getEstimateFee(w http.ResponseWriter, r *http.Request) {
	nbBlocks := c.GetNbBlocksCtx(r)
	if nbBlocks == 0 {
		nbBlocks = 2
	}
	estimateFee := make(map[string]float64)

	// A better solution would be a call to the DCRD RPC "estimatefee" endpoint
	// but that does not appear to be exposed currently.
	infoResult, err := c.nodeClient.GetInfo()
	if err != nil {
		apiLog.Error("Error getting status")
		writeInsightError(w, fmt.Sprintf("Error getting status (%s)", err))
		return
	}
	estimateFee[strconv.Itoa(nbBlocks)] = infoResult.RelayFee

	writeJSON(w, estimateFee, c.getIndentQuery(r))
}

// GetPeerStatus handles requests for node peer info (i.e. getpeerinfo RPC).
func (c *insightApiContext) GetPeerStatus(w http.ResponseWriter, r *http.Request) {
	// Use a RPC call to tell if we are connected or not
	_, err := c.nodeClient.GetPeerInfo()
	var connected bool
	if err == nil {
		connected = true
	} else {
		connected = false
	}
	var port *string
	peerInfo := struct {
		Connected bool    `json:"connected"`
		Host      string  `json:"host"`
		Port      *string `json:"port"`
	}{
		connected, "127.0.0.1", port,
	}

	writeJSON(w, peerInfo, c.getIndentQuery(r))
}
