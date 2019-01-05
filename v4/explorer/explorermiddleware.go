// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package explorer

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi"
)

type contextKey int

const (
	ctxSearch contextKey = iota
	ctxBlockIndex
	ctxBlockHash
	ctxTxHash
	ctxTxInOut
	ctxTxInOutId
	ctxAddress
	ctxAgendaId
)

func (exp *explorerUI) BlockHashPathOrIndexCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		height, err := strconv.ParseInt(chi.URLParam(r, "blockhash"), 10, 0)
		var hash string
		if err != nil {
			// Not a height, try it as a hash.
			hash = chi.URLParam(r, "blockhash")
			if exp.liteMode {
				height, err = exp.blockData.GetBlockHeight(hash)
			} else {
				height, err = exp.explorerSource.BlockHeight(hash)
			}
			if exp.timeoutErrorPage(w, err, "BlockHashPathOrIndexCtx>BlockHeight") {
				return
			}
			if err != nil {
				if err != sql.ErrNoRows {
					log.Warnf("BlockHeight(%s) failed: %v", hash, err)
				}
				exp.StatusPage(w, defaultErrorCode, "could not find that block", hash, ExpStatusNotFound)
				return
			}
		} else {
			// Check best DB block to recognize future blocks.
			var maxHeight int64
			if exp.liteMode {
				maxHeight = int64(exp.blockData.GetHeight())
			} else {
				bestBlockHeight, err := exp.explorerSource.HeightDB()
				if err != nil {
					log.Errorf("HeightDB() failed: %v", err)
					exp.StatusPage(w, defaultErrorCode,
						"an unexpected error had occured while retrieving the best block",
						"", ExpStatusError)
					return
				}
				maxHeight = int64(bestBlockHeight)
			}

			if height > maxHeight {
				expectedTime := time.Duration(height-maxHeight) * exp.ChainParams.TargetTimePerBlock
				message := fmt.Sprintf("This block is expected to arrive in approximately in %v. ", expectedTime)
				exp.StatusPage(w, defaultErrorCode, message,
					string(expectedTime), ExpStatusFutureBlock)
				return
			}

			hash, err = exp.blockData.GetBlockHash(height)
			if err != nil {
				f := "GetBlockHash"
				if !exp.liteMode {
					hash, err = exp.explorerSource.BlockHash(height)
					f = "BlockHash"
				}
				if err != nil {
					log.Errorf("%s(%d) failed: %v", f, height, err)
					exp.StatusPage(w, defaultErrorCode, "could not find that block",
						string(height), ExpStatusNotFound)
					return
				}
			}
		}

		ctx := context.WithValue(r.Context(), ctxBlockHash, hash)
		ctx = context.WithValue(ctx, ctxBlockIndex, height)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SyncStatusPageActivation serves only the syncing status page until its
// deactivated when DisplaySyncStatusPage is set to false. This page is served
// for all the possible routes supported until the background syncing is done.
func (exp *explorerUI) SyncStatusPageActivation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exp.DisplaySyncStatusPage() {
			exp.StatusPage(w, "Database Update Running. Please Wait...",
				"Blockchain sync is running. Please wait ...", "", ExpStatusSyncing)
			return
		}
		// Otherwise, proceed to the next http handler.
		next.ServeHTTP(w, r)
	})
}

// SyncStatusApiResponse returns a json response back instead of a web page when
// display sync status is active for the api endpoints supported.
func (exp *explorerUI) SyncStatusApiResponse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exp.DisplaySyncStatusPage() {
			exp.HandleApiRequestsOnSync(w, r)
			return
		}
		// Otherwise, proceed to the next http handler.
		next.ServeHTTP(w, r)
	})
}

func getBlockHashCtx(r *http.Request) string {
	hash, ok := r.Context().Value(ctxBlockHash).(string)
	if !ok {
		log.Trace("Block Hash not set")
		return ""
	}
	return hash
}

func getBlockHeightCtx(r *http.Request) int64 {
	idxI, ok := r.Context().Value(ctxBlockIndex).(int)
	idx := int64(idxI)
	if !ok {
		log.Trace("Block Height not set")
		return -1
	}
	return idx
}

func getAgendaIDCtx(r *http.Request) string {
	hash, ok := r.Context().Value(ctxAgendaId).(string)
	if !ok {
		log.Trace("Agendaid not set")
		return ""
	}
	return hash
}

// TransactionHashCtx embeds "txid" into the request context
func TransactionHashCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		txid := chi.URLParam(r, "txid")
		ctx := context.WithValue(r.Context(), ctxTxHash, txid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TransactionIoIndexCtx embeds "inout" and "inoutid" into the request context
func TransactionIoIndexCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inout := chi.URLParam(r, "inout")
		inoutid := chi.URLParam(r, "inoutid")
		ctx := context.WithValue(r.Context(), ctxTxInOut, inout)
		ctx = context.WithValue(ctx, ctxTxInOutId, inoutid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AddressPathCtx embeds "address" into the request context
func AddressPathCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address := chi.URLParam(r, "address")
		ctx := context.WithValue(r.Context(), ctxAddress, address)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AgendaPathCtx embeds "agendaid" into the request context
func AgendaPathCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agendaid := chi.URLParam(r, "agendaid")
		ctx := context.WithValue(r.Context(), ctxAgendaId, agendaid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
