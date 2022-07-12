package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/julienschmidt/httprouter"
	st "github.com/lyulka/trivial-ledger/structs"
	cv3 "go.etcd.io/etcd/clientv3"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// const PREFIX = "tledger/"

var newPrefix = "testledger/"

// var TLEDGER_SERVER_ENDPOINT = "192.168.0.4:9090"
var TLEDGER_SERVER_ENDPOINT = "localhost:9090"

var DEFAULT_ENDPOINTS []string = []string{"223.194.92.152:12379", "223.194.92.152:22379", "223.194.92.152:32379", "223.194.92.152:42379", "223.194.92.152:52379"}

// var DEFAULT_ENDPOINTS []string = []string{"127.0.0.1:2379", "127.0.0.1:22379", "127.0.0.1:32379"}
var DEFAULT_DIAL_TIMEOUT time.Duration = 1 * time.Second

type Server struct {
	Router     *httprouter.Router
	etcdClient *cv3.Client

	blockCache BlockCache

	// TODO: Refactor into type BlockchainEngine
	latestTxIndex int64
}

func updatePremifx(x string) {
	newPrefix = x
}

func enableCors(w *http.ResponseWriter) {
	fmt.Printf("enable Cors %v \n", "start")
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
}

func New() (*Server, error) {

	client, err := cv3.New(cv3.Config{
		Endpoints:   DEFAULT_ENDPOINTS,
		DialTimeout: DEFAULT_DIAL_TIMEOUT,
	})
	if err != nil {
		return nil, err
	}

	router := httprouter.New()
	server := Server{
		Router:        router,
		etcdClient:    client,
		blockCache:    make(BlockCache),
		latestTxIndex: -1,
	}

	server.blockCache = make(BlockCache)

	err = server.BringBlockCacheAndLatestTxNumUpToDate()
	if err != nil {
		return nil, err
	}
	router.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Access-Control-Request-Method") != "" {
			// Set CORS headers
			header := w.Header()
			header.Set("Access-Control-Allow-Methods", "*")
			header.Set("Access-Control-Allow-Origin", "*")
			header.Set("Access-Control-Allow-Headers", "*")
			header.Add("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		// Adjust status code to 204
		w.WriteHeader(http.StatusNoContent)
	})
	router.GET("/login/", server.HelloWorldGet)
	router.GET("/login/:prefix/getTransaction/", server.getTransactionGet)
	router.GET("/login/:prefix/getBlock/", server.getBlockGet)
	router.POST("/login/:prefix/proposeTransaction/", server.proposeTransactionPost)
	log.Fatal(http.ListenAndServe(":9090", router))
	return &server, nil
}

func (s *Server) BringBlockCacheAndLatestTxNumUpToDate() error {
	fmt.Printf("%v \n", newPrefix)
	// Determine the latest transaction index (note that this is distinct from within-block txNum)
	ctx, cancel := context.WithTimeout(context.Background(), DEFAULT_DIAL_TIMEOUT)
	resp, err := s.etcdClient.Get(
		// ctx, PREFIX,
		ctx, newPrefix,
		cv3.WithPrefix(),
		cv3.WithSort(cv3.SortByKey, cv3.SortDescend),
		cv3.WithLimit(1))
	cancel()
	if err != nil {
		return err
	}

	if len(resp.Kvs) != 0 {
		// Store latestTxIndex
		keyPathSegments := strings.Split(string(resp.Kvs[0].Key), "/")
		fmt.Printf("oldPath %v", keyPathSegments)
		s.latestTxIndex, err = binaryToInt(keyPathSegments[len(keyPathSegments)-1])
		fmt.Printf("old lastindex %v \n", s.latestTxIndex)
		if err != nil {
			return err
		}
	}

	if len(resp.Kvs) == 0 {
		fmt.Printf("new lastindex %v \n", s.latestTxIndex)
		s.latestTxIndex = -1

	}
	s.blockCache = make(BlockCache)

	// Determine the block number up to which transactions have been committed (floor div.)
	latestBlockNumCommitted := int(s.latestTxIndex+1) / st.DEFAULT_BLOCK_SIZE

	// if len(resp.Kvs) != 0 {
	// 	err = s.blockCache.PopulateWithBlocks(*s.etcdClient,
	// 		s.blockCache.latestBlockNumInCache()+1, latestBlockNumCommitted)
	// }

	// if len(resp.Kvs) == 0 {
	// 	err = s.blockCache.PopulateWithBlocks(*s.etcdClient,
	// 		0, latestBlockNumCommitted)
	// }

	// fmt.Printf("new latestBlockNumInCache %v \n", s.blockCache.latestBlockNumInCache())

	err = s.blockCache.PopulateWithBlocks(*s.etcdClient,
		s.blockCache.latestBlockNumInCache()+1, latestBlockNumCommitted)

	// fmt.Printf("new latestBlockNumInCache %v \n", s.blockCache.latestBlockNumInCache())

	if err != nil {
		return err
	}

	return nil
}

func (s *Server) Teardown() {
	s.etcdClient.Close()

	outDir := fmt.Sprintf("/tmp/tledger/" + TLEDGER_SERVER_ENDPOINT)
	timeStamp := strconv.Itoa(int(time.Now().UnixNano()))
	fmt.Println("TLedger: SIGINT received. Printing BlockCache into " + outDir + "/" + timeStamp)

	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		err = os.MkdirAll(outDir, 0755)
		if err != nil {
			fmt.Println("TLedger: Failed to create directory in /tmp")
			fmt.Println(err)
		}
	}

	outFile, err := os.Create(outDir + "/" + timeStamp + ".txt")
	if err != nil {
		fmt.Println("TLedger: Failed to log results.")
		fmt.Println(err)
	}
	outFile.Write([]byte(fmt.Sprintf("%v", s.blockCache)))
	outFile.Close()

	fmt.Println("TLedger: Tearing down server. Goodbye!")
}

func (s *Server) proposeTransaction(propTx st.ProposedTransaction) (blockNum int, txNumber int, err error) {

	accepted := false
	for !accepted {
		s.BringBlockCacheAndLatestTxNumUpToDate()

		// If the etcd TXN coming up succeeds, the new transaction will have the following
		// blockNum and txNumber
		fmt.Printf("propose Transaction Block cache %v\n", s.blockCache.latestBlockNumInCache())
		blockNum = s.blockCache.latestBlockNumInCache() + 1
		txNumber = int(s.latestTxIndex+1) % st.DEFAULT_BLOCK_SIZE

		// Generate KV pair
		// key := fmt.Sprintf("%s%s", PREFIX, intToBinary(s.latestTxIndex+1))
		key := fmt.Sprintf("%s%s", newPrefix, intToBinary(s.latestTxIndex+1))
		valueBytes, err := json.Marshal(st.Transaction{
			ProposedTransaction: propTx,
			BlockNum:            blockNum,
			TxNumber:            txNumber,
			Timestamp:           time.Now().String(),
		})
		value := string(valueBytes)

		if err != nil {
			return -1, -1, err
		}

		ctx, cancel := context.WithTimeout(context.Background(), DEFAULT_DIAL_TIMEOUT)
		// CreateRevision == 0 if key does not exist
		txnResp, err := s.etcdClient.Txn(ctx).If(
			cv3.Compare(cv3.CreateRevision(key), "=", 0),
		).Then(
			cv3.OpPut(key, value),
		).Commit()

		cancel()
		if err != nil {
			return -1, -1, err
		}

		if txnResp.Succeeded {
			accepted = true
		}
	}

	return blockNum, txNumber, nil

}

// Queries block cache
// txNum must be smaller than configured block size
func (s *Server) getTransaction(blockNum int, txNum int) *st.Transaction {

	block := s.getBlock(blockNum)
	if block == nil {
		return nil
	}

	return &block.Transactions[txNum]
}

// Queries block cache
func (s *Server) getBlock(blockNum int) *st.Block {

	// Check if block is in BlockCache
	if block, ok := s.blockCache[blockNum]; ok {
		return &block
	}

	s.BringBlockCacheAndLatestTxNumUpToDate()

	// Check again
	if block, ok := s.blockCache[blockNum]; ok {
		return &block
	}

	return nil
}

func (s *Server) proposeTransactionPost(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	// enableCors(&w)
	updatePremifx(ps.ByName("prefix") + "/")
	proposedTx := st.ProposedTransaction{}
	err := json.NewDecoder(r.Body).Decode(&proposedTx)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "Request body should be properly formatted JSON")
		return
	}

	blockNum, txNumber, err := s.proposeTransaction(proposedTx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "TODO: more granular errors")
		return
	}
	enableCors(&w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(ProposeTransactionResponse{
		BlockNum: blockNum,
		TxNumber: txNumber,
	})
	fmt.Printf("4 \n")
}

func (s *Server) getTransactionGet(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	updatePremifx(ps.ByName("prefix") + "/")
	reqBody := GetTransactionRequest{}
	err := json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "Request body should be properly formatted JSON")
		return
	}

	transaction := s.getTransaction(reqBody.BlockNum, reqBody.TxNumber)
	if transaction == nil {
		w.WriteHeader(http.StatusNoContent)
		io.WriteString(w, "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(GetTransactionResponse(*transaction))
}

func (s *Server) getBlockGet(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	updatePremifx(ps.ByName("prefix") + "/")
	reqBody := GetBlockRequest{}
	err := json.NewDecoder(r.Body).Decode(&reqBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "Request body should be properly formatted JSON")
		return
	}

	block := s.getBlock(reqBody.BlockNum)
	if block == nil {
		w.WriteHeader(http.StatusNoContent)
		io.WriteString(w, "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(GetBlockResponse(*block))
}

func (s *Server) HelloWorldGet(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	enableCors(&w)
	fmt.Println("Hello world: in")
	fmt.Fprintln(w, "Hello world!")
}

func intToBinary(n int64) string {
	return fmt.Sprintf("%064s", strconv.FormatInt(int64(n), 2))
}

func binaryToInt(binary string) (int64, error) {
	i, err := strconv.ParseInt(binary, 2, 64)
	if err != nil {
		return -1, err
	}

	return i, nil
}
