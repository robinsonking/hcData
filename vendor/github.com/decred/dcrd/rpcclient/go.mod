module github.com/decred/dcrd/rpcclient

require (
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd
	github.com/davecgh/go-spew v1.1.0
	github.com/decred/dcrd/chaincfg/chainhash v1.0.1
	github.com/decred/dcrd/dcrjson v1.0.0
	github.com/decred/dcrd/dcrutil v1.1.1
	github.com/decred/dcrd/gcs v1.0.1
	github.com/decred/dcrd/wire v1.2.0
	github.com/decred/slog v1.0.0
	github.com/gorilla/websocket v1.2.0
)

replace (
	github.com/decred/dcrd/blockchain => ../blockchain
	github.com/decred/dcrd/blockchain/stake => ../blockchain/stake
	github.com/decred/dcrd/chaincfg => ../chaincfg
	github.com/decred/dcrd/chaincfg/chainhash => ../chaincfg/chainhash
	github.com/decred/dcrd/database => ../database
	github.com/decred/dcrd/dcrec => ../dcrec
	github.com/decred/dcrd/dcrec/edwards => ../dcrec/edwards
	github.com/decred/dcrd/dcrec/secp256k1 => ../dcrec/secp256k1
	github.com/decred/dcrd/dcrjson => ../dcrjson
	github.com/decred/dcrd/dcrutil => ../dcrutil
	github.com/decred/dcrd/gcs => ../gcs
	github.com/decred/dcrd/txscript => ../txscript
	github.com/decred/dcrd/wire => ../wire
)
