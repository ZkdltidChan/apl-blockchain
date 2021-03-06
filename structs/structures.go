package structs

import (
	"crypto/sha256"
	"fmt"
)

const DEFAULT_BLOCK_SIZE int = 3

type ProposedTransaction struct {
	Content string   `json:"content"`
	Name    []string `json:"name"`
	Percent []int    `json:"percent"`
}

type Transaction struct {
	ProposedTransaction

	Timestamp string `json:"timestamp"`

	// BlockNum+TxNumber uniquely identifies a transaction.
	BlockNum int `json:"blockNum"`
	TxNumber int `json:"txNumber"`
}

type Block struct {

	// Uniquely identifies a block in the blockchain.
	BlockNum int `json:"blockNum"`

	// Equal to the timestamp of the *last* transaction in TxList
	Timestamp string `json:"timestamp"`

	// SHA-256 hash.
	PreviousHash string `json:"previousHash"`

	// SHA-256 hash of the entire block.
	Hash string `json:"hash"`

	Transactions [DEFAULT_BLOCK_SIZE]Transaction
}

// transactions need to be ordered in the same way they will be packed into the
// block
func NewBlock(blockNum int, prevHash string, transactions [DEFAULT_BLOCK_SIZE]Transaction) (*Block, error) {

	b := Block{
		BlockNum:     blockNum,
		Timestamp:    transactions[len(transactions)-1].Timestamp,
		PreviousHash: prevHash,
		Hash:         "",
		Transactions: transactions,
	}

	hash := AsSha256(b)
	b.Hash = hash

	return &b, nil
}

func AsSha256(o interface{}) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%v", o)))

	return fmt.Sprintf("%x", h.Sum(nil))
}
